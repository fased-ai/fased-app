//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestProtectedUpdateReviewCancelStalenessAndCommit(t *testing.T) {
	for _, tc := range []string{"cancel", "changed-core", "changed-signer", "bad-artifact", "prepare", "stage", "stage-stale-core", "stage-bad-source", "preflight", "preflight-existing-idle", "preflight-existing-released", "preflight-existing-race", "preflight-owner-interrupted", "preflight-corrupt-reply", "preflight-core-released", "preflight-abort-interrupted", "backup", "backup-interrupted", "backup-corrupt-reply", "backup-signer-aborted", "backup-staged-corrupt", "backup-held-resume"} {
		t.Run(tc, func(t *testing.T) {
			needsPreflight := strings.HasPrefix(tc, "preflight") || strings.HasPrefix(tc, "backup")
			root := filepath.Join("..", "..", ".evidence", "p4-intents", "owner-fixtures")
			if e := os.MkdirAll(root, 0700); e != nil {
				t.Fatal(e)
			}
			dir, e := os.MkdirTemp(root, "owner-")
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() {
				if e := os.RemoveAll(dir); e != nil {
					t.Error(e)
				}
			})
			state := filepath.Join(dir, "signer")
			if e = os.Mkdir(state, 0700); e != nil {
				t.Fatal(e)
			}
			v, e := signer.Open(state, strings.Repeat("1", 32))
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = v.Close() }()
			password := []byte("Disposable intent test password")
			if e = v.Create(password); e != nil {
				t.Fatal(e)
			}
			if e = v.Unlock(password, time.Minute); e != nil {
				t.Fatal(e)
			}
			store, e := core.OpenStore(filepath.Join(dir, "core"))
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = store.Close() }()
			artifactDir := filepath.Join(dir, "artifacts")
			if e = os.Mkdir(artifactDir, 0700); e != nil {
				t.Fatal(e)
			}
			contents := []byte("disposable binary stand-in")
			h := sha256.Sum256(contents)
			now := time.Now().Unix()
			pub, key, e := ed25519.GenerateKey(rand.Reader)
			if e != nil {
				t.Fatal(e)
			}
			m := update.Manifest{Version: 1, Product: "fased-app", Channel: "development", Sequence: 1, IssuedAt: now - 1, ExpiresAt: now + 600, Target: update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}}
			for _, c := range []update.Contract{core.UpdateContract(), signer.UpdateContract()} {
				name := c.Component
				if tc == "backup-held-resume" {
					name = "fased-app"
					if c.Component == "signer" {
						name = "fased-signer"
					}
				}
				m.Components = append(m.Components, update.Component{Version: "0.0.0-fixture.1", Contract: c, Artifact: update.Artifact{File: name, Size: int64(len(contents)), SHA256: hex.EncodeToString(h[:])}})
				if e = os.WriteFile(filepath.Join(artifactDir, name), contents, 0600); e != nil {
					t.Fatal(e)
				}
			}
			payload := mustJSON(m)
			manifest := mustJSON(update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: payload, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), payload...))})
			trust := mustJSON(update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: now - 60, NotAfter: now + 3600}}})
			if tc == "bad-artifact" {
				if e = os.WriteFile(filepath.Join(artifactDir, "core"), []byte("wrong"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			probes, prepares, consents := 0, 0, 0
			abortInterrupted := false
			probe := func(ctx context.Context, p string) (update.CoreSnapshot, error) {
				probes++
				s, e := store.UpdateSnapshot(ctx, p)
				if tc == "changed-core" && probes == 2 {
					s.State.Anchor = update.SnapshotDigest("changed", 0)
				}
				return s, e
			}
			call := func(_ context.Context, r signer.Request) (signer.Response, error) {
				switch r.Action {
				case "fixture-install-controller":
					i, e := v.Installation(*r.Install)
					return signer.Response{Installation: i}, e
				case "fixture-update-preflight":
					p, e := v.PreflightUpdate(*r.Preflight)
					if p != nil && tc == "preflight-corrupt-reply" {
						p.BindingSHA256 = "wrong"
					}
					return signer.Response{Preflight: p}, e
				case "fixture-update-abort":
					i, e := v.AbortUpdate(r.IntentID)
					if tc == "preflight-abort-interrupted" && !abortInterrupted && e == nil {
						abortInterrupted = true
						return signer.Response{}, errors.New("abort reply lost")
					}
					return signer.Response{UpdateIntent: i}, e

				case "fixture-update-stage-write", "fixture-update-stage-status", "fixture-update-stage-finish":
					s, e := v.StageUpdate(r.Action, *r.Stage)
					return signer.Response{Stage: s}, e
				case "fixture-update-status":
					i, e := v.UpdateIntent(r.IntentID)
					return signer.Response{UpdateIntent: i}, e
				case "fixture-update-inventory":
					s, e := v.UpdateInventory(r.ProbeID)
					return signer.Response{UpdateInventory: s}, e
				case "fixture-update-prepare":
					prepares++
					i, e := v.PrepareUpdate(*r.Update)
					return signer.Response{UpdateIntent: i}, e
				default:
					return signer.Response{}, errors.New("unexpected owner RPC")
				}
			}
			confirm := func(expected string) error {
				consents++
				if !strings.HasPrefix(expected, "PREPARE ") || len(expected) != 72 {
					t.Fatal("unbound review")
				}
				if tc == "cancel" {
					return errors.New("cancelled")
				}
				if tc == "changed-signer" {
					return v.SetFixturePolicy(signer.FixturePolicy{Account: *v.Status().Account, Asset: signer.FixtureAsset, Budget: 10, MaxOperation: 10, Destinations: []string{"fixture:treasury"}, ExpiresAt: now + 600})
				}
				return nil
			}
			var review, out bytes.Buffer
			id := update.NewProbe()
			e = conductUpdate(t.Context(), id, manifest, trust, artifactDir, probe, call, confirm, &review, &out)
			if tc == "prepare" || strings.HasPrefix(tc, "stage") || needsPreflight {
				if e != nil || prepares != 1 || !v.Status().UpdateHeld || !v.Status().Locked {
					t.Fatal("protected intent not persisted", e)
				}
				var result struct {
					InstallAllowed, ResumeAllowed, FinancialExecution bool
					Intent                                            *update.Intent
				}
				if e = json.Unmarshal(out.Bytes(), &result); e != nil || result.Intent == nil || result.InstallAllowed || result.ResumeAllowed || result.FinancialExecution {
					t.Fatal("invalid intent receipt", e)
				}

				if strings.HasPrefix(tc, "stage") || needsPreflight {
					if tc == "stage-stale-core" {
						probe = func(_ context.Context, p string) (update.CoreSnapshot, error) {
							s, e := store.UpdateSnapshot(t.Context(), p)
							s.BootID = update.NewProbe()
							return s, e
						}
					}
					if tc == "stage-bad-source" {
						if e = os.WriteFile(filepath.Join(artifactDir, "core"), []byte("wrong"), 0600); e != nil {
							t.Fatal(e)
						}
					}
					out.Reset()
					e = conductStage(t.Context(), id, artifactDir, probe, call, &out)
					if tc == "stage" || needsPreflight {
						var result update.StageStatus
						if e != nil || json.Unmarshal(out.Bytes(), &result) != nil || !result.Verified || result.InstallAllowed || result.ResumeAllowed {
							t.Fatal("owner staging failed", e)
						}
					} else if e == nil || out.Len() != 0 {
						t.Fatal("invalid/stale staging succeeded", e)
					}
					if !v.Status().Locked || !v.Status().UpdateHeld {
						t.Fatal("staging released hold")
					}
					if needsPreflight {
						interrupted := false
						statusReads := 0
						if tc == "preflight-existing-released" {
							intent, err := v.UpdateIntent(id)
							if err != nil {
								t.Fatal(err)
							}
							intent.State = "aborted"
							if _, err = store.UpdatePause(t.Context(), update.CorePauseCommand{Action: "release", ProbeID: update.NewProbe(), Aborted: intent}); err != nil {
								t.Fatal(err)
							}
						}
						if tc == "preflight-existing-race" {
							intent, err := v.UpdateIntent(id)
							if err != nil {
								t.Fatal(err)
							}
							request := update.PauseFor(*intent)
							if _, err = store.UpdatePause(t.Context(), update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: &request}); err != nil {
								t.Fatal(err)
							}
						}
						control := func(ctx context.Context, c update.CorePauseCommand) (update.CorePauseReceipt, error) {
							if tc == "preflight-core-released" && c.Action == "status" {
								i, e := v.UpdateIntent(id)
								if e != nil {
									return update.CorePauseReceipt{}, e
								}
								i.State = "aborted"
								c.Action = "release"
								c.Aborted = i
							}
							p, e := store.UpdatePause(ctx, c)
							if tc == "preflight-existing-race" && c.Action == "status" && e == nil {
								statusReads++
								if statusReads == 2 {
									p.PauseID = update.NewProbe()
								}
							}
							if tc == "preflight-owner-interrupted" && !interrupted && c.Action == "pause" && e == nil {
								interrupted = true
								return update.CorePauseReceipt{}, errors.New("owner helper interrupted after durable pause")
							}
							return p, e
						}
						out.Reset()
						e = conductPreflight(t.Context(), id, control, call, strings.HasPrefix(tc, "preflight-existing-"), &out)
						if tc == "preflight-owner-interrupted" {
							if e == nil || out.Len() != 0 {
								t.Fatal("interruption returned proof")
							}
							held, statusErr := store.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
							if statusErr != nil || held.State != "paused" {
								t.Fatal("owner interruption released core", statusErr)
							}
							e = conductPreflight(t.Context(), id, control, call, false, &out)
						}
						if tc == "preflight" || tc == "preflight-owner-interrupted" || tc == "preflight-abort-interrupted" || strings.HasPrefix(tc, "backup") {
							var p update.Preflight
							if e != nil || json.Unmarshal(out.Bytes(), &p) != nil || !p.Verified || p.InstallAllowed || p.ResumeAllowed {
								t.Fatal("owner preflight failed", e)
							}
						} else if e == nil || out.Len() != 0 {
							t.Fatal("stale or forged preflight returned proof", e)
						}
						if tc == "preflight-existing-idle" {
							current, statusErr := store.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
							if statusErr != nil || current.State != "idle" {
								t.Fatal("recovery preflight changed an absent pause", statusErr)
							}
						}
						if tc == "preflight-existing-released" || tc == "preflight-existing-race" {
							current, statusErr := store.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
							want := "released"
							if tc == "preflight-existing-race" {
								want = "paused"
							}
							if statusErr != nil || current.State != want {
								t.Fatal("recovery preflight changed the retained core state", statusErr)
							}
						}
						if !v.Status().Locked || !v.Status().UpdateHeld {
							t.Fatal("preflight released financial hold")
						}
						if strings.HasPrefix(tc, "backup") {
							once := false
							backup := func(ctx context.Context, p update.Preflight) (update.RecoveryIndex, error) {
								i, e := store.BackupMetadata(ctx, p)
								if e != nil {
									return i, e
								}
								if tc == "backup-interrupted" && !once {
									once = true
									return i, errors.New("owner interrupted after durable index")
								}
								if tc == "backup-corrupt-reply" {
									i.Snapshot.SHA256 = "corrupt"
								}
								if tc == "backup-signer-aborted" {
									_, e = v.AbortUpdate(id)
								}
								if tc == "backup-staged-corrupt" {
									e = os.WriteFile(filepath.Join(state, "update-"+id, "0.artifact"), []byte("bad"), 0600)
								}
								return i, e
							}
							out.Reset()
							e = conductBackup(t.Context(), id, control, backup, call, &out)
							if tc == "backup-interrupted" {
								if e == nil || out.Len() != 0 {
									t.Fatal("lost backup reply returned success")
								}
								e = conductBackup(t.Context(), id, control, backup, call, &out)
							}
							if tc == "backup" || tc == "backup-interrupted" || tc == "backup-held-resume" {
								var i update.RecoveryIndex
								if e != nil || json.Unmarshal(out.Bytes(), &i) != nil || i.Phase != "metadata-backed-up" || i.InstallAllowed || i.ResumeAllowed || i.FinancialExecution {
									t.Fatal("owner backup failed", e)
								}
								if tc == "backup-held-resume" {
									assertHeldBackupResumption(t, &v, &store, filepath.Join(dir, "core"), id, i, control, call)
								}
							} else if e == nil || out.Len() != 0 {
								t.Fatal("unbound/stale backup succeeded", e)
							}
						}
						out.Reset()
						if tc == "preflight-abort-interrupted" {
							if e = abortAndRelease(t.Context(), id, control, call, &out); e == nil || out.Len() != 0 {
								t.Fatal("lost abort reply returned result")
							}
							held, statusErr := store.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
							if statusErr != nil || held.State != "paused" || !v.Status().Locked {
								t.Fatal("abort interruption released core or unlocked", statusErr)
							}
						}
						if e = abortAndRelease(t.Context(), id, control, call, &out); e != nil {
							t.Fatal("abort/release failed", e)
						}
						if !v.Status().Locked || v.Status().UpdateHeld {
							t.Fatal("abort unlocked or kept slot")
						}
					}

				}
			} else {
				if e == nil || out.Len() != 0 || v.Status().UpdateHeld {
					t.Fatal("failed review created intent", e)
				}
				if tc != "changed-signer" && prepares != 0 {
					t.Fatal("unreviewed RPC sent")
				}
			}
			if tc != "bad-artifact" && (!strings.Contains(review.String(), hex.EncodeToString(h[:])) || !strings.Contains(review.String(), `"spent": "0"`) || !strings.Contains(review.String(), `"sequence": "1"`) || bytes.Contains(review.Bytes(), password)) {
				t.Fatal("incomplete or secret-bearing review")
			}
			if tc == "bad-artifact" && consents != 0 {
				t.Fatal("asked consent for invalid artifact")
			}
		})
	}
}

