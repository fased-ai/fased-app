//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// Only the disposable signer test binary contains this one-shot cut point.
// It withholds an actual validated status response while the real worker waits;
// it never creates a signer hold or authorizes an update.
type fixtureCutMarker struct {
	Version            int    `json:"version"`
	OperationID        string `json:"operationId"`
	InstallationDigest string `json:"installationDigest"`
	TargetCore         string `json:"targetCore"`
	TargetSigner       string `json:"targetSigner"`
	BootID             string `json:"bootId"`
	PID                int    `json:"pid"`
}

// installFixtureCutMarker creates a durable public marker exclusively. A
// matching marker permits continuation only after a different boot.
func installFixtureCutMarker(root *os.Root, want fixtureCutMarker) (bool, error) {
	name := "fixture-cut-" + want.OperationID + ".json"
	if !update.IDOK(want.OperationID) || len(want.InstallationDigest) != 64 || len(want.TargetCore) != 64 || len(want.TargetSigner) != 64 || want.PID <= 0 || !regexp.MustCompile(`^[a-f0-9-]{36}$`).MatchString(want.BootID) {
		return false, errors.New("exact fixture cut identity required")
	}
	if existing, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0); err == nil {
		info, e := existing.Stat()
		st, ok := infoSysFixture(info)
		b, readErr := io.ReadAll(io.LimitReader(existing, 4097))
		closeErr := existing.Close()
		parts := bytes.Split(b, []byte("\n"))
		var old fixtureCutMarker
		if e != nil || !ok || int(st.Uid) != os.Getuid() || st.Nlink != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0644 || readErr != nil || closeErr != nil || len(b) > 4096 || len(parts) != 3 || string(parts[1]) != "READY" || len(parts[2]) != 0 || update.StrictJSON(parts[0], &old) != nil || old.Version != 1 || old.OperationID != want.OperationID || old.InstallationDigest != want.InstallationDigest || old.TargetCore != want.TargetCore || old.TargetSigner != want.TargetSigner || old.PID <= 0 || !regexp.MustCompile(`^[a-f0-9-]{36}$`).MatchString(old.BootID) {
			return false, errors.New("foreign or incomplete fixture cut marker")
		}
		if old.BootID == want.BootID {
			return false, errors.New("fixture cut already reserved on this boot")
		}
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	pending := name + ".pending"
	f, err := root.OpenFile(pending, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return false, err
	}
	defer f.Close()
	want.Version = 1
	b, err := json.Marshal(want)
	if err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Chmod(0644)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = root.Link(pending, name)
	}
	if err == nil {
		err = root.Remove(pending)
	}
	if err != nil {
		return false, err
	}
	dir, err := root.Open(".")
	if err != nil {
		return false, err
	}
	err = errors.Join(dir.Sync(), dir.Close())
	if err == nil {
		_, err = f.WriteString("READY\n")
	}
	if err == nil {
		err = f.Sync()
	}
	return err == nil, err
}

