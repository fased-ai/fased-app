//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

// An ordinary-user, persistent filesystem model of fixed trusted OS operations.
// It is supporting interruption proof, not root/systemd acceptance. Ubuntu CI
// separately executes the same controller with actual identities and services.
func modelTransaction(t *testing.T) (InstallTransaction, string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Fatal("project tests cannot run as root")
	}
	parent := filepath.Join("..", "..", ".evidence", "p4-install", "controller-fixtures")
	if e := os.MkdirAll(parent, 0700); e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(parent, "txn-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(dir); e != nil {
			t.Error(e)
		}
	})
	m := Manifest{Version: 1, Mode: "disposable-fixture:v1", ID: strings.Repeat("a", 32), Owner: 1000, Root: Root(1000), Listen: "127.0.0.1:17331", CoreHash: sum([]byte("old-core")), SignerHash: sum([]byte("old-signer")), App: Identity{Name: accountName(1000, "app"), UID: 1001, GID: 1001}, Signer: Identity{Name: accountName(1000, "signer"), UID: 1002, GID: 1002}}
	id := strings.Repeat("b", 32)
	pause := update.CorePauseRequest{Version: 1, IntentID: id, Sequence: 1, ManifestSHA256: sum([]byte("manifest"))}
	index := update.RecoveryIndex{Version: 1, Phase: "metadata-backed-up", Binding: update.RecoveryBinding{Version: 1, Pause: update.CorePauseReceipt{Version: 1, State: "paused", PauseID: strings.Repeat("c", 32), Request: &pause}}, Snapshot: update.MetadataSnapshot{File: "metadata.sqlite", Size: 16, SHA256: sum([]byte("metadata"))}}
	for _, p := range []struct{ name, data string }{{"fased-app", "next-core"}, {"fased-signer", "next-signer"}} {
		a := update.Artifact{File: p.name, Size: int64(len(p.data)), SHA256: sum([]byte(p.data))}
		index.Binding.Files = append(index.Binding.Files, update.StageFile{Artifact: a, Bytes: a.Size})
	}
	index.BindingSHA256 = update.RecoveryDigest(index.Binding)
	x := InstallTransaction{Version: 1, Base: m, Target: m}
	x.Target.CoreHash = sum([]byte("next-core"))
	x.Target.SignerHash = sum([]byte("next-signer"))
	x.Binding = update.InstallBinding{Contracts: []update.Contract{core.UpdateContract(), signer.UpdateContract()}, Version: 1, Index: index, InstallationID: m.ID, PreviousCore: m.CoreHash, PreviousSigner: m.SignerHash, PreviousManifest: sum(mustJSON(m)), TargetCore: x.Target.CoreHash, TargetSigner: x.Target.SignerHash}
	if !x.valid() {
		t.Fatal("invalid model binding")
	}
	write := func(p string, b []byte, mode os.FileMode) {
		t.Helper()
		target := dir + strings.TrimPrefix(p, m.Root)
		if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(target, b, mode); e != nil {
			t.Fatal(e)
		}
	}
	write(m.Root+"/fased-app", []byte("old-core"), 0755)
	write(m.Root+"/fased-signer", []byte("old-signer"), 0755)
	write(m.Root+"/install.json", mustJSON(m), 0644)
	for _, p := range []struct{ name, data string }{{"runner", "old-core"}, {"previous-signer", "old-signer"}, {"next-core", "next-core"}, {"next-signer", "next-signer"}} {
		write(x.dir()+"/"+p.name, []byte(p.data), 0755)
	}
	write(x.dir()+"/transaction.json", mustJSON(x), 0644)
	write(m.Root+"/financial-history", []byte("originals=retained; spent=70 held=30 unknown=1 floor=1"), 0600)
	return x, dir
}
func modelOps(t *testing.T, x InstallTransaction, dir string) installOps {
	t.Helper()
	translate := func(p string) string {
		if !strings.HasPrefix(p, x.Base.Root+"/") && p != x.Base.Root {
			t.Fatalf("unexpected model OS target %q", p)
		}
		return dir + strings.TrimPrefix(p, x.Base.Root)
	}
	read := func(p string) (hostFile, error) {
		target := translate(p)
		st, e := os.Lstat(target)
		if e != nil {
			return hostFile{}, e
		}
		b, e := os.ReadFile(target)
		return hostFile{Data: b, UID: 0, Mode: st.Mode().Perm(), Regular: st.Mode().IsRegular()}, e
	}
	run := func(_ context.Context, b []byte, a ...string) ([]byte, error) {
		switch a[0] {
		case "/usr/bin/install":
			mode := os.FileMode(0644)
			for n, v := range a {
				if v == "-m" && a[n+1] == "755" {
					mode = 0755
				}
			}
			p := translate(a[len(a)-1])
			return nil, os.WriteFile(p, b, mode)
		case "/usr/bin/ln":
			return nil, os.Link(translate(a[len(a)-2]), translate(a[len(a)-1]))
		case "/usr/bin/mv":
			return nil, os.Rename(translate(a[len(a)-2]), translate(a[len(a)-1]))
		case "/usr/bin/rm":
			return nil, os.Remove(translate(a[len(a)-1]))
		case "/usr/bin/sync":
			return nil, nil
		case "/usr/bin/systemctl":
			if a[1] == "show" {
				return []byte("ActiveState=inactive\nMainPID=0\n"), nil
			}
			return nil, nil
		}
		return nil, errors.New("unsupported model OS operation")
	}
	o := installOps{read: read, run: run, topology: func(Manifest) error { return nil }, observe: func(context.Context, Manifest, update.InstallBinding) error { return nil }, health: func(_ context.Context, m Manifest) error {
		if _, e := os.Stat(dir + "/failed-health"); e == nil && m.CoreHash == x.Target.CoreHash {
			return errors.New("candidate unhealthy")
		}
		f, e := read(m.Root + "/fased-app")
		if e != nil || sum(f.Data) != m.CoreHash {
			return errors.New("wrong healthy executable")
		}
		return nil
	}, hold: func(_ context.Context, phase string, b update.InstallBinding) (*update.Installation, error) {
		p := dir + "/signer-hold.json"
		old, e := os.ReadFile(p)
		var i update.Installation
		if e == nil {
			if strict(old, &i) != nil {
				return nil, errors.New("damaged model hold")
			}
			if i.Digest != update.InstallDigest(b) {
				return nil, errors.New("competing model hold")
			}
			if phase == "active" {
				return &i, nil
			}
		}
		i = update.Installation{Version: 1, Binding: b, Digest: update.InstallDigest(b), Phase: phase}
		e = os.WriteFile(p, mustJSON(i), 0600)
		return &i, e
	}}
	o.state = func(context.Context) (*update.Installation, error) {
		if _, e := os.Stat(dir + "/aborted"); e == nil {
			return nil, errors.New("inactive intent")
		}
		b, e := os.ReadFile(dir + "/signer-hold.json")
		if e != nil {
			return nil, e
		}
		var i update.Installation
		if strict(b, &i) != nil {
			return nil, errors.New("invalid model hold")
		}
		return &i, nil
	}
	return o
}
func TestInstallationInterruptionRetryAndFinancialPreservation(t *testing.T) {
	for _, phase := range []string{"held", "stopped", "core-replaced", "pair-replaced", "inventory-replaced", "started", "healthy-held", "rollback", "rollback-stopped", "rollback-core-replaced", "rollback-pair-replaced", "rollback-inventory-replaced", "rollback-started", "rolled-back-held"} {
		t.Run(phase, func(t *testing.T) {
			x, dir := modelTransaction(t)
			o := modelOps(t, x, dir)
			before, _ := os.ReadFile(dir + "/financial-history")
			rollback := strings.HasPrefix(phase, "rollback") || phase == "rolled-back-held"
			if rollback {
				if _, e := runTransaction(t.Context(), x, false, o); e != nil {
					t.Fatal(e)
				}
			}
			o.checkpoint = func(at string) error {
				if at == phase {
					return syscall.ENOSPC
				}
				return nil
			}
			if _, e := runTransaction(t.Context(), x, rollback, o); !errors.Is(e, syscall.ENOSPC) {
				t.Fatal("fault missed", e)
			}
			o = modelOps(t, x, dir)
			i, e := runTransaction(t.Context(), x, false, o)
			if e != nil {
				t.Fatal("same-input recovery", e)
			}
			want := "healthy-held"
			if rollback {
				want = "rolled-back-held"
			}
			if i.Phase != want || i.ResumeAllowed || i.InstalledSequenceCommitted || i.FinancialExecution {
				t.Fatal(i)
			}
			after, _ := os.ReadFile(dir + "/financial-history")
			if string(after) != string(before) {
				t.Fatal("financial bytes changed")
			}
		})
	}
}
func TestInstallationFailedHealthAndForeignBytes(t *testing.T) {
	t.Run("failed-health", func(t *testing.T) {
		x, dir := modelTransaction(t)
		os.WriteFile(dir+"/failed-health", nil, 0600)
		o := modelOps(t, x, dir)
		i, e := runTransaction(t.Context(), x, false, o)
		if e != nil || i.Phase != "rolled-back-held" {
			t.Fatal(i, e)
		}
		f, _ := o.read(x.Base.Root + "/fased-app")
		if sum(f.Data) != x.Base.CoreHash {
			t.Fatal("old executable not restored")
		}
		if _, e = os.Stat(dir + "/update-active.json"); !os.IsNotExist(e) {
			t.Fatal("active slot not released after verified rollback")
		}
	})
	t.Run("foreign", func(t *testing.T) {
		x, dir := modelTransaction(t)
		os.WriteFile(dir+"/fased-signer", []byte("foreign"), 0755)
		o := modelOps(t, x, dir)
		if _, e := runTransaction(t.Context(), x, false, o); e == nil {
			t.Fatal("foreign installed bytes overwritten")
		}
		b, _ := os.ReadFile(dir + "/fased-signer")
		if string(b) != "foreign" {
			t.Fatal("foreign data changed")
		}
	})
}
func TestInstallationActualChildDeath(t *testing.T) {
	if dir := os.Getenv("FASED_INSTALL_MODEL"); dir != "" {
		b, e := os.ReadFile(dir + "/transaction-input.json")
		if e != nil {
			os.Exit(80)
		}
		var x InstallTransaction
		if strict(b, &x) != nil {
			os.Exit(81)
		}
		o := modelOps(t, x, dir)
		o.checkpoint = func(at string) error {
			if at == os.Getenv("FASED_INSTALL_PHASE") {
				os.Exit(79)
			}
			return nil
		}
		runTransaction(t.Context(), x, false, o)
		os.Exit(82)
	}
	for _, phase := range []string{"held", "stopped", "core-replaced", "inventory-replaced", "healthy-held"} {
		t.Run(phase, func(t *testing.T) {
			x, dir := modelTransaction(t)
			os.WriteFile(dir+"/transaction-input.json", mustJSON(x), 0600)
			c := exec.Command(os.Args[0], "-test.run=^TestInstallationActualChildDeath$")
			c.Env = append(os.Environ(), "FASED_INSTALL_MODEL="+dir, "FASED_INSTALL_PHASE="+phase)
			if e := c.Run(); e == nil || c.ProcessState.ExitCode() != 79 {
				t.Fatal("child missed boundary", e)
			}
			i, e := runTransaction(t.Context(), x, false, modelOps(t, x, dir))
			if e != nil || i.Phase != "healthy-held" {
				t.Fatal(i, e)
			}
		})
	}
}

