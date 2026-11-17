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
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestAtomicPairPlanBindsGuardAndSerializesDirections(t *testing.T) {
	tx, _ := modelTransaction(t)
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	guard := strings.Repeat("d", 64)
	plan, err := makeAtomicPairPlan(tx, c, guard, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	forward := string(plan.units["/etc/systemd/system/"+plan.forward])
	rollback := string(plan.units["/etc/systemd/system/"+plan.rollback])
	completion := string(plan.units["/etc/systemd/system/"+plan.completion])
	cleanupRollback := string(plan.units["/etc/systemd/system/"+plan.cleanupRollback])
	cleanupComplete := string(plan.units["/etc/systemd/system/"+plan.cleanupComplete])
	reusable, err := makeReusableAtomicPairPlan(tx, c, guard, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	reusableForward := string(reusable.units["/etc/systemd/system/"+reusable.forward])
	recovery := strings.Index(reusableForward, "--verify-recovery --recovery-digest "+update.InstallDigest(tx.Binding))
	if recovery < strings.Index(reusableForward, "--guard-stage before") || recovery > strings.Index(reusableForward, "ExecStart=/usr/bin/rename.ul ") {
		t.Fatal("forward replacement lacks the exact app-owned recovery check")
	}
	if strings.Contains(string(reusable.units["/etc/systemd/system/"+reusable.rollback]), "--verify-recovery") {
		t.Fatal("a damaged backup must not prevent restoring the predecessor pair")
	}
	proof := reusableMaintenancePath(tx.Base, "predecessor-proof-v1.json")
	for _, name := range []string{reusable.forward, reusable.rollback} {
		body := string(reusable.units["/etc/systemd/system/"+name])
		checked := strings.Index(body, "--guard-stage predecessor")
		linked := strings.Index(body, "ExecStart=-/usr/bin/ln --no-target-directory -- "+tx.dir()+"/atomic-held.json "+proof)
		replaced := strings.Index(body, "ExecStart=/usr/bin/rename.ul ")
		if checked < 0 || linked <= checked || replaced <= linked {
			t.Fatal("replacement can precede protected predecessor proof", name)
		}
		if !strings.Contains(body, "ExecStart=/usr/bin/test "+tx.dir()+"/atomic-held.json -ef "+proof) {
			t.Fatal("proof publication does not require the protected inode", name)
		}
	}
	for _, piece := range []string{"Conflicts=" + plan.rollback, "After=" + plan.rollback, "KillMode=control-group", "--guard-direction forward", "--guard-stage before", "--guard-stage after", "ExecStart=/usr/bin/test ! -e " + tx.dir() + "/rollback.json"} {
		if !strings.Contains(forward, piece) {
			t.Fatal("forward unit missing", piece)
		}
	}
	for _, piece := range []string{"Conflicts=" + plan.forward, "--guard-direction rollback", "--guard-stage before", "--guard-stage after", tx.dir() + "/atomic-rollback.json " + tx.dir() + "/rollback.json"} {
		if !strings.Contains(rollback, piece) {
			t.Fatal("rollback unit missing", piece)
		}
	}
	for _, piece := range []string{"Conflicts=" + plan.forward + " " + plan.rollback, "--guard-direction completion", tx.dir() + "/target-intent.json", tx.dir() + "/target-ready.json"} {
		if !strings.Contains(completion, piece) {
			t.Fatal("completion unit missing", piece)
		}
	}
	if !strings.Contains(cleanupRollback, "fixture-update-pause-guard") || !strings.Contains(cleanupRollback, "--phase paused") || !strings.Contains(cleanupComplete, "fixture-update-pause-guard") || !strings.Contains(cleanupComplete, "--phase released") {
		t.Fatal("cleanup lacks independently verified core state")
	}
	for _, body := range []string{forward, rollback, completion, cleanupRollback, cleanupComplete} {
		markerLink := strings.Index(body, "update-start-blocked.json\n")
		markerSync := strings.Index(body, "ExecStart=/usr/bin/sync -f "+tx.Base.Root+"\n")
		serviceStop := strings.Index(body, "ExecStart=/usr/bin/systemctl stop --")
		if !(markerLink >= 0 && markerLink < markerSync && markerSync < serviceStop) {
			t.Fatal("restart marker is not durable before service stop")
		}
		guardBefore := strings.Index(body, "--guard-stage before")
		guardFileCheck := strings.Index(body, "ExecStart=/usr/bin/find "+tx.dir()+"/atomic-guard")
		checksumCheck := strings.Index(body, "ExecStart=/usr/bin/sha256sum --check")
		firstReplacement := strings.Index(body, "ExecStart=/usr/bin/rename.ul ")
		if firstReplacement < 0 {
			firstReplacement = strings.Index(body, "ExecStart=/usr/bin/rm -f -- "+tx.Base.Root+"/update-active.json")
		}
		guardAfter := strings.Index(body, "--guard-stage after")
		removeMarker := strings.Index(body, "ExecStart=/usr/bin/rm -- "+tx.Base.Root+"/update-start-blocked.json")
		start := strings.Index(body, "ExecStart=/usr/bin/systemctl start --")
		if !(guardFileCheck >= 0 && guardFileCheck < checksumCheck && checksumCheck < guardBefore && guardBefore < firstReplacement && firstReplacement < guardAfter && guardAfter < removeMarker && removeMarker < start) {
			t.Fatal("unsafe action order")
		}
		if strings.Contains(body, "ExecStart=/bin/sh ") || strings.Contains(body, "ExecStart=/bin/bash ") || strings.Contains(body, "ExecStart=/usr/bin/bash ") {
			t.Fatal("root shell introduced")
		}
	}
	checksums := string(plan.inputs[tx.dir()+"/atomic-checksums.sha256"])
	if !strings.Contains(checksums, guard+"  "+tx.dir()+"/atomic-guard\n") {
		t.Fatal("guard digest not bound")
	}
	for _, bad := range []string{"", tx.Base.SignerHash, tx.Target.SignerHash, strings.Repeat("/", 64)} {
		if _, err := makeAtomicPairPlan(tx, c, bad, strings.Repeat("e", 64)); err == nil {
			t.Fatal("invalid independent signer guard accepted")
		}
	}
	c.SignerUID++
	if _, err := makeAtomicPairPlan(tx, c, guard, strings.Repeat("e", 64)); err == nil {
		t.Fatal("foreign signer identity accepted")
	}
}

func TestAtomicWorkerChecksSignerHoldBeforeBlockingServices(t *testing.T) {
	tx, _ := modelTransaction(t)
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	plan, err := makeAtomicPairPlan(tx, c, strings.Repeat("d", 64), strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range plan.names() {
		body := string(plan.units["/etc/systemd/system/"+name])
		txnCheck := strings.Index(body, tx.dir()+"/transaction.json")
		txnSync := strings.Index(body, "ExecStart=/usr/bin/sync -f "+tx.dir()+"\n")
		parentSync := strings.Index(body, "ExecStart=/usr/bin/sync -f "+tx.Base.Root+"/updates\n")
		guard := strings.Index(body, " fixture-install-start-guard ")
		marker := strings.Index(body, "ExecStart=-/usr/bin/ln --no-target-directory -- "+tx.dir()+"/atomic-marker-")
		stop := strings.Index(body, "ExecStart=/usr/bin/systemctl stop --")
		if txnCheck < 0 || txnSync <= txnCheck || parentSync <= txnSync || guard <= parentSync || marker <= guard || stop <= marker {
			t.Fatal("worker can block services before its exact transaction is durable and the signer hold is checked", name)
		}
	}
}

func TestAtomicPairRejectsUntrustedPauseIdentifierBeforeUnitGeneration(t *testing.T) {
	for _, field := range []string{"pause", "intent"} {
		t.Run(field, func(t *testing.T) {
			tx, _ := modelTransaction(t)
			c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
			// A self-consistent recovery checksum does not make owner-supplied
			// data safe to interpolate into a root-managed systemd unit.
			bad := "bad\nExecStartPre=/usr/bin/true\n#"
			if field == "pause" {
				tx.Binding.Index.Binding.Pause.PauseID = bad
			} else {
				tx.Binding.Index.Binding.Pause.Request.IntentID = bad
			}
			tx.Binding.Index.BindingSHA256 = update.RecoveryDigest(tx.Binding.Index.Binding)
			if !tx.valid() {
				t.Fatal("fixture must reach the real unit-generation boundary")
			}
			if _, err := makeReusableAtomicPairPlan(tx, c, strings.Repeat("d", 64), strings.Repeat("e", 64)); err == nil {
				t.Fatal("untrusted identifier reached the privileged unit")
			}
		})
	}
}

func TestAtomicPairRejectsUnreviewedExecutionSections(t *testing.T) {
	for _, property := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload", "ExecReloadPost"} {
		body := []byte("[Service]\nExecStart=/usr/bin/true\n" + property + "=/usr/bin/true\n")
		if _, err := parseAtomicPairCommands(body); err == nil {
			t.Fatalf("unreviewed %s reached an atomic worker", property)
		}
	}
}

func TestAtomicServicePropertySnapshotRejectsHiddenCommands(t *testing.T) {
	properties := map[string]any{}
	empty := func() any { return map[string]any{"type": "a(sasbttttuii)", "data": []any{}} }
	properties["ExecStart"] = empty()
	required := []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload"}
	for _, name := range required {
		properties[name] = empty()
	}
	encode := func() []byte {
		b, _ := json.Marshal(map[string]any{"type": "a{sv}", "data": []any{properties}})
		return b
	}
	if _, err := decodeAtomicServiceProperties(encode()); err != nil {
		t.Fatal("empty execution hooks refused", err)
	}
	for _, name := range append(append([]string{}, required...), "ExecReloadPost") {
		properties[name] = map[string]any{"type": "a(sasbttttuii)", "data": []any{[]any{"/usr/bin/true"}}}
		if _, err := decodeAtomicServiceProperties(encode()); err == nil {
			t.Fatal("nonempty execution hook accepted", name)
		}
		delete(properties, name)
		if name != "ExecReloadPost" {
			if _, err := decodeAtomicServiceProperties(encode()); err == nil {
				t.Fatal("missing execution property accepted", name)
			}
			properties[name] = empty()
		}
	}
	properties["ExecStartPre"] = map[string]any{"type": "a(sasbttttuii)", "data": nil}
	if _, err := decodeAtomicServiceProperties(encode()); err == nil {
		t.Fatal("null was accepted as an empty command array")
	}
}

func TestAtomicPairUnitSyntax(t *testing.T) {
	tool, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze unavailable")
	}
	renameFallback := false
	if _, err := os.Stat("/usr/bin/rename.ul"); errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat("/usr/bin/rename"); err != nil {
			t.Skip("util-linux rename unavailable")
		}
		renameFallback = true
	}
	tx, _ := modelTransaction(t)
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	for name, build := range map[string]func(InstallTransaction, signer.Config, string, string) (atomicPairPlan, error){
		"existing": makeAtomicPairPlan,
		"reusable": makeReusableAtomicPairPlan,
	} {
		plan, err := build(tx, c, strings.Repeat("d", 64), strings.Repeat("e", 64))
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		var paths []string
		for _, unit := range plan.names() {
			p := filepath.Join(dir, unit)
			body := plan.units[filepath.Join("/etc/systemd/system", unit)]
			if renameFallback {
				body = bytes.ReplaceAll(body, []byte("/usr/bin/rename.ul"), []byte("/usr/bin/rename"))
			}
			if err := os.WriteFile(p, body, 0600); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, p)
		}
		cmd := exec.Command(tool, append([]string{"verify"}, paths...)...)
		if b, err := cmd.CombinedOutput(); err != nil {
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			blocked := len(lines) > 0
			for _, line := range lines {
				if line != "Failed to turn off SO_PASSRIGHTS on user lookup socket, ignoring: Operation not permitted" && line != "Failed to enable SO_PASSCRED on handoff timestamp socket: Operation not permitted" {
					blocked = false
				}
			}
			if blocked {
				t.Skip("sandbox blocks systemd-analyze sockets")
			}
			t.Fatalf("invalid %s systemd unit: %v: %s", name, err, b)
		}
	}
}

