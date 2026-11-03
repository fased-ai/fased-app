package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

func TestLocalOwnerLaunchIsPrivateSingleUseAndPreservesPassword(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			s, err := OpenStore(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if existing {
				code, e := s.IssueClaimCode(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.Claim(t.Context(), code.Code, "preserved-owner-password"); e != nil {
					t.Fatal(e)
				}
			}
			h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			call := func(private bool, action, code string) *httptest.ResponseRecorder {
				b, _ := json.Marshal(map[string]string{"action": action, "code": code})
				r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/local", bytes.NewReader(b))
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
			for _, action := range []string{"launch", "session"} {
				if w := call(false, action, ""); w.Code != http.StatusForbidden {
					t.Fatalf("HTTP acquired OS authority: %d", w.Code)
				}
			}
			w := call(true, "launch", "")
			if w.Code != 200 {
				t.Fatalf("OS launch: %d %s", w.Code, w.Body)
			}
			var grant struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(w.Body.Bytes(), &grant) != nil || len(grant.Code) != 43 {
				t.Fatal("missing private grant")
			}
			if w = call(false, "exchange", "wrong"); w.Code != 401 {
				t.Fatal("invalid grant admitted", w.Code)
			}
			w = call(false, "exchange", grant.Code)
			if w.Code != 200 {
				t.Fatal("launch exchange", w.Code, w.Body)
			}
			var session struct {
				SessionToken string `json:"sessionToken"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &session)
			if !s.Session(t.Context(), session.SessionToken) {
				t.Fatal("no ordinary authenticated session")
			}
			if w = call(false, "exchange", grant.Code); w.Code != 401 {
				t.Fatal("grant replayed")
			}
			if _, e := s.Login(t.Context(), "preserved-owner-password"); (e == nil) != existing {
				t.Fatal("password identity changed", e)
			}
			h.instanceLocation = "remote"
			if w = call(true, "session", ""); w.Code != 403 {
				t.Fatal("Remote acquired Local identity")
			}
			h.instanceLocation = "local"
			s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
				return signer.Response{}, errors.New("not called")
			}
			if w = call(true, "session", ""); w.Code != 403 {
				t.Fatal("separately owned custody acquired Local identity", w.Code)
			}
			state := httptest.NewRecorder()
			h.ServeHTTP(state, httptest.NewRequest("GET", "http://127.0.0.1:7331/api/v1/auth/state", nil))
			var auth struct {
				LocalAccess bool `json:"localAccess"`
			}
			if state.Code != 200 || json.Unmarshal(state.Body.Bytes(), &auth) != nil || auth.LocalAccess {
				t.Fatal("custody must retain protected sign-in", state.Code, state.Body)
			}
		})
	}
}

func TestLocalOwnerLaunchExpiresAndDoesNotCrossRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	code, err := s.issueLocalLaunch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.exchangeLocalLaunch(t.Context(), code); err != nil {
		t.Fatal(err)
	}
	if _, err = s.exchangeLocalLaunch(t.Context(), code); err == nil {
		t.Fatal("replay accepted")
	}
	code, err = s.issueLocalLaunch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := s.now()
	s.now = func() time.Time { return now.Add(localLaunchLifetime) }
	if _, err = s.exchangeLocalLaunch(t.Context(), code); err == nil {
		t.Fatal("expired proof admitted")
	}
	s.now = time.Now
	code, err = s.issueLocalLaunch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err = restarted.exchangeLocalLaunch(t.Context(), code); err == nil {
		t.Fatal("proof survived restart")
	}
}

func TestLocalOwnerCLIAndBrowserShareManualAndAutomaticUpdater(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Drive controlled ticks explicitly; this is API/controller evidence, not
	// a claim of installed file replacement or release authenticity.
	srv.taskCancel()
	<-srv.taskDone
	driver := fixtureUpdateController()
	if err = srv.Store.BindApplicationUpdateController(driver); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	defer srv.Close(context.Background())
	cli, err := NewClient(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if err = cli.LoadSession(t.Context(), dir); err != nil {
		t.Fatal("password-free CLI", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "cli-session.json")); err != nil {
		t.Fatal(err)
	}
	proof, err := cli.LocalLaunch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	web := &Client{Endpoint: cli.Endpoint, HTTP: &http.Client{}}
	if err = web.authenticate(t.Context(), "/api/v1/auth/local", map[string]string{"action": "exchange", "code": proof}); err != nil {
		t.Fatal(err)
	}
	read := func(c *Client) ApplicationUpdateView {
		t.Helper()
		var v ApplicationUpdateView
		if e := c.Do(t.Context(), "GET", "/api/v1/updates/application", nil, &v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	command := func(c *Client, action string) {
		t.Helper()
		v := read(c)
		r := ApplicationUpdateCommand{Action: action, Revision: v.Revision, PolicyRevision: v.PolicyRevision}
		if action == "apply" {
			r.ManifestSHA256 = v.Operation.Release.ManifestSHA256
		}
		if e := c.Do(t.Context(), "POST", "/api/v1/updates/application", r, nil); e != nil {
			t.Fatal(e)
		}
	}
	command(web, "check")
	updateTickFixture(t, srv.Store)
	if read(cli).Operation.Phase != "available" {
		t.Fatal("CLI cannot observe UI check")
	}
	command(cli, "apply")
	updateTickFixture(t, srv.Store)
	view := read(web)
	if view.Operation.Phase != "completed" || driver.applies != 1 || read(cli).Operation.ID != view.Operation.ID {
		t.Fatal("UI/CLI operation diverged", view)
	}
	// Another release is consumed by the same scheduler/controller after all
	// browser/CLI sessions are gone; no password, wallet unlock or SSH actor.
	driver.release.Sequence++
	driver.release.ManifestSHA256 = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	var prefs UpdatePreferencesView
	if err = cli.Do(t.Context(), "GET", "/api/v1/updates/preferences", nil, &prefs); err != nil {
		t.Fatal(err)
	}
	if err = cli.Do(t.Context(), "PUT", "/api/v1/updates/preferences", UpdatePreferencesRequest{Action: "policy", Revision: prefs.Revision, Policy: &UpdatePolicy{Mode: "automatic", Notify: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err = srv.Store.Logout(t.Context(), web.Token); err != nil {
		t.Fatal(err)
	}
	if err = srv.Store.Logout(t.Context(), cli.Token); err != nil {
		t.Fatal(err)
	}
	commandErr := cli.loadSavedSession(t.Context(), dir)
	if !errors.Is(commandErr, ErrUnauthorized) {
		t.Fatal("revoked CLI was still authenticated", commandErr)
	}
	nextCheck := srv.Store.now().Add(time.Hour)
	srv.Store.now = func() time.Time { return nextCheck }
	for n := 0; n < 5; n++ {
		updateTickFixture(t, srv.Store)
	}
	after, err := srv.Store.ApplicationUpdates(t.Context())
	if err != nil || !after.Operation.Automatic || after.Operation.Phase != "completed" || driver.applies != 2 {
		t.Fatal("unattended update diverged", after, err, driver)
	}
	updateModeFixture(t, srv.Store, UpdatePolicy{Mode: "manual", Notify: true})
}
