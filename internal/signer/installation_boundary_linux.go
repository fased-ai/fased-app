//go:build linux

package signer

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// InstallationGuardMarker is public metadata, not signer or wallet authority.
// Only a root-managed transaction may publish or remove its exact bytes.
type InstallationGuardMarker struct {
	Version   int    `json:"version"`
	Owner     int    `json:"owner"`
	SignerID  string `json:"signerId"`
	IntentID  string `json:"intentId"`
	Digest    string `json:"digest"`
	Direction string `json:"direction"`
}

// InstallationPreparationFenceBytes is a denial-only marker. It never conveys
// signer authority; each subsequent operation must inspect the real cold hold.
func InstallationPreparationFenceBytes(owner int, signerID string) ([]byte, error) {
	if owner <= 0 || !update.IDOK(signerID) {
		return nil, errors.New("exact preparation fence identity required")
	}
	return json.Marshal(struct {
		Version  int    `json:"version"`
		Owner    int    `json:"owner"`
		SignerID string `json:"signerId"`
		Phase    string `json:"phase"`
	}{1, owner, signerID, "preparation"})
}

// VerifyInstallationPreparationBoundary accepts only the fixed root-owned
// sentinel inode and both stopped services. The caller must then inspect the
// signer journal and independently bind the actual hold before any mutation.
func VerifyInstallationPreparationBoundary(c Config) error {
	if c.Validate() != nil || os.Getuid() != c.SignerUID || os.Geteuid() != c.SignerUID || os.Getuid() <= 0 {
		return errors.New("configured unprivileged signer identity required")
	}
	root := fmt.Sprintf("/var/lib/fased-app-custody-%d", c.OwnerUID)
	marker, source := root+"/update-start-blocked.json", root+"/maintenance/preparation-fence.json"
	want, err := InstallationPreparationFenceBytes(c.OwnerUID, c.SignerID)
	if err != nil {
		return err
	}
	check := func() error {
		var b bytes.Buffer
		if err := readGuardRootFile(marker, 0644, 8192, &b); err != nil || !bytes.Equal(b.Bytes(), want) {
			return errors.New("exact protected preparation fence required")
		}
		return sameGuardRootFile(marker, source)
	}
	if err = check(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err = verifyStoppedCustodyServices(ctx, c.OwnerUID, marker, systemdGuardProperty); err != nil {
		return err
	}
	return check()
}

// VerifyInstallationPreparationStart checks the loaded restart assertions
// before the fixed preparation unit is allowed to create its sentinel.
func VerifyInstallationPreparationStart(c Config) error {
	if c.Validate() != nil || os.Getuid() != c.SignerUID || os.Geteuid() != c.SignerUID || os.Getuid() <= 0 {
		return errors.New("configured unprivileged signer identity required")
	}
	marker := fmt.Sprintf("/var/lib/fased-app-custody-%d/update-start-blocked.json", c.OwnerUID)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, role := range []string{"app", "signer"} {
		unit := "/org/freedesktop/systemd1/unit/fased_2dcustody_2d" + strconv.Itoa(c.OwnerUID) + "_2d" + role + "_2eservice"
		assertions, err := systemdGuardProperty(ctx, unit, "org.freedesktop.systemd1.Unit", "Asserts")
		if err != nil {
			return err
		}
		kill, err := systemdGuardProperty(ctx, unit, "org.freedesktop.systemd1.Service", "KillMode")
		if err != nil {
			return err
		}
		if err = validateSignerSystemdAssertions(assertions, kill, marker); err != nil {
			return fmt.Errorf("%s service boundary: %w", role, err)
		}
	}
	return nil
}

// VerifyInstallationGuardBoundary checks the persistent root marker and the
// assertion actually loaded by systemd. The OS policy must independently deny
// the owner direct marker removal and serialize all replacement/recovery units.
func VerifyInstallationGuardBoundary(c Config, intent, digest string) error {
	if c.Validate() != nil || os.Getuid() != c.SignerUID || os.Geteuid() != c.SignerUID || os.Getuid() <= 0 || !update.IDOK(intent) {
		return errors.New("configured unprivileged signer identity required")
	}
	h, err := hex.DecodeString(digest)
	if err != nil || len(h) != 32 || hex.EncodeToString(h) != digest {
		return errors.New("exact installation digest required")
	}
	marker := fmt.Sprintf("/var/lib/fased-app-custody-%d/update-start-blocked.json", c.OwnerUID)
	if err = trustedPath(marker); err != nil {
		return errors.New("root-managed signer restart guard is unavailable")
	}
	fd, err := unix.Open(marker, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("root-managed signer restart guard is unavailable")
	}
	f := os.NewFile(uintptr(fd), marker)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || st.Gid != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0644 || info.Size() <= 0 || info.Size() > 8192 {
		return errors.New("foreign signer restart guard; preserving it")
	}
	got, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil {
		return err
	}
	if err = validateInstallationGuardMarker(c, intent, digest, got); err != nil {
		return err
	}
	if err = trustedPath("/usr/bin/busctl"); err != nil {
		return errors.New("trusted systemd inspection utility required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err = verifyStoppedCustodyServices(ctx, c.OwnerUID, marker, systemdGuardProperty); err != nil {
		return err
	}
	current, err := os.Lstat(marker)
	if err != nil || !os.SameFile(info, current) || current.Mode() != info.Mode() || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return errors.New("signer restart guard changed during inspection")
	}
	return nil
}

func validateInstallationGuardMarker(c Config, intent, digest string, got []byte) error {
	var bound InstallationGuardMarker
	if update.StrictJSON(got, &bound) != nil || bound.Version != 1 || bound.Owner != c.OwnerUID || bound.SignerID != c.SignerID || bound.IntentID != intent || bound.Digest != digest || (bound.Direction != "forward" && bound.Direction != "rollback" && bound.Direction != "completion" && bound.Direction != "cleanup-rollback" && bound.Direction != "cleanup-complete") {
		return errors.New("signer restart guard differs from the installation")
	}
	want, err := json.Marshal(bound)
	if err != nil || !bytes.Equal(got, want) {
		return errors.New("signer restart guard is not canonical")
	}
	return nil
}

func verifyStoppedCustodyServices(ctx context.Context, owner int, marker string, property func(context.Context, string, string, string) ([]byte, error)) error {
	for _, role := range []string{"app", "signer"} {
		if err := verifyStoppedGuardService(ctx, owner, role, marker, property); err != nil {
			return fmt.Errorf("%s service boundary: %w", role, err)
		}
	}
	return nil
}

func verifyStoppedGuardService(ctx context.Context, owner int, role, marker string, property func(context.Context, string, string, string) ([]byte, error)) error {
	unitPath := "/org/freedesktop/systemd1/unit/fased_2dcustody_2d" + strconv.Itoa(owner) + "_2d" + role + "_2eservice"
	assertions, err := property(ctx, unitPath, "org.freedesktop.systemd1.Unit", "Asserts")
	if err != nil {
		return err
	}
	active, err := property(ctx, unitPath, "org.freedesktop.systemd1.Unit", "ActiveState")
	if err != nil {
		return err
	}
	pid, err := property(ctx, unitPath, "org.freedesktop.systemd1.Service", "MainPID")
	if err != nil {
		return err
	}
	killMode, err := property(ctx, unitPath, "org.freedesktop.systemd1.Service", "KillMode")
	if err != nil {
		return err
	}
	return validateSignerSystemdGuard(assertions, active, pid, killMode, marker)
}

// VerifyInstallationStartGuard runs as the owner before the OS worker creates
// a restart marker or stops services. A prior exact marker permits cold resume
// only when systemd confirms the signer is already stopped behind its guard.
func VerifyInstallationStartGuard(ctx context.Context, c Config, intent, digest, direction string) error {
	if err := CheckOwner(c); err != nil {
		return err
	}
	if c.Version == 3 && c.Provenance != ControlledCustodyProfile {
		return errors.New("start guard requires controlled disposable custody")
	}
	root := fmt.Sprintf("/var/lib/fased-app-custody-%d", c.OwnerUID)
	if c.StateDir != root+"/signer-state" || c.SocketDir != root+"/signer-ipc" {
		return errors.New("start guard requires the exact custody installation")
	}
	marker := root + "/update-start-blocked.json"
	read := func() ([]byte, error) {
		var b bytes.Buffer
		if err := readGuardRootFile(marker, 0644, 8192, &b); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	stopped := func() error {
		return verifyStoppedGuardService(ctx, c.OwnerUID, "signer", marker, systemdGuardProperty)
	}
	status := func() (Response, error) {
		return Call(ctx, c, true, Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "status", IntentID: intent}})
	}
	return validateInstallationStartGuard(c, intent, digest, direction, read, stopped, status)
}

