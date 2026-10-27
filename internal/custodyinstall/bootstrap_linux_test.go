//go:build linux

package custodyinstall

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func bootstrapModel(t *testing.T) (BootstrapTransaction, string, update.RecoveryIndex) {
	t.Helper()
	x, dir := modelTransaction(t)
	current := update.Manifest{Version: 1, Product: "fased-app", Channel: "development", Sequence: 1, IssuedAt: time.Now().Unix() - 10, ExpiresAt: time.Now().Unix() + 600, Target: update.Target{OS: "linux", Arch: "amd64"}}
	for _, c := range x.Binding.Contracts {
		name := "fased-app"
		data := "next-core"
		if c.Component == "signer" {
			name = "fased-signer"
			data = "next-signer"
		}
		current.Components = append(current.Components, update.Component{Version: "0.0.0-fixture", Contract: c, Artifact: update.Artifact{File: name, Size: int64(len(data)), SHA256: sum([]byte(data))}})
	}
	var prior update.Manifest
	json.Unmarshal(mustJSON(current), &prior)
	for n := range prior.Components {
		c := &prior.Components[n]
		data := "old-core"
		if c.Contract.Component == "signer" {
			data = "old-signer"
		}
		c.Artifact.Size = int64(len(data))
		c.Artifact.SHA256 = sum([]byte(data))
		if c.Contract.Component == "core" {
			c.Contract.Lifecycle.Entrypoint = 3
			c.Contract.Lifecycle.Features = c.Contract.Lifecycle.Features[:5]
		}
		for m := range c.Contract.State {
			if c.Contract.State[m].Name == "operations" {
				c.Contract.State[m].Features = c.Contract.State[m].Features[:len(c.Contract.State[m].Features)-1]
			}
		}
	}
	current.BootstrapFrom = sum(mustJSON(prior))
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	trust := update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: current.IssuedAt - 60, NotAfter: current.ExpiresAt + 60}}}
	signed := func(m update.Manifest) []byte {
		b := mustJSON(m)
		return mustJSON(update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: b, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), b...))})
	}
	tx := BootstrapTransaction{Version: 1, ID: strings.Repeat("b", 32), Base: x.Base, Target: x.Target, ReviewedAt: time.Now().Unix(), Trust: mustJSON(trust), Bundle: update.BootstrapBundle{Version: 1, Maintenance: signed(prior), Target: signed(current)}}
	tx.Core = update.CoreSnapshot{Version: 1, ProbeID: tx.ID, BootID: tx.ID, InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: 3, Features: prior.Components[0].Contract.State[0].Features, Anchor: sum([]byte("metadata"))}}
	tx.Signer = update.SignerSnapshot{Version: 1, ProbeID: tx.ID, BootID: tx.ID, AuthoritySHA256: sum([]byte("authority")), Spent: 70, Held: 30, Pending: 1, Unknown: 1, RecoveryHold: true}
	for _, v := range prior.Components[1].Contract.State {
		tx.Signer.States = append(tx.Signer.States, update.State{Name: v.Name, Version: v.Versions.Min, Features: v.Features, Anchor: sum([]byte(v.Name))})
	}
	if _, e = tx.Plan(); e != nil {
		t.Fatal(e)
	}
	write := func(path string, b []byte, mode os.FileMode) {
		t.Helper()
		p := dir + strings.TrimPrefix(path, tx.Base.Root)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, b, mode); e != nil {
			t.Fatal(e)
		}
	}
	for _, v := range []struct{ group, core, signer string }{{"previous", "old-core", "old-signer"}, {"candidate", "next-core", "next-signer"}} {
		write(tx.Dir()+"/"+v.group+"/fased-app", []byte(v.core), 0755)
		write(tx.Dir()+"/"+v.group+"/fased-signer", []byte(v.signer), 0755)
	}
	write(tx.Dir()+"/transaction.json", mustJSON(tx), 0644)
	for _, v := range []struct {
		name string
		b    []byte
		mode os.FileMode
	}{{"intent.json", mustJSON(installerIntent(tx.Base)), 0644}, {"ready.json", readyRecord(tx.Base), 0644}, {"owner-intent.json", mustJSON(installerIntent(tx.Base)), 0600}} {
		write(tx.Base.Root+"/"+v.name, v.b, v.mode)
	}
	index := x.Binding.Index
	index.Binding.Pause.Request.ManifestSHA256 = sum(mustJSON(prior))
	index.Binding.Pause.Request.Expected = tx.Core
	index.BindingSHA256 = update.RecoveryDigest(index.Binding)
	write(tx.Dir()+"/model-index.json", mustJSON(index), 0644)
	return tx, dir, index
}
func bootstrapOps(t *testing.T, tx BootstrapTransaction, dir string) (installOps, BootstrapCallbacks) {
	x := InstallTransaction{Base: tx.Base, Target: tx.Target}
	o := modelOps(t, x, dir)
	o.ownerRecord = func(write bool) error {
		p := dir + "/owner-intent.json"
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		want := mustJSON(installerIntent(tx.Target))
		if !write && string(b) != string(want) {
			return errors.New("owner record unconverged")
		}
		if write {
			if string(b) != string(mustJSON(installerIntent(tx.Base))) && string(b) != string(want) {
				return errors.New("foreign owner record")
			}
			return os.WriteFile(p, want, 0600)
		}
		return nil
	}
	var index update.RecoveryIndex
	b, e := os.ReadFile(dir + strings.TrimPrefix(tx.Dir(), tx.Base.Root) + "/model-index.json")
	if e != nil || json.Unmarshal(b, &index) != nil {
		t.Fatal(e)
	}
	c := BootstrapCallbacks{Maintain: func(context.Context, BootstrapTransaction) (update.RecoveryIndex, error) { return index, nil }, Observe: func(_ context.Context, _ BootstrapTransaction, _ update.RecoveryIndex, _ bool, allowAborted bool) error {
		if _, e := os.Stat(dir + "/superseded"); e == nil {
			return errors.New("new financial head or floor")
		}
		if _, e := os.Stat(dir + "/aborted"); e == nil && !allowAborted {
			return errors.New("inactive maintenance")
		}
		return nil
	}, Finish: func(context.Context, BootstrapTransaction, update.RecoveryIndex) error {
		return os.WriteFile(dir+"/aborted", nil, 0600)
	}}
	return o, c
}

