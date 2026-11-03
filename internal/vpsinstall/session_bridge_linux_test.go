//go:build linux

package vpsinstall

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

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
