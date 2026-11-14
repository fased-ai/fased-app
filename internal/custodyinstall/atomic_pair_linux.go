//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func atomicPolicyPath(t InstallTransaction) string {
	return fmt.Sprintf("/etc/sudoers.d/fased-custody-%d-atomic-%s", t.Base.Owner, t.Binding.Index.Binding.Pause.Request.IntentID)
}

func atomicPolicyBytes(t InstallTransaction, plan atomicPairPlan, name string) ([]byte, error) {
	if !regexp.MustCompile(`^[a-z_][a-z0-9_-]*\$?$`).MatchString(name) {
		return nil, errors.New("invalid ordinary owner account")
	}
	p := atomicPolicyPath(t)
	var b strings.Builder
	grant := func(args ...string) {
		escape := strings.NewReplacer("\\", "\\\\", ",", "\\,", ":", "\\:", "=", "\\=")
		for n := range args {
			args[n] = escape.Replace(args[n])
		}
		fmt.Fprintf(&b, "%s ALL=(root) NOPASSWD: %s\n", name, strings.Join(args, " "))
	}
	for _, unit := range plan.names() {
		grant("/usr/bin/systemctl", "start", "--", unit)
	}
	grant("/usr/bin/cat", "--", p)
	grant("/usr/bin/find", "/etc/sudoers.d", "-maxdepth", "1", "-name", filepath.Base(p), "-printf", "%U:%G:%m:%y")
	for _, policy := range []string{nativePolicyPath(t.Base), remoteProofPolicyPath(t.Base)} {
		grant("/usr/bin/cat", "--", policy)
		grant("/usr/bin/find", "/etc/sudoers.d", "-maxdepth", "1", "-name", filepath.Base(policy), "-printf", "%U:%G:%m:%y")
	}
	grant("/usr/bin/setpriv", "--version")
	grant("/usr/bin/systemctl", "show", Unit(t.Base.Owner, "signer"), "--property=ActiveState,MainPID")
	for _, action := range []string{"status", "update-pause", "update-recovery"} {
		args := appArgs(t.Base, action)
		if action != "status" {
			args = append(args, "--request-stdin")
		}
		grant(args...)
	}
	i := t.Base.Signer
	grant("/usr/bin/setpriv", "--reuid", strconv.Itoa(i.UID), "--regid", strconv.Itoa(i.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", t.dir()+"/atomic-guard", "fixture-install-guard-status", "--disposable-fixture", "--config", t.Base.Root+"/identity.json", "--intent-id", t.Binding.Index.Binding.Pause.Request.IntentID, "--guard-digest", update.InstallDigest(t.Binding))
	return []byte(b.String()), nil
}

func verifyAtomicPolicy(ctx context.Context, t InstallTransaction, plan atomicPairPlan, s *System) error {
	active, err := atomicPolicyExists(ctx, t, s)
	if err != nil || !active {
		return errors.New("exact atomic start policy is unavailable; preserve the installation")
	}
	u, err := user.LookupId(strconv.Itoa(t.Base.Owner))
	if err != nil {
		return errors.New("ordinary owner account is unavailable")
	}
	want, err := atomicPolicyBytes(t, plan, u.Username)
	if err != nil {
		return err
	}
	return verifyEffectiveAtomicPolicy(ctx, t, s, atomicPolicyPath(t), want)
}

func verifyEffectiveAtomicPolicy(ctx context.Context, t InstallTransaction, s *System, policyPath string, want []byte) error {
	meta, err := s.Run(ctx, nil, "/usr/bin/find", filepath.Dir(policyPath), "-maxdepth", "1", "-name", filepath.Base(policyPath), "-printf", "%U:%G:%m:%y")
	if err != nil || string(meta) != "0:0:440:f" {
		return errors.New("exact atomic start policy is unavailable; preserve the installation")
	}
	b, err := s.Run(ctx, nil, "/usr/bin/cat", "--", policyPath)
	if err != nil || !bytes.Equal(b, want) {
		return errors.New("atomic start policy changed; preserving it")
	}
	list, err := atomicInspectionOutput(ctx, 1024*1024, "/usr/bin/sudo", "-n", "-ll")
	if err != nil {
		return errors.New("cannot inspect effective password-free authority")
	}
	paths, err := passwordFreeSudoPaths(list)
	if err != nil {
		return err
	}
	atomic := policyPath
	if !slices.Contains(paths, atomic) {
		return errors.New("atomic start policy is not effective")
	}
	for _, path := range paths {
		if path == atomic {
			continue
		}
		var expected []byte
		switch path {
		case nativePolicyPath(t.Base):
			if !t.Base.NativeOwnerAccess {
				return errors.New("unexpected native policy authority")
			}
			expected = nativePolicy(t.Base)
		case remoteProofPolicyPath(t.Base):
			expected, err = remoteProofPolicy(t.Base)
			if err != nil {
				return err
			}
		default:
			return errors.New("additional password-free OS authority would bypass atomic ordering")
		}
		meta, err := s.Run(ctx, nil, "/usr/bin/find", filepath.Dir(path), "-maxdepth", "1", "-name", filepath.Base(path), "-printf", "%U:%G:%m:%y")
		if err != nil || string(meta) != "0:0:440:f" {
			return errors.New("foreign auxiliary owner policy")
		}
		actual, err := s.Run(ctx, nil, "/usr/bin/cat", "--", path)
		if err != nil || !bytes.Equal(actual, expected) {
			return errors.New("auxiliary owner policy changed")
		}
	}
	return nil
}

// sudo's own expanded listing is used only to reject unexpected password-free
// grants. Authenticated administrator access is a separate owner authority.
func passwordFreeSudoPaths(raw []byte) ([]string, error) {
	var result []string
	path := ""
	for _, line := range strings.Split(string(raw), "\n") {
		// A global or user-specific sudo default can make every otherwise
		// authenticated command password-free without an entry-level tag.
		// Reject it instead of classifying those commands as safe authority.
		if path == "" && strings.Contains(line, "!authenticate") {
			return nil, errors.New("password-free sudo default would bypass atomic ordering")
		}
		if strings.HasPrefix(line, "Sudoers entry: ") {
			path = strings.TrimPrefix(line, "Sudoers entry: ")
			if !filepath.IsAbs(path) || filepath.Clean(path) != path {
				return nil, errors.New("invalid sudo policy origin")
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "Options:") && strings.Contains(line, "!authenticate") {
			if path == "" || strings.TrimSpace(line) != "Options: !authenticate" {
				return nil, errors.New("unclassified password-free authority")
			}
			result = append(result, path)
		}
	}
	return result, nil
}

func atomicUnitInspection(ctx context.Context, unit string) (map[string]string, []byte, error) {
	const props = "--property=LoadState,FragmentPath,DropInPaths,NeedDaemonReload,Type,KillMode,NoNewPrivileges,ProtectSystem,User,Group,Conflicts,After"
	b, err := atomicInspectionOutput(ctx, 65536, "/usr/bin/systemctl", "show", unit, props)
	if err != nil {
		return nil, nil, errors.New("cannot inspect loaded atomic unit")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return nil, nil, errors.New("invalid atomic service metadata")
		}
		if _, exists := fields[key]; exists {
			return nil, nil, errors.New("ambiguous atomic service metadata")
		}
		fields[key] = value
	}
	b, err = atomicUnitServiceProperties(ctx, unit)
	if err != nil {
		return nil, nil, errors.New("cannot inspect loaded atomic command vectors")
	}
	start, err := decodeAtomicServiceProperties(b)
	if err != nil {
		return nil, nil, err
	}
	// Empty Exec* properties are omitted by systemctl show, even with --all.
	// One typed D-Bus snapshot proves they are empty without separate processes.
	for _, property := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload"} {
		fields[property] = ""
	}
	return fields, start, nil
}

func atomicUnitObject(unit string) string {
	var object strings.Builder
	object.WriteString("/org/freedesktop/systemd1/unit/")
	for _, b := range []byte(unit) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' {
			object.WriteByte(b)
		} else {
			fmt.Fprintf(&object, "_%02x", b)
		}
	}
	return object.String()
}

