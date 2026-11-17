//go:build linux

package custodyinstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func noActiveController(m Manifest) error {
	_, e := readRoot(m.Root + "/update-active.json")
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	return errors.New("recoverable installation owns this instance; resume or roll back that exact intent first")
}
func LoadController(uid int, id string) (InstallTransaction, error) {
	var t InstallTransaction
	if uid <= 0 || !update.IDOK(id) {
		return t, errors.New("ordinary owner and exact installation intent required")
	}
	f, e := readRoot(Root(uid) + "/updates/" + id + "/transaction.json")
	if e != nil {
		return t, e
	}
	if f.Mode != 0644 || strict(f.Data, &t) != nil || !t.valid() || t.Base.Owner != uid || t.Binding.Index.Binding.Pause.Request.IntentID != id {
		return t, errors.New("foreign installation transaction; preserve state")
	}
	return t, nil
}

// LoadCanonicalController is used by the app-side OS guard, which must bind
// the exact protected transaction bytes to the signer's installation digest.
func LoadCanonicalController(uid int, id string) (InstallTransaction, error) {
	var zero InstallTransaction
	t, err := LoadController(uid, id)
	if err != nil {
		return zero, err
	}
	f, err := readRoot(t.dir() + "/transaction.json")
	if err != nil || !known(f, mustJSON(t), 0644) {
		return zero, errors.New("protected installation transaction changed; preserve it")
	}
	return t, nil
}

type preparationFenceState uint8

// ErrTransactionRecoveryFence directs a verified published transaction to its
// own recovery path. It must never be mistaken for an absent fence.
var ErrTransactionRecoveryFence = errors.New("transaction recovery owns the restart fence")

const (
	preparationFenceAbsent preparationFenceState = iota
	preparationFencePending
	preparationFenceTransaction
)

// inspectPreparationFence only reads root-owned markers. It never resumes a
// worker; the owner-locked controller remains responsible for that operation.
func inspectPreparationFence(m Manifest, id, digest string, read readHost, same func() error) (preparationFenceState, error) {
	if m.Owner <= 0 || !update.IDOK(id) || read == nil || same == nil {
		return preparationFenceAbsent, errors.New("exact preparation identity required")
	}
	f, err := read(m.Root + "/update-start-blocked.json")
	if errors.Is(err, os.ErrNotExist) {
		return preparationFenceAbsent, nil
	}
	sentinel, sentinelErr := signer.InstallationPreparationFenceBytes(m.Owner, m.ID)
	if err != nil || sentinelErr != nil {
		return preparationFenceAbsent, errors.New("another recovery fence owns this installation")
	}
	if known(f, sentinel, 0644) {
		if same() != nil {
			return preparationFenceAbsent, errors.New("another recovery fence owns this installation")
		}
		return preparationFencePending, nil
	}
	var marker signer.InstallationGuardMarker
	if strict(f.Data, &marker) == nil && known(f, mustJSON(marker), 0644) && marker.Version == 1 && marker.Owner == m.Owner && marker.SignerID == m.ID && marker.IntentID == id && validHex(marker.Digest, 32) {
		switch marker.Direction {
		case "forward", "rollback", "completion", "cleanup-rollback", "cleanup-complete":
			if digest == "" || marker.Digest != digest {
				break
			}
			return preparationFenceTransaction, nil
		}
	}
	return preparationFenceAbsent, errors.New("another recovery fence owns this installation")
}

// UpdateFencePresent is a read-only pre-lock check used only to avoid creating
// a live client session while an existing recovery fence owns the pair.
func UpdateFencePresent(m Manifest, id, digest string) (bool, error) {
	if m.Owner != os.Getuid() || os.Geteuid() != m.Owner {
		return false, errors.New("ordinary installation owner required")
	}
	state, err := inspectPreparationFence(m, id, digest, readRoot, func() error { return sameReusablePreparationFence(m) })
	return state != preparationFenceAbsent, err
}

