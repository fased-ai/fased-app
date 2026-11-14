//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// reusableMaintenanceAuthority is installed once by the OS administrator.
// Owner-controlled update staging cannot choose either privileged boundary
// reader: both are pinned outside the transaction's approved-input directory.
type reusableMaintenanceAuthority struct {
	Version        int    `json:"version"`
	Owner          int    `json:"owner"`
	InstallationID string `json:"installationId"`
	RunnerSHA256   string `json:"runnerSha256"`
	GuardSHA256    string `json:"guardSha256"`
}

type reusableMaintenanceMarker struct {
	Version         int    `json:"version"`
	Owner           int    `json:"owner"`
	InstallationID  string `json:"installationId"`
	AuthoritySHA256 string `json:"authoritySha256"`
}

type reusableAtomicRequest struct {
	Version        int    `json:"version"`
	Owner          int    `json:"owner"`
	InstallationID string `json:"installationId"`
	IntentID       string `json:"intentId"`
	Direction      string `json:"direction"`
	RequestID      string `json:"requestId,omitempty"`
	StageKind      string `json:"stageKind,omitempty"`
	StageSlot      string `json:"stageSlot,omitempty"`
	Probe          string `json:"probe,omitempty"`
}

func validateReusableAtomicRequest(b []byte, owner int) (reusableAtomicRequest, error) {
	var r reusableAtomicRequest
	if owner <= 0 || strict(b, &r) != nil || r.Version != 1 || r.Owner != owner || !update.IDOK(r.InstallationID) || !update.IDOK(r.IntentID) || (r.RequestID != "" && !update.IDOK(r.RequestID)) || !bytes.Equal(b, mustJSON(r)) {
		return reusableAtomicRequest{}, errors.New("invalid reusable maintenance request")
	}
	switch r.Direction {
	case "stage":
		if _, err := reusableStageName(owner, r.StageKind, r.StageSlot); err != nil || r.Probe != "" {
			return reusableAtomicRequest{}, errors.New("unsupported maintenance stage")
		}
	case "compatibility":
		if r.StageKind != "" || r.StageSlot != "" || !slices.Contains(reusableCompatibilitySlots, r.Probe) {
			return reusableAtomicRequest{}, errors.New("unsupported compatibility probe")
		}
	case "fence", "prepare", "forward", "rollback", "completion", "cleanup-rollback", "cleanup-complete":
		if r.StageKind != "" || r.StageSlot != "" || r.Probe != "" {
			return reusableAtomicRequest{}, errors.New("stage fields require a stage operation")
		}
	default:
		return reusableAtomicRequest{}, errors.New("unsupported reusable maintenance action")
	}
	return r, nil
}

func reusableMaintenanceMarkerBytes(a reusableMaintenanceAuthority) []byte {
	return mustJSON(reusableMaintenanceMarker{Version: 1, Owner: a.Owner, InstallationID: a.InstallationID, AuthoritySHA256: sum(mustJSON(a))})
}

func reusableMaintenancePath(m Manifest, name string) string {
	return filepath.Join(m.Root, "maintenance", name)
}

func reusableAtomicLauncherName(owner int) string {
	return fmt.Sprintf("fased-custody-%d-atomic-launcher.service", owner)
}

func reusableAtomicProvisionName(owner int) string {
	return fmt.Sprintf("fased-custody-%d-atomic-provision.service", owner)
}

func reusablePreparationFenceName(owner int) string {
	return fmt.Sprintf("fased-custody-%d-atomic-fence.service", owner)
}

func reusableAtomicSetupName(owner int) string {
	return fmt.Sprintf("fased-custody-%d-atomic-setup.service", owner)
}

var reusableCompatibilitySlots = []string{"runner", "next-core", "previous-signer", "next-signer"}

func reusableCompatibilityName(owner int, slot string) (string, error) {
	if owner <= 0 || !slices.Contains(reusableCompatibilitySlots, slot) {
		return "", errors.New("unsupported compatibility probe")
	}
	return fmt.Sprintf("fased-custody-%d-atomic-compat-%s.service", owner, slot), nil
}

func reusableAtomicPolicyPath(owner int) string {
	return fmt.Sprintf("/etc/sudoers.d/fased-custody-%d-reusable", owner)
}

func reusablePolicyCandidate(m Manifest, slotID string) string {
	return fmt.Sprintf("/etc/sudoers.d/.fased-custody-%d-reusable-%s.pending", m.Owner, slotID)
}

func reusablePolicySelected(m Manifest) string {
	return fmt.Sprintf("/etc/sudoers.d/.fased-custody-%d-reusable-%s.selected", m.Owner, m.ID)
}

func reusablePolicySlotChecksum(m Manifest, slot int) string {
	return reusableMaintenancePath(m, fmt.Sprintf("policy-slot-%d.sha256", slot))
}

func reusableSetupGrantPath(m Manifest) string {
	return fmt.Sprintf("/etc/sudoers.d/fased-custody-%d-setup-%s", m.Owner, m.ID)
}

func reusableSetupGrantSource(m Manifest) string {
	return fmt.Sprintf("/etc/sudoers.d/.fased-custody-%d-setup-%s.source", m.Owner, m.ID)
}

func appendReusableGuardPreamble(unit *strings.Builder, m Manifest, a reusableMaintenanceAuthority, env string) {
	guard := reusableMaintenancePath(m, "atomic-guard")
	checksum := reusableMaintenancePath(m, "guard.sha256")
	line := a.GuardSHA256 + "  " + guard
	fmt.Fprintf(unit, "User=%d\nGroup=%d\nNoNewPrivileges=yes\nPrivateTmp=yes\nProtectHome=yes\nProtectSystem=strict\nReadWritePaths=%s\n", m.Signer.UID, m.Signer.GID, reusableMaintenancePath(m, ""))
	fmt.Fprintf(unit, "EnvironmentFile=-%s\nStandardOutput=append:%s\nStandardError=journal\n", env, env)
	for _, path := range []string{env, env + ".owner"} {
		fmt.Fprintf(unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0644 ) -exec /usr/bin/false {} +\n", path)
	}
	fmt.Fprintf(unit, "ExecStartPre=+/usr/bin/test %s -ef %s.owner\n", env, env)
	fmt.Fprintf(unit, "ExecStartPre=+/usr/bin/truncate --size=0 %s\n", env)
	for _, parent := range []string{"/", "/var", "/var/lib", m.Root, reusableMaintenancePath(m, "")} {
		fmt.Fprintf(unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type d -o ! -uid 0 -o -perm /022 ) -exec /usr/bin/false {} +\n", parent)
	}
	fmt.Fprintf(unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0755 ) -exec /usr/bin/false {} +\n", guard)
	fmt.Fprintf(unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0644 ) -exec /usr/bin/false {} +\n", checksum)
	fmt.Fprintf(unit, "ExecStartPre=+/usr/bin/grep -Fqx -- \"%s\" %s\nExecStartPre=+/usr/bin/sha256sum --check --status -- %s\n", line, checksum, checksum)
}

var reusableStageSlots = map[string][]string{
	"image": {"runner", "previous-signer", "next-core", "next-signer"},
	"meta":  {"transaction", "approval", "base-install", "target-install", "atomic-rollback", "base-intent", "target-intent", "base-ready", "target-ready", "atomic-held", "atomic-active", "atomic-marker-forward", "atomic-marker-rollback", "atomic-marker-completion", "atomic-marker-cleanup-rollback", "atomic-marker-cleanup-complete", "atomic-markers", "atomic-checksums"},
	"unit":  {"unit-forward", "unit-rollback", "unit-completion", "unit-cleanup-rollback", "unit-cleanup-complete"},
}

var reusableMetadataFiles = []struct{ slot, name string }{
	{"transaction", "transaction.json"}, {"approval", "migration-runner.json"},
	{"base-install", "base-install.json"}, {"target-install", "target-install.json"}, {"atomic-rollback", "atomic-rollback.json"},
	{"base-intent", "base-intent.json"}, {"target-intent", "target-intent.json"}, {"base-ready", "base-ready.json"}, {"target-ready", "target-ready.json"},
	{"atomic-held", "atomic-held.json"}, {"atomic-active", "atomic-active.json"},
	{"atomic-marker-forward", "atomic-marker-forward.json"}, {"atomic-marker-rollback", "atomic-marker-rollback.json"},
	{"atomic-marker-completion", "atomic-marker-completion.json"}, {"atomic-marker-cleanup-rollback", "atomic-marker-cleanup-rollback.json"}, {"atomic-marker-cleanup-complete", "atomic-marker-cleanup-complete.json"},
	{"atomic-markers", "atomic-markers.txt"}, {"atomic-checksums", "atomic-checksums.sha256"},
}

var reusableWorkerDirections = []string{"forward", "rollback", "completion", "cleanup-rollback", "cleanup-complete"}

var reusableStageKinds = []string{"image", "meta", "unit"}

func reusableStageClass(kind string) (base, suffix string, err error) {
	if _, ok := reusableStageSlots[kind]; !ok {
		return "", "", errors.New("unsupported maintenance staging class")
	}
	return kind, "", nil
}

func reusableStageName(owner int, kind, slot string) (string, error) {
	base, _, err := reusableStageClass(kind)
	if err != nil || owner <= 0 || !slices.Contains(reusableStageSlots[base], slot) {
		return "", errors.New("unsupported bounded maintenance stage")
	}
	return fmt.Sprintf("fased-custody-%d-stage-%s@%s.service", owner, kind, slot), nil
}

func reusableStageAllowlist(kind string) ([]byte, error) {
	base, _, err := reusableStageClass(kind)
	if err != nil {
		return nil, errors.New("unsupported maintenance staging class")
	}
	slots := reusableStageSlots[base]
	return []byte(strings.Join(slots, "\n") + "\n"), nil
}