func atomicUnitServiceProperties(ctx context.Context, unit string) ([]byte, error) {
	return atomicInspectionOutput(ctx, 1024*1024, "/usr/bin/busctl", "--json=short", "call", "org.freedesktop.systemd1", atomicUnitObject(unit), "org.freedesktop.DBus.Properties", "GetAll", "s", "org.freedesktop.systemd1.Service")
}

func decodeAtomicServiceProperties(raw []byte) ([]byte, error) {
	var snapshot struct {
		Type string                       `json:"type"`
		Data []map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Type != "a{sv}" || len(snapshot.Data) != 1 {
		return nil, errors.New("invalid loaded atomic service properties")
	}
	properties := snapshot.Data[0]
	start, ok := properties["ExecStart"]
	if !ok {
		return nil, errors.New("loaded atomic start commands unavailable")
	}
	for _, property := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload"} {
		commands, exists := properties[property]
		if !exists || validateAtomicCommandRows(commands, nil) != nil {
			return nil, errors.New("unreviewed loaded atomic execution is present or cannot be inspected")
		}
	}
	// Newer systemd releases expose this additional hook; older supported
	// managers do not. If present, it must also be empty.
	if commands, exists := properties["ExecReloadPost"]; exists && validateAtomicCommandRows(commands, nil) != nil {
		return nil, errors.New("unreviewed loaded atomic execution is present")
	}
	return start, nil
}

func atomicUnitCommands(ctx context.Context, unit, property string) ([]byte, error) {
	if !slices.Contains([]string{"ExecStartPre", "ExecStart", "ExecStartPost", "ExecStopPost"}, property) {
		return nil, errors.New("unsupported atomic command property")
	}
	return atomicInspectionOutput(ctx, 1024*1024, "/usr/bin/busctl", "--json=short", "get-property", "org.freedesktop.systemd1", atomicUnitObject(unit), "org.freedesktop.systemd1.Service", property)
}

func atomicInspectionOutput(ctx context.Context, limit int64, path string, args ...string) ([]byte, error) {
	if err := trustedUtility(path); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LC_ALL=C"}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(out, limit+1))
	if readErr != nil || int64(len(b)) > limit {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("invalid OS inspection response")
	}
	if err = cmd.Wait(); err != nil {
		return nil, err
	}
	return b, nil
}

