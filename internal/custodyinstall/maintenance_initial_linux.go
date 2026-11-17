//go:build linux

package custodyinstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// ValidateInitialMaintenanceRecord recognizes an idle, independently verified
// installed pair. It never converts a pending update or signer hold to idle.
func ValidateInitialMaintenanceRecord(r update.MaintenanceRecord) (Manifest, error) {
	var empty Manifest
	if err := r.ValidateIdle(); err != nil {
		return empty, err
	}
	base, target, err := protectedRecordManifests(r)
	if err != nil || base.Layout != "" || base != target || base.Version != 3 || !base.Enrollment || r.OperationID != base.ID {
		return empty, errors.New("initial custody installation identity differs")
	}
	if base.Provenance == signer.OwnerKeyProvenance && r.Package.DevelopmentOnly {
		return empty, errors.New("development release cannot initialize owner custody")
	}
	for _, c := range r.Package.Components {
		switch c.Contract.Component {
		case "core":
			if c.Artifact.SHA256 != base.CoreHash {
				return empty, errors.New("initial core differs from the installed pair")
			}
		case "signer":
			if c.Artifact.SHA256 != base.SignerHash {
				return empty, errors.New("initial signer differs from the installed pair")
			}
		}
	}
	if !base.NativeOwnerAccess && r.Package.NativeDesktop != nil {
		return empty, errors.New("headless custody cannot own a Local desktop")
	}
	return base, nil
}

// ValidateInitialPackageMaintenanceRecord checks the shape and identities of
// an initial record. Its caller must independently authenticate the package
// and verify signer state; this check alone cannot create or clear a hold.
func ValidateInitialPackageMaintenanceRecord(r update.MaintenanceRecord, owner int, location, instance string) (Manifest, error) {
	m, err := ValidateIdlePackageMaintenanceRecord(r, owner, location, instance)
	if err != nil || m.Network != "" {
		return Manifest{}, errors.New("initial package installation must have an unconfigured signer")
	}
	return m, nil
}

// ValidateIdlePackageMaintenanceRecord also accepts an already configured
// signer. Creating an initial journal still requires the empty-signer check.
func ValidateIdlePackageMaintenanceRecord(r update.MaintenanceRecord, owner int, location, instance string) (Manifest, error) {
	var base, target Manifest
	if r.ValidateIdle() != nil || r.Owner != owner || r.OperationID != instance ||
		strict(r.Base, &base) != nil || strict(r.Target, &target) != nil || base != target ||
		base.validatePackage() != nil || base.Owner != owner || base.Location != location || base.ID != instance {
		return Manifest{}, errors.New("initial package installation identity differs")
	}
	if base.Provenance == signer.OwnerKeyProvenance && r.Package.DevelopmentOnly {
		return Manifest{}, errors.New("development release cannot initialize owner custody")
	}
	for _, component := range r.Package.Components {
		if component.Contract.Component == "core" && component.Artifact.SHA256 != base.CoreHash ||
			component.Contract.Component == "signer" && component.Artifact.SHA256 != base.SignerHash {
			return Manifest{}, errors.New("initial package core or signer differs")
		}
	}
	if r.Package.NativeDesktop != nil && base.Location != "local" {
		return Manifest{}, errors.New("a Local wallet client cannot belong to a Remote package")
	}
	return base, nil
}