// PreparationHold recovers only the signer's real held metadata while the
// fixed preparation sentinel keeps both services stopped. An absent sentinel
// means the ordinary live-owner path should be used instead.
func PreparationHold(ctx context.Context, m Manifest, id, digest string, s *System) (*update.Installation, error) {
	if m.Owner != os.Getuid() || os.Geteuid() != m.Owner || !update.IDOK(id) || s == nil {
		return nil, errors.New("exact ordinary owner and preparation intent required")
	}
	state, err := inspectPreparationFence(m, id, digest, readRoot, func() error { return sameReusablePreparationFence(m) })
	if err != nil {
		return nil, err
	}
	if state == preparationFenceAbsent {
		return nil, os.ErrNotExist
	}
	if state == preparationFenceTransaction {
		return nil, ErrTransactionRecoveryFence
	}
	pin, err := readReusableMaintenanceAuthority(m, readRoot)
	if err != nil || pin == nil || verifyReusableMaintenanceAuthority(m, pin, pin.RunnerSHA256, pin.GuardSHA256, verifiedMigrationRunnerSize) != nil {
		return nil, errors.New("pinned preparation authority unavailable; preserve the fence")
	}
	// A crash may have published the sentinel before systemd finished stopping
	// both services. Resume that exact fixed unit before attempting cold reads.
	if err = resumeReusablePreparationFence(ctx, m, id, s, pin); err != nil {
		return nil, err
	}
	p := m.Signer
	args := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(p.UID), "--regid", strconv.Itoa(p.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", reusableMaintenancePath(m, "atomic-guard"), "fixture-preparation-hold", "--owner", strconv.Itoa(m.Owner), "--intent-id", id, "--disposable-fixture"}
	b, err := s.Run(ctx, nil, args...)
	if err != nil {
		return nil, errors.New("cold preparation hold is unavailable; preserve the fence")
	}
	var held update.Installation
	if len(b) > 4<<20 || update.StrictJSON(b, &held) != nil || !update.ValidInstallation(held) || held.Phase != "active" || held.InstalledSequenceCommitted || held.Binding.Index.Binding.Pause.Request == nil || held.Binding.Index.Binding.Pause.Request.IntentID != id || held.Binding.InstallationID != m.ID || held.Binding.PreviousCore != m.CoreHash || held.Binding.PreviousSigner != m.SignerHash || held.Binding.PreviousManifest != sum(mustJSON(m)) || sameReusablePreparationFence(m) != nil {
		return nil, errors.New("cold preparation hold differs; preserve the fence")
	}
	return &held, nil
}

func resumeReusablePreparationFence(ctx context.Context, m Manifest, id string, s *System, pin *reusableMaintenanceAuthority) error {
	if err := verifyReusableAtomicHost(ctx, InstallTransaction{Base: m}, s, pin); err != nil {
		return err
	}
	if err := verifyReusablePreparationConfiguration(ctx, m, pin); err != nil {
		return err
	}
	inbox := reusableMaintenancePath(m, "inbox")
	r, err := readReusableAtomicRequestAt(inbox, m.Owner, m.Signer.GID, trustedParents)
	if err != nil || r.InstallationID != m.ID || r.IntentID != id {
		return errors.New("original preparation request unavailable; preserve the fence")
	}
	r.Direction, r.StageKind, r.StageSlot, r.Probe, r.RequestID = "fence", "", "", "", update.NewProbe()
	if err = writeReusableAtomicRequestAt(inbox, r, m.Signer.GID, trustedParents); err != nil {
		return err
	}
	if _, err = s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", reusableAtomicLauncherName(m.Owner)); err != nil {
		return fmt.Errorf("preparation fence recovery failed; preserve it: %w", err)
	}
	return requireReusableWorkerCompletion(m, r.RequestID, reusablePreparationFenceName(m.Owner), readReusableWorkerCompletion)
}

// LoadCanonicalStagedController reads only the protected preparation slot.
// This is read-only: the enclosing OS unit must first authenticate the same
// binding against the stopped signer's journal and serialize privileged work.
func LoadCanonicalStagedController(uid int) (InstallTransaction, error) {
	return loadCanonicalStagedController(uid, readRoot, sameReusablePreparationFence)
}

func loadCanonicalStagedController(uid int, read readHost, sameSentinel func(Manifest) error) (InstallTransaction, error) {
	var t InstallTransaction
	if uid <= 0 || read == nil {
		return t, errors.New("ordinary installation owner required")
	}
	root := Root(uid)
	f, err := read(filepath.Join(root, "maintenance", "staging", "transaction"))
	if err != nil || strict(f.Data, &t) != nil || !t.valid() || t.Base.Owner != uid || !known(f, mustJSON(t), 0644) {
		return InstallTransaction{}, errors.New("exact protected staging transaction required")
	}
	marker := signer.InstallationGuardMarker{Version: 1, Owner: uid, SignerID: t.Base.ID, IntentID: t.Binding.Index.Binding.Pause.Request.IntentID, Digest: update.InstallDigest(t.Binding), Direction: "forward"}
	f, err = read(filepath.Join(root, "update-start-blocked.json"))
	if err != nil {
		return InstallTransaction{}, errors.New("exact protected preparation or forward fence required")
	}
	if !known(f, mustJSON(marker), 0644) {
		sentinel, sentinelErr := signer.InstallationPreparationFenceBytes(uid, t.Base.ID)
		source, sourceErr := read(reusableMaintenancePath(t.Base, "preparation-fence.json"))
		if sentinelErr != nil || sourceErr != nil || !known(f, sentinel, 0644) || !known(source, sentinel, 0644) || sameSentinel == nil || sameSentinel(t.Base) != nil {
			return InstallTransaction{}, errors.New("exact protected preparation or forward fence required")
		}
	}
	return t, nil
}

// The old immutable runner is retained before either live executable changes.
// It is selected only by a validated fixed transaction path, never a CLI path.
func ControllerRunner(uid int, id string) (string, error) {
	t, e := LoadController(uid, id)
	if e != nil {
		return "", e
	}
	if e = refuseRepairedPredecessor(t, readRoot); e != nil {
		return "", e
	}
	f, e := readRoot(t.dir() + "/runner")
	if e != nil {
		return "", e
	}
	if f.Mode != 0755 || sum(f.Data) != t.Base.CoreHash {
		return "", errors.New("recovery runner changed")
	}
	f, e = readRoot(t.dir() + "/previous-signer")
	if e != nil {
		return "", e
	}
	if f.Mode != 0755 || sum(f.Data) != t.Base.SignerHash {
		return "", errors.New("cold inspection signer changed")
	}
	if e = verifyTopology(t.Base); e != nil {
		return "", e
	}
	if e = allowedInstallation(t, readRoot); e != nil {
		return "", e
	}
	return selectMigrationRunner(t, readRoot, scopedMigrationEnabled, verifiedMigrationRunnerSize, verifyApprovedExecutable, probeMigrationContract)
}

