//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

func TestReusablePreparationInspectsEveryPermittedStageInstance(t *testing.T) {
	m := fixtureManifest()
	for _, kind := range reusableStageKinds {
		base, _, err := reusableStageClass(kind)
		if err != nil {
			t.Fatal(err)
		}
		body, err := reusableStageTemplate(m, 1000, kind)
		if err != nil {
			t.Fatal(err)
		}
		if err := inspectReusableStageInstances(t.Context(), m, 1000, kind, body, func(context.Context, string, string, []byte, string, string, []string, ...string) error { return nil }); err != nil {
			t.Fatalf("%s: unchanged permitted instances refused: %v", kind, err)
		}
		foreign := reusableStageSlots[base][len(reusableStageSlots[base])-1]
		seen := make(map[string]bool)
		inspect := func(_ context.Context, name, _ string, _ []byte, _, _ string, _ []string, _ ...string) error {
			seen[name] = true
			if strings.HasSuffix(name, "@"+foreign+".service") {
				return errors.New("foreign instance configuration")
			}
			return nil
		}
		if err := inspectReusableStageInstances(t.Context(), m, 1000, kind, body, inspect); err == nil {
			t.Fatalf("%s: foreign configuration on later allowed instance accepted", kind)
		}
		for _, slot := range reusableStageSlots[base] {
			name, err := reusableStageName(m.Owner, kind, slot)
			if err != nil || !seen[name] {
				t.Fatalf("%s: allowed instance %q was not inspected: %v", kind, slot, err)
			}
		}
	}
}

func TestReusableSetupActivatesOnlyAfterVerifiedConfigurationAndPolicy(t *testing.T) {
	m := fixtureManifest()
	pin := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	unit, checksum, err := reusableAtomicSetup(m, pin, "fased_owner")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := reusableAtomicPolicy(m, "fased_owner")
	if err != nil {
		t.Fatal(err)
	}
	pending := reusablePolicySelected(m)
	if string(checksum) != sum(policy)+"  "+pending+"\n" {
		t.Fatal("setup does not bind the exact policy")
	}
	text := string(unit)
	output := reusableMaintenancePath(m, "setup.env")
	markerCheck := strings.Index(text, "ExecStartPre=+/usr/bin/test "+output+" -ef "+output+".owner")
	truncate := strings.Index(text, "ExecStartPre=+/usr/bin/truncate --size=0 "+output)
	guard := strings.Index(text, "fixture-validate-reusable-setup")
	check := strings.Index(text, "ExecStart=+/usr/bin/sha256sum --check")
	visudo := strings.Index(text, "ExecStart=+/usr/sbin/visudo -cf")
	removeGrant := strings.Index(text, "-samefile "+reusableSetupGrantSource(m)+" -delete")
	link := strings.Index(text, "ExecStart=-+/usr/bin/ln --no-target-directory")
	if markerCheck < 0 || !(markerCheck < truncate && truncate < guard && guard < check && check < visudo && visudo < removeGrant && removeGrant < link) || !strings.Contains(text, "User=997\nGroup=997\n") || !strings.Contains(text, "ReadWritePaths=/etc/sudoers.d\n") || !strings.Contains(text, "grep -zFxq -- FASED_SETUP_READY=yes") || !strings.Contains(text, "Restart=on-failure\n") || !strings.Contains(text, "WantedBy=multi-user.target\n") || strings.Contains(text, "/bin/sh ") || strings.Contains(text, "/bin/bash ") {
		t.Fatal("setup can activate before its unprivileged guard and exact policy verification")
	}
	if _, err := BoundAtomicUnitWriteArgs(m.Owner, "/etc/systemd/system/"+reusableAtomicSetupName(m.Owner), unit); err != nil {
		t.Fatal("the exact owner setup unit cannot be provisioned", err)
	}
	if tool, err := exec.LookPath("systemd-analyze"); err == nil {
		unit = bytes.ReplaceAll(unit, []byte(reusableMaintenancePath(m, "atomic-guard")+" fixture-validate"), []byte("/usr/bin/true fixture-validate"))
		path := filepath.Join(t.TempDir(), reusableAtomicSetupName(m.Owner))
		if err = os.WriteFile(path, unit, 0600); err != nil {
			t.Fatal(err)
		}
		if output, verifyErr := exec.Command(tool, "verify", path).CombinedOutput(); verifyErr != nil {
			t.Fatalf("invalid setup service: %v: %s", verifyErr, output)
		}
	}
}

func TestReusableSetupSelectionCommandsRemainInspectable(t *testing.T) {
	m := fixtureManifest()
	pin := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	body, _, err := reusableAtomicSetup(m, pin, "fased_owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = parseAtomicCommands(body, "ExecStart", reusableMaintenancePath(m, "atomic-guard"), "/usr/sbin/visudo"); err != nil {
		t.Fatal("the actual setup service cannot be checked against loaded commands", err)
	}
	if _, err = parseAtomicCommands([]byte("ExecStart=/usr/bin/true \\x\n"), "ExecStart"); err == nil {
		t.Fatal("unrecognized systemd escapes were accepted")
	}
}

func TestReusableTemporaryPolicyBindsSetupAndHasValidSyntax(t *testing.T) {
	m := fixtureManifest()
	pin := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	policy, err := draftReusableSetupTemporaryPolicy(m, pin, 1000, "fased_owner", 100, 200, 1893456000)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("temporary policy size: %d", len(policy))
	if len(policy) > 512*1024 {
		t.Fatal("temporary setup policy exceeds the guard's bounded input")
	}
	if bytes.Contains(policy, []byte(" NOPASSWD: /usr/bin/ln ")) {
		t.Fatal("temporary setup policy grants unchecked direct publication")
	}
	for _, name := range []string{"runner", "atomic-guard"} {
		pending := reusableMaintenancePath(m, "."+name+"-"+m.ID+".pending")
		if bytes.Contains(policy, []byte(" NOPASSWD: /usr/bin/rm -- "+pending)) {
			t.Fatal("temporary setup policy grants direct partial-image deletion")
		}
	}
	if bytes.Contains(policy, []byte("/usr/bin/ln --no-target-directory -- "+reusablePolicySelected(m)+" "+reusableAtomicPolicyPath(m.Owner))) || !bytes.Contains(policy, []byte("/usr/bin/systemctl start -- "+reusableAtomicSetupName(m.Owner))) || bytes.Contains(policy, []byte("ALL=(root) NOPASSWD:")) {
		t.Fatal("temporary policy must not directly publish recurring authority")
	}
	if tool, err := exec.LookPath("visudo"); err == nil {
		path := filepath.Join(t.TempDir(), "setup-policy")
		if err = os.WriteFile(path, policy, 0600); err != nil {
			t.Fatal(err)
		}
		if output, verifyErr := exec.Command(tool, "-cf", path).CombinedOutput(); verifyErr != nil {
			t.Fatalf("invalid temporary setup policy: %v: %s", verifyErr, output)
		}
	}
}

