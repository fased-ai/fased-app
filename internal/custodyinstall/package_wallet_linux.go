//go:build linux

package custodyinstall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// CheckPackageWalletSigner binds admission to the authenticated installed
// signer's recorded contract, not the older pinned helper's own build.
func CheckPackageWalletSigner(m Manifest, p *update.MaintenancePackage) error {
	if m.validatePackage() != nil || p == nil || !packagePairMatches(*p, m) {
		return signer.ErrUnavailable
	}
	feature := update.OwnerKeyAdmissionFeature
	if m.Provenance == signer.ControlledCustodyProfile {
		feature = hostplatform.FixturePackageSignerFeature()
		if feature == "" {
			return signer.ErrUnavailable
		}
	}
	for _, c := range p.Components {
		if c.Contract.Component == "signer" && c.Artifact.SHA256 == m.SignerHash && slices.Contains(c.Contract.Features, feature) {
			return nil
		}
	}
	return signer.ErrUnavailable
}

// ValidatePackageWalletInitialization keeps the original installation and release in
// the same protected journal while selecting a network for an empty signer.
func ValidatePackageWalletInitialization(r update.MaintenanceRecord, owner int, location, instance string) (Manifest, Manifest, error) {
	if r.Phase != "wallet-initializing" || r.Start != "" || r.Wallet == nil || !r.Wallet.Valid() {
		return Manifest{}, Manifest{}, errors.New("original wallet initialization required")
	}
	idle := r
	idle.Phase, idle.Start, idle.Wallet = "idle", "base", nil
	base, err := ValidateInitialPackageMaintenanceRecord(idle, owner, location, instance)
	if err != nil {
		return Manifest{}, Manifest{}, err
	}
	target := base
	target.Network = r.Wallet.Network
	if target.validatePackage() != nil {
		return Manifest{}, Manifest{}, errors.New("invalid wallet network for this package")
	}
	return base, target, nil
}

// A completed empty-signer update can become the starting point for Wallets
// only after the caller has retained its original protected receipt.
func walletInitialRecord(r update.MaintenanceRecord, owner int, location, instance string) (update.MaintenanceRecord, error) {
	if r.Phase == "idle" {
		m, err := ValidateInitialPackageMaintenanceRecord(r, owner, location, instance)
		if err != nil {
			return update.MaintenanceRecord{}, err
		}
		if err = CheckPackageWalletSigner(m, r.Package); err != nil {
			return update.MaintenanceRecord{}, err
		}
		return r, nil
	}
	if r.Phase != "completed" || r.Start != "target" || r.Application == nil || !r.RecoveryEligible() {
		return update.MaintenanceRecord{}, errors.New("finish the original package update before Wallets")
	}
	target, err := SelectProtectedPackageStart(r, owner, location, instance)
	if err != nil || target.Network != "" {
		return update.MaintenanceRecord{}, errors.New("the original empty-signer target is required")
	}
	if err = CheckPackageWalletSigner(target, r.Package); err != nil {
		return update.MaintenanceRecord{}, err
	}
	initial := update.MaintenanceRecord{Version: 1, Owner: owner, OperationID: instance, Phase: "idle", Start: "base", Base: mustJSON(target), Target: mustJSON(target), Package: r.Package}
	if _, err := ValidateInitialPackageMaintenanceRecord(initial, owner, location, instance); err != nil {
		return update.MaintenanceRecord{}, err
	}
	return initial, nil
}

// PendingPackageWallet exposes only the public identity of an interrupted
// initialization. The installed helper still rechecks all files and state
// before resuming the exact recorded request.
func PendingPackageWallet(owner int, location, instance string) (Manifest, *update.WalletInitialization, error) {
	if owner <= 0 || owner != os.Getuid() || owner != os.Geteuid() {
		return Manifest{}, nil, errors.New("the ordinary package owner is required")
	}
	r, err := update.ReadPackageMaintenanceRecord(owner, location, instance)
	if err != nil {
		return Manifest{}, nil, err
	}
	if r.Phase != "wallet-initializing" {
		return Manifest{}, nil, nil
	}
	base, _, err := ValidatePackageWalletInitialization(r, owner, location, instance)
	if err != nil {
		return Manifest{}, nil, err
	}
	for _, item := range []struct{ name, hash string }{{"fased-app", base.CoreHash}, {"fased-signer", base.SignerHash}} {
		actual, _, err := maintenance.ProtectedDigest(base.Root+"/"+item.name, 0755, 128<<20)
		if err != nil || actual != item.hash {
			return Manifest{}, nil, errors.New("the original package wallet executable changed")
		}
	}
	return base, r.Wallet, nil
}