// migrationRunnerApproval is a root-owned, transaction-specific authorization.
// It never changes the saved predecessor or the signed target pair.
type migrationRunnerApproval struct {
	Version        int    `json:"version"`
	Owner          int    `json:"owner"`
	InstallationID string `json:"installationId"`
	IntentID       string `json:"intentId"`
	BindingDigest  string `json:"bindingDigest"`
	PreviousCore   string `json:"previousCore"`
	TargetCore     string `json:"targetCore"`
	TargetSigner   string `json:"targetSigner"`
	RunnerSHA256   string `json:"runnerSha256"`
	GuardSHA256    string `json:"guardSha256"`
}

func validMigrationApproval(t InstallTransaction, a migrationRunnerApproval) bool {
	return a.Version == 1 && a.Owner == t.Base.Owner && a.InstallationID == t.Base.ID && a.IntentID == t.Binding.Index.Binding.Pause.Request.IntentID && a.BindingDigest == update.InstallDigest(t.Binding) && a.PreviousCore == t.Base.CoreHash && a.TargetCore == t.Target.CoreHash && a.TargetSigner == t.Target.SignerHash && validHex(a.RunnerSHA256, 32) && a.RunnerSHA256 != t.Base.CoreHash && a.RunnerSHA256 != t.Target.CoreHash && validHex(a.GuardSHA256, 32) && a.GuardSHA256 != t.Base.SignerHash && a.GuardSHA256 != t.Target.SignerHash
}

// InitialMigrationRunner resolves only a separately root-approved launcher.
// The controller will revalidate the complete transaction binding before any
// installation write. An absent scope retains the ordinary predecessor path.
func InitialMigrationRunner(uid int, id string) (string, error) {
	if uid <= 0 || uid != os.Getuid() || os.Geteuid() != uid || !update.IDOK(id) {
		return "", errors.New("exact ordinary owner and intent required")
	}
	m, err := Load(uid)
	if err != nil {
		return "", err
	}
	if err = Verify(m); err != nil {
		return "", err
	}
	return selectInitialMigrationRunner(m, id, readRoot, scopedMigrationEnabled, verifiedMigrationRunnerSize, verifyApprovedExecutable, probeInitialMigrationCapabilities)
}

// PinnedMaintenanceRunner is the reusable owner entry for inventory and the
// pre-install update steps. A missing pin preserves the predecessor path;
// a damaged pin or missing scope never falls back to an older executable.
func PinnedMaintenanceRunner(uid int) (string, error) {
	if uid <= 0 || uid != os.Getuid() || os.Geteuid() != uid {
		return "", errors.New("ordinary installation owner required")
	}
	m, err := Load(uid)
	if err != nil {
		return "", err
	}
	if err = Verify(m); err != nil {
		return "", err
	}
	pin, err := readReusableMaintenanceAuthority(m, readRoot)
	if err != nil {
		return "", err
	}
	if pin == nil {
		return "", os.ErrNotExist
	}
	enabled, err := scopedMigrationEnabled(uid)
	if err != nil || !enabled {
		return "", errors.New("pinned maintenance scope is unavailable; preserve the installation")
	}
	return verifiedPinnedMaintenanceRunner(m, pin, verifiedMigrationRunnerSize, probeInitialMigrationCapabilities)
}

func verifiedPinnedMaintenanceRunner(m Manifest, pin *reusableMaintenanceAuthority, candidate func(string, string) (int64, error), probe func(string, Manifest) error) (string, error) {
	if pin == nil || candidate == nil || probe == nil {
		return "", errors.New("pinned migration checks unavailable")
	}
	if err := verifyReusableMaintenanceAuthority(m, pin, pin.RunnerSHA256, pin.GuardSHA256, candidate); err != nil {
		return "", err
	}
	launcher := reusableMaintenancePath(m, "runner")
	if err := probe(launcher, m); err != nil {
		return "", errors.New("pinned migration launcher failed its capability check")
	}
	return launcher, nil
}

func selectInitialMigrationRunner(m Manifest, id string, read readHost, scope func(int) (bool, error), candidate func(string, string) (int64, error), approval approvedExecutableVerifier, probe func(string, Manifest) error) (string, error) {
	if m.validate() != nil || !update.IDOK(id) || read == nil || scope == nil {
		return "", errors.New("exact managed owner and intent required")
	}
	enabled, err := scope(m.Owner)
	if err != nil {
		return "", errors.New("initial migration scope is unavailable; preserve the transaction")
	}
	if !enabled {
		pinned, pinErr := readReusableMaintenanceAuthority(m, read)
		if pinErr != nil {
			return "", pinErr
		}
		if pinned != nil {
			return "", errors.New("pinned maintenance scope is unavailable; preserve the installation")
		}
		return "", os.ErrNotExist
	}
	pinned, err := readReusableMaintenanceAuthority(m, read)
	if err != nil {
		return "", err
	}
	if pinned != nil {
		return verifiedPinnedMaintenanceRunner(m, pinned, candidate, probe)
	}
	dir := fmt.Sprintf("/var/lib/fased-app-approved-%d", m.Owner)
	f, err := read(filepath.Join(dir, "migration-"+id+".json"))
	var a migrationRunnerApproval
	if err != nil || strict(f.Data, &a) != nil || !known(f, mustJSON(a), 0644) || a.Version != 1 || a.Owner != m.Owner || a.InstallationID != m.ID || a.IntentID != id || a.PreviousCore != m.CoreHash || !validHex(a.BindingDigest, 32) || !validHex(a.TargetCore, 32) || !validHex(a.TargetSigner, 32) || !validHex(a.RunnerSHA256, 32) || a.RunnerSHA256 == m.CoreHash || a.RunnerSHA256 == a.TargetCore || !validHex(a.GuardSHA256, 32) || a.GuardSHA256 == m.SignerHash || a.GuardSHA256 == a.TargetSigner {
		return "", errors.New("exact root-approved initial migration required")
	}
	if candidate == nil || approval == nil || probe == nil {
		return "", errors.New("approved migration checks unavailable")
	}
	launcher := filepath.Join(dir, "migration-launcher-"+id)
	size, err := candidate(launcher, a.RunnerSHA256)
	if err != nil {
		return "", errors.New("approved migration launcher is unavailable or changed")
	}
	if err = approval(filepath.Join(dir, a.RunnerSHA256), a.RunnerSHA256, size); err != nil {
		return "", errors.New("approved migration source is unavailable or changed")
	}
	if err = probe(launcher, m); err != nil {
		return "", errors.New("approved migration launcher failed its capability check")
	}
	return launcher, nil
}