func TestReusableCompatibilityRefusesUnrecoverablePair(t *testing.T) {
	tx, _ := modelTransaction(t)
	coreContract, signerContract := core.UpdateContract(), signer.UpdateContract()
	responses := func(bad string) func(string, bool) ([]byte, error) {
		return func(name string, isCore bool) ([]byte, error) {
			if isCore {
				c := coreContract
				if bad == "old-core" && name == "runner" {
					c.Lifecycle = nil
				}
				return json.Marshal(struct {
					Contract           update.Contract `json:"contract"`
					WalletSigner       bool            `json:"walletSigner"`
					FinancialExecution bool            `json:"financialExecution"`
					UpdaterEnabled     bool            `json:"updaterEnabled"`
				}{c, false, bad == "live" && name == "next-core", false})
			}
			c := signerContract
			if bad == "old-signer" && name == "previous-signer" {
				c.State = nil
			}
			if bad == "target-signer" && name == "next-signer" {
				c.Protocol++
			}
			return json.Marshal(c)
		}
	}
	if err := checkRollbackCompatibility(tx, responses("")); err != nil {
		t.Fatal("compatible pair refused:", err)
	}
	for _, bad := range []string{"old-core", "old-signer", "target-signer", "live"} {
		if err := checkRollbackCompatibility(tx, responses(bad)); err == nil {
			t.Errorf("unsafe pair accepted: %s", bad)
		}
	}
}

func TestReusableProvisionPublishesTransactionAfterDependencies(t *testing.T) {
	m := fixtureManifest()
	a := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	body, err := reusableAtomicProvision(m, a)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	stage := reusableMaintenancePath(m, "staging")
	txdir := m.Root + "/updates/${FASED_TXID}"
	publish := strings.Index(text, "ExecStart=-+/usr/bin/ln --no-target-directory -- "+stage+"/transaction "+txdir+"/transaction.json\n")
	for _, dependency := range []string{
		stage + "/previous-signer " + txdir + "/previous-signer",
		stage + "/runner " + txdir + "/runner",
		reusableMaintenancePath(m, "runner") + " " + txdir + "/migration-runner",
		"ExecStart=+/usr/bin/systemctl daemon-reload\n",
	} {
		at := strings.Index(text, dependency)
		if publish < 0 || at < 0 || at >= publish {
			t.Fatalf("transaction is visible before its dependency %q", dependency)
		}
	}
	if !strings.Contains(text[publish:], "ExecStart=+/usr/bin/sync -f "+txdir+"\n") {
		t.Fatal("published transaction is not synced")
	}
	admit := strings.Index(text, " --disposable-fixture --verify-published\n")
	sentinelCheck := strings.Index(text, "ExecStart=+/usr/bin/test "+reusableMaintenancePath(m, "preparation-fence.json")+" -ef "+m.Root+"/update-start-blocked.json\n")
	rename := strings.Index(text, "ExecStart=+/usr/bin/mv --no-target-directory -- "+m.Root+"/.preparation-forward-${FASED_TXID}-${INVOCATION_ID}.marker "+m.Root+"/update-start-blocked.json\n")
	if admit <= publish || sentinelCheck <= admit || rename <= sentinelCheck || strings.Contains(text, "ExecStart=+/usr/bin/rm -- "+m.Root+"/update-start-blocked.json") || !strings.Contains(text[rename:], "ExecStart=+/usr/bin/sync -f "+m.Root+"\n") {
		t.Fatal("the sentinel-to-forward handoff is not guarded, atomic and durable")
	}
}

func TestReusablePreparationFenceUsesFixedSentinelAndColdGuard(t *testing.T) {
	m := fixtureManifest()
	a := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	body, err := reusablePreparationFenceUnit(m, a)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	startCheck := strings.Index(text, " fixture-validate-preparation-start ")
	link := strings.Index(text, "ExecStart=-+/usr/bin/ln --no-target-directory -- "+reusableMaintenancePath(m, "preparation-fence.json")+" "+m.Root+"/update-start-blocked.json")
	sync := strings.Index(text, "ExecStart=+/usr/bin/sync -f "+m.Root+"\n")
	stop := strings.Index(text, "ExecStart=+/usr/bin/systemctl stop --")
	cold := strings.Index(text, " fixture-validate-preparation-fence --owner ")
	if startCheck < 0 || link <= startCheck || sync <= link || stop <= sync || cold <= stop {
		t.Fatal("preparation did not check the phase, durably fence, stop and inspect the signer in order")
	}
	if !strings.Contains(text, a.GuardSHA256+"  "+reusableMaintenancePath(m, "atomic-guard")) || strings.Contains(text, "/bin/sh ") || strings.Contains(text, "/bin/bash ") {
		t.Fatal("preparation fence lacks its pinned guard or invokes a root shell")
	}
}

