//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// Setup approval is supplied once by the OS administrator in the existing
// root-approved input directory. It names two independent reviewed images;
// the app cannot choose either image through an owner-writable request.
type reusableSetupApproval struct {
	Version   int                          `json:"version"`
	Authority reusableMaintenanceAuthority `json:"authority"`
	IssuedAt  int64                        `json:"issuedAt"`
	NotAfter  int64                        `json:"notAfter"`
}

// ReusableMaintenanceSetupAvailable only detects the root-owned setup record;
// it never creates a grant or changes the installed predecessor.
func ReusableMaintenanceSetupAvailable(m Manifest) (bool, error) {
	_, err := loadReusableSetupApproval(m)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func loadReusableSetupApproval(m Manifest) (*reusableSetupApproval, error) {
	if m.validate() != nil {
		return nil, errors.New("valid managed predecessor required")
	}
	path := fmt.Sprintf("/var/lib/fased-app-approved-%d/reusable-%s.json", m.Owner, m.ID)
	f, err := readRoot(path)
	if err != nil {
		return nil, err
	}
	var approval reusableSetupApproval
	if strict(f.Data, &approval) != nil || !known(f, mustJSON(approval), 0644) || approval.Version != 1 || approval.IssuedAt <= 0 || approval.NotAfter <= approval.IssuedAt || approval.NotAfter-approval.IssuedAt > 10*60 || approval.Authority.Owner != m.Owner || approval.Authority.InstallationID != m.ID {
		return nil, errors.New("root-approved maintenance setup changed; preserving it")
	}
	if _, _, err = reusableAtomicLauncher(m, approval.Authority); err != nil {
		return nil, err
	}
	return &approval, nil
}

func readReusableSetupApproval(m Manifest, targetCore, targetSigner string) (*reusableSetupApproval, error) {
	if m.validate() != nil || !validHex(targetCore, 32) || !validHex(targetSigner, 32) {
		return nil, errors.New("verified target pair required for maintenance setup")
	}
	approval, err := loadReusableSetupApproval(m)
	if err != nil || approval.Authority.RunnerSHA256 == targetCore || approval.Authority.GuardSHA256 == targetSigner {
		return nil, errors.New("exact root-approved independent maintenance setup required")
	}
	return approval, nil
}

func approvedReusableImage(owner int, digest string) (string, int64, error) {
	if owner <= 0 || !validHex(digest, 32) {
		return "", 0, errors.New("exact approved maintenance image required")
	}
	path := filepath.Join(fmt.Sprintf("/var/lib/fased-app-approved-%d", owner), digest)
	info, err := os.Lstat(path)
	if err != nil || info.Size() <= 0 || verifyApprovedExecutable(path, digest, info.Size()) != nil {
		return "", 0, errors.New("approved maintenance image is unavailable or changed")
	}
	return path, info.Size(), nil
}

// The immutable predecessor cannot execute new code. Its one-time entry runs
// the independently root-approved maintenance reader as the ordinary owner;
// compare the running inode itself, not a replaceable owner pathname.
func VerifyReusableSetupLauncher(m Manifest) error {
	if m.Owner <= 0 || os.Getuid() != m.Owner || os.Geteuid() != m.Owner {
		return errors.New("ordinary managed installation owner required")
	}
	approval, err := loadReusableSetupApproval(m)
	if err != nil {
		return err
	}
	_, size, err := approvedReusableImage(m.Owner, approval.Authority.RunnerSHA256)
	if err != nil {
		return err
	}
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		return err
	}
	defer f.Close()
	path, err := os.Executable()
	if err != nil {
		return err
	}
	if err = verifyReusableSetupLauncherLocation(f, path); err != nil {
		return err
	}
	if err = verifyReusableSetupLauncherFile(f, approval.Authority.RunnerSHA256, size); err != nil {
		return err
	}
	return verifyReusableSetupLauncherLocation(f, path)
}

// The approved content source is non-executable. The running successor is a
// separately installed root-owned executable with those same approved bytes.
// Bind its pathname to the running inode so an owner-controlled copy or a
// replaced pathname cannot become an owner-authorized maintenance entry.
func verifyReusableSetupLauncherLocation(f *os.File, path string) error {
	if f == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("protected running maintenance launcher required")
	}
	if err := trustedParents(filepath.Dir(path)); err != nil {
		return errors.New("protected running maintenance launcher required")
	}
	running, err := f.Stat()
	if err != nil || executableInfo(running) != nil {
		return errors.New("protected running maintenance launcher required")
	}
	current, err := os.Lstat(path)
	if err != nil || !sameExecutable(running, current) {
		return errors.New("running maintenance launcher pathname changed")
	}
	return trustedParents(filepath.Dir(path))
}

func verifyReusableSetupLauncherFile(f *os.File, digest string, size int64) error {
	if f == nil || !validHex(digest, 32) || size <= 0 || size > maxInstalledExecutableSize {
		return errors.New("approved maintenance launcher required")
	}
	before, err := f.Stat()
	if err != nil || executableInfo(before) != nil || before.Size() != size {
		return errors.New("approved maintenance launcher required")
	}
	if err = verifyExecutableContents(f, size, digest); err != nil {
		return errors.New("approved maintenance launcher changed")
	}
	after, err := f.Stat()
	if err != nil || !sameExecutable(before, after) {
		return errors.New("maintenance launcher changed during verification")
	}
	a, aok := before.Sys().(*syscall.Stat_t)
	b, bok := after.Sys().(*syscall.Stat_t)
	if !aok || !bok || a.Ctim != b.Ctim {
		return errors.New("maintenance launcher metadata changed")
	}
	return nil
}

