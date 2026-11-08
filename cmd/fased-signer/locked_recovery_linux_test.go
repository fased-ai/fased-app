//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestLockedRecoveryCLIAndNativeKeepExclusiveIntent(t *testing.T) {
	src, c := windowVault(t, "11111111111111111111111111111111")
	if e := src.Create(flowUnlock); e != nil {
		t.Fatal(e)
	}
	if e := src.Unlock(flowUnlock, time.Minute); e != nil {
		t.Fatal(e)
	}
	backup, e := src.BackupHistory(flowBackup)
	if e != nil {
		t.Fatal(e)
	}
	src.Close()
	dir := ownerFixture(t)
	v, e := signer.OpenControlledOwnerCustody(dir, c.SignerID, "devnet")
	if e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	if e = v.RestoreHistory(backup, flowBackup, flowUnlock, signer.BackupFingerprint(backup)); e != nil {
		t.Fatal(e)
	}
	probe := update.NewProbe()
	snap, e := v.UpdateInventory(probe)
	if e != nil {
		t.Fatal(e)
	}
	cc := core.OrdinaryUpdateContract()
	sc := signer.OrdinaryUpdateContract()
	current := update.CoreSnapshot{Version: 1, ProbeID: probe, BootID: update.NewProbe(), InstanceID: windowInput("recover", update.NewProbe()).Intent.InstanceID, State: update.State{Name: "core-db", Version: core.StateSchema, Features: cc.State[0].Features, Anchor: update.SnapshotDigest("owner-fixture", 1)}}
	now := time.Now().Unix()
	manifest := update.Manifest{Version: 1, Product: "fased-app", Channel: "development", Sequence: 2, IssuedAt: now - 1, ExpiresAt: now + 600, Target: update.Target{OS: "linux", Arch: "amd64"}}
	for _, contract := range []update.Contract{cc, sc} {
		file := "fased-app"
		if contract.Component == "signer" {
			file = "fased-signer"
		}
		manifest.Components = append(manifest.Components, update.Component{Version: "0.0.0-fixture", Contract: contract, Artifact: update.Artifact{File: file, Size: 1, SHA256: update.SnapshotDigest("artifact", file)}})
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	payload, _ := json.Marshal(manifest)
	envelope, _ := json.Marshal(update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: payload, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), payload...))})
	trust, _ := json.Marshal(update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: now - 30, NotAfter: now + 700}}})
	intent, e := v.PrepareUpdate(update.IntentRequest{ID: update.NewProbe(), Core: current, Signer: *snap, Manifest: envelope, Trust: trust})
	if e != nil {
		t.Fatal(e)
	}
	before, e := v.UpdateInventory(update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	keyBytes, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	history, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
	call := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		switch r.Action {
		case "status", "lock", "verify-backup":
		default:
			t.Fatal("recovery attempted mutation/unlock", r.Action)
		}
		return flowCall(v)(ctx, r)
	}
	file := filepath.Join(ownerFixture(t), "original.backup.json")
	if e = os.WriteFile(file, backup, 0600); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = conductFlow(t.Context(), "recover", file, flowAsk(flowBackup, flowUnlock), &out, call, signer.BackupFingerprint(backup)); e != nil {
		t.Fatal("ordinary CLI recovery", e)
	}
	in := windowInput("recover", update.NewProbe())
	in.Backup = bytes.Clone(backup)
	in.Fingerprint = signer.BackupFingerprint(backup)
	root := filepath.Join(ownerFixture(t), "operation")
	if e = conductWindow(t.Context(), c, root, in, &bytes.Buffer{}, call); e != nil {
		t.Fatal("native preparation", e)
	}
	in.Phase = "verify"
	if e = conductWindow(t.Context(), c, root, in, &bytes.Buffer{}, call); e != nil {
		t.Fatal("native reopened acknowledgement", e)
	}
	for _, bad := range []string{"wallet-password", "backup-password", "cancel"} {
		ctx := t.Context()
		ask := flowAsk(flowBackup, flowUnlock)
		if bad == "wallet-password" {
			ask = flowAsk(flowBackup, []byte("wrong wallet password"))
		}
		if bad == "backup-password" {
			ask = flowAsk([]byte("wrong backup password"), flowUnlock)
		}
		if bad == "cancel" {
			ask = func(string, bool) ([]byte, error) { return nil, context.Canceled }
		}
		if e = conductFlow(ctx, "recover", file, ask, &bytes.Buffer{}, call, signer.BackupFingerprint(backup)); e == nil {
			t.Fatal("bad owner input accepted", bad)
		}
	}
	if e = v.Unlock(flowUnlock, time.Minute); e == nil {
		t.Fatal("exclusive Unlock guard changed")
	}
	after, e := v.UpdateInventory(update.NewProbe())
	if e != nil || !update.SameOrdinarySnapshot(*before, *after) || after.ActiveIntentID != intent.ID || !v.Status().Locked {
		t.Fatal("owner verification changed intent/lock", e)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	journal, _ := os.ReadFile(filepath.Join(dir, "operations.log"))
	if !bytes.Equal(keyBytes, got) || !bytes.Equal(history, journal) {
		t.Fatal("owner verification wrote live key/history")
	}
}
