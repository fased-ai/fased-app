//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

func windowInput(action, id string) signer.OwnerInput {
	return signer.OwnerInput{Intent: signer.OwnerIntent{Version: 1, InstanceID: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32)), OperationID: id, Action: action, Network: "devnet"}, SignerID: "11111111111111111111111111111111", Phase: "prepare", Unlock: bytes.Clone(flowUnlock), BackupPassword: bytes.Clone(flowBackup)}
}
func windowVault(t *testing.T, id string) (*signer.Vault, signer.Config) {
	t.Helper()
	v, err := signer.OpenControlledOwnerCustody(ownerFixture(t), id, "devnet")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v, signer.Config{Version: 3, Network: "devnet", SignerID: id}
}
func windowRun(t *testing.T, v *signer.Vault, c signer.Config, root string, in signer.OwnerInput) signer.OwnerResult {
	t.Helper()
	var out bytes.Buffer
	if err := conductWindow(t.Context(), c, root, in, &out, flowCall(v)); err != nil {
		t.Fatal(err)
	}
	var result signer.OwnerResult
	if json.Unmarshal(out.Bytes(), &result) != nil || result.Status.Account == nil || !result.Status.Locked || !result.Status.RecoveryRequired || result.Status.WalletSigner || result.Status.FinancialExecution {
		t.Fatal("unsafe owner result")
	}
	if bytes.Contains(out.Bytes(), flowUnlock) || bytes.Contains(out.Bytes(), flowBackup) {
		t.Fatal("secret in result")
	}
	return result
}