type reusableCopyMarker struct {
	Version        int    `json:"version"`
	Owner          int    `json:"owner"`
	InstallationID string `json:"installationId"`
	Component      string `json:"component"`
	SHA256         string `json:"sha256"`
	Bytes          int64  `json:"bytes"`
}

type reusableSetupFile struct {
	path string
	data []byte
	unit bool
}

// Generate the files once for both the installed setup and its exact temporary
// OS policy. This keeps approved command bytes tied to the files actually used.
func reusableSetupFiles(m Manifest, a reusableMaintenanceAuthority, ownerGID int, ownerName string, runnerSize, guardSize int64) ([]reusableSetupFile, []byte, error) {
	launcher, guardSum, err := reusableAtomicLauncher(m, a)
	if err != nil {
		return nil, nil, err
	}
	provision, err := reusableAtomicProvision(m, a)
	if err != nil {
		return nil, nil, err
	}
	fence, err := reusablePreparationFenceUnit(m, a)
	if err != nil {
		return nil, nil, err
	}
	sentinel, err := signer.InstallationPreparationFenceBytes(m.Owner, m.ID)
	if err != nil {
		return nil, nil, err
	}
	setup, policySum, err := reusableAtomicSetup(m, a, ownerName)
	if err != nil {
		return nil, nil, err
	}
	policy, err := reusableAtomicPolicy(m, ownerName)
	if err != nil || runnerSize <= 0 || guardSize <= 0 || runnerSize > maxInstalledExecutableSize || guardSize > maxInstalledExecutableSize {
		return nil, nil, errors.New("bounded approved setup components required")
	}
	metadata := func(name string, data []byte) reusableSetupFile {
		return reusableSetupFile{path: reusableMaintenancePath(m, name), data: data}
	}
	unit := func(name string, data []byte) reusableSetupFile {
		return reusableSetupFile{path: filepath.Join("/etc/systemd/system", name), data: data, unit: true}
	}
	files := []reusableSetupFile{
		metadata("authority.json", mustJSON(a)), metadata("reusable-v1.json", reusableMaintenanceMarkerBytes(a)),
		metadata("guard.sha256", guardSum), metadata("policy.sha256", policySum),
		metadata("preparation-fence.json", sentinel), metadata("preparation-fence.sha256", []byte(sum(sentinel)+"  "+reusableMaintenancePath(m, "preparation-fence.json")+"\n")),
		metadata("setup.env", []byte("FASED_MAINTENANCE\n")), metadata("worker.env", []byte("FASED_MAINTENANCE\n")), metadata("provision.env", []byte("FASED_MAINTENANCE\n")),
		metadata("copy-runner.json", mustJSON(reusableCopyMarker{1, m.Owner, m.ID, "runner", a.RunnerSHA256, runnerSize})),
		metadata("copy-guard.json", mustJSON(reusableCopyMarker{1, m.Owner, m.ID, "guard", a.GuardSHA256, guardSize})),
		unit(reusableAtomicLauncherName(m.Owner), launcher), unit(reusableAtomicProvisionName(m.Owner), provision), unit(reusablePreparationFenceName(m.Owner), fence),
		unit(reusableAtomicSetupName(m.Owner), setup),
	}
	for _, slot := range reusableCompatibilitySlots {
		name, nameErr := reusableCompatibilityName(m.Owner, slot)
		body, bodyErr := reusableCompatibilityUnit(m, ownerGID, slot)
		if nameErr != nil || bodyErr != nil {
			return nil, nil, errors.New("fixed compatibility probe required")
		}
		files = append(files, metadata("compat-"+slot+".out", []byte("FASED_MAINTENANCE\n")), unit(name, body))
	}
	for n, slotID := range reusableSetupSlotIDs(m.ID) {
		data := []byte(sum(policy) + "  " + reusablePolicyCandidate(m, slotID) + "\n")
		files = append(files, metadata(fmt.Sprintf("policy-slot-%d.sha256", n), data))
	}
	for _, kind := range reusableStageKinds {
		body, err := reusableStageTemplate(m, ownerGID, kind)
		if err != nil {
			return nil, nil, err
		}
		allow, err := reusableStageAllowlist(kind)
		if err != nil {
			return nil, nil, err
		}
		base, _, err := reusableStageClass(kind)
		if err != nil {
			return nil, nil, err
		}
		if base == kind {
			files = append(files, metadata("stage-"+base+".allow", allow))
		}
		files = append(files, unit(fmt.Sprintf("fased-custody-%d-stage-%s@.service", m.Owner, kind), body))
	}
	return files, policy, nil
}

// rejectUnguardedSetupPolicy rejects the known direct staging bypass. Passing
// is not a complete policy review; other privileged operations still need it.
func rejectUnguardedSetupPolicy(policy []byte) error {
	if len(policy) == 0 {
		return errors.New("temporary maintenance authority is empty")
	}
	for _, line := range bytes.Split(policy, []byte("\n")) {
		if bytes.Contains(line, []byte(" NOPASSWD: /usr/bin/ln ")) || bytes.Contains(line, []byte(" NOPASSWD: /usr/bin/rm ")) {
			return errors.New("temporary maintenance authority permits unguarded staging publication or cleanup")
		}
	}
	return nil
}

