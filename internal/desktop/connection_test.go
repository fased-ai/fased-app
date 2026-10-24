package desktop

import (
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteConnectionDoesNotRequireOrStartLocalEngine(t *testing.T) {
	id := strings.Repeat("x", 43)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("unexpected probe")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(core.Health{Status: "ok", APIVersion: "1", InstanceID: id, Connection: &core.ConnectionInfo{Location: "remote", Transport: "loopback"}})
	}))
	defer target.Close()
	h := NewHandler("/nonexistent/fased-fixture-no-local-engine", nil)
	opened := 0
	h.openBrowser = func(url, expected string) error {
		if expected != id {
			t.Fatal("lost expected identity")
		}
		if url != target.URL {
			t.Fatal("changed address")
		}
		opened++
		return nil
	}
	body, _ := json.Marshal(map[string]string{"url": target.URL, "instanceId": id})
	request := func(origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://wails.localhost/api/v1/installation/connect", strings.NewReader(body))
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := request("http://wails.localhost", string(body))
	if w.Code != 200 || opened != 1 || !strings.Contains(w.Body.String(), `"installationCreated":false`) {
		t.Fatal(w.Code, w.Body.String(), opened)
	}
	w = request("https://evil.invalid", string(body))
	if w.Code != 403 || opened != 1 {
		t.Fatal("external origin opened browser")
	}
	w = request("http://wails.localhost", `{"url":"file:///tmp/a"}`)
	if w.Code != 400 || opened != 1 {
		t.Fatal("unsafe destination opened")
	}
	w = request("http://wails.localhost", `{"url":"`+target.URL+`","command":"ignored"}`)
	if w.Code != 400 || opened != 1 {
		t.Fatal("unknown native binding accepted")
	}
}
