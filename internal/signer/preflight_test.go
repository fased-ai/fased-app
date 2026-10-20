//go:build linux

package signer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func pausedFixture(t *testing.T, v *Vault) (update.Intent, update.PreflightRequest) {
	t.Helper()
	i, data := stageFixture(t, v)
	writeStage(t, v, i, data)
	if _, e := v.StageUpdate("fixture-update-stage-finish", stageRequest(i)); e != nil {
		t.Fatal(e)
	}
	p := update.NewProbe()
	s, e := v.UpdateInventory(p)
	if e != nil {
		t.Fatal(e)
	}
	expected := update.PauseFor(i)
	c := i.Core
	c.ProbeID = p
	paused := update.CorePauseReceipt{Version: 1, State: "paused", PauseID: update.NewProbe(), Request: &expected, Core: c}
	return i, update.PreflightRequest{IntentID: i.ID, Sequence: i.Sequence, ManifestSHA256: i.ManifestSHA256, ProbeID: p, Pause: paused, ExpectedSigner: *s}
}

func TestPreflightPreservesAuthoritativeHistoryAndHold(t *testing.T) {
	dir := fixtureDir(t)
	v, _, g := configuredFixture(t, dir)
	paid := intentFor(g, 5)
	if _, e := v.ReserveFixture(paid); e != nil {
		t.Fatal(e)
	}
	if _, e := v.ExecuteFixture(paid.OperationID); e != nil {
		t.Fatal(e)
	}
	unknown := intentFor(g, 7)
	if _, e := v.ReserveFixture(unknown); e != nil {
		t.Fatal(e)
	}
	original, h := original(unknown)
	if e := v.ledger.append(v, journalRecord{Kind: "prepare", ID: unknown.OperationID, Original: original, OriginalHash: h}); e != nil {
		t.Fatal(e)
	}
	if e := v.ledger.append(v, journalRecord{Kind: "sending", ID: unknown.OperationID}); e != nil {
		t.Fatal(e)
	}
	i, r := pausedFixture(t, v)
	before := map[string][]byte{}
	for _, name := range []string{"vault.json", "operations.log", "mock-receipts.log"} {
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		before[name] = b
	}
	for n := 0; n < 2; n++ {
		p, e := v.PreflightUpdate(r)
		if e != nil || !p.Verified || p.InstallAllowed || p.ResumeAllowed || p.FinancialExecution || p.Signer.Spent != 5 || p.Signer.Held != 7 || p.Signer.Unknown != 1 || p.Signer.SequenceFloor != 1 || !update.IDOK(p.Pause.PauseID) {
			t.Fatal("invalid preflight", p, e)
		}
	}
	for name, want := range before {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); !bytes.Equal(got, want) {
			t.Fatal("preflight wrote financial history", name)
		}
	}
	if e := v.Unlock(fixturePassword, time.Minute); e == nil {
		t.Fatal("preflight unlocked")
	}
	if out := execute(v, Request{Action: "fixture-update-preflight", Preflight: &r}, false); out.Error != "owner administration denied" {
		t.Fatal("app obtained preflight", out.Error)
	}
	v.Close()
	v = openFixture(t, dir)
	if _, e := v.PreflightUpdate(r); e == nil {
		t.Fatal("stale signer boot accepted")
	}
	fresh, e := v.UpdateInventory(r.ProbeID)
	if e != nil {
		t.Fatal(e)
	}
	r.ExpectedSigner = *fresh
	r.Pause.Core.BootID = update.NewProbe() // Durable core pause can report a fresh boot.
	if _, e = v.PreflightUpdate(r); e != nil {
		t.Fatal("held restart could not revalidate", e)
	}
	if _, e = v.AbortUpdate(i.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = v.PreflightUpdate(r); e == nil {
		t.Fatal("aborted intent preflight accepted")
	}
	op, e := v.FixtureOperation(unknown.OperationID)
	if e != nil || !bytes.Equal(op.Original, original) || op.State != "sending" {
		t.Fatal("unknown original lost", e)
	}
	requireSummary(t, v, 5, 7)
}

