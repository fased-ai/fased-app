package privateaccess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestIsolatedRoutePreservesExistingServeAndRecovers(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("managed Tailscale Unix ingress targets Linux")
	}
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "lost-write"}[lost], func(t *testing.T) {
			root := fixture(t)
			original := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "unix:/var/lib/fased-app-custody-1000/app-state/tailscale.sock"}
			isolated := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "unix:/var/lib/fased-app-vps-1300/app-state/tailscale.sock"}
			cfg := expected(original)
			before, _ := json.Marshal(cfg)
			writes := 0
			m := Manager{GetConfig: func(context.Context) ([]byte, string, error) { b, _ := json.Marshal(cfg); return b, "etag", nil }, SetConfig: func(_ context.Context, b []byte, tag string) error {
				if tag != "etag" {
					t.Fatal("unconditional write")
				}
				writes++
				if json.Unmarshal(b, &cfg) != nil {
					t.Fatal("invalid route")
				}
				if lost && writes == 1 {
					return errors.New("lost committed response")
				}
				return nil
			}, Client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"ok","apiVersion":"1","instanceId":"fixture-instance","connection":{"location":"remote","transport":"private-https"}}`))}, nil
			})}}
			err := m.Enable(t.Context(), root, isolated, "fixture-instance")
			if lost {
				if err == nil {
					t.Fatal("unconfirmed write accepted")
				}
				err = m.Enable(t.Context(), root, isolated, "fixture-instance")
			}
			if err != nil {
				t.Fatal("isolated enable", err)
			}
			if writes != 1 {
				t.Fatal("route replayed", writes)
			}
			if err = m.Verify(t.Context(), original, "fixture-instance"); err != nil {
				t.Fatal("original route rejected", err)
			}
			if err = m.Verify(t.Context(), isolated, "fixture-instance"); err != nil {
				t.Fatal("isolated route rejected", err)
			}
			if err = m.Remove(t.Context(), root); err != nil {
				t.Fatal("isolated removal", err)
			}
			var originalCfg map[string]any
			json.Unmarshal(before, &originalCfg)
			if !reflect.DeepEqual(cfg, originalCfg) {
				t.Fatalf("existing route changed: %v", cfg)
			}
		})
	}
}

func TestIsolatedPreflightCancellationPreservesOriginalRoute(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("managed Tailscale Unix ingress targets Linux")
	}
	root := fixture(t)
	original := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "unix:/var/lib/fased-app-custody-1000/app-state/tailscale.sock"}
	p := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "http://127.0.0.1:7333"}
	cfg := expected(original)
	before, _ := json.Marshal(cfg)
	writes := 0
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := Manager{GetConfig: func(context.Context) ([]byte, string, error) { b, _ := json.Marshal(cfg); return b, "etag", nil }, SetConfig: func(_ context.Context, b []byte, _ string) error {
		writes++
		if json.Unmarshal(b, &cfg) != nil {
			t.Fatal("invalid configuration")
		}
		if writes == 1 {
			cancel()
		}
		return nil
	}}
	if _, err := m.BeforeInstall(ctx, root, p, io.Discard); err == nil {
		t.Fatal("unconfirmed preflight accepted")
	}
	var want map[string]any
	json.Unmarshal(before, &want)
	if !reflect.DeepEqual(cfg, want) || writes != 2 {
		t.Fatalf("foreign route changed or cleanup lost: %v (%d)", cfg, writes)
	}
	if _, err := os.Stat(filepath.Join(root, "private-setup.json")); !os.IsNotExist(err) {
		t.Fatal("preflight receipt not reconciled", err)
	}
}

func TestSelectIsolatedKeepsExistingAndStablePort(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("managed Tailscale Unix ingress targets Linux")
	}
	original := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "unix:/var/lib/fased-app-custody-1000/app-state/tailscale.sock"}
	base := Plan{Mode: "tailscale", Origin: original.Origin, Target: "http://127.0.0.1:7333"}
	cfg := expected(original)
	m := Manager{GetConfig: func(context.Context) ([]byte, string, error) { b, _ := json.Marshal(cfg); return b, "etag", nil }}
	root := fixture(t)
	chosen, err := m.SelectIsolated(t.Context(), root, base)
	if err != nil || chosen.Origin != "https://node.owner.ts.net:8443" {
		t.Fatal(chosen, err)
	}
	bound, err := BindOrigin(base, chosen.Origin)
	if err != nil || bound.Origin != chosen.Origin {
		t.Fatal(bound, err)
	}
	if _, err = BindOrigin(base, "https://foreign.owner.ts.net:8443"); err == nil {
		t.Fatal("different Tailscale device accepted")
	}
	saved := chosen
	saved.Target = "unix:/tmp/fased-private-retained/tailscale.sock"
	if err = core.WritePrivateJSON(filepath.Join(root, "private-setup.json"), preflightReceipt{Version: 1, Plan: saved}); err != nil {
		t.Fatal(err)
	}
	chosen, err = m.SelectIsolated(t.Context(), root, base)
	if err != nil || chosen.Origin != saved.Origin {
		t.Fatal("retry lost recorded port", chosen, err)
	}
}

func TestSelectIsolatedResumesProtectedPreflightPort(t *testing.T) {
	dir := fixture(t)
	base := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "http://127.0.0.1:7333"}
	m := Manager{GetConfig: func(context.Context) ([]byte, string, error) {
		t.Fatal("retry must retain the protected port before inspecting occupied routes")
		return nil, "", nil
	}}
	got, err := m.SelectIsolated(t.Context(), dir, base, "https://node.owner.ts.net:8443")
	if err != nil || got.Origin != "https://node.owner.ts.net:8443" {
		t.Fatal("protected preflight retry changed origin", got, err)
	}
	if _, err = m.SelectIsolated(t.Context(), dir, base, "https://other.owner.ts.net:8443"); err == nil {
		t.Fatal("protected retry accepted another device")
	}
	saved := base
	saved.Target = "unix:/tmp/fased-private-retained/tailscale.sock"
	if err = core.WritePrivateJSON(filepath.Join(dir, "private-setup.json"), preflightReceipt{Version: 1, Plan: saved}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.SelectIsolated(t.Context(), dir, base, "https://node.owner.ts.net:8443"); err == nil {
		t.Fatal("conflicting owner and protected receipts were ignored")
	}
}

func TestIsolatedRouteRefusesCollisionAndFunnel(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("managed Tailscale Unix ingress targets Linux")
	}
	original := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "unix:/var/lib/fased-app-custody-1000/app-state/tailscale.sock"}
	other := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "unix:/var/lib/fased-app-vps-1300/app-state/tailscale.sock"}
	for _, mode := range []string{"port collision", "funnel"} {
		t.Run(mode, func(t *testing.T) {
			cfg := expected(original)
			if mode == "port collision" {
				cfg["TCP"].(map[string]any)["8443"] = map[string]any{"TCPForward": "127.0.0.1:22"}
			} else {
				cfg["AllowFunnel"] = map[string]any{"node.owner.ts.net:443": true}
			}
			writes := 0
			m := Manager{GetConfig: func(context.Context) ([]byte, string, error) { b, _ := json.Marshal(cfg); return b, "etag", nil }, SetConfig: func(context.Context, []byte, string) error { writes++; return nil }}
			if err := m.Enable(t.Context(), fixture(t), other, "instance"); err == nil || writes != 0 {
				t.Fatal("foreign policy accepted", err, writes)
			}
		})
	}
}

func TestConditionalOwnerDeltaNeverChangesForeignRoute(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("managed Tailscale Unix ingress targets Linux")
	}
	foreign := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "unix:/var/lib/fased-app-custody-1000/app-state/tailscale.sock"}
	own := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "unix:/var/lib/fased-app-vps-1300/app-state/tailscale.sock"}
	before := expected(foreign)
	after, err := routeChange(before, own, true)
	if err != nil {
		t.Fatal(err)
	}
	owns := func(p Plan) bool { return p.Origin == own.Origin && p.Target == own.Target }
	if !routeDeltaAllowed(before, after, []Plan{own}, owns) {
		t.Fatal("owned addition refused")
	}
	if !routeDeltaAllowed(after, before, []Plan{own}, owns) {
		t.Fatal("owned removal refused")
	}
	modified, _ := routeChange(after, foreign, false)
	if routeDeltaAllowed(before, modified, []Plan{own, foreign}, owns) {
		t.Fatal("foreign route removed")
	}
	modified["AllowFunnel"] = map[string]any{"node.owner.ts.net:8443": true}
	if routeDeltaAllowed(before, modified, []Plan{own}, owns) {
		t.Fatal("Funnel admitted")
	}
	if routeDeltaAllowed(before, after, []Plan{foreign}, owns) {
		t.Fatal("unowned addition admitted")
	}
}