// A scoped migration uses a separately approved immutable controller so that
// recovery does not depend on executing the target application. A malformed or
// incomplete scope never falls back to the predecessor's unbound OS writes.
func selectMigrationRunner(t InstallTransaction, read readHost, scope func(int) (bool, error), candidate func(string, string) (int64, error), approval approvedExecutableVerifier, probe func(string, InstallTransaction) error) (string, error) {
	enabled, err := scope(t.Base.Owner)
	if err != nil {
		return "", err
	}
	f, err := read(t.dir() + "/migration-runner.json")
	if errors.Is(err, os.ErrNotExist) {
		if enabled {
			return "", errors.New("scoped migration approval is missing; preserve the transaction")
		}
		pinned, pinErr := readReusableMaintenanceAuthority(t.Base, read)
		if pinErr != nil {
			return "", pinErr
		}
		if pinned != nil {
			return "", errors.New("pinned maintenance scope is unavailable; preserve the transaction")
		}
		return t.dir() + "/runner", nil
	}
	if err != nil {
		return "", errors.New("scoped migration approval is unavailable; preserve the transaction")
	}
	var a migrationRunnerApproval
	if !enabled || strict(f.Data, &a) != nil || !known(f, mustJSON(a), 0644) || !validMigrationApproval(t, a) {
		return "", errors.New("foreign or non-independent migration approval; preserve the transaction")
	}
	pinned, err := readReusableMaintenanceAuthority(t.Base, read)
	if err != nil {
		return "", err
	}
	if pinned != nil {
		if err = verifyReusableMaintenanceAuthority(t.Base, pinned, a.RunnerSHA256, a.GuardSHA256, candidate); err != nil {
			return "", err
		}
		runner := reusableMaintenancePath(t.Base, "runner")
		if err = probe(runner, t); err != nil {
			return "", err
		}
		return runner, nil
	}
	runner := t.dir() + "/migration-runner"
	size, err := candidate(runner, a.RunnerSHA256)
	if err != nil {
		return "", err
	}
	source := filepath.Join(fmt.Sprintf("/var/lib/fased-app-approved-%d", t.Base.Owner), a.RunnerSHA256)
	if err = approval(source, a.RunnerSHA256, size); err != nil {
		return "", err
	}
	if err = probe(runner, t); err != nil {
		return "", err
	}
	return runner, nil
}

// Check every root-approved input before any privileged transaction write.
func migrationRunnerInputs(t InstallTransaction, read readHost, verify approvedExecutableVerifier) ([]byte, []byte, []byte, error) {
	dir := fmt.Sprintf("/var/lib/fased-app-approved-%d", t.Base.Owner)
	plan, err := read(filepath.Join(dir, "migration-"+t.Binding.Index.Binding.Pause.Request.IntentID+".json"))
	var a migrationRunnerApproval
	if err != nil || strict(plan.Data, &a) != nil || !known(plan, mustJSON(a), 0644) || !validMigrationApproval(t, a) {
		return nil, nil, nil, errors.New("exact root-approved migration plan required; preserve the transaction")
	}
	pinned, err := readReusableMaintenanceAuthority(t.Base, read)
	if err != nil {
		return nil, nil, nil, err
	}
	if pinned != nil {
		if err = verifyReusableMaintenanceAuthority(t.Base, pinned, a.RunnerSHA256, a.GuardSHA256, verifiedMigrationRunnerSize); err != nil {
			return nil, nil, nil, err
		}
	}
	source := filepath.Join(dir, a.RunnerSHA256)
	f, err := read(source)
	if err != nil || !f.Regular || f.UID != 0 || f.Mode != 0444 || sum(f.Data) != a.RunnerSHA256 {
		return nil, nil, nil, errors.New("approved migration runner unavailable or changed")
	}
	if err = verify(source, a.RunnerSHA256, int64(len(f.Data))); err != nil {
		return nil, nil, nil, err
	}
	if err = verify(ApprovedExecutablePath(t.Base.Owner, plan.Data), sum(plan.Data), int64(len(plan.Data))); err != nil {
		return nil, nil, nil, err
	}
	guardSource := filepath.Join(dir, a.GuardSHA256)
	guard, err := read(guardSource)
	if err != nil || !guard.Regular || guard.UID != 0 || guard.Mode != 0444 || sum(guard.Data) != a.GuardSHA256 {
		return nil, nil, nil, errors.New("approved independent signer guard unavailable or changed")
	}
	if err = verify(guardSource, a.GuardSHA256, int64(len(guard.Data))); err != nil {
		return nil, nil, nil, err
	}
	return plan.Data, f.Data, guard.Data, nil
}