func TestLoadedAtomicUnitRequiresExactSystemdCommands(t *testing.T) {
	tx, _ := modelTransaction(t)
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	plan, err := makeAtomicPairPlan(tx, c, strings.Repeat("d", 64), strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	others := strings.Join(plan.names()[1:], " ")
	fields := map[string]string{"LoadState": "loaded", "FragmentPath": "/etc/systemd/system/" + plan.forward, "DropInPaths": "", "NeedDaemonReload": "no", "Type": "oneshot", "KillMode": "control-group", "NoNewPrivileges": "yes", "ProtectSystem": "strict", "User": "", "Group": "", "Conflicts": others, "After": others}
	for _, property := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload"} {
		fields[property] = ""
	}
	var rows [][]any
	for _, command := range plan.commands[plan.forward] {
		rows = append(rows, []any{command.argv[0], command.argv, command.ignore, 0, 0, 0, 0, 0, 0, 0})
	}
	encode := func() []byte { b, _ := json.Marshal(map[string]any{"type": "a(sasbttttuii)", "data": rows}); return b }
	if err := validateLoadedAtomicUnit(plan, plan.forward, fields, encode()); err != nil {
		t.Fatal(err)
	}
	rows[0][1] = []string{"/usr/bin/true"}
	if err := validateLoadedAtomicUnit(plan, plan.forward, fields, encode()); err == nil {
		t.Fatal("altered loaded command accepted")
	}
	rows[0][1] = plan.commands[plan.forward][0].argv
	for _, change := range []struct{ key, value string }{{"DropInPaths", "/foreign"}, {"NeedDaemonReload", "yes"}, {"After", ""}, {"Conflicts", ""}, {"User", "1800"}} {
		old := fields[change.key]
		fields[change.key] = change.value
		if err := validateLoadedAtomicUnit(plan, plan.forward, fields, encode()); err == nil {
			t.Fatal("unsafe loaded unit accepted", change.key)
		}
		fields[change.key] = old
	}
	for _, property := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload"} {
		fields[property] = "{ path=/usr/bin/true ; argv[]=/usr/bin/true ; }"
		if err := validateLoadedAtomicUnit(plan, plan.forward, fields, encode()); err == nil {
			t.Fatal("unreviewed loaded execution accepted", property)
		}
		delete(fields, property)
		if err := validateLoadedAtomicUnit(plan, plan.forward, fields, encode()); err == nil {
			t.Fatal("missing loaded execution property accepted", property)
		}
		fields[property] = ""
	}
}