var bootstrapPhases = []string{"core-replaced", "pair-replaced", "started", "installer-intent.json", "installer-ready.json", "installer-owner", "forward-only", "released", "completed"}

func TestBootstrapInterruptionCurrentAndStale(t *testing.T) {
	for _, phase := range bootstrapPhases {
		t.Run(phase, func(t *testing.T) {
			tx, dir, _ := bootstrapModel(t)
			o, c := bootstrapOps(t, tx, dir)
			before, _ := os.ReadFile(dir + "/financial-history")
			o.checkpoint = func(at string) error {
				if at == phase {
					return syscall.ENOSPC
				}
				return nil
			}
			if _, e := runBootstrap(t.Context(), tx, c, o); !errors.Is(e, syscall.ENOSPC) {
				t.Fatal("fault missed", e)
			}
			o, c = bootstrapOps(t, tx, dir)
			for n := 0; n < 2; n++ {
				r, e := runBootstrap(t.Context(), tx, c, o)
				if e != nil || r.Phase != "reader-upgraded" || r.InstalledSequenceCommitted || r.ResumeAllowed || r.FinancialExecution {
					t.Fatal(r, e)
				}
			}
			if b, _ := os.ReadFile(dir + "/financial-history"); string(b) != string(before) {
				t.Fatal("financial state changed")
			}
			os.WriteFile(dir+"/superseded", nil, 0600)
			if _, e := runBootstrap(t.Context(), tx, c, o); e == nil {
				t.Fatal("stale bootstrap replay")
			}
		})
	}
}
func TestBootstrapFailedHealthAndForeignRecords(t *testing.T) {
	for _, bad := range []string{"health", "foreign"} {
		t.Run(bad, func(t *testing.T) {
			tx, dir, _ := bootstrapModel(t)
			o, c := bootstrapOps(t, tx, dir)
			if bad == "health" {
				os.WriteFile(dir+"/failed-health", nil, 0600)
			} else {
				os.WriteFile(dir+"/fased-signer", []byte("foreign"), 0755)
			}
			r, e := runBootstrap(t.Context(), tx, c, o)
			if bad == "foreign" {
				if e == nil {
					t.Fatal("foreign file replaced")
				}
				return
			}
			if e != nil || r.Phase != "rolled-back" {
				t.Fatal(r, e)
			}
			if f, _ := os.ReadFile(dir + "/fased-app"); sum(f) != tx.Base.CoreHash {
				t.Fatal("old pair not restored")
			}
			if _, e = os.Stat(dir + "/aborted"); e != nil {
				t.Fatal("maintenance not aborted", e)
			}
		})
	}
}
func TestBootstrapActualChildDeath(t *testing.T) {
	if phase := os.Getenv("FASED_BOOTSTRAP_DEATH"); phase != "" {
		dir := os.Getenv("FASED_BOOTSTRAP_DIR")
		var tx BootstrapTransaction
		b, e := os.ReadFile(dir + "/bootstraps/" + strings.Repeat("b", 32) + "/transaction.json")
		if e != nil || json.Unmarshal(b, &tx) != nil {
			t.Fatal(e)
		}
		o, c := bootstrapOps(t, tx, dir)
		o.checkpoint = func(at string) error {
			if at == phase {
				os.Exit(79)
			}
			return nil
		}
		_, e = runBootstrap(t.Context(), tx, c, o)
		t.Fatal("death missed", e)
	}
	for _, phase := range bootstrapPhases {
		t.Run(phase, func(t *testing.T) {
			tx, dir, _ := bootstrapModel(t)
			abs, e := filepath.Abs(dir)
			if e != nil {
				t.Fatal(e)
			}
			child := exec.Command(os.Args[0], "-test.run=^TestBootstrapActualChildDeath$")
			child.Env = append(os.Environ(), "FASED_BOOTSTRAP_DEATH="+phase, "FASED_BOOTSTRAP_DIR="+abs)
			if b, e := child.CombinedOutput(); e == nil {
				t.Fatalf("death missed %s", b)
			} else if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() != 79 {
				t.Fatalf("wrong death %v %s", e, b)
			}
			o, c := bootstrapOps(t, tx, dir)
			if r, e := runBootstrap(t.Context(), tx, c, o); e != nil || r.Phase != "reader-upgraded" {
				t.Fatal(r, e)
			}
		})
	}
}

func TestBootstrapNeverRestartsOldReaderAfterHistoryChanges(t *testing.T) {
	tx, dir, _ := bootstrapModel(t)
	o, c := bootstrapOps(t, tx, dir)
	health := o.health
	o.health = func(ctx context.Context, m Manifest) error {
		if m.CoreHash == tx.Target.CoreHash {
			if e := os.WriteFile(dir+"/superseded", nil, 0600); e != nil {
				return e
			}
			return errors.New("new financial history detected during failure")
		}
		return health(ctx, m)
	}
	if _, e := runBootstrap(t.Context(), tx, c, o); e == nil {
		t.Fatal("unexpected history permitted rollback")
	}
	f, e := os.ReadFile(dir + "/fased-app")
	if e != nil || sum(f) != tx.Target.CoreHash {
		t.Fatal("old reader restored despite new history", e)
	}
}
