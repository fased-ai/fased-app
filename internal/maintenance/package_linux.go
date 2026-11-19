//go:build linux

package maintenance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// ReadPackageAuthority never falls back to the older owner-wide authority.
func ReadPackageAuthority(owner int, location, instance string) (Authority, error) {
	var a Authority
	root, err := update.PackageMaintenanceRoot(owner, location, instance)
	if err != nil {
		return a, err
	}
	f, err := openProtected(root+"/authority.json", 8192, 0644)
	if err != nil {
		return a, err
	}
	b, readErr := io.ReadAll(io.LimitReader(f, 8193))
	if err = errors.Join(readErr, f.Close()); err != nil || len(b) > 8192 || update.StrictJSON(b, &a) != nil || validatePackageAuthority(a, owner, location, instance) != nil {
		return Authority{}, errors.New("invalid protected package authority")
	}
	return a, nil
}

func validatePackageAuthority(a Authority, owner int, location, instance string) error {
	if _, err := update.PackageMaintenanceRoot(owner, location, instance); err != nil || a.Version != 1 || a.Owner != owner || a.Profile != location || a.Group <= 0 || !filepath.IsAbs(a.Home) || filepath.Clean(a.Home) != a.Home || a.Home == "/" || a.Home == "/root" {
		return errors.New("invalid protected package authority")
	}
	hash, err := hex.DecodeString(a.ImageSHA256)
	if err != nil || len(hash) != 32 || hex.EncodeToString(hash) != a.ImageSHA256 {
		return errors.New("invalid protected maintenance image")
	}
	return nil
}

func packageSudoPolicy(a Authority, location, instance string) ([]byte, error) {
	if err := validatePackageAuthority(a, a.Owner, location, instance); err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("# Fased instance maintenance; no general OS-tool authority.\n#%d ALL=(root) NOPASSWD: NOSETENV: %s maintenance-package-request --owner %d --location %s --instance %s\n", a.Owner, ImagePath(a.ImageSHA256), a.Owner, location, instance)), nil
}

func packagePolicyPath(owner int, location, instance string) (string, error) {
	if _, err := update.PackageMaintenanceRoot(owner, location, instance); err != nil {
		return "", err
	}
	return fmt.Sprintf("/etc/sudoers.d/fased-package-%d-%s-%s", owner, location, instance), nil
}