func TestReusableWorkerAdmissionRejectsAlteredOSExecution(t *testing.T) {
	tx, _ := modelTransaction(t)
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	pin := reusableMaintenanceAuthority{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	approval := migrationRunnerApproval{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, BindingDigest: update.InstallDigest(tx.Binding), PreviousCore: tx.Base.CoreHash, TargetCore: tx.Target.CoreHash, TargetSigner: tx.Target.SignerHash, RunnerSHA256: pin.RunnerSHA256, GuardSHA256: pin.GuardSHA256}
	plan, err := makeReusableAtomicPairPlan(tx, c, pin.GuardSHA256, pin.RunnerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	name := plan.forward
	unitPath := "/etc/systemd/system/" + name
	files := map[string]hostFile{
		tx.dir() + "/transaction.json":                       {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(tx)},
		tx.dir() + "/migration-runner.json":                  {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(approval)},
		reusableMaintenancePath(tx.Base, "authority.json"):   {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(pin)},
		reusableMaintenancePath(tx.Base, "reusable-v1.json"): {UID: 0, Mode: 0644, Regular: true, Data: reusableMaintenanceMarkerBytes(pin)},
		unitPath: {UID: 0, Mode: 0644, Regular: true, Data: bytes.Clone(plan.units[unitPath])},
	}
	for path, data := range plan.inputs {
		files[path] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: bytes.Clone(data)}
	}
	read := func(path string) (hostFile, error) {
		f, ok := files[path]
		if !ok {
			return hostFile{}, os.ErrNotExist
		}
		return f, nil
	}
	images := map[string]string{
		reusableMaintenancePath(tx.Base, "atomic-guard"): pin.GuardSHA256,
		reusableMaintenancePath(tx.Base, "runner"):       pin.RunnerSHA256,
		tx.dir() + "/atomic-guard":                       pin.GuardSHA256,
		tx.dir() + "/migration-runner":                   pin.RunnerSHA256,
	}
	check := func(path, hash string) (int64, error) {
		if images[path] != hash {
			return 0, errors.New("executable changed")
		}
		return 1, nil
	}
	others := strings.Join(plan.names()[1:], " ")
	fields := map[string]string{"LoadState": "loaded", "FragmentPath": unitPath, "DropInPaths": "", "NeedDaemonReload": "no", "Type": "oneshot", "KillMode": "control-group", "NoNewPrivileges": "yes", "ProtectSystem": "strict", "User": "", "Group": "", "Conflicts": others, "After": others}
	for _, property := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload"} {
		fields[property] = ""
	}
	var rows [][]any
	for _, command := range plan.commands[name] {
		rows = append(rows, []any{command.argv[0], command.argv, command.ignore, 0, 0, 0, 0, 0, 0, 0})
	}
	inspect := func(_ context.Context, got string) (map[string]string, []byte, error) {
		if got != name {
			return nil, nil, errors.New("foreign worker")
		}
		b, _ := json.Marshal(map[string]any{"type": "a(sasbttttuii)", "data": rows})
		return fields, b, nil
	}
	validate := func() error { return validateReusableAtomicWorker(t.Context(), tx, c, "forward", read, check, inspect) }
	if err := validate(); err != nil {
		t.Fatal("exact worker refused", err)
	}
	old := files[unitPath]
	files[unitPath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("[Service]\nExecStart=/usr/bin/true\n")}
	if err := validate(); err == nil {
		t.Fatal("altered root worker accepted")
	}
	files[unitPath] = old
	rows[0][1] = []string{"/usr/bin/true"}
	if err := validate(); err == nil {
		t.Fatal("changed loaded command accepted")
	}
	rows[0][1] = plan.commands[name][0].argv
	images[tx.dir()+"/atomic-guard"] = strings.Repeat("f", 64)
	if err := validate(); err == nil {
		t.Fatal("substituted transaction guard accepted")
	}
}