func validateInstallationStartGuard(c Config, intent, digest, direction string, read func() ([]byte, error), stopped func() error, status func() (Response, error)) error {
	h, err := hex.DecodeString(digest)
	if !update.IDOK(intent) || err != nil || len(h) != 32 || hex.EncodeToString(h) != digest {
		return errors.New("exact guarded installation required")
	}
	switch direction {
	case "forward", "rollback", "completion", "cleanup-rollback", "cleanup-complete":
	default:
		return errors.New("unsupported guarded update direction")
	}
	marker, err := read()
	if err == nil {
		if err = validateInstallationGuardMarker(c, intent, digest, marker); err != nil {
			return err
		}
		if stopped() == nil {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("restart guard is unavailable; preserve installation")
	}
	r, err := status()
	if err != nil {
		return fmt.Errorf("signer installation status unavailable: %w", err)
	}
	if r.Installation == nil || !r.Status.Locked || r.Status.WalletSigner || r.Status.FinancialExecution {
		return errors.New("actual locked signer installation is unavailable")
	}
	i := r.Installation
	if i.Binding.InstallationID != c.SignerID {
		return errors.New("signer installation identity differs")
	}
	component, source := "core", i.Binding.TargetCore
	if direction == "rollback" {
		source = i.Binding.PreviousCore
	} else if direction == "cleanup-rollback" || direction == "cleanup-complete" {
		component, source = "installation", digest
	}
	if err = ValidateInstallationReplacement(i, intent, digest, direction, component, source); err != nil {
		return err
	}
	if direction == "forward" || direction == "rollback" {
		source = i.Binding.TargetSigner
		if direction == "rollback" {
			source = i.Binding.PreviousSigner
		}
		return ValidateInstallationReplacement(i, intent, digest, direction, "signer", source)
	}
	return nil
}

func systemdGuardProperty(ctx context.Context, path, iface, property string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/busctl", "--json=short", "get-property", "org.freedesktop.systemd1", path, iface, property)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LC_ALL=C"}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(stdout, 65537))
	if readErr != nil || len(b) > 65536 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("invalid systemd guard response")
	}
	if err = cmd.Wait(); err != nil {
		return nil, errors.New("systemd guard inspection failed")
	}
	return b, nil
}

