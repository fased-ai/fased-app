package privateaccess

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeAuthorizationHandlesOnlyDefinitePermissionRefusal(t *testing.T) {
	body := []byte(`{"TCP":{"443":{"HTTPS":true}},"Web":{"node.owner.ts.net:443":{"Handlers":{"/":{"Proxy":"unix:/var/lib/fased-app-vps-1000/app-state/tailscale.sock"}}}}}`)
	tag := strings.Repeat("a", 64)
	calls := 0
	m := Manager{SetConfig: func(context.Context, []byte, string) error { return servePermissionError{} }}.WithServeAuthorization(func(_ context.Context, b []byte, e string) error {
		calls++
		if !bytes.Equal(b, body) || e != tag {
			t.Fatal("conditional write changed")
		}
		return nil
	})
	if err := m.SetConfig(t.Context(), body, tag); err != nil || calls != 1 {
		t.Fatal("root-only Unix setup has no protected OS handoff", err, calls)
	}
	for _, cause := range []error{nil, errors.New("lost committed response"), errors.New("ETag conflict")} {
		calls = 0
		m = Manager{SetConfig: func(context.Context, []byte, string) error { return cause }}.WithServeAuthorization(func(context.Context, []byte, string) error { calls++; return nil })
		if got := m.SetConfig(t.Context(), body, tag); !errors.Is(got, cause) || calls != 0 {
			t.Fatal("ambiguous/successful write was replayed", got, calls)
		}
	}
}

func TestServeOSRequestIsFixedBoundedAndConditional(t *testing.T) {
	body := []byte(`{"TCP":{"443":{"HTTPS":true}},"Web":{"node.owner.ts.net:443":{"Handlers":{"/":{"Proxy":"unix:/var/lib/fased-app-vps-1000/app-state/tailscale.sock"}}}}}`)
	tag := strings.Repeat("a", 64)
	args, err := serveCommand(body, tag)
	if err != nil {
		t.Fatal(err)
	}
	if !AllowedServeCommand(body, args) {
		t.Fatal("own request refused")
	}
	calls := 0
	run := func(_ context.Context, data []byte, command ...string) ([]byte, error) {
		calls++
		if !bytes.Equal(data, body) || !AllowedServeCommand(data, command) {
			t.Fatal("OS request expanded")
		}
		return []byte("200"), nil
	}
	if err := SetServeWithOS(t.Context(), body, tag, run); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	for _, response := range []string{"", "204", "412", "500"} {
		if err := SetServeWithOS(t.Context(), body, tag, func(context.Context, []byte, ...string) ([]byte, error) { return []byte(response), nil }); err == nil {
			t.Fatal("unconfirmed response accepted", response)
		}
	}
	for _, replacement := range []string{"--config", "/tmp/attacker.sock", "https://outside.invalid/", "--output", "If-Match: wrong"} {
		changed := append([]string(nil), args...)
		changed[len(changed)-1] = replacement
		if AllowedServeCommand(body, changed) {
			t.Fatal("argument substitution admitted", replacement)
		}
	}
	if AllowedServeCommand(body, append(args, "--output", "/etc/sudoers")) {
		t.Fatal("extra OS argument admitted")
	}
	for _, data := range []string{
		`null`, `{"AllowFunnel":{"node.owner.ts.net:443":true}}`,
		strings.ReplaceAll(string(body), "tailscale.sock", "core.sock"),
		strings.ReplaceAll(string(body), "unix:/var/lib/fased-app-vps-1000/app-state/tailscale.sock", "http://127.0.0.1:7332"),
		strings.ReplaceAll(string(body), "node.owner.ts.net:443", "outside.invalid:443"),
		strings.ReplaceAll(string(body), `"HTTPS":true`, `"HTTPS":true,"TCPForward":"127.0.0.1:22"`),
	} {
		if _, err := serveCommand([]byte(data), tag); err == nil {
			t.Fatal("expanded Serve authority admitted", data)
		}
	}
	for _, tag := range []string{"", strings.Repeat("a", 63), "W/" + tag, tag + "\r\nOther: injected"} {
		if _, err := serveCommand(body, tag); err == nil {
			t.Fatal("unsafe conditional identity admitted")
		}
	}
	emptyArgs, err := serveCommand([]byte("{}"), tag)
	if err != nil || !AllowedServeCommand([]byte("{}"), emptyArgs) {
		t.Fatal("owned preflight cleanup refused", err)
	}
}