func infoSysFixture(info os.FileInfo) (*syscall.Stat_t, bool) {
	if info == nil {
		return nil, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return st, ok
}

func pauseControlledHeldStatus(args []string) error {
	if len(args) != 5 || args[0] != "custody-maintenance-status" || args[1] != "--owner" || args[2] != "1100" || args[3] != "--operation" || !update.IDOK(args[4]) {
		return nil
	}
	product, err := os.ReadFile("/sys/class/dmi/id/product_name")
	if err != nil || !(strings.Contains(string(product), "QEMU") || strings.Contains(string(product), "Standard PC")) {
		return nil
	}
	c, err := signer.MaintenanceConfig(1100)
	if err != nil {
		return err
	}
	if c.Provenance != signer.ControlledCustodyProfile || c.Profile != signer.OwnerCustodyProfile || c.Mode != signer.OrdinaryCustodyMode || c.OwnerUID != 1100 || c.SignerUID != os.Getuid() || os.Geteuid() != os.Getuid() || c.SocketDir != "/var/lib/fased-app-custody-1100/signer-ipc" {
		return nil
	}
	r, err := update.ReadMaintenanceRecord(1100)
	if err != nil || r.OperationID != args[4] {
		return errors.New("exact fixture maintenance operation required")
	}
	if r.Phase != "held" {
		return nil
	}
	approval, err := update.ReadMaintenanceApproval(1100, r.OperationID)
	if err != nil || r.Start != "" || r.Package == nil || !r.Package.DevelopmentOnly || !approval.DevelopmentOnly || approval.InstallationID != c.SignerID || r.Installation == nil || !update.ValidInstallation(*r.Installation) || r.Installation.Phase != "active" {
		return errors.New("protected fixture held approval required")
	}
	actual, err := signer.InspectInstallationReadOnly(c, r.OperationID)
	if err != nil || actual == nil || !update.ValidInstallation(*actual) || actual.Phase != "active" || actual.Digest != r.Installation.Digest || actual.Binding.InstallationID != c.SignerID {
		return errors.New("actual signer hold differs from the fixture journal")
	}
	f, err := update.OpenProtected("/var/lib/fased-update-fixture/fixture.json", 8192, 0644)
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	err = errors.Join(err, f.Close())
	var trust struct {
		Connected          bool   `json:"connected"`
		SeedSHA256         string `json:"seedSha256"`
		CASHA256           string `json:"caSha256"`
		SourceSHA256       string `json:"sourceSha256"`
		TargetSHA256       string `json:"targetSha256"`
		TargetSignerSHA256 string `json:"targetSignerSha256"`
	}
	if err != nil || len(b) > 8192 || update.StrictJSON(b, &trust) != nil || !trust.Connected || trust.TargetSHA256 != actual.Binding.TargetCore || trust.TargetSignerSHA256 != actual.Binding.TargetSigner {
		return errors.New("protected fixture target pair differs")
	}
	info, err := os.Lstat(c.SocketDir)
	st, ok := infoSysFixture(info)
	if err != nil || !ok || int(st.Uid) != c.SignerUID || !info.IsDir() || info.Mode().Perm() != 0755 {
		return errors.New("protected fixture signer directory required")
	}
	root, err := os.OpenRoot(c.SocketDir)
	if err != nil {
		return err
	}
	defer root.Close()
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	first, err := installFixtureCutMarker(root, fixtureCutMarker{OperationID: r.OperationID, InstallationDigest: actual.Digest, TargetCore: trust.TargetSHA256, TargetSigner: trust.TargetSignerSHA256, BootID: strings.TrimSpace(string(boot)), PID: os.Getpid()})
	if err != nil || !first {
		return err
	}
	// Stop only after the marker and directory are durable. The root worker is
	// synchronously waiting for this real status response. The observer requires
	// this exact process to be stopped before it cuts power.
	if err := syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
		return err
	}
	return errors.New("fixture signer unexpectedly resumed without reboot")
}

func TestFixtureCutMarkerRequiresSameHoldAndNewBoot(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	want := fixtureCutMarker{OperationID: strings.Repeat("a", 32), InstallationDigest: strings.Repeat("b", 64), TargetCore: strings.Repeat("c", 64), TargetSigner: strings.Repeat("d", 64), BootID: "01234567-89ab-cdef-0123-456789abcdef", PID: os.Getpid()}
	first, err := installFixtureCutMarker(root, want)
	if err != nil || !first {
		t.Fatal("first durable fixture cut refused", err)
	}
	if _, err = installFixtureCutMarker(root, want); err == nil {
		t.Fatal("same boot unexpectedly resumed")
	}
	want.BootID = "11234567-89ab-cdef-0123-456789abcdef"
	if first, err = installFixtureCutMarker(root, want); err != nil || first {
		t.Fatal("matching operation did not resume after reboot", err)
	}
	want.InstallationDigest = fmt.Sprintf("%064x", 1)
	if _, err = installFixtureCutMarker(root, want); err == nil {
		t.Fatal("foreign hold unexpectedly resumed")
	}
}