// The draft uses fixed OS commands and cannot publish the recurring sudo
// policy directly. Its other commands must pass admission before activation.
func draftReusableSetupTemporaryPolicy(m Manifest, a reusableMaintenanceAuthority, ownerGID int, ownerName string, runnerSize, guardSize int64, notAfter int64) ([]byte, error) {
	if notAfter <= 0 || notAfter > 4102444800 {
		return nil, errors.New("bounded temporary setup deadline required")
	}
	files, policy, err := reusableSetupFiles(m, a, ownerGID, ownerName, runnerSize, guardSize)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	seen := make(map[string]bool)
	escape := strings.NewReplacer("\\", "\\\\", ",", "\\,", ":", "\\:", "=", "\\=")
	grant := func(args ...string) {
		key := strings.Join(args, "\x00")
		if seen[key] {
			return
		}
		seen[key] = true
		for i := range args {
			args[i] = escape.Replace(args[i])
		}
		fmt.Fprintf(&out, "%s ALL=(root) NOTAFTER=%s NOPASSWD: %s\n", ownerName, time.Unix(notAfter, 0).UTC().Format("20060102150405Z"), strings.Join(args, " "))
	}
	policyPaths := []string{reusableSetupGrantPath(m), reusableAtomicPolicyPath(m.Owner), reusablePolicySelected(m), nativePolicyPath(m), remoteProofPolicyPath(m)}
	for _, slotID := range reusableSetupSlotIDs(m.ID) {
		policyPaths = append(policyPaths, reusablePolicyCandidate(m, slotID))
	}
	for _, path := range policyPaths {
		grant("/usr/bin/find", "/etc/sudoers.d", "-maxdepth", "1", "-name", filepath.Base(path), "-printf", "%U:%G:%m:%y")
		grant("/usr/bin/cat", "--", path)
	}
	grant("/usr/bin/cat", "--", reusableSetupGrantSource(m))
	grant("/usr/bin/find", "/etc/sudoers.d", "-maxdepth", "1", "-name", filepath.Base(reusableSetupGrantPath(m)), "-samefile", reusableSetupGrantSource(m), "-printf", "x")
	grant("/usr/bin/setpriv", "--version")
	for _, dir := range []string{reusableMaintenancePath(m, ""), reusableMaintenancePath(m, "staging"), m.Root + "/updates", "/etc/systemd/system/" + Unit(m.Owner, "app") + ".d", "/etc/systemd/system/" + Unit(m.Owner, "signer") + ".d"} {
		grant("/usr/bin/mkdir", "--mode=0755", "--", dir)
	}
	inboxArgs, err := boundReusableInboxCreateArgs(m.Owner, m.Signer.GID, m.ID)
	if err != nil {
		return nil, err
	}
	grant(inboxArgs...)
	for _, f := range files {
		for _, slotID := range reusableSetupSlotIDs(m.ID) {
			pending := filepath.Join(filepath.Dir(f.path), "."+filepath.Base(f.path)+"-"+slotID+".pending")
			var args []string
			if f.unit {
				args, err = BoundAtomicUnitWriteArgs(m.Owner, pending, f.data)
			} else {
				args, err = BoundReusableMetadataWriteArgs(m.Owner, pending, f.data)
			}
			if err != nil {
				return nil, err
			}
			grant(args...)
			grant("/usr/bin/sync", "-f", pending)
			linkArgs, linkErr := boundReusableSetupLinkArgs(m.Owner, slotID, pending, f.path, f.data)
			if linkErr != nil {
				return nil, linkErr
			}
			grant(linkArgs...)
			cleanupArgs, cleanupErr := boundReusableSetupCleanupArgs(m.Owner, slotID, pending, f.path, f.data)
			if cleanupErr != nil {
				return nil, cleanupErr
			}
			grant(cleanupArgs...)
		}
		grant("/usr/bin/sync", "-f", filepath.Dir(f.path))
		if strings.HasSuffix(f.path, ".env") || strings.HasPrefix(filepath.Base(f.path), "compat-") && strings.HasSuffix(f.path, ".out") {
			markerArgs, markerErr := boundReusableSetupLinkArgs(m.Owner, m.ID, f.path, f.path+".owner", f.data)
			if markerErr != nil {
				return nil, markerErr
			}
			grant(markerArgs...)
		}
	}
	for _, image := range []struct{ name, component, digest string }{{"runner", "runner", a.RunnerSHA256}, {"atomic-guard", "guard", a.GuardSHA256}} {
		target := reusableMaintenancePath(m, image.name)
		for _, slotID := range reusableSetupSlotIDs(m.ID) {
			pending := reusableMaintenancePath(m, "."+image.name+"-"+slotID+".pending")
			copyArgs, copyErr := boundReusableImageCopyArgs(m.Owner, slotID, image.component, image.digest)
			if copyErr != nil {
				return nil, copyErr
			}
			grant(copyArgs...)
			grant("/usr/bin/sync", "-f", pending)
			linkArgs, linkErr := boundReusableImageLinkArgs(m.Owner, slotID, image.component, image.digest)
			if linkErr != nil {
				return nil, linkErr
			}
			grant(linkArgs...)
			cleanupArgs, cleanupErr := boundReusableImageCleanupArgs(m.Owner, slotID, image.component, image.digest)
			if cleanupErr != nil {
				return nil, cleanupErr
			}
			grant(cleanupArgs...)
		}
		grant("/usr/bin/sync", "-f", filepath.Dir(target))
	}
	guard := []byte("[Unit]\nAssertPathExists=!" + m.Root + "/update-start-blocked.json\n")
	for _, role := range []string{"app", "signer"} {
		target := "/etc/systemd/system/" + Unit(m.Owner, role) + ".d/20-fased-update-guard.conf"
		for _, slotID := range reusableSetupSlotIDs(m.ID) {
			pending := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-"+slotID+".pending")
			args, bindErr := BoundAtomicUnitWriteArgs(m.Owner, pending, guard)
			if bindErr != nil {
				return nil, bindErr
			}
			grant(args...)
			grant("/usr/bin/sync", "-f", pending)
			linkArgs, linkErr := boundReusableSetupLinkArgs(m.Owner, slotID, pending, target, guard)
			if linkErr != nil {
				return nil, linkErr
			}
			grant(linkArgs...)
			cleanupArgs, cleanupErr := boundReusableSetupCleanupArgs(m.Owner, slotID, pending, target, guard)
			if cleanupErr != nil {
				return nil, cleanupErr
			}
			grant(cleanupArgs...)
		}
		grant("/usr/bin/sync", "-f", filepath.Dir(target))
	}
	for _, slotID := range reusableSetupSlotIDs(m.ID) {
		pending := reusablePolicyCandidate(m, slotID)
		args, bindErr := BoundReusablePolicyWriteArgs(m.Owner, pending, policy)
		if bindErr != nil {
			return nil, bindErr
		}
		grant(args...)
		grant("/usr/bin/sync", "-f", pending)
	}
	grant("/usr/bin/systemctl", "daemon-reload")
	grant("/usr/bin/systemctl", "enable", "--", reusableAtomicSetupName(m.Owner))
	grant("/usr/bin/systemctl", "start", "--", reusableAtomicSetupName(m.Owner))
	return []byte(out.String()), nil
}

