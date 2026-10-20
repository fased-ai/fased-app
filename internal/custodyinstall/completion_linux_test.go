//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

var completionPhases = []string{"sequence-committed", "completion-intent.json", "completion-ready.json", "completion-owner", "signer-completed", "core-released", "completed"}

func completionModel(t *testing.T, x InstallTransaction, dir string) installOps {
	o := modelOps(t, x, dir)
	o.ownerRecord = func(write bool) error {
		p := dir + "/owner-intent.json"
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		if string(b) != string(mustJSON(installerIntent(x.Base))) && string(b) != string(mustJSON(installerIntent(x.Target))) {
			return errors.New("foreign owner intent")
		}
		if write {
			return os.WriteFile(p, mustJSON(installerIntent(x.Target)), 0600)
		}
		return nil
	}
	o.release = func(_ context.Context, i update.Installation) error {
		actual, e := o.state(t.Context())
		if e != nil || actual.Phase != "completed" || actual.Digest != i.Digest {
			return errors.New("not actual completion")
		}
		return os.WriteFile(dir+"/core-released", []byte(i.Digest), 0600)
	}
	hold := o.hold
	o.hold = func(ctx context.Context, phase string, b update.InstallBinding) (*update.Installation, error) {
		i, e := hold(ctx, phase, b)
		if e != nil {
			return nil, e
		}
		i.InstalledSequenceCommitted = phase == "committed-held" || phase == "completed"
		return i, os.WriteFile(dir+"/signer-hold.json", mustJSON(i), 0600)
	}
	return o
}
func prepareCompletionModel(t *testing.T) (InstallTransaction, string) {
	x, dir := modelTransaction(t)
	o := completionModel(t, x, dir)
	for _, p := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"intent.json", mustJSON(installerIntent(x.Base)), 0644}, {"ready.json", readyRecord(x.Base), 0644}, {"owner-intent.json", mustJSON(installerIntent(x.Base)), 0600},
	} {
		if e := os.WriteFile(dir+"/"+p.name, p.data, p.mode); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := runTransaction(t.Context(), x, false, o); e != nil {
		t.Fatal(e)
	}
	return x, dir
}
func TestCompletionInterruptionAndCurrentRetry(t *testing.T) {
	for _, phase := range completionPhases {
		t.Run(phase, func(t *testing.T) {
			x, dir := prepareCompletionModel(t)
			o := completionModel(t, x, dir)
			before, _ := os.ReadFile(dir + "/financial-history")
			o.checkpoint = func(at string) error {
				if at == phase {
					return syscall.ENOSPC
				}
				return nil
			}
			if _, e := runCompletion(t.Context(), x, o); !errors.Is(e, syscall.ENOSPC) {
				t.Fatal("fault missed", e)
			}
			o = completionModel(t, x, dir)
			for n := 0; n < 2; n++ {
				i, e := runCompletion(t.Context(), x, o)
				if e != nil || i.Phase != "completed" || !i.InstalledSequenceCommitted || i.ResumeAllowed || i.FinancialExecution {
					t.Fatal(i, e)
				}
			}
			if _, e := os.Stat(dir + "/update-active.json"); !os.IsNotExist(e) {
				t.Fatal("active remains", e)
			}
			if b, _ := os.ReadFile(dir + "/financial-history"); string(b) != string(before) {
				t.Fatal("financial rewind")
			}
			if _, e := runTransaction(t.Context(), x, true, o); e == nil {
				t.Fatal("committed rollback permitted")
			}
		})
	}
}
func TestCompletionRejectsHealthForeignAndSuperseded(t *testing.T) {
	for _, bad := range []string{"health", "owner", "ready", "active", "superseded"} {
		t.Run(bad, func(t *testing.T) {
			x, dir := prepareCompletionModel(t)
			o := completionModel(t, x, dir)
			name := map[string]string{"health": "failed-health", "owner": "owner-intent.json", "ready": "ready.json", "active": "update-active.json", "superseded": "aborted"}[bad]
			os.WriteFile(dir+"/"+name, []byte("foreign"), 0600)
			if _, e := runCompletion(t.Context(), x, o); e == nil {
				t.Fatal("unsafe completion")
			}
			i, e := o.state(t.Context())
			if bad != "superseded" && (e != nil || i.Phase != "healthy-held") {
				t.Fatal("committed before validation", i, e)
			}
		})
	}
}
func TestCompletionActualChildDeath(t *testing.T) {
	if phase := os.Getenv("FASED_COMPLETION_TEST_PHASE"); phase != "" {
		var x InstallTransaction
		dir := os.Getenv("FASED_COMPLETION_TEST_DIR")
		b, e := os.ReadFile(dir + "/updates/" + strings.Repeat("b", 32) + "/transaction.json")
		if e != nil || strict(b, &x) != nil {
			t.Fatal(e)
		}
		o := completionModel(t, x, dir)
		o.checkpoint = func(at string) error {
			if at == phase {
				os.Exit(79)
			}
			return nil
		}
		_, e = runCompletion(t.Context(), x, o)
		t.Fatal("death missed", e)
	}
	for _, phase := range completionPhases {
		t.Run(phase, func(t *testing.T) {
			x, dir := prepareCompletionModel(t)
			abs, e := filepath.Abs(dir)
			if e != nil {
				t.Fatal(e)
			}
			command := exec.Command(os.Args[0], "-test.run=^TestCompletionActualChildDeath$")
			command.Env = append(os.Environ(), "FASED_COMPLETION_TEST_PHASE="+phase, "FASED_COMPLETION_TEST_DIR="+abs)
			if out, e := command.CombinedOutput(); e == nil {
				t.Fatalf("death missed %s", out)
			} else if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() != 79 {
				t.Fatalf("wrong child failure %v %s", e, out)
			}
			if i, e := runCompletion(t.Context(), x, completionModel(t, x, dir)); e != nil || i.Phase != "completed" {
				t.Fatal(i, e)
			}
		})
	}
}

func TestCompletionFailedHealthAfterCommitRetainsState(t *testing.T) {
	x, dir := prepareCompletionModel(t)
	o := completionModel(t, x, dir)
	o.checkpoint = func(at string) error {
		if at == "sequence-committed" {
			return syscall.ENOSPC
		}
		return nil
	}
	if _, e := runCompletion(t.Context(), x, o); !errors.Is(e, syscall.ENOSPC) {
		t.Fatal(e)
	}
	if e := os.WriteFile(dir+"/failed-health", nil, 0600); e != nil {
		t.Fatal(e)
	}
	o = completionModel(t, x, dir)
	if _, e := runCompletion(t.Context(), x, o); e == nil {
		t.Fatal("failed health completed")
	}
	i, e := o.state(t.Context())
	if e != nil || i.Phase != "committed-held" || !i.InstalledSequenceCommitted {
		t.Fatal(i, e)
	}
	if _, e := os.Stat(dir + "/update-active.json"); e != nil {
		t.Fatal("hold marker lost", e)
	}
	if _, e := runTransaction(t.Context(), x, true, o); e == nil {
		t.Fatal("post-commit rollback")
	}
}
