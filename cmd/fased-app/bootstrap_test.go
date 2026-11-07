//go:build linux

package main

import (
	"bytes"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func TestBootstrapReviewCancellationDoesNotPrepare(t *testing.T) {
	tx := custodyinstall.BootstrapTransaction{Version: 2, ID: strings.Repeat("a", 32)}
	for _, choice := range []string{"CANCEL\n", "BOOTSTRAP " + strings.Repeat("b", 64) + "\n", "", "BOOTSTRAP " + tx.Digest() + "\n"} {
		t.Run(choice, func(t *testing.T) {
			r, w, e := os.Pipe()
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			_, _ = io.WriteString(w, choice)
			_ = w.Close()
			dir := t.TempDir()
			marker := filepath.Join(dir, "transaction.json")
			called := false
			err := finishBootstrapReview(tx, true, r, &bytes.Buffer{}, func() error { called = true; return os.WriteFile(marker, []byte("prepared"), 0600) })
			approved := choice == "BOOTSTRAP "+tx.Digest()+"\n"
			if (err == nil) != approved || called != approved {
				t.Fatal("review did not gate preparation", err)
			}
			_, stat := os.Stat(marker)
			if !approved && !os.IsNotExist(stat) {
				t.Fatal("cancellation wrote transaction")
			}
		})
	}
}

func TestBootstrapRequiresExplicitProtectedInputs(t *testing.T) {
	for _, args := range [][]string{{"custody", "bootstrap"}, {"owner-bootstrap"}, {"custody", "bootstrap", "--disposable-fixture", "--prepare-host", "--intent-id", "invalid"}} {
		if e := run(args, nil, io.Discard, io.Discard); e == nil {
			t.Fatal("unprotected bootstrap accepted", args)
		}
	}
}

func TestBootstrapPredecessorInventoryRequiresPairedReadOnlyEvidence(t *testing.T) {
	p := update.NewProbe()
	c := update.CoreSnapshot{Version: 1, ProbeID: p, BootID: update.NewProbe(), InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: 3, Features: core.UpdateContract().State[0].Features, Anchor: update.SnapshotDigest("core", 1)}}
	s := update.SignerSnapshot{Version: 1, ProbeID: p, BootID: update.NewProbe(), AuthoritySHA256: update.SnapshotDigest("authority", 1)}
	for _, state := range signer.UpdateContract().State {
		s.States = append(s.States, update.State{Name: state.Name, Version: state.Versions.Min, Features: state.Features, Anchor: update.SnapshotDigest("state", state.Name)})
	}
	r := map[string]any{"core": c, "signer": s, "installAllowed": false, "financialExecution": false}
	if _, _, e := decodeBootstrapInventory(mustJSON(r)); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"missing-signer", "grant", "challenge"} {
		q := map[string]any{"core": c, "signer": s, "installAllowed": false, "financialExecution": false}
		switch bad {
		case "missing-signer":
			q["signer"] = nil
		case "grant":
			q["financialExecution"] = true
		case "challenge":
			copy := s
			copy.ProbeID = update.NewProbe()
			q["signer"] = copy
		}
		if _, _, e := decodeBootstrapInventory(mustJSON(q)); e == nil {
			t.Fatal("unsafe inventory accepted", bad)
		}
	}
}
