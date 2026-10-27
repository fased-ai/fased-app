//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

var flowUnlock = []byte("Disposable flow unlock password")
var flowBackup = []byte("Disposable flow backup password")

func flowVault(t *testing.T) *signer.Vault {
	t.Helper()
	v, e := signer.Open(ownerFixture(t), "11111111111111111111111111111111")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { v.Close() })
	return v
}
func flowCall(v *signer.Vault) ownerCall {
	return func(ctx context.Context, r signer.Request) (signer.Response, error) {
		if e := ctx.Err(); e != nil {
			return signer.Response{}, e
		}
		var doc []byte
		var e error
		switch r.Action {
		case "status":
		case "lock":
			v.Lock()
		case "import-fixture-seed":
			e = v.ImportFixtureSeed(r.Document, r.Password)
		case "create":
			e = v.Create(r.Password)
		case "unlock":
			e = v.Unlock(r.Password, 60*1000000000)
		case "backup":
			doc, e = v.Backup(r.Password)
		case "restore":
			e = v.Restore(r.Document, r.Password, r.NewPassword)
		case "verify-backup":
			e = v.VerifyBackup(r.Document, r.Password)
		default:
			e = signer.ErrUnavailable
		}
		return signer.Response{Status: v.Status(), Document: doc}, e
	}
}
func flowAsk(backup, unlock []byte) askPassword {
	return func(label string, _ bool) ([]byte, error) {
		if bytes.Contains([]byte(label), []byte("backup")) {
			return bytes.Clone(backup), nil
		}
		return bytes.Clone(unlock), nil
	}
}
func TestProtectedSetupBackupAndRecoveryFlow(t *testing.T) {
	v := flowVault(t)
	file := filepath.Join(ownerFixture(t), "backup.json")
	var out bytes.Buffer
	if e := conductFlow(context.Background(), "setup", file, flowAsk(flowBackup, flowUnlock), &out, flowCall(v)); e != nil {
		t.Fatal(e)
	}
	initial := v.Status()
	if !initial.Locked || initial.Account == nil || !bytes.Contains(out.Bytes(), []byte(`"backupVerified":true`)) || bytes.Contains(out.Bytes(), flowUnlock) || bytes.Contains(out.Bytes(), flowBackup) {
		t.Fatal("unsafe setup completion")
	}
	backup, e := readOwnerFile(file)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(backup)
	if e = conductFlow(context.Background(), "setup", file, flowAsk(flowBackup, flowUnlock), &out, flowCall(v)); e != nil {
		t.Fatal("setup retry", e)
	}
	if *v.Status().Account != *initial.Account {
		t.Fatal("setup retry replaced key")
	}
	if e = conductFlow(context.Background(), "recover", file, flowAsk(flowBackup, flowUnlock), &out, flowCall(v)); e == nil {
		t.Fatal("recovery overwrote existing key")
	}
	restored := flowVault(t)
	if e = conductFlow(context.Background(), "recover", file, flowAsk(flowBackup, flowUnlock), &out, flowCall(restored)); e != nil {
		t.Fatal(e)
	}
	s := restored.Status()
	if !s.Locked || !s.RecoveryRequired || s.Account.PublicKey != initial.Account.PublicKey || s.Account.Epoch == initial.Account.Epoch || s.WalletSigner {
		t.Fatal("recovery bypassed hold/lock or changed key")
	}
}
func TestInterruptedOwnerFlowsResumeWithoutReplacingKey(t *testing.T) {
	v := flowVault(t)
	file := filepath.Join(ownerFixture(t), "backup.json")
	out := &bytes.Buffer{}
	call := flowCall(v)
	fail := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		if r.Action == "backup" {
			return signer.Response{}, errors.New("disposable owner interruption")
		}
		return call(ctx, r)
	}
	if conductFlow(context.Background(), "setup", file, flowAsk(flowBackup, flowUnlock), out, fail) == nil || !v.Status().Locked {
		t.Fatal("interruption retained unlock")
	}
	initial := *v.Status().Account
	if e := conductFlow(context.Background(), "setup", file, flowAsk(flowBackup, flowUnlock), out, call); e != nil {
		t.Fatal(e)
	}
	if *v.Status().Account != initial {
		t.Fatal("interrupted setup regenerated identity")
	}
	restored := flowVault(t)
	restoreCall := flowCall(restored)
	failRestore := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		if r.Action == "verify-backup" {
			return signer.Response{}, errors.New("interrupted verification")
		}
		return restoreCall(ctx, r)
	}
	if conductFlow(context.Background(), "recover", file, flowAsk(flowBackup, flowUnlock), out, failRestore) == nil {
		t.Fatal("recovery injection")
	}
	hold := restored.Status()
	if !hold.Locked || !hold.RecoveryRequired {
		t.Fatal("interrupted restore was not held/locked")
	}
	if e := conductFlow(context.Background(), "recover", file, flowAsk(flowBackup, flowUnlock), out, restoreCall); e != nil {
		t.Fatal(e)
	}
	if *restored.Status().Account != *hold.Account {
		t.Fatal("recovery retry changed authority epoch again")
	}
}
func TestOwnerCancellationAndDamagedBackupStayLocked(t *testing.T) {
	v := flowVault(t)
	file := filepath.Join(ownerFixture(t), "backup.json")
	if e := conductFlow(context.Background(), "setup", file, flowAsk(flowBackup, flowUnlock), &bytes.Buffer{}, flowCall(v)); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	if conductFlow(context.Background(), "setup", file, flowAsk([]byte("Wrong disposable backup password"), flowUnlock), &bytes.Buffer{}, flowCall(v)) == nil || !v.Status().Locked {
		t.Fatal("wrong backup password accepted or retained key")
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(before, after) {
		t.Fatal("backup overwritten")
	}
	ctx, cancel := context.WithCancel(context.Background())
	base := flowCall(v)
	call := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		response, e := base(ctx, r)
		if r.Action == "unlock" {
			cancel()
		}
		return response, e
	}
	if conductFlow(ctx, "setup", file, flowAsk(flowBackup, flowUnlock), &bytes.Buffer{}, call) == nil || !v.Status().Locked {
		t.Fatal("cancellation did not use independent lock context")
	}
	if e = os.WriteFile(file, []byte("corrupt disposable backup"), 0600); e != nil {
		t.Fatal(e)
	}
	empty := flowVault(t)
	if conductFlow(context.Background(), "recover", file, flowAsk(flowBackup, flowUnlock), &bytes.Buffer{}, flowCall(empty)) == nil || empty.Status().Account != nil {
		t.Fatal("corrupt restore changed key state")
	}
}