func reusableSetupTemporaryPolicy(m Manifest, a reusableMaintenanceAuthority, ownerGID int, ownerName string, runnerSize, guardSize int64, notAfter int64) ([]byte, error) {
	policy, err := draftReusableSetupTemporaryPolicy(m, a, ownerGID, ownerName, runnerSize, guardSize, notAfter)
	if err != nil {
		return nil, err
	}
	if err = rejectUnguardedSetupPolicy(policy); err != nil {
		return nil, err
	}
	// The copy guard requires an earlier signer hold and OS fence; their normal
	// predecessor handoff and interruption recovery are not established yet.
	// A count limit alone also does not guarantee filesystem free space.
	// Never activate the temporary grant until those boundaries are established.
	return nil, errors.New("guarded staging recovery is not yet established; preserve setup state")
}

// Classify only an inode-marked copy with matching original bytes. The caller
// preserves it until the root-enforced partial recovery operation is available.
func classifyReusableImageCopy(pending, source string, size int64) error {
	return classifyReusableImageCopyAt(pending, source, size, 0, 0, trustedParents)
}

func classifyReusableImageCopyAt(pending, source string, size int64, uid, gid int, trust func(string) error) error {
	if trust == nil || trust(filepath.Dir(pending)) != nil || trust(filepath.Dir(source)) != nil {
		return errors.New("protected maintenance copy parents required")
	}
	stage, err := openStageRecoveryFile(pending, uid, gid, []uint32{0755}, size)
	if err != nil {
		return err
	}
	defer stage.file.Close()
	marker, err := openStageRecoveryFile(pending+".source", uid, gid, []uint32{0755}, size)
	if err != nil {
		return errors.New("maintenance image copy lacks its ownership marker; preserving it")
	}
	defer marker.file.Close()
	approved, err := openStageRecoveryFile(source, uid, gid, []uint32{0444}, size)
	if err != nil {
		return err
	}
	defer approved.file.Close()
	if stage.stat.Nlink != 2 || marker.stat.Nlink != 2 || stage.stat.Dev != marker.stat.Dev || stage.stat.Ino != marker.stat.Ino || approved.stat.Nlink != 1 || approved.stat.Size != size || stageRecoveryPrefix(stage, approved) != nil || !stage.stable() || !marker.stable() || !approved.stable() {
		return errors.New("foreign maintenance image copy; preserving it")
	}
	return nil
}