// RegisterPackage provisions authority for one instance without adopting the
// owner's older installation, helper or journal. A repeated registration must
// match the original protected image and scope exactly.
func RegisterPackage(owner int, location, instance string) (Authority, error) {
	var a Authority
	root, err := update.PackageMaintenanceRoot(owner, location, instance)
	if err != nil || os.Getuid() != 0 || os.Geteuid() != 0 {
		return a, errors.New("fixed administrator package registration required")
	}
	if caller := os.Getenv("SUDO_UID"); caller != "" && caller != "0" && caller != strconv.Itoa(owner) {
		return a, errors.New("maintenance caller does not own this instance")
	}
	image, err := os.Executable()
	if err != nil {
		return a, err
	}
	hash := filepath.Base(filepath.Dir(image))
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != hash || image != ImagePath(hash) {
		return a, errors.New("register only the protected verified installer image")
	}
	u, err := user.LookupId(strconv.Itoa(owner))
	if err != nil {
		return a, err
	}
	group, err := strconv.Atoi(u.Gid)
	if err != nil {
		return a, errors.New("invalid ordinary installation owner")
	}
	a = Authority{Version: 1, Owner: owner, Group: group, Home: u.HomeDir, Profile: location, ImageSHA256: hash}
	if err = validatePackageAuthority(a, owner, location, instance); err != nil {
		return Authority{}, err
	}
	if err = VerifyImage(a); err != nil {
		return Authority{}, err
	}
	policy, err := packageSudoPolicy(a, location, instance)
	if err != nil {
		return Authority{}, err
	}
	cmd := exec.Command("/usr/sbin/visudo", "-c", "-f", "-")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	cmd.Stdin = bytes.NewReader(policy)
	if err = cmd.Run(); err != nil {
		return Authority{}, errors.New("package maintenance policy did not validate")
	}
	policyPath, _ := packagePolicyPath(owner, location, instance)
	if f, e := openProtected(policyPath, int64(len(policy)), 0440); e == nil {
		actual, readErr := io.ReadAll(io.LimitReader(f, int64(len(policy))+1))
		if e = errors.Join(readErr, f.Close()); e != nil || !bytes.Equal(actual, policy) {
			return Authority{}, errors.New("existing package policy differs; preserve it")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return Authority{}, e
	}
	for _, dir := range []string{StateRoot, StateRoot + "/instances", StateRoot + "/instances/" + strconv.Itoa(owner), filepath.Dir(root), root} {
		if err = ensureDirectory(dir); err != nil {
			return Authority{}, err
		}
	}
	lock, err := lockPackage(root)
	if err != nil {
		return Authority{}, err
	}
	defer lock.Close()
	previous, err := ReadPackageAuthority(owner, location, instance)
	if err == nil {
		if previous != a || VerifyImage(previous) != nil {
			return Authority{}, errors.New("existing package authority differs; preserve it")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Authority{}, err
	} else {
		// Authority precedes all installed state. Never infer freshness from a
		// missing authority when an instance or another operation exists.
		instanceRoot, _ := update.PackageInstanceRoot(owner, location, instance)
		if _, err = os.Lstat(instanceRoot); !errors.Is(err, os.ErrNotExist) {
			return Authority{}, errors.New("existing package instance must be preserved")
		}
		entries, e := os.ReadDir(root)
		if e != nil {
			return Authority{}, e
		}
		for _, entry := range entries {
			if entry.Name() != "operation.lock" && entry.Name() != ".authority.json.pending" {
				return Authority{}, errors.New("unclassified package maintenance state; preserve it")
			}
		}
		body, e := json.Marshal(a)
		if e != nil {
			return Authority{}, e
		}
		if err = publishResumableFixed(root+"/authority.json", append(body, '\n'), 0644); err != nil {
			return Authority{}, err
		}
	}
	if err = configurePackageSELinux(a, location, instance); err != nil {
		return Authority{}, err
	}
	if err = publishResumableFixed(policyPath, policy, 0440); err != nil {
		return Authority{}, err
	}
	return a, nil
}

// ValidatePackageProcess binds the running protected image and caller to the
// authority of exactly one instance. It grants no operation by itself.
func ValidatePackageProcess(owner int, location, instance string) (Authority, error) {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return Authority{}, errors.New("protected package maintenance entry required")
	}
	a, err := ReadPackageAuthority(owner, location, instance)
	if err != nil {
		return Authority{}, err
	}
	if err = ValidateOwnerIdentity(a); err != nil {
		return Authority{}, err
	}
	if _, e := update.ReadPackageHelperTransition(owner, location, instance); e == nil {
		root, _ := update.PackageInstanceRoot(owner, location, instance)
		if _, e = os.Lstat(filepath.Join(root, "update-start-blocked.json")); !errors.Is(e, os.ErrNotExist) {
			return Authority{}, errors.New("resume the original installer helper transition")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return Authority{}, e
	}
	path, err := os.Executable()
	if err != nil || path != ImagePath(a.ImageSHA256) {
		return Authority{}, errors.New("use this instance's protected maintenance helper")
	}
	if caller := os.Getenv("SUDO_UID"); caller != "" && caller != "0" && caller != strconv.Itoa(owner) {
		return Authority{}, errors.New("maintenance caller does not own this instance")
	}
	return a, VerifyImage(a)
}

// ValidateLocalAdoptionBridge is for a one-time administrator command. The
// administrator installs this independently attested bridge in the protected
// image store. The instance continues to pin the original release installer.
// The bridge command is deliberately absent from the instance sudo policy.
func ValidateLocalAdoptionBridge(owner int, instance string) (Authority, string, error) {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return Authority{}, "", errors.New("administrator Local adoption required")
	}
	if caller := os.Getenv("SUDO_UID"); caller != "" && caller != "0" && caller != strconv.Itoa(owner) {
		return Authority{}, "", errors.New("Local adoption caller does not own this instance")
	}
	a, err := ReadPackageAuthority(owner, "local", instance)
	if err != nil || a.ImageSHA256 != LegacyLocalRetainedInstaller || ValidateOwnerIdentity(a) != nil || VerifyImage(a) != nil {
		return Authority{}, "", errors.New("the retained Local package authority is required")
	}
	path, err := os.Executable()
	hash := filepath.Base(filepath.Dir(path))
	if err != nil || !canonicalSHA256(hash) || hash == a.ImageSHA256 || path != ImagePath(hash) {
		return Authority{}, "", errors.New("use the separately attested protected Local adoption bridge")
	}
	bridge := a
	bridge.ImageSHA256 = hash
	if err := VerifyImage(bridge); err != nil {
		return Authority{}, "", err
	}
	return a, path, nil
}

// PackageLock serializes protected operations for one instance. Callers must
// validate their own protected entry before acquiring it and after waiting.
func PackageLock(owner int, location, instance string) (*os.File, error) {
	root, err := update.PackageMaintenanceRoot(owner, location, instance)
	if err != nil || os.Getuid() != 0 || os.Geteuid() != 0 {
		return nil, errors.New("protected package lock required")
	}
	return lockPackage(root)
}

// LocalAdoptionApplyLock remains held while boot recovery takes PackageLock.
// It serializes the one-time bridge commands across the service-start boundary.
func LocalAdoptionApplyLock(owner int, instance string) (*os.File, error) {
	if _, _, err := ValidateLocalAdoptionBridge(owner, instance); err != nil {
		return nil, err
	}
	root, err := update.PackageMaintenanceRoot(owner, "local", instance)
	if err != nil {
		return nil, err
	}
	return lockPackageFile(root+"/legacy-local-apply.lock", 0, 0)
}

// WaitPackageLock is for boot recovery, which may overlap a short protected
// owner request. It uses the same instance lock and remains context-bounded.
func WaitPackageLock(ctx context.Context, owner int, location, instance string) (*os.File, error) {
	root, err := update.PackageMaintenanceRoot(owner, location, instance)
	if err != nil || os.Getuid() != 0 || os.Geteuid() != 0 {
		return nil, errors.New("protected package lock required")
	}
	return waitPackageFile(ctx, root+"/operation.lock", 0, 0)
}

func lockPackage(root string) (*os.File, error) {
	return lockPackageFile(root+"/operation.lock", 0, 0)
}

// PackageSystemLock serializes the package-manager files shared by instances.
// Its root-owned directory already holds the verified maintenance images.
func PackageSystemLock(ctx context.Context) (*os.File, error) {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return nil, errors.New("protected system package lock required")
	}
	root := StateRoot + "/images"
	if err := ensureDirectory(root); err != nil {
		return nil, err
	}
	return waitPackageFile(ctx, root+"/system-package.lock", 0, 0)
}