// InitializeProtectedPackageJournal is called by the fixed helper while its
// instance lock is held, after the helper independently authenticates p. It
// publishes only an empty-signer initial record, never an existing hold.
func InitializeProtectedPackageJournal(ctx context.Context, owner int, location, instance string, p update.MaintenancePackage) error {
	a, err := maintenance.ValidatePackageProcess(owner, location, instance)
	if err != nil {
		return err
	}
	root, err := PackageRoot(owner, location, instance)
	if err != nil {
		return err
	}
	m, err := LoadPackageInstance(owner, root)
	if err != nil || m.ID != instance || m.Location != location || m.Network != "" || VerifyPackageInstance(m) != nil {
		return errors.New("fresh unconfigured package instance required")
	}
	helper, _, err := maintenance.ProtectedDigest(root+"/fased-install", 0755, 128<<20)
	if err != nil || helper != a.ImageSHA256 {
		return errors.New("the instance gate differs from its pinned helper")
	}
	r := update.MaintenanceRecord{Version: 1, Owner: owner, OperationID: instance, Phase: "idle", Start: "base", Base: mustJSON(m), Target: mustJSON(m), Package: &p}
	if _, err = ValidateInitialPackageMaintenanceRecord(r, owner, location, instance); err != nil {
		return err
	}
	if p.NativeDesktop != nil {
		if err = verifyProtectedDesktop(m, &p); err != nil {
			return err
		}
	}
	old, err := update.ReadPackageMaintenanceRecord(owner, location, instance)
	if err == nil {
		if _, err = ValidateInitialPackageMaintenanceRecord(old, owner, location, instance); err != nil || update.SnapshotDigest("initial-package", old) != update.SnapshotDigest("initial-package", r) {
			return errors.New("existing package operation differs; preserve it")
		}
		if err = VerifyUnconfiguredPackageSigner(ctx, m); err != nil {
			return err
		}
		for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
			if _, err = os.Lstat(root + "/" + name); !errors.Is(err, os.ErrNotExist) {
				return errors.New("existing package recovery fence must be preserved")
			}
		}
		if err = verifyLoadedPackageServices(ctx, m); err != nil {
			return err
		}
		return verifyPackageRootUnits(ctx, a, m, instance)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = servicesStopped(ctx, m, func(ctx context.Context, _ []byte, args ...string) ([]byte, error) {
		return protectedSystemctl(ctx, args[1:]...)
	}); err != nil {
		return err
	}
	if err = VerifyUnconfiguredPackageSigner(ctx, m); err != nil {
		return err
	}
	for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
		if _, err = os.Lstat(root + "/" + name); !errors.Is(err, os.ErrNotExist) {
			return errors.New("existing package recovery fence must be preserved")
		}
	}
	if err = installPackageMaintenanceUnits(ctx, a, m); err != nil {
		return err
	}
	if err = verifyLoadedPackageServices(ctx, m); err != nil {
		return err
	}
	journal, err := update.PackageMaintenanceRoot(owner, location, instance)
	if err != nil {
		return err
	}
	return maintenance.PublishProtected(journal+"/custody.json", mustJSON(r), 0644)
}

// VerifyUnconfiguredPackageSigner is read-only. A missing wallet network in a
// manifest is not evidence that the real signer is stopped or has no state.
// The fixed helper calls this before initial journal creation and boot entry.
func VerifyUnconfiguredPackageSigner(ctx context.Context, m Manifest) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("protected package recovery required")
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return verifyUnconfiguredPackageSigner(bounded, m, protectedSystemctl, emptySignerDirectory)
}

func verifyUnconfiguredPackageSigner(ctx context.Context, m Manifest, systemctl func(context.Context, ...string) ([]byte, error), inspect func(string, int, int) error) error {
	if m.validatePackage() != nil || m.Network != "" || systemctl == nil || inspect == nil {
		return errors.New("exact unconfigured package signer required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := systemctl(ctx, "show", instanceUnit(m, "signer"), "--property=ActiveState,MainPID,ControlPID")
	if err != nil {
		return errors.New("signer state is unavailable; preserve it")
	}
	fields := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return errors.New("signer service state is ambiguous; preserve it")
		}
		if _, exists := fields[key]; exists {
			return errors.New("signer service state is ambiguous; preserve it")
		}
		fields[key] = value
	}
	if len(fields) != 3 || fields["ActiveState"] != "inactive" || fields["MainPID"] != "0" || fields["ControlPID"] != "0" {
		return errors.New("signer must be stopped before empty-state recovery")
	}
	if err = inspect(m.Root+"/signer-state", m.Signer.UID, m.Signer.GID); err != nil {
		return err
	}
	return ctx.Err()
}

func emptySignerDirectory(path string, uid, gid int) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("signer state is unavailable; preserve it")
	}
	f := os.NewFile(uintptr(fd), "package-signer-state")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || int(st.Uid) != uid || int(st.Gid) != gid || st.Mode&07777 != 0700 {
		return errors.New("signer state identity differs; preserve it")
	}
	entries, err := f.ReadDir(1)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) != 0 {
		return errors.New("existing signer state must be preserved")
	}
	return nil
}

