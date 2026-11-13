//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestReusableControllerRefusesFreshFenceBeforeHold(t *testing.T) {
	tx, _ := modelTransaction(t)
	m := tx.Base
	m.Owner = 2140000000
	m.Root = Root(m.Owner)
	m.App.Name, m.Signer.Name = accountName(m.Owner, "app"), accountName(m.Owner, "signer")
	if _, err := os.Lstat(m.Root); !errors.Is(err, os.ErrNotExist) {
		t.Skip("test-only managed root already exists or cannot be inspected")
	}
	tx.Base = m
	called := false
	_, err := prepareReusableController(t.Context(), tx, signer.Config{}, "", &System{}, nil, func(context.Context, string, update.InstallBinding) (*update.Installation, error) {
		called = true
		return nil, nil
	})
	if called || err == nil || !strings.Contains(err.Error(), "new preparation fence unavailable") {
		t.Fatalf("fresh preparation reached the signer hold: called=%t err=%v", called, err)
	}
}

func TestReadOnlyPreparationFenceClassification(t *testing.T) {
	tx, _ := modelTransaction(t)
	m, id := tx.Base, tx.Binding.Index.Binding.Pause.Request.IntentID
	digest := update.InstallDigest(tx.Binding)
	files := map[string]hostFile{}
	read := func(path string) (hostFile, error) {
		if f, ok := files[path]; ok {
			return f, nil
		}
		return hostFile{}, os.ErrNotExist
	}
	check := func(want preparationFenceState, wantErr bool, same func() error) {
		t.Helper()
		got, err := inspectPreparationFence(m, id, digest, read, same)
		if (err != nil) != wantErr || (!wantErr && got != want) {
			t.Fatalf("fence classification=%v err=%v", got, err)
		}
	}
	same := func() error { return nil }
	check(preparationFenceAbsent, false, same)
	path := m.Root + "/update-start-blocked.json"
	sentinel, err := signer.InstallationPreparationFenceBytes(m.Owner, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	files[path] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: sentinel}
	check(preparationFencePending, false, same)
	check(preparationFenceAbsent, true, func() error { return errors.New("different sentinel inode") })
	marker := signer.InstallationGuardMarker{Version: 1, Owner: m.Owner, SignerID: m.ID, IntentID: id, Digest: digest, Direction: "forward"}
	files[path] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(marker)}
	check(preparationFenceTransaction, false, same)
	if _, err = inspectPreparationFence(m, id, "", read, same); err == nil {
		t.Fatal("a transaction fence was accepted without its published binding")
	}
	marker.Digest = strings.Repeat("f", 64)
	files[path] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(marker)}
	check(preparationFenceAbsent, true, same)
	marker.Digest, marker.Direction = digest, "foreign"
	files[path] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(marker)}
	check(preparationFenceAbsent, true, same)
}