// The owner supplies bytes without root read access to its paths. Systemd
// copies them into a separate root-owned inode before publishing a fixed slot.
func reusableStageTemplate(m Manifest, ownerGID int, kind string) ([]byte, error) {
	if m.validate() != nil || ownerGID <= 0 {
		return nil, errors.New("valid maintenance staging identity required")
	}
	base, suffix, err := reusableStageClass(kind)
	if err != nil {
		return nil, err
	}
	limit, mode := 0, "0644"
	switch base {
	case "image":
		limit, mode = 64<<20, "0755"
	case "meta":
		limit = 4 << 20
	case "unit":
		limit = 32768
	default:
		return nil, errors.New("unsupported maintenance staging class")
	}
	stage := reusableMaintenancePath(m, "staging")
	inbox := reusableMaintenancePath(m, "inbox")
	attempt := stage + "/.stage-%i" + suffix + "-${INVOCATION_ID}.d"
	input, copied, final := attempt+"/input", attempt+"/copy", stage+"/%i"
	var unit strings.Builder
	unit.WriteString("[Unit]\nDescription=Fased bounded maintenance staging\n")
	fmt.Fprintf(&unit, "AssertPathExists=!%s\n", final)
	fmt.Fprintf(&unit, "AssertPathIsSymbolicLink=!%s\nAssertPathIsSymbolicLink=!%s\n\n[Service]\nType=oneshot\nUser=%d\nGroup=%d\nNoNewPrivileges=yes\nPrivateTmp=yes\nProtectHome=yes\nProtectSystem=strict\n", stage, final, m.Owner, ownerGID)
	fmt.Fprintf(&unit, "ReadWritePaths=%s\nLimitFSIZE=%d\nLimitCORE=0\nUMask=0022\nStandardOutput=journal\nStandardError=journal\n", stage, limit)
	for _, parent := range []string{"/", "/var", "/var/lib", m.Root, reusableMaintenancePath(m, ""), stage} {
		fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type d -o ! -uid 0 -o -perm /022 ) -exec /usr/bin/false {} +\n", parent)
	}
	allow := reusableMaintenancePath(m, "stage-"+base+".allow")
	fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0644 ) -exec /usr/bin/false {} +\n", allow)
	fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/grep -Fxq -- %%i %s\n", allow)
	// The pinned signer-side guard checks the source and invocation before any
	// attempt directory is created. Prior attempts remain untouched.
	guard := reusableMaintenancePath(m, "atomic-guard")
	checksum := reusableMaintenancePath(m, "guard.sha256")
	fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0755 ) -exec /usr/bin/false {} +\n", guard)
	fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/sha256sum --check --status %s\n", checksum)
	fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/setpriv --reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-validate-stage-recovery --owner %d --kind %s --slot %%i --worker --disposable-fixture\n", m.Signer.UID, m.Signer.GID, guard, m.Owner, kind)
	// A private root-owned directory prevents the owner from replacing the
	// precreated input path. A fresh systemd invocation never reuses a partial.
	fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/mkdir -- %s\nExecStartPre=+/usr/bin/chmod 0711 %s\nExecStartPre=+/usr/bin/install -m 0600 -o %d -g %d -- /dev/null %s\n", attempt, attempt, m.Owner, ownerGID, input)
	fmt.Fprintf(&unit, "ExecStart=/usr/bin/cp --no-preserve=mode -- %s/%%i %s\n", inbox, input)
	fmt.Fprintf(&unit, "ExecStartPost=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid %d -o ! -gid %d -o ! -perm 0600 -o ! -links 1 -o -empty -o -size +%dc ) -exec /usr/bin/false {} +\n", input, m.Owner, ownerGID, limit)
	// The owner may still hold a writable descriptor. Bound the privileged OS
	// copy itself, rather than relying on the service's unprivileged limit.
	fmt.Fprintf(&unit, "ExecStartPost=+/usr/bin/dd if=%s of=%s bs=65536 count=%d iflag=fullblock conv=excl status=none\n", input, copied, limit/65536+1)
	fmt.Fprintf(&unit, "ExecStartPost=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -links 1 -o -empty -o -size +%dc ) -exec /usr/bin/false {} +\n", copied, limit)
	fmt.Fprintf(&unit, "ExecStartPost=+/usr/bin/setpriv --reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-validate-stage-recovery --owner %d --kind %s --slot %%i --copy --disposable-fixture\n", m.Signer.UID, m.Signer.GID, guard, m.Owner, kind)
	fmt.Fprintf(&unit, "ExecStartPost=+/usr/bin/cmp --silent -- %s %s\nExecStartPost=+/usr/bin/chmod %s %s\nExecStartPost=+/usr/bin/sync -f %s\n", input, copied, mode, copied, copied)
	fmt.Fprintf(&unit, "ExecStartPost=+/usr/bin/ln --no-target-directory -- %s %s\nExecStartPost=+/usr/bin/sync -f %s\nExecStartPost=+/usr/bin/sync -f %s\nExecStartPost=+/usr/bin/rm -- %s %s\nExecStartPost=+/usr/bin/rmdir -- %s\n", copied, final, final, stage, copied, input, attempt)
	return []byte(unit.String()), nil
}

// Systemd opens a separate output as root for each unprivileged compatibility
// command. A transient identity and denied network/async-I/O syscalls prevent
// owner processes from controlling a probe or retaining its output descriptor.
func reusableCompatibilityUnit(m Manifest, ownerGID int, slot string) ([]byte, error) {
	if m.validate() != nil || ownerGID <= 0 || !slices.Contains(reusableCompatibilitySlots, slot) {
		return nil, errors.New("valid maintenance compatibility identity required")
	}
	stage := reusableMaintenancePath(m, "staging")
	out := reusableMaintenancePath(m, "compat-"+slot+".out")
	var unit strings.Builder
	fmt.Fprintf(&unit, "[Unit]\nDescription=Fased paired update compatibility\nAssertPathExists=%s\nAssertPathExists=%s.owner\nAssertPathIsSymbolicLink=!%s\nAssertPathIsSymbolicLink=!%s.owner\n\n[Service]\nType=oneshot\nKillMode=control-group\nTimeoutStartSec=1min\nDynamicUser=yes\nNoNewPrivileges=yes\nPrivateTmp=yes\nPrivateNetwork=yes\nSystemCallArchitectures=native\nSystemCallFilter=~@network-io @aio\nProtectHome=yes\nProtectSystem=strict\nReadWritePaths=%s\nLimitFSIZE=65536\nLimitCORE=0\nUMask=0022\nStandardOutput=append:%s\nStandardError=journal\n", out, out, out, out, reusableMaintenancePath(m, ""), out)
	for _, path := range []string{out, out + ".owner"} {
		fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0644 -o ! -links 2 -o -size +65536c ) -exec /usr/bin/false {} +\n", path)
	}
	fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/test %s -ef %s.owner\nExecStartPre=+/usr/bin/truncate --size=0 %s\nExecStart=/usr/bin/sha256sum -- %s/transaction\n", out, out, out, stage)
	core := slot == "runner" || slot == "next-core"
	fmt.Fprintf(&unit, "ExecStart=%s/%s %s\n", stage, slot, strings.Join(rollbackCompatibilityArgs(m.Version, core), " "))
	fmt.Fprintf(&unit, "ExecStartPost=+/usr/bin/sync -f %s\n", out)
	return []byte(unit.String()), nil
}

// The fixed sentinel only denies service starts. The signer guard must inspect
// its actual cold hold after the pair is stopped; this unit cannot replace any
// application, signer or transaction file.
func reusablePreparationFenceUnit(m Manifest, a reusableMaintenanceAuthority) ([]byte, error) {
	if _, _, err := reusableAtomicLauncher(m, a); err != nil {
		return nil, err
	}
	sentinel := reusableMaintenancePath(m, "preparation-fence.json")
	checksum := reusableMaintenancePath(m, "preparation-fence.sha256")
	marker := m.Root + "/update-start-blocked.json"
	guard := reusableMaintenancePath(m, "atomic-guard")
	contents, err := signer.InstallationPreparationFenceBytes(m.Owner, m.ID)
	if err != nil {
		return nil, err
	}
	var unit strings.Builder
	fmt.Fprintf(&unit, "[Unit]\nDescription=Fased guarded custody preparation\n\n[Service]\nType=oneshot\nKillMode=control-group\nTimeoutStartSec=1min\nUser=%d\nGroup=%d\nNoNewPrivileges=yes\nPrivateTmp=yes\nProtectHome=yes\nProtectSystem=strict\nReadWritePaths=%s\n", m.Signer.UID, m.Signer.GID, m.Root)
	for _, parent := range []string{"/", "/var", "/var/lib", m.Root, reusableMaintenancePath(m, "")} {
		fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type d -o ! -uid 0 -o -perm /022 ) -exec /usr/bin/false {} +\n", parent)
	}
	for _, file := range []string{sentinel, checksum} {
		fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0644 ) -exec /usr/bin/false {} +\n", file)
	}
	fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/grep -Fqx -- \"%s  %s\" %s\nExecStartPre=+/usr/bin/sha256sum --check --status -- %s\n", sum(contents), sentinel, checksum, checksum)
	fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0755 ) -exec /usr/bin/false {} +\n", guard)
	guardChecksum := reusableMaintenancePath(m, "guard.sha256")
	fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0644 ) -exec /usr/bin/false {} +\n", guardChecksum)
	fmt.Fprintf(&unit, "ExecStartPre=+/usr/bin/grep -Fqx -- \"%s  %s\" %s\nExecStartPre=+/usr/bin/sha256sum --check --status -- %s\n", a.GuardSHA256, guard, guardChecksum, guardChecksum)
	fmt.Fprintf(&unit, "ExecStartPre=%s fixture-validate-preparation-fence --capability\n", guard)
	fmt.Fprintf(&unit, "ExecStartPre=%s fixture-validate-preparation-start --owner %d --disposable-fixture\n", guard, m.Owner)
	fmt.Fprintf(&unit, "ExecStart=-+/usr/bin/ln --no-target-directory -- %s %s\nExecStart=+/usr/bin/test %s -ef %s\nExecStart=+/usr/bin/sync -f %s\n", sentinel, marker, sentinel, marker, m.Root)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/systemctl stop -- %s %s\n", Unit(m.Owner, "app"), Unit(m.Owner, "signer"))
	fmt.Fprintf(&unit, "ExecStart=%s fixture-validate-preparation-fence --owner %d --disposable-fixture\n", guard, m.Owner)
	return []byte(unit.String()), nil
}

// The fixed launcher permits only the signer-owned guard to select a worker.
// Other staging and compatibility services still require a shared transaction
// boundary; systemd may coalesce concurrent starts of this launcher.
func reusableAtomicLauncher(m Manifest, a reusableMaintenanceAuthority) ([]byte, []byte, error) {
	if m.validate() != nil || a.Version != 1 || a.Owner != m.Owner || a.InstallationID != m.ID || !validHex(a.GuardSHA256, 32) || !validHex(a.RunnerSHA256, 32) || a.GuardSHA256 == m.SignerHash || a.RunnerSHA256 == m.CoreHash {
		return nil, nil, errors.New("valid independent maintenance authority required")
	}
	guard := reusableMaintenancePath(m, "atomic-guard")
	env := reusableMaintenancePath(m, "worker.env")
	line := a.GuardSHA256 + "  " + guard
	pattern := fmt.Sprintf(`^FASED_REQUEST_ID=[0-9a-f]{32}.FASED_WORKER_UNIT=(fased-custody-%d-atomic-[0-9a-f]{32}-(forward|rollback|completion|cleanup-rollback|cleanup-complete)|fased-custody-%d-atomic-(fence|provision|compat-(runner|next-core|previous-signer|next-signer))|fased-custody-%d-stage-(image|meta|unit)@[a-z0-9-]+)[.]service.`, m.Owner, m.Owner, m.Owner)
	var unit strings.Builder
	// One update legitimately invokes the same launcher for each fixed stage.
	unit.WriteString("[Unit]\nDescription=Fased protected custody maintenance\nStartLimitIntervalSec=60\nStartLimitBurst=64\n\n[Service]\nType=oneshot\nKillMode=control-group\nTimeoutStartSec=15min\nRestart=no\nLimitFSIZE=4096\nLimitCORE=0\n")
	appendReusableGuardPreamble(&unit, m, a, env)
	fmt.Fprintf(&unit, "ExecStart=%s fixture-validate-atomic-worker --owner %d --disposable-fixture\n", guard, m.Owner)
	// -z makes the complete, unterminated environment file one grep record.
	// A partial or additional line must not reach either privileged systemctl.
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/grep -zEq \"%s$$\" %s\nExecStart=+/usr/bin/systemctl start -- ${FASED_WORKER_UNIT}\n", pattern, env)
	unit.WriteString("ExecStart=+/usr/bin/printf FASED_WORKER_COMPLETED=yes\n")
	fmt.Fprintf(&unit, "ExecStopPost=+/usr/bin/grep -zEq \"%s(FASED_WORKER_COMPLETED=yes)?$$\" %s\nExecStopPost=+/usr/bin/systemctl stop -- ${FASED_WORKER_UNIT}\n", pattern, env)
	return []byte(unit.String()), []byte(line + "\n"), nil
}