func publishReusableImage(ctx context.Context, m Manifest, component, digest string, source string, size int64, s *System) error {
	if err := verifyApprovedExecutable(source, digest, size); err != nil {
		return err
	}
	name := "runner"
	if component == "guard" {
		name = "atomic-guard"
	} else if component != "runner" {
		return errors.New("unsupported maintenance image")
	}
	marker := reusableMaintenancePath(m, "copy-"+component+".json")
	target := reusableMaintenancePath(m, name)
	pendingFor := func(slotID string) string {
		return reusableMaintenancePath(m, "."+name+"-"+slotID+".pending")
	}
	want := mustJSON(reusableCopyMarker{1, m.Owner, m.ID, component, digest, size})
	if _, err := readRoot(marker); errors.Is(err, os.ErrNotExist) {
		for _, slotID := range reusableSetupSlotIDs(m.ID) {
			for _, path := range []string{pendingFor(slotID), pendingFor(slotID) + ".source"} {
				if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
					return errors.New("unmarked maintenance image copy; preserving it")
				}
			}
		}
	} else if err != nil {
		return err
	}
	if err := publishReusableBound(ctx, m.Owner, m.ID, marker, want, readRoot, s.Run, BoundReusableMetadataWriteArgs); err != nil {
		return err
	}
	ready := false
	var finalInfo os.FileInfo
	if info, err := os.Lstat(target); err == nil {
		if err = verifyRootExecutable(target, digest); err != nil {
			return errors.New("foreign pinned maintenance image; preserving it")
		}
		finalInfo = info
		ready = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if ready {
		for _, slotID := range reusableSetupSlotIDs(m.ID) {
			pending := pendingFor(slotID)
			for _, path := range []string{pending, pending + ".source"} {
				info, statErr := os.Lstat(path)
				if errors.Is(statErr, os.ErrNotExist) {
					continue
				}
				if statErr != nil {
					return statErr
				}
				if os.SameFile(finalInfo, info) {
					if err := cleanupReusableImage(ctx, m, slotID, component, digest, pending, s); err != nil {
						return err
					}
					break
				}
			}
		}
		return nil
	}
	var slotID, pending string
	complete := false
	for _, candidate := range reusableSetupSlotIDs(m.ID) {
		path := pendingFor(candidate)
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			if _, markerErr := os.Lstat(path + ".source"); !errors.Is(markerErr, os.ErrNotExist) {
				return errors.New("foreign maintenance image marker; preserving it")
			}
			slotID, pending = candidate, path
			break
		}
		if statErr != nil {
			return statErr
		}
		if _, markerErr := os.Lstat(path + ".source"); errors.Is(markerErr, os.ErrNotExist) {
			// A crash can occur between exclusive creation and marker linking.
			// Never change the unmarked inode; use the other bounded slot.
			f, openErr := openStageRecoveryFile(path, 0, 0, []uint32{0755}, size)
			if openErr != nil {
				return openErr
			}
			valid := f.stat.Size == 0 && f.stat.Nlink == 1 && f.stable()
			f.file.Close()
			if !valid {
				return errors.New("unmarked maintenance image copy; preserving it")
			}
			continue
		} else if markerErr != nil {
			return markerErr
		}
		if err := classifyReusableImageCopy(path, source, size); err != nil {
			return err
		}
		if info.Size() == size {
			if err := verifyRootExecutable(path, digest); err != nil {
				return err
			}
			slotID, pending, complete = candidate, path, true
			break
		}
	}
	if slotID == "" {
		return errors.New("maintenance image retry slots are occupied; preserve them")
	}
	if err := verifyApprovedExecutable(source, digest, size); err != nil {
		return err
	}
	if !complete {
		copyArgs, err := boundReusableImageCopyArgs(m.Owner, slotID, component, digest)
		if err != nil {
			return err
		}
		if _, err = s.Run(ctx, nil, copyArgs...); err != nil {
			return err
		}
	}
	if err := verifyRootExecutable(pending, digest); err != nil {
		return errors.New("maintenance image copy is incomplete; preserve it for retry")
	}
	if err := verifyApprovedExecutable(source, digest, size); err != nil {
		return err
	}
	if _, err := s.Run(ctx, nil, "/usr/bin/sync", "-f", pending); err != nil {
		return err
	}
	linkArgs, err := boundReusableImageLinkArgs(m.Owner, slotID, component, digest)
	if err != nil {
		return err
	}
	if _, err = s.Run(ctx, nil, linkArgs...); err != nil {
		return err
	}
	if err = verifyRootExecutable(target, digest); err != nil {
		return errors.New("protected maintenance image publication was refused; preserving staging")
	}
	if _, err := s.Run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(target)); err != nil {
		return err
	}
	if err = cleanupReusableImage(ctx, m, slotID, component, digest, pending, s); err != nil {
		return err
	}
	_, err = s.Run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(target))
	return err
}

func cleanupReusableImage(ctx context.Context, m Manifest, slotID, component, digest, pending string, s *System) error {
	args, err := boundReusableImageCleanupArgs(m.Owner, slotID, component, digest)
	if err != nil {
		return err
	}
	if _, err = s.Run(ctx, nil, args...); err != nil {
		return err
	}
	for _, path := range []string{pending, pending + ".source"} {
		if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("protected image cleanup was refused; preserving staging")
		}
	}
	return nil
}

// Hidden sudoers candidates are inert. Incomplete files are preserved; the
// separate root-managed setup unit selects only a complete checksum-bound file.
func stageReusablePolicy(ctx context.Context, m Manifest, policy []byte, s *System) error {
	inspect := func(pending string) ([]byte, bool, error) {
		meta, err := s.Run(ctx, nil, "/usr/bin/find", "/etc/sudoers.d", "-maxdepth", "1", "-name", filepath.Base(pending), "-printf", "%U:%G:%m:%y")
		if err != nil {
			return nil, false, err
		}
		if len(meta) == 0 {
			return nil, false, nil
		}
		if string(meta) != "0:0:440:f" {
			return nil, false, errors.New("foreign maintenance policy staging; preserving it")
		}
		data, err := s.Run(ctx, nil, "/usr/bin/cat", "--", pending)
		if err != nil || !bytes.HasPrefix(policy, data) {
			return nil, false, errors.New("foreign maintenance policy staging; preserving it")
		}
		return data, true, nil
	}
	for _, slotID := range reusableSetupSlotIDs(m.ID) {
		pending := reusablePolicyCandidate(m, slotID)
		current, exists, err := inspect(pending)
		if err != nil {
			return err
		}
		if exists && !bytes.Equal(current, policy) {
			continue
		}
		if !exists {
			args, bindErr := BoundReusablePolicyWriteArgs(m.Owner, pending, policy)
			if bindErr != nil {
				return bindErr
			}
			if _, err = s.Run(ctx, policy, args...); err != nil {
				return err
			}
		}
		current, exists, err = inspect(pending)
		if err != nil || !exists || !bytes.Equal(current, policy) {
			return errors.New("maintenance policy staging is incomplete; preserve it")
		}
		_, err = s.Run(ctx, nil, "/usr/bin/sync", "-f", pending)
		return err
	}
	return errors.New("maintenance policy retry slots are occupied; preserve them")
}