func TestReusableProvisionRejectsForeignStagedTransaction(t *testing.T) {
	tx, _ := modelTransaction(t)
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	pin := reusableMaintenanceAuthority{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	approval := migrationRunnerApproval{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, BindingDigest: update.InstallDigest(tx.Binding), PreviousCore: tx.Base.CoreHash, TargetCore: tx.Target.CoreHash, TargetSigner: tx.Target.SignerHash, RunnerSHA256: pin.RunnerSHA256, GuardSHA256: pin.GuardSHA256}
	plan, err := makeReusableAtomicPairPlan(tx, c, pin.GuardSHA256, pin.RunnerSHA256)
	if err != nil {
		t.Fatal(err)
	}
	stage := reusableMaintenancePath(tx.Base, "staging")
	files := map[string]hostFile{
		tx.Base.Root + "/install.json":                       {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(tx.Base)},
		reusableMaintenancePath(tx.Base, "authority.json"):   {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(pin)},
		reusableMaintenancePath(tx.Base, "reusable-v1.json"): {UID: 0, Mode: 0644, Regular: true, Data: reusableMaintenanceMarkerBytes(pin)},
	}
	for _, item := range reusableMetadataFiles {
		data := plan.inputs[tx.dir()+"/"+item.name]
		if item.slot == "transaction" {
			data = mustJSON(tx)
		} else if item.slot == "approval" {
			data = mustJSON(approval)
		}
		files[stage+"/"+item.slot] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: bytes.Clone(data)}
	}
	for _, direction := range reusableWorkerDirections {
		name, nameErr := atomicWorkerName(plan, direction)
		if nameErr != nil {
			t.Fatal(nameErr)
		}
		files[stage+"/unit-"+direction] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: bytes.Clone(plan.units["/etc/systemd/system/"+name])}
	}
	reads := make(map[string]int)
	read := func(path string) (hostFile, error) {
		reads[path]++
		if f, ok := files[path]; ok {
			return f, nil
		}
		return hostFile{}, os.ErrNotExist
	}
	images := map[string]string{
		reusableMaintenancePath(tx.Base, "atomic-guard"): pin.GuardSHA256,
		reusableMaintenancePath(tx.Base, "runner"):       pin.RunnerSHA256,
		tx.Base.Root + "/fased-app":                      tx.Base.CoreHash,
		tx.Base.Root + "/fased-signer":                   tx.Base.SignerHash,
		stage + "/runner":                                tx.Base.CoreHash,
		stage + "/previous-signer":                       tx.Base.SignerHash,
		stage + "/next-core":                             tx.Target.CoreHash,
		stage + "/next-signer":                           tx.Target.SignerHash,
	}
	check := func(path, digest string) (int64, error) {
		if images[path] != digest {
			return 0, errors.New("foreign image")
		}
		return 1, nil
	}
	matchingHold := &update.Installation{Version: 1, Binding: tx.Binding, Digest: update.InstallDigest(tx.Binding), Phase: "active"}
	actualHold := matchingHold
	validate := func() error { return validateReusableAtomicProvision(tx, c, actualHold, read, check) }
	if err := validate(); err == nil {
		t.Fatal("direct provisioning bypassed protected compatibility")
	}
	coreOutput, err := json.Marshal(struct {
		Contract           update.Contract `json:"contract"`
		WalletSigner       bool            `json:"walletSigner"`
		FinancialExecution bool            `json:"financialExecution"`
		UpdaterEnabled     bool            `json:"updaterEnabled"`
	}{core.UpdateContract(), false, false, false})
	if err != nil {
		t.Fatal(err)
	}
	signerOutput, err := json.Marshal(signer.UpdateContract())
	if err != nil {
		t.Fatal(err)
	}
	for i, slot := range reusableCompatibilitySlots {
		output := signerOutput
		if i < 2 {
			output = coreOutput
		}
		path := reusableMaintenancePath(tx.Base, "compat-"+slot+".out")
		data := []byte(fmt.Sprintf("%s  %s/transaction\n%s\n", sum(mustJSON(tx)), stage, output))
		files[path] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: data}
	}
	compatPath := reusableMaintenancePath(tx.Base, "compat-runner.out")
	compat := files[compatPath].Data
	actualHold = nil
	if err := validate(); err == nil {
		t.Fatal("provision accepted a staged transaction without the real signer hold")
	}
	actualHold = matchingHold
	if err := validate(); err != nil {
		t.Fatal("exact staged transaction refused", err)
	}
	for _, change := range []func(*update.Installation){
		func(i *update.Installation) { i.Digest = strings.Repeat("f", 64) },
		func(i *update.Installation) { i.Phase = "rolled-back-held" },
		func(i *update.Installation) { i.InstalledSequenceCommitted = true },
		func(i *update.Installation) { i.Binding.Index.Snapshot.SHA256 = strings.Repeat("f", 64) },
	} {
		foreign := *matchingHold
		change(&foreign)
		actualHold = &foreign
		if err := validate(); err == nil {
			t.Fatal("foreign, changed or non-active signer hold accepted for provisioning")
		}
	}
	actualHold = matchingHold
	basePath := tx.Base.Root + "/install.json"
	baseFile := files[basePath]
	files[basePath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("foreign predecessor")}
	if err := validate(); err == nil {
		t.Fatal("foreign installed predecessor accepted for provisioning")
	}
	files[basePath] = baseFile
	baseConfig := c
	c.Network = "foreign"
	if err := validate(); err == nil {
		t.Fatal("foreign signer configuration accepted for provisioning")
	}
	c = baseConfig
	images[tx.Base.Root+"/fased-app"] = strings.Repeat("f", 64)
	if err := validate(); err == nil {
		t.Fatal("changed live predecessor executable accepted for provisioning")
	}
	images[tx.Base.Root+"/fased-app"] = tx.Base.CoreHash
	for _, foreign := range []hostFile{
		{UID: tx.Base.Owner, Mode: 0644, Regular: true, Data: compat},
		{UID: 0, Mode: 0644, Regular: true, Data: bytes.TrimSuffix(compat, []byte("\n"))},
		{UID: 0, Mode: 0644, Regular: true, Data: bytes.Replace(compat, []byte(sum(mustJSON(tx))), []byte(strings.Repeat("f", 64)), 1)},
	} {
		files[compatPath] = foreign
		if err := validate(); err == nil {
			t.Fatal("foreign or interrupted compatibility output accepted")
		}
	}
	files[compatPath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: compat}
	forwardPath := "/etc/systemd/system/" + plan.forward
	pendingPath := "/etc/systemd/system/." + plan.forward + ".pending"
	forward := plan.units[forwardPath]
	files[pendingPath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: bytes.Clone(forward[:10])}
	if err := validate(); err != nil {
		t.Fatal("unrelated interrupted unit copy blocked the new attempt", err)
	}
	files[pendingPath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("foreign")}
	if err := validate(); err != nil || reads[pendingPath] != 0 || string(files[pendingPath].Data) != "foreign" {
		t.Fatal("legacy pending unit was opened, changed or blocked the new attempt", err)
	}
	delete(files, pendingPath)
	files[forwardPath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("foreign final")}
	if err := validate(); err == nil {
		t.Fatal("foreign final unit accepted")
	}
	delete(files, forwardPath)
	old := files[stage+"/unit-forward"]
	files[stage+"/unit-forward"] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("[Service]\nExecStart=/usr/bin/true\n")}
	if err := validate(); err == nil {
		t.Fatal("foreign root worker accepted")
	}
	files[stage+"/unit-forward"] = old
	images[stage+"/next-signer"] = strings.Repeat("f", 64)
	if err := validate(); err == nil {
		t.Fatal("foreign staged signer accepted")
	}
	images[stage+"/next-signer"] = tx.Target.SignerHash
	files[reusableMaintenancePath(tx.Base, "active.json")] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("foreign active transaction")}
	if err := validate(); err == nil {
		t.Fatal("another active transaction was overwritten")
	}
	delete(files, reusableMaintenancePath(tx.Base, "active.json"))
	files[tx.Base.Root+"/update-start-blocked.json"] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("foreign recovery guard")}
	if err := validate(); err == nil {
		t.Fatal("another recovery guard was ignored")
	}
}

func TestReusableProvisionRequiresSystemdInvocation(t *testing.T) {
	if os.Getuid() == 0 || os.Getgid() == 0 {
		t.Skip("ordinary unprivileged signer required")
	}
	for _, value := range []string{"", "../foreign", strings.Repeat("A", 32), strings.Repeat("a", 31)} {
		t.Setenv("INVOCATION_ID", value)
		if _, err := VerifyReusableAtomicProvision(os.Getuid()); err == nil || !strings.Contains(err.Error(), "systemd invocation identity") {
			t.Fatalf("unscoped invocation %q was not rejected before provisioning: %v", value, err)
		}
	}
}

func TestReusableProvisionVerifiesOriginalBackupBeforePublication(t *testing.T) {
	m := fixtureManifest()
	pin := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	body, err := reusableAtomicProvision(m, pin)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(body)
	firstSignerCheck := strings.Index(unit, " fixture-validate-atomic-provision --owner ")
	backupCheck := strings.Index(unit, " fixture-update-pause-guard --owner "+strconv.Itoa(m.Owner)+" --disposable-fixture --verify-staged-recovery")
	lastSignerCheck := strings.Index(unit, " --disposable-fixture --verify-only\n")
	firstPublication := strings.Index(unit, "ExecStart=-+/usr/bin/ln --no-target-directory -- "+reusableMaintenancePath(m, "staging")+"/transaction ")
	if firstSignerCheck < 0 || backupCheck <= firstSignerCheck || lastSignerCheck <= backupCheck || firstPublication <= lastSignerCheck {
		t.Fatal("signer hold and original app backup must be checked before protected publication")
	}
}