// Prepare only root-approved, transaction-bound inputs. Both writes use the
// same immutable approved-source mechanism as the ordinary update controller.
func prepareMigrationRunner(ctx context.Context, t InstallTransaction, read readHost, run runHost, verify approvedExecutableVerifier, probe func(string, InstallTransaction) error) error {
	plan, image, guard, err := migrationRunnerInputs(t, read, verify)
	if err != nil {
		return err
	}
	runner := t.dir() + "/migration-runner"
	if err = publish(ctx, runner, t.Base.ID, image, 0755, read, run, verify); err != nil {
		return err
	}
	if err = probe(runner, t); err != nil {
		return err
	}
	if err = publish(ctx, t.dir()+"/atomic-guard", t.Base.ID, guard, 0755, read, run, verify); err != nil {
		return err
	}
	return publish(ctx, t.dir()+"/migration-runner.json", t.Base.ID, plan, 0644, read, run, verify)
}

// The migration reader is executed only as the ordinary owner. Its declared
// core contract and held capability flags must match the signed transaction.
func migrationCapabilityReply(path string, m Manifest) ([]byte, error) {
	if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() || os.Getuid() != m.Owner {
		return nil, errors.New("migration reader must run as its ordinary owner")
	}
	arg := "compatibility"
	if m.Version == 2 {
		arg = "custody-compatibility"
	} else if m.Version == 3 {
		arg = "ordinary-custody-compatibility"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "updates", arg)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LC_ALL=C"}
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(stdout, 1024*1024+1))
	if readErr != nil || len(b) > 1024*1024 {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("invalid migration reader response")
	}
	if err = cmd.Wait(); err != nil {
		return nil, errors.New("migration reader compatibility probe failed")
	}
	return b, nil
}

func probeMigrationContract(path string, t InstallTransaction) error {
	b, err := migrationCapabilityReply(path, t.Base)
	if err != nil {
		return err
	}
	return validateMigrationContract(t, b)
}

func migrationReplyContract(b []byte) (update.Contract, error) {
	var response struct {
		Contract           update.Contract `json:"contract"`
		WalletSigner       *bool           `json:"walletSigner"`
		FinancialExecution *bool           `json:"financialExecution"`
		UpdaterEnabled     *bool           `json:"updaterEnabled"`
	}
	if err := json.Unmarshal(b, &response); err != nil || response.WalletSigner == nil || *response.WalletSigner || response.FinancialExecution == nil || *response.FinancialExecution || response.UpdaterEnabled == nil || *response.UpdaterEnabled {
		return update.Contract{}, errors.New("migration reader capabilities are not explicitly held")
	}
	return response.Contract, nil
}

func probeInitialMigrationCapabilities(path string, m Manifest) error {
	b, err := migrationCapabilityReply(path, m)
	if err != nil {
		return err
	}
	c, err := migrationReplyContract(b)
	if err != nil || c.Component != "core" {
		return errors.New("initial migration requires a held core reader")
	}
	return nil
}

func validateMigrationContract(t InstallTransaction, b []byte) error {
	contract, err := migrationReplyContract(b)
	if err != nil {
		return err
	}
	for _, expected := range t.Binding.Contracts {
		if expected.Component == "core" && update.SnapshotDigest("contract", expected) == update.SnapshotDigest("contract", contract) {
			return nil
		}
	}
	return errors.New("migration reader contract differs from the signed target")
}

func scopedMigrationEnabled(owner int) (bool, error) {
	return scopedMigrationState(owner, trustedParents, readRoot)
}