func TestOwnerWindowCreateReopenVerifyAndRefuseDuplicate(t *testing.T) {
	in := windowInput("setup", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	v, c := windowVault(t, in.SignerID)
	root := filepath.Join(ownerFixture(t), "owner", "operation")
	prepared := windowRun(t, v, c, root, in)
	if prepared.BackupVerified {
		t.Fatal("transmission is not durable local backup verification")
	}
	local := filepath.Join(ownerFixture(t), "backup.json")
	if err := writeOwnerFile(local, prepared.Backup); err != nil {
		t.Fatal(err)
	}
	saved, err := readOwnerFile(local)
	if err != nil {
		t.Fatal(err)
	}
	in.Phase, in.Backup, in.Fingerprint = "verify", saved, signer.BackupFingerprint(saved)
	verified := windowRun(t, v, c, root, in)
	if !verified.BackupVerified || *verified.Status.Account != *prepared.Status.Account {
		t.Fatal("acknowledgement changed wallet")
	}
	in.Phase, in.Backup, in.Fingerprint = "prepare", nil, ""
	again := windowRun(t, v, c, root, in)
	if !bytes.Equal(again.Backup, prepared.Backup) {
		t.Fatal("retry replaced encrypted backup")
	}
	in.Intent.OperationID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := conductWindow(t.Context(), c, root, in, &bytes.Buffer{}, flowCall(v)); err == nil {
		t.Fatal("duplicate operation accepted")
	}
}

func TestOwnerWindowInterruptedImportResumesSameKey(t *testing.T) {
	in := windowInput("import", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	in.Seed = bytes.Repeat([]byte{43}, 32)
	v, c := windowVault(t, in.SignerID)
	root := ownerFixture(t)
	call := flowCall(v)
	failed := false
	err := conductWindow(t.Context(), c, root, in, &bytes.Buffer{}, func(ctx context.Context, r signer.Request) (signer.Response, error) {
		if r.Action == "backup-history" && !failed {
			failed = true
			return signer.Response{}, context.Canceled
		}
		return call(ctx, r)
	})
	if !errors.Is(err, context.Canceled) || !v.Status().Locked || v.Status().Account == nil {
		t.Fatal("interrupted import did not preserve locked key", err)
	}
	ref := *v.Status().Account
	result := windowRun(t, v, c, root, in)
	if *result.Status.Account != ref {
		t.Fatal("retry created a second key")
	}
	in.Seed = bytes.Repeat([]byte{44}, 32)
	if err := conductWindow(t.Context(), c, root, in, &bytes.Buffer{}, call); err == nil || !v.Status().Locked || *v.Status().Account != ref {
		t.Fatal("conflicting import changed key")
	}
}

func TestOwnerWindowIndependentKeyRestoreRemainsHeld(t *testing.T) {
	in := windowInput("setup", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	source, c := windowVault(t, in.SignerID)
	root := ownerFixture(t)
	original := windowRun(t, source, c, root, in)
	source.Close() // No source RPC or tailnet is available for restoration.
	before, err := os.ReadFile(filepath.Join(root, "backup.json"))
	if err != nil {
		t.Fatal(err)
	}
	target, next := windowVault(t, "22222222222222222222222222222222")
	in.Intent.Action, in.SignerID = "recover", next.SignerID
	in.Backup, in.Fingerprint = original.Backup, original.Fingerprint
	restored := windowRun(t, target, next, ownerFixture(t), in)
	a, b := original.Status.Account, restored.Status.Account
	if a.PublicKey != b.PublicKey || a.WalletID != b.WalletID || a.Network != b.Network || a.Epoch == b.Epoch || a.SignerID == b.SignerID {
		t.Fatal("recovery identity did not rotate safely")
	}
	after, err := os.ReadFile(filepath.Join(root, "backup.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("source backup changed")
	}
	// This controlled owner-profile history is key-only genesis. The general
	// signer history test covers nonzero spent/held financial journals; neither
	// path confers spending/admission authority.
	if !restored.Status.RecoveryRequired || !restored.Status.AdmissionHeld {
		t.Fatal("key-only recovery released history/admission hold")
	}
}

func TestOwnerWindowPrivateParserIsBoundedStrictAndInstanceBound(t *testing.T) {
	in := windowInput("setup", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	c := signer.Config{Version: 3, SignerID: in.SignerID, Network: in.Intent.Network}
	valid, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var parsed signer.OwnerInput
	defer parsed.Clear()
	if err = readOwnerWindowInput(c, in.Intent.InstanceID, bytes.NewReader(valid), &parsed); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{append(bytes.Clone(valid), []byte(`{}`)...), append([]byte(`{"unexpected":1,`), valid[1:]...), bytes.Repeat([]byte{'x'}, signer.MaxOwnerInput+1)} {
		var parsed signer.OwnerInput
		err := readOwnerWindowInput(c, in.Intent.InstanceID, bytes.NewReader(bad), &parsed)
		parsed.Clear()
		if err == nil {
			t.Fatal("unsafe private input admitted")
		}
	}
	if err = readOwnerWindowInput(c, "another-instance", bytes.NewReader(valid), &parsed); err == nil {
		t.Fatal("different instance accepted")
	}
}

func TestOwnerWindowLegacyKeyBackupStaysHeld(t *testing.T) {
	in := windowInput("setup", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	source, _ := windowVault(t, in.SignerID)
	if err := source.Create(flowUnlock); err != nil {
		t.Fatal(err)
	}
	if err := source.Unlock(flowUnlock, time.Minute); err != nil {
		t.Fatal(err)
	}
	legacy, err := source.Backup(flowBackup)
	if err != nil {
		t.Fatal(err)
	}
	source.Close()
	target, next := windowVault(t, "33333333333333333333333333333333")
	in.Intent.Action = "recover"
	in.SignerID = next.SignerID
	in.Backup = legacy
	in.Fingerprint = signer.BackupFingerprint(legacy)
	call := flowCall(target)
	err = conductWindow(t.Context(), next, ownerFixture(t), in, &bytes.Buffer{}, func(ctx context.Context, r signer.Request) (signer.Response, error) {
		if r.Action == "restore" && r.SnapshotSHA256 != "" {
			return signer.Response{}, errors.New("key-only restore must not carry a history fingerprint")
		}
		return call(ctx, r)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !target.Status().RecoveryRequired || !target.Status().Locked || target.Status().WalletSigner {
		t.Fatal("old key-only backup acquired authority")
	}
}
