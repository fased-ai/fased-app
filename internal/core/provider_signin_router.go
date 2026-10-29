package core

// OpenRouter's public PKCE contract issues an API key. It is stored and billed
// exactly like an entered API key; no subscription, refresh token or plan grant.
// https://openrouter.ai/docs/guides/overview/auth/oauth
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func (s *Store) startRouterSignIn(c AIConnectionCommand) (AIConnectionView, error) {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if !s.providerSignInAvailableLocked() || s.provider.signIn != nil || c.Ref != "" {
		return AIConnectionView{}, ErrConflict
	}
	a := &providerSignInAttempt{name: strings.TrimSpace(c.Name), fixture: s.providerFixture}
	var err error
	if a.state, err = randomToken(); err != nil {
		return AIConnectionView{}, err
	}
	if a.verifier, err = randomToken(); err != nil {
		return AIConnectionView{}, err
	}
	id, err := randomToken()
	if err != nil {
		return AIConnectionView{}, err
	}
	a.view = ProviderSignInView{ID: id, Provider: "openrouter", State: "waiting", ExpiresAt: s.now().Add(5 * time.Minute).Unix(), Remote: c.Remote}
	var listener net.Listener
	if !c.Remote {
		listener, err = s.listenProviderSignInLocked(a)
		if err != nil {
			return AIConnectionView{}, err
		}
	}
	challenge := sha256.Sum256([]byte(a.verifier))
	q := url.Values{"code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}, "key_label": {"Fased"}}
	if !c.Remote {
		q.Set("callback_url", a.redirect)
		q.Set("state", a.state)
	}
	a.view.URL = "https://openrouter.ai/auth?" + q.Encode()
	a.wire = s.provider.signInTransport
	if a.wire == nil && a.fixture {
		a.wire = routerSignInFixture{verifier: a.verifier}
		if !c.Remote {
			a.view.URL = a.redirect + "?" + url.Values{"code": {"fixture-router-code"}, "state": {a.state}}.Encode()
		}
	} else if a.wire == nil && providerActivationEnabled && s.provider.apiCredentials {
		a.wire = newNativeProviderTransport("openrouter").wire
	}
	if a.wire == nil {
		if listener != nil {
			listener.Close()
		}
		return AIConnectionView{}, errors.New("sign-in transport unavailable")
	}
	s.provider.signIn = a
	a.timer = time.AfterFunc(5*time.Minute, func() {
		s.provider.mu.Lock()
		defer s.provider.mu.Unlock()
		if s.provider.signIn == a {
			s.cancelProviderSignInLocked()
		}
	})
	if listener != nil {
		go a.server.Serve(listener)
	}
	return s.aiConnectionLocked()
}
func routerSignInCallback(a *providerSignInAttempt, callback string) (string, error) {
	if a.view.Remote {
		if !validSignInValue(callback, 2048) || strings.ContainsAny(callback, "?/#&=") {
			return "", errors.New("enter the authorization code")
		}
		return callback, nil
	}
	u, err := url.Parse(callback)
	base, _ := url.Parse(a.redirect)
	if err != nil || len(callback) > 4096 || u.Scheme != base.Scheme || u.Host != base.Host || u.Path != base.Path || u.RawPath != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.ForceQuery {
		return "", errors.New("invalid sign-in callback")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 2 || len(q["state"]) != 1 || q.Get("state") != a.state || len(q["code"]) != 1 || !validSignInValue(q.Get("code"), 2048) {
		return "", errors.New("sign-in state or code mismatch")
	}
	return q.Get("code"), nil
}
func (s *Store) completeRouterSignIn(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	s.provider.mu.Lock()
	a := s.provider.signIn
	if a == nil || a.view.Provider != "openrouter" || a.view.ID != c.SignInID || a.busy || a.view.ExpiresAt <= s.now().Unix() {
		s.provider.mu.Unlock()
		return AIConnectionView{}, ErrConflict
	}
	callback := c.Callback
	if callback == "" {
		callback = a.callback
	}
	code, err := routerSignInCallback(a, callback)
	if err != nil {
		s.cancelProviderSignInLocked()
		s.provider.mu.Unlock()
		return AIConnectionView{}, err
	}
	a.busy = true
	wire, verifier := a.wire, a.verifier
	s.provider.mu.Unlock()
	// No retries: an authorization code can create exactly one API key.
	key, err := exchangeRouterSignIn(ctx, wire, code, verifier)
	mode := "openrouter"
	if a.fixture {
		mode = "test"
	}
	defer func() {
		s.provider.mu.Lock()
		defer s.provider.mu.Unlock()
		if s.provider.signIn == a {
			s.cancelProviderSignInLocked()
		}
	}()
	if err != nil || !validAICredential(mode, key) {
		return AIConnectionView{}, errors.New("Sign-in incomplete; review OpenRouter keys before reconnecting")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AIConnectionView{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE instance SET revision=revision WHERE id=1"); err != nil {
		return AIConnectionView{}, metadataWriteError(err)
	}
	s.provider.mu.Lock()
	if s.provider.signIn != a || !s.providerSignInAvailableLocked() || a.view.ExpiresAt <= s.now().Unix() || !s.aiCredentialModeAllowed(mode) {
		s.provider.mu.Unlock()
		return AIConnectionView{}, ErrConflict
	}
	col, err := s.readAIVaultCollection()
	if errors.Is(err, os.ErrNotExist) {
		col = aiVaultCollection{Version: 3}
		err = nil
	}
	var view AIConnectionView
	if err == nil && len(col.Accounts) >= aiMaxAccounts {
		err = errors.New("connection limit reached")
	}
	if err == nil {
		view, err = s.persistAIConnectionLocked(AIConnectionCommand{Action: "add", Provider: "openrouter", Name: a.name, Key: key, Passphrase: c.Passphrase}, col, "openrouter", mode, false)
	}
	s.cancelProviderSignInLocked()
	s.provider.mu.Unlock()
	if err != nil {
		return AIConnectionView{}, err
	}
	// Release the SQLite write lock before refresh, just like ordinary key setup.
	tx.Rollback()
	return s.refreshProviderModels(ctx, view.Ref, s.providerModelTransport(view.Ref))
}
func exchangeRouterSignIn(ctx context.Context, wire http.RoundTripper, code, verifier string) (string, error) {
	if wire == nil {
		return "", errors.New("sign-in unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"code": code, "code_verifier": verifier, "code_challenge_method": "S256"})
	defer clear(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/auth/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := http.Client{Transport: wire, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("sign-in redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		return "", errors.New("sign-in exchange unavailable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4097))
	defer clear(raw)
	media, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || res.StatusCode != 200 || len(raw) > 4096 || media != "application/json" || res.Header.Get("Content-Encoding") != "" || !uniqueAIJSON(raw) {
		return "", errors.New("sign-in exchange refused")
	}
	var data struct {
		Key string `json:"key"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return "", errors.New("sign-in key invalid")
	}
	return data.Key, nil
}

type routerSignInFixture struct{ verifier string }

func (f routerSignInFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != "POST" || r.URL.String() != "https://openrouter.ai/api/v1/auth/keys" || r.Header.Get("Authorization") != "" {
		return nil, errors.New("fixture exchange route")
	}
	var body map[string]string
	if json.NewDecoder(io.LimitReader(r.Body, 4097)).Decode(&body) != nil || len(body) != 3 || body["code"] != "fixture-router-code" || body["code_verifier"] != f.verifier || body["code_challenge_method"] != "S256" {
		return nil, errors.New("fixture PKCE mismatch")
	}
	return fixtureAdmissionResponse(r, map[string]string{"key": "fased-test-router-oauth-disposable-key"}), nil
}
