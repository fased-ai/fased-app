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