func readAtomicMigrationApproval(t InstallTransaction, read readHost, candidate func(string, string) (int64, error)) (migrationRunnerApproval, error) {
	if !t.valid() || read == nil || candidate == nil {
		return migrationRunnerApproval{}, errors.New("valid atomic maintenance context required")
	}
	f, err := read(t.dir() + "/migration-runner.json")
	var approval migrationRunnerApproval
	if err != nil || strict(f.Data, &approval) != nil || !known(f, mustJSON(approval), 0644) || !validMigrationApproval(t, approval) {
		return migrationRunnerApproval{}, errors.New("exact atomic recovery approval required")
	}
	pinned, err := readReusableMaintenanceAuthority(t.Base, read)
	if err != nil {
		return migrationRunnerApproval{}, err
	}
	if pinned != nil {
		if err = verifyReusableMaintenanceAuthority(t.Base, pinned, approval.RunnerSHA256, approval.GuardSHA256, candidate); err != nil {
			return migrationRunnerApproval{}, err
		}
	}
	return approval, nil
}

func admitAtomicPair(ctx context.Context, t InstallTransaction, s *System) (atomicPairPlan, error) {
	var empty atomicPairPlan
	approval, err := readAtomicMigrationApproval(t, readRoot, verifiedMigrationRunnerSize)
	if err != nil {
		return empty, err
	}
	c, err := signer.LoadConfig(t.Base.Root + "/identity.json")
	if err != nil {
		return empty, err
	}
	pin, err := readReusableMaintenanceAuthority(t.Base, readRoot)
	if err != nil {
		return empty, err
	}
	var plan atomicPairPlan
	if pin != nil {
		plan, err = makeReusableAtomicPairPlan(t, c, approval.GuardSHA256, approval.RunnerSHA256)
	} else {
		plan, err = makeAtomicPairPlan(t, c, approval.GuardSHA256, approval.RunnerSHA256)
	}
	if err != nil {
		return empty, err
	}
	if err = preflightRestartDropIns(t.Base, readRoot); err != nil {
		return empty, err
	}
	for path, data := range plan.inputs {
		f, err := readRoot(path)
		if err != nil || !known(f, data, 0644) {
			return empty, errors.New("atomic transaction inputs are incomplete or changed")
		}
	}
	if err = verifyRootExecutable(t.dir()+"/atomic-guard", plan.guardHash); err != nil {
		return empty, err
	}
	for _, name := range plan.names() {
		path := filepath.Join("/etc/systemd/system", name)
		f, err := readRoot(path)
		if err != nil || !known(f, plan.units[path], 0644) {
			return empty, errors.New("atomic service file is missing or changed")
		}
		fields, commands, err := atomicUnitInspection(ctx, name)
		if err != nil {
			return empty, err
		}
		if err = validateLoadedAtomicUnit(plan, name, fields, commands); err != nil {
			return empty, err
		}
	}
	if pin != nil {
		err = verifyReusableAtomicHost(ctx, t, s, pin)
	} else {
		err = verifyAtomicPolicy(ctx, t, plan, s)
	}
	if err != nil {
		return empty, err
	}
	return plan, nil
}

func runAtomicWorker(ctx context.Context, t InstallTransaction, s *System, plan atomicPairPlan, action string) error {
	name, err := atomicWorkerName(plan, action)
	if err != nil {
		return err
	}
	pin, err := readReusableMaintenanceAuthority(t.Base, readRoot)
	if err != nil {
		return err
	}
	worker := name
	requestID := ""
	if pin != nil {
		requestID, err = writeReusableAtomicRequest(t, action, "", "", "")
		if err != nil {
			return err
		}
		name = reusableAtomicLauncherName(t.Base.Owner)
	}
	if _, err := s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", name); err != nil {
		return fmt.Errorf("guarded %s operation did not complete; preserve and retry the same transaction: %w", action, err)
	}
	if pin != nil {
		return requireReusableWorkerCompletion(t.Base, requestID, worker, readReusableWorkerCompletion)
	}
	return nil
}

func atomicWorkerName(plan atomicPairPlan, action string) (string, error) {
	switch action {
	case "forward":
		return plan.forward, nil
	case "rollback":
		return plan.rollback, nil
	case "completion":
		return plan.completion, nil
	case "cleanup-rollback":
		return plan.cleanupRollback, nil
	case "cleanup-complete":
		return plan.cleanupComplete, nil
	default:
		return "", errors.New("unsupported atomic update action")
	}
}

func atomicPolicyExists(ctx context.Context, t InstallTransaction, s *System) (bool, error) {
	p := atomicPolicyPath(t)
	b, err := s.Run(ctx, nil, "/usr/bin/find", filepath.Dir(p), "-maxdepth", "1", "-name", filepath.Base(p), "-printf", "%U:%G:%m:%y")
	if err != nil {
		return false, errors.New("cannot inspect atomic policy; preserve installation")
	}
	if len(b) == 0 {
		return false, nil
	}
	if string(b) != "0:0:440:f" {
		return false, errors.New("foreign atomic policy; preserve it")
	}
	return true, nil
}

