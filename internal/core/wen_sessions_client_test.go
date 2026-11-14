package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWENSessionRehearsalClientCompleteHistory(t *testing.T) {
	for _, tc := range []struct {
		name, route string
		bytes       int
		ok          bool
	}{{"complete-session-history", "/api/v1/wen/sessions?profile=wen-operator", 40000, true}, {"complete-step-history", "/api/v1/wen/sessions/step", 40000, true}, {"other-endpoint-limit-unchanged", "/api/v1/setup", 40000, false}, {"session-response-bounded", "/api/v1/wen/sessions?profile=wen-operator", WENSessionResponseLimit + 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]string{"history": strings.Repeat("x", tc.bytes)})
			}))
			defer server.Close()
			client := &Client{HTTP: server.Client()}
			client.Endpoint.URL = server.URL
			var body map[string]string
			e := client.Do(t.Context(), http.MethodGet, tc.route, nil, &body)
			if (e == nil) != tc.ok {
				t.Fatal("complete bounded history", e)
			}
			if tc.ok && len(body["history"]) != tc.bytes {
				t.Fatal("history clipped")
			}
		})
	}
}
