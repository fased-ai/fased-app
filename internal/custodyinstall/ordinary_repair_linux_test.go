//go:build linux

package custodyinstall

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func ordinaryRepairModel(t *testing.T) (BootstrapTransaction, string, installOps, ordinaryBootstrapOps) {
	t.Helper()
	tx, dir, o, c := ordinaryBootstrapModel(t)
	var prior, target update.Envelope
	if update.StrictJSON(tx.Bundle.Maintenance, &prior) != nil || update.StrictJSON(tx.Bundle.Target, &target) != nil {
		t.Fatal("model signatures")
	}
	var old, next update.Manifest
	_ = update.StrictJSON(prior.Payload, &old)
	_ = update.StrictJSON(target.Payload, &next)
	for n := range old.Components {
		old.Components[n].Contract = next.Components[n].Contract
	}
	next.BootstrapFrom = sum(mustJSON(old))
	for n := range next.Components {
		data := "repair-core"
		if next.Components[n].Contract.Component == "signer" {
			data = "repair-signer"
		}
		next.Components[n].Artifact.Size = int64(len(data))
		next.Components[n].Artifact.SHA256 = sum([]byte(data))
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	tx.Trust = mustJSON(update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: old.IssuedAt - 60, NotAfter: old.ExpiresAt + 60}}})
	sign := func(m update.Manifest) []byte {
		p := mustJSON(m)
		return mustJSON(update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: p, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), p...))})
	}
	tx.Version = 3
	tx.Bundle = update.BootstrapBundle{Version: 3, Maintenance: sign(old), Target: sign(next)}
	tx.Target.CoreHash = sum([]byte("repair-core"))
	tx.Target.SignerHash = sum([]byte("repair-signer"))
	_, s, e := c.cold(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	s.ActiveIntentID = tx.ID
	s.SequenceFloor = next.Sequence
	s.InstalledSequence = 1
	s.States[1].Features = append(s.States[1].Features, "ordinary-maintenance-v1", "install-completion-v1")
	s.States[1].Anchor = sum([]byte("genuine recovered history plus prior handoff and staged intent"))
	tx.Signer = s
	x, _ := modelTransaction(t)
	x.Base = tx.Base
	x.Target = tx.Base
	x.Target.CoreHash = sum([]byte("next-core"))
	x.Target.SignerHash = sum([]byte("next-signer"))
	x.Binding.InstallationID = tx.Base.ID
	x.Binding.PreviousManifest = sum(mustJSON(tx.Base))
	x.Binding.Contracts = repairContracts(next)
	x.Binding.Index.Binding.Pause.Request.Sequence = next.Sequence
	x.Binding.Index.Binding.Pause.Request.Expected = tx.Core
	x.Binding.Index.Binding.Pause.Core = tx.Core
	x.Binding.Index.Binding.Signer = s
	x.Binding.Index.BindingSHA256 = update.RecoveryDigest(x.Binding.Index.Binding)
	tx.Core.BootID, tx.Core.ProbeID = "", ""
	tx.Signer.BootID, tx.Signer.ProbeID = "", ""
	x.Binding.Index.Binding.Pause.Core = tx.Core
	x.Binding.Index.Binding.Signer = tx.Signer
	x.Binding.Index.BindingSHA256 = update.RecoveryDigest(x.Binding.Index.Binding)
	tx.Repair = &x
	if !x.valid() {
		t.Fatal("invalid retained ordinary transaction")
	}
	if _, e = tx.Plan(); e != nil {
		t.Fatal("repair model", e)
	}
	write := func(p string, b []byte, mode os.FileMode) {
		t.Helper()
		p = dir + strings.TrimPrefix(p, tx.Base.Root)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, b, mode); e != nil {
			t.Fatal(e)
		}
	}
	for _, v := range []struct {
		p string
		b []byte
	}{{tx.Dir() + "/candidate/fased-app", []byte("repair-core")}, {tx.Dir() + "/candidate/fased-signer", []byte("repair-signer")}, {tx.Dir() + "/transaction.json", mustJSON(tx)}, {x.dir() + "/transaction.json", mustJSON(x)}} {
		mode := os.FileMode(0644)
		if strings.Contains(v.p, "/candidate/") {
			mode = 0755
		}
		write(v.p, v.b, mode)
	}
	c.cold = func(context.Context) (update.CoreSnapshot, update.SignerSnapshot, error) {
		return tx.Core, tx.Signer, nil
	}
	c.observe = func(context.Context) error { return nil }
	c.commit = func(context.Context, update.OrdinaryHandoffAuthority) (update.SignerSnapshot, error) {
		t.Fatal("repair appended a handoff or reserved another floor")
		return s, nil
	}
	o.ownerRecord = func(write bool) error {
		path := dir + "/owner-intent.json"
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if string(b) != string(mustJSON(installerIntent(tx.Base))) && string(b) != string(mustJSON(installerIntent(tx.Target))) {
			return errors.New("foreign repair owner")
		}
		if write {
			return os.WriteFile(path, mustJSON(installerIntent(tx.Target)), 0600)
		}
		return nil
	}
	return tx, dir, o, c
}