// A single root-managed service publishes the recurring policy only after its
// unprivileged signer guard has checked the complete loaded configuration.
func reusableAtomicSetup(m Manifest, a reusableMaintenanceAuthority, ownerName string) ([]byte, []byte, error) {
	if _, _, err := reusableAtomicLauncher(m, a); err != nil {
		return nil, nil, err
	}
	policy, err := reusableAtomicPolicy(m, ownerName)
	if err != nil {
		return nil, nil, err
	}
	pending, final := reusablePolicySelected(m), reusableAtomicPolicyPath(m.Owner)
	checksumPath := reusableMaintenancePath(m, "policy.sha256")
	checksum := []byte(sum(policy) + "  " + pending + "\n")
	env := reusableMaintenancePath(m, "setup.env")
	var unit strings.Builder
	unit.WriteString("[Unit]\nDescription=Fased protected maintenance setup\n\n[Service]\nType=oneshot\nKillMode=control-group\nTimeoutStartSec=15min\nRestart=on-failure\nRestartSec=2s\nLimitFSIZE=4096\nLimitCORE=0\n")
	appendReusableGuardPreamble(&unit, m, a, env)
	unit.WriteString("ReadWritePaths=/etc/sudoers.d\n")
	fmt.Fprintf(&unit, "StandardInput=file:%s\n", reusableSetupGrantSource(m))
	fmt.Fprintf(&unit, "ExecStart=%s fixture-validate-reusable-setup --owner %d --disposable-fixture\n", reusableMaintenancePath(m, "atomic-guard"), m.Owner)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/grep -zFxq -- FASED_SETUP_READY=yes %s\n", env)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find /etc/sudoers.d -maxdepth 0 ( ! -type d -o ! -uid 0 -o -perm /022 ) -exec /usr/bin/false {} +\n")
	// Select only a complete checksum-bound policy. Earlier incomplete slots
	// are left untouched; the fixed selected link is created without clobbering.
	for n, slotID := range reusableSetupSlotIDs(m.ID) {
		candidate := reusablePolicyCandidate(m, slotID)
		fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find /etc/sudoers.d -maxdepth 1 -name %s -type f -uid 0 -gid 0 -perm 0440 -exec /usr/bin/sha256sum --check --status -- %s \\; -exec /usr/sbin/visudo -cf {} \\; -exec /usr/bin/ln --no-target-directory -- {} %s \\;\n", filepath.Base(candidate), reusablePolicySlotChecksum(m, n), pending)
	}
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/test ! -L %s\n", pending)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0440 ) -exec /usr/bin/false {} +\n", pending)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/grep -Fqx -- \"%s  %s\" %s\n", sum(policy), pending, checksumPath)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/sha256sum --check --status -- %s\n", checksumPath)
	fmt.Fprintf(&unit, "ExecStart=+/usr/sbin/visudo -cf %s\n", pending)
	grant, source := reusableSetupGrantPath(m), reusableSetupGrantSource(m)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0440 ) -exec /usr/bin/false {} +\n", source)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find /etc/sudoers.d -maxdepth 1 -name %s -type f -uid 0 -gid 0 -perm 0440 -samefile %s -delete\n", filepath.Base(grant), source)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/test ! -e %s\nExecStart=+/usr/bin/test ! -L %s\nExecStart=+/usr/bin/sync -f /etc/sudoers.d\n", grant, grant)
	fmt.Fprintf(&unit, "ExecStart=-+/usr/bin/ln --no-target-directory -- %s %s\n", pending, final)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0440 ) -exec /usr/bin/false {} +\n", final)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/test %s -ef %s\nExecStart=+/usr/bin/sync -f %s\nExecStart=+/usr/bin/sync -f /etc/sudoers.d\n", pending, final, final)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/systemctl disable -- %s\nExecStartPost=+/usr/bin/rm -- %s\n\n[Install]\nWantedBy=multi-user.target\n", reusableAtomicSetupName(m.Owner), source)
	return []byte(unit.String()), checksum, nil
}

// Provision links only signer-checked fixed slots. The active transaction link
// bounds preparation to one intent until guarded completion or recovery.
func reusableAtomicProvision(m Manifest, a reusableMaintenanceAuthority) ([]byte, error) {
	if _, _, err := reusableAtomicLauncher(m, a); err != nil {
		return nil, err
	}
	stage := reusableMaintenancePath(m, "staging")
	active := reusableMaintenancePath(m, "active.json")
	env := reusableMaintenancePath(m, "provision.env")
	txdir := m.Root + "/updates/${FASED_TXID}"
	attempt := fmt.Sprintf("/etc/systemd/system/.fased-custody-%d-atomic-${FASED_TXID}-${INVOCATION_ID}.d", m.Owner)
	guard := reusableMaintenancePath(m, "atomic-guard")
	var unit strings.Builder
	unit.WriteString("[Unit]\nDescription=Fased protected custody preparation\n\n[Service]\nType=oneshot\nKillMode=control-group\nTimeoutStartSec=15min\nRestart=no\nLimitFSIZE=32768\nLimitCORE=0\n")
	appendReusableGuardPreamble(&unit, m, a, env)
	fmt.Fprintf(&unit, "ReadWritePaths=%s/updates /etc/systemd/system\n", m.Root)
	fmt.Fprintf(&unit, "ExecStart=%s fixture-validate-atomic-provision --verify-only-capability\n", guard)
	fmt.Fprintf(&unit, "ExecStart=%s fixture-validate-atomic-provision --published-capability\n", guard)
	fmt.Fprintf(&unit, "ExecStart=%s fixture-validate-atomic-provision --owner %d --disposable-fixture\n", guard, m.Owner)
	fmt.Fprintf(&unit, "ExecStart=%s fixture-install-guard --predecessor-capability\n", guard)
	// An older pinned reader cannot run the cold-backup guard. Refuse before
	// publishing the transaction's privileged worker units.
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/setpriv --reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-update-pause-guard --recovery-capability\n", m.App.UID, m.App.GID, reusableMaintenancePath(m, "runner"))
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/setpriv --reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-update-pause-guard --staged-recovery-capability\n", m.App.UID, m.App.GID, reusableMaintenancePath(m, "runner"))
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/setpriv --reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-update-pause-guard --sentinel-staged-recovery-capability\n", m.App.UID, m.App.GID, reusableMaintenancePath(m, "runner"))
	// The previous signer guard authenticated this root-staged binding and the
	// stopped services. The app UID now verifies its original recovery snapshot.
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/setpriv --reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-update-pause-guard --owner %d --disposable-fixture --verify-staged-recovery\n", m.App.UID, m.App.GID, reusableMaintenancePath(m, "runner"), m.Owner)
	// Recheck the cold signer and fence after the app-side check; the OS policy
	// must still serialize all privileged work until publication completes.
	fmt.Fprintf(&unit, "ExecStart=%s fixture-validate-atomic-provision --owner %d --disposable-fixture --verify-only\n", guard, m.Owner)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/grep -zEq \"^FASED_TXID=[0-9a-f]{32}$$\" %s\n", env)
	// mkdir is exclusive: an unknown directory or symlink is never reused.
	// Systemd supplies a new, guard-validated invocation ID for each retry.
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/mkdir --mode=0700 -- %s\n", attempt)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find %s -maxdepth 0 ( ! -type d -o ! -uid 0 -o ! -gid 0 -o ! -perm 0700 ) -exec /usr/bin/false {} +\n", attempt)
	fmt.Fprintf(&unit, "ExecStart=-+/usr/bin/ln --no-target-directory -- %s/transaction %s\nExecStart=+/usr/bin/test %s/transaction -ef %s\n", stage, active, stage, active)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0644 ) -exec /usr/bin/false {} +\nExecStart=+/usr/bin/sync -f %s\n", active, reusableMaintenancePath(m, ""))
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/mkdir -p --mode=0755 -- %s\n", txdir)
	for _, dir := range []string{m.Root + "/updates", txdir} {
		fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find %s -maxdepth 0 ( ! -type d -o ! -uid 0 -o -perm /022 ) -exec /usr/bin/false {} +\n", dir)
	}
	link := func(src, dst, mode string) {
		fmt.Fprintf(&unit, "ExecStart=-+/usr/bin/ln --no-target-directory -- %s %s\nExecStart=+/usr/bin/test %s -ef %s\n", src, dst, src, dst)
		fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm %s ) -exec /usr/bin/false {} +\n", dst, mode)
	}
	for _, item := range reusableMetadataFiles {
		if item.slot == "transaction" {
			continue
		}
		link(stage+"/"+item.slot, txdir+"/"+item.name, "0644")
	}
	for _, name := range reusableStageSlots["image"] {
		link(stage+"/"+name, txdir+"/"+name, "0755")
	}
	for _, direction := range reusableWorkerDirections {
		name := "unit-" + direction
		link(stage+"/"+name, txdir+"/"+name, "0644")
	}
	link(guard, txdir+"/atomic-guard", "0755")
	link(reusableMaintenancePath(m, "runner"), txdir+"/migration-runner", "0755")
	for _, direction := range reusableWorkerDirections {
		dst := fmt.Sprintf("/etc/systemd/system/fased-custody-%d-atomic-${FASED_TXID}-%s.service", m.Owner, direction)
		pending := attempt + "/" + direction + ".service"
		src := stage + "/unit-" + direction
		// /etc and /var may be different filesystems. Copy only inside the
		// exclusively created directory; old partials are never opened or removed.
		fmt.Fprintf(&unit, "ExecStart=+/usr/bin/install -m 0644 -o root -g root -- %s %s\nExecStart=+/usr/bin/cmp --silent -- %s %s\nExecStart=+/usr/bin/sync -f %s\n", src, pending, src, pending, pending)
		fmt.Fprintf(&unit, "ExecStart=-+/usr/bin/ln --no-target-directory -- %s %s\nExecStart=+/usr/bin/cmp --silent -- %s %s\n", pending, dst, src, dst)
		fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0644 ) -exec /usr/bin/false {} +\nExecStart=+/usr/bin/rm -- %s\n", dst, pending)
	}
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/rmdir -- %s\nExecStart=+/usr/bin/sync -f %s\nExecStart=+/usr/bin/sync -f /etc/systemd/system\nExecStart=+/usr/bin/systemctl daemon-reload\n", attempt, txdir)
	// The normal delegate treats transaction.json as a prepared transaction.
	// Publish it after the inputs and worker files are durable and systemd has
	// reloaded. Loaded-unit admission is still independently required afterward.
	link(stage+"/transaction", txdir+"/transaction.json", "0644")
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/sync -f %s\nExecStart=+/usr/bin/sync -f %s/updates\n", txdir, m.Root)
	// Recheck the original app backup and the loaded workers while the fixed
	// sentinel still blocks both services. Replace it atomically with the exact
	// staged forward marker; the restart-asserted path is never absent.
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/setpriv --reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-update-pause-guard --owner %d --disposable-fixture --verify-staged-recovery\n", m.App.UID, m.App.GID, reusableMaintenancePath(m, "runner"), m.Owner)
	fmt.Fprintf(&unit, "ExecStart=%s fixture-validate-atomic-provision --owner %d --disposable-fixture --verify-published\n", guard, m.Owner)
	pendingMarker := m.Root + "/.preparation-forward-${FASED_TXID}-${INVOCATION_ID}.marker"
	markerSource, marker := stage+"/atomic-marker-forward", m.Root+"/update-start-blocked.json"
	// Prior interrupted attempts are removable only when they are the same
	// protected forward-marker inode for this exact transaction.
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/find %s -maxdepth 1 -regextype posix-extended -regex %s/[.]preparation-forward-${FASED_TXID}-[0-9a-f]{32}[.]marker -type f -uid 0 -gid 0 -samefile %s -delete\nExecStart=+/usr/bin/sync -f %s\n", m.Root, m.Root, markerSource, m.Root)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/ln --no-target-directory -- %s %s\nExecStart=+/usr/bin/test %s -ef %s\n", markerSource, pendingMarker, markerSource, pendingMarker)
	fmt.Fprintf(&unit, "ExecStart=+/usr/bin/test %s -ef %s\nExecStart=+/usr/bin/mv --no-target-directory -- %s %s\nExecStart=+/usr/bin/sync -f %s\n", reusableMaintenancePath(m, "preparation-fence.json"), marker, pendingMarker, marker, m.Root)
	fmt.Fprintf(&unit, "ExecStart=%s fixture-validate-atomic-provision --owner %d --disposable-fixture --verify-published\n", guard, m.Owner)
	return []byte(unit.String()), nil
}

