//go:build linux

package custodyinstall

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func protectedRecordPath(owner int) string { return maintenance.OwnerRoot(owner) + "/custody.json" }

func storeProtectedRecord(r update.MaintenanceRecord) error {
	if r.Version != 1 || r.Owner <= 0 || !update.IDOK(r.OperationID) {
		return errors.New("invalid maintenance journal identity")
	}
	previous, _, err := maintenance.ProtectedDigest(protectedRecordPath(r.Owner), 0644, 256<<10)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return maintenance.ReplaceProtected(protectedRecordPath(r.Owner), mustJSON(r), 0644, previous)
}

func protectedRecordManifests(r update.MaintenanceRecord) (Manifest, Manifest, error) {
	var base, target Manifest
	if strict(r.Base, &base) != nil || strict(r.Target, &target) != nil || base.validate() != nil || target.validate() != nil || base.Owner != r.Owner {
		return base, target, errors.New("protected deployment identities differ")
	}
	check := target
	check.CoreHash = base.CoreHash
	check.SignerHash = base.SignerHash
	if check != base {
		return base, target, errors.New("maintenance cannot change custody identities or paths")
	}
	return base, target, nil
}

// StageProtectedPackage consumes only root-protected independently verified
// artifact files. The IPC handler cannot supply a path, command, UID or trust.
func StageProtectedPackage(ctx context.Context, owner int, id string, p update.MaintenancePackage, bootstrap bool) error {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return err
	}
	if !update.IDOK(id) || p.Sequence == 0 || len(p.ReleaseSHA256) != 64 || len(p.Components) != 2 {
		return errors.New("verified maintenance package required")
	}
	m, err := Load(owner)
	if err != nil {
		return err
	}
	if err = Verify(m); err != nil {
		return err
	}
	var live signer.Response
	live, err = protectedOwnerPeer(ctx, a, m, signer.Request{Action: "fixture-update-inventory", ProbeID: update.NewProbe()})
	if err != nil || live.UpdateInventory == nil {
		return errors.Join(err, errors.New("actual predecessor signer inventory required before migration"))
	}
	if live.UpdateInventory.ActiveIntentID != "" {
		return errors.New("original signer intent requires its own recovery; no new update was admitted")
	}
	if m.Location != a.Profile || m.Version != 3 {
		return errors.New("matching ordinary Local/Remote custody required")
	}
	if _, err = os.Lstat(m.Root + "/update-start-blocked.json"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("original predecessor fence remains authoritative; preserve it")
	}
	if _, err = os.Lstat(m.Root + "/update-active.json"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("original installation marker remains authoritative; preserve it")
	}
	next := m
	for _, c := range p.Components {
		if c.Version != p.Version {
			return errors.New("component version differs from the verified package")
		}
		path := filepath.Join(update.MaintenanceDirectory(owner, id), c.Artifact.File)
		hash, size, err := maintenance.ProtectedDigest(path, 0755, 64<<20)
		if err != nil || hash != c.Artifact.SHA256 || size != c.Artifact.Size {
			return errors.New("protected artifact differs from authenticated package")
		}
		switch c.Contract.Component {
		case "core":
			if c.Artifact.File != "fased-app" {
				return errors.New("fixed core artifact required")
			}
			next.CoreHash = hash
		case "signer":
			if c.Artifact.File != "fased-signer" {
				return errors.New("fixed signer artifact required")
			}
			next.SignerHash = hash
		default:
			return errors.New("only the core/signer pair may be installed")
		}
	}
	var previousPackage *update.MaintenancePackage
	if old, err := update.ReadMaintenanceRecord(owner); err == nil {
		if old.OperationID == id {
			if old.Phase == "idle" {
				return errors.New("installation identity cannot be reused as an update")
			}
			if old.Package == nil || update.SnapshotDigest("package", *old.Package) != update.SnapshotDigest("package", p) {
				return errors.New("pending update target cannot change")
			}
			return nil
		}
		if old.Phase != "idle" && old.Phase != "completed" && old.Phase != "refused" {
			return errors.New("another exact maintenance operation requires recovery")
		}
		if old.Phase == "idle" || old.PreviousPackage != nil {
			if old.Phase == "idle" {
				if _, e := ValidateInitialMaintenanceRecord(old); e != nil {
					return e
				}
			}
			installed, e := old.InstalledPackage()
			if e != nil || p.Validate() != nil || p.Sequence <= installed.Sequence {
				return errors.New("custody release must advance the authenticated installed pair")
			}
			base, target, e := protectedRecordManifests(old)
			if e != nil || old.Phase == "idle" && (base != m || target != m) ||
				old.Phase == "completed" && (old.Start != "target" || target != m || old.Installation == nil || old.Installation.Phase != "completed" || !update.ValidInstallation(*old.Installation)) ||
				old.Phase == "refused" && (old.Start != "base" || base != m) {
				return errors.New("installed custody pair differs from the prior journal")
			}
			copy := *installed
			previousPackage = &copy
		}
		if old.Phase != "idle" {
			if err = maintenance.PublishProtected(update.MaintenanceDirectory(owner, old.OperationID)+"/receipt.json", mustJSON(old), 0644); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range []struct{ name, path, hash string }{{"previous-core", m.Root + "/fased-app", m.CoreHash}, {"previous-signer", m.Root + "/fased-signer", m.SignerHash}} {
		_, size, err := maintenance.ProtectedDigest(entry.path, 0755, 64<<20)
		if err != nil {
			return err
		}
		if err = maintenance.CopyProtected(entry.path, update.MaintenanceDirectory(owner, id)+"/"+entry.name, 0755, entry.hash, size, ""); err != nil {
			return err
		}
	}
	r := update.MaintenanceRecord{Version: 1, Owner: owner, OperationID: id, Phase: "prepared", Start: "base", Base: mustJSON(m), Target: mustJSON(next), Package: &p, PreviousPackage: previousPackage, Bootstrap: bootstrap}
	if err = storeProtectedRecord(r); err != nil {
		return err
	}
	if err = installProtectedMaintenanceUnits(ctx, a, m); err != nil {
		return err
	}
	return verifyProtectedLoadedUnits(ctx, a, m)
}

func DescribeProtectedPair(ctx context.Context, owner int, id, version string) ([]update.Component, error) {
	if _, err := maintenance.ValidateProcess(owner); err != nil {
		return nil, err
	}
	if !update.IDOK(id) {
		return nil, errors.New("fixed package required")
	}
	m, err := Load(owner)
	if err != nil {
		return nil, err
	}
	dir := update.MaintenanceDirectory(owner, id)
	var coreContract struct {
		Version            int             `json:"version"`
		Target             update.Target   `json:"target"`
		Contract           update.Contract `json:"contract"`
		WalletSigner       bool            `json:"walletSigner"`
		FinancialExecution bool            `json:"financialExecution"`
		UpdaterEnabled     bool            `json:"updaterEnabled"`
	}
	if err = readMaintenanceRole(ctx, m, "app", dir+"/fased-app", []string{"updates", "ordinary-custody-compatibility"}, &coreContract); err != nil {
		return nil, err
	}
	var signerContract update.Contract
	if err = readMaintenanceRole(ctx, m, "signer", dir+"/fased-signer", []string{"ordinary-custody-compatibility"}, &signerContract); err != nil {
		return nil, err
	}
	if coreContract.Version != 1 || coreContract.Contract.Component != "core" || signerContract.Component != "signer" {
		return nil, errors.New("actual ordinary custody contracts required")
	}
	var out []update.Component
	for _, c := range []update.Contract{coreContract.Contract, signerContract} {
		name := "fased-" + c.Component
		if c.Component == "core" {
			name = "fased-app"
		}
		hash, size, err := maintenance.ProtectedDigest(dir+"/"+name, 0755, 64<<20)
		if err != nil {
			return nil, err
		}
		out = append(out, update.Component{Version: version, Contract: c, Artifact: update.Artifact{File: name, Size: size, SHA256: hash}})
	}
	return out, nil
}

func verifyProtectedLoadedUnits(ctx context.Context, a maintenance.Authority, m Manifest) error {
	if err := verifyProtectedRootUnits(ctx, a); err != nil {
		return err
	}
	return verifyProtectedLoadedServices(ctx, a, m)
}

func verifyProtectedLoadedServices(ctx context.Context, a maintenance.Authority, m Manifest) error {
	for _, role := range []string{"app", "signer"} {
		if err := loadedUnit(m, role); err != nil {
			return err
		}
		raw, err := atomicUnitServiceProperties(ctx, Unit(m.Owner, role))
		if err != nil {
			return err
		}
		var v struct {
			Type string                       `json:"type"`
			Data []map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Type != "a{sv}" || len(v.Data) != 1 {
			return errors.New("actual loaded service commands required")
		}
		image := maintenance.ImagePath(a.ImageSHA256)
		for _, property := range []string{"ExecCondition", "ExecStartPre", "ExecStart", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload"} {
			var commands []atomicPairCommand
			if property == "ExecStartPre" {
				commands = []atomicPairCommand{{argv: []string{image, "maintenance-gate", "--owner", strconv.Itoa(m.Owner), "--role", role}}}
			}
			if property == "ExecStart" {
				for _, line := range strings.Split(string(definitions(m)["fased-"+role+".service"]), "\n") {
					if strings.HasPrefix(line, "ExecStart=") {
						commands = []atomicPairCommand{{argv: strings.Fields(strings.TrimPrefix(line, "ExecStart="))}}
					}
				}
				if len(commands) != 1 {
					return errors.New("fixed custody service command required")
				}
			}
			if err = validateAtomicCommandRows(v.Data[0][property], commands); err != nil {
				return fmt.Errorf("loaded %s %s: %w", role, property, err)
			}
		}
	}
	return nil
}

func protectedGuardPath(owner int, role string) string {
	return "/etc/systemd/system/" + Unit(owner, role) + ".d/30-fased-maintenance.conf"
}
func protectedRecoveryUnit(owner int) string {
	return fmt.Sprintf("fased-maintenance-%d-recover.service", owner)
}
func protectedWorkerUnit(owner int, id string) string {
	return fmt.Sprintf("fased-maintenance-%d@%s.service", owner, id)
}
func protectedGuard(a maintenance.Authority, role string) []byte {
	return []byte(fmt.Sprintf("[Unit]\nWants=%s\nAfter=%s\n[Service]\nExecStartPre=%s maintenance-gate --owner %d --role %s\n", protectedRecoveryUnit(a.Owner), protectedRecoveryUnit(a.Owner), maintenance.ImagePath(a.ImageSHA256), a.Owner, role))
}

func protectedMaintenanceFiles(a maintenance.Authority, m Manifest) (map[string][]byte, error) {
	image := maintenance.ImagePath(a.ImageSHA256)
	recovery := fmt.Sprintf("[Unit]\nDescription=Fased update recovery\nBefore=%s %s\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=%s maintenance-recover --owner %d\nTimeoutStartSec=10min\nNoNewPrivileges=yes\n[Install]\nWantedBy=multi-user.target\n", Unit(m.Owner, "app"), Unit(m.Owner, "signer"), image, m.Owner)
	worker := fmt.Sprintf("[Unit]\nDescription=Fased update\nWants=%s %s\nAfter=%s %s %s\n[Service]\nType=oneshot\nExecStart=%s maintenance-worker --owner %d --operation %%i\nTimeoutStartSec=10min\nNoNewPrivileges=yes\nPrivateTmp=yes\nProtectSystem=strict\nProtectHome=read-only\nReadWritePaths=%s %s %s %s\nRestrictSUIDSGID=yes\n", Unit(m.Owner, "app"), Unit(m.Owner, "signer"), protectedRecoveryUnit(m.Owner), Unit(m.Owner, "app"), Unit(m.Owner, "signer"), image, m.Owner, m.Root, maintenance.OwnerRoot(m.Owner), maintenance.StateRoot+"/images", a.Home+"/.config/fased-app-custody-installer")
	u, err := user.LookupId(strconv.Itoa(m.Owner))
	if err != nil {
		return nil, err
	}
	policy := fmt.Sprintf("polkit.addRule(function(action, subject) {\n  if (action.id == 'org.freedesktop.systemd1.manage-units' && (subject.user == %s || subject.user == %s) && action.lookup('verb') == 'start' && /^fased-maintenance-%d@[0-9a-f]{32}\\.service$/.test(action.lookup('unit'))) return polkit.Result.YES;\n});\n", strconv.Quote(u.Username), strconv.Quote(m.App.Name), m.Owner)
	return map[string][]byte{protectedGuardPath(m.Owner, "app"): protectedGuard(a, "app"), protectedGuardPath(m.Owner, "signer"): protectedGuard(a, "signer"), "/etc/systemd/system/" + protectedRecoveryUnit(m.Owner): []byte(recovery), fmt.Sprintf("/etc/systemd/system/fased-maintenance-%d@.service", m.Owner): []byte(worker), fmt.Sprintf("/etc/polkit-1/rules.d/60-fased-maintenance-%d.rules", m.Owner): []byte(policy)}, nil
}

func installProtectedMaintenanceUnits(ctx context.Context, a maintenance.Authority, m Manifest) error {
	return installProtectedMaintenanceUnitsForInitial(ctx, a, m, false)
}

func installProtectedMaintenanceUnitsForInitial(ctx context.Context, a maintenance.Authority, m Manifest, initial bool) error {
	files, err := protectedMaintenanceFiles(a, m)
	if err != nil {
		return err
	}
	ready := true
	for path, want := range files {
		f, e := readRoot(path)
		if errors.Is(e, os.ErrNotExist) {
			ready = false
			continue
		}
		if e != nil {
			return e
		}
		if !known(f, want, 0644) {
			return errors.New("foreign maintenance setup; preserving it")
		}
	}
	// Routine Update validates the immutable installed setup without trying to
	// write /etc from its read-only service sandbox.
	if ready && !initial {
		return nil
	}
	for _, role := range []string{"app", "signer"} {
		if err = maintenance.EnsureProtectedDirectory(filepath.Dir(protectedGuardPath(m.Owner, role))); err != nil {
			return err
		}
	}
	for path, want := range files {
		if err = maintenance.PublishProtected(path, want, 0644); err != nil {
			return err
		}
	}
	if _, err = protectedSystemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err = verifyProtectedRootUnits(ctx, a); err != nil {
		return err
	}
	if _, err = protectedSystemctl(ctx, "enable", "--", protectedRecoveryUnit(m.Owner)); err != nil {
		return err
	}
	// The prepared/Base recovery entry is a no-op while setup owns the lock.
	_, err = protectedSystemctl(ctx, "start", "--", protectedRecoveryUnit(m.Owner))
	return err
}

func protectedSystemctl(ctx context.Context, args ...string) ([]byte, error) {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return nil, errors.New("protected service operation required")
	}
	if err := trustedUtility("/usr/bin/systemctl"); err != nil {
		return nil, err
	}
	c := exec.CommandContext(ctx, "/usr/bin/systemctl", args...)
	c.Dir = "/"
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
	var b maintenanceOutput
	c.Stdout = &b
	c.Stderr = &b
	err := c.Run()
	if err != nil {
		return b.data, fmt.Errorf("fixed service operation failed: %w: %s", err, b.data)
	}
	return b.data, nil
}

type maintenanceOutput struct {
	mu   sync.Mutex
	data []byte
}

func (b *maintenanceOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data)+len(p) > 64<<10 {
		return 0, errors.New("bounded maintenance output exceeded")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func (b *maintenanceOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.data) }

// VerifyProtectedStart is read-only and runs under the service's real UID. A
// durable no-start phase survives crashes; the updater stops/cancels all jobs
// and proves both units inactive before replacing any file.
func VerifyProtectedStart(owner int, role string) error {
	a, err := maintenance.ReadAuthority(owner)
	if err != nil {
		return err
	}
	if err = maintenance.VerifyImage(a); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil || self != maintenance.ImagePath(a.ImageSHA256) {
		return errors.New("installed maintenance gate required")
	}
	r, err := update.ReadMaintenanceRecord(owner)
	if err != nil {
		return err
	}
	if r.Phase == "idle" {
		if err = VerifyInitialProtectedDeployment(owner); err != nil {
			return err
		}
	}
	base, target, err := protectedRecordManifests(r)
	if err != nil {
		return err
	}
	want := base
	if r.Start == "target" {
		want = target
		if r.Installation == nil || !update.ValidInstallation(*r.Installation) || r.Installation.Binding.InstallationID != want.ID {
			return errors.New("actual installation hold required before target start")
		}
	} else if r.Start != "base" {
		return errors.New("recover the exact pending update before starting services")
	}
	i := want.App
	if role == "signer" {
		i = want.Signer
	} else if role != "app" {
		return errors.New("fixed custody role required")
	}
	if os.Getuid() != i.UID || os.Geteuid() != i.UID {
		return errors.New("service gate identity differs")
	}
	for _, f := range []struct{ path, hash string }{{want.Root + "/fased-app", want.CoreHash}, {want.Root + "/fased-signer", want.SignerHash}} {
		hash, _, err := maintenance.ProtectedDigest(f.path, 0755, 64<<20)
		if err != nil || hash != f.hash {
			return errors.New("mixed or changed deployment; recover before starting")
		}
	}
	m, err := Load(owner)
	if err != nil || m != want {
		return errors.New("deployment manifest does not match the permitted pair")
	}
	return nil
}

type maintenanceChild struct {
	cmd         *exec.Cmd
	in          io.WriteCloser
	out         *bufio.Reader
	diagnostics maintenanceOutput
}

func startMaintenanceChild(ctx context.Context, m Manifest, role, image string, args ...string) (*maintenanceChild, error) {
	return startMaintenanceChildWithLock(ctx, m, role, image, nil, args...)
}

func startMaintenanceChildWithLock(ctx context.Context, m Manifest, role, image string, lock *os.File, args ...string) (*maintenanceChild, error) {
	i := m.App
	if role == "signer" {
		i = m.Signer
	} else if role == "owner" {
		a, err := maintenance.ReadAuthority(m.Owner)
		if err != nil {
			return nil, err
		}
		i = Identity{UID: a.Owner, GID: a.Group}
	}
	if i.UID <= 0 || i.GID <= 0 {
		return nil, errors.New("non-root maintenance role required")
	}
	if err := signer.VerifyExecutablePath(image); err != nil {
		return nil, err
	}
	argv := []string{"--reuid", strconv.Itoa(i.UID), "--regid", strconv.Itoa(i.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", image}
	argv = append(argv, args...)
	c := exec.CommandContext(ctx, "/usr/bin/setpriv", argv...)
	if lock != nil {
		c.ExtraFiles = []*os.File{lock}
	}
	c.Dir = "/"
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
	if role == "owner" {
		a, err := maintenance.ReadAuthority(m.Owner)
		if err != nil {
			return nil, err
		}
		c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + a.Home, "XDG_CONFIG_HOME=" + a.Home + "/.config", "LANG=C"}
	}
	x := &maintenanceChild{cmd: c}
	c.Stderr = &x.diagnostics
	in, err := c.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := c.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	x.in = in
	x.out = bufio.NewReaderSize(out, 64<<10)
	if err = c.Start(); err != nil {
		in.Close()
		return nil, err
	}
	return x, nil
}

func (c *maintenanceChild) call(q any, out any) error {
	if err := json.NewEncoder(c.in).Encode(q); err != nil {
		return err
	}
	return c.response(out)
}
func (c *maintenanceChild) once(q any, out any) error {
	if err := json.NewEncoder(c.in).Encode(q); err != nil {
		return err
	}
	if err := c.in.Close(); err != nil {
		return err
	}
	return c.response(out)
}
func (c *maintenanceChild) response(out any) error {
	b, err := c.out.ReadSlice('\n')
	if err != nil {
		return fmt.Errorf("maintenance process response unavailable: %w: %s", err, c.diagnostics.String())
	}
	if err = update.StrictJSON(b, out); err != nil {
		return err
	}
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &failure) == nil && failure.Error != "" {
		return errors.New(failure.Error)
	}
	return nil
}
func (c *maintenanceChild) close() error {
	c.in.Close()
	if err := c.cmd.Wait(); err != nil {
		return fmt.Errorf("maintenance process failed: %w: %s", err, c.diagnostics.String())
	}
	return nil
}

func readMaintenanceRole(ctx context.Context, m Manifest, role, image string, args []string, out any) error {
	c, err := startMaintenanceChild(ctx, m, role, image, args...)
	if err != nil {
		return err
	}
	c.in.Close()
	b, err := c.out.ReadSlice('\n')
	if err == nil {
		err = update.StrictJSON(b, out)
	}
	return errors.Join(err, c.close())
}

// RunProtectedUpdate owns one exact transaction. Boot recovery stops at a
// verified ready pair so systemd can start dependencies before reconciliation.
func RunProtectedUpdate(ctx context.Context, owner int, id string, boot bool) (result error) {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return err
	}
	return runProtectedUpdate(ctx, a, owner, id, boot, "", nil)
}

