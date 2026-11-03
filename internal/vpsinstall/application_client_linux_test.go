//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func TestApplicationUpdateClientAllowsConfirmedPasswordFreeApply(t *testing.T) {
	_, o, m, _ := ownerCallFixture(t)
	var out bytes.Buffer
	called := false
	if err := applicationUpdateClient(t.Context(), m, "apply", o, &out, func(_ context.Context, args []string) ([]byte, error) {
		called = true
		if !contains(args, "--clear-groups") || !contains(args, "--no-new-privs") || !contains(args, "apply") || !contains(args, m.Core) || !contains(args, m.Data) {
			t.Fatal(args)
		}
		return []byte(`{"operation":{"phase":"applying"}}`), nil
	}); err != nil || !called || out.Len() == 0 {
		t.Fatal(called, err)
	}
	// The public entry must reach identity verification, not confuse sudo -n
	// with a missing owner confirmation. An invalid installation still refuses.
	err := ApplicationUpdateClient(t.Context(), Manifest{}, "apply", &custodyinstall.System{NonInteractive: true}, io.Discard)
	if err == nil || err.Error() == "owner terminal required" {
		t.Fatal(err)
	}
	o.uid, o.euid = m.AppUID, m.AppUID
	called = false
	if err := applicationUpdateClient(t.Context(), m, "apply", o, &out, func(context.Context, []string) ([]byte, error) { called = true; return nil, nil }); err == nil || called {
		t.Fatal("wrong owner crossed helper", err)
	}
}
