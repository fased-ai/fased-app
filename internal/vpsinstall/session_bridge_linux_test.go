//go:build linux

package vpsinstall

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