func TestReusablePreparationMarkerSwapPreservesFence(t *testing.T) {
	m := fixtureManifest()
	a := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	body, err := reusableAtomicProvision(m, a)
	if err != nil {
		t.Fatal(err)
	}
	commands, err := parseAtomicCommands(body, "ExecStart", reusableMaintenancePath(m, "atomic-guard"))
	if err != nil {
		t.Fatal(err)
	}
	var rename atomicPairCommand
	for _, command := range commands {
		if command.argv[0] == "/usr/bin/mv" {
			rename = command
		}
	}
	if len(rename.argv) != 5 {
		t.Fatal("atomic marker replacement is missing")
	}
	root := t.TempDir()
	marker, sentinel, forward := filepath.Join(root, "update-start-blocked.json"), filepath.Join(root, "sentinel"), filepath.Join(root, "forward")
	pending := strings.ReplaceAll(strings.ReplaceAll(rename.argv[3], m.Root, root), "${FASED_TXID}", strings.Repeat("a", 32))
	pending = strings.ReplaceAll(pending, "${INVOCATION_ID}", strings.Repeat("b", 32))
	if err = os.WriteFile(sentinel, []byte("sentinel"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(forward, []byte("bound forward"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(sentinel, marker); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(forward, pending); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(marker)
	if err != nil {
		t.Fatal("the fence was absent before replacement", err)
	}
	args := append([]string{}, rename.argv[1:]...)
	args[2] = pending
	args[3] = marker
	if output, err := exec.Command(rename.argv[0], args...).CombinedOutput(); err != nil {
		t.Fatalf("marker replacement failed: %v: %s", err, output)
	}
	after, err := os.Stat(marker)
	forwardInfo, forwardErr := os.Stat(forward)
	if err != nil || forwardErr != nil || os.SameFile(before, after) || !os.SameFile(after, forwardInfo) {
		t.Fatal("the replacement did not retain the exact forward fence inode", err, forwardErr)
	}
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "sentinel" {
		t.Fatal("the approved sentinel source changed", err)
	}
}

func TestReusableCompatibilityPreservesForeignOutput(t *testing.T) {
	m := fixtureManifest()
	a := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	files, _, err := reusableSetupFiles(m, a, 1000, "fased_owner", 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, slot := range reusableCompatibilitySlots {
		body, err := reusableCompatibilityUnit(m, 1000, slot)
		if err != nil {
			t.Fatal(err)
		}
		output := reusableMaintenancePath(m, "compat-"+slot+".out")
		text := string(body)
		if !strings.Contains(text, "DynamicUser=yes\n") || !strings.Contains(text, "SystemCallArchitectures=native\n") || !strings.Contains(text, "SystemCallFilter=~@network-io @aio\n") || strings.Contains(text, "User=1000\n") || strings.Contains(text, "Group=1000\n") {
			t.Fatalf("%s compatibility can transfer its output descriptor or be controlled by the owner", slot)
		}
		for _, other := range reusableCompatibilitySlots {
			if other != slot && strings.Contains(text, "compat-"+other+".out") {
				t.Fatalf("%s compatibility can write %s output", slot, other)
			}
		}
		marker := strings.Index(text, "ExecStartPre=+/usr/bin/test "+output+" -ef "+output+".owner")
		truncate := strings.Index(text, "ExecStartPre=+/usr/bin/truncate --size=0 "+output)
		if marker < 0 || truncate <= marker || !strings.Contains(text, "AssertPathExists="+output+".owner") {
			t.Fatalf("%s compatibility may truncate an unclassified output", slot)
		}
		found := false
		for _, file := range files {
			if file.path == output && bytes.Equal(file.data, []byte("FASED_MAINTENANCE\n")) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s compatibility output has no protected setup record", slot)
		}
	}
}

func TestReusableLauncherValidatesWorkerStartAndStop(t *testing.T) {
	m := fixtureManifest()
	a := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	body, checksum, err := reusableAtomicLauncher(m, a)
	if err != nil {
		t.Fatal(err)
	}
	guard := reusableMaintenancePath(m, "atomic-guard")
	if string(checksum) != a.GuardSHA256+"  "+guard+"\n" {
		t.Fatal("guard digest was not bound")
	}
	text := string(body)
	guardRun := strings.Index(text, "ExecStart="+guard+" fixture-validate-atomic-worker")
	startCheck := strings.Index(text, "ExecStart=+/usr/bin/grep -zEq")
	start := strings.Index(text, "ExecStart=+/usr/bin/systemctl start -- ${FASED_WORKER_UNIT}")
	stopCheck := strings.Index(text, "ExecStopPost=+/usr/bin/grep -zEq")
	stop := strings.Index(text, "ExecStopPost=+/usr/bin/systemctl stop -- ${FASED_WORKER_UNIT}")
	if guardRun < 0 || !(guardRun < startCheck && startCheck < start && start < stopCheck && stopCheck < stop) {
		t.Fatal("launcher permits an unchecked worker start or stop")
	}
	if !strings.Contains(text, "User=997\nGroup=997\n") || !strings.Contains(text, "StartLimitIntervalSec=60\nStartLimitBurst=64\n") || !strings.Contains(text, "StandardError=journal\n") || strings.Contains(text, "/bin/sh ") || strings.Contains(text, "/bin/bash ") {
		t.Fatal("launcher does not isolate its guard or contains a root shell")
	}
	policy, err := reusableAtomicPolicy(m, "fased_owner")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(policy)), "\n")
	if len(lines) == 0 || lines[0] != "fased_owner ALL=(root) NOPASSWD: /usr/bin/systemctl start -- "+reusableAtomicLauncherName(m.Owner) {
		t.Fatal("policy does not contain the single serialized start")
	}
	starts := 0
	for _, line := range lines {
		if strings.Contains(line, "NOPASSWD: /usr/bin/systemctl start -- ") {
			starts++
		}
		if strings.Contains(line, "/usr/bin/mv") || strings.Contains(line, "/usr/bin/install") || strings.Contains(line, "/usr/bin/rm") || strings.Contains(line, "/usr/bin/systemd-run") {
			t.Fatal("direct privileged mutation granted", line)
		}
	}
	if starts != 1 {
		t.Fatal("policy grants more than the serialized systemd start")
	}
	if visudo, lookupErr := exec.LookPath("visudo"); lookupErr == nil {
		path := filepath.Join(t.TempDir(), "policy")
		if err := os.WriteFile(path, policy, 0600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(visudo, "-cf", path).CombinedOutput(); err != nil {
			t.Fatalf("invalid reusable policy: %v: %s", err, output)
		}
	}
	if _, err := reusableAtomicPolicy(m, "bad owner"); err == nil {
		t.Fatal("unsafe owner name accepted")
	}
	provision, err := reusableAtomicProvision(m, a)
	if err != nil {
		t.Fatal(err)
	}
	pending := "/etc/systemd/system/." + reusableAtomicProvisionName(m.Owner) + "-" + m.ID + ".pending"
	if _, err := BoundAtomicUnitWriteArgs(m.Owner, pending, provision); err != nil {
		t.Fatal("provision service exceeds its bounded publication", err)
	}
	provisionText := string(provision)
	if !strings.Contains(provisionText, "ReadWritePaths="+m.Root+"/updates /etc/systemd/system\n") || !strings.Contains(provisionText, "LimitFSIZE=32768\n") {
		t.Fatal("protected provisioning cannot write its bounded transaction and unit files")
	}
	checked := strings.Index(provisionText, " fixture-validate-atomic-provision ")
	capability := strings.Index(provisionText, " fixture-update-pause-guard --recovery-capability")
	linked := strings.Index(provisionText, "ExecStart=-+/usr/bin/ln --no-target-directory")
	if checked < 0 || capability <= checked || linked <= capability || !strings.Contains(provisionText, "FASED_TXID=[0-9a-f]{32}$$") || !strings.Contains(provisionText, "atomic-${FASED_TXID}-forward.service") {
		t.Fatal("provisioning can publish before exact signer validation")
	}
	if !strings.Contains(provisionText, "ExecStart=+/usr/bin/install -m 0644 -o root -g root -- "+reusableMaintenancePath(m, "staging")+"/unit-forward ") {
		t.Fatal("unit publication does not support separate /var and /etc filesystems")
	}
	compat, err := reusableCompatibilityUnit(m, m.Owner, "runner")
	if err != nil || !strings.Contains(string(compat), "DynamicUser=yes\n") || !strings.Contains(string(compat), "PrivateNetwork=yes\n") {
		t.Fatal("compatibility commands must run as an isolated unprivileged user", err)
	}
	stage := reusableMaintenancePath(m, "staging")
	if _, err = parseAtomicCommands(compat, "ExecStart", stage+"/runner", stage+"/next-core", stage+"/previous-signer", stage+"/next-signer"); err != nil {
		t.Fatal("cannot verify loaded compatibility commands", err)
	}
	tool, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze unavailable")
	}
	// Only the command location is substituted; parsing and all arguments stay.
	body = bytes.ReplaceAll(body, []byte(guard+" fixture-validate"), []byte("/usr/bin/true fixture-validate"))
	path := filepath.Join(t.TempDir(), reusableAtomicLauncherName(m.Owner))
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(tool, "verify", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		blocked := len(lines) > 0
		for _, line := range lines {
			if line != "Failed to turn off SO_PASSRIGHTS on user lookup socket, ignoring: Operation not permitted" && line != "Failed to enable SO_PASSCRED on handoff timestamp socket: Operation not permitted" {
				blocked = false
			}
		}
		if blocked {
			t.Skip("sandbox blocks systemd-analyze sockets")
		}
		t.Fatalf("invalid fixed launcher: %v: %s", err, output)
	}
	provision = bytes.ReplaceAll(provision, []byte(guard+" fixture-"), []byte("/usr/bin/true fixture-"))
	path = filepath.Join(t.TempDir(), reusableAtomicProvisionName(m.Owner))
	if err := os.WriteFile(path, provision, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(tool, "verify", path).CombinedOutput(); err != nil {
		t.Fatalf("invalid fixed provision service: %v: %s", err, output)
	}
	for _, name := range []string{"runner", "next-core", "previous-signer", "next-signer"} {
		compat = bytes.ReplaceAll(compat, []byte(stage+"/"+name+" "), []byte("/usr/bin/true "))
	}
	compatName, err := reusableCompatibilityName(m.Owner, "runner")
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), compatName)
	if err := os.WriteFile(path, compat, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(tool, "verify", path).CombinedOutput(); err != nil {
		t.Fatalf("invalid fixed compatibility service: %v: %s", err, output)
	}
}

func TestReusableProvisionIsNotDirectlyGranted(t *testing.T) {
	m := fixtureManifest()
	policy, err := reusableAtomicPolicy(m, "fased_owner")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(policy, []byte("NOPASSWD: /usr/bin/systemctl start -- "+reusableAtomicProvisionName(m.Owner))) {
		t.Fatal("provision can start outside the serialized launcher")
	}
	a := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	unit, _, err := reusableAtomicLauncher(m, a)
	if err != nil {
		t.Fatal(err)
	}
	_, tail, ok := strings.Cut(string(unit), "ExecStart=+/usr/bin/grep -zEq \"")
	if !ok {
		t.Fatal("launcher request check missing")
	}
	pattern, _, ok := strings.Cut(tail, "\" ")
	if !ok {
		t.Fatal("launcher request pattern missing")
	}
	re, err := regexp.Compile("(?s)" + strings.ReplaceAll(pattern, "$$", "$"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"FASED_REQUEST_ID=" + strings.Repeat("b", 32) + "\nFASED_WORKER_UNIT=" + reusableAtomicProvisionName(m.Owner) + "\n", true},
		{fmt.Sprintf("FASED_REQUEST_ID=%s\nFASED_WORKER_UNIT=fased-custody-%d-atomic-%s-forward.service\n", strings.Repeat("b", 32), m.Owner, strings.Repeat("a", 32)), true},
		{"FASED_REQUEST_ID=" + strings.Repeat("b", 32) + "\nFASED_WORKER_UNIT=" + reusableAtomicProvisionName(m.Owner+1) + "\n", false},
		{"FASED_REQUEST_ID=" + strings.Repeat("b", 32) + "\nFASED_WORKER_UNIT=" + reusableAtomicProvisionName(m.Owner) + "\nFASED_WORKER_UNIT=foreign", false},
	} {
		if re.MatchString(tc.value) != tc.want {
			t.Fatalf("launcher pattern admitted %q incorrectly", tc.value)
		}
	}
}

func TestReusableWorkerCompletionBindsFinishedRequest(t *testing.T) {
	m := fixtureManifest()
	requestID := strings.Repeat("a", 32)
	worker := fmt.Sprintf("fased-custody-%d-atomic-%s-forward.service", m.Owner, strings.Repeat("b", 32))
	want := "FASED_REQUEST_ID=" + requestID + "\nFASED_WORKER_UNIT=" + worker + "\nFASED_WORKER_COMPLETED=yes"
	for _, tc := range []struct {
		name string
		file hostFile
		ok   bool
	}{
		{"finished request", hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte(want)}, true},
		{"worker still running", hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte(strings.TrimSuffix(want, "FASED_WORKER_COMPLETED=yes"))}, false},
		{"different request", hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte(strings.Replace(want, requestID, strings.Repeat("c", 32), 1))}, false},
		{"different worker", hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte(strings.Replace(want, "-forward.service", "-rollback.service", 1))}, false},
		{"owner-controlled result", hostFile{UID: m.Owner, Mode: 0644, Regular: true, Data: []byte(want)}, false},
		{"writable result", hostFile{UID: 0, Mode: 0664, Regular: true, Data: []byte(want)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := func(path string) (hostFile, error) {
				if path != reusableMaintenancePath(m, "worker.env") {
					t.Fatal("foreign completion path", path)
				}
				return tc.file, nil
			}
			if err := requireReusableWorkerCompletion(m, requestID, worker, read); (err == nil) != tc.ok {
				t.Fatalf("unexpected completion result: %v", err)
			}
		})
	}
	dispatch := strings.TrimSuffix(want, "FASED_WORKER_COMPLETED=yes")
	read := func(string) (hostFile, error) {
		return hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte(dispatch)}, nil
	}
	if err := requireReusableWorkerDispatch(m, requestID, worker, read); err != nil {
		t.Fatal("the selected request was refused", err)
	}
	if err := requireReusableWorkerDispatch(m, strings.Repeat("d", 32), worker, read); err == nil {
		t.Fatal("a different request selected the child worker")
	}
	if err := requireReusableWorkerCompletion(m, requestID, worker, read); err == nil {
		t.Fatal("an unfinished dispatch was accepted as completed")
	}
}