func runProtectedUpdate(ctx context.Context, a maintenance.Authority, owner int, id string, boot bool, successorImage string, lock *os.File) (result error) {
	successor := successorImage != ""
	r, err := update.ReadMaintenanceRecord(owner)
	if err != nil {
		return err
	}
	if err = validateProtectedUpdateRecord(r, id); err != nil {
		return err
	}
	base, target, err := protectedRecordManifests(r)
	if err != nil {
		return err
	}
	if successorImage == "" {
		if err = verifyProtectedLoadedUnits(ctx, a, base); err != nil {
			return err
		}
		successorImage = maintenance.ImagePath(a.ImageSHA256)
	} else {
		if err = verifySuccessorRootUnits(ctx, a, id, successorImage); err != nil {
			return err
		}
		if err = verifyProtectedLoadedServices(ctx, a, base); err != nil {
			return err
		}
	}
	if r.Phase == "completed" {
		if err = verifyProtectedPair(target); err != nil {
			return err
		}
		marker := base.Root + "/update-active.json"
		_, markerErr := os.Lstat(marker)
		if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
			return markerErr
		}
		if boot {
			// The app cannot answer while this boot service precedes it. Queue
			// the same worker after the services to validate any remaining fence.
			units := []string{Unit(owner, "signer"), Unit(owner, "app")}
			if markerErr == nil || successor {
				units = append(units, protectedWorkerUnit(owner, id))
			}
			_, err = protectedSystemctl(ctx, append([]string{"start", "--no-block", "--"}, units...)...)
			return err
		}
		if markerErr == nil {
			if r.Installation == nil || !update.ValidInstallation(*r.Installation) || r.Installation.Phase != "completed" {
				return errors.New("actual completed signer receipt required before fence cleanup")
			}
			var pause update.CorePauseReceipt
			err = protectedAppPeerImage(ctx, a, target, successorImage, lock, maintenance.AppMaintenanceRequest{Action: "pause", OperationID: id, Pause: &update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()}}, &pause)
			if err != nil || !update.SameReleasedPause(id, r.Installation.Binding.Index.Binding.Pause, pause) {
				return errors.New("original core pause is not released; preserve the fence")
			}
			if err = maintenance.RemoveProtected(marker, sum(update.MaintenanceFence(id)), 0644); err != nil {
				return err
			}
		}
		if successor {
			return reconcileProtectedSuccessor(ctx, a, r, target, successorImage, lock)
		}
		return nil
	}
	if boot && r.Phase == "base-recovery" && r.Start == "base" {
		if err = verifyProtectedPair(base); err != nil {
			return err
		}
		_, err = protectedSystemctl(ctx, "start", "--no-block", "--", Unit(owner, "signer"), Unit(owner, "app"))
		return err
	}
	dir := update.MaintenanceDirectory(owner, id)
	defer func() {
		if result == nil {
			return
		}
		fresh, e := update.ReadMaintenanceRecord(owner)
		if e == nil && fresh.OperationID == id && fresh.Start == "" {
			recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			if recoveryErr := recoverProtectedBase(recoveryCtx, a, &fresh, base, dir); recoveryErr != nil {
				result = errors.Join(result, fmt.Errorf("original pair remains held: %w", recoveryErr))
			}
		}
	}()
	if err = maintenance.PublishProtected(base.Root+"/update-active.json", update.MaintenanceFence(r.OperationID), 0644); err != nil {
		return err
	}
	if r.Start != "" {
		r.Start = ""
		r.Phase = "quiescing"
		if err = storeProtectedRecord(r); err != nil {
			return err
		}
	}
	// During successor boot recovery these units are ordered after this service.
	// A synchronous stop can cancel or deadlock their queued starts. Refuse an
	// unexpectedly active process and leave the original operation fenced.
	if !boot || !successor {
		if _, err = protectedSystemctl(ctx, "stop", "--", Unit(owner, "app"), Unit(owner, "signer")); err != nil {
			return err
		}
	}
	if err = servicesStopped(ctx, base, func(ctx context.Context, _ []byte, args ...string) ([]byte, error) {
		return protectedSystemctl(ctx, args[1:]...)
	}); err != nil {
		return err
	}
	if r.Installation == nil {
		if err = prepareProtectedHold(ctx, a, &r, base, target, dir); err != nil {
			return err
		}
	} else {
		var actual signer.Response
		if err = readMaintenanceRole(ctx, base, "signer", dir+"/fased-signer", []string{"custody-maintenance-status", "--owner", strconv.Itoa(owner), "--operation", id}, &actual); err != nil {
			return err
		}
		if actual.Installation == nil || !update.ValidInstallation(*actual.Installation) || actual.Installation.Digest != r.Installation.Digest {
			return errors.New("actual signer installation differs; preserve recovery")
		}
		r.Installation = actual.Installation
		if err = storeProtectedRecord(r); err != nil {
			return err
		}
	}
	if r.Installation == nil || !update.ValidInstallation(*r.Installation) {
		return errors.New("actual held installation required")
	}
	if r.Installation.Phase != "active" && r.Installation.Phase != "healthy-held" && r.Installation.Phase != "committed-held" && r.Installation.Phase != "completed" {
		return errors.New("signer direction does not authorize the target; preserve the actual hold")
	}
	if r.Installation.Phase != "completed" {
		if err = verifyProtectedRecovery(ctx, r, base, dir); err != nil {
			return err
		}
	}
	r.Start = ""
	r.Phase = "replacing"
	if err = storeProtectedRecord(r); err != nil {
		return err
	}
	for _, c := range r.Package.Components {
		previous := base.CoreHash
		if c.Contract.Component == "signer" {
			previous = base.SignerHash
		}
		if err = maintenance.CopyProtected(filepath.Join(dir, c.Artifact.File), filepath.Join(base.Root, c.Artifact.File), 0755, c.Artifact.SHA256, c.Artifact.Size, previous); err != nil {
			return err
		}
	}
	if err = maintenance.ReplaceProtected(base.Root+"/install.json", mustJSON(target), 0644, sum(mustJSON(base))); err != nil {
		return err
	}
	if err = verifyProtectedPair(target); err != nil {
		return err
	}
	r.Start = "target"
	r.Phase = "ready"
	if err = storeProtectedRecord(r); err != nil {
		return err
	}
	if boot {
		_, err = protectedSystemctl(ctx, "start", "--no-block", "--", protectedWorkerUnit(owner, id))
		return err
	}
	if _, err = protectedSystemctl(ctx, "reset-failed", "--", Unit(owner, "app"), Unit(owner, "signer")); err != nil {
		return err
	}
	if _, err = protectedSystemctl(ctx, "start", "--", Unit(owner, "signer"), Unit(owner, "app")); err != nil {
		return err
	}
	if err = completeProtectedUpdate(ctx, a, &r, target, successorImage, lock); err != nil {
		return err
	}
	if successor {
		return reconcileProtectedSuccessor(ctx, a, r, target, successorImage, lock)
	}
	return nil
}

