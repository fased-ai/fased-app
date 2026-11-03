package privateaccess

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPredeploymentRequiresPrivateOwnerProof(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		trusted              bool
		origin, code, funnel string
		want                 int
	}{
		{"approved", true, "https://node.owner.ts.net", "secret", "", 204},
		{"local spoof", false, "https://node.owner.ts.net", "secret", "", 403},
		{"wrong proof", true, "https://node.owner.ts.net", "wrong", "", 403},
		{"cross origin", true, "https://attacker.test", "secret", "", 403},
		{"funnel", true, "https://node.owner.ts.net", "secret", "?1", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			approved := make(chan tailnet.Identity, 1)
			calls := 0
			handler := setupProbe("https://node.owner.ts.net", "secret", func(*http.Request) bool { return tc.trusted }, func(context.Context, string) (tailnet.Identity, error) {
				calls++
				return tailnet.Identity{UserID: 7, NodeID: "approved", Login: "owner@example.test"}, nil
			}, approved)
			request := httptest.NewRequest("POST", "http://localhost/approve", strings.NewReader(`{"code":"`+tc.code+`"}`))
			request.Header.Set("Origin", tc.origin)
			request.Header.Set("X-Forwarded-Host", "node.owner.ts.net")
			request.Header.Set("X-Forwarded-Proto", "https")
			request.Header.Set("Tailscale-Funnel-Request", tc.funnel)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, request)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body)
			}
			if tc.want != 204 && (calls != 0 || len(approved) != 0) {
				t.Fatal("untrusted setup affected deployment gate")
			}
		})
	}
}
func TestRemotePredeploymentCannotUseSSH(t *testing.T) {
	called := false
	m := Manager{GetConfig: func(context.Context) ([]byte, string, error) { called = true; return []byte(`{}`), "tag", nil }}
	if _, err := m.BeforeInstall(t.Context(), t.TempDir(), Plan{Mode: "ssh"}, nil); err == nil || called {
		t.Fatal("SSH bypassed prerequisite or touched Serve")
	}
}
func TestPrivateTargetRejectsMaintenanceSocket(t *testing.T) {
	for _, target := range []string{"unix:/tmp/core.sock", "unix:relative/tailscale.sock", "unix:/tmp/../tail/tailscale.sock", "http://0.0.0.0:7331"} {
		if validTarget(target) {
			t.Fatal(target)
		}
	}
	if !validTarget("unix:/var/lib/fased/app-state/tailscale.sock") {
		t.Fatal("dedicated ingress refused")
	}
}

func TestPredeploymentLostWriteResponseIsReconciled(t *testing.T) {
	dir := fixture(t)
	current := []byte(`{}`)
	writes := 0
	m := Manager{GetConfig: func(context.Context) ([]byte, string, error) { return current, "etag", nil }, SetConfig: func(_ context.Context, data []byte, _ string) error {
		writes++
		current = append([]byte(nil), data...)
		if writes == 1 {
			return errors.New("lost committed response")
		}
		return nil
	}}
	_, setupErr := m.BeforeInstall(t.Context(), dir, Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net", Target: "http://127.0.0.1:7331"}, io.Discard)
	if setupErr == nil {
		t.Fatal("lost readiness admitted deployment")
	}
	if string(current) != "{}" || writes != 2 {
		t.Fatal("owned route was left dangling", writes, string(current), setupErr)
	}
	if _, err := os.Stat(filepath.Join(dir, "private-setup.json")); !os.IsNotExist(err) {
		t.Fatal("reconciled ownership receipt retained", err)
	}
}
func TestPredeploymentNeverDisclosesApprovalProofInHTML(t *testing.T) {
	secret := "approval-proof-must-not-be-in-page"
	h := setupProbe("https://node.owner.ts.net", secret, func(*http.Request) bool { return true }, nil, make(chan tailnet.Identity, 1))
	r := httptest.NewRequest("GET", "http://localhost/", nil)
	r.Header.Set("X-Forwarded-Host", "node.owner.ts.net")
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Header().Get("Content-Security-Policy"), secret) {
		t.Fatal("approval proof exposed")
	}
}