func prepareRestartGuards(ctx context.Context, m Manifest, read readHost, ensureDir func(string) error, run runHost) error {
	if m.validate() != nil || read == nil || ensureDir == nil || run == nil {
		return errors.New("valid managed restart guard context required")
	}
	if _, err := read(m.Root + "/update-start-blocked.json"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("existing update restart guard requires recovery; preserve it")
	}
	want := []byte("[Unit]\nAssertPathExists=!" + m.Root + "/update-start-blocked.json\n")
	var paths []string
	for _, role := range []string{"app", "signer"} {
		path := "/etc/systemd/system/" + Unit(m.Owner, role) + ".d/20-fased-update-guard.conf"
		f, err := read(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && !known(f, want, 0644) {
			return errors.New("foreign restart assertion; preserving it")
		}
		paths = append(paths, path)
	}
	for _, path := range paths {
		if err := ensureDir(filepath.Dir(path)); err != nil {
			return err
		}
		if err := publishAtomicPairUnit(ctx, path, want, read, run); err != nil {
			return err
		}
	}
	if _, err := run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	return preflightRestartDropIns(m, read)
}

func preflightAtomicPairInputs(owner int, plan atomicPairPlan, verify approvedExecutableVerifier) error {
	if verify == nil {
		return errors.New("approved input verifier required")
	}
	for _, data := range plan.inputs {
		if len(data) == 0 || verify(ApprovedExecutablePath(owner, data), sum(data), int64(len(data))) != nil {
			return errors.New("root-approved atomic transaction input unavailable or changed")
		}
	}
	for path, data := range plan.units {
		if _, err := BoundAtomicUnitWriteArgs(owner, path, data); err != nil {
			return err
		}
	}
	return nil
}

// prepareApprovedAtomicInputs creates digest-named immutable metadata sources.
// Existing or interrupted files are never overwritten, and only exact bytes
// may proceed to transaction provisioning.
func prepareApprovedAtomicInputs(ctx context.Context, owner int, plan atomicPairPlan, read readHost, run runHost, verify approvedExecutableVerifier) error {
	if owner <= 0 || owner != os.Getuid() || read == nil || run == nil || verify == nil {
		return errors.New("ordinary owner and protected input setup required")
	}
	paths := make([]string, 0, len(plan.inputs))
	for path := range plan.inputs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		data := plan.inputs[path]
		source := ApprovedExecutablePath(owner, data)
		f, err := read(source)
		if errors.Is(err, os.ErrNotExist) {
			args, bindErr := BoundApprovedInputWriteArgs(owner, data)
			if bindErr != nil {
				return bindErr
			}
			if _, err = run(ctx, data, args...); err != nil {
				return err
			}
			f, err = read(source)
		}
		if err != nil || !known(f, data, 0444) || verify(source, sum(data), int64(len(data))) != nil {
			return fmt.Errorf("approved transaction source %s is incomplete or foreign; preserving it", filepath.Base(source))
		}
	}
	return nil
}

// All approved sources are checked before writing. A policy that already
// admits starting these units freezes their inputs: a retry may inspect exact
// files, but must not create or replace any missing file under active policy.
func prepareAtomicPair(ctx context.Context, t InstallTransaction, plan atomicPairPlan, read readHost, run runHost, verify approvedExecutableVerifier, policyExists func() (bool, error)) error {
	if read == nil || run == nil || policyExists == nil {
		return errors.New("complete atomic setup context required")
	}
	if err := preflightAtomicPairInputs(t.Base.Owner, plan, verify); err != nil {
		return err
	}
	guard, err := read(filepath.Join(t.dir(), "atomic-guard"))
	if err != nil || guard.UID != 0 || !guard.Regular || guard.Mode != 0755 || sum(guard.Data) != plan.guardHash {
		return errors.New("independent signer guard unavailable or changed")
	}
	active, err := policyExists()
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(plan.inputs)+len(plan.units))
	for path := range plan.inputs {
		paths = append(paths, path)
	}
	for path := range plan.units {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if active {
		for _, path := range paths {
			data, ok := plan.inputs[path]
			if !ok {
				data = plan.units[path]
			}
			f, err := read(path)
			if err != nil || !known(f, data, 0644) {
				return errors.New("atomic setup is incomplete under active policy; preserving it")
			}
		}
		return nil
	}
	for _, path := range paths {
		if data, ok := plan.inputs[path]; ok {
			if err := publish(ctx, path, t.Base.ID, data, 0644, read, run, verify); err != nil {
				return err
			}
		} else if err := publishAtomicPairUnit(ctx, path, plan.units[path], read, run); err != nil {
			return err
		}
	}
	_, err = run(ctx, nil, "/usr/bin/systemctl", "daemon-reload")
	return err
}

