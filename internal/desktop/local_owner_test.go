package desktop

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestNativeLocalOwnerRequiresNativeOriginAndPrivateCore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, err := core.Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve() }()
	defer s.Close(context.Background())
	h := NewHandler(dir, fstest.MapFS{})
	call := func(method, host, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://"+host+"/api/v1/installation/local-owner", strings.NewReader("{}"))
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, entry := range [][3]string{{"POST", "wails.localhost", "https://evil.example"}, {"POST", "127.0.0.1", "http://wails.localhost"}, {"GET", "wails.localhost", "http://wails.localhost"}} {
		if w := call(entry[0], entry[1], entry[2]); w.Code == 200 {
			t.Fatal("untrusted native access admitted")
		}
	}
	w := call("POST", "wails.localhost", "http://wails.localhost")
	var result struct {
		SessionToken string `json:"sessionToken"`
		CSRF         string `json:"csrfToken"`
		Mode         string `json:"sessionMode"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !s.Store.Session(t.Context(), result.SessionToken) || result.CSRF == "" || result.Mode != "bearer" {
		t.Fatal("native Local session", w.Code, w.Body)
	}
	// No native route is present on the public TCP listener.
	request := httptest.NewRequest("POST", s.URL+"/api/v1/installation/local-owner", strings.NewReader("{}"))
	request.Header.Set("Origin", s.URL)
	request.Header.Set("Content-Type", "application/json")
	refused := httptest.NewRecorder()
	s.HTTP.Handler.ServeHTTP(refused, request)
	if refused.Code == 200 {
		t.Fatal("network acquired native owner authority")
	}
}
