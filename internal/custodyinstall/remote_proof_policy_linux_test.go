//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRemoteProofPolicyPinsOwnerAndAppOnly(t *testing.T) {
	tx, _, _, _ := ordinaryBootstrapModel(t, true)
	m := tx.Base
	p, err := remoteProofPolicy(m)
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Join(append(appArgs(m, "status"), "--private-proof-stdin"), " ")
	if !strings.Contains(string(p), command+"\n") {
		t.Fatal("missing exact app-UID proof command")
	}
	for _, bad := range []string{"NOPASSWD: ALL", "*", "fased-signer", "--request-stdin", "bash"} {
		if strings.Contains(string(p), bad) {
			t.Fatalf("unexpected proof authority: %q", bad)
		}
	}
	updated, err := remoteProofPolicy(tx.Target)
	if err != nil || !bytes.Equal(p, updated) {
		t.Fatal("paired image update changed the fixed app identity policy")
	}
	local := m
	local.Location, local.PublicOrigin = "local", ""
	if _, err = remoteProofPolicy(local); err == nil {
		t.Fatal("Local installation admitted Remote policy")
	}
	foreign := m
	foreign.App.UID = m.Signer.UID
	if _, err = remoteProofPolicy(foreign); err == nil {
		t.Fatal("signer identity admitted as app")
	}
	if _, err = os.Stat("/usr/sbin/visudo"); err != nil {
		t.Skip("host has no sudo policy validator; Ubuntu CI validates syntax")
	}
	path := filepath.Join(t.TempDir(), "policy")
	if err = os.WriteFile(path, p, 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/usr/sbin/visudo", "-cf", path).CombinedOutput(); err != nil {
		t.Fatal("invalid exact proof policy", err, string(out))
	}
	cvt, err := exec.LookPath("cvtsudoers")
	if err != nil {
		t.Skip("host has no sudo policy converter")
	}
	parsed, err := exec.Command(cvt, "-f", "json", path).Output()
	if err != nil {
		t.Fatal("cannot parse proof policy", err)
	}
	var rules struct {
		Users []struct {
			UserList []struct {
				ID int `json:"userid"`
			} `json:"User_List"`
			Commands []struct {
				Options []struct {
					Authenticate *bool `json:"authenticate"`
				} `json:"Options"`
				Items []struct {
					Command string `json:"command"`
				} `json:"Commands"`
			} `json:"Cmnd_Specs"`
		} `json:"User_Specs"`
	}
	if json.Unmarshal(parsed, &rules) != nil || len(rules.Users) != 1 || len(rules.Users[0].UserList) != 1 || rules.Users[0].UserList[0].ID != m.Owner || len(rules.Users[0].Commands) != 1 {
		t.Fatal("proof policy is not bound to one owner")
	}
	c := rules.Users[0].Commands[0]
	if len(c.Items) != 1 || c.Items[0].Command != command || len(c.Options) != 1 || c.Options[0].Authenticate == nil || *c.Options[0].Authenticate {
		t.Fatal("proof policy admits a different command or needs another password")
	}
}