// Verify the actual loaded systemd commands, not just a matching unit file.
func verifyLoadedPackageServices(ctx context.Context, m Manifest) error {
	for _, role := range []string{"app", "signer"} {
		definition := definitions(m)["fased-"+role+".service"]
		if _, err := loadedUnitPolicyFields(m, role, definition, m.Root+"/fased-"+role); err != nil {
			return err
		}
		raw, err := atomicUnitServiceProperties(ctx, instanceUnit(m, role))
		if err != nil {
			return err
		}
		var snapshot struct {
			Type string                       `json:"type"`
			Data []map[string]json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &snapshot) != nil || snapshot.Type != "a{sv}" || len(snapshot.Data) != 1 {
			return errors.New("actual package service commands required")
		}
		for _, property := range []string{"ExecCondition", "ExecStartPre", "ExecStart", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload"} {
			var expected []atomicPairCommand
			if property == "ExecStartPre" || property == "ExecStart" {
				for _, line := range strings.Split(string(definition), "\n") {
					if strings.HasPrefix(line, property+"=") {
						expected = append(expected, atomicPairCommand{argv: strings.Fields(strings.TrimPrefix(line, property+"="))})
					}
				}
				if len(expected) != 1 {
					return errors.New("one exact package gate and start command required")
				}
			}
			if err = validateAtomicCommandRows(snapshot.Data[0][property], expected); err != nil {
				return fmt.Errorf("loaded package %s %s: %w", role, property, err)
			}
		}
		if commands, ok := snapshot.Data[0]["ExecReloadPost"]; ok && validateAtomicCommandRows(commands, nil) != nil {
			return errors.New("unreviewed package service execution is present")
		}
	}
	return nil
}

func verifyProtectedDesktop(m Manifest, p *update.MaintenancePackage) error {
	if !(m.NativeOwnerAccess || m.Layout == PackageLayout && m.Location == "local") || p == nil || p.Validate() != nil || p.NativeDesktop == nil {
		return errors.New("protected Local desktop package required")
	}
	if m.Layout == PackageLayout && m.validatePackage() != nil {
		return errors.New("exact protected Local package required")
	}
	hash, size, err := maintenance.ProtectedDigest(m.Root+"/fased-desktop", 0755, 64<<20)
	if err != nil || hash != p.NativeDesktop.SHA256 || size != p.NativeDesktop.Size {
		return errors.New("protected desktop differs from its release")
	}
	return nil
}

// VerifyProtectedDesktop checks the exact native artifact in a verified
// release; it grants no start, update, or signing authority.
func VerifyProtectedDesktop(m Manifest, p *update.MaintenancePackage) error {
	return verifyProtectedDesktop(m, p)
}