func packageWalletMarker(m Manifest, instance string) []byte {
	return mustJSON(struct {
		Version  int    `json:"version"`
		Instance string `json:"instanceId"`
		Signer   string `json:"signerId"`
		Network  string `json:"network"`
	}{1, instance, m.ID, m.Network})
}

// The systemd condition is only a derived signal. The journal, protected
// owner commitment and signer identity remain the authority to start.
func verifyPackageWalletMarker(m Manifest) error {
	path := m.Root + "/wallet-configured.json"
	if m.Network == "" {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("unconfigured package has an existing wallet marker; preserve it")
		}
		return nil
	}
	commit, err := ReadEnrollmentCommit(m)
	if err != nil {
		return err
	}
	f, err := readRoot(path)
	if err != nil || !known(f, packageWalletMarker(m, commit.InstanceID), 0644) {
		return errors.New("wallet configuration differs from the original package owner")
	}
	return nil
}

func packageWalletCommit(m Manifest, instance string) EnrollmentCommit {
	return EnrollmentCommit{Version: 1, ManifestSHA256: sum(mustJSON(EnrollmentIdentity(m))), InstanceID: instance, Digest: packageOwnerDigest(m, instance)}
}

type packageWalletFile struct {
	path        string
	old, target []byte
}

func packageWalletFiles(base, target Manifest, instance string) []packageWalletFile {
	return []packageWalletFile{
		{base.Root + "/identity.json", mustJSON(identityConfig(base)), mustJSON(identityConfig(target))},
		{base.Root + "/install.json", mustJSON(base), mustJSON(target)},
		{base.Root + "/enrollment-ready.json", mustJSON(packageWalletCommit(base, instance)), mustJSON(packageWalletCommit(target, instance))},
	}
}