func TestStagedRecoveryRequiresExactTransactionAndForwardFence(t *testing.T) {
	tx, _ := modelTransaction(t)
	stage := reusableMaintenancePath(tx.Base, "staging") + "/transaction"
	markerPath := tx.Base.Root + "/update-start-blocked.json"
	marker := signer.InstallationGuardMarker{Version: 1, Owner: tx.Base.Owner, SignerID: tx.Base.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, Digest: update.InstallDigest(tx.Binding), Direction: "forward"}
	files := map[string]hostFile{
		stage:      {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(tx)},
		markerPath: {UID: 0, Mode: 0644, Regular: true, Data: mustJSON(marker)},
	}
	read := func(path string) (hostFile, error) {
		f, ok := files[path]
		if !ok {
			return hostFile{}, os.ErrNotExist
		}
		return f, nil
	}
	got, err := loadCanonicalStagedController(tx.Base.Owner, read, nil)
	if err != nil || !bytes.Equal(mustJSON(got), mustJSON(tx)) {
		t.Fatal("exact staged recovery binding refused", err)
	}
	for _, foreign := range []hostFile{
		{UID: tx.Base.Owner, Mode: 0644, Regular: true, Data: mustJSON(marker)},
		{UID: 0, Mode: 0600, Regular: true, Data: mustJSON(marker)},
		{UID: 0, Mode: 0644, Regular: true, Data: []byte("foreign")},
	} {
		files[markerPath] = foreign
		if _, err = loadCanonicalStagedController(tx.Base.Owner, read, nil); err == nil {
			t.Fatal("foreign staged recovery fence accepted")
		}
	}
	marker.Direction = "rollback"
	files[markerPath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(marker)}
	if _, err = loadCanonicalStagedController(tx.Base.Owner, read, nil); err == nil {
		t.Fatal("rollback fence accepted for staged forward backup")
	}
	marker.Direction = "forward"
	files[markerPath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(marker)}
	sentinel, err := signer.InstallationPreparationFenceBytes(tx.Base.Owner, tx.Base.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := reusableMaintenancePath(tx.Base, "preparation-fence.json")
	files[markerPath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: sentinel}
	files[source] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: sentinel}
	if _, err = loadCanonicalStagedController(tx.Base.Owner, read, nil); err == nil {
		t.Fatal("an unproved preparation sentinel was accepted")
	}
	if _, err = loadCanonicalStagedController(tx.Base.Owner, read, func(Manifest) error { return errors.New("different inode") }); err == nil {
		t.Fatal("a foreign preparation sentinel was accepted")
	}
	if _, err = loadCanonicalStagedController(tx.Base.Owner, read, func(Manifest) error { return nil }); err != nil {
		t.Fatal("the protected preparation sentinel was refused", err)
	}
	files[markerPath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(marker)}
	files[stage] = hostFile{UID: tx.Base.Owner, Mode: 0644, Regular: true, Data: mustJSON(tx)}
	if _, err = loadCanonicalStagedController(tx.Base.Owner, read, nil); err == nil {
		t.Fatal("owner-writable staged transaction accepted")
	}
}