// ProtectedPackageNativeRelease selects the owner client from this exact
// package journal. No browser-provided path or account grants this authority.
func ProtectedPackageNativeRelease(m Manifest, workspace string) (*update.MaintenancePackage, error) {
	if m.validatePackage() != nil || m.Owner <= 0 || os.Getuid() != m.Owner || os.Geteuid() != m.Owner || m.Location != "local" || !m.Enrollment {
		return nil, errors.New("original Local package owner required")
	}
	a, err := maintenance.ReadPackageAuthority(m.Owner, m.Location, m.ID)
	if err != nil || maintenance.ValidateOwnerIdentity(a) != nil || maintenance.VerifyImage(a) != nil {
		return nil, errors.New("verified package maintenance authority required")
	}
	helper, _, err := maintenance.ProtectedDigest(m.Root+"/fased-install", 0755, 128<<20)
	if err != nil || helper != a.ImageSHA256 {
		return nil, errors.New("package maintenance gate differs from its authority")
	}
	for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
		if _, e := os.Lstat(m.Root + "/" + name); !errors.Is(e, os.ErrNotExist) {
			return nil, errors.New("finish the existing package update first")
		}
	}
	r, err := update.ReadPackageMaintenanceRecord(m.Owner, m.Location, m.ID)
	if err != nil {
		return nil, err
	}
	var instance string
	switch r.Phase {
	case "idle", "completed":
		selected, e := SelectProtectedPackageStart(r, m.Owner, m.Location, m.ID)
		if e != nil || selected != m || VerifyPackageOwnerTarget(m) != nil {
			return nil, errors.New("package owner differs from the completed installation")
		}
		commit, e := ReadEnrollmentCommit(m)
		if e != nil {
			return nil, e
		}
		instance = commit.InstanceID
	case "wallet-initializing":
		base, target, e := ValidatePackageWalletInitialization(r, m.Owner, m.Location, m.ID)
		if e != nil || m != base && m != target {
			return nil, errors.New("original package wallet recovery required")
		}
		_, pending, e := PendingPackageWallet(m.Owner, m.Location, m.ID)
		if e != nil || pending == nil || *pending != *r.Wallet || inspectPackageWalletFiles(packageWalletFiles(base, target, pending.InstanceID), base.Root+"/wallet-configured.json", packageWalletMarker(target, pending.InstanceID), readRoot) != nil {
			return nil, errors.New("package wallet recovery differs; preserve it")
		}
		instance = pending.InstanceID
	default:
		return nil, errors.New("finish the original package recovery before Wallets")
	}
	if workspace != "" && workspace != instance {
		return nil, errors.New("wallet request belongs to another workspace")
	}
	p, err := r.InstalledPackage()
	if err != nil || verifyProtectedDesktop(m, p) != nil {
		return nil, errors.New("verified package wallet client required")
	}
	return p, nil
}

// ProtectedNativeRelease is the owner-side read-only view of the same durable
// operation. An unfinished update never selects a desktop for Wallets.
func ProtectedNativeRelease(m Manifest) (*update.MaintenancePackage, error) {
	if m.Owner <= 0 || os.Getuid() != m.Owner || os.Geteuid() != m.Owner || !m.NativeOwnerAccess || !m.Enrollment || m.Location != "local" {
		return nil, errors.New("original Local owner required")
	}
	a, err := maintenance.ReadAuthority(m.Owner)
	if err != nil || a.Profile != "local" || maintenance.VerifyImage(a) != nil || Verify(m) != nil {
		return nil, errors.New("verified Local maintenance installation required")
	}
	if _, err = ReadEnrollmentCommit(m); err != nil {
		return nil, err
	}
	for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
		if _, e := os.Lstat(m.Root + "/" + name); !errors.Is(e, os.ErrNotExist) {
			return nil, errors.New("finish the existing custody update first")
		}
	}
	r, err := update.ReadMaintenanceRecord(m.Owner)
	if err != nil {
		return nil, err
	}
	switch r.Phase {
	case "idle":
		base, e := ValidateInitialMaintenanceRecord(r)
		if e != nil || base != m {
			return nil, errors.New("initial native installation differs")
		}
	case "completed":
		var target Manifest
		if r.Start != "target" || update.StrictJSON(r.Target, &target) != nil || target != m || r.Installation == nil || r.Installation.Phase != "completed" || !update.ValidInstallation(*r.Installation) || r.Installation.Binding.InstallationID != m.ID || r.Installation.Binding.TargetCore != m.CoreHash || r.Installation.Binding.TargetSigner != m.SignerHash {
			return nil, errors.New("the original signer update is not complete")
		}
	default:
		return nil, errors.New("finish the existing custody update first")
	}
	p, err := r.InstalledPackage()
	if err != nil {
		return nil, err
	}
	for _, c := range p.Components {
		if c.Contract.Component == "core" && c.Artifact.SHA256 != m.CoreHash || c.Contract.Component == "signer" && c.Artifact.SHA256 != m.SignerHash {
			return nil, errors.New("native release differs from the installed pair")
		}
	}
	if err = verifyProtectedDesktop(m, p); err != nil {
		return nil, err
	}
	return p, nil
}