func prepareReusableInbox(ctx context.Context, m Manifest, s *System) error {
	path := reusableMaintenancePath(m, "inbox")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		args, bindErr := boundReusableInboxCreateArgs(m.Owner, m.Signer.GID, m.ID)
		if bindErr != nil {
			return bindErr
		}
		if _, err = s.Run(ctx, nil, args...); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || st.Uid != uint32(m.Owner) || st.Gid != uint32(m.Signer.GID) || st.Mode&07777 != 02710 {
		return errors.New("foreign maintenance inbox; preserving it")
	}
	return nil
}

func verifyReusableOutput(path string) error {
	return verifyReusableOutputAt(path, 0, 0)
}

func verifyReusableOutputAt(path string, uid, gid uint32) error {
	limit := int64(4096)
	if strings.HasPrefix(filepath.Base(path), "compat-") && strings.HasSuffix(path, ".out") {
		limit = 65536
	}
	a, err := os.Lstat(path)
	if err != nil {
		return err
	}
	b, err := os.Lstat(path + ".owner")
	if err != nil {
		return err
	}
	valid := func(info os.FileInfo) bool {
		st, ok := info.Sys().(*syscall.Stat_t)
		return ok && info.Mode().IsRegular() && st.Uid == uid && st.Gid == gid && st.Mode&07777 == 0644 && st.Nlink == 2 && info.Size() <= limit
	}
	if !valid(a) || !valid(b) || !os.SameFile(a, b) {
		return errors.New("foreign maintenance output; preserving it")
	}
	return nil
}

func prepareReusableOutput(ctx context.Context, m Manifest, path string, s *System) error {
	_, markerErr := os.Lstat(path + ".owner")
	if markerErr == nil {
		return verifyReusableOutput(path)
	}
	if !errors.Is(markerErr, os.ErrNotExist) {
		return markerErr
	}
	initial := []byte("FASED_MAINTENANCE\n")
	if err := publishReusableBound(ctx, m.Owner, m.ID, path, initial, readRoot, s.Run, BoundReusableMetadataWriteArgs); err != nil {
		return err
	}
	markerArgs, err := boundReusableSetupLinkArgs(m.Owner, m.ID, path, path+".owner", initial)
	if err != nil {
		return err
	}
	if _, err := s.Run(ctx, initial, markerArgs...); err != nil {
		return err
	}
	if _, err := s.Run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(path)); err != nil {
		return err
	}
	return verifyReusableOutput(path)
}

func prepareReusableRootDir(ctx context.Context, path string, s *System) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, err = s.Run(ctx, nil, "/usr/bin/mkdir", "--mode=0755", "--", path); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || st.Uid != 0 || st.Gid != 0 || st.Mode&07777 != 0755 {
		return errors.New("foreign setup directory; preserving it")
	}
	return nil
}

// ReviewReusableMaintenanceSetup produces only the exact short-lived OS rule;
// it does not install a rule, change the predecessor, or open a signer intent.
func ReviewReusableMaintenanceSetup(m Manifest, targetCore, targetSigner string) ([]byte, error) {
	if m.Owner <= 0 || os.Getuid() != m.Owner || os.Geteuid() != m.Owner {
		return nil, errors.New("ordinary managed installation owner required")
	}
	lock, err := OwnerLock()
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	current, err := Load(m.Owner)
	if err != nil || current != m || Verify(m) != nil {
		return nil, errors.New("installed predecessor changed; preserving it")
	}
	if err = noActiveController(m); err != nil {
		return nil, err
	}
	approval, err := readReusableSetupApproval(m, targetCore, targetSigner)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if approval.IssuedAt > now || approval.NotAfter <= now || approval.NotAfter-now > 10*60 {
		return nil, errors.New("temporary maintenance authority is expired or exceeds ten minutes")
	}
	_, runnerSize, err := approvedReusableImage(m.Owner, approval.Authority.RunnerSHA256)
	if err != nil {
		return nil, err
	}
	_, guardSize, err := approvedReusableImage(m.Owner, approval.Authority.GuardSHA256)
	if err != nil {
		return nil, err
	}
	u, err := user.LookupId(strconv.Itoa(m.Owner))
	if err != nil {
		return nil, errors.New("ordinary installation owner unavailable")
	}
	ownerGID, err := strconv.Atoi(u.Gid)
	if err != nil || ownerGID <= 0 {
		return nil, errors.New("ordinary installation group unavailable")
	}
	return reusableSetupTemporaryPolicy(m, approval.Authority, ownerGID, u.Username, runnerSize, guardSize, approval.NotAfter)
}