func TestReusablePreparationStartPreservesOtherUpdatePhases(t *testing.T) {
	tx, _ := modelTransaction(t)
	m := tx.Base
	r := reusableAtomicRequest{Version: 1, Owner: m.Owner, InstallationID: m.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, Direction: "fence", RequestID: strings.Repeat("d", 32)}
	files := map[string]hostFile{}
	read := func(path string) (hostFile, error) {
		if f, ok := files[path]; ok {
			return f, nil
		}
		return hostFile{}, os.ErrNotExist
	}
	check := func(same func() error) error { return validateReusablePreparationStart(m, r, read, same) }
	same := func() error { return nil }
	if err := check(same); err == nil {
		t.Fatal("fresh preparation could stop services before safe recovery is available")
	}
	marker := m.Root + "/update-start-blocked.json"
	sentinel, err := signer.InstallationPreparationFenceBytes(m.Owner, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	files[marker] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: sentinel}
	activePath := m.Root + "/update-active.json"
	files[activePath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("preserve")}
	if check(same) == nil {
		t.Fatal("an existing active update could be fenced again")
	}
	delete(files, activePath)
	proofPath := reusableMaintenancePath(m, "predecessor-proof-v1.json")
	files[proofPath] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("preserve")}
	if check(same) == nil {
		t.Fatal("an existing predecessor proof could be fenced again")
	}
	delete(files, proofPath)
	delete(files, marker)
	preparation := reusableMaintenancePath(m, "active.json")
	files[preparation] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(tx)}
	if check(same) == nil {
		t.Fatal("an existing preparation without its sentinel was accepted")
	}
	delete(files, preparation)
	published := m.Root + "/updates/" + r.IntentID + "/transaction.json"
	files[published] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(tx)}
	if check(same) == nil {
		t.Fatal("a published transaction without its sentinel was fenced again")
	}
	files[marker] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: sentinel}
	if err = check(same); err != nil {
		t.Fatal("the exact predecessor transaction was refused before adoption", err)
	}
	progress := m.Root + "/updates/" + r.IntentID + "/rollback.json"
	files[progress] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("preserve")}
	if check(same) == nil {
		t.Fatal("a progressed predecessor was admitted as a fresh adoption")
	}
	delete(files, progress)
	files[preparation] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(tx)}
	if check(func() error { return errors.New("different inode") }) == nil {
		t.Fatal("a foreign sentinel inode was accepted")
	}
	if err = check(same); err != nil {
		t.Fatal("the same interrupted preparation was refused", err)
	}
	files[published] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("foreign")}
	if check(same) == nil {
		t.Fatal("a changed published transaction was accepted")
	}
}

func TestReusablePreparationRefusesQuarantinedPredecessor(t *testing.T) {
	repair, dir, ops, _ := ordinaryRepairModel(t)
	tx := *repair.Repair
	m := tx.Base
	sentinel, err := signer.InstallationPreparationFenceBytes(m.Owner, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "update-start-blocked.json"), sentinel, 0644); err != nil {
		t.Fatal(err)
	}
	r := reusableAtomicRequest{Version: 1, Owner: m.Owner, InstallationID: m.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, Direction: "fence", RequestID: strings.Repeat("d", 32)}
	check := func() error { return validateReusablePreparationStart(m, r, ops.read, func() error { return nil }) }
	if err = check(); err == nil || !strings.Contains(err.Error(), "reader repair owns") {
		t.Fatalf("prepared reader repair did not quarantine the predecessor: %v", err)
	}
	c := identityConfig(m)
	pin := &reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	held := &update.Installation{Version: 1, Binding: tx.Binding, Digest: update.InstallDigest(tx.Binding), Phase: "active"}
	stage := r
	stage.Direction, stage.StageKind, stage.StageSlot = "stage", "image", "next-core"
	checkStage := func() error {
		_, err := verifyReusableStageHeld(m, c, pin, stage, "image", "next-core", func() error { return nil }, func(signer.Config, string) (*update.Installation, error) { return held, nil }, ops.read)
		return err
	}
	checkExecutable := func(string, string) (int64, error) { return 0, nil }
	if err = checkStage(); err == nil || !strings.Contains(err.Error(), "reader repair owns") {
		t.Fatalf("reader repair did not quarantine staging: %v", err)
	}
	if err = validateReusableAtomicInputs(tx, c, held, ops.read, checkExecutable, true); err == nil || !strings.Contains(err.Error(), "reader repair owns") {
		t.Fatalf("reader repair did not quarantine provisioning: %v", err)
	}
	inspect := func(context.Context, string) (map[string]string, []byte, error) { return nil, nil, nil }
	if err = validateReusableAtomicWorker(t.Context(), tx, c, "forward", ops.read, checkExecutable, inspect); err == nil || !strings.Contains(err.Error(), "reader repair owns") {
		t.Fatalf("reader repair did not quarantine worker admission: %v", err)
	}
	cancelled := filepath.Join(dir, "bootstraps", repair.ID, "cancelled.json")
	if err = os.WriteFile(cancelled, mustJSON(phaseRecord{1, repair.Digest(), "cancelled"}), 0644); err != nil {
		t.Fatal(err)
	}
	if err = check(); err != nil {
		t.Fatalf("a cancelled pre-forward repair blocked its predecessor: %v", err)
	}
	if err = checkStage(); err == nil || !strings.Contains(err.Error(), "protected staging adoption") {
		t.Fatalf("cancelled repair must leave published files protected until adoption: %v", err)
	}
	forward := filepath.Join(dir, "bootstraps", repair.ID, "forward-only.json")
	if err = os.WriteFile(forward, []byte("preserve"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = check(); err == nil || !strings.Contains(err.Error(), "reader repair owns") {
		t.Fatalf("a forward-only repair did not quarantine its predecessor: %v", err)
	}
	if got, err := os.ReadFile(forward); err != nil || string(got) != "preserve" {
		t.Fatalf("quarantine check changed the original repair: %v", err)
	}
}

func TestReusableStageRetainedEntryLimit(t *testing.T) {
	owner, group := os.Getuid(), os.Getgid()
	if owner <= 0 || group <= 0 {
		t.Skip("ordinary unprivileged user required")
	}
	parent := t.TempDir()
	stage, inbox := filepath.Join(parent, "stage"), filepath.Join(parent, "inbox")
	if err := os.Mkdir(stage, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(inbox, 0710); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inbox, os.ModeSetgid|0710); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "transaction"), []byte("original"), 0640); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		if err := os.WriteFile(filepath.Join(stage, fmt.Sprintf("foreign-%02d", i)), []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func() error {
		return validateStageRecoveryAt(stage, inbox, owner, group, owner, group, "meta", "transaction", strings.Repeat("a", 32))
	}
	if err := check(); err == nil {
		t.Fatal("unbounded retained staging entries were accepted")
	}
	for i := 0; i < 64; i++ {
		if b, err := os.ReadFile(filepath.Join(stage, fmt.Sprintf("foreign-%02d", i))); err != nil || string(b) != "preserve" {
			t.Fatal("refusal changed a retained entry", i, err)
		}
	}
	if err := os.Remove(filepath.Join(stage, "foreign-63")); err != nil { // Test-owned only.
		t.Fatal(err)
	}
	if err := check(); err != nil {
		t.Fatal("staging below the retained-entry bound was refused", err)
	}
}

