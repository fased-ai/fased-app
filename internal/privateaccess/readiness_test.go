package privateaccess

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPreparingHTTPSReadiness(t *testing.T) {
	p := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "http://127.0.0.1:7331"}
	for _, scenario := range []string{"startup", "cancel", "deadline", "certificate", "redirect", "wrong instance", "route changed", "ready"} {
		t.Run(scenario, func(t *testing.T) {
			root := fixture(t)
			cfg := map[string]any{}
			requests, writes := 0, 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 20*time.Millisecond)
				defer stop()
			}
			if scenario == "ready" {
				cfg = expected(p)
				if err := coreWriteReadinessFixture(root, p); err != nil {
					t.Fatal(err)
				}
			}
			m := Manager{
				GetConfig: func(context.Context) ([]byte, string, error) { b, _ := json.Marshal(cfg); return b, "version", nil },
				SetConfig: func(_ context.Context, b []byte, _ string) error { writes++; return json.Unmarshal(b, &cfg) },
				Client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }, Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
					requests++
					if requests == 1 {
						switch scenario {
						case "startup", "ready", "deadline":
							return nil, syscall.ECONNREFUSED
						case "cancel":
							cancel()
							return nil, syscall.ECONNREFUSED
						case "certificate":
							return nil, x509.UnknownAuthorityError{}
						case "redirect":
							return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://elsewhere.owner.ts.net"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
						case "route changed":
							cfg["AllowFunnel"] = map[string]any{"node.owner.ts.net:443": true}
						}
					}
					id := "fixture-instance"
					if scenario == "wrong instance" {
						id = "other-instance"
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"ok","apiVersion":"1","instanceId":"` + id + `","connection":{"location":"remote","transport":"private-https"}}`))}, nil
				})},
			}
			err := m.Enable(ctx, root, p, "fixture-instance")
			if (err == nil) != (scenario == "startup") {
				t.Fatal(scenario, err)
			}
			if scenario == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation", err)
			}
			if scenario == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("lost original deadline", err)
			}
			wantRequests := 1
			if scenario == "startup" {
				wantRequests = 2
			}
			if requests != wantRequests {
				t.Fatal("unexpected probe/retry", requests)
			}
			wantWrites := 1
			if scenario == "ready" {
				wantWrites = 0
			}
			if writes != wantWrites {
				t.Fatal("route reapplied", writes)
			}
			r, loadErr := load(root)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			wantPhase := "preparing"
			if scenario == "startup" || scenario == "ready" {
				wantPhase = "ready"
			}
			if r.Phase != wantPhase {
				t.Fatal("unsafe readiness receipt", r.Phase)
			}
		})
	}
}

func coreWriteReadinessFixture(root string, p Plan) error {
	b, err := json.Marshal(receipt{Schema: 1, Phase: "ready", Plan: p})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "private-access.json"), b, 0600)
}