func TestOrdinaryRepairPreservesStagedIntentAndForwardRecovery(t *testing.T) {
	for _, phase := range []string{"ordinary-stopped", "ordinary-trial-verified", "forward-only", "core-replaced", "pair-replaced", "inventory-replaced", "installer-intent.json", "installer-ready.json", "installer-owner", "started", "ordinary-healthy", "completed"} {
		t.Run(phase, func(t *testing.T) {
			tx, dir, o, c := ordinaryRepairModel(t)
			retained, _ := os.ReadFile(dir + "/updates/" + tx.ID + "/transaction.json")
			history, _ := os.ReadFile(dir + "/financial-history")
			o.checkpoint = func(at string) error {
				if at == phase {
					return syscall.ENOSPC
				}
				return nil
			}
			if _, e := runOrdinaryBootstrap(t.Context(), tx, o, c); !errors.Is(e, syscall.ENOSPC) {
				t.Fatal("fault not reached", e)
			}
			if phase != "ordinary-stopped" && phase != "ordinary-trial-verified" {
				if _, e := runOrdinaryBootstrap(t.Context(), tx, o, c, true); e == nil {
					t.Fatal("post-fence rollback accepted")
				}
			}
			o.checkpoint = nil
			r, e := runOrdinaryBootstrap(t.Context(), tx, o, c)
			if e != nil || r.Phase != "reader-repaired" || r.InstalledSequenceCommitted || r.FinancialExecution {
				t.Fatal("repair recovery", r, e)
			}
			r, e = runOrdinaryBootstrap(t.Context(), tx, o, c)
			if e != nil || !r.AlreadyCurrent {
				t.Fatal("repeat repair", e)
			}
			got, _ := os.ReadFile(dir + "/updates/" + tx.ID + "/transaction.json")
			after, _ := os.ReadFile(dir + "/financial-history")
			if string(got) != string(retained) || string(after) != string(history) || tx.Signer.SequenceFloor != 7 || tx.Signer.ActiveIntentID != tx.ID {
				t.Fatal("repair rebound staged intent/history/floor")
			}
			for _, rollback := range []bool{false, true} {
				if _, e = runTransaction(t.Context(), *tx.Repair, rollback, o); e == nil {
					t.Fatal("obsolete transaction dispatched after repair")
				}
			}
			if _, e = os.Stat(dir + "/signer-hold.json"); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("obsolete install acquired signer hold")
			}
		})
	}
}
func TestOrdinaryRepairRefusesDriftAndCancellation(t *testing.T) {
	for _, kind := range []string{"cancel", "snapshot", "raw-write", "intent", "held", "source-contract", "unsigned-target", "foreign-active", "startup-drift"} {
		t.Run(kind, func(t *testing.T) {
			tx, dir, o, c := ordinaryRepairModel(t)
			switch kind {
			case "snapshot":
				base := c.cold
				c.cold = func(ctx context.Context) (update.CoreSnapshot, update.SignerSnapshot, error) {
					a, b, e := base(ctx)
					b.SequenceFloor--
					return a, b, e
				}
			case "raw-write":
				n := 0
				c.raw = func(context.Context) (string, error) { n++; return sum([]byte(strings.Repeat("x", n))), nil }
			case "intent":
				_ = os.WriteFile(dir+"/updates/"+tx.ID+"/transaction.json", []byte("foreign"), 0644)
			case "held":
				_ = os.WriteFile(dir+"/updates/"+tx.ID+"/held.json", []byte("foreign hold"), 0644)
			case "source-contract":
				tx.Signer.ActiveIntentID = strings.Repeat("a", 32)
			case "unsigned-target":
				tx.Bundle.Target = []byte("forged")
			case "startup-drift":
				c.observe = func(context.Context) error { return errors.New("startup drift") }
			case "foreign-active":
				_ = os.WriteFile(dir+"/update-active.json", []byte("foreign"), 0644)
			}
			r, e := runOrdinaryBootstrap(t.Context(), tx, o, c, kind == "cancel")
			if kind == "cancel" {
				if e != nil || r.Phase != "cancelled" {
					t.Fatal("pre-fence cancel", e)
				}
				if e = refuseRepairedPredecessor(*tx.Repair, o.read); e != nil {
					t.Fatal("cancel blocked original", e)
				}
			} else if e == nil {
				t.Fatal("unsafe repair accepted")
			}
			if kind == "startup-drift" {
				if _, e = os.Stat(dir + "/bootstraps/" + tx.ID + "/completed.json"); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("startup drift fabricated completion")
				}
				if _, e = os.Stat(dir + "/update-active.json"); e != nil {
					t.Fatal("startup failure cleared recovery fence")
				}
				if _, e = runOrdinaryBootstrap(t.Context(), tx, o, c, true); e == nil {
					t.Fatal("startup failure allowed old-reader rollback")
				}
				c.observe = func(context.Context) error { return nil }
				if _, e = runOrdinaryBootstrap(t.Context(), tx, o, c); e != nil {
					t.Fatal("same repair cannot recover after corrected startup", e)
				}
				return
			}
			b, _ := os.ReadFile(dir + "/fased-signer")
			if string(b) != "old-signer" {
				t.Fatal("refusal replaced original signer")
			}
			if _, e = os.Stat(dir + "/bootstraps/" + tx.ID + "/forward-only.json"); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("refusal crossed repair fence")
			}
		})
	}
}

