//go:build linux

package hostprep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type preparationFixture struct {
	files           map[string]preparationFile
	calls           []string
	state, operator string
	fault           string
}

func preparationDirectory(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if e := os.Chmod(p, 0700); e != nil {
		t.Fatal(e)
	}
	return p
}

func setupFixture() *preparationFixture {
	return &preparationFixture{files: map[string]preparationFile{}, state: "NeedsLogin"}
}
func (f *preparationFixture) read(p string) (preparationFile, error) {
	v, ok := f.files[p]
	if !ok {
		return v, os.ErrNotExist
	}
	return v, nil
}
func (f *preparationFixture) run(ctx context.Context, b []byte, args ...string) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	call := strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.fault != "" && strings.Contains(call, f.fault) {
		f.fault = ""
		return nil, errors.New("interrupted fixture OS operation")
	}
	switch args[0] {
	case "/usr/bin/install":
		f.files[args[len(args)-1]] = preparationFile{data: append([]byte{}, b...), root: true, regular: true, mode: 0644}
	case "/usr/bin/ln":
		if _, ok := f.files[args[3]]; ok {
			return nil, os.ErrExist
		}
		f.files[args[3]] = f.files[args[2]]
	case "/usr/bin/rm":
		delete(f.files, args[2])
	case "/usr/bin/apt-get":
		if strings.Contains(call, " install ") {
			f.files["/usr/bin/tailscale"] = preparationFile{root: true, regular: true, mode: 0755}
		}
	case "/usr/bin/tailscale":
		switch args[1] {
		case "debug":
			return json.Marshal(map[string]string{"OperatorUser": f.operator})
		case "status":
			return json.Marshal(map[string]string{"BackendState": f.state})
		case "login":
			f.state = "Running"
		case "set":
			f.operator = strings.TrimPrefix(args[2], "--operator=")
		default:
			return nil, errors.New("unexpected Tailscale operation")
		}
	case "/usr/bin/systemctl":
	default:
		return nil, errors.New("unexpected OS operation")
	}
	return nil, nil
}
func TestGuidedTailscaleFreshInstallUsesPinnedSignedPackages(t *testing.T) {
	f := setupFixture()
	dir := preparationDirectory(t)
	var out bytes.Buffer
	if e := prepareTailscale(t.Context(), dir, 1000, "owner", &out, f.read, f.run); e != nil {
		t.Fatal(e)
	}
	for p, b := range map[string][]byte{tailscaleKeyPath: tailscaleAPTKey, tailscaleSourcePath: []byte(tailscaleSource)} {
		v, _ := f.read(p)
		if !knownPreparationFile(v, b) {
			t.Fatal("published wrong trust material", p)
		}
	}
	calls := strings.Join(f.calls, "\n")
	for _, want := range []string{"APT::Get::AllowUnauthenticated=false", "Acquire::AllowInsecureRepositories=false", "--no-remove", "login --timeout=120s", "set --operator=owner"} {
		if !strings.Contains(calls, want) {
			t.Fatal("missing bounded preparation", want, calls)
		}
	}
	for _, bad := range []string{"--reset", "--authkey", "logout", "funnel", "serve", "--ssh", "/bin/sh", "curl"} {
		if strings.Contains(calls, bad) {
			t.Fatal("unexpected authority", bad, calls)
		}
	}
	if f.operator != "owner" || !strings.Contains(out.String(), "computer's browser") || !strings.Contains(out.String(), "same tailnet") {
		t.Fatal("lost login/device handoff", out.String())
	}
	for p := range f.files {
		if strings.HasSuffix(p, ".pending") {
			t.Fatal("left successful staging", p)
		}
	}
	b, e := os.ReadFile(filepath.Join(dir, "preparation.json"))
	if e != nil || strings.Contains(string(b), "auth") {
		t.Fatal("missing or secret-bearing recovery receipt", e)
	}
}
func TestGuidedTailscalePreservesExistingAccountAndOperator(t *testing.T) {
	for _, tc := range []struct {
		name, state, operator string
		wantErr               bool
	}{
		{"owned running", "Running", "owner", false}, {"foreign operator", "Running", "someone-else", true}, {"owner stopped", "Stopped", "owner", true}, {"approval required", "NeedsMachineAuth", "owner", true}, {"unknown status", "future-state", "owner", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setupFixture()
			f.files["/usr/bin/tailscale"] = preparationFile{root: true, regular: true, mode: 0755}
			f.state, f.operator = tc.state, tc.operator
			e := prepareTailscale(t.Context(), preparationDirectory(t), 1000, "owner", &bytes.Buffer{}, f.read, f.run)
			if (e != nil) != tc.wantErr {
				t.Fatal(e)
			}
			calls := strings.Join(f.calls, "\n")
			if strings.Contains(calls, "apt-get") || strings.Contains(calls, " login ") || strings.Contains(calls, " set ") {
				t.Fatal("existing account/operator was changed", calls)
			}
			if f.operator != tc.operator || f.state != tc.state {
				t.Fatal("state changed")
			}
		})
	}
}
func TestGuidedTailscaleInterruptedPackagePreparationResumes(t *testing.T) {
	for _, fault := range []string{"/usr/bin/install", "/usr/bin/ln", "/usr/bin/rm", "sourceparts=-", "--no-remove"} {
		t.Run(fault, func(t *testing.T) {
			f := setupFixture()
			f.fault = fault
			dir := preparationDirectory(t)
			if e := prepareTailscale(t.Context(), dir, 1000, "owner", &bytes.Buffer{}, f.read, f.run); e == nil {
				t.Fatal("fault did not stop preparation")
			}
			if e := prepareTailscale(t.Context(), dir, 1000, "owner", &bytes.Buffer{}, f.read, f.run); e != nil {
				t.Fatal("retry failed", e)
			}
			for p := range f.files {
				if strings.HasSuffix(p, ".pending") {
					t.Fatal("retry left staging", p)
				}
			}
		})
	}
}
func TestGuidedTailscaleRejectsForeignRepositoryBeforePublishing(t *testing.T) {
	for _, path := range []string{tailscaleKeyPath, tailscaleSourcePath} {
		t.Run(path, func(t *testing.T) {
			f := setupFixture()
			foreign := preparationFile{data: []byte("owner repository"), root: true, regular: true, mode: 0644}
			f.files[path] = foreign
			if e := prepareTailscale(t.Context(), preparationDirectory(t), 1000, "owner", &bytes.Buffer{}, f.read, f.run); e == nil {
				t.Fatal("foreign destination adopted")
			}
			if len(f.calls) != 0 || !bytes.Equal(f.files[path].data, foreign.data) {
				t.Fatal("foreign host mutated", f.calls)
			}
		})
	}
}
func TestGuidedTailscaleCancellationNeverElevates(t *testing.T) {
	f := setupFixture()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if e := prepareTailscale(ctx, preparationDirectory(t), 1000, "owner", &bytes.Buffer{}, f.read, f.run); !errors.Is(e, context.Canceled) || len(f.calls) > 0 {
		t.Fatal(e, f.calls)
	}
	if e := prepareTailscale(t.Context(), preparationDirectory(t), 0, "root", &bytes.Buffer{}, f.read, f.run); e == nil || len(f.calls) > 0 {
		t.Fatal("root execution allowed", e, f.calls)
	}
}