// Exercise the generated provision commands with ordinary-user OS tools. The
// owner flags are omitted only because this isolated fixture is not root.
func TestReusableProvisionPreservesInterruptedWorkerCopies(t *testing.T) {
	m := fixtureManifest()
	pin := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	body, err := reusableAtomicProvision(m, pin)
	if err != nil {
		t.Fatal(err)
	}
	commands, err := parseAtomicCommands(body, "ExecStart", reusableMaintenancePath(m, "atomic-guard"))
	if err != nil {
		t.Fatal(err)
	}
	stage := reusableMaintenancePath(m, "staging")
	installAt, mkdirAt, linkAt := -1, -1, -1
	for i, command := range commands {
		if command.argv[0] == "/usr/bin/mkdir" && strings.Contains(strings.Join(command.argv, " "), "${INVOCATION_ID}") {
			mkdirAt = i
		}
		if command.argv[0] == "/usr/bin/install" && strings.Contains(strings.Join(command.argv, " "), stage+"/unit-forward") {
			installAt = i
		}
		if installAt >= 0 && i > installAt && command.argv[0] == "/usr/bin/ln" {
			linkAt = i
			break
		}
	}
	if installAt < 0 || linkAt < 0 {
		t.Fatal("generated forward-worker copy or publication is missing")
	}
	root := t.TempDir()
	etc, staged := filepath.Join(root, "etc"), filepath.Join(root, "stage")
	if err = os.Mkdir(etc, 0700); err != nil {
		t.Fatal(err)
	}
	// Prefer a separate source filesystem when an ordinary-user tmpfs exists;
	// the destination and its no-clobber link must remain on the same device.
	if candidate, createErr := os.MkdirTemp("/dev/shm", "fased-provision-source-"); createErr == nil {
		sourceInfo, sourceErr := os.Stat(candidate)
		destInfo, destErr := os.Stat(etc)
		var sourceDev, destDev uint64
		if sourceErr == nil && destErr == nil {
			if stat, ok := sourceInfo.Sys().(*syscall.Stat_t); ok {
				sourceDev = uint64(stat.Dev)
			}
			if stat, ok := destInfo.Sys().(*syscall.Stat_t); ok {
				destDev = uint64(stat.Dev)
			}
		}
		if sourceDev != 0 && destDev != 0 && sourceDev != destDev {
			staged = candidate
			t.Cleanup(func() { _ = os.RemoveAll(candidate) })
			t.Log("source and destination use separate filesystems")
		} else {
			_ = os.Remove(candidate)
		}
	}
	if staged == filepath.Join(root, "stage") {
		if err = os.Mkdir(staged, 0700); err != nil {
			t.Fatal(err)
		}
	}
	want := []byte("matching prefix and complete worker\n")
	if err = os.WriteFile(filepath.Join(staged, "unit-forward"), want, 0644); err != nil {
		t.Fatal(err)
	}
	tx := strings.Repeat("a", 32)
	legacy := filepath.Join(etc, fmt.Sprintf(".fased-custody-%d-atomic-%s-forward.service.pending", m.Owner, tx))
	original := []byte("matching prefix")
	if err = os.WriteFile(legacy, original, 0644); err != nil {
		t.Fatal(err)
	}
	arguments := func(command atomicPairCommand, attempt string) []string {
		var args []string
		for i := 1; i < len(command.argv); i++ {
			if (command.argv[i] == "-o" || command.argv[i] == "-g") && i+1 < len(command.argv) && command.argv[i+1] == "root" {
				i++
				continue
			}
			if (command.argv[i] == "-uid" || command.argv[i] == "-gid") && i+1 < len(command.argv) && command.argv[i+1] == "0" {
				args = append(args, command.argv[i])
				if command.argv[i] == "-uid" {
					args = append(args, strconv.Itoa(os.Getuid()))
				} else {
					args = append(args, strconv.Itoa(os.Getgid()))
				}
				i++
				continue
			}
			arg := strings.ReplaceAll(command.argv[i], stage, staged)
			arg = strings.ReplaceAll(arg, "/etc/systemd/system", etc)
			arg = strings.ReplaceAll(arg, "${FASED_TXID}", tx)
			arg = strings.ReplaceAll(arg, "${INVOCATION_ID}", attempt)
			args = append(args, arg)
		}
		return args
	}
	run := func(command atomicPairCommand, attempt string) {
		t.Helper()
		args := arguments(command, attempt)
		if out, runErr := exec.Command(command.argv[0], args...).CombinedOutput(); runErr != nil {
			t.Fatalf("%s failed: %v: %s", command.argv[0], runErr, out)
		}
	}
	first, second := strings.Repeat("1", 32), strings.Repeat("2", 32)
	if mkdirAt >= 0 {
		run(commands[mkdirAt], first)
	}
	run(commands[installAt], first) // Simulate interruption before publication.
	if got, err := os.ReadFile(legacy); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("pre-existing pending worker was changed: %q, %v", got, err)
	}
	firstArgs := arguments(commands[installAt], first)
	firstCopy := firstArgs[len(firstArgs)-1]
	partial := bytes.Clone(want[:8])
	if err = os.WriteFile(firstCopy, partial, 0644); err != nil { // Simulate a copy interrupted mid-write.
		t.Fatal(err)
	}
	if mkdirAt < 0 || exec.Command(commands[mkdirAt].argv[0], arguments(commands[mkdirAt], first)...).Run() == nil {
		t.Fatal("an existing invocation directory was reused")
	}
	third := strings.Repeat("3", 32)
	thirdArgs := arguments(commands[mkdirAt], third)
	foreignDir := filepath.Join(root, "foreign")
	if err = os.Mkdir(foreignDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(foreignDir, thirdArgs[len(thirdArgs)-1]); err != nil {
		t.Fatal(err)
	}
	if exec.Command(commands[mkdirAt].argv[0], thirdArgs...).Run() == nil {
		t.Fatal("a symlinked invocation directory was reused")
	}
	if mkdirAt >= 0 {
		run(commands[mkdirAt], second)
	}
	run(commands[installAt], second)
	for i := installAt + 1; i < linkAt; i++ {
		run(commands[i], second)
	}
	final := filepath.Join(etc, fmt.Sprintf("fased-custody-%d-atomic-%s-forward.service", m.Owner, tx))
	if err = os.WriteFile(final, []byte("foreign"), 0644); err != nil {
		t.Fatal(err)
	}
	if exec.Command(commands[linkAt].argv[0], arguments(commands[linkAt], second)...).Run() == nil {
		t.Fatal("foreign final worker was replaced")
	}
	if linkAt+1 >= len(commands) || commands[linkAt+1].argv[0] != "/usr/bin/cmp" || exec.Command(commands[linkAt+1].argv[0], arguments(commands[linkAt+1], second)...).Run() == nil {
		t.Fatal("the generated service did not refuse the foreign final worker")
	}
	if got, err := os.ReadFile(final); err != nil || string(got) != "foreign" {
		t.Fatalf("foreign final worker was changed: %q, %v", got, err)
	}
	if err = os.Remove(final); err != nil { // Only this test-created file.
		t.Fatal(err)
	}
	run(commands[linkAt], second)
	for i := linkAt + 1; i < len(commands); i++ {
		if commands[i].argv[0] == "/usr/bin/install" {
			break
		}
		run(commands[i], second)
	}
	removed := false
	for _, command := range commands {
		if command.argv[0] == "/usr/bin/rmdir" && strings.Contains(strings.Join(command.argv, " "), "${INVOCATION_ID}") {
			run(command, second)
			removed = true
			break
		}
	}
	if !removed {
		t.Fatal("the completed invocation directory was not cleaned")
	}
	if got, err := os.ReadFile(final); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("retry did not publish the expected worker: %q, %v", got, err)
	}
	if got, err := os.ReadFile(firstCopy); err != nil || !bytes.Equal(got, partial) {
		t.Fatalf("the interrupted invocation copy was changed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(legacy); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("retry changed the preserved pending worker: %q, %v", got, err)
	}
	// Simulate a later retry after the final link succeeded but cleanup was
	// interrupted. The service ignores the no-clobber link failure, then checks
	// the existing final and removes only this new invocation's copy.
	fourth := strings.Repeat("4", 32)
	run(commands[mkdirAt], fourth)
	run(commands[installAt], fourth)
	for i := installAt + 1; i < linkAt; i++ {
		run(commands[i], fourth)
	}
	if !commands[linkAt].ignore || exec.Command(commands[linkAt].argv[0], arguments(commands[linkAt], fourth)...).Run() == nil {
		t.Fatal("an existing final was not handled by the no-clobber retry")
	}
	for i := linkAt + 1; i < len(commands); i++ {
		if commands[i].argv[0] == "/usr/bin/install" {
			break
		}
		run(commands[i], fourth)
	}
	for _, command := range commands {
		if command.argv[0] == "/usr/bin/rmdir" && strings.Contains(strings.Join(command.argv, " "), "${INVOCATION_ID}") {
			run(command, fourth)
			break
		}
	}
	if got, err := os.ReadFile(final); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("idempotent retry changed the final worker: %q, %v", got, err)
	}
}