func readSignerSystemdProperty(raw []byte, typ string, target any) error {
	type property struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	var p property
	if json.Unmarshal(raw, &p) != nil || p.Type != typ || json.Unmarshal(p.Data, target) != nil {
		return errors.New("invalid systemd guard property")
	}
	return nil
}

func validateSignerSystemdGuard(assertions, active, pid, killMode []byte, marker string) error {
	var state string
	var mainPID uint32
	if readSignerSystemdProperty(active, "s", &state) != nil || readSignerSystemdProperty(pid, "u", &mainPID) != nil || state != "inactive" || mainPID != 0 {
		return errors.New("service must be stopped with control-group cleanup")
	}
	return validateSignerSystemdAssertions(assertions, killMode, marker)
}

func validateSignerSystemdAssertions(assertions, killMode []byte, marker string) error {
	var kill string
	var rows [][]json.RawMessage
	if readSignerSystemdProperty(assertions, "a(sbbsi)", &rows) != nil || readSignerSystemdProperty(killMode, "s", &kill) != nil || kill != "control-group" {
		return errors.New("service must have its restart assertion and control-group cleanup")
	}
	for _, row := range rows {
		if len(row) != 5 {
			return errors.New("invalid systemd assertion")
		}
		var kind, path string
		var trigger, negate bool
		var result int32
		if json.Unmarshal(row[0], &kind) != nil || json.Unmarshal(row[1], &trigger) != nil || json.Unmarshal(row[2], &negate) != nil || json.Unmarshal(row[3], &path) != nil || json.Unmarshal(row[4], &result) != nil {
			return errors.New("invalid systemd assertion")
		}
		if kind == "AssertPathExists" && !trigger && negate && path == marker {
			return nil
		}
	}
	return errors.New("persistent signer restart assertion is not loaded")
}
