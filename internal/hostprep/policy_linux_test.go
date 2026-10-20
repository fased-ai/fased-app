package hostprep

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestIncompleteIntentDoesNotAuthorizePublication(t *testing.T) {
	root, _ := filepath.Abs("../../.evidence/hostprep-fixtures")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	d, e := os.MkdirTemp(root, "invalid-intent-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	if e = core.WritePrivateJSON(filepath.Join(d, "host-preparation.json"), receipt{Schema: 1, Phase: "ready"}); e != nil {
		t.Fatal(e)
	}
	policy, e := os.ReadFile("../../packaging/linux/apparmor/bwrap-userns-restrict")
	if e != nil {
		t.Fatal(e)
	}
	h := &fakeHost{files: map[string]hostFile{}}
	if e = preparePolicy(t.Context(), d, policy, h.read, h.run, func() error { return errors.New("not ready") }); e == nil {
		t.Fatal("incomplete intent was silently reinitialized")
	}
	if len(h.files) != 0 {
		t.Fatal("invalid intent caused host-file publication")
	}
}

func TestOSCopyAcceptsGoPipe(t *testing.T) {
	root, _ := filepath.Abs("../../.evidence/hostprep-fixtures")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	d, e := os.MkdirTemp(root, "pipe-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	b, e := os.ReadFile("../../packaging/linux/apparmor/bwrap-userns-restrict")
	if e != nil {
		t.Fatal(e)
	}
	c := exec.Command("/usr/bin/install", "-m", "0644", "--", "/dev/stdin", filepath.Join(d, "policy"))
	c.Stdin = bytes.NewReader(b)
	if _, e = c.Output(); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(filepath.Join(d, "policy"))
	if e != nil || !bytes.Equal(got, b) {
		t.Fatal("Go pipe did not preserve policy bytes")
	}
}

type fakeHost struct {
	files  map[string]hostFile
	loaded string
	calls  []string
	fail   string
	ready  bool
}

func (h *fakeHost) read(p string) (hostFile, error) {
	f, ok := h.files[p]
	if !ok {
		return f, os.ErrNotExist
	}
	return f, nil
}
func (h *fakeHost) run(_ context.Context, input []byte, args ...string) ([]byte, error) {
	h.calls = append(h.calls, strings.Join(args, " "))
	if args[0] == "/usr/bin/cat" {
		return []byte(h.loaded), nil
	}
	if args[0] == "/usr/bin/install" {
		p := args[len(args)-1]
		h.files[p] = hostFile{Data: append([]byte(nil), input...), Root: true, Regular: true, Mode: 0644}
		if h.fail == "copy" {
			h.files[p] = hostFile{Data: input[:9], Root: true, Regular: true, Mode: 0644}
			h.fail = ""
			return nil, errors.New("interrupted copy")
		}
	}
	if args[0] == "/usr/bin/ln" {
		p := args[len(args)-1]
		if _, ok := h.files[p]; ok {
			return nil, os.ErrExist
		}
		h.files[p] = h.files[args[len(args)-2]]
	}
	if args[0] == "/usr/bin/rm" {
		delete(h.files, args[len(args)-1])
	}
	if args[0] == "/usr/sbin/apparmor_parser" {
		if h.fail == "load" {
			h.fail = ""
			return nil, errors.New("interrupted load")
		}
		h.loaded = "bwrap (enforce)\nunpriv_bwrap (enforce)\n"
		h.ready = true
	}
	return nil, nil
}
func TestPolicyPreparationPreservesConflictsAndResumes(t *testing.T) {
	root, _ := filepath.Abs("../../.evidence/hostprep-fixtures")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	for _, failure := range []string{"copy", "load"} {
		t.Run(failure, func(t *testing.T) {
			d, e := os.MkdirTemp(root, "retry-")
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { os.RemoveAll(d) })
			policy, e := os.ReadFile("../../packaging/linux/apparmor/bwrap-userns-restrict")
			if e != nil {
				t.Fatal(e)
			}
			h := &fakeHost{files: map[string]hostFile{}, fail: failure}
			probe := func() error {
				if h.ready {
					return nil
				}
				return errors.New("not ready")
			}
			if e = preparePolicy(t.Context(), d, policy, h.read, h.run, probe); e == nil {
				t.Fatal("interruption accepted")
			}
			if e = preparePolicy(t.Context(), d, policy, h.read, h.run, probe); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(h.files[policyTarget].Data, policy) {
				t.Fatal("wrong policy installed")
			}
			before := len(h.calls)
			if e = preparePolicy(t.Context(), d, policy, h.read, h.run, probe); e != nil {
				t.Fatal(e)
			}
			if len(h.calls) != before {
				t.Fatal("successful retry mutated host")
			}
			for _, c := range h.calls {
				if strings.Contains(c, " -r ") {
					t.Fatal("policy replaced")
				}
			}
		})
	}
	d, e := os.MkdirTemp(root, "conflict-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	policy, _ := os.ReadFile("../../packaging/linux/apparmor/bwrap-userns-restrict")
	for _, f := range []hostFile{{Data: []byte("administrator policy"), Root: true, Regular: true, Mode: 0644}, {Data: policy, Root: false, Regular: true, Mode: 0644}, {Data: policy, Root: true, Regular: false, Mode: 0644}, {Data: policy, Root: true, Regular: true, Mode: 0666}} {
		h := &fakeHost{files: map[string]hostFile{policyTarget: f}}
		if e = preparePolicy(t.Context(), d, policy, h.read, h.run, func() error { return errors.New("blocked") }); e == nil {
			t.Fatal("foreign file accepted")
		}
		if len(h.calls) != 0 {
			t.Fatal("foreign file caused privileged mutation")
		}
	}
	h := &fakeHost{files: map[string]hostFile{}, loaded: "bwrap (enforce)\n"}
	if e = preparePolicy(t.Context(), d, policy, h.read, h.run, func() error { return errors.New("blocked") }); e == nil {
		t.Fatal("foreign loaded identity adopted")
	}
	if len(h.files) != 0 {
		t.Fatal("foreign loaded profile caused write")
	}
	h = &fakeHost{files: map[string]hostFile{}}
	if e = preparePolicy(t.Context(), d, []byte("edited policy"), h.read, h.run, func() error { return errors.New("blocked") }); e == nil {
		t.Fatal("untrusted privileged input accepted")
	}
	if len(h.calls) != 0 {
		t.Fatal("edited policy reached privileged tools")
	}
}
