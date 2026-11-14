//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func atomicTransactionModel(t *testing.T, tx InstallTransaction, dir string, fail *string) installOps {
	t.Helper()
	o := completionModel(t, tx, dir)
	state := o.state
	o.state = func(ctx context.Context) (*update.Installation, error) {
		i, err := state(ctx)
		if errors.Is(err, os.ErrNotExist) {
			return nil, signer.ErrNoInstallationHold
		}
		return i, err
	}
	release := o.release
	o.release = func(ctx context.Context, i update.Installation) error {
		if _, err := os.Stat(dir + "/update-start-blocked.json"); err == nil {
			return errors.New("core service is stopped")
		}
		return release(ctx, i)
	}
	write := func(name string, data []byte) error {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0644)
	}
	o.atomic = func(ctx context.Context, action string) error {
		current, err := o.state(ctx)
		if err != nil || current == nil {
			return errors.New("actual signer state missing")
		}
		component, source := "core", tx.Binding.TargetCore
		if action == "rollback" {
			source = tx.Binding.PreviousCore
		}
		if action == "cleanup-rollback" || action == "cleanup-complete" {
			component, source = "installation", current.Digest
		}
		if err := signer.ValidateInstallationReplacement(current, tx.Binding.Index.Binding.Pause.Request.IntentID, current.Digest, action, component, source); err != nil {
			return err
		}
		marker := signer.InstallationGuardMarker{Version: 1, Owner: tx.Base.Owner, SignerID: tx.Base.ID, IntentID: tx.Binding.Index.Binding.Pause.Request.IntentID, Digest: current.Digest, Direction: action}
		if _, err := os.Stat(dir + "/update-start-blocked.json"); errors.Is(err, os.ErrNotExist) {
			if err := write("update-start-blocked.json", mustJSON(marker)); err != nil {
				return err
			}
		}
		failAt := func(at string) error {
			if *fail == at {
				*fail = ""
				return errors.New("injected interruption")
			}
			return nil
		}
		if action == "forward" || action == "rollback" {
			if err := write("update-active.json", mustJSON(phaseRecord{1, current.Digest, marker.IntentID})); err != nil {
				return err
			}
			if action == "rollback" {
				if err := write("updates/"+marker.IntentID+"/rollback.json", mustJSON(phaseRecord{1, current.Digest, "rollback"})); err != nil {
					return err
				}
			}
			srcCore, srcSigner, manifest := "next-core", "next-signer", tx.Target
			if action == "rollback" {
				srcCore, srcSigner, manifest = "runner", "previous-signer", tx.Base
			}
			for _, item := range []struct{ src, target string }{{srcCore, "fased-app"}, {srcSigner, "fased-signer"}} {
				b, err := os.ReadFile(filepath.Join(dir, "updates", marker.IntentID, item.src))
				if err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(dir, item.target), b, 0755); err != nil {
					return err
				}
				if err := failAt(action + ":" + item.target); err != nil {
					return err
				}
			}
			if err := write("install.json", mustJSON(manifest)); err != nil {
				return err
			}
		} else if action == "completion" {
			if err := write("intent.json", mustJSON(installerIntent(tx.Target))); err != nil {
				return err
			}
			if err := failAt("completion:intent"); err != nil {
				return err
			}
			if err := write("ready.json", readyRecord(tx.Target)); err != nil {
				return err
			}
		} else {
			if action == "cleanup-complete" {
				if _, err := os.Stat(dir + "/core-released"); err != nil {
					return errors.New("core has not released")
				}
			}
			if err := os.Remove(dir + "/update-active.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := failAt(action + ":active"); err != nil {
				return err
			}
		}
		return os.Remove(dir + "/update-start-blocked.json")
	}
	return o
}

