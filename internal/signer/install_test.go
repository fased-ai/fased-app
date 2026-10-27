//go:build linux

package signer

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func installationFixture(t *testing.T, v *Vault) (update.Intent, update.InstallBinding) {
	t.Helper()
	r := intentFixture(t, v, 1)
	var env update.Envelope
	var m update.Manifest
	json.Unmarshal(r.Manifest, &env)
	json.Unmarshal(env.Payload, &m)
	data := []byte("disposable compatible binary")
	for n := range m.Components {
		name := "fased-app"
		if m.Components[n].Contract.Component == "signer" {
			name = "fased-signer"
		}
		m.Components[n].Artifact = update.Artifact{File: name, Size: int64(len(data)), SHA256: metadataFixtureHash(data)}
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().Unix()
	r.Trust, _ = json.Marshal(update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: now - 60, NotAfter: now + 600}}})
	payload, _ := json.Marshal(m)
	r.Manifest, _ = json.Marshal(update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: payload, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), payload...))})
	i, e := v.PrepareUpdate(r)
	if e != nil {
		t.Fatal(e)
	}
	writeStage(t, v, *i, data)
	if _, e = v.StageUpdate("fixture-update-stage-finish", stageRequest(*i)); e != nil {
		t.Fatal(e)
	}
	i, e = v.UpdateIntent(i.ID)
	if e != nil {
		t.Fatal(e)
	}
	p := installationPreflight(t, v, *i)
	binding, e := update.BackupBinding(p)
	if e != nil {
		t.Fatal(e)
	}
	index := update.RecoveryIndex{Version: 1, Phase: "metadata-backed-up", Binding: binding, BindingSHA256: update.RecoveryDigest(binding), Snapshot: update.MetadataSnapshot{File: "metadata.sqlite", Size: 1, SHA256: update.SnapshotDigest("metadata", 1)}}
	b := update.InstallBinding{Contracts: i.Contracts, Version: 1, Index: index, InstallationID: v.Status().Account.SignerID, PreviousCore: update.SnapshotDigest("old-core", 1), PreviousSigner: update.SnapshotDigest("old-signer", 1), PreviousManifest: update.SnapshotDigest("old-manifest", 1), TargetCore: i.Artifacts[0].SHA256, TargetSigner: i.Artifacts[1].SHA256}
	return *i, b
}
func metadataFixtureHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func installationPreflight(t *testing.T, v *Vault, i update.Intent) update.Preflight {
	t.Helper()
	p := update.NewProbe()
	s, e := v.UpdateInventory(p)
	if e != nil {
		t.Fatal(e)
	}
	want := update.PauseFor(i)
	core := i.Core
	core.BootID = update.NewProbe()
	core.ProbeID = p
	r := update.PreflightRequest{IntentID: i.ID, Sequence: i.Sequence, ManifestSHA256: i.ManifestSHA256, ProbeID: p, Pause: update.CorePauseReceipt{Version: 1, State: "paused", PauseID: i.ID, Request: &want, Core: core}, ExpectedSigner: *s}
	proof, e := v.PreflightUpdate(r)
	if e != nil {
		t.Fatal(e)
	}
	return *proof
}
func installCommand(t *testing.T, v *Vault, i update.Intent, b update.InstallBinding, phase string) update.InstallRequest {
	t.Helper()
	p := installationPreflight(t, v, i)
	return update.InstallRequest{Action: phase, IntentID: i.ID, Binding: &b, Preflight: &update.PreflightRequest{IntentID: i.ID, Sequence: i.Sequence, ManifestSHA256: i.ManifestSHA256, ProbeID: p.Pause.Core.ProbeID, Pause: p.Pause, ExpectedSigner: p.Signer}}
}
func TestInstallationHoldRestartRollbackAndFinancialHistory(t *testing.T) {
	dir := fixtureDir(t)
	v, _, g := configuredFixture(t, dir)
	paid := intentFor(g, 5)
	v.ReserveFixture(paid)
	v.ExecuteFixture(paid.OperationID)
	unknown := intentFor(g, 7)
	v.ReserveFixture(unknown)
	original, h := original(unknown)
	v.ledger.append(v, journalRecord{Kind: "prepare", ID: unknown.OperationID, Original: original, OriginalHash: h})
	v.ledger.append(v, journalRecord{Kind: "sending", ID: unknown.OperationID})
	i, b := installationFixture(t, v)
	vault, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	mock, _ := os.ReadFile(filepath.Join(dir, "mock-receipts.log"))
	if out := execute(v, Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "active"}}, false); out.Error != "owner administration denied" {
		t.Fatal(out)
	}
	if _, e := v.Installation(installCommand(t, v, i, b, "active")); e != nil {
		t.Fatal(e)
	}
	if _, e := v.AbortUpdate(i.ID); e == nil {
		t.Fatal("aborted during installation")
	}
	if e := v.Unlock(fixturePassword, time.Minute); e == nil {
		t.Fatal("installation unlocked")
	}
	v.Close()
	var e error
	v, e = Open(dir, b.InstallationID)
	if e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	if _, e = v.AbortUpdate(i.ID); e == nil {
		t.Fatal("restart lost installation hold")
	}
	if _, e = v.Installation(installCommand(t, v, i, b, "healthy-held")); e != nil {
		t.Fatal(e)
	}
	if _, e = v.AbortUpdate(i.ID); e == nil {
		t.Fatal("healthy trial falsely permitted abort")
	}
	if _, e = v.Installation(installCommand(t, v, i, b, "rolled-back-held")); e != nil {
		t.Fatal(e)
	}
	if _, e = v.AbortUpdate(i.ID); e != nil {
		t.Fatal(e)
	}
	s, e := v.UpdateInventory(update.NewProbe())
	if e != nil || s.Spent != 5 || s.Held != 7 || s.Unknown != 1 || s.SequenceFloor != 1 || s.ActiveIntentID != "" {
		t.Fatal(s, e)
	}
	for name, want := range map[string][]byte{"vault.json": vault, "mock-receipts.log": mock} {
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if !bytes.Equal(got, want) {
			t.Fatal("financial file changed", name)
		}
	}
}
func TestInstallationHoldPersistenceFaultAndConcurrency(t *testing.T) {
	for _, step := range []string{"journal:update-install:before", "journal:update-install:written", "journal:update-install:durable"} {
		t.Run(step, func(t *testing.T) {
			dir := fixtureDir(t)
			v, _, _ := configuredFixture(t, dir)
			i, b := installationFixture(t, v)
			r := installCommand(t, v, i, b, "active")
			v.store.crash = func(at string) error {
				if at == step {
					return syscall.ENOSPC
				}
				return nil
			}
			if _, e := v.Installation(r); e == nil {
				t.Fatal("missing persistence fault")
			}
			v.Close()
			var e error
			v, e = Open(dir, b.InstallationID)
			if e != nil {
				t.Fatal(e)
			}
			defer v.Close()
			r = installCommand(t, v, i, b, "active")
			var wg sync.WaitGroup
			success := 0
			var mu sync.Mutex
			for n := 0; n < 3; n++ {
				wg.Go(func() {
					if _, e := v.Installation(r); e == nil {
						mu.Lock()
						success++
						mu.Unlock()
					}
				})
			}
			wg.Wait()
			if success == 0 {
				t.Fatal("no holder recovered")
			}
			if _, e = v.AbortUpdate(i.ID); e == nil {
				t.Fatal("hold not durable")
			}
		})
	}
}

