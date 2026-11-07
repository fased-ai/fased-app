package core

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestNativeOwnerSessionIsPrivateAndPreservesRecoveryFence(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	code, err := s.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.Claim(t.Context(), code.Code, "Disposable original owner password")
	if err != nil {
		t.Fatal(err)
	}
	var beforeSalt, beforeHash []byte
	if err = s.db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&beforeSalt, &beforeHash); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplicationUpdates(t.Context()); err != nil {
		t.Fatal(err)
	}
	release := fixtureUpdateController().release
	op := ApplicationUpdateOperation{ID: update.NewProbe(), Phase: "recovery", OwnerLease: update.NewProbe(), Release: &release}
	raw, _ := json.Marshal(op)
	if _, err = s.db.Exec("UPDATE application_updates SET document=? WHERE id=1", string(raw)); err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.instanceLocation, h.instanceOwnerUID = "local", 1000
	s.custodyOwnerSupported, s.custodyNetwork = true, "devnet"
	call := func(private bool, path, body, token, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7331"+path, bytes.NewBufferString(body))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if csrf != "" {
			r.Header.Set("X-Fased-CSRF", csrf)
		}
		w := httptest.NewRecorder()
		if private {
			privateIPCHandler(h).ServeHTTP(w, r)
		} else {
			h.ServeHTTP(w, r)
		}
		return w
	}
	if w := call(false, "/api/v1/auth/native-local", `{"action":"session"}`, "", ""); w.Code != 403 {
		t.Fatal("HTTP acquired native session", w.Code)
	}
	w := call(true, "/api/v1/auth/native-local", `{"action":"session"}`, "", "")
	var session struct {
		Token string `json:"sessionToken"`
		CSRF  string `json:"csrfToken"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &session) != nil || session.Token == "" || session.CSRF == "" {
		t.Fatal("native recovery login failed", w.Code, w.Body.String())
	}
	if w = call(false, "/api/v1/agents", `{}`, session.Token, session.CSRF); w.Code != 409 {
		t.Fatal("native session cleared recovery fence", w.Code)
	}
	retained, _, err := readApplicationUpdate(t.Context(), s.db)
	if err != nil || retained.Phase != "recovery" || retained.ID != op.ID {
		t.Fatal("update state changed", err)
	}
	var afterSalt, afterHash []byte
	if err = s.db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&afterSalt, &afterHash); err != nil || !bytes.Equal(beforeSalt, afterSalt) || !bytes.Equal(beforeHash, afterHash) {
		t.Fatal("owner password changed", err)
	}
	if !s.Session(t.Context(), old) {
		t.Fatal("original session lost")
	}
	if w = call(true, "/api/v1/auth/local", `{"action":"session"}`, "", ""); w.Code != 403 {
		t.Fatal("ordinary LocalOwner restriction weakened")
	}
}