func TestServeHandoffDoesNotElevateForeignPolicy(t *testing.T) {
	calls := 0
	m := Manager{SetConfig: func(context.Context, []byte, string) error { return servePermissionError{} }}.WithServeAuthorization(func(context.Context, []byte, string) error { calls++; return nil })
	if err := m.SetConfig(t.Context(), []byte(`{"AllowFunnel":{"node.owner.ts.net:443":true}}`), strings.Repeat("a", 64)); err == nil || calls != 0 {
		t.Fatal("foreign policy elevated", err, calls)
	}
}

func TestPredeploymentProtectedServeHandoffCleansUpWithoutDeployment(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			dir := fixture(t)
			current := []byte("{}")
			writes := 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			m := Manager{GetConfig: func(context.Context) ([]byte, string, error) {
				return current, fmt.Sprintf("%x", sha256.Sum256(current)), nil
			}, SetConfig: func(context.Context, []byte, string) error { return servePermissionError{} }}
			m = m.WithServeAuthorization(func(ctx context.Context, body []byte, tag string) error {
				return SetServeWithOS(ctx, body, tag, func(_ context.Context, data []byte, args ...string) ([]byte, error) {
					if !AllowedServeCommand(data, args) || tag != fmt.Sprintf("%x", sha256.Sum256(current)) {
						t.Fatal("unbound OS write")
					}
					writes++
					current = append([]byte(nil), data...)
					if writes == 1 {
						cancel()
						if lost {
							return nil, errors.New("lost committed OS response")
						}
					}
					return []byte("200"), nil
				})
			})
			_, err := m.BeforeInstall(ctx, dir, Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "http://127.0.0.1:7332"}, io.Discard)
			if err == nil || string(current) != "{}" || writes != 2 {
				t.Fatal("failed predeployment left authority or retried mutation", err, string(current), writes)
			}
			if _, err := os.Stat(filepath.Join(dir, "private-setup.json")); !os.IsNotExist(err) {
				t.Fatal("owned cleanup receipt retained", err)
			}
		})
	}
}

func TestProtectedIngressHandoffRecoversCommittedOSWriteForward(t *testing.T) {
	dir := fixture(t)
	old := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "http://127.0.0.1:7332"}
	next := old
	next.Target = "unix:/var/lib/fased-app-vps-1000/app-state/tailscale.sock"
	if err := core.WritePrivateJSON(filepath.Join(dir, "private-access.json"), receipt{Schema: 1, Phase: "ready", Plan: old}); err != nil {
		t.Fatal(err)
	}
	current, _ := json.Marshal(expected(old))
	writes := 0
	m := Manager{GetConfig: func(context.Context) ([]byte, string, error) {
		return current, fmt.Sprintf("%x", sha256.Sum256(current)), nil
	}, SetConfig: func(context.Context, []byte, string) error { return servePermissionError{} }, Client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"ok","apiVersion":"1","instanceId":"` + strings.Repeat("a", 43) + `","connection":{"location":"remote","transport":"private-https"}}`))}, nil
	})}}
	m = m.WithServeAuthorization(func(ctx context.Context, data []byte, tag string) error {
		return SetServeWithOS(ctx, data, tag, func(_ context.Context, data []byte, args ...string) ([]byte, error) {
			writes++
			if !AllowedServeCommand(data, args) {
				t.Fatal("invalid OS handoff")
			}
			current = append([]byte(nil), data...)
			return nil, errors.New("lost response")
		})
	})
	if err := m.Enable(t.Context(), dir, next, strings.Repeat("a", 43)); err == nil || writes != 1 {
		t.Fatal("ambiguous root write accepted/replayed", err, writes)
	}
	if err := m.Enable(t.Context(), dir, next, strings.Repeat("a", 43)); err != nil || writes != 1 {
		t.Fatal("committed transition did not recover forward", err, writes)
	}
	r, err := load(dir)
	if err != nil || r.Phase != "ready" || r.Plan.Target != next.Target || r.Next != nil {
		t.Fatal("transition receipt lost", r, err)
	}
}
