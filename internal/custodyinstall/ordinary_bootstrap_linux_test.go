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
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func ordinaryBootstrapModel(t *testing.T) (BootstrapTransaction, string, installOps, ordinaryBootstrapOps) {
	x, dir := modelTransaction(t)
	b := x.Base
	b.Version = 3
	b.Mode = signer.OrdinaryCustodyMode
	b.Profile = signer.OwnerCustodyProfile
	b.Provenance = signer.ControlledCustodyProfile
	b.Network = "devnet"
	b.Enrollment = true
	b.Location = "local"
	b.NativeOwnerAccess = true
	next := b
	next.CoreHash = x.Target.CoreHash
	next.SignerHash = x.Target.SignerHash
	at := time.Now().Unix()
	prior := update.Manifest{Version: 1, Product: "fased-app", Channel: "development", Sequence: 7, IssuedAt: at - 10, ExpiresAt: at + 600, Target: update.Target{OS: "linux", Arch: "amd64"}}
	target := prior
	for _, c := range []update.Contract{core.UpdateContract(), signer.OrdinaryCustodyReadContract()} {
		file, data := "fased-app", "old-core"
		if c.Component == "signer" {
			file, data = "fased-signer", "old-signer"
		}
		prior.Components = append(prior.Components, update.Component{Version: "0.0.0-fixture", Contract: c, Artifact: update.Artifact{File: file, Size: int64(len(data)), SHA256: sum([]byte(data))}})
	}
	for _, c := range []update.Contract{core.OrdinaryUpdateContract(), signer.OrdinaryUpdateContract()} {
		file, data := "fased-app", "next-core"
		if c.Component == "signer" {
			file, data = "fased-signer", "next-signer"
		}
		target.Components = append(target.Components, update.Component{Version: "0.0.0-fixture", Contract: c, Artifact: update.Artifact{File: file, Size: int64(len(data)), SHA256: sum([]byte(data))}})
	}
	target.BootstrapFrom = sum(mustJSON(prior))
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	trust := update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: at - 60, NotAfter: at + 1000}}}
	sign := func(m update.Manifest) []byte {
		p := mustJSON(m)
		return mustJSON(update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: p, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), p...))})
	}
	tx := BootstrapTransaction{Version: 2, ID: strings.Repeat("b", 32), Base: b, Target: next, ReviewedAt: at, Trust: mustJSON(trust), Bundle: update.BootstrapBundle{Version: 2, Maintenance: sign(prior), Target: sign(target)}}
	tx.Core = update.CoreSnapshot{Version: 1, ProbeID: tx.ID, BootID: tx.ID, InstanceID: strings.Repeat("A", 43), State: update.State{Name: "core-db", Version: core.StateSchema, Features: core.UpdateContract().State[0].Features, Anchor: sum([]byte("preserved auth/accounts/tasks/RPC"))}}
	if _, e = tx.Plan(); e != nil {
		t.Fatal(e)
	}
	path := func(p string) string { return dir + strings.TrimPrefix(p, b.Root) }
	write := func(p string, bytes []byte, mode os.FileMode) {
		t.Helper()
		p = path(p)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, bytes, mode); e != nil {
			t.Fatal(e)
		}
	}
	for _, v := range []struct{ group, core, signer string }{{"previous", "old-core", "old-signer"}, {"candidate", "next-core", "next-signer"}} {
		write(tx.Dir()+"/"+v.group+"/fased-app", []byte(v.core), 0755)
		write(tx.Dir()+"/"+v.group+"/fased-signer", []byte(v.signer), 0755)
	}
	for _, v := range []struct {
		name string
		data []byte
	}{{"install.json", mustJSON(b)}, {"intent.json", mustJSON(installerIntent(b))}, {"ready.json", readyRecord(b)}, {"owner-intent.json", mustJSON(installerIntent(b))}, {"enrollment-ready.json", mustJSON(EnrollmentCommit{Version: 1, InstanceID: tx.Core.InstanceID, Digest: sum([]byte("enrollment")), ManifestSHA256: sum(mustJSON(EnrollmentIdentity(b)))})}} {
		write(b.Root+"/"+v.name, v.data, 0644)
	}
	write(tx.Dir()+"/transaction.json", mustJSON(tx), 0644)
	o := modelOps(t, InstallTransaction{Base: b, Target: next}, dir)
	o.ownerRecord = func(w bool) error {
		data, e := os.ReadFile(dir + "/owner-intent.json")
		if e != nil {
			return e
		}
		if string(data) != string(mustJSON(installerIntent(b))) && string(data) != string(mustJSON(installerIntent(next))) {
			return errors.New("foreign owner intent")
		}
		if w {
			return os.WriteFile(dir+"/owner-intent.json", mustJSON(installerIntent(next)), 0600)
		}
		return nil
	}
	o.ownerCurrent = func(m Manifest) error {
		data, e := os.ReadFile(dir + "/owner-intent.json")
		if e != nil || string(data) != string(mustJSON(installerIntent(m))) {
			return errors.New("owner generation differs")
		}
		return nil
	}
	before := update.SignerSnapshot{Version: 3, ProbeID: tx.ID, BootID: tx.ID, AuthoritySHA256: sum([]byte("key admission authority")), RecoveryHold: true, States: []update.State{{Name: "vault", Version: 4, Features: []string{"ordinary-custody-admission-v1", "key-only-recovery-hold-v1", "owner-profile-v1", "controlled-provenance-v1"}, Anchor: sum([]byte("unchanged encrypted key"))}, {Name: "operations", Version: 1, Features: []string{"ordinary-key-genesis-v1"}, Anchor: sum([]byte("unchanged genesis"))}}}
	c := ordinaryBootstrapOps{raw: func(context.Context) (string, error) { return sum([]byte("preserved raw sources")), nil }, cold: func(context.Context) (update.CoreSnapshot, update.SignerSnapshot, error) { return tx.Core, before, nil }, commit: func(_ context.Context, a update.OrdinaryHandoffAuthority) (update.SignerSnapshot, error) {
		f, e := o.read(tx.Dir() + "/forward-only.json")
		if e != nil || !known(f, mustJSON(phaseRecord{1, tx.Digest(), "forward-only"}), 0644) {
			return before, errors.New("signer write before fence")
		}
		p := dir + "/handoff-journal.json"
		data, e := os.ReadFile(p)
		if e == nil && string(data) != string(mustJSON(a.Receipt)) {
			return before, errors.New("foreign signer handoff")
		}
		if errors.Is(e, os.ErrNotExist) {
			e = os.WriteFile(p, mustJSON(a.Receipt), 0600)
		}
		if e != nil {
			return before, e
		}
		after := before
		after.SequenceFloor = a.Receipt.Sequence
		after.States = append([]update.State(nil), before.States...)
		after.States[1].Anchor = sum([]byte("handoff record"))
		after.States[1].Features = append(append([]string(nil), before.States[1].Features...), "ordinary-maintenance-v1", "install-completion-v1")
		return after, nil
	}}
	return tx, dir, o, c
}
func TestOrdinaryBootstrapInterruptionForwardOnlyAndPreservation(t *testing.T) {
	for _, phase := range []string{"ordinary-stopped", "ordinary-trial-verified", "forward-only", "ordinary-reader-reserved", "core-replaced", "pair-replaced", "inventory-replaced", "installer-intent.json", "installer-ready.json", "installer-owner", "started", "ordinary-healthy", "completed"} {
		t.Run(phase, func(t *testing.T) {
			tx, dir, o, c := ordinaryBootstrapModel(t)
			original, _ := os.ReadFile(dir + "/financial-history")
			o.checkpoint = func(at string) error {
				if at == phase {
					return syscall.ENOSPC
				}
				return nil
			}
			if _, e := runOrdinaryBootstrap(t.Context(), tx, o, c); !errors.Is(e, syscall.ENOSPC) {
				t.Fatal("fault not reached", e)
			}
			if phase == "ordinary-stopped" || phase == "ordinary-trial-verified" {
				if _, e := os.Stat(dir + "/handoff-journal.json"); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("pre-fence signer mutation")
				}
				b, _ := os.ReadFile(dir + "/fased-app")
				if string(b) != "old-core" {
					t.Fatal("pre-fence reader replaced")
				}
			}
			o.checkpoint = nil
			r, e := runOrdinaryBootstrap(t.Context(), tx, o, c)
			if e != nil || r.Phase != "reader-upgraded" || r.InstalledSequenceCommitted || r.FinancialExecution {
				t.Fatal(r, e)
			}
			r, e = runOrdinaryBootstrap(t.Context(), tx, o, c)
			if e != nil || !r.AlreadyCurrent {
				t.Fatal("same transaction no-op", r, e)
			}
			now, _ := os.ReadFile(dir + "/financial-history")
			if string(now) != string(original) {
				t.Fatal("financial state rewritten")
			}
			var m Manifest
			raw, _ := os.ReadFile(dir + "/install.json")
			json.Unmarshal(raw, &m)
			if m != tx.Target || !m.NativeOwnerAccess {
				t.Fatal("topology/native access changed")
			}
		})
	}
}
func TestOrdinaryBootstrapRefusesForeignAndPostFenceHealthFailure(t *testing.T) {
	for _, bad := range []string{"core-drift", "genesis-drift", "enrollment", "owner", "authority", "post-fence-health"} {
		t.Run(bad, func(t *testing.T) {
			tx, dir, o, c := ordinaryBootstrapModel(t)
			switch bad {
			case "core-drift":
				old := c.cold
				c.cold = func(ctx context.Context) (update.CoreSnapshot, update.SignerSnapshot, error) {
					a, b, e := old(ctx)
					a.State.Anchor = sum([]byte("foreign"))
					return a, b, e
				}
			case "genesis-drift":
				old := c.cold
				n := 0
				c.cold = func(ctx context.Context) (update.CoreSnapshot, update.SignerSnapshot, error) {
					a, b, e := old(ctx)
					n++
					if n > 1 {
						b.States = append([]update.State(nil), b.States...)
						b.States[1].Anchor = sum([]byte("changed"))
					}
					return a, b, e
				}
			case "enrollment":
				os.WriteFile(dir+"/enrollment-ready.json", []byte("{}"), 0644)
			case "owner":
				os.WriteFile(dir+"/owner-intent.json", []byte("{}"), 0600)
			case "authority":
				tx.Base.Owner = 2000
			case "post-fence-health":
				os.WriteFile(dir+"/failed-health", nil, 0600)
			}
			if _, e := runOrdinaryBootstrap(t.Context(), tx, o, c); e == nil {
				t.Fatal("bad source accepted", bad)
			}
			if bad == "post-fence-health" {
				b, _ := os.ReadFile(dir + "/fased-app")
				if string(b) != "next-core" {
					t.Fatal("old reader restored after signer append")
				}
				os.Remove(dir + "/failed-health")
				if _, e := runOrdinaryBootstrap(t.Context(), tx, o, c); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}

func TestOrdinaryPairedControllerAndCompletionPreserveNativeOwnership(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "rollback"}[rollback], func(t *testing.T) {
			tx, dir, _, cold := ordinaryBootstrapModel(t)
			x, _ := modelTransaction(t)
			x.Base, x.Target = tx.Base, tx.Target
			x.Binding.InstallationID = tx.Base.ID
			x.Binding.PreviousManifest = sum(mustJSON(tx.Base))
			x.Binding.Contracts = []update.Contract{core.OrdinaryUpdateContract(), signer.OrdinaryUpdateContract()}
			_, s, e := cold.cold(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			s.ActiveIntentID = x.Binding.Index.Binding.Pause.Request.IntentID
			s.SequenceFloor = x.Binding.Index.Binding.Pause.Request.Sequence
			x.Binding.Index.Binding.Signer = s
			x.Binding.Index.BindingSHA256 = update.RecoveryDigest(x.Binding.Index.Binding)
			if !x.valid() {
				t.Fatal("ordinary paired controller refused")
			}
			write := func(name string, data []byte, mode os.FileMode) {
				t.Helper()
				p := dir + strings.TrimPrefix(x.dir()+"/"+name, x.Base.Root)
				os.MkdirAll(filepath.Dir(p), 0700)
				if e := os.WriteFile(p, data, mode); e != nil {
					t.Fatal(e)
				}
			}
			for _, f := range []struct{ name, data string }{{"runner", "old-core"}, {"previous-signer", "old-signer"}, {"next-core", "next-core"}, {"next-signer", "next-signer"}} {
				write(f.name, []byte(f.data), 0755)
			}
			o := completionModel(t, x, dir)
			if _, e = runTransaction(t.Context(), x, false, o); e != nil {
				t.Fatal("ordinary controller apply", e)
			}
			if rollback {
				if _, e = runTransaction(t.Context(), x, true, o); e != nil {
					t.Fatal("ordinary rollback", e)
				}
			} else {
				if _, e = runCompletion(t.Context(), x, o); e != nil {
					t.Fatal("ordinary completion", e)
				}
			}
			raw, _ := os.ReadFile(dir + "/install.json")
			var m Manifest
			json.Unmarshal(raw, &m)
			want := tx.Target
			if rollback {
				want = tx.Base
			}
			if m != want || !m.NativeOwnerAccess || m.App != tx.Base.App || m.Signer != tx.Base.Signer {
				t.Fatal("paired controller changed custody ownership")
			}
			if !rollback {
				raw, _ = os.ReadFile(dir + "/owner-intent.json")
				var j journal
				json.Unmarshal(raw, &j)
				if !j.NativeOwnerAccess {
					t.Fatal("completion dropped native owner access")
				}
			}
		})
	}
}

func TestOrdinaryHandoffAuthorityRejectsTampering(t *testing.T) {
	tx, _, _, c := ordinaryBootstrapModel(t)
	_, s, e := c.cold(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	files, _ := c.raw(t.Context())
	a, e := ordinaryAuthority(tx, s, files)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"digest", "sequence", "signer", "config-role", "root", "source", "signature", "snapshot"} {
		t.Run(bad, func(t *testing.T) {
			b := a
			raw := mustJSON(tx)
			switch bad {
			case "digest":
				b.Digest = sum([]byte("other"))
			case "sequence":
				b.Receipt.Sequence++
			case "signer":
				b.Receipt.TargetSigner = sum([]byte("other"))
			case "config-role":
				b.Signer = b.App
			case "root":
				b.Root = "/tmp/not-an-installation"
			case "source":
				b.Receipt.PreviousCore = sum([]byte("other"))
			case "signature":
				x := tx
				x.Bundle.Target = []byte("{}")
				raw = mustJSON(x)
			case "snapshot":
				b.Before.RecoveryHold = false
			}
			if b.VerifyTransaction(raw) == nil {
				t.Fatal("tampered handoff descriptor admitted", bad)
			}
		})
	}
}

func TestOrdinaryBootstrapCancellationOnlyBeforeForwardFence(t *testing.T) {
	tx, dir, o, c := ordinaryBootstrapModel(t)
	o.checkpoint = func(at string) error {
		if at == "ordinary-trial-verified" {
			return syscall.ENOSPC
		}
		return nil
	}
	if _, e := runOrdinaryBootstrap(t.Context(), tx, o, c); !errors.Is(e, syscall.ENOSPC) {
		t.Fatal(e)
	}
	o.checkpoint = nil
	r, e := runOrdinaryBootstrap(t.Context(), tx, o, c, true)
	if e != nil || r.Phase != "cancelled" {
		t.Fatal(r, e)
	}
	if _, e = os.Stat(dir + "/handoff-journal.json"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("cancelled trial evolved signer history")
	}
	b, _ := os.ReadFile(dir + "/fased-signer")
	if string(b) != "old-signer" {
		t.Fatal("cancel changed predecessor")
	}
	if r, e = runOrdinaryBootstrap(t.Context(), tx, o, c); e != nil || r.Phase != "cancelled" {
		t.Fatal("cancel restart", r, e)
	}
	tx, dir, o, c = ordinaryBootstrapModel(t)
	o.checkpoint = func(at string) error {
		if at == "forward-only" {
			return syscall.ENOSPC
		}
		return nil
	}
	if _, e = runOrdinaryBootstrap(t.Context(), tx, o, c); !errors.Is(e, syscall.ENOSPC) {
		t.Fatal(e)
	}
	o.checkpoint = nil
	if _, e = runOrdinaryBootstrap(t.Context(), tx, o, c, true); e == nil {
		t.Fatal("cancel crossed committed fence")
	}
	if _, e = runOrdinaryBootstrap(t.Context(), tx, o, c); e != nil {
		t.Fatal("forward recovery failed", e)
	}
	b, _ = os.ReadFile(dir + "/fased-signer")
	if string(b) != "next-signer" {
		t.Fatal("forward recovery restored old signer")
	}
}