// PrepareReusableMaintenance connects the existing installer to the pinned
// maintenance boundary. The temporary OS grant and its source must already be
// root-approved; the setup service removes that grant before publishing the
// persistent policy and remains enabled until that transition is durable.
func PrepareReusableMaintenance(ctx context.Context, m Manifest, targetCore, targetSigner string, s *System) error {
	if s == nil || m.Owner <= 0 || os.Getuid() != m.Owner || os.Geteuid() != m.Owner {
		return errors.New("ordinary managed installation owner required")
	}
	lock, err := OwnerLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	current, err := Load(m.Owner)
	if err != nil || current != m || Verify(m) != nil {
		return errors.New("installed predecessor changed; preserving it")
	}
	if err = noActiveController(m); err != nil {
		return err
	}
	if m.Enrollment {
		if _, err = ReadEnrollmentCommit(m); err != nil {
			return err
		}
	}
	approval, err := readReusableSetupApproval(m, targetCore, targetSigner)
	if err != nil {
		return err
	}
	a := &approval.Authority
	runner, runnerSize, err := approvedReusableImage(m.Owner, a.RunnerSHA256)
	if err != nil {
		return err
	}
	guard, guardSize, err := approvedReusableImage(m.Owner, a.GuardSHA256)
	if err != nil {
		return err
	}
	u, err := user.LookupId(strconv.Itoa(m.Owner))
	if err != nil {
		return errors.New("ordinary installation owner unavailable")
	}
	ownerGID, err := strconv.Atoi(u.Gid)
	if err != nil || ownerGID <= 0 {
		return errors.New("ordinary installation group unavailable")
	}
	files, policy, err := reusableSetupFiles(m, *a, ownerGID, u.Username, runnerSize, guardSize)
	if err != nil {
		return err
	}
	if err = s.UseScopedMaintenance(m.Owner); err != nil {
		return err
	}
	policyMeta, err := s.Run(ctx, nil, "/usr/bin/find", "/etc/sudoers.d", "-maxdepth", "1", "-name", filepath.Base(reusableAtomicPolicyPath(m.Owner)), "-printf", "%U:%G:%m:%y")
	if err != nil {
		return err
	}
	if len(policyMeta) != 0 {
		if err = verifyReusableAtomicHost(ctx, InstallTransaction{Base: m}, s, a); err != nil {
			return err
		}
		return verifyReusablePreparationConfiguration(ctx, m, a)
	}
	now := time.Now().Unix()
	if approval.IssuedAt > now || approval.NotAfter <= now || approval.NotAfter-now > 10*60 {
		return errors.New("temporary maintenance authority is expired or exceeds ten minutes")
	}
	wantGrant, err := reusableSetupTemporaryPolicy(m, *a, ownerGID, u.Username, runnerSize, guardSize, approval.NotAfter)
	if err != nil {
		return err
	}
	grantSource, err := s.Run(ctx, nil, "/usr/bin/cat", "--", reusableSetupGrantSource(m))
	if err != nil || !bytes.Equal(grantSource, wantGrant) {
		return errors.New("root-approved temporary maintenance authority differs")
	}
	link, err := s.Run(ctx, nil, "/usr/bin/find", "/etc/sudoers.d", "-maxdepth", "1", "-name", filepath.Base(reusableSetupGrantPath(m)), "-samefile", reusableSetupGrantSource(m), "-printf", "x")
	if err != nil || string(link) != "x" {
		return errors.New("temporary maintenance authority is not the approved file")
	}
	if err = verifyEffectiveAtomicPolicy(ctx, InstallTransaction{Base: m}, s, reusableSetupGrantPath(m), wantGrant); err != nil {
		return err
	}
	// Refuse an existing policy staging file if this installation has not yet
	// published its matching root-owned digest marker.
	markerPath := reusableMaintenancePath(m, "policy.sha256")
	marker, markerErr := readRoot(markerPath)
	if errors.Is(markerErr, os.ErrNotExist) {
		paths := []string{reusablePolicySelected(m)}
		for _, slotID := range reusableSetupSlotIDs(m.ID) {
			paths = append(paths, reusablePolicyCandidate(m, slotID))
		}
		for _, path := range paths {
			meta, inspectErr := s.Run(ctx, nil, "/usr/bin/find", "/etc/sudoers.d", "-maxdepth", "1", "-name", filepath.Base(path), "-printf", "%U:%G:%m:%y")
			if inspectErr != nil || len(meta) != 0 {
				return errors.New("unmarked maintenance policy staging; preserving it")
			}
		}
	} else if markerErr != nil || !known(marker, []byte(sum(policy)+"  "+reusablePolicySelected(m)+"\n"), 0644) {
		return errors.New("foreign maintenance policy digest; preserving it")
	}
	for _, dir := range []string{reusableMaintenancePath(m, ""), reusableMaintenancePath(m, "staging"), m.Root + "/updates", "/etc/systemd/system/" + Unit(m.Owner, "app") + ".d", "/etc/systemd/system/" + Unit(m.Owner, "signer") + ".d"} {
		if err = prepareReusableRootDir(ctx, dir, s); err != nil {
			return err
		}
	}
	if err = prepareReusableInbox(ctx, m, s); err != nil {
		return err
	}
	if err = publishReusableImage(ctx, m, "runner", a.RunnerSHA256, runner, runnerSize, s); err != nil {
		return err
	}
	if err = probeInitialMigrationCapabilities(reusableMaintenancePath(m, "runner"), m); err != nil {
		return errors.New("approved maintenance reader failed its capability check")
	}
	if err = publishReusableImage(ctx, m, "guard", a.GuardSHA256, guard, guardSize, s); err != nil {
		return err
	}
	for _, f := range files {
		if strings.HasSuffix(f.path, "/copy-runner.json") || strings.HasSuffix(f.path, "/copy-guard.json") {
			continue
		}
		if strings.HasSuffix(f.path, ".env") || strings.HasPrefix(filepath.Base(f.path), "compat-") && strings.HasSuffix(f.path, ".out") {
			if err = prepareReusableOutput(ctx, m, f.path, s); err != nil {
				return err
			}
			continue
		}
		writer := BoundReusableMetadataWriteArgs
		if f.unit {
			writer = BoundAtomicUnitWriteArgs
		}
		if err = publishReusableBound(ctx, m.Owner, m.ID, f.path, f.data, readRoot, s.Run, writer); err != nil {
			return err
		}
	}
	assertion := []byte("[Unit]\nAssertPathExists=!" + m.Root + "/update-start-blocked.json\n")
	for _, role := range []string{"app", "signer"} {
		path := "/etc/systemd/system/" + Unit(m.Owner, role) + ".d/20-fased-update-guard.conf"
		if err = publishReusableBound(ctx, m.Owner, m.ID, path, assertion, readRoot, s.Run, BoundAtomicUnitWriteArgs); err != nil {
			return err
		}
	}
	if err = stageReusablePolicy(ctx, m, policy, s); err != nil {
		return err
	}
	if _, err = s.Run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err = s.Run(ctx, nil, "/usr/bin/systemctl", "enable", "--", reusableAtomicSetupName(m.Owner)); err != nil {
		return err
	}
	if _, err = s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", reusableAtomicSetupName(m.Owner)); err != nil {
		return fmt.Errorf("protected maintenance setup did not complete; preserve and resume it: %w", err)
	}
	if err = verifyReusableAtomicHost(ctx, InstallTransaction{Base: m}, s, a); err != nil {
		return err
	}
	return verifyReusablePreparationConfiguration(ctx, m, a)
}