func publishAtomicPairUnit(ctx context.Context, target string, data []byte, read readHost, run runHost) error {
	current, err := read(target)
	if err == nil {
		if !known(current, data, 0644) {
			return errors.New("foreign or incomplete atomic unit; preserve it")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	args, err := BoundAtomicUnitWriteArgs(os.Getuid(), target, data)
	if err != nil {
		return err
	}
	if _, err = run(ctx, data, args...); err != nil {
		return err
	}
	current, err = read(target)
	if err != nil || !known(current, data, 0644) {
		return errors.New("atomic unit creation incomplete; preserve it")
	}
	return nil
}

// atomicPairPlan binds both systemd actions and every small root-owned input
// to one signer installation. Only trusted OS commands run as root. The guard
// executable runs with the signer's locked unprivileged UID and GID.
type atomicPairPlan struct {
	forward, rollback, completion, cleanupRollback, cleanupComplete string
	guardHash                                                       string
	units                                                           map[string][]byte
	inputs                                                          map[string][]byte
	commands                                                        map[string][]atomicPairCommand
}

func (p atomicPairPlan) names() []string {
	return []string{p.forward, p.rollback, p.completion, p.cleanupRollback, p.cleanupComplete}
}

type atomicPairCommand struct {
	argv   []string
	ignore bool
}

// Unit arguments are generated exclusively from validated IDs, numeric UIDs,
// digests and fixed paths. The only quoting used is the checksum grep line.
func parseAtomicPairCommands(body []byte) ([]atomicPairCommand, error) {
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Exec") && (line != trimmed || !strings.HasPrefix(line, "ExecStart=")) {
			return nil, errors.New("unreviewed atomic execution section")
		}
	}
	return parseAtomicCommands(body, "ExecStart", "")
}

func parseAtomicCommands(body []byte, section string, allowed ...string) ([]atomicPairCommand, error) {
	var result []atomicPairCommand
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, section+"=") {
			continue
		}
		value := strings.TrimPrefix(line, section+"=")
		ignore := strings.HasPrefix(value, "-")
		if ignore {
			value = value[1:]
		}
		if strings.HasPrefix(value, "+") {
			value = value[1:]
		}
		var args []string
		var word strings.Builder
		quoted, present := false, false
		characters := []rune(value)
		for i := 0; i < len(characters); i++ {
			ch := characters[i]
			switch {
			case ch == '"':
				quoted = !quoted
				present = true
			case ch == '\\':
				// systemd requires a standalone literal semicolon to be escaped.
				// Accept no other escape or concatenation in inspected commands.
				if quoted || present || i+1 >= len(characters) || characters[i+1] != ';' || (i+2 < len(characters) && characters[i+2] != ' ') {
					return nil, errors.New("unsupported atomic unit argument")
				}
				word.WriteRune(';')
				present = true
				i++
			case ch == '\'' || ch == '%':
				return nil, errors.New("unsupported atomic unit argument")
			case ch == ' ' && !quoted:
				if present {
					args = append(args, word.String())
					word.Reset()
					present = false
				}
			default:
				word.WriteRune(ch)
				present = true
			}
		}
		if quoted {
			return nil, errors.New("unclosed atomic unit argument")
		}
		if present {
			args = append(args, word.String())
		}
		if len(args) == 0 || (!strings.HasPrefix(args[0], "/usr/bin/") && !slices.Contains(allowed, args[0])) {
			return nil, errors.New("invalid atomic OS command")
		}
		result = append(result, atomicPairCommand{argv: args, ignore: ignore})
	}
	if len(result) == 0 {
		return nil, errors.New("atomic unit has no commands")
	}
	return result, nil
}

// validateLoadedAtomicUnit compares systemd's actual command vectors, not
// merely the root-owned unit file. Unknown drop-ins and stale loads refuse.
func validateLoadedAtomicUnit(plan atomicPairPlan, name string, fields map[string]string, raw []byte) error {
	if !slices.Contains(plan.names(), name) {
		return errors.New("foreign atomic unit")
	}
	contains := func(s, want string) bool { return slices.Contains(strings.Fields(s), want) }
	if fields["LoadState"] != "loaded" || fields["FragmentPath"] != filepath.Join("/etc/systemd/system", name) || fields["DropInPaths"] != "" || fields["NeedDaemonReload"] != "no" || fields["Type"] != "oneshot" || fields["KillMode"] != "control-group" || fields["NoNewPrivileges"] != "yes" || fields["ProtectSystem"] != "strict" || fields["User"] != "" || fields["Group"] != "" {
		return errors.New("atomic unit identity, isolation or ordering differs")
	}
	for _, property := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload"} {
		value, present := fields[property]
		if !present || value != "" {
			return errors.New("unreviewed loaded atomic execution")
		}
	}
	for _, other := range plan.names() {
		if other != name && !contains(fields["Conflicts"], other) {
			return errors.New("atomic worker conflict is missing")
		}
	}
	idx := slices.Index(plan.names(), name)
	for _, later := range plan.names()[idx+1:] {
		if !contains(fields["After"], later) {
			return errors.New("atomic worker ordering is missing")
		}
	}
	return validateAtomicCommandRows(raw, plan.commands[name])
}

func validateAtomicCommandRows(raw []byte, expected []atomicPairCommand) error {
	var prop struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	var rows [][]json.RawMessage
	if json.Unmarshal(raw, &prop) != nil || prop.Type != "a(sasbttttuii)" || len(prop.Data) == 0 || bytes.Equal(prop.Data, []byte("null")) || json.Unmarshal(prop.Data, &rows) != nil || len(rows) != len(expected) {
		return errors.New("invalid loaded atomic command list")
	}
	for n, row := range rows {
		if len(row) != 10 {
			return errors.New("invalid loaded atomic command")
		}
		var path string
		var argv []string
		var ignore bool
		if json.Unmarshal(row[0], &path) != nil || json.Unmarshal(row[1], &argv) != nil || json.Unmarshal(row[2], &ignore) != nil || len(argv) == 0 || path != argv[0] || !slices.Equal(argv, expected[n].argv) || ignore != expected[n].ignore {
			return errors.New("loaded atomic command differs from the reviewed unit")
		}
	}
	return nil
}

