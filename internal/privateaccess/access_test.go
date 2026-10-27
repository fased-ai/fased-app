package privateaccess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixture(t *testing.T) string {
	t.Helper()
	root, _ := filepath.Abs("../../.evidence/access-fixtures")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	p, e := os.MkdirTemp(root, "f-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(p) })
	return p
}
func TestPlansAndOwnerBoundaries(t *testing.T) {
	m := Manager{Run: func(context.Context, ...string) ([]byte, error) {
		return []byte(`{"BackendState":"Running","Self":{"Online":true,"DNSName":"node.owner.ts.net."}}`), nil
	}}
	p, e := m.Plan(t.Context(), "tailscale", "127.0.0.1:7331")
	if e != nil || p.Origin != "https://node.owner.ts.net" {
		t.Fatal(p, e)
	}
	for _, s := range []string{"0.0.0.0:7331", "127.0.0.1:0", "evil.example:7331"} {
		if _, e = m.Plan(t.Context(), "ssh", s); e == nil {
			t.Fatal("exposed/unstable listener", s)
		}
	}
	p, e = m.Plan(t.Context(), "ssh", "127.0.0.1:7331")
	if e != nil || !strings.Contains(strings.Join(p.Instructions, " "), "ExitOnForwardFailure=yes") {
		t.Fatal(p, e)
	}
	m.Run = func(context.Context, ...string) ([]byte, error) { return []byte(`{"BackendState":"NeedsLogin"}`), nil }
	if _, e = m.Plan(t.Context(), "tailscale", "127.0.0.1:7331"); e == nil {
		t.Fatal("unenrolled device accepted")
	}
}
func TestConditionalRouteRecoveryAndForeignConfiguration(t *testing.T) {
	root := fixture(t)
	p := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "http://127.0.0.1:7331"}
	cfg := map[string]any{}
	version := "first"
	writes := 0
	interrupt := true
	probeID := "fixture-instance"
	m := Manager{GetConfig: func(context.Context) ([]byte, string, error) { b, _ := json.Marshal(cfg); return b, version, nil }, SetConfig: func(_ context.Context, b []byte, etag string) error {
		if etag != version {
			return errors.New("conflict")
		}
		cfg = map[string]any{}
		if e := json.Unmarshal(b, &cfg); e != nil {
			return e
		}
		writes++
		version = "next"
		if interrupt {
			interrupt = false
			return errors.New("interrupted after daemon accepted")
		}
		return nil
	}, Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != p.Origin+"/api/v1/health" {
			t.Fatal("unexpected probe", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"ok","instanceId":"` + probeID + `","apiVersion":"1","connection":{"location":"remote","transport":"private-https"}}`)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
	})}}
	if e := m.Enable(t.Context(), root, p, "fixture-instance"); e == nil {
		t.Fatal("interrupted write reported success")
	}
	r, e := load(root)
	if e != nil || r.Phase != "preparing" {
		t.Fatal(r, e)
	}
	if e = m.Enable(t.Context(), root, p, "fixture-instance"); e != nil {
		t.Fatal(e)
	}
	if writes != 1 {
		t.Fatal("retry overwrote route")
	}
	cfg["AllowFunnel"] = map[string]any{"node.owner.ts.net:443": true}
	if e = m.Remove(t.Context(), root); e == nil || writes != 1 {
		t.Fatal("foreign/public configuration removed")
	}
	cfg = expected(p)
	if e = m.Remove(t.Context(), root); e != nil {
		t.Fatal(e)
	}
	if _, e = load(root); !os.IsNotExist(e) {
		t.Fatal("receipt remains", e)
	}
	cfg = expected(p)
	if e = m.Enable(t.Context(), root, p, "fixture-instance"); e == nil {
		t.Fatal("unowned route was adopted")
	}
	cfg = map[string]any{}
	probeID = "different-instance"
	if e = m.Enable(t.Context(), root, p, "fixture-instance"); e == nil {
		t.Fatal("wrong instance accepted")
	}
}

func TestConcurrentOwnerRouteChangeIsNotOverwritten(t *testing.T) {
	root := fixture(t)
	p := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "http://127.0.0.1:7331"}
	writes := 0
	m := Manager{GetConfig: func(context.Context) ([]byte, string, error) { return []byte(`{}`), `"old"`, nil }, SetConfig: func(_ context.Context, b []byte, etag string) error {
		if etag != `"old"` {
			t.Fatal("conditional identity dropped")
		}
		writes++
		return errors.New("daemon rejected stale If-Match")
	}}
	if e := m.Enable(t.Context(), root, p, "fixture-instance"); e == nil {
		t.Fatal("conflicting update reported success")
	}
	if writes != 1 {
		t.Fatal("blind retry after conflict")
	}
	r, e := load(root)
	if e != nil || r.Phase != "preparing" {
		t.Fatal("recovery intent lost", e)
	}
}

func TestPrivateAccessReadinessRequiresHealthyCompatibleInstance(t *testing.T) {
	valid := `{"status":"ok","instanceId":"fixture-instance","apiVersion":"1","connection":{"location":"remote","transport":"private-https"}}`
	for _, tc := range []struct {
		name, body, contentType string
	}{
		{"missing runtime", `{"status":"ok","instanceId":"fixture-instance","apiVersion":"1"}`, "application/json"},
		{"unhealthy runtime", strings.Replace(valid, `"status":"ok"`, `"status":"starting"`, 1), "application/json"},
		{"wrong media", valid, "text/html"},
		{"trailing content", valid + `{}`, "application/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fixture(t)
			plan := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "http://127.0.0.1:7331"}
			cfg, writes := []byte(`{}`), 0
			body, contentType := tc.body, tc.contentType
			manager := Manager{
				GetConfig: func(context.Context) ([]byte, string, error) { return cfg, "revision", nil },
				SetConfig: func(_ context.Context, next []byte, _ string) error { cfg = next; writes++; return nil },
				Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
					if r.URL.String() != plan.Origin+"/api/v1/health" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
						t.Fatal("unexpected private probe")
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{contentType}}}, nil
				})},
			}
			if err := manager.Enable(t.Context(), root, plan, "fixture-instance"); err == nil {
				t.Fatal("incompatible private app reported ready")
			}
			if receipt, err := load(root); err != nil || receipt.Phase != "preparing" {
				t.Fatal("recovery receipt lost", receipt, err)
			}
			body, contentType = valid, "application/json; charset=utf-8"
			if err := manager.Enable(t.Context(), root, plan, "fixture-instance"); err != nil {
				t.Fatal("same-instance recovery failed", err)
			}
			if receipt, err := load(root); err != nil || receipt.Phase != "ready" || writes != 1 {
				t.Fatal("retry changed the route or failed readiness", receipt, writes, err)
			}
		})
	}
}
