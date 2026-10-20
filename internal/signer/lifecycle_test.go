//go:build linux

package signer

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func intentFixture(t *testing.T, v *Vault, sequence uint64) update.IntentRequest {
	t.Helper()
	probe := update.NewProbe()
	s, e := v.UpdateInventory(probe)
	if e != nil {
		t.Fatal(e)
	}
	core := update.Contract{Lifecycle: update.CurrentLifecycle(), Version: 1, Component: "core", Protocol: 1, PeerProtocol: update.Range{Min: 1, Max: 1}, Features: []string{"account-registry-v1", "fixture-proposals-v1"}, RequiresPeer: []string{"fixture-update-intents-v1"}, State: []update.StateSupport{{Name: "core-db", Versions: update.Range{Min: 3, Max: 3}, Features: []string{"accounts-v1", "owner-sessions-v1", "setup-v1", "core-pause-v1"}}}}
	c := update.CoreSnapshot{Version: 1, ProbeID: probe, BootID: update.NewProbe(), InstanceID: "disposable-core", State: update.State{Name: "core-db", Version: 3, Features: core.State[0].Features, Anchor: update.SnapshotDigest("core-fixture", probe)}}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().Unix()
	m := update.Manifest{Version: 1, Product: "fased-app", Channel: "development", Sequence: sequence, IssuedAt: now - 1, ExpiresAt: now + 600, Target: update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}}
	for _, contract := range []update.Contract{core, UpdateContract()} {
		m.Components = append(m.Components, update.Component{Version: "0.0.0-fixture.1", Artifact: update.Artifact{File: contract.Component, Size: 1, SHA256: update.SnapshotDigest("artifact", contract.Component)}, Contract: contract})
	}
	mb, _ := json.Marshal(m)
	tr := update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: now - 60, NotAfter: now + 3600}}}
	tb, _ := json.Marshal(tr)
	env, _ := json.Marshal(update.Envelope{Version: 1, KeyID: tr.Keys[0].ID, Payload: mb, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), mb...))})
	return update.IntentRequest{ID: randomID(), Core: c, Signer: *s, Manifest: env, Trust: tb}
}