func inspectPackageWalletFiles(files []packageWalletFile, marker string, markerData []byte, read readHost) error {
	for _, file := range files {
		actual, err := read(file.path)
		if err != nil || !known(actual, file.old, 0644) && !known(actual, file.target, 0644) {
			return errors.New("wallet configuration changed; preserve recovery")
		}
	}
	if actual, err := read(marker); err == nil {
		if !known(actual, markerData, 0644) {
			return errors.New("another wallet marker exists; preserve it")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func publishPackageWalletFiles(files []packageWalletFile, marker string, markerData []byte, replace func(string, []byte, os.FileMode, string) error, publish func(string, []byte, os.FileMode) error) error {
	for _, file := range files {
		if err := replace(file.path, file.target, 0644, sum(file.old)); err != nil {
			return err
		}
	}
	return publish(marker, markerData, 0644)
}

// The completed candidate is archived by the installer before the same
// journal is reused. Require its exact protected record on interruption too.
func retainCompletedWalletPredecessor(r update.MaintenanceRecord, m Manifest) error {
	if r.Package == nil || r.Package.InstallerDigest() == "" || r.PreviousPackage == nil {
		return errors.New("the original completed package is required")
	}
	root, err := update.PackageMaintenanceRoot(m.Owner, m.Location, m.ID)
	if err != nil {
		return err
	}
	archive := root + "/candidate-archive/" + r.Package.ReleaseSHA256
	read := func(path string, max int64) ([]byte, error) {
		f, err := update.OpenProtected(path, max, 0644)
		if err != nil {
			return nil, err
		}
		b, readErr := io.ReadAll(io.LimitReader(f, max+1))
		return b, errors.Join(readErr, f.Close())
	}
	journal, err := read(archive+".journal.json", 256<<10)
	if err != nil || !bytes.Equal(journal, append(mustJSON(r), '\n')) {
		return errors.New("the original completed package journal must be preserved")
	}
	b, err := read(archive+".receipt.json", 8192)
	var receipt maintenance.CandidateReceipt
	base, target, manifestErr := protectedRecordManifests(r)
	if err != nil || update.StrictJSON(b, &receipt) != nil || manifestErr != nil || target != m ||
		!receipt.MatchesBase(m.Owner, r.PreviousPackage.Sequence, r.PreviousPackage.ReleaseSHA256, m.ID, update.SnapshotDigest("candidate-base-manifest:v1", base)) ||
		receipt.ReleaseSHA256 != r.Package.ReleaseSHA256 || receipt.PacketSHA256 != r.Package.ReleaseSHA256 || receipt.Sequence != r.Package.Sequence ||
		receipt.ProductVersion != r.Package.Version || receipt.EngineSHA256 != m.CoreHash || receipt.InstallerSHA256 != r.Package.InstallerDigest() {
		return errors.New("the original completed package receipt must be preserved")
	}
	dir, err := update.PackageMaintenanceDirectory(m.Owner, m.Location, m.ID, r.OperationID)
	if err != nil {
		return err
	}
	return maintenance.PublishProtected(dir+"/receipt.json", mustJSON(r), 0644)
}

// ReadPackageWalletPredecessor verifies a completed app-only update after
// Wallets reuses its journal slot. It never supplies a live worker approval.
func ReadPackageWalletPredecessor(current update.MaintenanceRecord, owner int, location, instance, operation string) (update.MaintenanceRecord, string, error) {
	var receipt update.MaintenanceRecord
	target, err := ValidateIdlePackageMaintenanceRecord(current, owner, location, instance)
	if err != nil || target.Network == "" {
		return receipt, "", errors.New("configured idle package required")
	}
	dir, err := update.PackageMaintenanceDirectory(owner, location, instance, operation)
	if err != nil {
		return receipt, "", err
	}
	f, err := update.OpenProtected(dir+"/receipt.json", 256<<10, 0644)
	if err != nil {
		return receipt, "", err
	}
	b, err := io.ReadAll(io.LimitReader(f, (256<<10)+1))
	if err = errors.Join(err, f.Close()); err != nil {
		return receipt, "", err
	}
	if update.StrictJSONBounded(b, &receipt, 256<<10) != nil || receipt.OperationID != operation || receipt.Application == nil ||
		update.SnapshotDigest("wallet-predecessor-package", receipt.Package) != update.SnapshotDigest("wallet-predecessor-package", current.Package) {
		return update.MaintenanceRecord{}, "", errors.New("original completed wallet predecessor required")
	}
	previous, err := SelectArchivedPackageCompletion(receipt, owner, location, instance)
	if err != nil || previous.Network != "" {
		return update.MaintenanceRecord{}, "", errors.New("original empty-signer completion required")
	}
	previous.Network = target.Network
	if previous != target || VerifyPackageOwnerTarget(target) != nil {
		return update.MaintenanceRecord{}, "", errors.New("configured package differs from the original completed predecessor")
	}
	return receipt, target.Root, nil
}

// ConfigureProtectedPackageWallet is called only by the installed fixed helper
// while the instance lock is held. It never receives or writes a wallet key.
func ConfigureProtectedPackageWallet(ctx context.Context, owner int, location, instance string, request update.WalletInitialization, lock *os.File) error {
	a, err := maintenance.ValidatePackageProcess(owner, location, instance)
	if err != nil || lock == nil || !request.Valid() {
		return errors.New("original package owner and wallet request required")
	}
	r, err := update.ReadPackageMaintenanceRecord(owner, location, instance)
	if err != nil {
		return err
	}
	var original Manifest
	originalBytes := r.Base
	if r.Phase == "completed" {
		originalBytes = r.Target
	}
	if strict(originalBytes, &original) != nil || original.Owner != owner || original.Location != location || original.ID != instance {
		return errors.New("the original package identity is required")
	}
	if err = CheckPackageWalletSigner(original, r.Package); err != nil {
		return err
	}
	if r.Phase == "completed" {
		selected, e := SelectProtectedPackageStart(r, owner, location, instance)
		actual, loadErr := LoadPackageInstance(owner, selected.Root)
		if e != nil || loadErr != nil || actual != selected || VerifyPackageOwnerTarget(actual) != nil {
			return errors.New("the original completed package identity is unavailable")
		}
		commit, e := ReadEnrollmentCommit(actual)
		if e != nil || commit.InstanceID != request.InstanceID {
			return errors.New("wallet request belongs to another workspace")
		}
		for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
			if _, e = os.Lstat(actual.Root + "/" + name); !errors.Is(e, os.ErrNotExist) {
				return errors.New("finish the original update before configuring Wallets")
			}
		}
		if actual.Network != "" {
			if actual.Network != request.Network {
				return errors.New("the existing wallet network cannot be changed")
			}
			return startConfiguredPackageWallet(ctx, actual, request.InstanceID, lock)
		}
		initial, e := walletInitialRecord(r, owner, location, instance)
		if e != nil {
			return e
		}
		var pause update.CorePauseReceipt
		q := maintenance.AppMaintenanceRequest{Action: "pause", OperationID: r.OperationID, Pause: &update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()}}
		if e = protectedAppPeer(ctx, a, actual, q, &pause); e != nil || !update.SameReleasedPause(r.OperationID, r.Application.Pause, pause) {
			return errors.New("the original update must be released before configuring Wallets")
		}
		if e = retainCompletedWalletPredecessor(r, actual); e != nil {
			return e
		}
		r = initial
	}
	if r.Phase == "idle" {
		m, err := ValidateIdlePackageMaintenanceRecord(r, owner, location, instance)
		if err != nil {
			return err
		}
		actual, err := LoadPackageInstance(owner, m.Root)
		if err != nil || actual != m || VerifyPackageInstance(m) != nil {
			return errors.New("the original package identity is unavailable")
		}
		commit, err := ReadEnrollmentCommit(m)
		if err != nil || commit.InstanceID != request.InstanceID {
			return errors.New("wallet request belongs to another workspace")
		}
		if err = verifyPackageWalletMarker(m); err != nil {
			return err
		}
		for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
			if _, err = os.Lstat(m.Root + "/" + name); !errors.Is(err, os.ErrNotExist) {
				return errors.New("finish the original update before configuring Wallets")
			}
		}
		if m.Network != "" {
			if m.Network != request.Network {
				return errors.New("the existing wallet network cannot be changed")
			}
			return startConfiguredPackageWallet(ctx, m, request.InstanceID, lock)
		}
		if err = VerifyUnconfiguredPackageSigner(ctx, m); err != nil {
			return err
		}
		if err = verifyPackageRootUnits(ctx, a, m, instance); err != nil {
			return err
		}
		if err = verifyLoadedPackageServices(ctx, m); err != nil {
			return err
		}
		r.Phase, r.Start, r.Wallet = "wallet-initializing", "", &request
		if _, _, err = ValidatePackageWalletInitialization(r, owner, location, instance); err != nil {
			return err
		}
		if err = storeProtectedRecord(r); err != nil {
			return err
		}
	} else if r.Phase != "wallet-initializing" || r.Wallet == nil || *r.Wallet != request {
		return errors.New("another package operation requires recovery")
	}
	target, err := resumePackageWalletInitialization(ctx, a, r, false)
	if err != nil {
		return err
	}
	return startConfiguredPackageWallet(ctx, target, request.InstanceID, lock)
}

