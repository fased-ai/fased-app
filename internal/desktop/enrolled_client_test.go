package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestNativeHealthIsFreshBoundAndPreservesResponse(t *testing.T) {
	const instance = "fixture-local-instance"
	for _, tc := range []struct {
		name        string
		change      func(*core.Health)
		contentType string
		raw         string
		status      int
		allow       bool
	}{
		{name: "bound owner", allow: true},
		{name: "foreign instance", change: func(h *core.Health) { h.InstanceID = "other" }},
		{name: "foreign owner", change: func(h *core.Health) { h.Connection.OwnerUID++ }},
		{name: "remote location", change: func(h *core.Health) { h.Connection.Location = "remote" }},
		{name: "remote transport", change: func(h *core.Health) { h.Connection.Transport = "private-https" }},
		{name: "missing connection", change: func(h *core.Health) { h.Connection = nil }},
		{name: "wrong API", change: func(h *core.Health) { h.APIVersion = "other" }},
		{name: "unready", change: func(h *core.Health) { h.Status = "starting" }},
		{name: "wrong content type", contentType: "text/plain"},
		{name: "malformed", raw: "{"},
		{name: "oversized", raw: strings.Repeat(" ", 32*1024+1)},
		{name: "unavailable", status: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			health := core.Health{Status: "ok", APIVersion: core.APIVersion, InstanceID: instance, Connection: &core.ConnectionInfo{Location: "local", Transport: "loopback", OwnerUID: 1800}}
			if tc.change != nil {
				tc.change(&health)
			}
			data, err := json.Marshal(health)
			if err != nil {
				t.Fatal(err)
			}
			// Preserve future health fields in the response delivered to the UI.
			data = append(data[:len(data)-1], []byte(`,"futureField":"retained"}`)...)
			if tc.raw != "" {
				data = []byte(tc.raw)
			}
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/health" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("unexpected native health request")
				}
				contentType := tc.contentType
				if contentType == "" {
					contentType = "application/json; charset=utf-8"
				}
				w.Header().Set("Content-Type", contentType)
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write(data)
			}))
			defer s.Close()
			client := &core.Client{Endpoint: core.Endpoint{URL: s.URL, InstanceID: instance}, HTTP: s.Client()}
			got, err := verifiedNativeHealth(t.Context(), client, instance, 1800)
			if (err == nil) != tc.allow {
				t.Fatalf("health result = %v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("health required %d requests", calls.Load())
			}
			if tc.allow && string(got) != string(data) {
				t.Fatal("verified health response was changed")
			}
		})
	}
}

func TestNativeHandlerReusesOnlyItsOwnVerifiedHealth(t *testing.T) {
	const instance = "fixture-local-instance"
	var healthCalls, stateCalls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/health":
			healthCalls.Add(1)
			_, _ = w.Write([]byte(`{"status":"ok","apiVersion":"1","instanceId":"fixture-local-instance","connection":{"location":"local","transport":"loopback","ownerUid":1800},"futureField":"retained"}`))
		case "/api/v1/auth/state":
			stateCalls.Add(1)
			_, _ = w.Write([]byte(`{"claimed":true,"authenticated":false}`))
		default:
			t.Error("unexpected core route", r.URL.Path)
		}
	}))
	defer s.Close()
	h := NewHandler("fixture", nil)
	h.newClient = func(ctx context.Context, _ string) (*core.Client, bool, json.RawMessage, error) {
		c := &core.Client{Endpoint: core.Endpoint{URL: s.URL, InstanceID: instance}, HTTP: s.Client()}
		health, err := verifiedNativeHealth(ctx, c, instance, 1800)
		return c, true, health, err
	}
	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://wails.localhost"+path, nil))
		return w
	}
	w := request("/api/v1/health")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"futureField":"retained"`) || healthCalls.Load() != 1 {
		t.Fatalf("health was not reused: code=%d calls=%d body=%s", w.Code, healthCalls.Load(), w.Body)
	}
	w = request("/api/v1/auth/state")
	if w.Code != http.StatusOK || healthCalls.Load() != 2 || stateCalls.Load() != 1 || !strings.Contains(w.Body.String(), `"localAccess":true`) {
		t.Fatalf("ordinary request was not forwarded: code=%d health=%d state=%d body=%s", w.Code, healthCalls.Load(), stateCalls.Load(), w.Body)
	}
	h.instance = "foreign-instance"
	if w = request("/api/v1/health"); w.Code != http.StatusConflict || healthCalls.Load() != 3 {
		t.Fatal("foreign instance received the verified health response")
	}
}

func TestOriginalLocalStartRefusesInvalidEnrollmentFence(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Local custody enrollment is Linux only")
	}
	dir := t.TempDir()
	if allowed, err := OriginalLocalStartAllowed(dir, os.Getuid()); err != nil || !allowed {
		t.Fatalf("ordinary Local source was blocked: allowed=%v err=%v", allowed, err)
	}
	link := filepath.Join(t.TempDir(), "linked-source")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if allowed, err := OriginalLocalStartAllowed(link, os.Getuid()); err == nil || allowed {
		t.Fatal("symlinked source was allowed")
	}
	if allowed, err := OriginalLocalStartAllowed("/etc", os.Getuid()); err == nil || allowed {
		t.Fatal("foreign root-owned directory was allowed as an unfenced source")
	}
}
