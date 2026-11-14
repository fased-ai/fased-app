package core

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWENMarketDataReadBoundary(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.Path != "/api/mining/v1/research" {
			t.Error("authority forwarded", r)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("lookback") == "60" {
			w.Header().Set("Location", "http://127.0.0.1:1/")
			w.WriteHeader(302)
			return
		}
		if r.URL.Query().Get("lookback") == "240" {
			w.Write([]byte(strings.Repeat("x", 262145)))
			return
		}
		w.Write([]byte(`{"schema":"wen-research-data/1","execution":"disabled","method":"wen-kraken-research/1"}`))
	}))
	defer up.Close()
	server := &Server{HTTP: &http.Server{Handler: h}}
	for _, bad := range []string{"http://localhost:10", "https://127.0.0.1:10", "http://127.0.0.1:1/path", "http://127.0.0.1:1?x=1", "http://u@127.0.0.1:1", "http://127.0.0.1:99999"} {
		if server.ConfigureWENResearch(bad) == nil {
			t.Fatal("accepted", bad)
		}
	}
	if e = server.ConfigureWENResearch(up.URL); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		method, path, auth string
		code               int
	}{{"GET", "", "", 401}, {"POST", "", token, 405}, {"GET", "?lookback=2", token, 400}, {"GET", "?lookback=1&lookback=30", token, 400}, {"GET", "?owner=secret", token, 400}, {"GET", "?lookback=1", token, 200}, {"GET", "?lookback=60", token, 503}, {"GET", "?lookback=240", token, 503}} {
		w := apiRequest(h, tc.method, "/api/v1/wen/market-data"+tc.path, "", tc.auth, "http://127.0.0.1:7331", "")
		if w.Code != tc.code {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if calls != 3 {
		t.Fatal("refused call reached upstream", calls)
	}
}