func TestInstallationOwnerLockIsExclusiveAcrossClients(t *testing.T) {
	parent := filepath.Join("..", "..", ".evidence", "p4-install", "lock-fixtures")
	if e := os.MkdirAll(parent, 0700); e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(parent, "owner-")
	if e != nil {
		t.Fatal(e)
	}
	dir, e = filepath.Abs(dir)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("XDG_CONFIG_HOME", dir)
	a, e := OwnerLock()
	if e != nil {
		t.Fatal(e)
	}
	if b, e := OwnerLock(); e == nil {
		b.Close()
		a.Close()
		t.Fatal("competing owner client acquired the lifecycle lock")
	}
	a.Close()
	b, e := OwnerLock()
	if e != nil {
		t.Fatal("dead owner did not release lock", e)
	}
	b.Close()
}

func TestInstallationOldReceiptCannotReplayAfterAbort(t *testing.T) {
	x, dir := modelTransaction(t)
	o := modelOps(t, x, dir)
	if _, e := runTransaction(t.Context(), x, false, o); e != nil {
		t.Fatal(e)
	}
	if _, e := runTransaction(t.Context(), x, true, o); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(dir+"/aborted", nil, 0600)
	if _, e := runTransaction(t.Context(), x, false, o); e == nil {
		t.Fatal("old host receipt replayed after signer abort")
	}
	if _, e := os.Stat(dir + "/update-active.json"); !os.IsNotExist(e) {
		t.Fatal("inactive transaction recreated active host slot")
	}
}