func TestReusableWorkerCompletionReadsStableOwnedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.env")
	data := []byte("completed output")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, path+".owner"); err != nil {
		t.Fatal(err)
	}
	f, err := readReusableWorkerCompletionAt(path, os.Getuid(), os.Getgid())
	if err != nil || !bytes.Equal(f.Data, data) {
		t.Fatal("stable protected output was refused", err)
	}
	opened, err := openStageRecoveryFile(path, os.Getuid(), os.Getgid(), []uint32{0644}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("changed output"), 0644); err != nil {
		t.Fatal(err)
	}
	if opened.stable() {
		t.Fatal("a concurrent rewrite was accepted as stable")
	}
	opened.file.Close()
	if err = os.Remove(path + ".owner"); err != nil {
		t.Fatal(err)
	}
	if _, err = readReusableWorkerCompletionAt(path, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("output without its protected identity marker was accepted")
	}
}

func TestReusableCompatibilityOutputIsBoundedAndAnchored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compat-runner.out")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 65536), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, path+".owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := readReusableProtectedOutputAt(path, os.Getuid(), os.Getgid(), 65536); err != nil {
		t.Fatal("bounded protected output refused", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = readReusableProtectedOutputAt(path, os.Getuid(), os.Getgid(), 65536); err == nil {
		t.Fatal("oversized protected output accepted")
	}
}

func TestReusableLauncherRecordsCompletionAfterWorker(t *testing.T) {
	m := fixtureManifest()
	a := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	body, _, err := reusableAtomicLauncher(m, a)
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(body), "ExecStart=+/usr/bin/systemctl start -- ${FASED_WORKER_UNIT}")
	completed := strings.Index(string(body), "ExecStart=+/usr/bin/printf FASED_WORKER_COMPLETED=yes")
	if start < 0 || completed <= start {
		t.Fatal("launcher can acknowledge a request before its worker completes")
	}
}