// Completed/recovered transactions keep their journal and remove only staging
// hardlinks which still refer to that exact transaction's protected inputs.
func makeReusableAtomicPairPlan(t InstallTransaction, c signer.Config, guardHash, runnerHash string) (atomicPairPlan, error) {
	plan, err := makeAtomicPairPlan(t, c, guardHash, runnerHash)
	if err != nil {
		return atomicPairPlan{}, err
	}
	// Check the actual app-owned recovery snapshot after the signer guard and
	// before any forward replacement. Rollback can still restore the previous
	// binaries; completing recovery remains subject to its own checks.
	forwardPath := filepath.Join("/etc/systemd/system", plan.forward)
	forward := string(plan.units[forwardPath])
	anchorBefore := " --guard-stage before\n"
	if strings.Count(forward, anchorBefore) != 1 {
		return atomicPairPlan{}, errors.New("atomic forward guard boundary differs")
	}
	pause := t.Binding.Index.Binding.Pause
	id := pause.Request.IntentID
	requestDigest := update.SnapshotDigest("pause-request", *pause.Request)
	instanceDigest := update.SnapshotDigest("atomic-instance:v1", pause.Core.InstanceID)
	recovery := fmt.Sprintf("ExecStart=/usr/bin/setpriv --reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-update-pause-guard --owner %d --disposable-fixture --intent-id %s --pause-id %s --request-digest %s --instance-digest %s --phase paused --verify-recovery --recovery-digest %s\n", t.Base.App.UID, t.Base.App.GID, t.dir()+"/migration-runner", t.Base.Owner, id, pause.PauseID, requestDigest, instanceDigest, update.InstallDigest(t.Binding))
	insertProof := func(name, body, extra string) error {
		index := strings.Index(body, anchorBefore)
		if index < 0 || strings.Count(body, anchorBefore) != 1 {
			return errors.New("atomic predecessor guard boundary differs")
		}
		start := strings.LastIndex(body[:index], "\n") + 1
		end := index + len(anchorBefore)
		guardLine := body[start:end]
		if !strings.Contains(guardLine, " fixture-install-guard ") {
			return errors.New("atomic predecessor signer guard is unavailable")
		}
		predecessor := strings.Replace(guardLine, anchorBefore, " --guard-stage predecessor\n", 1)
		proofSource := t.dir() + "/atomic-held.json"
		proof := reusableMaintenancePath(t.Base, "predecessor-proof-v1.json")
		publication := fmt.Sprintf("ExecStart=-/usr/bin/ln --no-target-directory -- %s %s\nExecStart=/usr/bin/test %s -ef %s\nExecStart=/usr/bin/cmp --silent -- %s %s\nExecStart=/usr/bin/sync -f %s\n", proofSource, proof, proofSource, proof, proofSource, proof, reusableMaintenancePath(t.Base, ""))
		body = body[:end] + extra + predecessor + publication + body[end:]
		parsed, parseErr := parseAtomicPairCommands([]byte(body))
		if parseErr != nil {
			return parseErr
		}
		plan.units[filepath.Join("/etc/systemd/system", name)], plan.commands[name] = []byte(body), parsed
		return nil
	}
	if err := insertProof(plan.forward, forward, recovery); err != nil {
		return atomicPairPlan{}, err
	}
	rollbackPath := filepath.Join("/etc/systemd/system", plan.rollback)
	if err := insertProof(plan.rollback, string(plan.units[rollbackPath]), ""); err != nil {
		return atomicPairPlan{}, err
	}
	stage := reusableMaintenancePath(t.Base, "staging")
	var cleanup strings.Builder
	removeLink := func(slot, target string) {
		fmt.Fprintf(&cleanup, "ExecStart=-/usr/bin/find %s/%s -maxdepth 0 -samefile %s -delete\n", stage, slot, target)
	}
	for _, item := range reusableMetadataFiles {
		removeLink(item.slot, t.dir()+"/"+item.name)
	}
	for _, name := range reusableStageSlots["image"] {
		removeLink(name, t.dir()+"/"+name)
	}
	for _, direction := range reusableWorkerDirections {
		name := "unit-" + direction
		removeLink(name, t.dir()+"/"+name)
	}
	fmt.Fprintf(&cleanup, "ExecStart=-/usr/bin/find %s -maxdepth 0 -samefile %s -delete\n", reusableMaintenancePath(t.Base, "active.json"), t.dir()+"/transaction.json")
	fmt.Fprintf(&cleanup, "ExecStart=/usr/bin/sync -f %s\n", reusableMaintenancePath(t.Base, ""))
	anchor := "ExecStart=/usr/bin/grep -Fqx -f " + t.dir() + "/atomic-markers.txt -- " + t.Base.Root + "/update-start-blocked.json\nExecStart=/usr/bin/rm -- " + t.Base.Root + "/update-start-blocked.json\n"
	for _, name := range []string{plan.cleanupRollback, plan.cleanupComplete} {
		path := filepath.Join("/etc/systemd/system", name)
		body := string(plan.units[path])
		// Never discard the active transaction while a stale or foreign proof
		// remains. A failed unlink is safe to retry with the fence still held.
		proof := reusableMaintenancePath(t.Base, "predecessor-proof-v1.json")
		active := "ExecStart=/usr/bin/rm -f -- " + t.Base.Root + "/update-active.json\n"
		proofCleanup := fmt.Sprintf("ExecStart=-/usr/bin/find %s -maxdepth 0 -samefile %s -delete\nExecStart=/usr/bin/test ! -e %s\nExecStart=/usr/bin/test ! -L %s\nExecStart=/usr/bin/sync -f %s\n", proof, t.dir()+"/atomic-held.json", proof, proof, reusableMaintenancePath(t.Base, ""))
		if strings.Count(body, active) != 1 {
			return atomicPairPlan{}, errors.New("atomic active cleanup boundary differs")
		}
		body = strings.Replace(body, active, proofCleanup+active, 1)
		if strings.Count(body, anchor) != 1 {
			return atomicPairPlan{}, errors.New("atomic cleanup boundary differs")
		}
		body = strings.Replace(body, anchor, cleanup.String()+anchor, 1)
		commands, parseErr := parseAtomicPairCommands([]byte(body))
		if parseErr != nil {
			return atomicPairPlan{}, parseErr
		}
		plan.units[path], plan.commands[name] = []byte(body), commands
	}
	return plan, nil
}

func reusableAtomicPolicy(m Manifest, name string) ([]byte, error) {
	if m.validate() != nil || !regexp.MustCompile(`^[a-z_][a-z0-9_-]*\$?$`).MatchString(name) {
		return nil, errors.New("valid ordinary owner required")
	}
	var policy strings.Builder
	grant := func(args ...string) {
		escape := strings.NewReplacer("\\", "\\\\", ",", "\\,", ":", "\\:", "=", "\\=")
		for i := range args {
			args[i] = escape.Replace(args[i])
		}
		fmt.Fprintf(&policy, "%s ALL=(root) NOPASSWD: %s\n", name, strings.Join(args, " "))
	}
	grant("/usr/bin/systemctl", "start", "--", reusableAtomicLauncherName(m.Owner))
	for _, path := range []string{reusableAtomicPolicyPath(m.Owner), nativePolicyPath(m), remoteProofPolicyPath(m)} {
		grant("/usr/bin/cat", "--", path)
		grant("/usr/bin/find", "/etc/sudoers.d", "-maxdepth", "1", "-name", filepath.Base(path), "-printf", "%U:%G:%m:%y")
	}
	grant("/usr/bin/setpriv", "--version")
	grant("/usr/bin/systemctl", "show", Unit(m.Owner, "signer"), "--property=ActiveState,MainPID")
	for _, operation := range []struct {
		action string
		stdin  bool
	}{{"status", false}, {"update-status", false}, {"update-preferences", false}, {"update-preferences", true}, {"update-pause", true}, {"update-backup", true}, {"update-recovery", true}} {
		args := appArgs(m, operation.action)
		if operation.stdin {
			args = append(args, "--request-stdin")
		}
		grant(args...)
	}
	if custodySourceSessionProfile(m) {
		grant(pinnedNativeUpdateSessionArgs(m)...)
	}
	inventory := strings.Join(appArgs(m, "update-inventory")[1:], " ")
	inventory = strings.ReplaceAll(inventory, ".", "[.]")
	grant("/usr/bin/setpriv", "^"+inventory+" --probe-id [0-9a-f]{32}$")
	// Only read-only cold inspection of this signer's pinned guard is reusable.
	// Sudo matches the entire argument expression; no path or operation varies.
	guard := strings.ReplaceAll(reusableMaintenancePath(m, "atomic-guard"), ".", "[.]")
	config := strings.ReplaceAll(m.Root+"/identity.json", ".", "[.]")
	args := fmt.Sprintf("^--reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-install-guard-status --disposable-fixture --config %s --intent-id [0-9a-f]{32} --guard-digest [0-9a-f]{64}$", m.Signer.UID, m.Signer.GID, guard, config)
	grant("/usr/bin/setpriv", args)
	cold := fmt.Sprintf("^--reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-preparation-hold --owner %d --intent-id [0-9a-f]{32} --disposable-fixture$", m.Signer.UID, m.Signer.GID, guard, m.Owner)
	grant("/usr/bin/setpriv", cold)
	return []byte(policy.String()), nil
}

func inspectReusableUnit(ctx context.Context, name, fragment string, body []byte, userID, groupID string, sections []string, allowed ...string) error {
	properties := "--property=LoadState,FragmentPath,DropInPaths,NeedDaemonReload,Type,KillMode,NoNewPrivileges,ProtectSystem,DynamicUser,User,Group"
	b, err := atomicInspectionOutput(ctx, 65536, "/usr/bin/systemctl", "show", name, properties)
	if err != nil {
		return errors.New("cannot inspect reusable maintenance service")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return errors.New("invalid reusable maintenance service metadata")
		}
		if _, duplicate := fields[key]; duplicate {
			return errors.New("ambiguous reusable maintenance service metadata")
		}
		fields[key] = value
	}
	if fields["LoadState"] != "loaded" || fields["FragmentPath"] != fragment || fields["DropInPaths"] != "" || fields["NeedDaemonReload"] != "no" || fields["Type"] != "oneshot" || fields["KillMode"] != "control-group" || fields["NoNewPrivileges"] != "yes" || fields["ProtectSystem"] != "strict" || fields["User"] != userID || fields["Group"] != groupID {
		return errors.New("loaded reusable maintenance service differs")
	}
	wantDynamic := "no"
	if bytes.Contains(body, []byte("\nDynamicUser=yes\n")) {
		wantDynamic = "yes"
	}
	if fields["DynamicUser"] != wantDynamic {
		return errors.New("loaded reusable maintenance service identity differs")
	}
	for _, section := range sections {
		expected, parseErr := parseAtomicCommands(body, section, allowed...)
		if parseErr != nil {
			return parseErr
		}
		actual, readErr := atomicUnitCommands(ctx, name, section)
		if readErr != nil {
			return errors.New("cannot inspect reusable maintenance commands")
		}
		if err = validateAtomicCommandRows(actual, expected); err != nil {
			return err
		}
	}
	return nil
}