func makeAtomicPairPlan(t InstallTransaction, c signer.Config, guardHash, runnerHash string) (atomicPairPlan, error) {
	var plan atomicPairPlan
	pause := t.Binding.Index.Binding.Pause
	if !t.valid() || pause.Request == nil || !update.IDOK(pause.Request.IntentID) || !update.IDOK(pause.PauseID) || c.Validate() != nil || c.SignerID != t.Base.ID || c.OwnerUID != t.Base.Owner || c.AppUID != t.Base.App.UID || c.SignerUID != t.Base.Signer.UID || c.StateDir != t.Base.Root+"/signer-state" || c.SocketDir != t.Base.Root+"/signer-ipc" || !validHex(guardHash, 32) || guardHash == t.Base.SignerHash || guardHash == t.Target.SignerHash || !validHex(runnerHash, 32) || runnerHash == t.Base.CoreHash || runnerHash == t.Target.CoreHash {
		return plan, errors.New("independent signer guard and exact installation identity required")
	}
	owner, err := user.LookupId(strconv.Itoa(t.Base.Owner))
	if err != nil {
		return plan, errors.New("ordinary installation owner unavailable")
	}
	ownerGID, err := strconv.Atoi(owner.Gid)
	if err != nil || ownerGID <= 0 {
		return plan, errors.New("ordinary installation owner group unavailable")
	}
	id := t.Binding.Index.Binding.Pause.Request.IntentID
	digest := update.InstallDigest(t.Binding)
	d := t.dir()
	plan.forward = fmt.Sprintf("fased-custody-%d-atomic-%s-forward.service", t.Base.Owner, id)
	plan.rollback = fmt.Sprintf("fased-custody-%d-atomic-%s-rollback.service", t.Base.Owner, id)
	plan.completion = fmt.Sprintf("fased-custody-%d-atomic-%s-completion.service", t.Base.Owner, id)
	plan.cleanupRollback = fmt.Sprintf("fased-custody-%d-atomic-%s-cleanup-rollback.service", t.Base.Owner, id)
	plan.cleanupComplete = fmt.Sprintf("fased-custody-%d-atomic-%s-cleanup-complete.service", t.Base.Owner, id)
	plan.guardHash = guardHash
	plan.inputs = map[string][]byte{
		filepath.Join(d, "transaction.json"):     mustJSON(t),
		filepath.Join(d, "base-install.json"):    mustJSON(t.Base),
		filepath.Join(d, "target-install.json"):  mustJSON(t.Target),
		filepath.Join(d, "atomic-rollback.json"): mustJSON(phaseRecord{1, digest, "rollback"}),
		filepath.Join(d, "base-intent.json"):     mustJSON(installerIntent(t.Base)),
		filepath.Join(d, "target-intent.json"):   mustJSON(installerIntent(t.Target)),
		filepath.Join(d, "base-ready.json"):      readyRecord(t.Base),
		filepath.Join(d, "target-ready.json"):    readyRecord(t.Target),
		filepath.Join(d, "atomic-held.json"):     mustJSON(update.Installation{Version: 1, Binding: t.Binding, Digest: digest, Phase: "active"}),
		filepath.Join(d, "atomic-active.json"):   mustJSON(phaseRecord{1, digest, id}),
	}
	var markers strings.Builder
	for _, direction := range []string{"forward", "rollback", "completion", "cleanup-rollback", "cleanup-complete"} {
		marker := mustJSON(signer.InstallationGuardMarker{Version: 1, Owner: t.Base.Owner, SignerID: c.SignerID, IntentID: id, Digest: digest, Direction: direction})
		plan.inputs[filepath.Join(d, "atomic-marker-"+direction+".json")] = marker
		markers.Write(marker)
		markers.WriteByte('\n')
	}
	plan.inputs[filepath.Join(d, "atomic-markers.txt")] = []byte(markers.String())
	guard := filepath.Join(d, "atomic-guard")
	runner := filepath.Join(d, "migration-runner")
	checksumPath := filepath.Join(d, "atomic-checksums.sha256")
	var sums strings.Builder
	inputNames := []string{"transaction.json", "atomic-marker-forward.json", "atomic-marker-rollback.json", "atomic-marker-completion.json", "atomic-marker-cleanup-rollback.json", "atomic-marker-cleanup-complete.json", "atomic-markers.txt", "base-install.json", "target-install.json", "atomic-rollback.json", "base-intent.json", "target-intent.json", "base-ready.json", "target-ready.json", "atomic-held.json", "atomic-active.json"}
	for _, name := range inputNames {
		p := filepath.Join(d, name)
		fmt.Fprintf(&sums, "%s  %s\n", sum(plan.inputs[p]), p)
	}
	fmt.Fprintf(&sums, "%s  %s\n", guardHash, guard)
	fmt.Fprintf(&sums, "%s  %s\n", runnerHash, runner)
	plan.inputs[checksumPath] = []byte(sums.String())
	plan.units = map[string][]byte{}
	plan.commands = map[string][]atomicPairCommand{}
	for _, direction := range []string{"forward", "rollback", "completion", "cleanup-rollback", "cleanup-complete"} {
		name := map[string]string{"forward": plan.forward, "rollback": plan.rollback, "completion": plan.completion, "cleanup-rollback": plan.cleanupRollback, "cleanup-complete": plan.cleanupComplete}[direction]
		var others []string
		for _, other := range plan.names() {
			if other != name {
				others = append(others, other)
			}
		}
		var unit strings.Builder
		fmt.Fprintf(&unit, "[Unit]\nDescription=Fased guarded custody pair %s\nConflicts=%s\n", direction, strings.Join(others, " "))
		// One ordering edge covers both start/stop directions. Systemd stops a
		// conflicting worker before starting the other, including on failure.
		idx := slices.Index(plan.names(), name)
		if idx+1 < len(plan.names()) {
			fmt.Fprintf(&unit, "After=%s\n", strings.Join(plan.names()[idx+1:], " "))
		}
		unit.WriteString("\n[Service]\nType=oneshot\nKillMode=control-group\nTimeoutStartSec=15min\nRestart=no\nNoNewPrivileges=yes\nPrivateTmp=yes\nProtectHome=yes\nProtectSystem=strict\n")
		fmt.Fprintf(&unit, "ReadWritePaths=%s\n", t.Base.Root)
		// Never execute the guard until its file and every parent are known to
		// be root-controlled. find's batched false makes an unsafe match fail.
		for _, parent := range []string{"/", "/var", "/var/lib", t.Base.Root, filepath.Join(t.Base.Root, "updates"), d} {
			fmt.Fprintf(&unit, "ExecStart=/usr/bin/find %s -maxdepth 0 ( ! -type d -o ! -uid 0 -o -perm /022 ) -exec /usr/bin/false {} +\n", parent)
		}
		var metadataPaths []string
		for _, input := range inputNames {
			metadataPaths = append(metadataPaths, filepath.Join(d, input))
		}
		metadataPaths = append(metadataPaths, checksumPath)
		for _, file := range metadataPaths {
			fmt.Fprintf(&unit, "ExecStart=/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0644 ) -exec /usr/bin/false {} +\n", file)
		}
		fmt.Fprintf(&unit, "ExecStart=/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0755 ) -exec /usr/bin/false {} +\n", guard)
		fmt.Fprintf(&unit, "ExecStart=/usr/bin/find %s -maxdepth 0 ( ! -type f -o ! -uid 0 -o ! -gid 0 -o ! -perm 0755 ) -exec /usr/bin/false {} +\n", runner)
		// grep binds each expected checksum line to this immutable unit; extra
		// lines in the checksum file can only cause sha256sum to refuse.
		for _, line := range strings.Split(strings.TrimSuffix(sums.String(), "\n"), "\n") {
			fmt.Fprintf(&unit, "ExecStart=/usr/bin/grep -Fqx -- \"%s\" %s\n", line, checksumPath)
		}
		fmt.Fprintf(&unit, "ExecStart=/usr/bin/sha256sum --check --status -- %s\n", checksumPath)
		// Presence after an interrupted provision is not a durability proof.
		// Re-sync the exact transaction and its parent before fencing or any
		// replacement, including retries after a failed final provision sync.
		fmt.Fprintf(&unit, "ExecStart=/usr/bin/sync -f %s\nExecStart=/usr/bin/sync -f %s/updates\n", d, t.Base.Root)
		rollbackRecord := filepath.Join(d, "rollback.json")
		if direction == "forward" || direction == "completion" {
			fmt.Fprintf(&unit, "ExecStart=/usr/bin/test ! -e %s\nExecStart=/usr/bin/test ! -L %s\n", rollbackRecord, rollbackRecord)
		}
		// A directly invoked worker must not strand a healthy installation by
		// stopping services before an actual signer hold exists. A prior exact
		// restart marker permits guarded cold recovery after interruption.
		fmt.Fprintf(&unit, "ExecStart=/usr/bin/setpriv --reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-install-start-guard --disposable-fixture --config %s/identity.json --intent-id %s --guard-digest %s --guard-direction %s\n", t.Base.Owner, ownerGID, guard, t.Base.Root, id, digest, direction)
		markerSource, markerPath := filepath.Join(d, "atomic-marker-"+direction+".json"), t.Base.Root+"/update-start-blocked.json"
		markerList := filepath.Join(d, "atomic-markers.txt")
		fmt.Fprintf(&unit, "ExecStart=-/usr/bin/ln --no-target-directory -- %s %s\nExecStart=/usr/bin/grep -Fqx -f %s -- %s\n", markerSource, markerPath, markerList, markerPath)
		// The marker must survive a reboot before either service is stopped or
		// any live file is replaced; otherwise a mixed pair could autostart.
		fmt.Fprintf(&unit, "ExecStart=/usr/bin/sync -f %s\n", t.Base.Root)
		fmt.Fprintf(&unit, "ExecStart=/usr/bin/systemctl stop -- %s %s\n", Unit(t.Base.Owner, "app"), Unit(t.Base.Owner, "signer"))
		guardCommand := func(stage string) {
			core := t.Binding.TargetCore
			component := "core"
			if direction == "rollback" {
				core = t.Binding.PreviousCore
			}
			if direction == "cleanup-rollback" || direction == "cleanup-complete" {
				component, core = "installation", digest
			}
			fmt.Fprintf(&unit, "ExecStart=/usr/bin/setpriv --reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-install-guard --disposable-fixture --config %s/identity.json --intent-id %s --guard-digest %s --guard-direction %s --guard-component %s --guard-source-sha256 %s --guard-stage %s\n", t.Base.Signer.UID, t.Base.Signer.GID, guard, t.Base.Root, id, digest, direction, component, core, stage)
		}
		guardCommand("before")
		if direction == "cleanup-rollback" || direction == "cleanup-complete" {
			phase := "paused"
			if direction == "cleanup-complete" {
				phase = "released"
			}
			pause := t.Binding.Index.Binding.Pause
			requestDigest := update.SnapshotDigest("pause-request", *pause.Request)
			instanceDigest := update.SnapshotDigest("atomic-instance:v1", pause.Core.InstanceID)
			fmt.Fprintf(&unit, "ExecStart=/usr/bin/setpriv --reuid %d --regid %d --clear-groups --no-new-privs --inh-caps=-all --ambient-caps=-all --bounding-set=-all -- %s fixture-update-pause-guard --owner %d --disposable-fixture --intent-id %s --pause-id %s --request-digest %s --instance-digest %s --phase %s\n", t.Base.App.UID, t.Base.App.GID, runner, t.Base.Owner, id, pause.PauseID, requestDigest, instanceDigest, phase)
		}
		if direction == "forward" || direction == "rollback" {
			for _, record := range []struct{ source, target string }{{"atomic-held.json", filepath.Join(d, "held.json")}, {"atomic-active.json", t.Base.Root + "/update-active.json"}} {
				source := filepath.Join(d, record.source)
				fmt.Fprintf(&unit, "ExecStart=-/usr/bin/ln --no-target-directory -- %s %s\nExecStart=/usr/bin/cmp --silent -- %s %s\n", source, record.target, source, record.target)
			}
		}
		if direction == "rollback" {
			source := filepath.Join(d, "atomic-rollback.json")
			fmt.Fprintf(&unit, "ExecStart=-/usr/bin/ln --no-target-directory -- %s %s\nExecStart=/usr/bin/cmp --silent -- %s %s\n", source, rollbackRecord, source, rollbackRecord)
		}
		if direction == "forward" || direction == "rollback" {
			// held/active and any rollback decision precede file replacement.
			fmt.Fprintf(&unit, "ExecStart=/usr/bin/sync -f %s\nExecStart=/usr/bin/sync -f %s\n", d, t.Base.Root)
		}
		items := []struct{ source, dest string }{{"next-core", "fased-app"}, {"next-signer", "fased-signer"}, {"target-install.json", "install.json"}}
		if direction == "rollback" {
			items = []struct{ source, dest string }{{"runner", "fased-app"}, {"previous-signer", "fased-signer"}, {"base-install.json", "install.json"}}
		}
		if direction == "completion" {
			items = []struct{ source, dest string }{{"target-intent.json", "intent.json"}, {"target-ready.json", "ready.json"}}
		}
		if direction == "cleanup-rollback" || direction == "cleanup-complete" {
			items = nil
		}
		for _, item := range items {
			source := filepath.Join(d, item.source)
			stageName := ".atomic-" + id + "-" + direction + "-" + item.dest
			staged := filepath.Join(t.Base.Root, stageName)
			// rename(2) succeeds even when source and destination are the same
			// inode. GNU mv rejects that legitimate interrupted-update retry.
			fmt.Fprintf(&unit, "ExecStart=-/usr/bin/ln --no-target-directory -- %s %s\nExecStart=/usr/bin/test %s -ef %s\nExecStart=/usr/bin/rename.ul -- %s %s %s\nExecStart=/usr/bin/rm -f -- %s\n", source, staged, source, staged, stageName, item.dest, staged, staged)
		}
		if direction == "cleanup-rollback" || direction == "cleanup-complete" {
			fmt.Fprintf(&unit, "ExecStart=/usr/bin/rm -f -- %s/update-active.json\n", t.Base.Root)
		}
		fmt.Fprintf(&unit, "ExecStart=/usr/bin/sync -f %s\n", t.Base.Root)
		guardCommand("after")
		fmt.Fprintf(&unit, "ExecStart=/usr/bin/grep -Fqx -f %s -- %s\nExecStart=/usr/bin/rm -- %s\nExecStart=/usr/bin/sync -f %s\nExecStart=/usr/bin/systemctl start -- %s %s\n", markerList, markerPath, markerPath, t.Base.Root, Unit(t.Base.Owner, "signer"), Unit(t.Base.Owner, "app"))
		body := bytes.Clone([]byte(unit.String()))
		commands, err := parseAtomicPairCommands(body)
		if err != nil {
			return atomicPairPlan{}, err
		}
		plan.units[filepath.Join("/etc/systemd/system", name)] = body
		plan.commands[name] = commands
	}
	return plan, nil
}
