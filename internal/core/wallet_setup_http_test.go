package core

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWalletSetupHTTPAuthorizationAndSecretRefusal(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	s.rpcAdmission = true
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := AccountSetupCommand{Action: "watch", Name: "Watch", Network: "devnet", Address: testRef().PublicIdentity().Address}
	b, _ := json.Marshal(c)
	for _, tc := range []struct {
		name, auth, csrf, method, body string
		status                         int
	}{
		{"owner required", "", "", "PUT", string(b), 401},
		{"csrf", token, "", "PUT", string(b), 403},
		{"seed", token, csrfToken(token), "PUT", string(b[:len(b)-1]) + `,"seed":"disposable-not-accepted"}`, 400},
		{"password", token, csrfToken(token), "PUT", string(b[:len(b)-1]) + `,"password":"disposable-not-accepted"}`, 400},
		{"key", token, csrfToken(token), "PUT", string(b[:len(b)-1]) + `,"privateKey":"disposable-not-accepted"}`, 400},
		{"create mutation", token, csrfToken(token), "PUT", `{"revision":0,"action":"create","name":"Wallet","network":"devnet"}`, 400},
		{"method", token, csrfToken(token), "POST", string(b), 405},
		{"save", token, csrfToken(token), "PUT", string(b), 200},
		{"stale", token, csrfToken(token), "PUT", string(b), 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := s.accountsRecord(t.Context())
			req := httptest.NewRequest(tc.method, "http://127.0.0.1:7331/api/v1/accounts/setup", bytes.NewBufferString(tc.body))
			req.Header.Set("Origin", "http://127.0.0.1:7331")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", tc.csrf)
			if tc.auth != "" {
				req.Header.Set("Authorization", "Bearer "+tc.auth)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			after, _ := s.accountsRecord(t.Context())
			if tc.status != 200 && before.Revision != after.Revision {
				t.Fatal("refusal wrote state")
			}
		})
	}
}