// Instance-specific drop-ins can alter one allowed staging service without
// changing the common template. Inspect every start granted by the policy.
func inspectReusableStageInstances(ctx context.Context, m Manifest, ownerGID int, kind string, body []byte, inspect func(context.Context, string, string, []byte, string, string, []string, ...string) error) error {
	base, _, err := reusableStageClass(kind)
	if err != nil {
		return err
	}
	slots := reusableStageSlots[base]
	if len(slots) == 0 || inspect == nil {
		return errors.New("supported staging instances and inspection required")
	}
	template := fmt.Sprintf("/etc/systemd/system/fased-custody-%d-stage-%s@.service", m.Owner, kind)
	for _, slot := range slots {
		instance, err := reusableStageName(m.Owner, kind, slot)
		if err != nil {
			return err
		}
		expanded := bytes.ReplaceAll(body, []byte("%i"), []byte(slot))
		if err = inspect(ctx, instance, template, expanded, strconv.Itoa(m.Owner), strconv.Itoa(ownerGID), []string{"ExecStartPre", "ExecStart", "ExecStartPost"}); err != nil {
			return err
		}
	}
	return nil
}

func verifyReusableAtomicConfiguration(ctx context.Context, m Manifest, pin *reusableMaintenanceAuthority) error {
	if pin == nil {
		return errors.New("pinned reusable maintenance host required")
	}
	launcher, checksum, err := reusableAtomicLauncher(m, *pin)
	if err != nil {
		return err
	}
	name := reusableAtomicLauncherName(m.Owner)
	path := filepath.Join("/etc/systemd/system", name)
	for _, item := range []struct {
		path string
		data []byte
	}{{path, launcher}, {reusableMaintenancePath(m, "guard.sha256"), checksum}} {
		f, readErr := readRoot(item.path)
		if readErr != nil || !known(f, item.data, 0644) {
			return errors.New("reusable maintenance host is incomplete or changed")
		}
	}
	if err = verifyReusableMaintenanceAuthority(m, pin, pin.RunnerSHA256, pin.GuardSHA256, verifiedMigrationRunnerSize); err != nil {
		return err
	}
	if err = inspectReusableUnit(ctx, name, path, launcher, strconv.Itoa(m.Signer.UID), strconv.Itoa(m.Signer.GID), []string{"ExecStartPre", "ExecStart", "ExecStopPost"}, reusableMaintenancePath(m, "atomic-guard")); err != nil {
		return err
	}
	return nil
}

func verifyReusableAtomicHost(ctx context.Context, t InstallTransaction, s *System, pin *reusableMaintenanceAuthority) error {
	if s == nil {
		return errors.New("pinned reusable maintenance host required")
	}
	if err := verifyReusableAtomicConfiguration(ctx, t.Base, pin); err != nil {
		return err
	}
	u, err := user.LookupId(strconv.Itoa(t.Base.Owner))
	if err != nil {
		return errors.New("ordinary installation owner unavailable")
	}
	policy, err := reusableAtomicPolicy(t.Base, u.Username)
	if err != nil {
		return err
	}
	return verifyEffectiveAtomicPolicy(ctx, t, s, reusableAtomicPolicyPath(t.Base.Owner), policy)
}