// VerifyInitialProtectedDeployment is read-only. A foreign fence remains a
// refusal even when an otherwise valid initial journal exists.
func VerifyInitialProtectedDeployment(owner int) error {
	a, err := maintenance.ReadAuthority(owner)
	if err != nil {
		return err
	}
	if err = maintenance.VerifyImage(a); err != nil {
		return err
	}
	r, err := update.ReadMaintenanceRecord(owner)
	if err != nil {
		return err
	}
	m, err := ValidateInitialMaintenanceRecord(r)
	if err != nil || m.Location != a.Profile {
		return errors.New("initial journal does not identify the installed profile")
	}
	if err = verifyProtectedPair(m); err != nil {
		return err
	}
	if r.Package.NativeDesktop != nil {
		if err = verifyProtectedDesktop(m, r.Package); err != nil {
			return err
		}
	}
	if _, err = ReadEnrollmentCommit(m); err != nil {
		return err
	}
	for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
		if _, err = os.Lstat(m.Root + "/" + name); !errors.Is(err, os.ErrNotExist) {
			return errors.New("existing custody update fence must be preserved")
		}
	}
	return nil
}

// InitializeProtectedJournal is called only by the protected helper while its
// owner operation lock is held, with a package independently verified by that
// helper. It never replaces an existing transaction or an occupied signer.
func InitializeProtectedJournal(ctx context.Context, owner int, p update.MaintenancePackage) error {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return err
	}
	m, err := Load(owner)
	if err != nil || m.Location != a.Profile || m.Version != 3 || !m.Enrollment {
		return errors.New("exact enrolled custody profile required")
	}
	if err = Verify(m); err != nil {
		return err
	}
	if _, err = ReadEnrollmentCommit(m); err != nil {
		return err
	}
	r := update.MaintenanceRecord{Version: 1, Owner: owner, OperationID: m.ID, Phase: "idle", Start: "base", Base: mustJSON(m), Target: mustJSON(m), Package: &p}
	if _, err = ValidateInitialMaintenanceRecord(r); err != nil {
		return err
	}
	old, err := update.ReadMaintenanceRecord(owner)
	if err == nil {
		if _, err = ValidateInitialMaintenanceRecord(old); err != nil || update.SnapshotDigest("initial-maintenance", old) != update.SnapshotDigest("initial-maintenance", r) {
			return errors.New("existing custody journal differs; preserve it")
		}
		if err = VerifyInitialProtectedDeployment(owner); err != nil {
			return err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		for _, role := range []string{"app", "signer"} {
			if e := ensureInitiallyStopped(ctx, Unit(owner, role), protectedSystemctl); e != nil {
				return e
			}
		}
		entries, e := os.ReadDir(m.Root + "/signer-state")
		if e != nil || len(entries) != 0 {
			return errors.New("existing signer state must be preserved")
		}
		for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
			if _, e = os.Lstat(m.Root + "/" + name); !errors.Is(e, os.ErrNotExist) {
				return errors.New("existing custody update fence must be preserved")
			}
		}
		if err = storeProtectedRecord(r); err != nil {
			return err
		}
	} else {
		return err
	}
	// A previous attempt may have written every file and stopped before
	// systemd reloaded or enabled the recovery service. Initial setup must
	// finish those idempotent actions even when the files already match.
	if err = installProtectedMaintenanceUnitsForInitial(ctx, a, m, true); err != nil {
		return err
	}
	return verifyProtectedLoadedUnits(ctx, a, m)
}

func ensureInitiallyStopped(ctx context.Context, unit string, systemctl func(context.Context, ...string) ([]byte, error)) error {
	read := func(property string) (string, error) {
		b, err := systemctl(ctx, "show", "--property="+property, "--value", "--", unit)
		return strings.TrimSpace(string(b)), err
	}
	state, err := read("ActiveState")
	if err != nil {
		return errors.New("custody service state is unavailable; preserve it")
	}
	if state == "inactive" {
		return nil
	}
	if state == "failed" {
		main, mainErr := read("MainPID")
		control, controlErr := read("ControlPID")
		if mainErr == nil && controlErr == nil && main == "0" && control == "0" {
			// Cancel any queued restart before clearing a failed start gate.
			if _, err = systemctl(ctx, "stop", "--", unit); err == nil {
				_, err = systemctl(ctx, "reset-failed", "--", unit)
			}
			if err == nil {
				state, err = read("ActiveState")
				if err == nil && state == "inactive" {
					return nil
				}
			}
		}
	}
	return errors.New("custody services must be inactive before first journal initialization")
}