func waitPackageFile(ctx context.Context, path string, owner, group int) (*os.File, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := lockPackageFile(path, owner, group)
		if err == nil || !errors.Is(err, syscall.EWOULDBLOCK) {
			return f, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func lockPackageFile(path string, owner, group int) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "package-maintenance-operation")
	i, err := f.Stat()
	if err == nil {
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || int(st.Uid) != owner || int(st.Gid) != group || st.Nlink != 1 || !i.Mode().IsRegular() || i.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || i.Mode().Perm()&^0600 != 0 || i.Size() != 0 {
			err = errors.New("foreign package lock; preserve it")
		}
	}
	if err == nil {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err == nil {
		err = f.Chmod(0600)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("package maintenance already running or unsafe lock: %w", err)
	}
	return f, nil
}

// PackageCandidateStreamCommand gives the owner only the fixed scoped entry.
// The privileged receiver independently verifies every streamed byte.
func PackageCandidateStreamCommand(ctx context.Context, location, instance string, request CandidateRequest, diagnostics io.Writer) (*exec.Cmd, []byte, error) {
	cmd, err := packageRequestCommand(ctx, location, instance, diagnostics)
	if err != nil {
		return nil, nil, err
	}
	b, err := json.Marshal(PackageRequest{Version: 1, Action: "candidate-stage", Candidate: &request})
	if err != nil {
		return nil, nil, err
	}
	if _, err = DecodePackageRequest(b); err != nil {
		return nil, nil, err
	}
	frame := append([]byte(CandidateStreamPrefix), b...)
	frame = append(frame, '\n')
	return cmd, frame, nil
}

// PackageOwnerStreamCommand reaches the same exact installed helper. The
// helper and the app-UID client independently restrict the HTTP operation.
func PackageOwnerStreamCommand(ctx context.Context, location, instance string, diagnostics io.Writer) (*exec.Cmd, []byte, error) {
	if location != "local" {
		return nil, nil, errors.New("Local package owner required")
	}
	cmd, err := packageRequestCommand(ctx, location, instance, diagnostics)
	if err != nil {
		return nil, nil, err
	}
	return cmd, []byte(PackageOwnerStreamPrefix + `{"version":1,"action":"owner-channel"}` + "\n"), nil
}

// PackageCall uses only this instance's fixed installed helper and a bounded
// semantic request. It accepts neither a path nor an executable from callers.
func PackageCall(ctx context.Context, location, instance string, request PackageRequest, diagnostics io.Writer) error {
	if request.Action != "provision" && request.Action != "wallet-initialize" {
		return errors.New("unsupported package operation")
	}
	b, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if _, err = DecodePackageRequest(b); err != nil {
		return err
	}
	cmd, err := packageRequestCommand(ctx, location, instance, diagnostics)
	if err != nil {
		return err
	}
	cmd.Stdin = bytes.NewReader(b)
	var output boundedResponse
	cmd.Stdout = &output
	if err = cmd.Run(); err != nil {
		return err
	}
	var response Response
	if update.StrictJSON(output.data, &response) != nil || response.Version != 1 || response.Action != request.Action || !response.Complete || len(response.Payload) != 0 {
		return errors.New("package operation result is unconfirmed")
	}
	return nil
}

func packageRequestCommand(ctx context.Context, location, instance string, diagnostics io.Writer) (*exec.Cmd, error) {
	owner := os.Getuid()
	if owner <= 0 || os.Geteuid() != owner {
		return nil, errors.New("ordinary package owner required")
	}
	a, err := ReadPackageAuthority(owner, location, instance)
	if err != nil {
		return nil, err
	}
	config, err := os.UserConfigDir()
	if err != nil || config != filepath.Join(a.Home, ".config") || ValidateOwnerIdentity(a) != nil || VerifyImage(a) != nil {
		return nil, errors.New("protected package owner and helper required")
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "--", ImagePath(a.ImageSHA256), "maintenance-package-request", "--owner", strconv.Itoa(owner), "--location", location, "--instance", instance)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=" + a.Home, "LANG=C"}
	cmd.Stderr = diagnostics
	return cmd, nil
}

// ReservePackageCandidate publishes only the exact public candidate identity
// under this instance. A crash can resume matching partial reservation bytes.
func ReservePackageCandidate(owner int, location, instance string, request CandidateRequest) error {
	if _, err := ValidatePackageProcess(owner, location, instance); err != nil {
		return err
	}
	b, err := json.Marshal(PackageRequest{Version: 1, Action: "candidate-stage", Candidate: &request})
	if err != nil {
		return err
	}
	if _, err = DecodePackageRequest(b); err != nil {
		return err
	}
	root, err := update.PackageMaintenanceRoot(owner, location, instance)
	if err != nil {
		return err
	}
	b, err = json.Marshal(request)
	if err != nil {
		return err
	}
	return publishResumableFixed(filepath.Join(root, "candidate", request.PacketSHA256+".reservation.json"), append(b, '\n'), 0644)
}

// EnsurePackageAcquisition prepares only the instance's fixed private verifier
// directory. Symlinks and foreign ownership are never adopted or repaired.
func EnsurePackageAcquisition(owner int, location, instance string) error {
	if _, err := ValidatePackageProcess(owner, location, instance); err != nil {
		return err
	}
	root, err := update.PackageMaintenanceRoot(owner, location, instance)
	if err != nil {
		return err
	}
	for _, dir := range []string{filepath.Join(root, "fased-app"), filepath.Join(root, "fased-app", "install")} {
		if err = os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		i, e := os.Lstat(dir)
		if e != nil || !packagePrivateDirectory(i, 0, 0) {
			return errors.New("foreign package acquisition directory; preserve it")
		}
		if i.Mode().Perm() != 0700 {
			if err = os.Chmod(dir, 0700); err != nil {
				return err
			}
		}
		if err = syncDirectory(dir); err != nil {
			return err
		}
		if err = syncDirectory(filepath.Dir(dir)); err != nil {
			return err
		}
	}
	return nil
}

func packagePrivateDirectory(i os.FileInfo, uid, gid uint32) bool {
	if i == nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || i.Mode().Perm()&^0700 != 0 {
		return false
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uid && st.Gid == gid
}

// publishResumableFixed uses one deterministic, root-owned temporary name.
// Only matching partial bytes can be resumed; foreign state is never removed.
func publishResumableFixed(path string, data []byte, mode os.FileMode) error {
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".pending")
	if old, err := openProtected(path, int64(len(data)), mode); err == nil {
		b, readErr := io.ReadAll(io.LimitReader(old, int64(len(data))+1))
		if err = errors.Join(readErr, old.Close()); err != nil || !bytes.Equal(b, data) {
			return errors.New("existing protected input differs; preserve it")
		}
		if _, err = os.Lstat(tmp); !errors.Is(err, os.ErrNotExist) {
			return errors.New("unclassified pending protected input; preserve it")
		}
		return syncDirectory(filepath.Dir(path))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fd, err := syscall.Open(tmp, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), tmp)
	err = resumePendingInput(f, data, mode, 0, 0)
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err = unix.Renameat2(unix.AT_FDCWD, tmp, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE); err != nil {
		return errors.New("protected publication conflicted; preserve it")
	}
	return syncDirectory(filepath.Dir(path))
}

// The file is already opened without following links. uid/gid are fixed to
// root by the production caller; tests exercise the same logic unprivileged.
func resumePendingInput(f *os.File, data []byte, mode os.FileMode, uid, gid uint32) error {
	info, err := f.Stat()
	if err == nil {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uid || st.Gid != gid || st.Nlink != 1 || !info.Mode().IsRegular() || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || (info.Mode().Perm()&^0600 != 0 && info.Mode().Perm() != mode) || info.Size() < 0 || info.Size() > int64(len(data)) {
			err = errors.New("foreign pending protected input; preserve it")
		}
	}
	if err == nil {
		var existing []byte
		existing, err = io.ReadAll(io.LimitReader(f, int64(len(data))+1))
		if err == nil && (len(existing) > len(data) || !bytes.Equal(existing, data[:len(existing)])) {
			err = errors.New("pending protected input differs; preserve it")
		}
		if err == nil {
			_, err = f.Write(data[len(existing):])
		}
	}
	if err == nil {
		err = errors.Join(f.Chmod(mode), f.Sync())
	}
	return err
}

// PackageTransitionProcess is an administrator-only bootstrap of a verified
// installer. The instance sudo rule grants only maintenance-package-request.
func PackageTransitionProcess(owner int, location, instance string) (Authority, error) {
	var a Authority
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return a, errors.New("administrator package helper transition required")
	}
	if caller := os.Getenv("SUDO_UID"); caller != "" && caller != "0" && caller != strconv.Itoa(owner) {
		return a, errors.New("maintenance caller does not own this instance")
	}
	a, err := ReadPackageAuthority(owner, location, instance)
	if err != nil {
		return a, err
	}
	if err = ValidateOwnerIdentity(a); err != nil {
		return Authority{}, err
	}
	image, err := os.Executable()
	if err != nil {
		return Authority{}, err
	}
	a.ImageSHA256 = filepath.Base(filepath.Dir(image))
	if err = validatePackageAuthority(a, owner, location, instance); err != nil || image != ImagePath(a.ImageSHA256) {
		return Authority{}, errors.New("run only the protected attested successor installer")
	}
	return a, VerifyImage(a)
}