func TestAtomicTransactionRecoversOriginalIntentAndCompletion(t *testing.T) {
	for _, boundary := range []string{"forward:fased-app", "completion:intent", "cleanup-complete:active"} {
		t.Run(boundary, func(t *testing.T) {
			tx, dir := modelTransaction(t)
			for _, item := range []struct {
				name string
				data []byte
				mode os.FileMode
			}{{"intent.json", mustJSON(installerIntent(tx.Base)), 0644}, {"ready.json", readyRecord(tx.Base), 0644}, {"owner-intent.json", mustJSON(installerIntent(tx.Base)), 0600}} {
				if err := os.WriteFile(filepath.Join(dir, item.name), item.data, item.mode); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(dir + "/financial-history")
			fail := boundary
			o := atomicTransactionModel(t, tx, dir, &fail)
			if _, err := runAtomicTransaction(t.Context(), tx, false, o); err != nil {
				if boundary != "forward:fased-app" {
					t.Fatal(err)
				}
				if _, err = runAtomicTransaction(t.Context(), tx, false, o); err != nil {
					t.Fatal("forward recovery failed", err)
				}
			}
			result, err := runAtomicCompletion(t.Context(), tx, o)
			if err != nil {
				if boundary == "forward:fased-app" {
					t.Fatal(err)
				}
				result, err = runAtomicCompletion(t.Context(), tx, o)
			}
			if err != nil || result == nil || result.Phase != "completed" {
				t.Fatal("completion recovery failed", result, err)
			}
			worker := o.atomic
			calls := 0
			o.atomic = func(ctx context.Context, action string) error { calls++; return worker(ctx, action) }
			if _, err := runAtomicCompletion(t.Context(), tx, o); err != nil || calls != 0 {
				t.Fatal("completed retry restarted services", calls, err)
			}
			if _, err = runAtomicTransaction(t.Context(), tx, true, o); err == nil {
				t.Fatal("committed rollback allowed")
			}
			if _, err = os.Stat(dir + "/update-active.json"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("active marker retained after completion", err)
			}
			if after, _ := os.ReadFile(dir + "/financial-history"); string(after) != string(before) {
				t.Fatal("financial history changed")
			}
		})
	}
}

func TestAtomicRollbackRecoversDecisionAndPreservesHistory(t *testing.T) {
	tx, dir := modelTransaction(t)
	for _, item := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{{"intent.json", mustJSON(installerIntent(tx.Base)), 0644}, {"ready.json", readyRecord(tx.Base), 0644}, {"owner-intent.json", mustJSON(installerIntent(tx.Base)), 0600}} {
		if err := os.WriteFile(filepath.Join(dir, item.name), item.data, item.mode); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := os.ReadFile(dir + "/financial-history")
	fail := ""
	o := atomicTransactionModel(t, tx, dir, &fail)
	if _, err := runAtomicTransaction(t.Context(), tx, false, o); err != nil {
		t.Fatal(err)
	}
	fail = "rollback:fased-app"
	if _, err := runAtomicTransaction(t.Context(), tx, true, o); err == nil {
		t.Fatal("rollback interruption missed")
	}
	result, err := runAtomicTransaction(t.Context(), tx, false, o)
	if err != nil || result == nil || result.Phase != "rolled-back-held" {
		t.Fatal("rollback decision did not recover", result, err)
	}
	if after, _ := os.ReadFile(dir + "/financial-history"); string(after) != string(before) {
		t.Fatal("financial history changed")
	}
	if _, err = os.Stat(dir + "/update-active.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rollback active marker retained", err)
	}
}

func TestAtomicTransactionDoesNotCreateHoldOnUnknownStateOrRollback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stateErr error
		rollback bool
	}{
		{"status unavailable", context.DeadlineExceeded, false},
		{"rollback without hold", signer.ErrNoInstallationHold, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, dir := modelTransaction(t)
			fail := ""
			o := atomicTransactionModel(t, tx, dir, &fail)
			o.state = func(context.Context) (*update.Installation, error) { return nil, tc.stateErr }
			holdCalls := 0
			o.hold = func(context.Context, string, update.InstallBinding) (*update.Installation, error) {
				holdCalls++
				return nil, errors.New("unexpected hold")
			}
			if _, err := runAtomicTransaction(t.Context(), tx, tc.rollback, o); err == nil {
				t.Fatal("unsafe transaction was accepted")
			}
			if holdCalls != 0 {
				t.Fatal("attempted to create a hold without confirmed initial state", holdCalls)
			}
		})
	}
}