func TestReusablePolicySerializesPreparationWorkers(t *testing.T) {
	m := fixtureManifest()
	policy, err := reusableAtomicPolicy(m, "fased_owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(policy), "\n") {
		if strings.Contains(line, "NOPASSWD: /usr/bin/systemctl") && (strings.Contains(line, "-atomic-compat") || strings.Contains(line, "-stage-")) {
			t.Fatalf("preparation can bypass the serialized launcher: %s", line)
		}
	}
}

func TestReusableProvisionDispatchBindsIntentAndRefusesFailures(t *testing.T) {
	owner := 1000
	r := reusableAtomicRequest{Version: 1, Owner: owner, InstallationID: strings.Repeat("a", 32), IntentID: strings.Repeat("b", 32), Direction: "prepare"}
	var provisionCalls, workerCalls int
	verifiedID := r.IntentID
	var verifyErr error
	provision := func(gotOwner int) (string, error) {
		provisionCalls++
		if gotOwner != owner {
			t.Fatal("foreign preparation owner")
		}
		return verifiedID, verifyErr
	}
	worker := func(_ context.Context, gotOwner int, id, direction string) error {
		workerCalls++
		if gotOwner != owner || id != r.IntentID || direction != "forward" {
			t.Fatal("foreign worker request")
		}
		return verifyErr
	}
	stage := func(gotOwner int, kind, slot string) error {
		if gotOwner != owner || kind != "meta" || slot != "transaction" {
			t.Fatal("foreign staging request")
		}
		return verifyErr
	}
	compatibility := func(gotOwner int) (string, error) {
		if gotOwner != owner {
			t.Fatal("foreign compatibility request")
		}
		return verifiedID, verifyErr
	}
	name, err := selectReusableAtomicRequest(t.Context(), owner, r, provision, worker, stage, compatibility)
	if err != nil || name != reusableAtomicProvisionName(owner) || provisionCalls != 1 || workerCalls != 0 {
		t.Fatal("exact verified provision was not selected", name, err)
	}
	verifiedID = strings.Repeat("c", 32)
	if name, err = selectReusableAtomicRequest(t.Context(), owner, r, provision, worker, stage, compatibility); err == nil || name != "" || workerCalls != 0 {
		t.Fatal("foreign verified intent was accepted", name, err)
	}
	verifiedID, verifyErr = r.IntentID, errors.New("signer unavailable")
	if name, err = selectReusableAtomicRequest(t.Context(), owner, r, provision, worker, stage, compatibility); err == nil || name != "" || workerCalls != 0 {
		t.Fatal("failed signer verification selected a service", name, err)
	}
	verifyErr = nil
	r.Direction = "forward"
	name, err = selectReusableAtomicRequest(t.Context(), owner, r, provision, worker, stage, compatibility)
	if err != nil || name != fmt.Sprintf("fased-custody-%d-atomic-%s-forward.service", owner, r.IntentID) || workerCalls != 1 {
		t.Fatal("exact verified worker was not selected", name, err)
	}
	r.Direction = "foreign"
	if name, err = selectReusableAtomicRequest(t.Context(), owner, r, provision, worker, stage, compatibility); err == nil || name != "" || workerCalls != 1 {
		t.Fatal("foreign operation selected a service", name, err)
	}
	r.Direction, r.StageKind, r.StageSlot = "stage", "meta", "transaction"
	name, err = selectReusableAtomicRequest(t.Context(), owner, r, provision, worker, stage, compatibility)
	if err != nil || name != fmt.Sprintf("fased-custody-%d-stage-meta@transaction.service", owner) {
		t.Fatal("verified stage was not selected", name, err)
	}
	r.Direction, r.StageKind, r.StageSlot, r.Probe = "compatibility", "", "", "runner"
	name, err = selectReusableAtomicRequest(t.Context(), owner, r, provision, worker, stage, compatibility)
	compatName, _ := reusableCompatibilityName(owner, "runner")
	if err != nil || name != compatName {
		t.Fatal("verified compatibility was not selected", name, err)
	}
	r.Probe = "foreign"
	if name, err = selectReusableAtomicRequest(t.Context(), owner, r, provision, worker, stage, compatibility); err == nil || name != "" {
		t.Fatal("foreign compatibility probe was selected", name, err)
	}
	r.Probe = "runner"
	verifiedID = strings.Repeat("d", 32)
	if name, err = selectReusableAtomicRequest(t.Context(), owner, r, provision, worker, stage, compatibility); err == nil || name != "" {
		t.Fatal("foreign compatibility intent was accepted", name, err)
	}
}

