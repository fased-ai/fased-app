package core

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestNativeOwnerSessionIsPrivateAndPreservesRecoveryFence(t *testing.T) {
	t.Parallel()
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
	s.protectedOwnerUID = 1000
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

func TestProtectedLocalOwnerCanOpenBrowserWithoutWalletAuthority(t *testing.T) {
	t.Parallel()
	s, err := OpenStore(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getuid() <= 0 {
		t.Skip("ordinary app identity required")
	}
	config := signer.Config{Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.OwnerKeyProvenance, Network: "devnet", SignerID: strings.Repeat("a", 32), OwnerUID: os.Getuid() + 1, AppUID: os.Getuid(), SignerUID: os.Getuid() + 2, StateDir: "/fixture/signer-state", SocketDir: "/fixture/signer-ipc"}
	if err = s.configureVerifiedSigner(config); err != nil {
		t.Fatal("verified signer identity refused", err)
	}
	if s.custodyOwnerSupported || s.protectedOwnerUID != config.OwnerUID {
		t.Fatal("owner sign-in must not enable wallet authority")
	}
	driver := fixtureUpdateController()
	if err = s.BindApplicationUpdateController(driver); err != nil {
		t.Fatal(err)
	}
	invalid := config
	invalid.OwnerUID = invalid.AppUID
	if err = s.configureVerifiedSigner(invalid); err == nil || s.protectedOwnerUID != config.OwnerUID {
		t.Fatal("invalid owner identity changed protected Local access")
	}
	invalid = config
	invalid.AppUID, invalid.OwnerUID = config.OwnerUID, config.AppUID
	if err = s.configureVerifiedSigner(invalid); err == nil || s.protectedOwnerUID != config.OwnerUID {
		t.Fatal("different app identity changed protected Local access")
	}
	h.instanceLocation, h.instanceOwnerUID = "local", config.OwnerUID
	call := func(private bool, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "http://127.0.0.1:7331"+path, bytes.NewBufferString(body))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if private {
			privateIPCHandler(h).ServeHTTP(w, r)
		} else {
			h.ServeHTTP(w, r)
		}
		return w
	}
	if w := call(false, "/api/v1/auth/native-local", `{"action":"launch"}`); w.Code != 403 {
		t.Fatal("network acquired a Local launch proof", w.Code)
	}
	h.instanceOwnerUID++
	if w := call(true, "/api/v1/auth/native-local", `{"action":"launch"}`); w.Code != 403 {
		t.Fatal("different owner acquired a Local launch proof", w.Code)
	}
	h.instanceOwnerUID = config.OwnerUID
	h.instanceLocation = "remote"
	if w := call(true, "/api/v1/auth/native-local", `{"action":"launch"}`); w.Code != 403 {
		t.Fatal("Remote acquired a Local launch proof", w.Code)
	}
	h.instanceLocation = "local"
	h.publicHost = "remote.invalid"
	if w := call(true, "/api/v1/auth/native-local", `{"action":"launch"}`); w.Code != 403 {
		t.Fatal("public origin acquired a Local launch proof", w.Code)
	}
	h.publicHost = ""
	w := call(true, "/api/v1/auth/native-local", `{"action":"launch"}`)
	var launch struct {
		Code string `json:"code"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &launch) != nil || len(launch.Code) != 43 {
		t.Fatal("private owner launch proof unavailable", w.Code, w.Body.String())
	}
	body, _ := json.Marshal(map[string]string{"action": "exchange", "code": launch.Code})
	if w = call(false, "/api/v1/auth/local", string(body)); w.Code != 200 {
		t.Fatal("browser could not exchange the owner proof", w.Code, w.Body.String())
	}
	var browser struct {
		Token string `json:"sessionToken"`
		CSRF  string `json:"csrfToken"`
	}
	if json.Unmarshal(w.Body.Bytes(), &browser) != nil || browser.Token == "" || browser.CSRF == "" {
		t.Fatal("browser session missing")
	}
	if w = call(false, "/api/v1/auth/local", string(body)); w.Code != 401 {
		t.Fatal("browser reused an owner proof", w.Code)
	}
	if w = call(true, "/api/v1/auth/local", `{"action":"session","code":""}`); w.Code != 403 {
		t.Fatal("managed owner bypassed its protected channel", w.Code)
	}
	if w = call(true, "/api/v1/auth/native-local", `{"action":"session"}`); w.Code != 200 {
		t.Fatal("protected CLI session required wallet activation", w.Code, w.Body.String())
	}
	var cli struct {
		Token string `json:"sessionToken"`
		CSRF  string `json:"csrfToken"`
	}
	if json.Unmarshal(w.Body.Bytes(), &cli) != nil || cli.Token == "" || cli.CSRF == "" {
		t.Fatal("CLI session missing")
	}
	updates := func(token, csrf, action string, view ApplicationUpdateView) ApplicationUpdateView {
		t.Helper()
		method, payload := "GET", []byte(nil)
		if action != "" {
			method = "POST"
			command := ApplicationUpdateCommand{Action: action, Revision: view.Revision, PolicyRevision: view.PolicyRevision}
			if action == "apply" {
				command.ManifestSHA256 = view.Operation.Release.ManifestSHA256
			}
			payload, _ = json.Marshal(command)
		}
		r := httptest.NewRequest(method, "http://127.0.0.1:7331/api/v1/updates/application", bytes.NewReader(payload))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-CSRF-Token", csrf)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		var result ApplicationUpdateView
		if (out.Code != 200 && out.Code != 202) || json.Unmarshal(out.Body.Bytes(), &result) != nil {
			t.Fatalf("shared Update %s failed: %d %s", action, out.Code, out.Body.String())
		}
		return result
	}
	view := updates(browser.Token, browser.CSRF, "", ApplicationUpdateView{})
	updates(browser.Token, browser.CSRF, "check", view)
	updateTickFixture(t, s)
	view = updates(cli.Token, cli.CSRF, "", ApplicationUpdateView{})
	if view.Operation.Phase != "available" {
		t.Fatal("CLI did not observe the browser check", view.Operation.Phase)
	}
	queued := updates(cli.Token, cli.CSRF, "apply", view)
	updateTickFixture(t, s)
	result := updates(browser.Token, browser.CSRF, "", ApplicationUpdateView{})
	cliResult := updates(cli.Token, cli.CSRF, "", ApplicationUpdateView{})
	if result.Operation.Phase != "completed" || driver.applies != 1 || result.Operation.ID != queued.Operation.ID || cliResult.Operation.ID != result.Operation.ID {
		t.Fatal("browser and CLI did not share one update", result.Operation.Phase, driver.applies)
	}
	if w = call(true, "/api/v1/auth/native-local", `{"action":"wallet"}`); w.Code != 400 {
		t.Fatal("protected owner channel accepted an unrelated action", w.Code)
	}
	if capabilities := s.Health().Capabilities; capabilities["walletSigner"] || capabilities["financialExecution"] {
		t.Fatal("Local owner access enabled a held capability")
	}
}