func TestGuidedTailscaleReusesExactOfficialRepository(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "official", true: "foreign"}[foreign], func(t *testing.T) {
			f := setupFixture()
			source := strings.ReplaceAll(tailscaleSource, tailscaleKeyPath, standardTailscaleKeyPath)
			if foreign {
				source = "deb https://owner.invalid noble main\n"
			}
			f.files[standardTailscaleSourcePath] = preparationFile{data: []byte("# official repository\n" + source), root: true, regular: true, mode: 0644}
			f.files[standardTailscaleKeyPath] = preparationFile{data: tailscaleAPTKey, root: true, regular: true, mode: 0644}
			e := prepareTailscale(t.Context(), preparationDirectory(t), 1000, "owner", &bytes.Buffer{}, f.read, f.run)
			if (e != nil) != foreign {
				t.Fatal(e)
			}
			for _, call := range f.calls {
				if strings.HasPrefix(call, "/usr/bin/install") || strings.HasPrefix(call, "/usr/bin/ln") || strings.HasPrefix(call, "/usr/bin/rm") {
					t.Fatal("existing repository mutated", call)
				}
			}
			if foreign && len(f.calls) > 0 {
				t.Fatal("foreign repo reached privileged tools", f.calls)
			}
		})
	}
}

func TestGuidedTailscaleReleaseMapping(t *testing.T) {
	for _, version := range []string{"22.04", "24.04", "26.04", "26.10"} {
		p, e := hostplatform.Lookup("ID=ubuntu\nVERSION_ID="+version+"\n", "amd64")
		if e != nil {
			t.Fatal(e)
		}
		f := setupFixture()
		if e = prepareTailscale(t.Context(), preparationDirectory(t), 1000, "owner", &bytes.Buffer{}, f.read, f.run, p); e != nil {
			t.Fatal(version, e)
		}
		got, _ := f.read(tailscaleSourcePath)
		if string(got.data) != p.TailscaleSource(tailscaleKeyPath) {
			t.Fatal(version, string(got.data))
		}
	}
}
