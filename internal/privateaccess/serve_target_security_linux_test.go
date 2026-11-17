//go:build linux

package privateaccess

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyOwnerServeGuardRejectsPackageScope(t *testing.T) {
	id := strings.Repeat("a", 32)
	plan := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "unix:/run/fased-package-1300-" + id + "/tailscale.sock"}
	if ownedServeTarget(plan, 1300) {
		t.Fatal("legacy owner-wide Serve authority must not adopt an instance-scoped package route")
	}
	if ownedServeTarget(plan, 1301) {
		t.Fatal("another owner acquired the package route")
	}
	for _, bad := range []string{
		"unix:/run/fased-package-1300-../tailscale.sock",
		"unix:/run/fased-package-1300-" + strings.ToUpper(id) + "/tailscale.sock",
		"unix:/run/fased-package-1300-" + id + "/other.sock",
		"unix:/tmp/fased-package-1300-" + id + "/tailscale.sock",
	} {
		foreign := plan
		foreign.Target = bad
		if ownedServeTarget(foreign, 1300) {
			t.Fatal("foreign package route was accepted", bad)
		}
	}
	if protectedServeDestination(plan, 1300) {
		t.Fatal("an absent protected package socket was accepted")
	}
}

func TestManagedDirectServeRefusesUnprotectedIngressBeforeWriting(t *testing.T) {
	root := fixture(t)
	plan := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "unix:/var/lib/fased-app-vps-1000/app-state/tailscale.sock"}
	writes := 0
	m := Manager{
		GetConfig:       func(context.Context) ([]byte, string, error) { return []byte("{}"), strings.Repeat("a", 64), nil },
		SetConfig:       func(context.Context, []byte, string) error { writes++; return nil },
		ValidateIngress: func(p Plan) error { return RequireProtectedIngress(p, os.Getuid()) },
	}
	if err := m.Enable(t.Context(), root, plan, "fixture-instance"); err == nil || writes != 0 {
		t.Fatal("direct Serve write admitted an app-controlled socket", err, writes)
	}
	if _, err := os.Stat(filepath.Join(root, "private-access.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused route must not leave an ownership receipt", err)
	}
}

func TestManagedGuardKeepsLegacyRouteRemovable(t *testing.T) {
	root := fixture(t)
	plan := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "unix:/var/lib/fased-app-vps-1000/app-state/tailscale.sock"}
	if err := storeAccessReceipt(root, receipt{Schema: 1, Phase: "ready", Plan: plan}); err != nil {
		t.Fatal(err)
	}
	cfg := expected(plan)
	writes := 0
	m := Manager{
		GetConfig: func(context.Context) ([]byte, string, error) {
			b, _ := json.Marshal(cfg)
			return b, strings.Repeat("a", 64), nil
		},
		SetConfig: func(_ context.Context, b []byte, _ string) error {
			writes++
			var next map[string]any
			if err := json.Unmarshal(b, &next); err != nil {
				return err
			}
			cfg = next
			return nil
		},
		ValidateIngress: func(p Plan) error { return RequireProtectedIngress(p, os.Getuid()) },
	}
	if err := m.Remove(t.Context(), root); err != nil || writes != 1 {
		t.Fatal("owned legacy route could not be safely removed", err, writes)
	}
	if _, err := os.Stat(filepath.Join(root, "private-access.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed removal did not retire its receipt", err)
	}
}

func TestReviewOwnedServeRejectsReplaceableSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "fased-private-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	foreign := filepath.Join(t.TempDir(), "foreign.sock")
	listener, err := net.Listen("unix", foreign)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Symlink(foreign, filepath.Join(dir, "tailscale.sock")); err != nil {
		t.Fatal(err)
	}
	plan := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "unix:" + filepath.Join(dir, "tailscale.sock")}
	before := expected(Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "unix:/var/lib/fased-app-vps-1300/app-state/tailscale.sock"})
	after, err := routeChange(before, plan, true)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := json.Marshal(before)
	requested, _ := json.Marshal(after)
	tag := strings.Repeat("a", 64)
	written := false
	err = applyOwnedServe(context.Background(), os.Getuid(), []Plan{plan}, requested, tag,
		func(context.Context) ([]byte, string, error) { return current, tag, nil },
		func(context.Context, []byte, string) error { written = true; return nil })
	if err == nil || written {
		t.Fatal("replaceable symlink was admitted by the protected Serve decision")
	}
}