func TestReusableStageIsBoundedAndPublishesIndependentInode(t *testing.T) {
	m := fixtureManifest()
	tool, _ := exec.LookPath("systemd-analyze")
	for _, kind := range reusableStageKinds {
		base, suffix, err := reusableStageClass(kind)
		if err != nil {
			t.Fatal(err)
		}
		body, err := reusableStageTemplate(m, 1000, kind)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		guard := strings.Index(text, "--slot %i --worker --disposable-fixture")
		mkdir := strings.Index(text, "ExecStartPre=+/usr/bin/mkdir -- ")
		prepare := strings.Index(text, "ExecStartPre=+/usr/bin/install -m 0600")
		copy := strings.Index(text, "ExecStart=/usr/bin/cp --no-preserve=mode")
		bounded := strings.Index(text, "ExecStartPost=+/usr/bin/dd if=")
		copyGuard := strings.Index(text, "--copy --disposable-fixture")
		compare := strings.Index(text, "ExecStartPost=+/usr/bin/cmp --silent -- ")
		chmod := strings.Index(text, "ExecStartPost=+/usr/bin/chmod ")
		link := strings.Index(text, "ExecStartPost=+/usr/bin/ln --no-target-directory")
		if guard < 0 || !(guard < mkdir && mkdir < prepare && prepare < copy && copy < bounded && bounded < copyGuard && copyGuard < link && bounded < compare && compare < chmod && chmod < link) || strings.Contains(text, "ExecStartPre=+/usr/bin/rm -f -- ") || !strings.Contains(text, ".stage-%i"+suffix+"-${INVOCATION_ID}.d") || !strings.Contains(text, "StandardError=journal\n") || !strings.Contains(text, "LimitFSIZE=") || !strings.Contains(text, "-links 1") || !strings.Contains(text, "iflag=fullblock conv=excl") || !strings.Contains(text, "ExecStartPre=+/usr/bin/grep -Fxq -- %i ") || !strings.Contains(text, "AssertPathIsSymbolicLink=!") || strings.Contains(text, "ConditionPath") {
			t.Fatal("stage is not bounded, isolated and no-clobber", kind)
		}
		commands := strings.Split(strings.TrimSpace(text), "\n")
		if !strings.HasPrefix(commands[len(commands)-1], "ExecStartPost=+/usr/bin/rmdir -- ") {
			t.Fatal("the completed invocation directory is not cleaned last", kind)
		}
		allow, err := reusableStageAllowlist(kind)
		if err != nil || string(allow) != strings.Join(reusableStageSlots[base], "\n")+"\n" {
			t.Fatal("incorrect protected slot allowlist", kind, err)
		}
		if _, err := reusableStageName(m.Owner, kind, reusableStageSlots[base][0]); err != nil {
			t.Fatal(err)
		}
		if _, err := reusableStageName(m.Owner, kind, "../../foreign"); err == nil {
			t.Fatal("foreign stage name accepted", kind)
		}
		if tool != "" {
			path := filepath.Join(t.TempDir(), "fased-custody-1000-stage-"+kind+"@.service")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command(tool, "verify", path).CombinedOutput(); err != nil {
				t.Fatalf("invalid stage unit %s: %v: %s", kind, err, output)
			}
		}
	}
	if _, err := reusableStageTemplate(m, 1000, "foreign"); err == nil {
		t.Fatal("foreign stage class accepted")
	}
	dir := t.TempDir()
	pending, copied := filepath.Join(dir, "pending"), filepath.Join(dir, "copy")
	if err := os.WriteFile(pending, []byte("expected"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("/usr/bin/dd", "if="+pending, "of="+copied, "bs=65536", "count=1", "conv=excl", "status=none").Run(); err == nil {
		t.Fatal("exclusive staging copy accepted an existing destination")
	}
	if data, err := os.ReadFile(copied); err != nil || string(data) != "foreign" {
		t.Fatal("foreign staging copy was changed", err)
	}
}

func TestReusableAtomicRequestRejectsForeignFilesAndPaths(t *testing.T) {
	owner, group := os.Getuid(), os.Getgid()
	if owner <= 0 || group <= 0 {
		t.Skip("ordinary unprivileged owner required")
	}
	inbox := filepath.Join(t.TempDir(), "inbox")
	if err := os.Mkdir(inbox, 0710); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inbox, os.ModeSetgid|0710); err != nil {
		t.Fatal(err)
	}
	want := reusableAtomicRequest{Version: 1, Owner: owner, InstallationID: strings.Repeat("a", 32), IntentID: strings.Repeat("b", 32), Direction: "forward"}
	path := filepath.Join(inbox, "request.json")
	write := func(b []byte) {
		t.Helper()
		if err := os.WriteFile(path, b, 0640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0640); err != nil {
			t.Fatal(err)
		}
	}
	trust := func(string) error { return nil } // The temporary parent is not root-owned.
	read := func() (reusableAtomicRequest, error) { return readReusableAtomicRequestAt(inbox, owner, group, trust) }
	write(mustJSON(want))
	if got, err := read(); err != nil || got != want {
		t.Fatal("valid private request refused", got, err)
	}
	write(append(mustJSON(want), '\n'))
	if _, err := read(); err == nil {
		t.Fatal("noncanonical request accepted")
	}
	write(mustJSON(want))
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatal(err)
	}
	if _, err := read(); err == nil {
		t.Fatal("group-writable request accepted")
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(inbox, "other")
	if err := os.Link(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := read(); err == nil {
		t.Fatal("multiply linked request accepted")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(link, path); err != nil {
		t.Fatal(err)
	}
	if _, err := read(); err == nil {
		t.Fatal("symlink request accepted")
	}
	if _, err := readReusableAtomicRequestAt(inbox, owner+1, group, trust); err == nil {
		t.Fatal("foreign inbox owner accepted")
	}
}

func TestReusableAtomicRequestRequiresExactActionAndIdentity(t *testing.T) {
	r := reusableAtomicRequest{Version: 1, Owner: os.Getuid(), InstallationID: strings.Repeat("a", 32), IntentID: strings.Repeat("b", 32), Direction: "forward"}
	if r.Owner <= 0 {
		t.Skip("ordinary owner required")
	}
	if _, err := validateReusableAtomicRequest(mustJSON(r), r.Owner); err != nil {
		t.Fatal(err)
	}
	prepare := r
	prepare.Direction = "prepare"
	if _, err := validateReusableAtomicRequest(mustJSON(prepare), r.Owner); err != nil {
		t.Fatal("preparation request refused", err)
	}
	for _, mutate := range []func(*reusableAtomicRequest){
		func(v *reusableAtomicRequest) { v.Owner++ },
		func(v *reusableAtomicRequest) { v.IntentID = "../foreign" },
		func(v *reusableAtomicRequest) { v.InstallationID = "../foreign" },
		func(v *reusableAtomicRequest) { v.Direction = "arbitrary.service" },
		func(v *reusableAtomicRequest) { v.RequestID = "../foreign" },
	} {
		bad := r
		mutate(&bad)
		if _, err := validateReusableAtomicRequest(mustJSON(bad), r.Owner); err == nil {
			t.Fatal("foreign request accepted")
		}
	}
}

func TestReusableAtomicRequestWriterPreservesForeignInput(t *testing.T) {
	owner, group := os.Getuid(), os.Getgid()
	if owner <= 0 || group <= 0 {
		t.Skip("ordinary unprivileged owner required")
	}
	inbox := filepath.Join(t.TempDir(), "inbox")
	if err := os.Mkdir(inbox, 0710); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inbox, os.ModeSetgid|0710); err != nil {
		t.Fatal(err)
	}
	trust := func(string) error { return nil } // Temporary parent is owner-controlled.
	r := reusableAtomicRequest{Version: 1, Owner: owner, InstallationID: strings.Repeat("a", 32), IntentID: strings.Repeat("b", 32), Direction: "forward"}
	if err := writeReusableAtomicRequestAt(inbox, r, group, trust); err != nil {
		t.Fatal("initial request failed", err)
	}
	r.Direction = "rollback"
	if err := writeReusableAtomicRequestAt(inbox, r, group, trust); err != nil {
		t.Fatal("same-installation retry failed", err)
	}
	if got, err := readReusableAtomicRequestAt(inbox, owner, group, trust); err != nil || got != r {
		t.Fatal("published request differs", got, err)
	}
	other := r
	other.IntentID = strings.Repeat("c", 32)
	if err := writeReusableAtomicRequestAt(inbox, other, group, trust); err == nil {
		t.Fatal("an interrupted different intent was replaced")
	}
	if got, err := readReusableAtomicRequestAt(inbox, owner, group, trust); err != nil || got != r {
		t.Fatal("different intent changed the original request", got, err)
	}
	path := filepath.Join(inbox, "request.json")
	foreign := []byte("owner data to preserve")
	if err := os.WriteFile(path, foreign, 0640); err != nil {
		t.Fatal(err)
	}
	if err := writeReusableAtomicRequestAt(inbox, r, group, trust); err == nil {
		t.Fatal("foreign request was overwritten")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, foreign) {
		t.Fatal("foreign input changed", err)
	}
}

func TestReusableOwnerStagePreservesForeignInput(t *testing.T) {
	owner, group := os.Getuid(), os.Getgid()
	if owner <= 0 || group <= 0 {
		t.Skip("ordinary unprivileged owner required")
	}
	inboxPath := filepath.Join(t.TempDir(), "inbox")
	if err := os.Mkdir(inboxPath, 0710); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inboxPath, os.ModeSetgid|0710); err != nil {
		t.Fatal(err)
	}
	inbox, err := openReusableInbox(inboxPath, owner, group, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	data := []byte("reviewed disposable input")
	stage := func(b []byte, digest string) (unix.Stat_t, error) {
		return prepareOwnerStage(inbox, "transaction", owner, group, bytes.NewReader(b), int64(len(b)), digest)
	}
	st, err := stage(data, sum(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stage(data, sum(data)); err != nil {
		t.Fatal("same input did not resume", err)
	}
	if _, err = stage([]byte("foreign"), sum([]byte("foreign"))); err == nil {
		t.Fatal("foreign staged input was overwritten")
	}
	if got, err := os.ReadFile(filepath.Join(inboxPath, "transaction")); err != nil || !bytes.Equal(got, data) {
		t.Fatal("original staged bytes changed", err)
	}
	if err = clearOwnerStage(inbox, "transaction", st); err != nil {
		t.Fatal("verified duplicate was not removed", err)
	}
	if _, err = stage(data, strings.Repeat("f", 64)); err == nil {
		t.Fatal("incorrect component digest accepted")
	}
	if _, err = os.Lstat(filepath.Join(inboxPath, "transaction")); !os.IsNotExist(err) {
		t.Fatal("failed stage was published", err)
	}
}

func TestProtectedUpdateFileRequiresSameInode(t *testing.T) {
	if os.Getuid() == 0 || os.Getgid() == 0 {
		t.Fatal("the inode regression must run as an ordinary user")
	}
	dir := t.TempDir()
	original, stage := filepath.Join(dir, "original"), filepath.Join(dir, "stage")
	data := []byte("unchanged predecessor bytes")
	if err := os.WriteFile(original, data, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(original, 0755); err != nil {
		t.Fatal(err)
	}
	check := func() error {
		return sameProtectedUpdateFileAt(original, stage, 0755, os.Getuid(), os.Getgid(), func(string) error { return nil })
	}
	if err := check(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing stage was not reported: %v", err)
	}
	if err := os.Link(original, stage); err != nil {
		t.Fatal(err)
	}
	if err := check(); err != nil {
		t.Fatalf("original hardlink refused: %v", err)
	}
	if err := os.Remove(stage); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, data, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stage, 0755); err != nil {
		t.Fatal(err)
	}
	if err := check(); err == nil {
		t.Fatal("equal-byte different inode was accepted")
	}
	if err := os.Remove(stage); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(original, stage); err != nil {
		t.Fatal(err)
	}
	if err := check(); err == nil {
		t.Fatal("symlink to original was accepted")
	}
	if got, err := os.ReadFile(original); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("refusal changed the original: %v", err)
	}
}

func TestReusableProtectedCandidatePreservesPublishedPredecessor(t *testing.T) {
	tx, _ := modelTransaction(t)
	stage := reusableMaintenancePath(tx.Base, "staging") + "/next-core"
	original := tx.dir() + "/next-core"
	foreign := tx
	foreign.Binding.TargetCore = strings.Repeat("f", 64)
	for _, tc := range []struct {
		name                                    string
		stageErr, originalErr, sameErr, loadErr error
		published                               InstallTransaction
		want                                    string
		wantMissing                             bool
		wantErr                                 bool
	}{
		{"existing protected stage", nil, nil, nil, nil, tx, stage, false, false},
		{"retained predecessor needs protected adoption", os.ErrNotExist, nil, nil, nil, tx, "", false, true},
		{"fresh transaction", os.ErrNotExist, nil, nil, os.ErrNotExist, tx, "", true, true},
		{"fresh staged image", nil, nil, nil, os.ErrNotExist, tx, stage, false, false},
		{"foreign staged file", errors.New("changed stage"), nil, nil, nil, tx, "", false, true},
		{"foreign predecessor", os.ErrNotExist, nil, nil, nil, foreign, "", false, true},
		{"missing original", os.ErrNotExist, os.ErrNotExist, nil, nil, tx, "", false, true},
		{"staged copy cannot replace missing original", nil, os.ErrNotExist, nil, nil, tx, "", false, true},
		{"equal bytes in different inode", nil, nil, errors.New("different inode"), nil, tx, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _, err := reusableProtectedCandidate(tx, "next-core", tx.Target.CoreHash, func(path, digest string) (int64, error) {
				if digest != tx.Target.CoreHash {
					t.Fatal("candidate digest changed")
				}
				switch path {
				case stage:
					return 9, tc.stageErr
				case original:
					return 9, tc.originalErr
				default:
					t.Fatalf("unexpected protected path %s", path)
					return 0, nil
				}
			}, func(gotOriginal, gotStage string) error {
				if gotOriginal != original || gotStage != stage {
					t.Fatal("unexpected inode comparison")
				}
				return tc.sameErr
			}, func() (InstallTransaction, error) { return tc.published, tc.loadErr })
			if (err != nil) != tc.wantErr || errors.Is(err, os.ErrNotExist) != tc.wantMissing || (!tc.wantErr && path != tc.want) {
				t.Fatalf("path=%s err=%v", path, err)
			}
		})
	}
}

func TestPublishedPredecessorRequiresOriginalStagedInodes(t *testing.T) {
	tx, _ := modelTransaction(t)
	stage := reusableMaintenancePath(tx.Base, "staging")
	for _, missing := range []string{"", "transaction", "runner", "previous-signer", "next-core", "next-signer"} {
		t.Run("missing="+missing, func(t *testing.T) {
			calls := 0
			err := requirePublishedPredecessorStage(tx, func() (InstallTransaction, error) { return tx, nil }, func(string) (hostFile, error) { return hostFile{}, os.ErrNotExist }, func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }, func(original, staged string, mode os.FileMode) error {
				calls++
				if !strings.HasPrefix(original, tx.dir()+"/") || !strings.HasPrefix(staged, stage+"/") || (mode != 0644 && mode != 0755) {
					t.Fatal("unexpected predecessor file", original, staged, mode)
				}
				if filepath.Base(staged) == missing {
					return os.ErrNotExist
				}
				return nil
			})
			if (err != nil) != (missing != "") || (missing == "" && calls != 5) || errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing=%s calls=%d err=%v", missing, calls, err)
			}
		})
	}
	for _, changed := range []string{"", "bytes", "inode", "unreadable"} {
		t.Run("published-active-"+changed, func(t *testing.T) {
			read := func(string) (hostFile, error) {
				if changed == "unreadable" {
					return hostFile{}, errors.New("unreadable")
				}
				data := mustJSON(tx)
				if changed == "bytes" {
					data = []byte("foreign")
				}
				return hostFile{UID: 0, Mode: 0644, Regular: true, Data: data}, nil
			}
			same := func(original, staged string, mode os.FileMode) error {
				if changed == "inode" && original == reusableMaintenancePath(tx.Base, "active.json") {
					return errors.New("different inode")
				}
				return nil
			}
			err := requirePublishedPredecessorStage(tx, func() (InstallTransaction, error) { return tx, nil }, read, func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }, same)
			if (err != nil) != (changed != "") {
				t.Fatalf("changed=%s err=%v", changed, err)
			}
		})
	}
	if err := requirePublishedPredecessorStage(tx, func() (InstallTransaction, error) { return InstallTransaction{}, os.ErrNotExist }, func(string) (hostFile, error) { return hostFile{}, os.ErrNotExist }, func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }, func(string, string, os.FileMode) error {
		t.Fatal("fresh installation inspected predecessor links")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{"", "active-bytes", "active-inode", "active-unreadable"} {
		t.Run("no-images-"+changed, func(t *testing.T) {
			read := func(string) (hostFile, error) {
				if changed == "active-unreadable" {
					return hostFile{}, errors.New("unreadable")
				}
				data := mustJSON(tx)
				if changed == "active-bytes" {
					data = []byte("foreign")
				}
				return hostFile{UID: 0, Mode: 0644, Regular: true, Data: data}, nil
			}
			same := func(string, string, os.FileMode) error {
				if changed == "active-inode" {
					return errors.New("different inode")
				}
				return nil
			}
			err := requirePublishedPredecessorStage(tx, func() (InstallTransaction, error) { return InstallTransaction{}, os.ErrNotExist }, read, func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }, same)
			if (err != nil) != (changed != "") {
				t.Fatalf("changed=%s err=%v", changed, err)
			}
		})
	}
	if err := requirePublishedPredecessorStage(tx, func() (InstallTransaction, error) { return InstallTransaction{}, os.ErrNotExist }, func(string) (hostFile, error) { return hostFile{}, os.ErrNotExist }, func(path string) (os.FileInfo, error) {
		if filepath.Base(path) == "previous-signer" {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}, func(string, string, os.FileMode) error { return nil }); err == nil {
		t.Fatal("partial unpublished predecessor was accepted")
	}
	if err := requirePublishedPredecessorStage(tx, func() (InstallTransaction, error) { return InstallTransaction{}, os.ErrNotExist }, func(string) (hostFile, error) { return hostFile{}, os.ErrNotExist }, func(path string) (os.FileInfo, error) {
		if filepath.Base(path) == ".runner-"+tx.Base.ID+".pending" {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}, func(string, string, os.FileMode) error { return nil }); err == nil {
		t.Fatal("retained predecessor pending file was accepted")
	}
	images := []string{"runner", "previous-signer", "next-core", "next-signer"}
	for count := 1; count <= len(images); count++ {
		for _, changed := range []string{"", "active", "image"} {
			t.Run(fmt.Sprintf("provision-prefix-%d-%s", count, changed), func(t *testing.T) {
				read := func(path string) (hostFile, error) {
					if path != reusableMaintenancePath(tx.Base, "active.json") || changed == "active" {
						return hostFile{}, os.ErrNotExist
					}
					return hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(tx)}, nil
				}
				stat := func(path string) (os.FileInfo, error) {
					for _, name := range images[:count] {
						if path == tx.dir()+"/"+name {
							return nil, nil
						}
					}
					return nil, os.ErrNotExist
				}
				same := func(original, staged string, mode os.FileMode) error {
					if original == reusableMaintenancePath(tx.Base, "active.json") && staged == stage+"/transaction" && mode == 0644 {
						return nil
					}
					if mode != 0755 || !strings.HasPrefix(original, tx.dir()+"/") || staged != stage+"/"+filepath.Base(original) || changed == "image" {
						return errors.New("different inode")
					}
					return nil
				}
				err := requirePublishedPredecessorStage(tx, func() (InstallTransaction, error) { return InstallTransaction{}, os.ErrNotExist }, read, stat, same)
				if (err != nil) != (changed != "") {
					t.Fatalf("prefix=%d changed=%s err=%v", count, changed, err)
				}
			})
		}
	}
	foreign := tx
	foreign.Binding.TargetCore = strings.Repeat("f", 64)
	if err := requirePublishedPredecessorStage(tx, func() (InstallTransaction, error) { return foreign, nil }, func(string) (hostFile, error) { return hostFile{}, os.ErrNotExist }, func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }, func(string, string, os.FileMode) error { return nil }); err == nil {
		t.Fatal("foreign published predecessor was accepted")
	}
}

