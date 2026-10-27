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
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestProtectedUpdateReviewCancelStalenessAndCommit(t *testing.T) {
	for _, tc := range []string{"cancel", "changed-core", "changed-signer", "bad-artifact", "prepare", "stage", "stage-stale-core", "stage-bad-source", "preflight", "preflight-owner-interrupted", "preflight-corrupt-reply", "preflight-core-released", "preflight-abort-interrupted", "backup", "backup-interrupted", "backup-corrupt-reply", "backup-signer-aborted", "backup-staged-corrupt"} {
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
			defer v.Close()
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
			defer store.Close()
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
				m.Components = append(m.Components, update.Component{Version: "0.0.0-fixture.1", Contract: c, Artifact: update.Artifact{File: c.Component, Size: int64(len(contents)), SHA256: hex.EncodeToString(h[:])}})
				if e = os.WriteFile(filepath.Join(artifactDir, c.Component), contents, 0600); e != nil {
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
							if tc == "preflight-owner-interrupted" && !interrupted && c.Action == "pause" && e == nil {
								interrupted = true
								return update.CorePauseReceipt{}, errors.New("owner helper interrupted after durable pause")
							}
							return p, e
						}
						out.Reset()
						e = conductPreflight(t.Context(), id, control, call, &out)
						if tc == "preflight-owner-interrupted" {
							if e == nil || out.Len() != 0 {
								t.Fatal("interruption returned proof")
							}
							held, statusErr := store.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
							if statusErr != nil || held.State != "paused" {
								t.Fatal("owner interruption released core", statusErr)
							}
							e = conductPreflight(t.Context(), id, control, call, &out)
						}
						if tc == "preflight" || tc == "preflight-owner-interrupted" || tc == "preflight-abort-interrupted" || strings.HasPrefix(tc, "backup") {
							var p update.Preflight
							if e != nil || json.Unmarshal(out.Bytes(), &p) != nil || !p.Verified || p.InstallAllowed || p.ResumeAllowed {
								t.Fatal("owner preflight failed", e)
							}
						} else if e == nil || out.Len() != 0 {
							t.Fatal("stale or forged preflight returned proof", e)
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
							if tc == "backup" || tc == "backup-interrupted" {
								var i update.RecoveryIndex
								if e != nil || json.Unmarshal(out.Bytes(), &i) != nil || i.Phase != "metadata-backed-up" || i.InstallAllowed || i.ResumeAllowed || i.FinancialExecution {
									t.Fatal("owner backup failed", e)
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

func TestOwnerUpdateDoesNotAcceptAppInventoryFiles(t *testing.T) {
	for _, args := range [][]string{{"owner-updates", "--inventory", "untrusted.json"}, {"owner-updates", "--disposable-fixture", "--prepare-host", "--action", "install"}} {
		var out bytes.Buffer
		if e := run(args, nil, &out, io.Discard); e == nil || out.Len() != 0 {
			t.Fatal("untrusted inventory or installation accepted")
		}
	}
}
