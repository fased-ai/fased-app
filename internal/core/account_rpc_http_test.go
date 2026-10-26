package core

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

func TestRPCConfigHTTPAuthorizationAndSecrets(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	r := defaultAccounts()
	r.Accounts = []Account{publicRecord()}
	v, err := s.UpdateAccounts(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(RPCConfigInput{AccountID: r.Accounts[0].ID, Revision: v.Record.Revision, Endpoint: FixtureRPCURL, Network: signer.FixtureNetwork})
	for _, tc := range []struct {
		name, auth, csrf, body string
		status                 int
	}{
		{"owner required", "", "", string(b), 401},
		{"CSRF", token, "", string(b), 403},
		{"secret", token, csrfToken(token), string(b[:len(b)-1]) + `,"apiKey":"disposable-refused"}`, 400},
		{"save", token, csrfToken(token), string(b), 200},
		{"stale", token, csrfToken(token), string(b), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := s.accountsRecord(t.Context())
			req := httptest.NewRequest("PUT", "http://127.0.0.1:7331/api/v1/accounts/rpc", bytes.NewBufferString(tc.body))
			req.Header.Set("Origin", "http://127.0.0.1:7331")
			req.Header.Set("Content-Type", "application/json")
			if tc.auth != "" {
				req.Header.Set("Authorization", "Bearer "+tc.auth)
			}
			req.Header.Set("X-CSRF-Token", tc.csrf)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			after, _ := s.accountsRecord(t.Context())
			if tc.status != 200 && after.Revision != before.Revision {
				t.Fatal("refused request wrote state")
			}
		})
	}
}
