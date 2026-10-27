package connection

import (
	"context"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOrigins(t *testing.T) {
	for _, value := range []string{"https://node.owner.ts.net", "https://node.owner.ts.net/", "http://127.0.0.1:7331", "http://[::1]:7331", "http://localhost:7331"} {
		if _, e := Origin(value); e != nil {
			t.Errorf("valid %q: %v", value, e)
		}
	}
	for _, value := range []string{"", "http://node.owner.ts.net", "file:///tmp/a", "javascript:alert(1)", "https://user:pass@node.owner.ts.net", "https://node.owner.ts.net/x", "https://node.owner.ts.net/?token=x", "https://node.owner.ts.net/?", "https://node.owner.ts.net/#x", "https://node.owner.ts.net:0", "https://node.owner.ts.net:65536", "https://node.owner.ts.net:", "https://node.owner.ts.net\\evil", "https://node.owner.ts.net/\nextra"} {
		if _, e := Origin(value); e == nil {
			t.Errorf("unsafe origin accepted %q", value)
		}
	}
}
func TestProbeIdentityTLSAndNoCredentialForwarding(t *testing.T) {
	id := strings.Repeat("a", 43)
	var requests int
	h := core.Health{Status: "ok", APIVersion: core.APIVersion, Version: core.Version, InstanceID: id, Connection: &core.ConnectionInfo{Location: "remote", Transport: "private-https"}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/api/v1/health" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected request %v", r)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(h)
	}))
	defer server.Close()
	good, e := probe(context.Background(), server.URL, id, server.Client())
	if e != nil || good.InstanceID != id || good.InstallationCreated {
		t.Fatal(good, e)
	}
	if _, e = probe(context.Background(), server.URL, strings.Repeat("b", 43), server.Client()); e == nil {
		t.Fatal("changed instance accepted")
	}
	if _, e = Probe(context.Background(), server.URL, id); e == nil {
		t.Fatal("untrusted certificate accepted")
	}
	h.APIVersion = "2"
	if _, e = probe(context.Background(), server.URL, "", server.Client()); e == nil {
		t.Fatal("unknown API accepted")
	}
	h.APIVersion = "1"
	h.Connection = nil
	if _, e = probe(context.Background(), server.URL, "", server.Client()); e == nil {
		t.Fatal("missing runtime descriptor accepted")
	}
	if requests != 4 {
		t.Fatal("unexpected operations/retries", requests)
	}
}
func TestProbeRefusesRedirectAndOversize(t *testing.T) {
	var followed int
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed++ }))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer redirect.Close()
	if _, e := Probe(context.Background(), redirect.URL, ""); e == nil || followed != 0 {
		t.Fatal("redirect followed")
	}
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(strings.Repeat(" ", 16385)))
	}))
	defer large.Close()
	if _, e := Probe(context.Background(), large.URL, ""); e == nil {
		t.Fatal("oversized reply accepted")
	}
}