func TestOrdinaryRepairColdUsesExactPauseAndUID(t *testing.T) {
	for _, kind := range []string{"ok", "pause-id", "request", "unpaused", "receiver", "unstaged", "signer-error"} {
		t.Run(kind, func(t *testing.T) {
			tx, _, _, _ := ordinaryRepairModel(t)
			calls := 0
			run := func(_ context.Context, _ []byte, args ...string) ([]byte, error) {
				calls++
				uid, gid, image := tx.Base.App.UID, tx.Base.App.GID, "fased-app"
				if calls == 2 {
					uid, gid, image = tx.Base.Signer.UID, tx.Base.Signer.GID, "fased-signer"
				}
				prefix := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(uid), "--regid", strconv.Itoa(gid), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", tx.Dir() + "/candidate/" + image}
				if len(args) < len(prefix) || !reflect.DeepEqual(args[:len(prefix)], prefix) {
					t.Fatal("cold adapter used wrong OS identity/image", args)
				}
				if calls == 1 {
					q := tx.Repair.Binding.Index.Binding.Pause
					metadata := core.FixtureColdMetadata{Core: tx.Core, Phase: "paused", PauseID: q.PauseID, Request: q.Request}
					if kind == "pause-id" {
						metadata.PauseID = update.NewProbe()
					}
					if kind == "unpaused" {
						metadata.Phase = "idle"
					}
					if kind == "request" {
						copy := *q.Request
						copy.Sequence++
						metadata.Request = &copy
					}
					return mustJSON(metadata), nil
				}
				want := []string{"fixture-update-inspect", "--config", tx.Base.Root + "/identity.json", "--disposable-fixture", "--intent-id", tx.ID}
				if !reflect.DeepEqual(args[len(prefix):], want) {
					t.Fatal("cold signer adapter lost exact intent", args)
				}
				id, state := tx.Base.ID, "staged"
				if kind == "receiver" {
					id = update.NewProbe()
				}
				if kind == "unstaged" {
					state = "prepared"
				}
				if kind == "signer-error" {
					return nil, errors.New("cold signer refused")
				}
				q := tx.Repair.Binding.Index.Binding.Pause.Request
				return mustJSON(map[string]any{"updateInventory": tx.Signer, "updateIntent": update.Intent{ID: tx.ID, State: state, Sequence: q.Sequence, ManifestSHA256: q.ManifestSHA256, Contracts: tx.Repair.Binding.Contracts}, "status": map[string]any{"locked": true, "account": map[string]any{"signerId": id, "network": tx.Base.Network}}}), nil
			}
			c := ordinaryBootstrapCallbacksWithRun(tx, run)
			_, _, e := c.cold(t.Context())
			if (e == nil) != (kind == "ok") {
				t.Fatal("cold repair validation", kind, e)
			}
		})
	}
}