func TestRemoteProofSetupRequiresCommittedIdleOwner(t *testing.T) {
	tx, _, _, _ := ordinaryBootstrapModel(t, true)
	m := tx.Base
	for _, scenario := range []string{"ready", "wrong-owner", "root", "changed-manifest", "foreign-files", "missing-commit", "active-update", "local"} {
		t.Run(scenario, func(t *testing.T) {
			installed := 0
			o := remoteProofSetupOps{
				uid: m.Owner, euid: m.Owner,
				lock:    func() (io.Closer, error) { return io.NopCloser(strings.NewReader("")), nil },
				load:    func(int) (Manifest, error) { return m, nil },
				verify:  func(Manifest) error { return nil },
				commit:  func(Manifest) (EnrollmentCommit, error) { return EnrollmentCommit{}, nil },
				active:  func(Manifest) error { return nil },
				install: func(context.Context, Manifest) error { installed++; return nil },
			}
			candidate := m
			switch scenario {
			case "wrong-owner":
				o.uid++
			case "root":
				o.euid = 0
			case "changed-manifest":
				o.load = func(int) (Manifest, error) { return tx.Target, nil }
			case "foreign-files":
				o.verify = func(Manifest) error { return errors.New("foreign") }
			case "missing-commit":
				o.commit = func(Manifest) (EnrollmentCommit, error) { return EnrollmentCommit{}, errors.New("missing") }
			case "active-update":
				o.active = func(Manifest) error { return errors.New("held") }
			case "local":
				candidate.Location, candidate.PublicOrigin = "local", ""
				o.load = func(int) (Manifest, error) { return candidate, nil }
			}
			err := preparePrivateAccessProof(t.Context(), candidate, o)
			if scenario == "ready" {
				if err != nil || installed != 1 {
					t.Fatal("exact enrolled setup refused", err, installed)
				}
			} else if err == nil || installed != 0 {
				t.Fatal("unverified setup reached policy publication", err, installed)
			}
		})
	}
}

func TestRemoteProofSkipsOnlyTheRedundantOSPasswordProbe(t *testing.T) {
	for _, tc := range []struct {
		action string
		flags  []string
		want   bool
	}{
		{"status", []string{"--private-proof-stdin"}, true},
		{"status", nil, false},
		{"status", []string{"--private-proof-stdin", "--other"}, false},
		{"agents", []string{"--private-proof-stdin"}, false},
	} {
		if privateProofStatus(tc.action, tc.flags) != tc.want {
			t.Fatal("unexpected OS authorization shortcut", tc)
		}
	}
}

func TestRemoteProofPolicyPublicationPreservesAndRecovers(t *testing.T) {
	tx, _, _, _ := ordinaryBootstrapModel(t, true)
	m := tx.Base
	policy, err := remoteProofPolicy(m)
	if err != nil {
		t.Fatal(err)
	}
	target := remoteProofPolicyPath(m)
	for fail := 1; fail <= 6; fail++ {
		h := &memoryHost{files: map[string]hostFile{}, failAfter: fail}
		run := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
			out, err := h.run(ctx, input, args...)
			if args[0] == "/usr/bin/install" {
				path := args[len(args)-1]
				f := h.files[path]
				f.Mode = 0440
				h.files[path] = f
			}
			return out, err
		}
		if err := publishRemoteProofPolicy(t.Context(), m, h.read, run); err == nil {
			t.Fatalf("interruption %d was not exercised", fail)
		}
		h.failAfter = 0
		if err := publishRemoteProofPolicy(t.Context(), m, h.read, run); err != nil {
			t.Fatal("same policy could not recover", fail, err)
		}
		if !known(h.files[target], policy, 0440) || len(h.files) != 1 {
			t.Fatal("recovery changed or retained policy staging", fail)
		}
		calls := h.calls
		if err := publishRemoteProofPolicy(t.Context(), m, h.read, run); err != nil || h.calls != calls {
			t.Fatal("completed policy was rewritten")
		}
	}
	for _, path := range []string{target, "/etc/sudoers.d/.fased-remote-proof-" + strconv.Itoa(m.Owner) + "-" + m.ID + ".pending"} {
		h := &memoryHost{files: map[string]hostFile{path: {Data: []byte("foreign"), UID: 0, Mode: 0440, Regular: true}}}
		if err := publishRemoteProofPolicy(t.Context(), m, h.read, h.run); err == nil || h.calls != 0 || !bytes.Equal(h.files[path].Data, []byte("foreign")) {
			t.Fatal("foreign policy or staging changed", path, err)
		}
	}
	if err := publishRemoteProofPolicy(t.Context(), m, func(string) (hostFile, error) { return hostFile{}, errors.New("permission unknown") }, func(context.Context, []byte, ...string) ([]byte, error) {
		t.Fatal("unknown policy was mutated")
		return nil, nil
	}); err == nil {
		t.Fatal("unknown policy state accepted")
	}
}
