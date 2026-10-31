//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func ownerCallFixture(t *testing.T) (*memoryHost, hostOps, Manifest, core.ApplicationOwnerCommand) {
	t.Helper()
	h, o, m := installFixture(t)
	for _, p := range []string{"/usr", "/usr/bin"} {
		h.files[p] = hostFile{UID: 0, GID: 0, Mode: os.ModeDir | 0755, Directory: true}
	}
	for _, p := range []string{"/usr/bin/sudo", "/usr/bin/setpriv", "/usr/bin/env"} {
		h.files[p] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0755}
	}
	// Observing an existing account must not invoke even a privileged read.
	verifyRun := o.run
	o.run = func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if len(args) == 3 && args[0] == "/usr/bin/passwd" && args[1] == "-S" {
			return []byte(m.AppUser + " L 01/01/2026 0 99999 7 -1\n"), nil
		}
		return verifyRun(ctx, input, args...)
	}
	return h, o, m, core.ApplicationOwnerCommand{Action: "inspect", ID: strings.Repeat("a", 32)}
}

func TestApplicationOwnerCallFixedDroppedHelper(t *testing.T) {
	h, o, m, command := ownerCallFixture(t)
	before := len(h.calls)
	var got []string
	result, e := applicationOwnerCall(t.Context(), m, command, o, func(_ context.Context, input []byte, args []string) ([]byte, error) {
		if !bytes.Equal(input, encoded(command)) {
			t.Fatal("command changed or another input copied")
		}
		got = args
		if len(h.calls) != before {
			t.Fatal("host operation before helper dispatch")
		}
		return encoded(core.ApplicationOwnerView{View: core.ApplicationUpdateView{Version: 1, InstanceID: "fixture-instance", Operation: core.ApplicationUpdateOperation{ID: command.ID}}}), nil
	})
	if e != nil || result.View.Operation.ID != command.ID {
		t.Fatalf("%+v %v", result, e)
	}
	want := []string{"/usr/bin/setpriv", "--reuid", "991", "--regid", "991", "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", "/usr/bin/env", "--ignore-environment", "--chdir=/", "HOME=/nonexistent", "PATH=/usr/bin:/bin", m.Core, "application-owner-client", "--config", m.Config}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unbounded helper arguments: %q", got)
	}
}

func TestApplicationOwnerCallRefusesBeforeElevation(t *testing.T) {
	for _, name := range []string{"owner", "root", "manifest", "binary", "extra-file", "unit", "env", "action", "lease", "outcome", "oversized", "offline-lease", "offline-outcome"} {
		t.Run(name, func(t *testing.T) {
			h, o, m, command := ownerCallFixture(t)
			switch name {
			case "owner":
				o.uid = m.AppUID
				o.euid = m.AppUID
			case "root":
				o.uid, o.euid = 0, 0
			case "manifest":
				f := h.files[m.Config]
				f.Mode = 0666
				h.files[m.Config] = f
			case "binary":
				f := h.files[m.Core]
				f.Data = []byte("foreign")
				h.files[m.Core] = f
			case "extra-file":
				h.files[m.Root+"/foreign"] = hostFile{Regular: true}
			case "unit":
				h.override = true
			case "env":
				f := h.files["/usr/bin/env"]
				f.UID = m.OwnerUID
				h.files["/usr/bin/env"] = f
			case "action":
				command.Action = "launch-shell"
			case "lease":
				command.Action = "prepare"
			case "outcome":
				command.Receipt = &core.ApplicationUpdateReceipt{ID: command.ID}
			case "oversized":
				command.Action = "finish"
				command.Lease = command.ID
				command.Receipt = &core.ApplicationUpdateReceipt{ID: command.ID, EngineSHA256: strings.Repeat("a", 5000)}
			case "offline-lease":
				command.Action, command.Lease = "offline", command.ID
			case "offline-outcome":
				command.Action = "offline"
				command.Receipt = &core.ApplicationUpdateReceipt{ID: command.ID}
			}
			called := false
			_, e := applicationOwnerCall(t.Context(), m, command, o, func(context.Context, []byte, []string) ([]byte, error) { called = true; return nil, nil })
			if e == nil || called {
				t.Fatalf("unsafe request crossed helper boundary: %v", e)
			}
		})
	}
}

func TestApplicationOwnerCallStrictResponseAndErrors(t *testing.T) {
	for _, response := range []string{`{}`, `{"unexpected":true}`, `{} {}`, strings.Repeat(" ", applicationOwnerResponseLimit+1), `{"view":{"version":1,"instanceId":"fixture","operation":{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}}`} {
		_, o, m, command := ownerCallFixture(t)
		if _, e := applicationOwnerCall(t.Context(), m, command, o, func(context.Context, []byte, []string) ([]byte, error) { return []byte(response), nil }); e == nil {
			t.Fatal("invalid response accepted")
		}
	}
	_, o, m, command := ownerCallFixture(t)
	want := errors.New("owner authorization unavailable")
	if _, e := applicationOwnerCall(t.Context(), m, command, o, func(context.Context, []byte, []string) ([]byte, error) { return nil, want }); !errors.Is(e, want) {
		t.Fatal(e)
	}
	var out applicationOwnerResponse
	if _, e := out.Write(make([]byte, applicationOwnerResponseLimit)); e != nil {
		t.Fatal(e)
	}
	if _, e := out.Write([]byte("x")); e == nil || out.Len() != applicationOwnerResponseLimit {
		t.Fatal("stdout limit not enforced")
	}
	out = applicationOwnerResponse{}
	if _, e := io.Copy(&out, strings.NewReader(strings.Repeat("x", applicationOwnerResponseLimit+1))); e == nil || out.Len() > applicationOwnerResponseLimit {
		t.Fatal("stdout copy bypassed the cap")
	}
}
