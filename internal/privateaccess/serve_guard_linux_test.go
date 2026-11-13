//go:build linux

package privateaccess

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestOwnedServeTargetRejectsOtherInstallations(t *testing.T) {
	owner := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "unix:/var/lib/fased-app-vps-1300/app-state/tailscale.sock"}
	if !ownedServeTarget(owner, 1300) {
		t.Fatal("own target refused")
	}
	if ownedServeTarget(owner, 1000) {
		t.Fatal("other owner's target admitted")
	}
	owner.Target = "unix:/var/lib/fased-app-vps-1300/app-state/core.sock"
	if ownedServeTarget(owner, 1300) {
		t.Fatal("private IPC admitted as ingress")
	}
}

func TestMaintenanceServeBoundaryChecksBeforeActualConditionalWrite(t *testing.T) {
	foreign := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "unix:/var/lib/fased-app-custody-1000/app-state/tailscale.sock"}
	own := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "unix:/var/lib/fased-app-vps-1300/app-state/tailscale.sock"}
	before := expected(foreign)
	after, err := routeChange(before, own, true)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := json.Marshal(before)
	data, _ := json.Marshal(after)
	tag := strings.Repeat("a", 64)
	for _, kind := range []string{"complete", "stale", "foreign", "wrong-owner", "unrecorded", "cancelled", "write-conflict"} {
		t.Run(kind, func(t *testing.T) {
			input := append([]byte(nil), data...)
			owner, plans, liveTag := 1300, []Plan{own}, tag
			switch kind {
			case "stale":
				liveTag = strings.Repeat("b", 64)
			case "foreign":
				changed, _ := routeChange(after, foreign, false)
				input, _ = json.Marshal(changed)
			case "wrong-owner":
				owner = 1000
			case "unrecorded":
				plans = nil
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if kind == "cancelled" {
				cancel()
			}
			writes := 0
			err := applyOwnedServe(ctx, owner, plans, input, tag,
				func(context.Context) ([]byte, string, error) { return current, liveTag, nil },
				func(_ context.Context, body []byte, conditional string) error {
					writes++
					if string(body) != string(data) || conditional != tag {
						t.Fatal("validated request changed at privileged write")
					}
					if kind == "write-conflict" {
						return errors.New("conditional conflict")
					}
					return nil
				})
			if kind == "complete" {
				if err != nil || writes != 1 {
					t.Fatalf("owned write failed: %v, writes=%d", err, writes)
				}
			} else if kind == "write-conflict" {
				if err == nil || writes != 1 {
					t.Fatal("unconfirmed write was retried or marked complete")
				}
			} else if err == nil || writes != 0 {
				t.Fatalf("refused request reached mutation: %v, writes=%d", err, writes)
			}
		})
	}
}

func TestOwnedServeGuardUsesRecordedRouteAndCurrentDaemonTag(t *testing.T) {
	foreign := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "unix:/var/lib/fased-app-custody-1000/app-state/tailscale.sock"}
	own := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "unix:/var/lib/fased-app-vps-1300/app-state/tailscale.sock"}
	before := expected(foreign)
	after, err := routeChange(before, own, true)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := json.Marshal(before)
	requested, _ := json.Marshal(after)
	tag := strings.Repeat("a", 64)
	args, err := serveRawCommand(requested, tag)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "private-access.json")
	if err = core.WritePrivateJSON(path, receipt{Schema: 1, Phase: "preparing", Plan: own}); err != nil {
		t.Fatal(err)
	}
	get := func(context.Context) ([]byte, string, error) { return current, tag, nil }
	if !allowedOwnedServeCommand(t.Context(), requested, args, get, dir, 1300, ownedServeTarget) {
		t.Fatal("recorded owner route refused")
	}
	stale := func(context.Context) ([]byte, string, error) { return current, strings.Repeat("b", 64), nil }
	if allowedOwnedServeCommand(t.Context(), requested, args, stale, dir, 1300, ownedServeTarget) {
		t.Fatal("stale daemon configuration admitted")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if allowedOwnedServeCommand(t.Context(), requested, args, get, dir, 1300, ownedServeTarget) {
		t.Fatal("route admitted without its owner receipt")
	}
}

func TestOwnedServeOSGuardPinsTagAndPreservesForeignRoute(t *testing.T) {
	foreign := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "unix:/var/lib/fased-app-custody-1000/app-state/tailscale.sock"}
	own := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "unix:/var/lib/fased-app-vps-1300/app-state/tailscale.sock"}
	before := expected(foreign)
	after, err := routeChange(before, own, true)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal(before)
	next, _ := json.Marshal(after)
	tag := strings.Repeat("a", 64)
	args, err := serveRawCommand(next, tag)
	if err != nil {
		t.Fatal(err)
	}
	owns := func(p Plan) bool { return ownedServeTarget(p, 1300) }
	if !allowedOwnerServeDelta(next, args, old, tag, []Plan{own}, owns) {
		t.Fatal("owned conditional write refused")
	}
	if allowedOwnerServeDelta(next, args, old, strings.Repeat("b", 64), []Plan{own}, owns) {
		t.Fatal("stale tag accepted")
	}
	if allowedOwnerServeDelta(next, args, old, tag, nil, owns) {
		t.Fatal("unrecorded route accepted")
	}
	changed, _ := routeChange(after, foreign, false)
	bad, _ := json.Marshal(changed)
	badArgs, _ := serveRawCommand(bad, tag)
	if allowedOwnerServeDelta(bad, badArgs, old, tag, []Plan{own}, owns) {
		t.Fatal("foreign route removed")
	}
}