// A second fixed staging name lets one interrupted copy be retried without
// deleting or modifying a file whose ownership cannot be established.
func reusableSetupSlotIDs(id string) []string {
	return []string{id, sum([]byte("reusable-setup-retry:v1:" + id))[:32]}
}

// publishReusableBound exposes a static file only after the exact staged bytes
// are durable. Incomplete staging is preserved; retry uses the other fixed
// slot and never needs a direct privileged removal.
func publishReusableBound(ctx context.Context, owner int, id, target string, data []byte, read readHost, run runHost, args func(int, string, []byte) ([]string, error)) error {
	if owner <= 0 || !update.IDOK(id) || len(data) == 0 || read == nil || run == nil || args == nil {
		return errors.New("valid reusable setup input required")
	}
	current, err := read(target)
	ready := err == nil
	if ready && !known(current, data, 0644) {
		return errors.New("foreign reusable setup file; preserving it")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, slotID := range reusableSetupSlotIDs(id) {
		pending := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-"+slotID+".pending")
		staged, stageErr := read(pending)
		if stageErr == nil && (staged.UID != 0 || !staged.Regular || staged.Mode != 0644 || !bytes.HasPrefix(data, staged.Data)) {
			return errors.New("foreign reusable staging file; preserving it")
		}
		if stageErr != nil && !errors.Is(stageErr, os.ErrNotExist) {
			return stageErr
		}
		if stageErr == nil && !bytes.Equal(staged.Data, data) {
			continue
		}
		if !ready {
			if errors.Is(stageErr, os.ErrNotExist) {
				command, bindErr := args(owner, pending, data)
				if bindErr != nil {
					return bindErr
				}
				if _, err = run(ctx, data, command...); err != nil {
					return err
				}
			}
			staged, stageErr = read(pending)
			if stageErr != nil || !known(staged, data, 0644) {
				return errors.New("reusable staging copy is incomplete; preserve it")
			}
			if _, err = run(ctx, nil, "/usr/bin/sync", "-f", pending); err != nil {
				return err
			}
			linkArgs, bindErr := boundReusableSetupLinkArgs(owner, slotID, pending, target, data)
			if bindErr != nil {
				return bindErr
			}
			if _, err = run(ctx, data, linkArgs...); err != nil {
				return err
			}
			published, readErr := read(target)
			if readErr != nil || !known(published, data, 0644) {
				return errors.New("protected setup publication was refused; preserving staging")
			}
			if _, err = run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(target)); err != nil {
				return err
			}
			ready = true
		}
		if stageErr == nil {
			cleanupArgs, bindErr := boundReusableSetupCleanupArgs(owner, slotID, pending, target, data)
			if bindErr != nil {
				return bindErr
			}
			if _, err = run(ctx, data, cleanupArgs...); err != nil {
				return err
			}
			if _, readErr := read(pending); !errors.Is(readErr, os.ErrNotExist) {
				return errors.New("protected setup cleanup was refused; preserving staging")
			}
			if _, err = run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(target)); err != nil {
				return err
			}
		}
		if ready {
			return nil
		}
	}
	if ready {
		return nil
	}
	return errors.New("reusable staging retry slots are occupied; preserve them")
}