func validateProtectedUpdateRecord(r update.MaintenanceRecord, id string) error {
	if r.OperationID != id || r.Package == nil || r.Phase == "idle" {
		return errors.New("exact pending package required")
	}
	return nil
}

func verifyProtectedPair(m Manifest) error {
	actual, err := Load(m.Owner)
	if err != nil || actual != m {
		return errors.New("protected manifest differs from selected deployment")
	}
	for _, c := range []struct{ path, hash string }{{m.Root + "/fased-app", m.CoreHash}, {m.Root + "/fased-signer", m.SignerHash}} {
		hash, _, err := maintenance.ProtectedDigest(c.path, 0755, 64<<20)
		if err != nil || hash != c.hash {
			return errors.New("protected deployment pair differs")
		}
	}
	return nil
}

func prepareProtectedHold(ctx context.Context, a maintenance.Authority, r *update.MaintenanceRecord, base, target Manifest, dir string) (result error) {
	app, err := startMaintenanceChild(ctx, base, "app", dir+"/fased-app", "custody-maintenance", "--owner", strconv.Itoa(base.Owner), "--operation", r.OperationID)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, app.close()) }()
	request := func(action string) update.MaintenanceCommand {
		return update.MaintenanceCommand{Action: action, OperationID: r.OperationID, ProbeID: update.NewProbe()}
	}
	probe := update.NewProbe()
	q := request("inventory")
	q.ProbeID = probe
	var metadata update.MetadataMaintenanceReply
	if err = app.call(q, &metadata); err != nil || metadata.Snapshot == nil {
		return errors.Join(err, errors.New("actual cold app snapshot required"))
	}
	var initial signer.Response
	if err = readMaintenanceRole(ctx, base, "signer", dir+"/fased-signer", []string{"custody-maintenance-inventory", "--owner", strconv.Itoa(base.Owner), "--probe", probe}, &initial); err != nil {
		return err
	}
	if initial.UpdateInventory == nil || initial.UpdateInventory.ActiveIntentID != "" && initial.UpdateInventory.ActiveIntentID != r.OperationID {
		return errors.New("another original signer intent remains authoritative; preserve it")
	}
	if r.Bootstrap {
		var reply update.MetadataMaintenanceReply
		if err = app.call(request("initialize"), &reply); err != nil {
			return err
		}
	}
	if r.Approval == nil {
		if existing, e := update.ReadMaintenanceApproval(base.Owner, r.OperationID); e == nil {
			r.Approval = &existing
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		} else {
			p := r.Package
			now := time.Now().Unix()
			approval := update.MaintenanceApproval{Version: 1, Owner: base.Owner, InstallationID: base.ID, OperationID: r.OperationID, HelperSHA256: a.ImageSHA256, Source: p.Source, ProductVersion: p.Version, ReleaseSHA256: p.ReleaseSHA256, ProvenanceSHA256: p.ProvenanceSHA256, DevelopmentOnly: p.DevelopmentOnly, PreviousCore: base.CoreHash, PreviousSigner: base.SignerHash, PreviousManifest: sum(mustJSON(base)), Core: *metadata.Snapshot, Plan: update.Manifest{Version: 1, Product: "fased-app-maintenance", Channel: "protected-installer", Sequence: p.Sequence, IssuedAt: now, ExpiresAt: now + 24*60*60, Target: update.Target{OS: "linux", Arch: "amd64"}, Components: p.Components}}
			if err = approval.Validate(); err != nil {
				return err
			}
			inv, err := update.JoinSnapshots(approval.Core, *initial.UpdateInventory, approval.Plan.Target)
			if err != nil {
				return err
			}
			if _, err = update.CompatibleMaintenance(approval.Plan, approval.Digest(), inv); err != nil {
				return err
			}
			if err = maintenance.PublishProtected(dir+"/approval.json", mustJSON(approval), 0644); err != nil {
				return err
			}
			r.Approval = &approval
		}
		if err = storeProtectedRecord(*r); err != nil {
			return err
		}
	}
	if !update.SameCoreMetadata(*metadata.Snapshot, r.Approval.Core) {
		return errors.New("original metadata changed; preserve the pending maintenance")
	}
	sig, err := startMaintenanceChild(ctx, base, "signer", dir+"/fased-signer", "custody-maintenance", "--owner", strconv.Itoa(base.Owner), "--operation", r.OperationID)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, sig.close()) }()
	if r.Staged == nil {
		var reply signer.Response
		if err = sig.call(request("prepare"), &reply); err != nil {
			return err
		}
		q = request("stage")
		q.Core = metadata.Snapshot
		if err = sig.call(q, &reply); err != nil || reply.UpdateIntent == nil || reply.Stage == nil || !reply.Stage.Verified {
			return errors.Join(err, errors.New("actual completed signer staging required"))
		}
		r.Staged = reply.UpdateIntent
		r.Phase = "staged"
		if err = storeProtectedRecord(*r); err != nil {
			return err
		}
	}
	var paused update.MetadataMaintenanceReply
	if err = app.call(request("pause"), &paused); err != nil || paused.Pause == nil {
		return errors.Join(err, errors.New("actual original metadata pause required"))
	}
	q = request("preflight")
	q.Pause = paused.Pause
	var verified signer.Response
	if err = sig.call(q, &verified); err != nil || verified.Preflight == nil {
		return errors.Join(err, errors.New("actual signer preflight required"))
	}
	if r.Recovery == nil {
		q = request("backup")
		q.Preflight = verified.Preflight
		var backup update.MetadataMaintenanceReply
		if err = app.call(q, &backup); err != nil || backup.Recovery == nil {
			return errors.Join(err, errors.New("actual metadata backup required"))
		}
		r.Recovery = backup.Recovery
		if err = storeProtectedRecord(*r); err != nil {
			return err
		}
	}
	binding := update.InstallBinding{Version: 1, Index: *r.Recovery, InstallationID: base.ID, PreviousCore: base.CoreHash, PreviousSigner: base.SignerHash, PreviousManifest: sum(mustJSON(base)), TargetCore: target.CoreHash, TargetSigner: target.SignerHash}
	for _, c := range r.Package.Components {
		binding.Contracts = append(binding.Contracts, c.Contract)
	}
	if err = update.ValidateInstallBinding(binding); err != nil {
		return err
	}
	q = request("verify")
	q.Preflight = verified.Preflight
	q.Binding = &binding
	var backup update.MetadataMaintenanceReply
	if err = app.call(q, &backup); err != nil {
		return err
	}
	q = request("install")
	q.Phase = "active"
	q.Pause = paused.Pause
	q.Binding = &binding
	if err = sig.call(q, &verified); err != nil || verified.Installation == nil || !update.ValidInstallation(*verified.Installation) || verified.Installation.Digest != update.InstallDigest(binding) {
		return errors.Join(err, errors.New("actual signer installation hold required"))
	}
	r.Installation = verified.Installation
	r.Phase = "held"
	t := InstallTransaction{Version: 1, Base: base, Target: target, Binding: binding}
	if !t.valid() {
		return errors.New("canonical maintenance transaction differs")
	}
	if err = maintenance.EnsureProtectedDirectory(base.Root + "/updates"); err != nil {
		return err
	}
	if err = maintenance.EnsureProtectedDirectory(t.dir()); err != nil {
		return err
	}
	if err = maintenance.PublishProtected(t.dir()+"/transaction.json", mustJSON(t), 0644); err != nil {
		return err
	}
	return storeProtectedRecord(*r)
}