func TestPreflightRejectsStalePauseStateAndArtifactBytes(t *testing.T) {
	for _, tc := range []string{"unpaused", "pause-id", "intent", "digest", "metadata", "challenge", "floor", "signer-history", "corrupt-bytes"} {
		t.Run(tc, func(t *testing.T) {
			dir := fixtureDir(t)
			v, _, _ := configuredFixture(t, dir)
			i, r := pausedFixture(t, v)
			switch tc {
			case "unpaused":
				r.Pause.State = "released"
			case "pause-id":
				r.Pause.PauseID = "wrong"
			case "intent":
				r.Pause.Request.IntentID = update.NewProbe()
			case "digest":
				r.ManifestSHA256 = update.SnapshotDigest("different", 0)
			case "metadata":
				r.Pause.Core.State.Anchor = update.SnapshotDigest("changed", 0)
			case "challenge":
				r.Pause.Core.ProbeID = update.NewProbe()
			case "floor":
				r.Sequence++
			case "signer-history":
				r.ExpectedSigner.States[1].Anchor = update.SnapshotDigest("stale", 0)
			case "corrupt-bytes":
				if e := os.WriteFile(filepath.Join(dir, "update-"+i.ID, "0.artifact"), []byte("wrong"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			seq := v.ledger.main.seq
			if _, e := v.PreflightUpdate(r); e == nil || v.ledger.main.seq != seq || !v.Status().Locked || !v.Status().UpdateHeld {
				t.Fatal("unsafe preflight changed hold", e)
			}
		})
	}
}

func TestLegacyLifecycleStagingJournalRemainsReadable(t *testing.T) {
	for _, entry := range []int{1, 2, 3} {
		t.Run(string(rune('0'+entry)), func(t *testing.T) {
			dir := fixtureDir(t)
			v, _, _ := configuredFixture(t, dir)
			r := intentFixture(t, v, 1)
			c := update.CurrentLifecycle()
			c.Entrypoint = entry
			if entry == 1 {
				c.Features = c.Features[:3]
			} else if entry == 2 {
				c.Features = c.Features[:4]
			} else {
				c.Features = c.Features[:5]
			}
			i := update.Intent{Version: 1, ID: r.ID, Sequence: 1, ManifestSHA256: update.SnapshotDigest("legacy-manifest", 0), Core: r.Core, Signer: r.Signer, CreatedAt: time.Now().Unix(), State: "prepared", Lifecycle: c, Artifacts: []update.Artifact{{File: "core", Size: 1, SHA256: update.SnapshotDigest("old-core", 0)}, {File: "signer", Size: 1, SHA256: update.SnapshotDigest("old-signer", 0)}}}
			for n := range i.Signer.States {
				if i.Signer.States[n].Name == "operations" {
					features := []string{}
					for _, f := range i.Signer.States[n].Features {
						if f != "install-completion-v1" && (entry >= 3 || f != "install-controller-v1") && (entry >= 2 || f != "lifecycle-v2-v1") {
							features = append(features, f)
						}
					}
					i.Signer.States[n].Features = features
				}
			}
			if entry == 3 {
				i.ManifestExpiresAt = i.CreatedAt + 600
			}
			if entry == 1 {
				i.Core.State.Version = 2
				i.Core.State.Features = i.Core.State.Features[:3]
			}
			if e := v.ledger.append(v, journalRecord{Kind: "update-prepare", Update: &i, At: i.CreatedAt}); e != nil {
				t.Fatal(e)
			}
			if e := v.ledger.append(v, journalRecord{Kind: "update-staged", ID: i.ID, OriginalHash: update.StageDigest(i)}); e != nil {
				t.Fatal(e)
			}
			v.Close()
			v = openFixture(t, dir)
			got, e := v.UpdateIntent(i.ID)
			if e != nil || got.State != "staged" || got.Sequence != 1 {
				t.Fatal("legacy history stranded", e)
			}
			if _, e = v.AbortUpdate(i.ID); e != nil || !v.Status().Locked {
				t.Fatal("legacy intent cannot safely abort", e)
			}

		})
	}
}