// RecoverProtectedPackageWallet resumes only the already recorded transition.
// The boot recovery unit returns before systemd starts either app or signer.
func RecoverProtectedPackageWallet(ctx context.Context, owner int, location, instance string) error {
	a, err := maintenance.ValidatePackageProcess(owner, location, instance)
	if err != nil {
		return err
	}
	r, err := update.ReadPackageMaintenanceRecord(owner, location, instance)
	if err != nil {
		return err
	}
	_, err = resumePackageWalletInitialization(ctx, a, r, true)
	return err
}

func resumePackageWalletInitialization(ctx context.Context, a maintenance.Authority, r update.MaintenanceRecord, boot bool) (Manifest, error) {
	var zero Manifest
	if r.Wallet == nil {
		return zero, errors.New("original wallet initialization required")
	}
	var raw Manifest
	if strict(r.Base, &raw) != nil {
		return zero, errors.New("original package identity required")
	}
	base, target, err := ValidatePackageWalletInitialization(r, a.Owner, a.Profile, raw.ID)
	if err != nil {
		return zero, err
	}
	if err = CheckPackageWalletSigner(base, r.Package); err != nil {
		return zero, err
	}
	if err = VerifyUnconfiguredPackageSigner(ctx, base); err != nil {
		return zero, err
	}
	if err = verifyManagedIdentities(base); err != nil {
		return zero, err
	}
	for _, item := range []struct{ name, hash string }{{"fased-app", base.CoreHash}, {"fased-signer", base.SignerHash}} {
		if err = verifyRootExecutable(base.Root+"/"+item.name, item.hash); err != nil {
			return zero, err
		}
	}
	if err = verifyPackageRootUnits(ctx, a, base, base.ID); err != nil {
		return zero, err
	}
	if err = verifyLoadedPackageServices(ctx, base); err != nil {
		return zero, err
	}
	files := packageWalletFiles(base, target, r.Wallet.InstanceID)
	marker := base.Root + "/wallet-configured.json"
	markerData := packageWalletMarker(target, r.Wallet.InstanceID)
	if err = inspectPackageWalletFiles(files, marker, markerData, readRoot); err != nil {
		return zero, err
	}
	for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
		if _, err = os.Lstat(base.Root + "/" + name); !errors.Is(err, os.ErrNotExist) {
			return zero, errors.New("an update fence requires its own recovery")
		}
	}
	if !boot {
		if _, err = protectedSystemctl(ctx, "stop", "--", instanceUnit(base, "app"), instanceUnit(base, "signer")); err != nil {
			return zero, err
		}
	}
	if err = servicesStopped(ctx, base, func(ctx context.Context, _ []byte, args ...string) ([]byte, error) {
		return protectedSystemctl(ctx, args[1:]...)
	}); err != nil {
		return zero, err
	}
	if err = VerifyUnconfiguredPackageSigner(ctx, base); err != nil {
		return zero, err
	}
	if err = inspectPackageWalletFiles(files, marker, markerData, readRoot); err != nil {
		return zero, err
	}
	if err = publishPackageWalletFiles(files, marker, markerData, maintenance.ReplaceProtected, maintenance.PublishProtected); err != nil {
		return zero, err
	}
	if err = VerifyPackageInstance(target); err != nil {
		return zero, err
	}
	if err = verifyPackageWalletMarker(target); err != nil {
		return zero, err
	}
	r.Phase, r.Start, r.Wallet = "idle", "base", nil
	r.Base, r.Target = mustJSON(target), mustJSON(target)
	if _, err = ValidateIdlePackageMaintenanceRecord(r, target.Owner, target.Location, target.ID); err != nil {
		return zero, err
	}
	if err = storeProtectedRecord(r); err != nil {
		return zero, err
	}
	return target, nil
}

func startConfiguredPackageWallet(ctx context.Context, m Manifest, instance string, lock *os.File) error {
	return packageOwnerAfterUnlock(lock, func() error {
		if _, err := protectedSystemctl(ctx, "start", "--", instanceUnit(m, "signer"), instanceUnit(m, "app")); err != nil {
			return err
		}
		deadline := time.NewTimer(20 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			var out bytes.Buffer
			request := []byte("GET /api/v1/health HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
			if Runtime(m) == nil && packageOwnerExchange(ctx, m, request, &out, 64<<10) == nil {
				response, err := http.ReadResponse(bufio.NewReader(&out), nil)
				if err == nil {
					var health core.Health
					decodeErr := json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&health)
					_ = response.Body.Close()
					if decodeErr == nil && response.StatusCode == http.StatusOK && health.Status == "ok" && health.APIVersion == core.APIVersion && health.InstanceID == instance {
						return nil
					}
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return errors.New("the original Wallets service is not ready; preserve it for recovery")
			case <-tick.C:
			}
		}
	})
}