// Run the generated OS copy commands as an ordinary user. This exercises
// retry and preservation without claiming root/systemd or signer acceptance.
func TestReusableStageInvocationRetriesPreserveInterruptedCopies(t *testing.T) {
	if os.Getuid() <= 0 || os.Getgid() <= 0 {
		t.Skip("ordinary unprivileged user required")
	}
	m := fixtureManifest()
	body, err := reusableStageTemplate(m, 1000, "meta")
	if err != nil {
		t.Fatal(err)
	}
	stage, inbox := reusableMaintenancePath(m, "staging"), reusableMaintenancePath(m, "inbox")
	if !strings.Contains(string(body), "${INVOCATION_ID}") || strings.Contains(string(body), "ConditionPathExists=!"+stage+"/%i.pending") {
		t.Fatal("staging still uses a fixed interrupted-copy slot")
	}
	bound := bytes.ReplaceAll(body, []byte("%i"), []byte("transaction"))
	pre, err := parseAtomicCommands(bound, "ExecStartPre", "")
	if err != nil {
		t.Fatal(err)
	}
	main, err := parseAtomicCommands(bound, "ExecStart", "")
	if err != nil || len(main) != 1 || main[0].argv[0] != "/usr/bin/cp" {
		t.Fatal("one unprivileged owner copy is required", err)
	}
	post, err := parseAtomicCommands(bound, "ExecStartPost", "")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	localStage, localInbox := filepath.Join(root, "stage"), filepath.Join(root, "inbox")
	for _, p := range []string{localStage, localInbox} {
		if err := os.Mkdir(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	want := []byte("original reviewed transaction\n")
	if err := os.WriteFile(filepath.Join(localInbox, "transaction"), want, 0640); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"transaction.pending", "transaction.retry.pending"} {
		if err := os.WriteFile(filepath.Join(localStage, name), []byte("preserved "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var mkdir, chmod, prepare atomicPairCommand
	for _, cmd := range pre {
		joined := strings.Join(cmd.argv, " ")
		if !strings.Contains(joined, "${INVOCATION_ID}") {
			continue
		}
		switch cmd.argv[0] {
		case "/usr/bin/mkdir":
			mkdir = cmd
		case "/usr/bin/chmod":
			chmod = cmd
		case "/usr/bin/install":
			prepare = cmd
		}
	}
	if len(mkdir.argv) == 0 || len(chmod.argv) == 0 || len(prepare.argv) == 0 {
		t.Fatal("exclusive invocation preparation is missing")
	}
	args := func(cmd atomicPairCommand, attempt string) []string {
		var out []string
		for i := 1; i < len(cmd.argv); i++ {
			if cmd.argv[0] == "/usr/bin/install" && (cmd.argv[i] == "-o" || cmd.argv[i] == "-g") && i+1 < len(cmd.argv) {
				i++ // The fixture runs as its existing ordinary user.
				continue
			}
			if (cmd.argv[i] == "-uid" || cmd.argv[i] == "-gid") && i+1 < len(cmd.argv) {
				uid := os.Getuid()
				if cmd.argv[i] == "-gid" {
					uid = os.Getgid()
				}
				out = append(out, cmd.argv[i], strconv.Itoa(uid))
				i++
				continue
			}
			a := strings.ReplaceAll(cmd.argv[i], stage, localStage)
			a = strings.ReplaceAll(a, inbox, localInbox)
			a = strings.ReplaceAll(a, "${INVOCATION_ID}", attempt)
			out = append(out, a)
		}
		return out
	}
	run := func(cmd atomicPairCommand, attempt string) {
		t.Helper()
		if cmd.argv[0] == "/usr/bin/setpriv" && strings.Contains(strings.Join(cmd.argv, " "), "--copy --disposable-fixture") {
			// This OS-command fixture has one UID; the held-copy check is exercised
			// separately below and the real signer/OS boundary remains untested.
			return
		}
		if out, runErr := exec.Command(cmd.argv[0], args(cmd, attempt)...).CombinedOutput(); runErr != nil {
			t.Fatalf("%s: %v: %s", cmd.argv[0], runErr, out)
		}
	}
	for _, digit := range []string{"1", "2"} {
		attempt := strings.Repeat(digit, 32)
		run(mkdir, attempt)
		run(chmod, attempt)
		run(prepare, attempt)
		work := args(prepare, attempt)
		if err := os.WriteFile(work[len(work)-1], []byte("partial "+digit), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// An owner holding an old descriptor may truncate its input after cp. The
	// simplest invalid case must refuse before any final path is published.
	attempt := strings.Repeat("3", 32)
	run(mkdir, attempt)
	run(chmod, attempt)
	run(prepare, attempt)
	run(main[0], attempt)
	work := args(prepare, attempt)
	if err := os.Truncate(work[len(work)-1], 0); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(post[0].argv[0], args(post[0], attempt)...).Run(); err == nil {
		t.Fatal("empty input was accepted after the owner copy")
	}
	if _, err := os.Lstat(filepath.Join(localStage, "transaction")); !os.IsNotExist(err) {
		t.Fatal("invalid input published a final slot", err)
	}
	// The owner may retain a writable descriptor across the first size check.
	attempt = strings.Repeat("6", 32)
	run(mkdir, attempt)
	run(chmod, attempt)
	run(prepare, attempt)
	run(main[0], attempt)
	run(post[0], attempt)
	work = args(prepare, attempt)
	if err := os.Truncate(work[len(work)-1], 0); err != nil {
		t.Fatal(err)
	}
	run(post[1], attempt)
	if err := exec.Command(post[2].argv[0], args(post[2], attempt)...).Run(); err == nil {
		t.Fatal("empty root copy was accepted after the input check")
	}
	if _, err := os.Lstat(filepath.Join(localStage, "transaction")); !os.IsNotExist(err) {
		t.Fatal("empty root copy published a final slot", err)
	}
	// A writer outside the unit can grow the same descriptor after the check;
	// the privileged copy must stay bounded and refuse the oversized result.
	attempt = strings.Repeat("7", 32)
	run(mkdir, attempt)
	run(chmod, attempt)
	run(prepare, attempt)
	run(main[0], attempt)
	run(post[0], attempt)
	work = args(prepare, attempt)
	if err := os.Truncate(work[len(work)-1], 32<<20); err != nil {
		t.Fatal(err)
	}
	run(post[1], attempt)
	copyArgs := args(post[1], attempt)
	var copyPath string
	for _, arg := range copyArgs {
		if strings.HasPrefix(arg, "of=") {
			copyPath = strings.TrimPrefix(arg, "of=")
		}
	}
	if info, err := os.Stat(copyPath); err != nil || info.Size() > (4<<20)+(64<<10) {
		t.Fatalf("privileged copy was not bounded: %v, %v", info, err)
	}
	if err := exec.Command(post[2].argv[0], args(post[2], attempt)...).Run(); err == nil {
		t.Fatal("oversized root copy was accepted")
	}
	if _, err := os.Lstat(filepath.Join(localStage, "transaction")); !os.IsNotExist(err) {
		t.Fatal("oversized root copy published a final slot", err)
	}
	// A foreign final may never be replaced, even by a valid new attempt.
	attempt = strings.Repeat("4", 32)
	run(mkdir, attempt)
	run(chmod, attempt)
	run(prepare, attempt)
	run(main[0], attempt)
	linkAt := -1
	for i, cmd := range post {
		if cmd.argv[0] == "/usr/bin/ln" {
			linkAt = i
			break
		}
		run(cmd, attempt)
	}
	if linkAt < 0 {
		t.Fatal("no guarded final publication")
	}
	final := filepath.Join(localStage, "transaction")
	if err := os.WriteFile(final, []byte("foreign final"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(post[linkAt].argv[0], args(post[linkAt], attempt)...).Run(); err == nil {
		t.Fatal("foreign final was replaced")
	}
	if got, err := os.ReadFile(final); err != nil || string(got) != "foreign final" {
		t.Fatalf("foreign final changed: %q, %v", got, err)
	}
	if err := os.Remove(final); err != nil { // Only the file created above.
		t.Fatal(err)
	}
	attempt = strings.Repeat("5", 32)
	run(mkdir, attempt)
	run(chmod, attempt)
	run(prepare, attempt)
	run(main[0], attempt)
	for _, cmd := range post {
		run(cmd, attempt)
	}
	if got, err := os.ReadFile(filepath.Join(localStage, "transaction")); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("retry did not publish exact input: %q, %v", got, err)
	}
	for _, digit := range []string{"1", "2"} {
		work := args(prepare, strings.Repeat(digit, 32))
		if got, err := os.ReadFile(work[len(work)-1]); err != nil || string(got) != "partial "+digit {
			t.Fatalf("interrupted attempt changed: %q, %v", got, err)
		}
	}
	for _, name := range []string{"transaction.pending", "transaction.retry.pending"} {
		if got, err := os.ReadFile(filepath.Join(localStage, name)); err != nil || string(got) != "preserved "+name {
			t.Fatalf("legacy pending file changed: %q, %v", got, err)
		}
	}
}

func TestReusableStageManifestMatchesProtectedBytes(t *testing.T) {
	m := fixtureManifest()
	f := hostFile{UID: 0, Regular: true, Mode: 0644, Data: mustJSON(m)}
	read := func(path string) (hostFile, error) {
		if path != filepath.Join(m.Root, "install.json") {
			return hostFile{}, fmt.Errorf("unexpected inventory path")
		}
		return f, nil
	}
	if err := reusableStageManifestMatches(m, read); err != nil {
		t.Fatal(err)
	}
	f.Data = append(append([]byte(nil), f.Data...), '\n')
	if err := reusableStageManifestMatches(m, read); err == nil {
		t.Fatal("different bytes of an equivalent manifest were accepted")
	}
	f.Data, f.Mode = mustJSON(m), 0600
	if err := reusableStageManifestMatches(m, read); err == nil {
		t.Fatal("different inventory mode was accepted")
	}
}

func TestReusableStageAdmissionRequiresTheActualHeldIntent(t *testing.T) {
	tx, _ := modelTransaction(t)
	m, c := tx.Base, identityConfig(tx.Base)
	pin := &reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	r := reusableAtomicRequest{Version: 1, Owner: m.Owner, InstallationID: m.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, Direction: "stage", StageKind: "image", StageSlot: "next-core", RequestID: strings.Repeat("a", 32)}
	held := &update.Installation{Version: 1, Binding: tx.Binding, Digest: update.InstallDigest(tx.Binding), Phase: "active"}
	boundaryCalls, inspectCalls := 0, 0
	boundary := func() error { boundaryCalls++; return nil }
	inspect := func(got signer.Config, id string) (*update.Installation, error) {
		inspectCalls++
		if got != c || id != r.IntentID {
			t.Fatal("inspection used a different signer or intent")
		}
		return held, nil
	}
	check := func(req reusableAtomicRequest, fence func() error, read func(signer.Config, string) (*update.Installation, error)) (*update.Installation, error) {
		return verifyReusableStageHeld(m, c, pin, req, "image", "next-core", fence, read, func(string) (hostFile, error) { return hostFile{}, os.ErrNotExist })
	}
	if got, err := check(r, boundary, inspect); err != nil || got != held || boundaryCalls != 2 || inspectCalls != 1 {
		t.Fatalf("exact held stage was refused: %v", err)
	}
	// Exercise the signer-side admission directly: an installed launcher can
	// bypass the owner's preflight, but may not copy an unadopted predecessor.
	publishedRead := func(path string) (hostFile, error) {
		if path == tx.dir()+"/transaction.json" {
			return hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(tx)}, nil
		}
		return hostFile{}, os.ErrNotExist
	}
	if _, err := verifyReusableStageHeld(m, c, pin, r, "image", "next-core", boundary, inspect, publishedRead); err == nil || !strings.Contains(err.Error(), "protected staging adoption") {
		t.Fatalf("direct staging did not preserve the published predecessor: %v", err)
	}
	if _, err := check(r, func() error { return errors.New("no stopped-service fence") }, inspect); err == nil {
		t.Fatal("stage was admitted without the protected fence")
	}
	if _, err := check(r, boundary, func(signer.Config, string) (*update.Installation, error) { return nil, nil }); err == nil {
		t.Fatal("stage was admitted without the actual signer hold")
	}
	foreign := *held
	foreign.Digest = strings.Repeat("f", 64)
	if _, err := check(r, boundary, func(signer.Config, string) (*update.Installation, error) { return &foreign, nil }); err == nil {
		t.Fatal("stage was admitted for a different held binding")
	}
	otherTx := tx
	otherTx.Target.CoreHash = strings.Repeat("f", 64)
	otherTx.Binding.TargetCore = otherTx.Target.CoreHash
	otherTx.Binding.Index.Binding.Files = append([]update.StageFile(nil), tx.Binding.Index.Binding.Files...)
	for n := range otherTx.Binding.Index.Binding.Files {
		if otherTx.Binding.Index.Binding.Files[n].Artifact.File == "fased-app" {
			otherTx.Binding.Index.Binding.Files[n].Artifact.SHA256 = otherTx.Target.CoreHash
		}
	}
	otherTx.Binding.Index.BindingSHA256 = update.RecoveryDigest(otherTx.Binding.Index.Binding)
	if !otherTx.valid() {
		t.Fatal("different-binding fixture is invalid")
	}
	for _, path := range []string{tx.dir() + "/transaction.json", reusableMaintenancePath(m, "active.json"), reusableMaintenancePath(m, "staging/transaction")} {
		foreignRead := func(got string) (hostFile, error) {
			if got == path {
				return hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(otherTx)}, nil
			}
			return hostFile{}, os.ErrNotExist
		}
		if _, err := verifyReusableStageHeld(m, c, pin, r, "image", "next-core", boundary, inspect, foreignRead); err == nil {
			t.Fatalf("stage accepted a different transaction for the same intent at %s", path)
		}
	}
	other := r
	other.IntentID = strings.Repeat("b", 31) + "c"
	if _, err := check(other, boundary, func(signer.Config, string) (*update.Installation, error) { return held, nil }); err == nil {
		t.Fatal("stage was admitted for another intent")
	}
	checks := 0
	if _, err := check(r, func() error {
		checks++
		if checks == 2 {
			return errors.New("fence changed")
		}
		return nil
	}, inspect); err == nil {
		t.Fatal("stage was admitted after the fence changed")
	}
}

func TestReusableStageCopyMatchesSignerHold(t *testing.T) {
	tx, installed := modelTransaction(t)
	c := signer.Config{Version: 1, Mode: signer.FixtureNetwork, SignerID: tx.Base.ID, OwnerUID: tx.Base.Owner, AppUID: tx.Base.App.UID, SignerUID: tx.Base.Signer.UID, StateDir: tx.Base.Root + "/signer-state", SocketDir: tx.Base.Root + "/signer-ipc"}
	pin := &reusableMaintenanceAuthority{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	hold := &update.Installation{Version: 1, Binding: tx.Binding, Digest: update.InstallDigest(tx.Binding), Phase: "active"}
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
	for _, item := range inputs {
		digest, size, err := reusableHeldStageExpectation(tx.Base, c, pin, hold, item.kind, item.slot)
		if err != nil || digest != item.digest || (size >= 0 && size != item.size) {
			t.Fatalf("held expectation differs for %s: %s, %d, %v", item.slot, digest, size, err)
		}
	}
	foreignConfig := c
	foreignConfig.StateDir = tx.Base.Root + "/foreign-signer-state"
	if _, _, err := reusableHeldStageExpectation(tx.Base, foreignConfig, pin, hold, "image", "next-core"); err == nil {
		t.Fatal("a different signer state directory was accepted")
	}
	stage := t.TempDir()
	invocation := strings.Repeat("a", 32)
	dir := filepath.Join(stage, ".stage-next-core-"+invocation+".d")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "copy")
	if err := os.WriteFile(path, []byte("next-core"), 0644); err != nil {
		t.Fatal(err)
	}
	check := func(h *update.Installation) error {
		return validateReusableStageCopyAt(stage, tx.Base, c, pin, h, "image", "next-core", invocation, os.Getuid(), os.Getgid())
	}
	if err := check(hold); err != nil {
		t.Fatal("exact held copy refused", err)
	}
	if err := os.WriteFile(path, []byte("wrongcore"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := check(hold); err == nil {
		t.Fatal("changed nonempty copy was accepted")
	}
	if err := os.WriteFile(path, []byte("next-core"), 0644); err != nil {
		t.Fatal(err)
	}
	foreign := *hold
	foreign.Digest = strings.Repeat("f", 64)
	if check(&foreign) == nil || check(nil) == nil {
		t.Fatal("foreign or missing signer hold was accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(artifacts, "fased-app"), path); err != nil {
		t.Fatal(err)
	}
	if err := check(hold); err == nil {
		t.Fatal("symlinked copy was accepted")
	}
}

func TestReusableStageRecoveryPreservesEveryPreexistingCopy(t *testing.T) {
	owner, group := os.Getuid(), os.Getgid()
	if owner <= 0 || group <= 0 {
		t.Skip("ordinary unprivileged owner required")
	}
	for _, tc := range []struct {
		name, pending, copied string
		marker                string
		copyExists            bool
	}{
		{name: "new empty slot"},
		{name: "preexisting linked marker", marker: "linked"},
		{name: "interrupted input", pending: "origi", marker: "linked"},
		{name: "empty interrupted copy", pending: "original input", marker: "linked", copyExists: true},
		{name: "interrupted copy", pending: "original input", copied: "original", marker: "linked", copyExists: true},
		{name: "completed copy before publication", pending: "original input", copied: "original input", marker: "linked", copyExists: true},
		{name: "unmarked output", pending: "original"},
		{name: "unmarked copy", copied: "foreign", copyExists: true},
		{name: "foreign input", pending: "different", marker: "linked"},
		{name: "foreign copy", pending: "original input", copied: "foreign", marker: "linked", copyExists: true},
		{name: "oversized copy", pending: "ori", copied: "original", marker: "linked", copyExists: true},
		{name: "foreign marker", pending: "original", marker: "foreign"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			stage, inbox := filepath.Join(parent, "stage"), filepath.Join(parent, "inbox")
			if err := os.Mkdir(stage, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(inbox, 0710); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(inbox, os.ModeSetgid|0710); err != nil {
				t.Fatal(err)
			}
			write := func(path, value string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, []byte(value), mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			source := filepath.Join(inbox, "transaction")
			pending := filepath.Join(stage, "transaction.pending")
			marker := filepath.Join(stage, "transaction.source")
			copied := filepath.Join(stage, "transaction.copy")
			write(source, "original input", 0640)
			write(pending, tc.pending, 0644)
			if tc.marker == "linked" {
				if err := os.Link(pending, marker); err != nil {
					t.Fatal(err)
				}
			} else if tc.marker == "foreign" {
				write(marker, tc.pending, 0644)
			}
			if tc.copyExists {
				write(copied, tc.copied, 0644)
			}
			err := validateStageRecoveryAt(stage, inbox, owner, group, owner, group, "meta", "transaction", strings.Repeat("a", 32))
			if err != nil {
				t.Fatalf("fresh invocation refused because of preserved legacy state: %v", err)
			}
			if got, readErr := os.ReadFile(pending); readErr != nil || string(got) != tc.pending {
				t.Fatal("pending input was changed", readErr)
			}
			if tc.copyExists {
				if got, readErr := os.ReadFile(copied); readErr != nil || string(got) != tc.copied {
					t.Fatal("partial or foreign copy was changed", readErr)
				}
			}
		})
	}
}

func TestReusableStageRetryUsesSeparatePaths(t *testing.T) {
	owner, group := os.Getuid(), os.Getgid()
	if owner <= 0 || group <= 0 {
		t.Skip("ordinary unprivileged owner required")
	}
	parent := t.TempDir()
	stage, inbox := filepath.Join(parent, "stage"), filepath.Join(parent, "inbox")
	if err := os.Mkdir(stage, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(inbox, 0710); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inbox, os.ModeSetgid|0710); err != nil {
		t.Fatal(err)
	}
	write := func(path, value string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(inbox, "transaction"), "original input", 0640)
	first := filepath.Join(stage, "transaction.pending")
	write(first, "origi", 0644)
	if err := os.Link(first, filepath.Join(stage, "transaction.source")); err != nil {
		t.Fatal(err)
	}
	retry := filepath.Join(stage, "transaction.retry.pending")
	write(retry, "", 0644)
	invocation := strings.Repeat("b", 32)
	if err := validateStageRecoveryAt(stage, inbox, owner, group, owner, group, "meta", "transaction", invocation); err != nil {
		t.Fatal("fresh retry was refused", err)
	}
	if got, err := os.ReadFile(first); err != nil || string(got) != "origi" {
		t.Fatal("original incomplete file changed", err)
	}
	if err := os.Link(retry, filepath.Join(stage, "transaction.retry.source")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(stage, ".stage-transaction-"+invocation+".d"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateStageRecoveryAt(stage, inbox, owner, group, owner, group, "meta", "transaction", invocation); err == nil {
		t.Fatal("preexisting invocation directory was accepted")
	}
	for _, invalid := range []string{"", "../foreign", strings.Repeat("A", 32), strings.Repeat("a", 31)} {
		if err := validateStageRecoveryAt(stage, inbox, owner, group, owner, group, "meta", "transaction", invalid); err == nil {
			t.Fatalf("invalid invocation accepted: %q", invalid)
		}
	}
}