func VerifyPackageTransitionPolicy(previous, next Authority, instance string, resuming bool) error {
	if previous.Owner != next.Owner || previous.Profile != next.Profile {
		return errors.New("the predecessor helper belongs to another package profile")
	}
	path, err := packagePolicyPath(next.Owner, next.Profile, instance)
	if err != nil {
		return err
	}
	oldPolicy, err := packageSudoPolicy(previous, next.Profile, instance)
	if err != nil {
		return err
	}
	newPolicy, err := packageSudoPolicy(next, next.Profile, instance)
	if err != nil {
		return err
	}
	digest, _, err := ProtectedDigest(path, 0440, 8192)
	if err != nil || digest != packageContentHash(oldPolicy) && (!resuming || digest != packageContentHash(newPolicy)) {
		return errors.New("existing package sudo policy differs; preserve it")
	}
	return nil
}

func ApplyPackageTransitionAuthority(previous, next Authority, instance string) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("administrator package helper transition required")
	}
	if err := VerifyPackageTransitionPolicy(previous, next, instance, true); err != nil {
		return err
	}
	if err := configurePackageSELinux(next, next.Profile, instance); err != nil {
		return err
	}
	oldPolicy, _ := packageSudoPolicy(previous, next.Profile, instance)
	newPolicy, _ := packageSudoPolicy(next, next.Profile, instance)
	cmd := exec.Command("/usr/sbin/visudo", "-c", "-f", "-")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	cmd.Stdin = bytes.NewReader(newPolicy)
	if err := cmd.Run(); err != nil {
		return errors.New("successor maintenance policy did not validate")
	}
	root, _ := update.PackageMaintenanceRoot(next.Owner, next.Profile, instance)
	oldAuthority, err := json.Marshal(previous)
	if err != nil {
		return err
	}
	newAuthority, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err = ReplaceProtected(root+"/authority.json", append(newAuthority, '\n'), 0644, packageContentHash(append(oldAuthority, '\n'))); err != nil {
		return err
	}
	path, _ := packagePolicyPath(next.Owner, next.Profile, instance)
	return ReplaceProtected(path, newPolicy, 0440, packageContentHash(oldPolicy))
}

func packageContentHash(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}
