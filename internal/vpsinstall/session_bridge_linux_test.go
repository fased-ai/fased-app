//go:build linux

package vpsinstall

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"golang.org/x/sys/unix"
)

func TestSealedSourceSessionExecutable(t *testing.T) {
	mode := os.Getenv("FASED_TEST_SEALED_SOURCE")
	if mode != "" {
		image, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		file, path, digest, err := openSealedSourceSession(image)
		if mode == "unsealed" || mode == "foreign" {
			if err == nil {
				file.Close()
				t.Fatal("unsealed or foreign running executable accepted")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if foreign, _, _, err := openSealedSourceSession(image + "-other"); err == nil {
			foreign.Close()
			t.Fatal("another executable identity was accepted")
		}
		copyPath := filepath.Join(t.TempDir(), "copied")
		if output, err := exec.Command("/usr/bin/install", "-m", "0500", "--", path, copyPath).CombinedOutput(); err != nil {
			t.Fatalf("copy held executable: %v: %s", err, output)
		}
		copied, err := bundle.Inspect(copyPath)
		if err != nil || copied.SHA256 != digest {
			t.Fatalf("copied executable changed: %v", err)
		}
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if file, _, _, err := openSealedSourceSession(self); err == nil {
		file.Close()
		t.Fatal("ordinary unsealed executable accepted")
	}
	source, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	fd, err := unix.MemfdCreate("fased-verified-upgrade", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_EXEC)
	if errors.Is(err, unix.EINVAL) {
		fd, err = unix.MemfdCreate("fased-verified-upgrade", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	}
	if err != nil {
		t.Fatal(err)
	}
	image := os.NewFile(uintptr(fd), "test-upgrade")
	defer image.Close()
	if _, err := io.Copy(image, source); err != nil {
		t.Fatal(err)
	}
	if err := image.Chmod(0500); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/proc/self/fd/3", "-test.run=^TestSealedSourceSessionExecutable$")
	command.ExtraFiles = []*os.File{image}
	command.Env = append(os.Environ(), "FASED_TEST_SEALED_SOURCE=unsealed")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unsealed successor refusal: %v: %s", err, output)
	}
	if _, err := unix.FcntlInt(image.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("/proc/self/fd/3", "-test.run=^TestSealedSourceSessionExecutable$")
	command.ExtraFiles = []*os.File{image}
	command.Env = append(os.Environ(), "FASED_TEST_SEALED_SOURCE=sealed")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("sealed successor session: %v: %s", err, output)
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	fd, err = unix.MemfdCreate("fased-verified-upgrade-foreign", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_EXEC)
	if errors.Is(err, unix.EINVAL) {
		fd, err = unix.MemfdCreate("fased-verified-upgrade-foreign", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	}
	if err != nil {
		t.Fatal(err)
	}
	foreign := os.NewFile(uintptr(fd), "foreign-upgrade")
	defer foreign.Close()
	if _, err := io.Copy(foreign, source); err != nil {
		t.Fatal(err)
	}
	if err := foreign.Chmod(0500); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.FcntlInt(foreign.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("/proc/self/fd/3", "-test.run=^TestSealedSourceSessionExecutable$")
	command.ExtraFiles = []*os.File{foreign}
	command.Env = append(os.Environ(), "FASED_TEST_SEALED_SOURCE=foreign")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("foreign successor refusal: %v: %s", err, output)
	}
}

func sourceSessionReceiverFixture(t *testing.T) (*memoryHost, hostOps, Manifest, string, string) {
	t.Helper()
	h, o, m, _ := ownerCallFixture(t)
	body := []byte("bounded verified source-session executable")
	digest := hash(body)
	dir := m.Root + "-session-" + digest[:24]
	image, record := dir+"/helper", dir+"/binding.json"
	h.files[dir] = hostFile{UID: 0, GID: 0, Mode: os.ModeDir | 0755, Directory: true}
	h.files[image] = hostFile{UID: 0, GID: 0, Mode: 0755, Regular: true, Data: body}
	h.files[record] = hostFile{UID: 0, GID: 0, Mode: 0644, Regular: true, Data: encoded(sourceSessionBinding{Version: 1, Installation: m, HelperSHA256: digest})}
	o.uid, o.euid, o.gid = m.AppUID, m.AppUID, m.AppGID
	o.executable = func() (string, error) { return image, nil }
	return h, o, m, image, record
}

func TestSourceSessionReceiverAdmitsActualBoundImage(t *testing.T) {
	_, o, m, image, record := sourceSessionReceiverFixture(t)
	got, err := validateSourceSessionHelper(m.Config, image, record, m.AppUID, o)
	if err != nil || got != m {
		t.Fatal("actual staged helper was mistaken for the installed core", err)
	}
	if _, err := validateApplicationOwnerHelper(m.Config, image, m.AppUID, o); err == nil {
		t.Fatal("source-session image gained ordinary owner-helper authority")
	}
}

func TestSourceSessionReceiverRefusesChangedProcessOrGeneration(t *testing.T) {
	for _, kind := range []string{"actual-image", "root", "owner", "group", "supplementary-group", "binding-owner", "binding-generation", "helper-content", "helper-mode", "installed-content", "source-path"} {
		t.Run(kind, func(t *testing.T) {
			h, o, m, image, record := sourceSessionReceiverFixture(t)
			switch kind {
			case "actual-image":
				o.executable = func() (string, error) { return m.Core, nil }
			case "root":
				o.uid, o.euid = 0, 0
			case "owner":
				o.uid, o.euid = m.OwnerUID, m.OwnerUID
			case "group":
				o.gid = m.OwnerGID
			case "supplementary-group":
				o.groups = func() ([]int, error) { return []int{m.AppGID, m.OwnerGID}, nil }
			case "binding-owner":
				f := h.files[record]
				f.UID = m.AppUID
				h.files[record] = f
			case "binding-generation":
				f := h.files[record]
				var b sourceSessionBinding
				if err := strict(f.Data, &b); err != nil {
					t.Fatal(err)
				}
				b.Installation.CoreSHA256 = strings.Repeat("f", 64)
				f.Data = encoded(b)
				h.files[record] = f
			case "helper-content", "helper-mode", "installed-content":
				path := image
				if kind == "installed-content" {
					path = m.Core
				}
				f := h.files[path]
				if kind == "helper-mode" {
					f.Mode = 0777
				} else {
					f.Data = []byte("foreign executable")
				}
				h.files[path] = f
			case "source-path":
				image = "/tmp/foreign-helper"
			}
			if _, err := validateSourceSessionHelper(m.Config, image, record, m.AppUID, o); err == nil {
				t.Fatal("changed process, binding or installed image admitted")
			}
		})
	}
}

func TestSourceSessionBridgeIsBoundAndDropsBeforeExecution(t *testing.T) {
	h, o, m, _ := ownerCallFixture(t)
	body := []byte("bounded-verified-current-helper")
	digest := hash(body)
	image := "/verified/bootstrap/fased-app"
	h.files[image] = hostFile{Data: body, UID: o.uid, GID: o.gid, Mode: 0700, Regular: true}
	run := o.run
	o.run = func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "/usr/bin/install" && args[len(args)-2] == image {
			input = body // model OS install's streamed file copy, not stdin
		}
		return run(ctx, input, args...)
	}
	oldHelper := o.helper
	calls := 0
	o.helper = func(_ context.Context, args []string, _ io.Writer) error {
		if contains(args, "updates") {
			return errors.New("predecessor cannot renew its own session")
		}
		calls++
		if args[0] != "/usr/bin/setpriv" || !contains(args, "--no-new-privs") || !contains(args, "--clear-groups") || !contains(args, "reader-session-client") || !contains(args, m.Config) {
			t.Fatal("unconfined helper", args)
		}
		for _, arg := range args {
			if strings.Contains(arg, "password") {
				t.Fatal("password handoff", args)
			}
		}
		return nil
	}
	defer func() { o.helper = oldHelper }()
	if err := prepareSourceSession(t.Context(), m, image, digest, o); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	dir := m.Root + "-session-" + digest[:24]
	imagePath := filepath.Join(dir, "helper")
	record := h.files[filepath.Join(dir, "binding.json")]
	var binding sourceSessionBinding
	if strict(record.Data, &binding) != nil || !sessionBindingMatches(binding, m.Config, imagePath, m.AppUID) {
		t.Fatal("unbound helper record")
	}
	if sessionBindingMatches(binding, m.Config, imagePath, m.OwnerUID) || sessionBindingMatches(binding, m.Config+".foreign", imagePath, m.AppUID) || sessionBindingMatches(binding, m.Config, "/tmp/helper", m.AppUID) {
		t.Fatal("foreign helper admitted")
	}
	// Existing unknown stage bytes cannot be overwritten or executed.
	h.files[imagePath] = hostFile{Data: []byte("foreign"), UID: 0, GID: 0, Mode: 0755, Regular: true}
	if err := prepareSourceSession(t.Context(), m, image, digest, o); err == nil || calls != 1 {
		t.Fatal("foreign stage used", err, calls)
	}
}

func TestSourceSessionUsesInstalledHelperWithoutStaging(t *testing.T) {
	h, o, m, _ := ownerCallFixture(t)
	var calls int
	o.helper = func(_ context.Context, args []string, out io.Writer) error {
		calls++
		want := applicationOwnerHelperArgs(m, m.Core)
		if calls == 1 {
			want = append(want[:len(want)-3], "updates", "status", "--data-dir", m.Data)
		} else {
			want = append(want[:len(want)-3], "status", "--data-dir", m.Data)
		}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("unexpected installed session command: %q", args)
		}
		admitted := false
		for _, rule := range maintenanceRules(m) {
			if rule.tool == args[0] && regexp.MustCompile(rule.args).MatchString(strings.Join(args[1:], " ")) {
				admitted = true
			}
		}
		if !admitted {
			t.Fatal("installed maintenance policy refuses the session command")
		}
		if calls == 1 {
			// `updates status` returns the application view, not the distinct
			// `update-status` observation.
			return json.NewEncoder(out).Encode(core.ApplicationUpdateView{
				Version: 1, Schema: 37, InstanceID: "installed-workspace",
				Operation: core.ApplicationUpdateOperation{Phase: "idle"},
			})
		}
		return json.NewEncoder(out).Encode(core.Health{Status: "ok", APIVersion: core.APIVersion, InstanceID: "installed-workspace", Connection: &core.ConnectionInfo{Location: "remote", OwnerUID: m.OwnerUID}})
	}
	digest := strings.Repeat("a", 64)
	if err := prepareSourceSession(t.Context(), m, "/unavailable/sealed-source", digest, o); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("installed helper calls = %d", calls)
	}
	for path := range h.files {
		if strings.HasPrefix(path, m.Root+"-session-") {
			t.Fatalf("unnecessary source helper staged: %s", path)
		}
	}
}

func TestSourceSessionRejectsMismatchedInstalledInstance(t *testing.T) {
	h, o, m, _ := ownerCallFixture(t)
	body := []byte("bounded-verified-current-helper")
	digest, image := hash(body), "/verified/bootstrap/fased-app"
	h.files[image] = hostFile{Data: body, UID: o.uid, GID: o.gid, Mode: 0700, Regular: true}
	run := o.run
	o.run = func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "/usr/bin/install" && args[len(args)-2] == image {
			input = body
		}
		return run(ctx, input, args...)
	}
	var staged int
	o.helper = func(_ context.Context, args []string, out io.Writer) error {
		switch {
		case contains(args, "updates"):
			return json.NewEncoder(out).Encode(core.ApplicationUpdateView{Version: 1, Schema: 37, InstanceID: "foreign-workspace"})
		case contains(args, "reader-session-client"):
			staged++
			return nil
		case contains(args, "status"):
			return json.NewEncoder(out).Encode(core.Health{Status: "ok", APIVersion: core.APIVersion, InstanceID: "installed-workspace", Connection: &core.ConnectionInfo{Location: "remote", OwnerUID: m.OwnerUID}})
		default:
			t.Fatalf("unexpected session command: %q", args)
			return nil
		}
	}
	if err := prepareSourceSession(t.Context(), m, image, digest, o); err == nil || staged != 0 {
		t.Fatalf("foreign status was not refused before staging: staged=%d err=%v", staged, err)
	}
	for path := range h.files {
		if strings.HasPrefix(path, m.Root+"-session-") {
			t.Fatalf("foreign status created staging state: %s", path)
		}
	}
}

func TestSourceSessionRejectsUnhealthyOrForeignOwner(t *testing.T) {
	for _, kind := range []string{"unhealthy", "missing-owner", "wrong-owner", "custody", "local"} {
		t.Run(kind, func(t *testing.T) {
			h, o, m, _ := ownerCallFixture(t)
			health := core.Health{Status: "ok", APIVersion: core.APIVersion, InstanceID: "installed-workspace", Connection: &core.ConnectionInfo{Location: "remote", OwnerUID: m.OwnerUID}}
			switch kind {
			case "unhealthy":
				health.Status = "failed"
			case "missing-owner":
				health.Connection = nil
			case "wrong-owner":
				health.Connection.OwnerUID++
			case "custody":
				health.Connection.OwnerKind = "custody"
			case "local":
				health.Connection.Location = "local"
			}
			o.helper = func(_ context.Context, args []string, out io.Writer) error {
				if contains(args, "updates") {
					return json.NewEncoder(out).Encode(core.ApplicationUpdateView{Version: 1, Schema: 37, InstanceID: health.InstanceID})
				}
				if contains(args, "status") {
					return json.NewEncoder(out).Encode(health)
				}
				t.Fatalf("unexpected session command: %q", args)
				return nil
			}
			if err := prepareSourceSession(t.Context(), m, "/unavailable/sealed-source", strings.Repeat("a", 64), o); err == nil {
				t.Fatal("unhealthy or foreign installation was accepted")
			}
			for path := range h.files {
				if strings.HasPrefix(path, m.Root+"-session-") {
					t.Fatalf("unhealthy or foreign installation created staging state: %s", path)
				}
			}
		})
	}
}

func TestSourceSessionCancellationNeverStagesFallback(t *testing.T) {
	h, o, m, _ := ownerCallFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	o.helper = func(context.Context, []string, io.Writer) error {
		cancel()
		return context.Canceled
	}
	if err := prepareSourceSession(ctx, m, "/unavailable/sealed-source", strings.Repeat("a", 64), o); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled session preparation = %v", err)
	}
	for path := range h.files {
		if strings.HasPrefix(path, m.Root+"-session-") {
			t.Fatalf("cancelled session staged helper: %s", path)
		}
	}
}

func TestSourceSessionRejectsMalformedInstalledStatus(t *testing.T) {
	h, o, m, _ := ownerCallFixture(t)
	body := []byte("bounded-verified-current-helper")
	digest := hash(body)
	image := "/verified/bootstrap/fased-app"
	h.files[image] = hostFile{Data: body, UID: o.uid, GID: o.gid, Mode: 0700, Regular: true}
	run := o.run
	o.run = func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if len(args) > 2 && args[0] == "/usr/bin/install" && args[len(args)-2] == image {
			input = body
		}
		return run(ctx, input, args...)
	}
	var installed, staged int
	o.helper = func(_ context.Context, args []string, out io.Writer) error {
		if contains(args, "updates") {
			installed++
			_, err := io.WriteString(out, `{"version":1,"instanceId":"","schema":37}`)
			return err
		}
		if !contains(args, "reader-session-client") {
			t.Fatalf("unexpected session command: %q", args)
		}
		staged++
		return nil
	}
	if err := prepareSourceSession(t.Context(), m, image, digest, o); err != nil {
		t.Fatal(err)
	}
	if installed != 1 || staged != 1 {
		t.Fatalf("invalid status was accepted: installed=%d staged=%d", installed, staged)
	}
}