func TestUpdateIntentPreservesUnknownAndSpentAcrossRestartAbort(t *testing.T) {
	dir := fixtureDir(t)
	v, _, g := configuredFixture(t, dir)
	paid := intentFor(g, 5)
	if _, e := v.ReserveFixture(paid); e != nil {
		t.Fatal(e)
	}
	if _, e := v.ExecuteFixture(paid.OperationID); e != nil {
		t.Fatal(e)
	}
	pending := intentFor(g, 7)
	if _, e := v.ReserveFixture(pending); e != nil {
		t.Fatal(e)
	}
	b, h := original(pending)
	if e := v.ledger.append(v, journalRecord{Kind: "prepare", ID: pending.OperationID, Original: b, OriginalHash: h}); e != nil {
		t.Fatal(e)
	}
	if e := v.ledger.append(v, journalRecord{Kind: "sending", ID: pending.OperationID}); e != nil {
		t.Fatal(e)
	} // fixed mock crash fixture, never actually sent
	r := intentFixture(t, v, 1)
	if r.Signer.Pending != 1 || r.Signer.Unknown != 1 || r.Signer.Spent != 5 || r.Signer.Held != 7 {
		t.Fatal("incorrect authoritative inventory")
	}
	vault, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	mock, _ := os.ReadFile(filepath.Join(dir, "mock-receipts.log"))
	ref := *v.Status().Account
	i, e := v.PrepareUpdate(r)
	if e != nil || i.State != "prepared" || !v.Status().Locked || !v.Status().UpdateHeld {
		t.Fatal(i, e)
	}
	if _, e = v.PrepareUpdate(r); e != nil {
		t.Fatal("identical retry failed", e)
	}
	changed := clone(r)
	changed.Core.State.Anchor = update.SnapshotDigest("different-core", 0)
	if _, e = v.PrepareUpdate(changed); e == nil {
		t.Fatal("existing intent rebound to different state")
	}
	conflict := r
	conflict.ID = randomID()
	if _, e = v.PrepareUpdate(conflict); e == nil {
		t.Fatal("competing update accepted")
	}
	if e = v.Unlock(fixturePassword, time.Minute); e == nil {
		t.Fatal("update unlocked fixture")
	}
	if _, e = v.ReserveFixture(intentFor(g, 1)); e == nil {
		t.Fatal("reserved while held")
	}
	if _, e = v.ExecuteFixture(pending.OperationID); e == nil {
		t.Fatal("resent original during update")
	}
	if _, e = v.ReconcileFixture(pending.OperationID); e == nil {
		t.Fatal("changed original during update")
	}
	for _, action := range []string{"fixture-update-inventory", "fixture-update-prepare", "fixture-update-abort", "fixture-update-status"} {
		if x := execute(v, Request{Action: action, Update: &r, IntentID: r.ID, ProbeID: r.Signer.ProbeID}, false); x.Error == "" {
			t.Fatal("app administered update", action)
		}
	}
	if e = v.Close(); e != nil {
		t.Fatal(e)
	}
	next := openFixture(t, dir)
	s, e := next.UpdateInventory(update.NewProbe())
	if e != nil || s.SequenceFloor != 1 || s.ActiveIntentID != r.ID || s.Spent != 5 || s.Held != 7 || s.Unknown != 1 || s.BootID == r.Signer.BootID || !next.Status().Locked {
		t.Fatal("unfinished intent lost on restart", s, e)
	}
	if _, e = next.PrepareUpdate(r); e != nil {
		t.Fatal("lost-receipt retry failed", e)
	}
	if _, e = next.AbortUpdate(r.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = next.AbortUpdate(r.ID); e != nil {
		t.Fatal("abort not idempotent", e)
	}
	s, e = next.UpdateInventory(update.NewProbe())
	if e != nil || s.SequenceFloor != 1 || s.ActiveIntentID != "" || s.Spent != 5 || s.Held != 7 || s.Unknown != 1 || !next.Status().Locked || *next.Status().Account != ref {
		t.Fatal("abort rewound state", s, e)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "vault.json")); !bytes.Equal(vault, got) {
		t.Fatal("vault changed")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "mock-receipts.log")); !bytes.Equal(mock, got) {
		t.Fatal("submission history changed")
	}
	replay := intentFixture(t, next, 1)
	if _, e = next.PrepareUpdate(replay); e == nil {
		t.Fatal("reserved sequence replayed after abort")
	}
	fresh := intentFixture(t, next, 2)
	if _, e = next.PrepareUpdate(fresh); e != nil {
		t.Fatal(e)
	}
	if _, e = next.AbortUpdate(r.ID); e != nil || next.ledger.activeUpdate != fresh.ID {
		t.Fatal("old abort released a newer intent", e)
	}
	if _, e = next.AbortUpdate(fresh.ID); e != nil {
		t.Fatal(e)
	}
	op, e := next.FixtureOperation(pending.OperationID)
	if e != nil || op.State != "sending" || !bytes.Equal(op.Original, b) {
		t.Fatal("unknown original changed", e)
	}
}

func TestUpdateSnapshotsRejectStaleBootEpochFloorAndTampering(t *testing.T) {
	for _, tc := range []string{"boot", "epoch", "floor", "head", "signature"} {
		t.Run(tc, func(t *testing.T) {
			v, _, _ := configuredFixture(t, fixtureDir(t))
			r := intentFixture(t, v, 1)
			switch tc {
			case "boot":
				r.Signer.BootID = randomID()
			case "epoch":
				r.Signer.AuthoritySHA256 = update.SnapshotDigest("wrong-epoch", 0)
			case "floor":
				r.Signer.SequenceFloor = 99
			case "head":
				r.Signer.States[1].Anchor = update.SnapshotDigest("wrong-head", 0)
			case "signature":
				r.Manifest = bytes.Replace(r.Manifest, []byte(`"sequence":"1"`), []byte(`"sequence":"2"`), 1)
			}
			seq := v.ledger.main.seq
			if _, e := v.PrepareUpdate(r); e == nil || v.ledger.main.seq != seq || v.ledger.updateFloor != 0 {
				t.Fatal("stale or altered update changed state", e)
			}
		})
	}
}