func TestReusableCleanupOnlyRemovesMatchingStagingLinks(t *testing.T) {
	tx, _ := modelTransaction(t)
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	plan, err := makeReusableAtomicPairPlan(tx, c, strings.Repeat("d", 64), strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	active := reusableMaintenancePath(tx.Base, "active.json")
	guarded := "ExecStart=-/usr/bin/find " + active + " -maxdepth 0 -samefile " + tx.dir() + "/transaction.json -delete"
	pin := reusableMaintenanceAuthority{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	provision, err := reusableAtomicProvision(tx.Base, pin)
	if err != nil {
		t.Fatal(err)
	}
	for _, direction := range reusableWorkerDirections {
		slot := "unit-" + direction
		source := reusableMaintenancePath(tx.Base, "staging") + "/" + slot
		target := tx.Base.Root + "/updates/${FASED_TXID}/" + slot
		if !strings.Contains(string(provision), "ExecStart=-+/usr/bin/ln --no-target-directory -- "+source+" "+target+"\nExecStart=+/usr/bin/test "+source+" -ef "+target) {
			t.Fatal("unit staging is not recorded as the transaction inode", slot)
		}
	}
	for _, name := range []string{plan.cleanupRollback, plan.cleanupComplete} {
		body := string(plan.units["/etc/systemd/system/"+name])
		if !strings.Contains(body, guarded) || strings.Contains(body, "rm -f -- "+active) {
			t.Fatal("cleanup does not preserve foreign active preparations", name)
		}
		proof := "ExecStart=-/usr/bin/find " + reusableMaintenancePath(tx.Base, "predecessor-proof-v1.json") + " -maxdepth 0 -samefile " + tx.dir() + "/atomic-held.json -delete"
		if !strings.Contains(body, proof) {
			t.Fatal("cleanup does not bind the predecessor proof inode", name)
		}
		proofPath := reusableMaintenancePath(tx.Base, "predecessor-proof-v1.json")
		removed := strings.Index(body, proof)
		absent := strings.Index(body, "ExecStart=/usr/bin/test ! -e "+proofPath)
		noSymlink := strings.Index(body, "ExecStart=/usr/bin/test ! -L "+proofPath)
		activeRemoved := strings.Index(body, "ExecStart=/usr/bin/rm -f -- "+tx.Base.Root+"/update-active.json")
		if removed < 0 || absent < removed || noSymlink < absent || activeRemoved < noSymlink || strings.Index(body, guarded) < noSymlink {
			t.Fatal("cleanup may discard active recovery state while the predecessor proof remains", name)
		}
		for _, item := range reusableMetadataFiles {
			want := "ExecStart=-/usr/bin/find " + reusableMaintenancePath(tx.Base, "staging") + "/" + item.slot + " -maxdepth 0 -samefile " + tx.dir() + "/" + item.name + " -delete"
			if !strings.Contains(body, want) {
				t.Fatal("cleanup omitted exact staging link", item.slot)
			}
		}
		for _, direction := range reusableWorkerDirections {
			slot := "unit-" + direction
			want := "ExecStart=-/usr/bin/find " + reusableMaintenancePath(tx.Base, "staging") + "/" + slot + " -maxdepth 0 -samefile " + tx.dir() + "/" + slot + " -delete"
			if !strings.Contains(body, want) {
				t.Fatal("unit staging cleanup does not bind the protected transaction inode", slot)
			}
		}
		if strings.Contains(body, ".pending") || strings.Contains(body, ".source") || strings.Contains(body, ".copy") {
			t.Fatal("cleanup may delete an unclassified interrupted file", name)
		}
	}
	if strings.Contains(string(plan.units["/etc/systemd/system/"+plan.forward]), "-samefile "+tx.dir()+"/transaction.json -delete") {
		t.Fatal("forward worker discarded preparation state")
	}
}

func TestReusableProofCleanupPreservesRecoveryOnRefusal(t *testing.T) {
	tx, _ := modelTransaction(t)
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	plan, err := makeReusableAtomicPairPlan(tx, c, strings.Repeat("d", 64), strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	proofPath := reusableMaintenancePath(tx.Base, "predecessor-proof-v1.json")
	activePath := tx.Base.Root + "/update-active.json"
	for _, name := range []string{plan.cleanupRollback, plan.cleanupComplete} {
		commands := plan.commands[name]
		first, last := -1, -1
		for i, command := range commands {
			if len(command.argv) > 1 && command.argv[0] == "/usr/bin/find" && command.argv[1] == proofPath {
				first = i
			}
			if len(command.argv) > 3 && command.argv[0] == "/usr/bin/rm" && command.argv[len(command.argv)-1] == activePath {
				last = i
			}
		}
		if first < 0 || last <= first {
			t.Fatal("proof cleanup does not precede active cleanup", name)
		}
		for _, kind := range []string{"matching", "foreign", "dangling-symlink", "unlink-refused"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				if kind == "unlink-refused" && os.Geteuid() == 0 {
					t.Skip("requires the ordinary owner to lack directory write access")
				}
				dir := t.TempDir()
				maintenance := filepath.Join(dir, "maintenance")
				if err := os.Mkdir(maintenance, 0700); err != nil {
					t.Fatal(err)
				}
				proof, source := filepath.Join(maintenance, "proof"), filepath.Join(dir, "held")
				active, fence := filepath.Join(dir, "active"), filepath.Join(dir, "fence")
				for _, path := range []string{source, active, fence} {
					if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				switch kind {
				case "matching", "unlink-refused":
					err = os.Link(source, proof)
				case "foreign":
					err = os.WriteFile(proof, []byte("preserve"), 0600)
				case "dangling-symlink":
					err = os.Symlink(filepath.Join(dir, "missing"), proof)
				}
				if err != nil {
					t.Fatal(err)
				}
				if kind == "unlink-refused" {
					if err := os.Chmod(maintenance, 0500); err != nil {
						t.Fatal(err)
					}
					defer os.Chmod(maintenance, 0700)
				}
				paths := map[string]string{proofPath: proof, tx.dir() + "/atomic-held.json": source, reusableMaintenancePath(tx.Base, ""): maintenance, activePath: active}
				var failure error
				for _, command := range commands[first : last+1] {
					args := append([]string(nil), command.argv...)
					for i, arg := range args {
						if replacement, ok := paths[arg]; ok {
							args[i] = replacement
						}
					}
					if err := exec.Command(args[0], args[1:]...).Run(); err != nil && !command.ignore {
						failure = err
						break
					}
				}
				if kind == "matching" {
					if failure != nil {
						t.Fatal("matching proof refused", failure)
					}
					if _, err := os.Lstat(active); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("completed cleanup retained active record", err)
					}
				} else {
					if failure == nil {
						t.Fatal("unsafe proof cleanup succeeded")
					}
					for _, path := range []string{proof, active, fence} {
						if _, err := os.Lstat(path); err != nil {
							t.Fatal("refusal did not preserve recovery input", path, err)
						}
					}
				}
			})
		}
	}
}

func TestAtomicPolicyIsExactAndHasValidSudoersSyntax(t *testing.T) {
	tx, _ := modelTransaction(t)
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	plan, err := makeAtomicPairPlan(tx, c, strings.Repeat("d", 64), strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	body, err := atomicPolicyBytes(tx, plan, "fased_test")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if strings.ContainsAny(line, "*?") || strings.Contains(line, "/usr/bin/mv") || strings.Contains(line, "/usr/bin/install") || strings.Contains(line, "/usr/bin/rm") || strings.Contains(line, "/usr/bin/systemd-run") {
			t.Fatal("broad or direct mutation granted", line)
		}
	}
	if _, err := atomicPolicyBytes(tx, plan, "user ALL=(root)"); err == nil {
		t.Fatal("invalid policy account accepted")
	}
	tool, err := exec.LookPath("visudo")
	if err != nil {
		t.Skip("visudo unavailable")
	}
	path := filepath.Join(t.TempDir(), "policy")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(tool, "-cf", path).CombinedOutput(); err != nil {
		t.Fatalf("invalid sudoers policy: %v: %s", err, b)
	}
}

func TestPasswordFreeSudoInventoryRejectsUnclassifiedAuthority(t *testing.T) {
	listing := []byte("Sudoers entry: /etc/sudoers\n    RunAsUsers: ALL\n    Commands:\n\tALL\n\nSudoers entry: /etc/sudoers.d/fased-native-1800\n    RunAsUsers: root\n    Options: !authenticate\n    Commands:\n\t/usr/bin/systemctl start fased-custody-1800-app.service\n")
	paths, err := passwordFreeSudoPaths(listing)
	if err != nil || len(paths) != 1 || paths[0] != "/etc/sudoers.d/fased-native-1800" {
		t.Fatal(paths, err)
	}
	for _, invalid := range []string{
		"Options: !authenticate\n",
		"Sudoers entry: relative\n    Options: !authenticate\n",
		"Sudoers entry: /etc/sudoers\n    Options: !authenticate, setenv\n",
		"Matching Defaults entries for owner on host:\n    env_reset, !authenticate, use_pty\n\nUser owner may run the following commands:\n\nSudoers entry: /etc/sudoers\n    RunAsUsers: root\n    Commands:\n\tALL\n",
	} {
		if _, err := passwordFreeSudoPaths([]byte(invalid)); err == nil {
			t.Fatal("unclassified sudo entry accepted")
		}
	}
}

func TestAtomicUnitCreationBindsBytesAndPreservesInterruptedFile(t *testing.T) {
	owner := os.Getuid()
	target := fmt.Sprintf("/etc/systemd/system/fased-custody-%d-atomic-%s-forward.service", owner, strings.Repeat("a", 32))
	body := []byte("[Service]\nType=oneshot\nExecStart=/usr/bin/true\n")
	args, err := BoundAtomicUnitWriteArgs(owner, target, body)
	if err != nil || !AllowedBoundAtomicUnitWrite(owner, body, args) || AllowedBoundAtomicUnitWrite(owner, append(bytes.Clone(body), 'x'), args) {
		t.Fatal("atomic unit bytes are not bound", err)
	}
	if _, err := BoundAtomicUnitWriteArgs(owner+1, target, body); err == nil {
		t.Fatal("foreign atomic unit accepted")
	}
	h := &memoryHost{files: map[string]hostFile{}, failAfter: 1}
	if err := publishAtomicPairUnit(t.Context(), target, body, h.read, h.run); err == nil {
		t.Fatal("interruption was not reported")
	}
	h.failAfter = 0
	if err := publishAtomicPairUnit(t.Context(), target, body, h.read, h.run); err != nil {
		t.Fatal("exact completed unit did not resume", err)
	}
	h.files[target] = hostFile{Data: body[:5], UID: 0, Mode: 0644, Regular: true}
	before := h.calls
	if err := publishAtomicPairUnit(t.Context(), target, body, h.read, h.run); err == nil || h.calls != before {
		t.Fatal("partial unit was overwritten")
	}
	delete(h.files, target)
	if err := publishAtomicPairUnit(t.Context(), target, body, func(string) (hostFile, error) { return hostFile{}, errors.New("unavailable") }, h.run); err == nil || h.calls != before {
		t.Fatal("unavailable unit identity reached privileged write")
	}
}

func TestPrepareAtomicPairPreflightsAndResumesWithoutReplacingActiveInputs(t *testing.T) {
	tx, _ := modelTransaction(t)
	if tx.Base.Owner != os.Getuid() {
		t.Skip("model requires its ordinary fixture owner")
	}
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	guard := []byte("independent guard signer")
	plan, err := makeAtomicPairPlan(tx, c, sum(guard), strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	h := &memoryHost{files: map[string]hostFile{}}
	verify := func(path, digest string, size int64) error {
		f, err := h.read(path)
		if err != nil || f.UID != 0 || f.Mode != 0444 || !f.Regular || sum(f.Data) != digest || int64(len(f.Data)) != size {
			return errors.New("approved input unavailable")
		}
		return nil
	}
	prepare := func(active bool) error {
		return prepareAtomicPair(t.Context(), tx, plan, h.read, h.run, verify, func() (bool, error) { return active, nil })
	}
	if err := prepare(false); err == nil || h.calls != 0 {
		t.Fatal("missing approved inputs reached privileged writes")
	}
	for _, data := range plan.inputs {
		h.files[ApprovedExecutablePath(tx.Base.Owner, data)] = hostFile{Data: bytes.Clone(data), UID: 0, Mode: 0444, Regular: true}
	}
	if err := prepare(false); err == nil || h.calls != 0 {
		t.Fatal("missing signer guard reached privileged writes")
	}
	h.files[tx.dir()+"/atomic-guard"] = hostFile{Data: bytes.Clone(guard), UID: 0, Mode: 0755, Regular: true}
	if err := prepare(true); err == nil || h.calls != 0 {
		t.Fatal("active policy allowed incomplete setup")
	}
	h.failAfter = 1
	if err := prepare(false); err == nil {
		t.Fatal("interruption was not reported")
	}
	h.failAfter = 0
	if err := prepare(false); err != nil {
		t.Fatal("same approved setup did not resume", err)
	}
	before := h.calls
	if err := prepare(true); err != nil || h.calls != before {
		t.Fatal("complete active setup was rewritten", err)
	}
	delete(h.files, "/etc/systemd/system/"+plan.rollback)
	if err := prepare(true); err == nil || h.calls != before {
		t.Fatal("missing active unit was recreated", err)
	}
}

func TestApprovedAtomicInputsAreBoundAndNeverOverwritePartialFiles(t *testing.T) {
	tx, _ := modelTransaction(t)
	if tx.Base.Owner != os.Getuid() {
		t.Skip("model requires its ordinary fixture owner")
	}
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	plan, err := makeAtomicPairPlan(tx, c, strings.Repeat("d", 64), strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	h := &memoryHost{files: map[string]hostFile{}, failAfter: 1}
	verify := func(path, digest string, size int64) error {
		f, err := h.read(path)
		if err != nil || f.UID != 0 || f.Mode != 0444 || !f.Regular || sum(f.Data) != digest || int64(len(f.Data)) != size {
			return errors.New("source unavailable")
		}
		return nil
	}
	if err := prepareApprovedAtomicInputs(t.Context(), tx.Base.Owner, plan, h.read, h.run, verify); err == nil {
		t.Fatal("interruption was not reported")
	}
	h.failAfter = 0
	if err := prepareApprovedAtomicInputs(t.Context(), tx.Base.Owner, plan, h.read, h.run, verify); err != nil {
		t.Fatal("exact sources did not resume", err)
	}
	for _, data := range plan.inputs {
		path := ApprovedExecutablePath(tx.Base.Owner, data)
		h.files[path] = hostFile{Data: bytes.Clone(data[:1]), UID: 0, Mode: 0444, Regular: true}
		before := h.calls
		if err := prepareApprovedAtomicInputs(t.Context(), tx.Base.Owner, plan, h.read, h.run, verify); err == nil || h.calls != before {
			t.Fatal("partial source overwritten")
		}
		break
	}
}

func TestRestartGuardsPreserveForeignFilesAndResumeProvisioning(t *testing.T) {
	m := fixtureManifest()
	if m.Owner != os.Getuid() {
		t.Skip("model requires its ordinary fixture owner")
	}
	h := &memoryHost{files: map[string]hostFile{}}
	ensure := func(string) error { return nil }
	path := "/etc/systemd/system/" + Unit(m.Owner, "signer") + ".d/20-fased-update-guard.conf"
	h.files[path] = hostFile{Data: []byte("foreign"), UID: 0, Mode: 0644, Regular: true}
	if err := prepareRestartGuards(t.Context(), m, h.read, ensure, h.run); err == nil || h.calls != 0 {
		t.Fatal("foreign assertion was touched")
	}
	delete(h.files, path)
	h.files[m.Root+"/update-start-blocked.json"] = hostFile{Data: []byte("held"), UID: 0, Mode: 0644, Regular: true}
	if err := prepareRestartGuards(t.Context(), m, h.read, ensure, h.run); err == nil || h.calls != 0 {
		t.Fatal("existing recovery marker was ignored")
	}
	delete(h.files, m.Root+"/update-start-blocked.json")
	h.failAfter = 1
	if err := prepareRestartGuards(t.Context(), m, h.read, ensure, h.run); err == nil {
		t.Fatal("interruption was not reported")
	}
	h.failAfter = 0
	if err := prepareRestartGuards(t.Context(), m, h.read, ensure, h.run); err != nil {
		t.Fatal("guard provisioning did not resume", err)
	}
}