func TestReusableControllerInputsBindPairAndWorkers(t *testing.T) {
	tx, installed := modelTransaction(t)
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	pin := &reusableMaintenanceAuthority{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	artifacts := t.TempDir()
	for name, data := range map[string]string{"fased-app": "next-core", "fased-signer": "next-signer"} {
		if err := os.WriteFile(filepath.Join(artifacts, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	inputs, err := reusableControllerInputsAt(tx, c, pin, artifacts, installed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != len(reusableStageSlots["image"])+len(reusableStageSlots["meta"])+len(reusableStageSlots["unit"]) {
		t.Fatal("not every bounded input was produced")
	}
	seen := map[string]bool{}
	for _, input := range inputs {
		if seen[input.slot] {
			t.Fatal("duplicate staging slot", input.slot)
		}
		seen[input.slot] = true
		if _, err := reusableStageName(tx.Base.Owner, input.kind, input.slot); err != nil {
			t.Fatal("unapproved staging slot", input.slot, err)
		}
	}
	if err := os.WriteFile(filepath.Join(artifacts, "fased-signer"), []byte("wrong-signer"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reusableControllerInputsAt(tx, c, pin, artifacts, installed, nil); err == nil {
		t.Fatal("changed signed component accepted")
	}
	staged := t.TempDir()
	for name, data := range map[string]string{"next-core": "next-core", "next-signer": "next-signer"} {
		if err := os.WriteFile(filepath.Join(staged, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	lookup := func(slot, digest string) (string, int64, error) {
		path := filepath.Join(staged, slot)
		f, size, err := stageSource(path, digest)
		if err != nil {
			return path, 0, err
		}
		defer f.Close()
		return path, size, verifyExecutableContents(f, size, digest)
	}
	for _, name := range []string{"fased-app", "fased-signer"} {
		if err := os.Remove(filepath.Join(artifacts, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reusableControllerInputsAt(tx, c, pin, artifacts, installed, lookup); err != nil {
		t.Fatal("protected staged images should not require missing owner copies", err)
	}
	if err := os.WriteFile(filepath.Join(staged, "next-core"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reusableControllerInputsAt(tx, c, pin, artifacts, installed, lookup); err == nil {
		t.Fatal("changed protected staging was accepted")
	}
}