func TestUpdateIntentConcurrentPreparationHasOneWinner(t *testing.T) {
	v, _, _ := configuredFixture(t, fixtureDir(t))
	r := intentFixture(t, v, 1)
	seq := v.ledger.main.seq
	var wg sync.WaitGroup
	wins := make(chan string, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := r
			q.ID = randomID()
			if _, e := v.PrepareUpdate(q); e == nil {
				wins <- q.ID
			}
		}()
	}
	wg.Wait()
	close(wins)
	if len(wins) != 1 || v.ledger.main.seq != seq+1 || v.ledger.updateFloor != 1 {
		t.Fatal("not exclusive", len(wins))
	}
}

func TestUpdateUncertainDurableAppendRecoversExclusiveSlot(t *testing.T) {
	dir := fixtureDir(t)
	v, _, _ := configuredFixture(t, dir)
	r := intentFixture(t, v, 1)
	v.store.crash = func(p string) error {
		if p == "journal:update-prepare:durable" {
			return errors.New("injected lost response")
		}
		return nil
	}
	if _, e := v.PrepareUpdate(r); e == nil {
		t.Fatal("failure not injected")
	}
	if _, e := v.UpdateInventory(randomID()); e == nil {
		t.Fatal("uncertain storage not held")
	}
	_ = v.Close()
	next := openFixture(t, dir)
	s, e := next.UpdateInventory(randomID())
	if e != nil || s.SequenceFloor != 1 || s.ActiveIntentID != r.ID {
		t.Fatal("durable intent not recovered", e)
	}
	if _, e = next.AbortUpdate(r.ID); e != nil {
		t.Fatal(e)
	}
	_ = next.Close()
	path := filepath.Join(dir, "operations.log")
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.WriteString(`{"version":`)
	_ = f.Close()
	if e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(path)
	if bad, e := Open(dir, "11111111111111111111111111111111"); e == nil {
		_ = bad.Close()
		t.Fatal("torn lifecycle history accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("torn history truncated")
	}
}

func TestUpdateKeyOnlyRecoveryCannotResetFloorAuthority(t *testing.T) {
	v, _, _ := configuredFixture(t, fixtureDir(t))
	backup, e := v.Backup(backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	restored := openFixture(t, fixtureDir(t))
	if e = restored.Restore(backup, backupPassword, fixturePassword); e != nil {
		t.Fatal(e)
	}
	r := intentFixture(t, restored, 1)
	if !r.Signer.RecoveryHold {
		t.Fatal("missing history not held")
	}
	if _, e = restored.PrepareUpdate(r); e == nil {
		t.Fatal("key-only restore established missing update history")
	}
}

func TestUpdateUncertainAbortRetainsFloorAfterRestart(t *testing.T) {
	dir := fixtureDir(t)
	v, _, _ := configuredFixture(t, dir)
	r := intentFixture(t, v, 1)
	if _, e := v.PrepareUpdate(r); e != nil {
		t.Fatal(e)
	}
	v.store.crash = func(p string) error {
		if p == "journal:update-abort:durable" {
			return errors.New("injected lost abort response")
		}
		return nil
	}
	if _, e := v.AbortUpdate(r.ID); e == nil {
		t.Fatal("abort failure not injected")
	}
	if !v.Status().Locked || !v.Status().UpdateHeld {
		t.Fatal("uncertain abort not held")
	}
	_ = v.Close()
	next := openFixture(t, dir)
	s, e := next.UpdateInventory(randomID())
	if e != nil || s.ActiveIntentID != "" || s.SequenceFloor != 1 || !next.Status().Locked {
		t.Fatal("abort recovery rewound or resumed", e)
	}
}