func TestProtectedImportFlow(t *testing.T) {
	v := flowVault(t)
	dir := ownerFixture(t)
	seedPath := filepath.Join(dir, "seed")
	backupPath := filepath.Join(dir, "backup.json")
	seed := bytes.Repeat([]byte{0x42}, 32)
	if e := os.WriteFile(seedPath, seed, 0600); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e := conductImport(context.Background(), seedPath, backupPath, flowAsk(flowBackup, flowUnlock), &out, flowCall(v)); e != nil {
		t.Fatal(e)
	}
	before := v.Status()
	if !before.Locked || !before.RecoveryRequired || before.WalletSigner || !bytes.Contains(out.Bytes(), []byte(`"backupVerified":true`)) || bytes.Contains(out.Bytes(), seed) {
		t.Fatal("unsafe import completion")
	}
	if e := conductImport(context.Background(), seedPath, backupPath, flowAsk(flowBackup, flowUnlock), &out, flowCall(v)); e == nil {
		t.Fatal("overwrite allowed")
	}
	after, _ := os.ReadFile(seedPath)
	if !bytes.Equal(seed, after) || *before.Account != *v.Status().Account {
		t.Fatal("owner data changed")
	}
}
func TestProtectedImportFailureLocksAndPreserves(t *testing.T) {
	for _, kind := range []string{"public", "symlink", "malformed", "existing-backup", "backup-failure"} {
		t.Run(kind, func(t *testing.T) {
			v := flowVault(t)
			dir := ownerFixture(t)
			seed := filepath.Join(dir, "seed")
			backup := filepath.Join(dir, "backup")
			if e := os.WriteFile(seed, bytes.Repeat([]byte{7}, 32), 0600); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "public":
				os.Chmod(seed, 0644)
			case "symlink":
				os.Rename(seed, seed+"-real")
				os.Symlink(seed+"-real", seed)
			case "malformed":
				os.WriteFile(seed, []byte("bad"), 0600)
			case "existing-backup":
				os.WriteFile(backup, []byte("preserve"), 0600)
			}
			call := flowCall(v)
			wrapped := func(ctx context.Context, r signer.Request) (signer.Response, error) {
				if kind == "backup-failure" && r.Action == "backup" {
					return signer.Response{}, errors.New("injected backup failure")
				}
				return call(ctx, r)
			}
			if e := conductImport(context.Background(), seed, backup, flowAsk(flowBackup, flowUnlock), &bytes.Buffer{}, wrapped); e == nil {
				t.Fatal("expected refusal")
			}
			if !v.Status().Locked {
				t.Fatal("failure left unlocked")
			}
			if kind == "backup-failure" {
				if v.Status().Account == nil || !v.Status().RecoveryRequired {
					t.Fatal("lost interrupted import")
				}
				before := *v.Status().Account
				if e := conductFlow(context.Background(), "setup", backup, flowAsk(flowBackup, flowUnlock), &bytes.Buffer{}, call); e != nil {
					t.Fatal(e)
				}
				if *v.Status().Account != before {
					t.Fatal("retry replaced import")
				}
			} else if v.Status().Account != nil {
				t.Fatal("refused input created wallet")
			}
		})
	}
}