func scopedMigrationState(owner int, trusted func(string) error, read readHost) (bool, error) {
	if owner <= 0 || trusted == nil || read == nil {
		return false, errors.New("ordinary owner required")
	}
	dir := fmt.Sprintf("/var/lib/fased-app-approved-%d", owner)
	if err := trusted(dir); errors.Is(err, os.ErrNotExist) {
		// A missing approved-input directory must never downgrade an
		// installation that has already opted into the pinned boundary.
		for _, name := range []string{"authority.json", "reusable-v1.json"} {
			_, pinErr := read(filepath.Join(Root(owner), "maintenance", name))
			if pinErr == nil || !errors.Is(pinErr, os.ErrNotExist) {
				return false, errors.New("pinned maintenance scope is unavailable; preserve the installation")
			}
		}
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

func verifiedMigrationRunnerSize(path, digest string) (int64, error) {
	if err := verifyRootExecutable(path, digest); err != nil {
		return 0, err
	}
	f, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if err = executableInfo(f); err != nil {
		return 0, err
	}
	return f.Size(), nil
}

func preflightScopedControllerInputs(t InstallTransaction, files map[string][]byte, verify approvedExecutableVerifier) error {
	for _, name := range []string{"runner", "previous-signer", "next-core", "next-signer", "transaction.json"} {
		data, ok := files[name]
		if !ok || len(data) == 0 {
			return errors.New("approved controller input unavailable")
		}
		if e := verify(ApprovedExecutablePath(t.Base.Owner, data), sum(data), int64(len(data))); e != nil {
			return e
		}
	}
	return nil
}

func preflightRestartDropIns(m Manifest, read readHost) error {
	if m.validate() != nil {
		return errors.New("valid custody installation required for restart guards")
	}
	want := []byte("[Unit]\nAssertPathExists=!" + m.Root + "/update-start-blocked.json\n")
	for _, role := range []string{"app", "signer"} {
		path := "/etc/systemd/system/" + Unit(m.Owner, role) + ".d/20-fased-update-guard.conf"
		f, err := read(path)
		if err != nil || !known(f, want, 0644) {
			return errors.New("exact root-owned app and signer restart assertions required")
		}
	}
	return nil
}

func PrepareController(ctx context.Context, m Manifest, index update.RecoveryIndex, contracts []update.Contract, artifacts string, s *System, hold func(context.Context, string, update.InstallBinding) (*update.Installation, error)) (InstallTransaction, error) {
	var zero InstallTransaction
	if m.Owner != os.Getuid() || Verify(m) != nil || !update.ValidRecoveryIndex(index, index.Binding) || index.Binding.Pause.Request == nil {
		return zero, errors.New("verified installation and metadata recovery index required")
	}
	id := index.Binding.Pause.Request.IntentID
	if !update.IDOK(id) {
		return zero, errors.New("exact intent required")
	}
	if e := noActiveController(m); e != nil {
		return zero, e
	}
	t := InstallTransaction{Version: 1, Base: m, Target: m}
	t.Binding = update.InstallBinding{Contracts: contracts, Version: 1, Index: index, InstallationID: m.ID, PreviousCore: m.CoreHash, PreviousSigner: m.SignerHash, PreviousManifest: sum(mustJSON(m))}
	for _, f := range index.Binding.Files {
		switch f.Artifact.File {
		case "fased-app":
			t.Target.CoreHash = f.Artifact.SHA256
			t.Binding.TargetCore = f.Artifact.SHA256
		case "fased-signer":
			t.Target.SignerHash = f.Artifact.SHA256
			t.Binding.TargetSigner = f.Artifact.SHA256
		}
	}
	if !t.valid() {
		return zero, errors.New("unsupported fixed candidate pair")
	}
	pin, e := readReusableMaintenanceAuthority(m, readRoot)
	if e != nil {
		return zero, e
	}
	if pin != nil {
		cfg, loadErr := signer.LoadConfig(m.Root + "/identity.json")
		if loadErr != nil {
			return zero, loadErr
		}
		return prepareReusableController(ctx, t, cfg, artifacts, s, pin, hold)
	}
	scoped, e := scopedMigrationEnabled(t.Base.Owner)
	if e != nil {
		return zero, e
	}
	var pairPlan atomicPairPlan
	if scoped {
		plan, _, _, err := migrationRunnerInputs(t, readRoot, verifyApprovedExecutable)
		if err != nil {
			return zero, err
		}
		var approved migrationRunnerApproval
		if err = strict(plan, &approved); err != nil {
			return zero, err
		}
		cfg, err := signer.LoadConfig(t.Base.Root + "/identity.json")
		if err != nil {
			return zero, err
		}
		pairPlan, err = makeAtomicPairPlan(t, cfg, approved.GuardSHA256, approved.RunnerSHA256)
		if err != nil {
			return zero, err
		}
		active, err := atomicPolicyExists(ctx, t, s)
		if err != nil {
			return zero, err
		}
		if active {
			return zero, errors.New("atomic policy already active without a complete transaction; preserve it")
		}
	}
	var fs unix.Statfs_t
	if e := unix.Statfs(m.Root, &fs); e != nil {
		return zero, e
	}
	needed := uint64(8 << 20)
	for _, f := range index.Binding.Files {
		needed += uint64(f.Bytes) * 2
	}
	for _, name := range []string{"fased-app", "fased-signer"} {
		st, e := os.Stat(m.Root + "/" + name)
		if e != nil {
			return zero, e
		}
		needed += uint64(st.Size()) * 2
	}
	if fs.Bsize <= 0 || fs.Bavail < uint64((needed+uint64(fs.Bsize)-1)/uint64(fs.Bsize)) {
		return zero, errors.New("insufficient space for candidate, recovery pair and atomic replacements")
	}
	files := map[string][]byte{}
	for _, p := range []struct{ source, name, hash string }{{m.Root + "/fased-app", "runner", m.CoreHash}, {m.Root + "/fased-signer", "previous-signer", m.SignerHash}} {
		f, e := readRoot(p.source)
		if e != nil || sum(f.Data) != p.hash {
			return zero, errors.New("previous executable changed")
		}
		files[p.name] = f.Data
	}
	for _, f := range index.Binding.Files {
		b, e := source(filepath.Join(artifacts, f.Artifact.File))
		if e != nil {
			return zero, e
		}
		if int64(len(b)) != f.Bytes || sum(b) != f.Artifact.SHA256 {
			return zero, errors.New("candidate source differs from staged identity")
		}
		name := "next-core"
		if f.Artifact.File == "fased-signer" {
			name = "next-signer"
		}
		files[name] = b
	}
	if scoped {
		files["transaction.json"] = mustJSON(t)
		if e := preflightScopedControllerInputs(t, files, verifyApprovedExecutable); e != nil {
			return zero, e
		}
		if e := prepareApprovedAtomicInputs(ctx, t.Base.Owner, pairPlan, readRoot, s.Run, verifyApprovedExecutable); e != nil {
			return zero, e
		}
		if e := preflightAtomicPairInputs(t.Base.Owner, pairPlan, verifyApprovedExecutable); e != nil {
			return zero, e
		}
		if e := prepareRestartGuards(ctx, m, readRoot, func(path string) error { return directory(ctx, path, 0, 0, 0755, s) }, s.Run); e != nil {
			return zero, e
		}
		for _, role := range []string{"app", "signer"} {
			if e := loadedUnit(m, role); e != nil {
				return zero, e
			}
		}
	}
	for _, d := range []string{m.Root + "/updates", t.dir()} {
		if e := directory(ctx, d, 0, 0, 0755, s); e != nil {
			return zero, e
		}
	}
	for _, name := range []string{"runner", "previous-signer", "next-core", "next-signer"} {
		if e := publish(ctx, t.dir()+"/"+name, m.ID, files[name], 0755, readRoot, s.Run, verifyApprovedExecutable); e != nil {
			return zero, e
		}
	}
	if e := probeRollbackCompatibility(ctx, t, s); e != nil {
		return zero, e
	}
	if scoped {
		if e = prepareMigrationRunner(ctx, t, readRoot, s.Run, verifyApprovedExecutable, probeMigrationContract); e != nil {
			return zero, e
		}
		if e = prepareAtomicPair(ctx, t, pairPlan, readRoot, s.Run, verifyApprovedExecutable, func() (bool, error) { return atomicPolicyExists(ctx, t, s) }); e != nil {
			return zero, e
		}
	}
	if e := publish(ctx, t.dir()+"/transaction.json", m.ID, mustJSON(t), 0644, readRoot, s.Run, verifyApprovedExecutable); e != nil {
		return zero, e
	}
	return t, nil
}
func probeRollbackCompatibility(ctx context.Context, t InstallTransaction, s *System) error {
	return checkRollbackCompatibility(t, func(name string, core bool) ([]byte, error) {
		i := t.Base.Signer
		if core {
			i = t.Base.App
		}
		cmd := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(i.UID), "--regid", strconv.Itoa(i.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", t.dir() + "/" + name}
		return s.Run(ctx, nil, append(cmd, rollbackCompatibilityArgs(t.Base.Version, core)...)...)
	})
}

func rollbackCompatibilityArgs(version int, core bool) []string {
	name := "fixture-compatibility"
	if core {
		name = "compatibility"
	}
	if version == 2 {
		name = "custody-compatibility"
	} else if version == 3 {
		name = "ordinary-custody-compatibility"
	}
	if core {
		return []string{"updates", name}
	}
	return []string{name}
}

// Both preparation paths apply the same compatibility and held-capability
// checks; callers differ only in the identity used to run the static probe.
func checkRollbackCompatibility(t InstallTransaction, output func(string, bool) ([]byte, error)) error {
	probe := func(name string, core bool) (update.Contract, error) {
		b, e := output(name, core)
		if e != nil {
			return update.Contract{}, e
		}
		var c update.Contract
		if core {
			var x struct {
				Contract           update.Contract `json:"contract"`
				WalletSigner       *bool           `json:"walletSigner"`
				FinancialExecution *bool           `json:"financialExecution"`
				UpdaterEnabled     *bool           `json:"updaterEnabled"`
			}
			if e = json.Unmarshal(b, &x); e != nil {
				return c, e
			}
			c = x.Contract
			if x.WalletSigner == nil || *x.WalletSigner || x.FinancialExecution == nil || *x.FinancialExecution || x.UpdaterEnabled == nil || *x.UpdaterEnabled {
				return c, errors.New("candidate financial/automatic-update capabilities must be explicitly disabled")
			}
		} else if e = update.StrictJSON(b, &c); e != nil {
			return c, e
		}
		return c, nil
	}
	oldCore, e := probe("runner", true)
	if e != nil {
		return e
	}
	newCore, e := probe("next-core", true)
	if e != nil {
		return e
	}
	oldSigner, e := probe("previous-signer", false)
	if e != nil {
		return e
	}
	newSigner, e := probe("next-signer", false)
	if e != nil {
		return e
	}
	for _, claimed := range t.Binding.Contracts {
		actual := newCore
		if claimed.Component == "signer" {
			actual = newSigner
		}
		if update.SnapshotDigest("contract", claimed) != update.SnapshotDigest("contract", actual) {
			return errors.New("candidate contract probe differs from signed manifest")
		}
	}
	for _, c := range []update.Contract{oldCore, newCore} {
		if c.Lifecycle == nil || c.Lifecycle.Entrypoint != 4 {
			return errors.New("both helpers must read installation-controller records")
		}
	}
	for _, c := range []update.Contract{oldSigner, newSigner} {
		found := false
		for _, v := range c.State {
			if v.Name == "operations" {
				for _, f := range v.Features {
					if f == "install-completion-v1" {
						found = true
					}
				}
			}
		}
		if !found {
			return errors.New("both signers must preserve installation holds during rollback")
		}
	}
	if oldCore.Protocol != newCore.Protocol || oldSigner.Protocol != newSigner.Protocol || update.SnapshotDigest("state-contract", oldCore.State) != update.SnapshotDigest("state-contract", newCore.State) || update.SnapshotDigest("state-contract", oldSigner.State) != update.SnapshotDigest("state-contract", newSigner.State) {
		return errors.New("trial installation supports unchanged state formats only; migrations unavailable")
	}
	return nil
}

type InstallationCallbacks struct {
	Observe func(context.Context, Manifest, update.InstallBinding) error
	Hold    func(context.Context, string, update.InstallBinding) (*update.Installation, error)
	State   func(context.Context) (*update.Installation, error)
	Release func(context.Context, update.Installation) error
}

func ExecuteController(ctx context.Context, t InstallTransaction, rollback bool, s *System, c InstallationCallbacks) (*update.Installation, error) {
	if !t.valid() {
		return nil, errors.New("invalid installation transaction")
	}
	if os.Getuid() != t.Base.Owner || os.Geteuid() != os.Getuid() {
		return nil, errors.New("ordinary installation owner required")
	}
	_, e := ControllerRunner(t.Base.Owner, t.Binding.Index.Binding.Pause.Request.IntentID)
	if e != nil {
		return nil, e
	}
	o := installOps{read: readRoot, run: s.Run, verify: verifyApprovedExecutable, state: func(ctx context.Context) (*update.Installation, error) {
		return currentInstallation(ctx, t, s, c.State)
	}, topology: func(m Manifest) error {
		if e := verifyTopology(m); e != nil {
			return e
		}
		for _, role := range []string{"app", "signer"} {
			if e := loadedUnit(m, role); e != nil {
				return e
			}
		}
		return nil
	}, health: func(ctx context.Context, m Manifest) error { return waitReady(ctx, m, s) }, observe: c.Observe, hold: c.Hold, checkpoint: func(phase string) error {
		fmt.Fprintln(s.Out, "Installation boundary:", phase)
		if s.installFault != nil {
			return s.installFault(phase)
		}
		return nil
	}}
	scoped, err := scopedMigrationEnabled(t.Base.Owner)
	if err != nil {
		return nil, err
	}
	if scoped {
		plan, err := admitAtomicPair(ctx, t, s)
		if err != nil {
			return nil, err
		}
		o.atomic = func(ctx context.Context, action string) error { return runAtomicWorker(ctx, t, s, plan, action) }
		return runAtomicTransaction(ctx, t, rollback, o)
	}
	return runTransaction(ctx, t, rollback, o)
}

func InstallationStatus(ctx context.Context, t InstallTransaction, s *System, c InstallationCallbacks, out io.Writer) error {
	if _, e := ControllerRunner(t.Base.Owner, t.Binding.Index.Binding.Pause.Request.IntentID); e != nil {
		return e
	}
	m, e := Load(t.Base.Owner)
	if e != nil {
		return e
	}
	if e = waitReady(ctx, m, s); e != nil {
		return e
	}
	i, e := c.State(ctx)
	if e == nil && i != nil && i.Phase != "completed" {
		e = c.Observe(ctx, m, t.Binding)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(struct {
		Transaction        InstallTransaction   `json:"transaction"`
		Installation       *update.Installation `json:"installation"`
		FinancialExecution bool                 `json:"financialExecution"`
		ResumeAllowed      bool                 `json:"resumeAllowed"`
	}{Transaction: t, Installation: i})
}

// Cold inspection is allowed only while systemd proves the signer inactive.
// The retained signer executes after a fixed drop to the configured signer UID.
func currentInstallation(ctx context.Context, t InstallTransaction, s *System, state func(context.Context) (*update.Installation, error)) (*update.Installation, error) {
	i, e := state(ctx)
	if e == nil {
		return i, nil
	}
	if errors.Is(e, signer.ErrNoInstallationHold) {
		return nil, e
	}
	b, se := s.Run(ctx, nil, "/usr/bin/systemctl", "show", Unit(t.Base.Owner, "signer"), "--property=ActiveState,MainPID")
	if se != nil {
		return nil, se
	}
	if string(b) != "ActiveState=inactive\nMainPID=0\n" && string(b) != "MainPID=0\nActiveState=inactive\n" {
		return nil, e
	}
	p := t.Base.Signer
	scoped, se := scopedMigrationEnabled(t.Base.Owner)
	if se != nil {
		return nil, se
	}
	reader, action := t.dir()+"/previous-signer", "fixture-install-inspect"
	if scoped {
		reader, action = t.dir()+"/atomic-guard", "fixture-install-guard-status"
		pin, pinErr := readReusableMaintenanceAuthority(t.Base, readRoot)
		if pinErr != nil {
			return nil, pinErr
		}
		if pin != nil {
			if pinErr = verifyReusableMaintenanceAuthority(t.Base, pin, pin.RunnerSHA256, pin.GuardSHA256, verifiedMigrationRunnerSize); pinErr != nil {
				return nil, pinErr
			}
			reader = reusableMaintenancePath(t.Base, "atomic-guard")
		}
	}
	args := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(p.UID), "--regid", strconv.Itoa(p.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", reader, action, "--disposable-fixture", "--config", t.Base.Root + "/identity.json", "--intent-id", t.Binding.Index.Binding.Pause.Request.IntentID}
	if scoped {
		args = append(args, "--guard-digest", update.InstallDigest(t.Binding))
	}
	b, se = s.Run(ctx, nil, args...)
	if se != nil {
		return nil, se
	}
	var inspected update.Installation
	if update.StrictJSON(b, &inspected) != nil || !update.ValidInstallation(inspected) {
		return nil, errors.New("invalid cold signer hold receipt")
	}
	return &inspected, nil
}