// Exercise the owner recovery decision against a real fixture signer journal
// and the core's original SQLite backup, not a manufactured hold or index.
func assertHeldBackupResumption(t *testing.T, v **signer.Vault, store **core.Store, coreDir, id string, index update.RecoveryIndex, control coreControl, call updateRPC) {
	t.Helper()
	ctx := t.Context()
	intent, err := (*v).UpdateIntent(id)
	if err != nil {
		t.Fatal(err)
	}
	m := custodyinstall.Manifest{ID: (*v).Status().Account.SignerID, CoreHash: update.SnapshotDigest("previous-core", 1), SignerHash: update.SnapshotDigest("previous-signer", 1)}
	manifestHash := sha256.Sum256(mustJSON(m))
	binding := update.InstallBinding{Version: 1, Contracts: intent.Contracts, Index: index, InstallationID: m.ID, PreviousCore: m.CoreHash, PreviousSigner: m.SignerHash, PreviousManifest: hex.EncodeToString(manifestHash[:])}
	for _, f := range index.Binding.Files {
		switch f.Artifact.File {
		case "fased-app":
			binding.TargetCore = f.Artifact.SHA256
		case "fased-signer":
			binding.TargetSigner = f.Artifact.SHA256
		}
	}
	preflight := func() (update.Preflight, error) {
		var b bytes.Buffer
		if err := conductPreflight(ctx, id, control, call, true, &b); err != nil {
			return update.Preflight{}, err
		}
		var p update.Preflight
		err := update.StrictJSON(b.Bytes(), &p)
		return p, err
	}
	fresh := func(ctx context.Context, got custodyinstall.Manifest, original *update.RecoveryIndex) (update.Preflight, error) {
		if got != m || original == nil {
			return update.Preflight{}, errors.New("foreign recovery identity")
		}
		// Revalidating an original backup must inspect the existing pause, not
		// create or replace it if the predecessor is unexpectedly idle.
		statusOnly := func(ctx context.Context, command update.CorePauseCommand) (update.CorePauseReceipt, error) {
			if command.Action != "status" {
				return update.CorePauseReceipt{}, errors.New("recovery tried to change the original core pause")
			}
			return control(ctx, command)
		}
		var output bytes.Buffer
		if err := conductPreflight(ctx, id, statusOnly, call, true, &output); err != nil {
			return update.Preflight{}, err
		}
		var p update.Preflight
		if err := update.StrictJSON(output.Bytes(), &p); err != nil {
			return p, err
		}
		verified, err := (*store).VerifyRecovery(ctx, update.RecoveryCheck{Preflight: p, Index: *original})
		if err != nil || update.SnapshotDigest("index", verified) != update.SnapshotDigest("index", *original) {
			return p, errors.New("original recovery verification failed")
		}
		return p, nil
	}
	backup := func(ctx context.Context, p update.Preflight) (update.RecoveryIndex, error) {
		return (*store).BackupMetadata(ctx, p)
	}
	got, held, err := installationRecoveryIndex(ctx, id, m, control, backup, call, fresh)
	if err != nil || held != nil || update.SnapshotDigest("index", got) != update.SnapshotDigest("index", index) {
		t.Fatal("no-hold preparation changed its original backup", err)
	}
	if err = verifyOwnerInstallationHold(ctx, id, nil, call); err != nil {
		t.Fatal("authenticated absence was refused", err)
	}
	transition := func(phase string) (*update.Installation, error) {
		return ownerInstallationPhase(ctx, id, phase, m, binding, fresh, call)
	}
	// The signer can durably append the hold even if the owner loses its RPC
	// reply. Retry must use that same journaled binding and original backup.
	activeCalled := false
	lostActive := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		if r.Action == "fixture-install-controller" && r.Install != nil && r.Install.Action == "active" {
			activeCalled = true
		}
		response, err := call(ctx, r)
		if err == nil && r.Action == "fixture-install-controller" && r.Install != nil && r.Install.Action == "active" {
			return signer.Response{}, errors.New("active hold reply lost")
		}
		return response, err
	}
	if _, err = ownerInstallationPhase(ctx, id, "active", m, binding, fresh, lostActive); err == nil || !activeCalled || !(*v).Status().Locked || !(*v).Status().UpdateHeld {
		t.Fatal("lost hold reply did not preserve the actual locked signer hold", err)
	}
	if err = verifyOwnerInstallationHold(ctx, id, nil, call); err == nil {
		t.Fatal("a hold appearing during review was ignored")
	}
	// Reopen the actual fixture journal and store after an interruption.
	if err = (*v).Close(); err != nil {
		t.Fatal(err)
	}
	if err = (*store).Close(); err != nil {
		t.Fatal(err)
	}
	reopenedVault, err := signer.Open(filepath.Join(filepath.Dir(coreDir), "signer"), strings.Repeat("1", 32))
	if err != nil {
		t.Fatal(err)
	}
	*v = reopenedVault
	reopenedStore, err := core.OpenStore(coreDir)
	if err != nil {
		t.Fatal(err)
	}
	*store = reopenedStore
	if !(*v).Status().Locked || !(*v).Status().UpdateHeld {
		t.Fatal("restart lost the original locked hold")
	}
	p, err := preflight()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (*store).BackupMetadata(ctx, p); err == nil {
		t.Fatal("the predecessor unexpectedly accepted a new backup after its journal advanced")
	}
	noBackup := func(context.Context, update.Preflight) (update.RecoveryIndex, error) {
		t.Fatal("resume attempted another backup")
		return update.RecoveryIndex{}, nil
	}
	got, held, err = installationRecoveryIndex(ctx, id, m, control, noBackup, call, fresh)
	if err != nil || held == nil || held.Digest != update.InstallDigest(binding) || update.SnapshotDigest("index", got) != update.SnapshotDigest("index", index) {
		t.Fatal("did not recover the exact original backup and hold", err)
	}
	if err = verifyOwnerInstallationHold(ctx, id, held, call); err != nil {
		t.Fatal("the unchanged original hold was refused", err)
	}
	healthyRetry := func(context.Context, signer.Request) (signer.Response, error) {
		changed := *held
		changed.Phase = "healthy-held"
		return signer.Response{Installation: &changed}, nil
	}
	if got, err := ownerInstallationPhase(ctx, id, "active", m, binding, fresh, healthyRetry); err != nil || got.Phase != "healthy-held" {
		t.Fatal("same-binding healthy hold retry was refused", err)
	}
	wrongPhase := func(context.Context, signer.Request) (signer.Response, error) {
		changed := *held
		changed.Phase = "rolled-back-held"
		return signer.Response{Installation: &changed}, nil
	}
	if _, err = ownerInstallationPhase(ctx, id, "active", m, binding, fresh, wrongPhase); err == nil {
		t.Fatal("a rolled-back installation was accepted as an active hold")
	}
	unavailable := func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{}, errors.New("signer unavailable")
	}
	if verifyOwnerInstallationHold(ctx, id, held, unavailable) == nil || verifyOwnerInstallationHold(ctx, id, nil, unavailable) == nil {
		t.Fatal("a lost final status was mistaken for an unchanged hold or absence")
	}
	ambiguous := func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Installation: held}, signer.ErrNoInstallationHold
	}
	if verifyOwnerInstallationHold(ctx, id, nil, ambiguous) == nil {
		t.Fatal("an ambiguous final status was accepted as absence")
	}
	if _, _, err = installationRecoveryIndex(ctx, id, m, control, noBackup, ambiguous, fresh); err == nil {
		t.Fatal("an ambiguous initial status was accepted as absence")
	}
	foreign := m
	foreign.CoreHash = update.SnapshotDigest("foreign-core", 1)
	if _, _, err = installationRecoveryIndex(ctx, id, foreign, control, noBackup, call, fresh); err == nil {
		t.Fatal("foreign predecessor accepted")
	}
	snapshot := filepath.Join(coreDir, "lifecycle", id, "metadata.sqlite")
	original, err := os.ReadFile(snapshot)
	if err != nil || len(original) == 0 {
		t.Fatal("original recovery snapshot unavailable", err)
	}
	damaged := append([]byte(nil), original...)
	damaged[len(damaged)-1] ^= 1
	if err = os.WriteFile(snapshot, damaged, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = installationRecoveryIndex(ctx, id, m, control, noBackup, call, fresh); err == nil {
		t.Fatal("damaged original recovery snapshot was accepted")
	}
	if err = os.WriteFile(snapshot, original, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	lost := func(ctx context.Context, r signer.Request) (signer.Response, error) {
		if r.Action == "fixture-install-controller" {
			calls++
			if calls == 2 {
				return signer.Response{}, errors.New("status reply lost")
			}
		}
		return call(ctx, r)
	}
	if _, _, err = installationRecoveryIndex(ctx, id, m, control, noBackup, lost, fresh); err == nil || !(*v).Status().Locked || !(*v).Status().UpdateHeld {
		t.Fatal("lost status did not preserve the locked hold", err)
	}
	if _, err = transition("healthy-held"); err != nil {
		t.Fatal("fixture healthy transition", err)
	}
	if got, err := transition("active"); err != nil || got.Phase != "healthy-held" {
		t.Fatal("actual signer refused its idempotent healthy hold retry", err)
	}
	if _, err = transition("rolled-back-held"); err != nil {
		t.Fatal("fixture rollback after recovery", err)
	}
	if err = verifyOwnerInstallationHold(ctx, id, held, call); err == nil {
		t.Fatal("a rolled-back hold was accepted as active")
	}
}

func TestOwnerUpdateDoesNotAcceptAppInventoryFiles(t *testing.T) {
	for _, args := range [][]string{{"owner-updates", "--inventory", "untrusted.json"}, {"owner-updates", "--disposable-fixture", "--prepare-host", "--action", "install"}} {
		var out bytes.Buffer
		if e := run(args, nil, &out, io.Discard); e == nil || out.Len() != 0 {
			t.Fatal("untrusted inventory or installation accepted")
		}
	}
}

func TestColdRecoveryGuardCapabilityIsExact(t *testing.T) {
	if err := run([]string{"fixture-update-pause-guard", "--recovery-capability"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal("cold recovery capability was not available", err)
	}
	if err := run([]string{"fixture-update-pause-guard", "--recovery-capability", "--owner", "1"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("capability probe accepted operational arguments")
	}
	if err := run([]string{"fixture-update-pause-guard", "--staged-recovery-capability"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal("staged recovery capability was not available", err)
	}
	if err := run([]string{"fixture-update-pause-guard", "--staged-recovery-capability", "--owner", "1"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("staged capability probe accepted operational arguments")
	}
}
