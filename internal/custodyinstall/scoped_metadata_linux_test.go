//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestScopedUpdateMetadataUsesApprovedSource(t *testing.T) {
	owner := os.Getuid()
	id := strings.Repeat("a", 32)
	intent := strings.Repeat("b", 32)
	data := []byte(`{"version":1,"phase":"active"}`)
	root := Root(owner)
	source := ApprovedExecutablePath(owner, data)
	for _, target := range []string{root + "/update-active.json", root + "/updates/" + intent + "/transaction.json", root + "/updates/" + intent + "/healthy-held.json", root + "/bootstraps/" + intent + "/repair-source.json", root + "/.replace-" + id + "-install.json-" + sum(data)} {
		h := &memoryHost{files: map[string]hostFile{source: {Data: bytes.Clone(data), UID: 0, Mode: 0444, Regular: true}}}
		used := false
		run := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
			if args[0] == "/usr/bin/install" {
				used = len(input) == 0 && args[len(args)-2] == source
			}
			return h.run(ctx, input, args...)
		}
		if err := publish(t.Context(), target, id, data, 0644, h.read, run); err != nil || !used || !known(h.files[target], data, 0644) {
			t.Fatalf("metadata source not bound for %s: %v", target, err)
		}
		h = &memoryHost{files: map[string]hostFile{}}
		reject := func(string, string, int64) error { return errors.New("approved metadata changed") }
		if err := publish(t.Context(), target, id, data, 0644, h.read, h.run, reject); err == nil || h.calls != 0 {
			t.Fatal("changed metadata reached privileged write", target)
		}
	}
	for _, foreign := range []string{Root(owner+1) + "/update-active.json", root + "/updates/" + intent + "/../held.json", root + "/.replace-" + id + "-install.json-" + strings.Repeat("0", 64), root + "/identity.json"} {
		if scopedUpdateMetadata(foreign, id, owner, data) {
			t.Fatal("foreign metadata destination accepted", foreign)
		}
	}
}

func TestScopedOrdinaryRepairInterruptionPreservesHistory(t *testing.T) {
	tx, dir, o, c := ordinaryRepairModel(t)
	retained, err := os.ReadFile(dir + "/updates/" + tx.ID + "/transaction.json")
	if err != nil {
		t.Fatal(err)
	}
	history, err := os.ReadFile(dir + "/financial-history")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string][]byte{}
	add := func(b []byte) { allowed[sum(b)] = bytes.Clone(b) }
	for _, phase := range []string{"ordinary-stopped", "ordinary-trial-verified", "forward-only", "core-replaced", "pair-replaced", "inventory-replaced", "started", "ordinary-healthy", "completed"} {
		add(mustJSON(phaseRecord{1, tx.Digest(), phase}))
	}
	add(mustJSON(phaseRecord{1, tx.Digest(), tx.ID}))
	add(mustJSON(phaseRecord{1, tx.Digest(), sum([]byte("preserved raw sources"))}))
	for _, b := range [][]byte{[]byte("repair-core"), []byte("repair-signer"), mustJSON(tx.Target), mustJSON(installerIntent(tx.Target)), readyRecord(tx.Target)} {
		add(b)
	}
	denied := sum(mustJSON(phaseRecord{1, tx.Digest(), "forward-only"}))
	o.verify = func(path, digest string, size int64) error {
		b, ok := allowed[digest]
		if !ok || digest == denied || path != ApprovedExecutablePath(os.Getuid(), b) || size != int64(len(b)) {
			return errors.New("approved source unavailable or changed")
		}
		return nil
	}
	originalRun := o.run
	o.run = func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if args[0] == "/usr/bin/install" && len(args) > 2 && strings.HasPrefix(args[len(args)-2], "/var/lib/fased-app-approved-") {
			if len(input) != 0 {
				t.Fatal("scoped input supplied through stdin")
			}
			b, ok := allowed[filepath.Base(args[len(args)-2])]
			if !ok {
				return nil, errors.New("unknown approved source")
			}
			input = bytes.Clone(b)
		}
		return originalRun(ctx, input, args...)
	}
	if _, err = runOrdinaryBootstrap(t.Context(), tx, o, c); err == nil {
		t.Fatal("missing approved forward fence was accepted")
	}
	if _, err = os.Stat(dir + "/bootstraps/" + tx.ID + "/forward-only.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("forward fence was written without its approved bytes", err)
	}
	denied = ""
	o.checkpoint = func(phase string) error {
		if phase == "core-replaced" {
			return syscall.ENOSPC
		}
		return nil
	}
	if _, err = runOrdinaryBootstrap(t.Context(), tx, o, c); !errors.Is(err, syscall.ENOSPC) {
		t.Fatal("post-fence interruption not reached", err)
	}
	o.checkpoint = nil
	result, err := runOrdinaryBootstrap(t.Context(), tx, o, c)
	if err != nil || result.Phase != "reader-repaired" || result.FinancialExecution || result.InstalledSequenceCommitted {
		t.Fatal("scoped repair did not recover", result, err)
	}
	after, err := os.ReadFile(dir + "/updates/" + tx.ID + "/transaction.json")
	if err != nil || !bytes.Equal(after, retained) {
		t.Fatal("staged intent changed", err)
	}
	after, err = os.ReadFile(dir + "/financial-history")
	if err != nil || !bytes.Equal(after, history) || !tx.Signer.RecoveryHold {
		t.Fatal("history or hold changed", err)
	}
}