func TestOrdinaryRepairSignedContractAndFloorRefusals(t *testing.T) {
	for _, kind := range []string{"schema", "same-signer", "expiry", "sequence", "predecessor", "floor", "pause", "foreign-owner", "legacy-v2"} {
		t.Run(kind, func(t *testing.T) {
			tx, _, _, _ := ordinaryRepairModel(t)
			var a, z update.Envelope
			_ = update.StrictJSON(tx.Bundle.Maintenance, &a)
			_ = update.StrictJSON(tx.Bundle.Target, &z)
			var old, next update.Manifest
			_ = update.StrictJSON(a.Payload, &old)
			_ = update.StrictJSON(z.Payload, &next)
			switch kind {
			case "schema":
				next.Components[0].Contract.State[0].Versions.Min++
			case "same-signer":
				next.Components[1].Artifact = old.Components[1].Artifact
				tx.Target.SignerHash = old.Components[1].Artifact.SHA256
			case "expiry":
				tx.ReviewedAt = next.ExpiresAt + 1
			case "sequence":
				next.Sequence++
			case "predecessor":
				old.Components[0].Artifact.SHA256 = sum([]byte("foreign predecessor"))
				next.BootstrapFrom = sum(mustJSON(old))
			case "floor":
				tx.Signer.SequenceFloor++
			case "pause":
				q := *tx.Repair.Binding.Index.Binding.Pause.Request
				q.IntentID = update.NewProbe()
				tx.Repair.Binding.Index.Binding.Pause.Request = &q
			case "foreign-owner":
				tx.Base.Owner++
				tx.Target.Owner++
			case "legacy-v2":
				tx.Version = 2
				tx.Bundle.Version = 2
			}
			pub, key, e := ed25519.GenerateKey(rand.Reader)
			if e != nil {
				t.Fatal(e)
			}
			tx.Trust = mustJSON(update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: old.IssuedAt - 60, NotAfter: old.ExpiresAt + 60}}})
			sign := func(m update.Manifest) []byte {
				p := mustJSON(m)
				return mustJSON(update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: p, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), p...))})
			}
			tx.Bundle.Maintenance, tx.Bundle.Target = sign(old), sign(next)
			if _, e = tx.Plan(); e == nil {
				t.Fatal("unsafe repair contract accepted", kind)
			}
		})
	}
}

func TestOrdinaryRepairPostStartObservesActualTypedRecords(t *testing.T) {
	for _, kind := range []string{"ok", "pause", "core", "floor", "journal", "receiver", "unlocked", "intent", "transport"} {
		t.Run(kind, func(t *testing.T) {
			tx, _, _, _ := ordinaryRepairModel(t)
			pause := func(_ context.Context, cmd update.CorePauseCommand) (update.CorePauseReceipt, error) {
				if cmd.Action != "status" || !update.IDOK(cmd.ProbeID) {
					t.Fatal("observation attempted to change pause")
				}
				p := tx.Repair.Binding.Index.Binding.Pause
				if kind == "pause" {
					p.PauseID = update.NewProbe()
				}
				if kind == "core" {
					p.Core.State.Anchor = sum([]byte("changed metadata"))
				}
				return p, nil
			}
			call := func(_ context.Context, r signer.Request) (signer.Response, error) {
				if kind == "transport" {
					return signer.Response{}, errors.New("private IPC unavailable")
				}
				var response signer.Response
				if r.Action == "fixture-update-inventory" {
					s := tx.Signer
					if kind == "floor" {
						s.SequenceFloor++
					}
					if kind == "journal" {
						s.States = append([]update.State(nil), s.States...)
						s.States[1].Anchor = sum([]byte("changed journal"))
					}
					_ = json.Unmarshal(mustJSON(map[string]any{"locked": kind != "unlocked", "account": map[string]any{"signerId": tx.Base.ID, "network": tx.Base.Network}}), &response.Status)
					if kind == "receiver" {
						response.Status.Account.SignerID = update.NewProbe()
					}
					response.UpdateInventory = &s
				} else if r.Action == "fixture-update-status" {
					q := tx.Repair.Binding.Index.Binding.Pause.Request
					i := update.Intent{ID: tx.ID, State: "staged", Sequence: q.Sequence, ManifestSHA256: q.ManifestSHA256, Contracts: tx.Repair.Binding.Contracts}
					if kind == "intent" {
						i.State = "aborted"
					}
					response.UpdateIntent = &i
				} else {
					t.Fatal("observation dispatched a write action", r.Action)
				}
				return response, nil
			}
			e := observeOrdinaryRepair(t.Context(), tx, pause, call)
			if (e == nil) != (kind == "ok") {
				t.Fatal("startup observation missed changed protected state", kind, e)
			}
		})
	}
}