func verifyProtectedRecovery(ctx context.Context, r update.MaintenanceRecord, base Manifest, dir string) error {
	var held signer.Response
	if err := readMaintenanceRole(ctx, base, "signer", dir+"/fased-signer", []string{"custody-maintenance-status", "--owner", strconv.Itoa(base.Owner), "--operation", r.OperationID}, &held); err != nil {
		return err
	}
	if held.Installation == nil || !update.ValidInstallation(*held.Installation) || held.Installation.Digest != r.Installation.Digest {
		return errors.New("actual cold signer hold differs from original backup")
	}
	// Recover committed WAL under the real AppUID and instance lock. The
	// following read-only verifier still requires the exact original backup.
	app, err := startMaintenanceChild(ctx, base, "app", dir+"/fased-app", "custody-maintenance", "--owner", strconv.Itoa(base.Owner), "--operation", r.OperationID)
	if err != nil {
		return err
	}
	var paused update.MetadataMaintenanceReply
	err = app.call(update.MaintenanceCommand{Action: "status", OperationID: r.OperationID, ProbeID: update.NewProbe()}, &paused)
	if err == nil && (paused.Pause == nil || paused.Pause.State != "paused" || r.Recovery == nil || paused.Pause.PauseID != r.Recovery.Binding.Pause.PauseID) {
		err = errors.New("original committed metadata pause differs")
	}
	err = errors.Join(err, app.close())
	if err != nil {
		return err
	}
	var verified update.MetadataMaintenanceReply
	if err := readMaintenanceRole(ctx, base, "app", dir+"/fased-app", []string{"custody-maintenance-verify", "--owner", strconv.Itoa(base.Owner), "--operation", r.OperationID}, &verified); err != nil {
		return err
	}
	return nil
}