func verifyReusablePreparationHost(ctx context.Context, t InstallTransaction, s *System, pin *reusableMaintenanceAuthority) error {
	if err := verifyReusableAtomicHost(ctx, t, s, pin); err != nil {
		return err
	}
	if os.Getuid() != t.Base.Owner || os.Geteuid() != t.Base.Owner {
		return errors.New("ordinary owner required for maintenance capability check")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, capability := range []string{"--recovery-capability", "--staged-recovery-capability", "--sentinel-staged-recovery-capability"} {
		probe := exec.CommandContext(probeCtx, reusableMaintenancePath(t.Base, "runner"), "fixture-update-pause-guard", capability)
		probe.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LC_ALL=C"}
		probe.Stdout, probe.Stderr = io.Discard, io.Discard
		if err := probe.Run(); err != nil {
			return errors.New("pinned maintenance reader lacks stopped-service recovery verification")
		}
	}
	for _, args := range [][]string{{"fixture-install-guard", "--predecessor-capability"}, {"fixture-validate-atomic-provision", "--verify-only-capability"}, {"fixture-validate-atomic-provision", "--published-capability"}, {"fixture-validate-preparation-fence", "--capability"}, {"fixture-validate-stage-recovery", "--worker-capability"}} {
		guardProbe := exec.CommandContext(probeCtx, reusableMaintenancePath(t.Base, "atomic-guard"), args...)
		guardProbe.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LC_ALL=C"}
		guardProbe.Stdout, guardProbe.Stderr = io.Discard, io.Discard
		if err := guardProbe.Run(); err != nil {
			return errors.New("pinned signer guard lacks protected provision verification")
		}
	}
	return verifyReusablePreparationConfiguration(ctx, t.Base, pin)
}

// The signer can inspect this root-owned configuration before the owner is
// granted any start permission. The owner opens the inbox separately before use.
func verifyReusablePreparationConfiguration(ctx context.Context, m Manifest, pin *reusableMaintenanceAuthority) error {
	if pin == nil || m.validate() != nil {
		return errors.New("pinned reusable maintenance host required")
	}
	if err := preflightRestartDropIns(m, readRoot); err != nil {
		return err
	}
	for _, role := range []string{"app", "signer"} {
		if err := loadedUnit(m, role); err != nil {
			return err
		}
	}
	stage := reusableMaintenancePath(m, "staging")
	if err := trustedParents(stage); err != nil {
		return errors.New("protected maintenance staging directory required")
	}
	info, err := os.Lstat(stage)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || st.Uid != 0 || st.Gid != 0 || st.Mode&07777 != 0755 {
		return errors.New("foreign maintenance staging directory")
	}
	inbox := reusableMaintenancePath(m, "inbox")
	info, err = os.Lstat(inbox)
	if err != nil {
		return err
	}
	st, ok = info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || st.Uid != uint32(m.Owner) || st.Gid != uint32(m.Signer.GID) || st.Mode&07777 != 02710 {
		return errors.New("foreign maintenance inbox")
	}
	u, err := user.LookupId(strconv.Itoa(m.Owner))
	if err != nil {
		return errors.New("ordinary installation owner unavailable")
	}
	ownerGID, err := strconv.Atoi(u.Gid)
	if err != nil || ownerGID <= 0 {
		return errors.New("ordinary installation group unavailable")
	}
	guard := reusableMaintenancePath(m, "atomic-guard")
	sentinel, err := signer.InstallationPreparationFenceBytes(m.Owner, m.ID)
	if err != nil {
		return err
	}
	sentinelPath := reusableMaintenancePath(m, "preparation-fence.json")
	for _, item := range []struct {
		path string
		data []byte
	}{{sentinelPath, sentinel}, {reusableMaintenancePath(m, "preparation-fence.sha256"), []byte(sum(sentinel) + "  " + sentinelPath + "\n")}} {
		f, readErr := readRoot(item.path)
		if readErr != nil || !known(f, item.data, 0644) {
			return errors.New("protected preparation fence inputs changed")
		}
	}
	fence, err := reusablePreparationFenceUnit(m, *pin)
	if err != nil {
		return err
	}
	fenceName := reusablePreparationFenceName(m.Owner)
	fencePath := filepath.Join("/etc/systemd/system", fenceName)
	fenceFile, err := readRoot(fencePath)
	if err != nil || !known(fenceFile, fence, 0644) {
		return errors.New("protected preparation fence service changed")
	}
	if err = inspectReusableUnit(ctx, fenceName, fencePath, fence, strconv.Itoa(m.Signer.UID), strconv.Itoa(m.Signer.GID), []string{"ExecStartPre", "ExecStart"}, guard); err != nil {
		return err
	}
	for _, slot := range reusableCompatibilitySlots {
		compat, buildErr := reusableCompatibilityUnit(m, ownerGID, slot)
		compatName, nameErr := reusableCompatibilityName(m.Owner, slot)
		if buildErr != nil || nameErr != nil {
			return errors.New("invalid fixed compatibility service")
		}
		compatPath := filepath.Join("/etc/systemd/system", compatName)
		compatFile, readErr := readRoot(compatPath)
		if readErr != nil || !known(compatFile, compat, 0644) {
			return errors.New("reusable compatibility service is incomplete or changed")
		}
		if err = inspectReusableUnit(ctx, compatName, compatPath, compat, "", "", []string{"ExecStartPre", "ExecStart", "ExecStartPost"}, stage+"/"+slot); err != nil {
			return err
		}
		if err = verifyReusableOutput(reusableMaintenancePath(m, "compat-"+slot+".out")); err != nil {
			return err
		}
	}
	provision, err := reusableAtomicProvision(m, *pin)
	if err != nil {
		return err
	}
	name := reusableAtomicProvisionName(m.Owner)
	path := filepath.Join("/etc/systemd/system", name)
	f, err := readRoot(path)
	if err != nil || !known(f, provision, 0644) {
		return errors.New("reusable preparation service is incomplete or changed")
	}
	if err = inspectReusableUnit(ctx, name, path, provision, strconv.Itoa(m.Signer.UID), strconv.Itoa(m.Signer.GID), []string{"ExecStartPre", "ExecStart"}, guard); err != nil {
		return err
	}
	for _, kind := range reusableStageKinds {
		body, buildErr := reusableStageTemplate(m, ownerGID, kind)
		if buildErr != nil {
			return buildErr
		}
		allow, allowErr := reusableStageAllowlist(kind)
		if allowErr != nil {
			return allowErr
		}
		template := fmt.Sprintf("/etc/systemd/system/fased-custody-%d-stage-%s@.service", m.Owner, kind)
		base, _, classErr := reusableStageClass(kind)
		if classErr != nil {
			return classErr
		}
		for _, item := range []struct {
			path string
			data []byte
		}{{template, body}, {reusableMaintenancePath(m, "stage-"+base+".allow"), allow}} {
			actual, readErr := readRoot(item.path)
			if readErr != nil || !known(actual, item.data, 0644) {
				return errors.New("reusable staging service or allowlist changed")
			}
		}
		if err = inspectReusableStageInstances(ctx, m, ownerGID, kind, body, inspectReusableUnit); err != nil {
			return err
		}
	}
	return nil
}

// VerifyReusableSetup is read-only and runs as the pinned signer, before the
// root-managed setup service is allowed to publish the owner policy.
func VerifyReusableSetup(ctx context.Context, owner int, policyInput io.Reader) error {
	if owner <= 0 || policyInput == nil || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || os.Getgid() <= 0 || os.Getgid() != os.Getegid() {
		return errors.New("configured unprivileged signer identity required")
	}
	m, err := Load(owner)
	if err != nil {
		return err
	}
	c, err := signer.LoadConfig(m.Root + "/identity.json")
	if err != nil || c.OwnerUID != owner || c.SignerID != m.ID || c.SignerUID != os.Getuid() || m.Signer.GID != os.Getgid() || (c.Version == 3 && c.Provenance != signer.ControlledCustodyProfile) {
		return errors.New("configured disposable signer identity required")
	}
	pin, err := readReusableMaintenanceAuthority(m, readRoot)
	if err != nil || pin == nil {
		return errors.New("pinned maintenance authority required")
	}
	exe, err := os.Executable()
	if err != nil || exe != reusableMaintenancePath(m, "atomic-guard") || verifyRootExecutable(exe, pin.GuardSHA256) != nil {
		return errors.New("pinned signer guard executable required")
	}
	if err = verifyReusableAtomicConfiguration(ctx, m, pin); err != nil {
		return err
	}
	if err = verifyReusablePreparationConfiguration(ctx, m, pin); err != nil {
		return err
	}
	for _, name := range []string{"setup.env", "worker.env", "provision.env"} {
		if err = verifyReusableOutput(reusableMaintenancePath(m, name)); err != nil {
			return err
		}
	}
	u, err := user.LookupId(strconv.Itoa(owner))
	if err != nil {
		return errors.New("ordinary installation owner unavailable")
	}
	ownerGID, err := strconv.Atoi(u.Gid)
	if err != nil || ownerGID <= 0 {
		return errors.New("ordinary installation group unavailable")
	}
	approval, err := loadReusableSetupApproval(m)
	if err != nil || approval.Authority != *pin {
		return errors.New("root-approved maintenance authority changed")
	}
	runnerSize, err := verifiedMigrationRunnerSize(reusableMaintenancePath(m, "runner"), pin.RunnerSHA256)
	if err != nil {
		return err
	}
	guardSize, err := verifiedMigrationRunnerSize(reusableMaintenancePath(m, "atomic-guard"), pin.GuardSHA256)
	if err != nil {
		return err
	}
	wantGrant, err := reusableSetupTemporaryPolicy(m, *pin, ownerGID, u.Username, runnerSize, guardSize, approval.NotAfter)
	if err != nil {
		return err
	}
	grant, err := io.ReadAll(io.LimitReader(policyInput, 512*1024+1))
	if err != nil || len(grant) > 512*1024 || !bytes.Equal(grant, wantGrant) {
		return errors.New("root-approved temporary maintenance authority changed")
	}
	body, checksum, err := reusableAtomicSetup(m, *pin, u.Username)
	if err != nil {
		return err
	}
	policy, err := reusableAtomicPolicy(m, u.Username)
	if err != nil {
		return err
	}
	name := reusableAtomicSetupName(owner)
	path := filepath.Join("/etc/systemd/system", name)
	for _, item := range []struct {
		path string
		data []byte
	}{{path, body}, {reusableMaintenancePath(m, "policy.sha256"), checksum}} {
		actual, readErr := readRoot(item.path)
		if readErr != nil || !known(actual, item.data, 0644) {
			return errors.New("reusable setup service or policy digest changed")
		}
	}
	for n, slotID := range reusableSetupSlotIDs(m.ID) {
		path := reusablePolicySlotChecksum(m, n)
		want := []byte(sum(policy) + "  " + reusablePolicyCandidate(m, slotID) + "\n")
		actual, readErr := readRoot(path)
		if readErr != nil || !known(actual, want, 0644) {
			return errors.New("reusable policy candidate digest changed")
		}
	}
	if err = inspectReusableUnit(ctx, name, path, body, strconv.Itoa(m.Signer.UID), strconv.Itoa(m.Signer.GID), []string{"ExecStartPre", "ExecStart", "ExecStartPost"}, exe, "/usr/sbin/visudo"); err != nil {
		return err
	}
	enabled, err := atomicInspectionOutput(ctx, 4096, "/usr/bin/systemctl", "is-enabled", name)
	if err != nil || string(enabled) != "enabled\n" {
		return errors.New("setup recovery must be enabled before activation")
	}
	return nil
}

func readReusableMaintenanceAuthority(m Manifest, read readHost) (*reusableMaintenanceAuthority, error) {
	if m.validate() != nil || read == nil {
		return nil, errors.New("valid managed installation required")
	}
	f, err := read(reusableMaintenancePath(m, "authority.json"))
	if errors.Is(err, os.ErrNotExist) {
		if _, markerErr := read(reusableMaintenancePath(m, "reusable-v1.json")); errors.Is(markerErr, os.ErrNotExist) {
			return nil, nil
		}
		return nil, errors.New("pinned maintenance authority is missing; preserving installation")
	}
	if err != nil {
		return nil, errors.New("maintenance authority is unavailable; preserving installation")
	}
	var a reusableMaintenanceAuthority
	if strict(f.Data, &a) != nil || !known(f, mustJSON(a), 0644) || a.Version != 1 || a.Owner != m.Owner || a.InstallationID != m.ID || !validHex(a.RunnerSHA256, 32) || !validHex(a.GuardSHA256, 32) {
		return nil, errors.New("foreign maintenance authority; preserving installation")
	}
	marker, err := read(reusableMaintenancePath(m, "reusable-v1.json"))
	if err != nil || !known(marker, reusableMaintenanceMarkerBytes(a), 0644) {
		return nil, errors.New("maintenance authority marker is missing or changed; preserving installation")
	}
	return &a, nil
}

func verifyReusableMaintenanceAuthority(m Manifest, a *reusableMaintenanceAuthority, runnerHash, guardHash string, check func(string, string) (int64, error)) error {
	if a == nil || check == nil || a.Owner != m.Owner || a.InstallationID != m.ID || a.RunnerSHA256 != runnerHash || a.GuardSHA256 != guardHash {
		return errors.New("transaction differs from the pinned maintenance authority")
	}
	for _, image := range []struct{ name, digest string }{{"runner", a.RunnerSHA256}, {"atomic-guard", a.GuardSHA256}} {
		if _, err := check(reusableMaintenancePath(m, image.name), image.digest); err != nil {
			return errors.New("pinned maintenance executable unavailable or changed")
		}
	}
	return nil
}

// The owner can replace only this non-secret request. The signer reads the
// descriptor once and independently verifies the root-owned transaction.
func readReusableAtomicRequest(owner, signerGID int) (reusableAtomicRequest, error) {
	inbox := filepath.Join(Root(owner), "maintenance", "inbox")
	return readReusableAtomicRequestAt(inbox, owner, signerGID, trustedParents)
}

func readReusableAtomicRequestAt(inbox string, owner, signerGID int, trust func(string) error) (reusableAtomicRequest, error) {
	var empty reusableAtomicRequest
	if owner <= 0 || signerGID <= 0 || trust == nil {
		return empty, errors.New("ordinary owner and signer group required")
	}
	if err := trust(filepath.Dir(inbox)); err != nil {
		return empty, err
	}
	dfd, err := unix.Open(inbox, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return empty, errors.New("maintenance request inbox unavailable")
	}
	d := os.NewFile(uintptr(dfd), inbox)
	defer d.Close()
	dir, err := d.Stat()
	if err != nil {
		return empty, err
	}
	dstat, ok := dir.Sys().(*syscall.Stat_t)
	if !ok || !dir.IsDir() || int(dstat.Uid) != owner || int(dstat.Gid) != signerGID || dstat.Mode&07777 != 02710 {
		return empty, errors.New("foreign maintenance request inbox")
	}
	fd, err := unix.Openat(dfd, "request.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return empty, errors.New("maintenance request unavailable")
	}
	f := os.NewFile(uintptr(fd), inbox+"/request.json")
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return empty, err
	}
	stat, ok := before.Sys().(*syscall.Stat_t)
	if !ok || !before.Mode().IsRegular() || int(stat.Uid) != owner || int(stat.Gid) != signerGID || stat.Mode&07777 != 0640 || stat.Nlink != 1 || before.Size() <= 0 || before.Size() > 4096 {
		return empty, errors.New("foreign maintenance request")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || int64(len(b)) != before.Size() || len(b) > 4096 {
		return empty, errors.New("maintenance request changed during reading")
	}
	after, err := f.Stat()
	var current unix.Stat_t
	if err != nil || unix.Fstatat(dfd, "request.json", &current, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return empty, errors.New("maintenance request disappeared")
	}
	astat, ok := after.Sys().(*syscall.Stat_t)
	if !ok || !os.SameFile(before, after) || stat.Dev != uint64(current.Dev) || stat.Ino != current.Ino || stat.Ctim != astat.Ctim || astat.Ctim.Sec != current.Ctim.Sec || astat.Ctim.Nsec != current.Ctim.Nsec || before.Size() != after.Size() || int(stat.Uid) != int(current.Uid) || int(stat.Gid) != int(current.Gid) || current.Mode&07777 != 0640 || current.Nlink != 1 {
		return empty, errors.New("maintenance request changed during reading")
	}
	currentDir, err := os.Lstat(inbox)
	if err != nil || !os.SameFile(dir, currentDir) || trust(filepath.Dir(inbox)) != nil {
		return empty, errors.New("maintenance request inbox changed")
	}
	return validateReusableAtomicRequest(b, owner)
}

// The ordinary owner replaces only its own validated request, never a signer
// record. The signer independently opens and checks the published descriptor.
func writeReusableAtomicRequest(t InstallTransaction, direction, kind, slot, probe string) (string, error) {
	if !t.valid() || os.Getuid() != t.Base.Owner || os.Geteuid() != t.Base.Owner {
		return "", errors.New("ordinary installation owner required")
	}
	r := reusableAtomicRequest{Version: 1, Owner: t.Base.Owner, InstallationID: t.Base.ID, IntentID: t.Binding.Index.Binding.Pause.Request.IntentID, Direction: direction, RequestID: update.NewProbe(), StageKind: kind, StageSlot: slot, Probe: probe}
	if err := writeReusableAtomicRequestAt(reusableMaintenancePath(t.Base, "inbox"), r, t.Base.Signer.GID, trustedParents); err != nil {
		return "", err
	}
	return r.RequestID, nil
}

// A systemd start can join an already-running launcher. Only its protected
// completion output can acknowledge the requested worker after it finished.
func requireReusableWorkerCompletion(m Manifest, requestID, worker string, read readHost) error {
	return requireReusableWorkerOutput(m, requestID, worker, true, read)
}

func requireReusableWorkerDispatch(m Manifest, requestID, worker string, read readHost) error {
	return requireReusableWorkerOutput(m, requestID, worker, false, read)
}

func requireReusableWorkerOutput(m Manifest, requestID, worker string, completed bool, read readHost) error {
	if !update.IDOK(requestID) || worker == "" || read == nil {
		return errors.New("exact maintenance request required")
	}
	want := "FASED_REQUEST_ID=" + requestID + "\nFASED_WORKER_UNIT=" + worker + "\n"
	if completed {
		want += "FASED_WORKER_COMPLETED=yes"
	}
	f, err := read(reusableMaintenancePath(m, "worker.env"))
	if err != nil || !known(f, []byte(want), 0644) {
		return errors.New("maintenance worker request differs; preserve and retry")
	}
	return nil
}

func readReusableWorkerCompletion(path string) (hostFile, error) {
	if err := trustedParents(filepath.Dir(path)); err != nil {
		return hostFile{}, err
	}
	return readReusableWorkerCompletionAt(path, 0, 0)
}

func readReusableWorkerCompletionAt(path string, uid, gid int) (hostFile, error) {
	return readReusableProtectedOutputAt(path, uid, gid, 4096)
}

func readReusableCompatibilityOutput(path string) (hostFile, error) {
	if err := trustedParents(filepath.Dir(path)); err != nil {
		return hostFile{}, err
	}
	return readReusableProtectedOutputAt(path, 0, 0, 65536)
}

func readReusableProtectedOutputAt(path string, uid, gid int, limit int64) (hostFile, error) {
	if limit <= 0 || limit > 65536 {
		return hostFile{}, errors.New("invalid protected output bound")
	}
	f, err := openStageRecoveryFile(path, uid, gid, []uint32{0644}, limit)
	if err != nil {
		return hostFile{}, err
	}
	defer f.file.Close()
	var marker unix.Stat_t
	if f.stat.Nlink != 2 || unix.Lstat(path+".owner", &marker) != nil || marker.Dev != f.stat.Dev || marker.Ino != f.stat.Ino || marker.Mode != f.stat.Mode || marker.Uid != f.stat.Uid || marker.Gid != f.stat.Gid || marker.Nlink != f.stat.Nlink {
		return hostFile{}, errors.New("foreign maintenance completion output")
	}
	b, err := io.ReadAll(io.LimitReader(f.file, limit+1))
	if err != nil || int64(len(b)) != f.stat.Size || !f.stable() || unix.Lstat(path+".owner", &marker) != nil || marker.Dev != f.stat.Dev || marker.Ino != f.stat.Ino || marker.Ctim != f.stat.Ctim || marker.Size != f.stat.Size || marker.Nlink != f.stat.Nlink {
		return hostFile{}, errors.New("maintenance completion output changed while reading")
	}
	return hostFile{UID: uid, Mode: 0644, Regular: true, Data: b}, nil
}

func writeReusableAtomicRequestAt(inbox string, r reusableAtomicRequest, signerGID int, trust func(string) error) error {
	if os.Getuid() != r.Owner || os.Geteuid() != r.Owner || signerGID <= 0 || trust == nil {
		return errors.New("ordinary owner and signer group required")
	}
	data := mustJSON(r)
	if _, err := validateReusableAtomicRequest(data, r.Owner); err != nil {
		return err
	}
	if err := trust(filepath.Dir(inbox)); err != nil {
		return err
	}
	dfd, err := unix.Open(inbox, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("maintenance request inbox unavailable")
	}
	d := os.NewFile(uintptr(dfd), inbox)
	defer d.Close()
	var ds unix.Stat_t
	if err = unix.Fstat(dfd, &ds); err != nil || int(ds.Uid) != r.Owner || int(ds.Gid) != signerGID || ds.Mode&unix.S_IFMT != unix.S_IFDIR || ds.Mode&07777 != 02710 {
		return errors.New("foreign maintenance request inbox")
	}
	var existing unix.Stat_t
	if err = unix.Fstatat(dfd, "request.json", &existing, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		old, readErr := readReusableAtomicRequestAt(inbox, r.Owner, signerGID, trust)
		if readErr != nil || old.InstallationID != r.InstallationID || old.IntentID != r.IntentID {
			return errors.New("foreign maintenance request; preserving it")
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return errors.New("maintenance request cannot be inspected")
	}
	temp := ".request-" + update.NewProbe()
	fd, err := unix.Openat(dfd, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0640)
	if err != nil {
		return err
	}
	var created unix.Stat_t
	if err = unix.Fstat(fd, &created); err != nil {
		unix.Close(fd)
		return err
	}
	published := false
	defer func() {
		var current unix.Stat_t
		if !published && unix.Fstatat(dfd, temp, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && current.Dev == created.Dev && current.Ino == created.Ino {
			_ = unix.Unlinkat(dfd, temp, 0)
		}
	}()
	f := os.NewFile(uintptr(fd), inbox+"/"+temp)
	if err = f.Chmod(0640); err == nil {
		var n int
		n, err = f.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		err = f.Sync()
	}
	var staged unix.Stat_t
	if err == nil {
		err = unix.Fstat(fd, &staged)
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil || int(staged.Uid) != r.Owner || int(staged.Gid) != signerGID || staged.Mode&unix.S_IFMT != unix.S_IFREG || staged.Mode&07777 != 0640 || staged.Nlink != 1 {
		return errors.New("maintenance request staging failed; preserving it")
	}
	if err = unix.Renameat(dfd, temp, dfd, "request.json"); err != nil {
		return err
	}
	published = true
	if err = unix.Fsync(dfd); err != nil {
		return err
	}
	got, err := readReusableAtomicRequestAt(inbox, r.Owner, signerGID, trust)
	if err != nil || got != r {
		return errors.New("maintenance request publication changed")
	}
	return nil
}

// The fixed OS launcher supplies only its owner. The isolated signer derives
// the exact worker from the owner's bounded request and protected transaction.
func VerifyReusableAtomicRequest(ctx context.Context, owner int) (string, string, error) {
	if owner <= 0 || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || os.Getgid() <= 0 || os.Getgid() != os.Getegid() {
		return "", "", errors.New("configured unprivileged signer identity required")
	}
	c, err := signer.LoadConfig(Root(owner) + "/identity.json")
	if err != nil || c.OwnerUID != owner || c.SignerUID != os.Getuid() {
		return "", "", errors.New("configured signer identity required")
	}
	r, err := readReusableAtomicRequest(owner, os.Getgid())
	if err != nil || r.InstallationID != c.SignerID || !update.IDOK(r.RequestID) {
		return "", "", errors.New("maintenance request differs from the installation")
	}
	worker, err := selectReusableAtomicRequest(ctx, owner, r, VerifyReusableAtomicProvision, VerifyReusableAtomicWorker, VerifyReusableStageRecovery, VerifyReusableAtomicCompatibility)
	if err != nil {
		return "", "", err
	}
	current, err := readReusableAtomicRequest(owner, os.Getgid())
	if err != nil || current != r {
		return "", "", errors.New("maintenance request changed during verification")
	}
	return worker, r.RequestID, nil
}

func selectReusableAtomicRequest(ctx context.Context, owner int, r reusableAtomicRequest, verifyProvision func(int) (string, error), verifyWorker func(context.Context, int, string, string) error, verifyStage func(int, string, string) error, verifyCompatibility func(int) (string, error)) (string, error) {
	if _, err := validateReusableAtomicRequest(mustJSON(r), owner); err != nil || verifyProvision == nil || verifyWorker == nil || verifyStage == nil || verifyCompatibility == nil {
		return "", errors.New("complete bound maintenance request required")
	}
	if r.Direction == "stage" {
		if err := verifyStage(owner, r.StageKind, r.StageSlot); err != nil {
			return "", fmt.Errorf("protected staging was refused: %w", err)
		}
		return reusableStageName(owner, r.StageKind, r.StageSlot)
	}
	if r.Direction == "fence" {
		// The unit installs only the fixed denial sentinel; its signer-UID
		// cold guard must authenticate the actual hold before staging proceeds.
		return reusablePreparationFenceName(owner), nil
	}
	if r.Direction == "compatibility" {
		id, err := verifyCompatibility(owner)
		if err != nil {
			return "", fmt.Errorf("protected compatibility was refused: %w", err)
		}
		if id != r.IntentID {
			return "", errors.New("protected compatibility does not match the requested intent")
		}
		return reusableCompatibilityName(owner, r.Probe)
	}
	if r.Direction == "prepare" {
		id, err := verifyProvision(owner)
		if err != nil {
			return "", fmt.Errorf("protected preparation was refused: %w", err)
		}
		if id != r.IntentID {
			return "", errors.New("protected preparation does not match the requested intent")
		}
		return reusableAtomicProvisionName(owner), nil
	}
	if err := verifyWorker(ctx, owner, r.IntentID, r.Direction); err != nil {
		return "", err
	}
	return fmt.Sprintf("fased-custody-%d-atomic-%s-%s.service", owner, r.IntentID, r.Direction), nil
}

// VerifyReusableAtomicProvision binds every staged input to the pinned reader,
// fixed transaction and generated OS workers before root publishes any link.
func VerifyReusableAtomicProvision(owner int) (string, error) {
	return verifyReusableAtomicPreparation(owner, "prepare", true, false)
}

// The child checks the request selected by its enclosing serialized launcher.
func VerifyReusableAtomicProvisionWorker(owner int) (string, error) {
	return verifyReusableAtomicPreparation(owner, "prepare", true, true)
}

// VerifyReusableAtomicPublished rechecks the cold hold and every actual loaded
// worker after transaction publication, before the sentinel can be replaced.
func VerifyReusableAtomicPublished(ctx context.Context, owner int) (string, error) {
	id, err := VerifyReusableAtomicProvisionWorker(owner)
	if err != nil {
		return "", err
	}
	t, err := LoadCanonicalController(owner, id)
	if err != nil {
		return "", err
	}
	c, err := signer.LoadConfig(t.Base.Root + "/identity.json")
	if err != nil {
		return "", err
	}
	for _, direction := range reusableWorkerDirections {
		if err = validateReusableAtomicWorker(ctx, t, c, direction, readRoot, verifiedMigrationRunnerSize, atomicUnitInspection); err != nil {
			return "", err
		}
	}
	if again, err := VerifyReusableAtomicProvisionWorker(owner); err != nil || again != id {
		return "", errors.New("published preparation changed during worker admission")
	}
	return id, nil
}

// Compatibility probes may run only after the same cold signer and staged
// inputs have been authenticated. Provision also requires their final output.
func VerifyReusableAtomicCompatibility(owner int) (string, error) {
	return verifyReusableAtomicPreparation(owner, "compatibility", false, false)
}

func verifyReusableAtomicPreparation(owner int, direction string, compatibility, dispatched bool) (string, error) {
	if owner <= 0 || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || os.Getgid() <= 0 || os.Getgid() != os.Getegid() {
		return "", errors.New("configured unprivileged signer identity required")
	}
	if !update.IDOK(os.Getenv("INVOCATION_ID")) {
		return "", errors.New("exact systemd invocation identity required")
	}
	c, err := signer.LoadConfig(Root(owner) + "/identity.json")
	if err != nil || c.OwnerUID != owner || c.SignerUID != os.Getuid() || (c.Version == 3 && c.Provenance != signer.ControlledCustodyProfile) {
		return "", errors.New("configured disposable signer identity required")
	}
	r, err := readReusableAtomicRequest(owner, os.Getgid())
	if err != nil || r.InstallationID != c.SignerID || r.Direction != direction {
		return "", errors.New("exact maintenance preparation request required")
	}
	stage := filepath.Join(Root(owner), "maintenance", "staging")
	f, err := readRoot(stage + "/transaction")
	var t InstallTransaction
	if err != nil || strict(f.Data, &t) != nil || !known(f, mustJSON(t), 0644) || !t.valid() || t.Base.Owner != owner || t.Base.ID != r.InstallationID || t.Base.Signer.GID != os.Getgid() || t.Binding.Index.Binding.Pause.Request.IntentID != r.IntentID {
		return "", errors.New("staged maintenance transaction differs from the request")
	}
	if dispatched {
		if err = requireReusableWorkerDispatch(t.Base, r.RequestID, reusableAtomicProvisionName(owner), readReusableWorkerCompletion); err != nil {
			return "", err
		}
	}
	pin, err := readReusableMaintenanceAuthority(t.Base, readRoot)
	if err != nil || pin == nil {
		return "", errors.New("pinned maintenance authority required")
	}
	exe, err := os.Executable()
	if err != nil || exe != reusableMaintenancePath(t.Base, "atomic-guard") || verifyRootExecutable(exe, pin.GuardSHA256) != nil {
		return "", errors.New("pinned signer guard executable required")
	}
	digest := update.InstallDigest(t.Binding)
	preparing := false
	boundary := func() error {
		if signer.VerifyInstallationPreparationBoundary(c) == nil {
			preparing = true
			return nil
		}
		preparing = false
		return signer.VerifyInstallationGuardBoundary(c, r.IntentID, digest)
	}
	if err = boundary(); err != nil {
		return "", err
	}
	hold, err := signer.InspectInstallationReadOnly(c, r.IntentID)
	if err != nil {
		return "", errors.New("actual cold signer hold unavailable; preserve preparation")
	}
	if err = validateReusableAtomicInputs(t, c, hold, readRoot, verifiedMigrationRunnerSize, preparing); err != nil {
		return "", err
	}
	if err = requireProtectedPredecessorStage(t, readRoot); err != nil {
		return "", err
	}
	if compatibility {
		if err = validateReusableCompatibility(t, readReusableCompatibilityOutput); err != nil {
			return "", err
		}
	}
	// The OS policy must also serialize every privileged worker and exclude
	// other signer-UID writers: this check alone cannot retain the vault lock.
	if err = boundary(); err != nil {
		return "", err
	}
	if err = requireProtectedPredecessorStage(t, readRoot); err != nil {
		return "", err
	}
	if dispatched {
		current, readErr := readReusableAtomicRequest(owner, os.Getgid())
		if readErr != nil || current != r {
			return "", errors.New("maintenance request changed during preparation")
		}
		if err = requireReusableWorkerDispatch(t.Base, r.RequestID, reusableAtomicProvisionName(owner), readReusableWorkerCompletion); err != nil {
			return "", err
		}
	}
	return r.IntentID, nil
}

func validateReusableCompatibility(t InstallTransaction, read readHost) error {
	index := 0
	if err := checkRollbackCompatibility(t, func(name string, core bool) ([]byte, error) {
		if index >= len(reusableCompatibilitySlots) || reusableCompatibilitySlots[index] != name || core != (index < 2) {
			return nil, errors.New("unexpected paired compatibility probe")
		}
		index++
		path := reusableMaintenancePath(t.Base, "compat-"+name+".out")
		f, err := read(path)
		if err != nil || !known(f, f.Data, 0644) || len(f.Data) > 65536 {
			return nil, errors.New("protected compatibility output required")
		}
		lines := bytes.Split(f.Data, []byte("\n"))
		if len(lines) != 3 || len(lines[2]) != 0 || string(lines[0]) != sum(mustJSON(t))+"  "+reusableMaintenancePath(t.Base, "staging")+"/transaction" {
			return nil, errors.New("compatibility output differs from the transaction")
		}
		return lines[1], nil
	}); err != nil {
		return fmt.Errorf("paired update is not safely reversible: %w", err)
	}
	return nil
}

func validateReusableAtomicProvision(t InstallTransaction, c signer.Config, hold *update.Installation, read readHost, check func(string, string) (int64, error)) error {
	if err := validateReusableAtomicInputs(t, c, hold, read, check, false); err != nil {
		return err
	}
	return validateReusableCompatibility(t, read)
}

func validateReusableAtomicInputs(t InstallTransaction, c signer.Config, hold *update.Installation, read readHost, check func(string, string) (int64, error), preparing bool) error {
	if !t.valid() || read == nil || check == nil {
		return errors.New("complete maintenance preparation context required")
	}
	if err := validateReusablePredecessor(t, read); err != nil {
		return err
	}
	id, digest := t.Binding.Index.Binding.Pause.Request.IntentID, update.InstallDigest(t.Binding)
	if hold == nil || !bytes.Equal(mustJSON(hold.Binding), mustJSON(t.Binding)) ||
		signer.ValidateInstallationReplacement(hold, id, digest, "forward", "core", t.Target.CoreHash) != nil ||
		signer.ValidateInstallationReplacement(hold, id, digest, "forward", "signer", t.Target.SignerHash) != nil {
		return errors.New("exact active signer installation hold required for preparation")
	}
	if c != identityConfig(t.Base) || reusableStageManifestMatches(t.Base, read) != nil {
		return errors.New("exact protected predecessor identity and inventory required")
	}
	for _, item := range []struct{ path, digest string }{{t.Base.Root + "/fased-app", t.Base.CoreHash}, {t.Base.Root + "/fased-signer", t.Base.SignerHash}} {
		if _, err := check(item.path, item.digest); err != nil {
			return errors.New("protected predecessor executable changed; preserving it")
		}
	}
	pin, err := readReusableMaintenanceAuthority(t.Base, read)
	if err != nil || pin == nil {
		return errors.New("pinned maintenance authority required")
	}
	if err = verifyReusableMaintenanceAuthority(t.Base, pin, pin.RunnerSHA256, pin.GuardSHA256, check); err != nil {
		return err
	}
	approval := migrationRunnerApproval{Version: 1, Owner: t.Base.Owner, InstallationID: t.Base.ID, IntentID: t.Binding.Index.Binding.Pause.Request.IntentID, BindingDigest: update.InstallDigest(t.Binding), PreviousCore: t.Base.CoreHash, TargetCore: t.Target.CoreHash, TargetSigner: t.Target.SignerHash, RunnerSHA256: pin.RunnerSHA256, GuardSHA256: pin.GuardSHA256}
	if !validMigrationApproval(t, approval) {
		return errors.New("pinned reader is incompatible with this transaction")
	}
	plan, err := makeReusableAtomicPairPlan(t, c, pin.GuardSHA256, pin.RunnerSHA256)
	if err != nil {
		return err
	}
	stage := reusableMaintenancePath(t.Base, "staging")
	for _, item := range reusableMetadataFiles {
		var expected []byte
		switch item.slot {
		case "transaction":
			expected = mustJSON(t)
		case "approval":
			expected = mustJSON(approval)
		default:
			expected = plan.inputs[t.dir()+"/"+item.name]
		}
		f, readErr := read(stage + "/" + item.slot)
		if len(expected) == 0 || readErr != nil || !known(f, expected, 0644) {
			return errors.New("staged maintenance metadata is incomplete or changed")
		}
	}
	for _, item := range []struct{ slot, digest string }{{"runner", t.Base.CoreHash}, {"previous-signer", t.Base.SignerHash}, {"next-core", t.Target.CoreHash}, {"next-signer", t.Target.SignerHash}} {
		if _, err = check(stage+"/"+item.slot, item.digest); err != nil {
			return errors.New("staged maintenance executable is incomplete or changed")
		}
	}
	for _, direction := range reusableWorkerDirections {
		name, nameErr := atomicWorkerName(plan, direction)
		if nameErr != nil {
			return nameErr
		}
		final := filepath.Join("/etc/systemd/system", name)
		expected := plan.units[final]
		f, readErr := read(stage + "/unit-" + direction)
		if readErr != nil || !known(f, expected, 0644) {
			return errors.New("staged atomic worker is incomplete or changed")
		}
		if f, readErr = read(final); readErr == nil {
			if !known(f, expected, 0644) {
				return errors.New("foreign atomic worker; preserve it")
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return errors.New("atomic worker cannot be inspected")
		}
	}
	active, err := read(reusableMaintenancePath(t.Base, "active.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("active preparation cannot be inspected")
	}
	if err == nil && !known(active, mustJSON(t), 0644) {
		return errors.New("another preparation owns the installation; preserve it")
	}
	marker, err := read(t.Base.Root + "/update-start-blocked.json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("restart guard cannot be inspected")
	}
	if err == nil {
		var guard signer.InstallationGuardMarker
		sentinel, sentinelErr := signer.InstallationPreparationFenceBytes(t.Base.Owner, t.Base.ID)
		if !(preparing && sentinelErr == nil && known(marker, sentinel, 0644)) && (strict(marker.Data, &guard) != nil || !known(marker, mustJSON(guard), 0644) || guard.Version != 1 || guard.Owner != t.Base.Owner || guard.SignerID != t.Base.ID || guard.IntentID != t.Binding.Index.Binding.Pause.Request.IntentID || guard.Digest != update.InstallDigest(t.Binding) || !slices.Contains(reusableWorkerDirections, guard.Direction)) {
			return errors.New("another recovery guard owns the installation; preserve it")
		}
	}
	updateActive, err := read(t.Base.Root + "/update-active.json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("active update cannot be inspected")
	}
	if err == nil && !known(updateActive, mustJSON(phaseRecord{1, update.InstallDigest(t.Binding), t.Binding.Index.Binding.Pause.Request.IntentID}), 0644) {
		return errors.New("another update owns the installation; preserve it")
	}
	return nil
}

// VerifyReusableAtomicWorker is called as the isolated signer by a fixed OS
// launcher. The policy must also prevent changing this unit after validation.
func VerifyReusableAtomicWorker(ctx context.Context, owner int, id, direction string) error {
	if owner <= 0 || !update.IDOK(id) || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return errors.New("exact unprivileged atomic worker required")
	}
	t, err := LoadController(owner, id)
	if err != nil {
		return err
	}
	c, err := signer.LoadConfig(t.Base.Root + "/identity.json")
	if err != nil || c.SignerUID != os.Getuid() || t.Base.Signer.GID != os.Getgid() || os.Getgid() != os.Getegid() || (c.Version == 3 && c.Provenance != signer.ControlledCustodyProfile) {
		return errors.New("configured disposable signer identity required")
	}
	pin, err := readReusableMaintenanceAuthority(t.Base, readRoot)
	if err != nil || pin == nil {
		return errors.New("pinned maintenance authority required")
	}
	exe, err := os.Executable()
	if err != nil || exe != reusableMaintenancePath(t.Base, "atomic-guard") || verifyRootExecutable(exe, pin.GuardSHA256) != nil {
		return errors.New("pinned signer guard executable required")
	}
	return validateReusableAtomicWorker(ctx, t, c, direction, readRoot, verifiedMigrationRunnerSize, atomicUnitInspection)
}

func validateReusableAtomicWorker(ctx context.Context, t InstallTransaction, c signer.Config, direction string, read readHost, check func(string, string) (int64, error), inspect func(context.Context, string) (map[string]string, []byte, error)) error {
	if !t.valid() || read == nil || check == nil || inspect == nil {
		return errors.New("complete atomic worker context required")
	}
	if err := validateReusablePredecessor(t, read); err != nil {
		return err
	}
	pin, err := readReusableMaintenanceAuthority(t.Base, read)
	if err != nil || pin == nil {
		return errors.New("pinned maintenance authority required")
	}
	approval, err := readAtomicMigrationApproval(t, read, check)
	if err != nil {
		return err
	}
	plan, err := makeReusableAtomicPairPlan(t, c, approval.GuardSHA256, approval.RunnerSHA256)
	if err != nil {
		return err
	}
	name, err := atomicWorkerName(plan, direction)
	if err != nil {
		return err
	}
	transaction, err := read(t.dir() + "/transaction.json")
	if err != nil || !known(transaction, mustJSON(t), 0644) {
		return errors.New("atomic transaction differs from its protected source")
	}
	for path, data := range plan.inputs {
		f, readErr := read(path)
		if readErr != nil || !known(f, data, 0644) {
			return errors.New("atomic input is incomplete or changed")
		}
	}
	for _, item := range []struct{ path, hash string }{{t.dir() + "/atomic-guard", pin.GuardSHA256}, {t.dir() + "/migration-runner", pin.RunnerSHA256}} {
		if _, err = check(item.path, item.hash); err != nil {
			return errors.New("atomic worker executable differs from its pin")
		}
	}
	path := filepath.Join("/etc/systemd/system", name)
	f, err := read(path)
	if err != nil || !known(f, plan.units[path], 0644) {
		return errors.New("atomic worker file is missing or changed")
	}
	fields, commands, err := inspect(ctx, name)
	if err != nil {
		return err
	}
	return validateLoadedAtomicUnit(plan, name, fields, commands)
}