func TestInstallationRejectsRebindingAndExpiredAdmission(t *testing.T) {
	dir := fixtureDir(t)
	v, _, _ := configuredFixture(t, dir)
	defer v.Close()
	i, b := installationFixture(t, v)
	for _, kind := range []string{"contracts", "floor", "authority", "pause", "instance", "stale-boot"} {
		t.Run(kind, func(t *testing.T) {
			bad := clone(b)
			r := installCommand(t, v, i, b, "active")
			switch kind {
			case "contracts":
				bad.Contracts[0].Features = append(bad.Contracts[0].Features, "foreign-feature")
			case "floor":
				bad.Index.Binding.Signer.SequenceFloor++
			case "authority":
				bad.Index.Binding.Signer.AuthoritySHA256 = update.SnapshotDigest("foreign", 1)
			case "pause":
				bad.Index.Binding.Pause.PauseID = update.NewProbe()
			case "instance":
				bad.InstallationID = update.NewProbe()
			case "stale-boot":
				r.Preflight.ExpectedSigner.BootID = update.NewProbe()
			}
			bad.Index.BindingSHA256 = update.RecoveryDigest(bad.Index.Binding)
			r.Binding = &bad
			if _, e := v.Installation(r); e == nil {
				t.Fatal("unbound installation admitted")
			}
		})
	}
	v.now = func() time.Time { return time.Unix(i.ManifestExpiresAt+1, 0) }
	if _, e := v.Installation(installCommand(t, v, i, b, "active")); e == nil {
		t.Fatal("expired initial installation admitted")
	}
	v.now = time.Now
	if _, e := v.Installation(installCommand(t, v, i, b, "active")); e != nil {
		t.Fatal(e)
	}
	v.now = func() time.Time { return time.Unix(i.ManifestExpiresAt+1, 0) }
	if _, e := v.Installation(installCommand(t, v, i, b, "rolled-back-held")); e != nil {
		t.Fatal("expiry stranded existing recovery hold", e)
	}
}