func TestScopedPairedUpdateRefusedRollbackRetainsHold(t *testing.T) {
	tx, dir := modelTransaction(t)
	o := modelOps(t, tx, dir)
	approval := migrationRunnerApproval{Version: 1, Owner: tx.Base.Owner, InstallationID: tx.Base.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, BindingDigest: update.InstallDigest(tx.Binding), PreviousCore: tx.Base.CoreHash, TargetCore: tx.Target.CoreHash, TargetSigner: tx.Target.SignerHash, RunnerSHA256: strings.Repeat("c", 64), GuardSHA256: strings.Repeat("d", 64)}
	marker := hostFile{UID: 0, Mode: 0644, Regular: true, Data: mustJSON(approval)}
	runner, err := selectMigrationRunner(tx, func(p string) (hostFile, error) {
		if p == tx.dir()+"/migration-runner.json" {
			return marker, nil
		}
		return hostFile{}, os.ErrNotExist
	}, func(int) (bool, error) { return true, nil }, func(p, digest string) (int64, error) {
		if p != tx.dir()+"/migration-runner" || digest != approval.RunnerSHA256 {
			return 0, errors.New("wrong migration reader")
		}
		return 123, nil
	}, func(p, digest string, size int64) error {
		if digest != approval.RunnerSHA256 || size != 123 {
			return errors.New("wrong migration approval")
		}
		return nil
	}, func(path string, got InstallTransaction) error {
		if path != tx.dir()+"/migration-runner" || update.InstallDigest(got.Binding) != update.InstallDigest(tx.Binding) {
			return errors.New("wrong migration contract probe")
		}
		return nil
	})
	if err != nil || runner != tx.dir()+"/migration-runner" {
		t.Fatal("independent migration runner was not selected", err)
	}
	history, err := os.ReadFile(dir + "/financial-history")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string][]byte{}
	add := func(b []byte) { allowed[sum(b)] = bytes.Clone(b) }
	digest := update.InstallDigest(tx.Binding)
	for _, phase := range []string{"stopped", "core-replaced", "pair-replaced", "inventory-replaced", "started", "healthy-held", "rollback", "rollback-stopped", "rollback-core-replaced", "rollback-pair-replaced", "rollback-inventory-replaced", "rollback-started", "rolled-back-held"} {
		add(mustJSON(phaseRecord{1, digest, phase}))
	}
	add(mustJSON(phaseRecord{1, digest, tx.Binding.Index.Binding.Pause.Request.IntentID}))
	add(mustJSON(update.Installation{Version: 1, Binding: tx.Binding, Digest: digest, Phase: "active"}))
	for _, b := range [][]byte{[]byte("old-core"), []byte("old-signer"), []byte("next-core"), []byte("next-signer"), mustJSON(tx.Base), mustJSON(tx.Target)} {
		add(b)
	}
	denied := sum(mustJSON(phaseRecord{1, digest, "rollback"}))
	o.verify = func(path, d string, size int64) error {
		b, ok := allowed[d]
		if !ok || d == denied || path != ApprovedExecutablePath(os.Getuid(), b) || size != int64(len(b)) {
			return errors.New("approved input unavailable")
		}
		return nil
	}
	originalRun := o.run
	o.run = func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if args[0] == "/usr/bin/install" && len(args) > 2 && strings.HasPrefix(args[len(args)-2], "/var/lib/fased-app-approved-") {
			if len(input) != 0 {
				t.Fatal("scoped input supplied through stdin")
			}
			b, ok := allowed[filepath.Base(args[len(args)-2])]
			if !ok {
				return nil, errors.New("unknown approved input")
			}
			input = bytes.Clone(b)
		}
		return originalRun(ctx, input, args...)
	}
	originalHealth := o.health
	o.health = func(ctx context.Context, m Manifest) error {
		if m.CoreHash == tx.Target.CoreHash {
			return errors.New("simulated target health failure")
		}
		return originalHealth(ctx, m)
	}
	if _, err = runTransaction(t.Context(), tx, false, o); err == nil {
		t.Fatal("missing approved rollback record accepted")
	}
	state, err := o.state(t.Context())
	if err != nil || state.Phase != "active" || state.Digest != digest {
		t.Fatal("signer hold changed after refusal", err)
	}
	denied = ""
	result, err := runTransaction(t.Context(), tx, false, o)
	if err != nil || result == nil || result.Phase != "rolled-back-held" || result.FinancialExecution || result.InstalledSequenceCommitted {
		t.Fatal("rollback did not recover same held transaction", result, err)
	}
	after, err := os.ReadFile(dir + "/financial-history")
	if err != nil || !bytes.Equal(after, history) {
		t.Fatal("financial history changed", err)
	}
}