func completeProtectedUpdate(ctx context.Context, a maintenance.Authority, r *update.MaintenanceRecord, target Manifest, appImage string, lock *os.File) error {
	// Implemented below with fixed live owner/app peers. No root code opens
	// a vault or a database, and no supplied receipt can advance the floor.
	return completeProtectedInstallation(ctx, a, r, target, appImage, lock)
}

// Only an untouched, uncommitted Base is an automatic failure escape. Its
// reservation, pause, backup and holds remain visible for explicit recovery.
func recoverProtectedBase(ctx context.Context, a maintenance.Authority, r *update.MaintenanceRecord, base Manifest, dir string) error {
	if err := verifyProtectedPair(base); err != nil {
		return err
	}
	if err := servicesStopped(ctx, base, func(ctx context.Context, _ []byte, args ...string) ([]byte, error) {
		return protectedSystemctl(ctx, args[1:]...)
	}); err != nil {
		return err
	}
	var sig signer.Response
	if err := readMaintenanceRole(ctx, base, "signer", dir+"/fased-signer", []string{"custody-maintenance-inventory", "--owner", strconv.Itoa(base.Owner), "--probe", update.NewProbe()}, &sig); err != nil {
		return err
	}
	if sig.UpdateInventory == nil || sig.UpdateInventory.ActiveIntentID != "" && (sig.UpdateInventory.ActiveIntentID != r.OperationID || sig.UpdateIntent == nil || r.Approval == nil || sig.UpdateIntent.ManifestSHA256 != r.Approval.Digest()) {
		return errors.New("unclassified original signer state")
	}
	if sig.Installation != nil && (sig.Installation.InstalledSequenceCommitted || sig.Installation.Phase != "active" && sig.Installation.Phase != "healthy-held" && sig.Installation.Phase != "rolled-back-held") {
		return errors.New("committed signer cannot restart an older pair")
	}
	app, err := startMaintenanceChild(ctx, base, "app", dir+"/fased-app", "custody-maintenance", "--owner", strconv.Itoa(base.Owner), "--operation", r.OperationID)
	if err != nil {
		return err
	}
	var metadata update.MetadataMaintenanceReply
	err = app.call(update.MaintenanceCommand{Action: "inventory", OperationID: r.OperationID, ProbeID: update.NewProbe()}, &metadata)
	err = errors.Join(err, app.close())
	if err != nil {
		return err
	}
	if metadata.Snapshot == nil || r.Approval != nil && !update.SameCoreMetadata(*metadata.Snapshot, r.Approval.Core) {
		return errors.New("original core metadata changed")
	}
	r.Start = "base"
	r.Phase = "base-recovery"
	if err = storeProtectedRecord(*r); err != nil {
		return err
	}
	_, err = protectedSystemctl(ctx, "start", "--no-block", "--", Unit(base.Owner, "signer"), Unit(base.Owner, "app"))
	return err
}
