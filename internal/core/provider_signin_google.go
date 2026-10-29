package core

// Google API OAuth is separate from the prohibited third-party Gemini CLI /
// Code Assist login. It uses a registered Desktop application and the owner's
// explicit quota project, retaining ordinary API billing and finite grants.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const googleIssuer = "https://accounts.google.com"
const googleScope = "openid https://www.googleapis.com/auth/cloud-platform"
const googleTokenURL = "https://oauth2.googleapis.com/token"
const googleCertsURL = "https://www.googleapis.com/oauth2/v3/certs"
const googleRevokeURL = "https://oauth2.googleapis.com/revoke"

type googleOAuthCredential struct {
	Version      int    `json:"version"`
	Issuer       string `json:"issuer"`
	Subject      string `json:"subject"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Project      string `json:"project"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	Scope        string `json:"scope"`
	ExpiresAt    int64  `json:"expiresAt"`
	SignedOut    bool   `json:"signedOut,omitempty"`
}

func validGoogleProject(p string) bool {
	if len(p) < 6 || len(p) > 30 || p[0] < 'a' || p[0] > 'z' || p[len(p)-1] == '-' {
		return false
	}
	for _, b := range p {
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
			return false
		}
	}
	return true
}
func parseGoogleCredential(raw []byte) (googleOAuthCredential, bool) {
	var c googleOAuthCredential
	if len(raw) > providerSignInMaxPlaintext || !uniqueAIJSON(raw, providerSignInMaxPlaintext) || json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.Issuer != googleIssuer || !validSignInValue(c.Subject, 512) || !validSignInValue(c.ClientID, 256) || !strings.HasSuffix(c.ClientID, ".apps.googleusercontent.com") || !validSignInValue(c.ClientSecret, 256) || !validGoogleProject(c.Project) {
		return c, false
	}
	if c.SignedOut {
		return c, c.AccessToken == "" && c.RefreshToken == "" && c.Scope == "" && c.ExpiresAt == 0
	}
	return c, validSignInValue(c.AccessToken, 4096) && validSignInValue(c.RefreshToken, 4096) && len(c.Scope) <= 2048 && strings.Contains(" "+c.Scope+" ", " https://www.googleapis.com/auth/cloud-platform ") && !strings.ContainsAny(c.Scope, "\r\n\t") && c.ExpiresAt > 0
}
func (s *Store) googleClient() googleOAuthCredential {
	if s.providerFixture {
		return googleOAuthCredential{Version: 1, Issuer: googleIssuer, ClientID: "123-fased-fixture.apps.googleusercontent.com", ClientSecret: "fased-fixture-google-client-secret"}
	}
	c := googleOAuthCredential{Version: 1, Issuer: googleIssuer, ClientID: os.Getenv("FASED_GOOGLE_OAUTH_CLIENT_ID"), ClientSecret: os.Getenv("FASED_GOOGLE_OAUTH_CLIENT_SECRET")}
	if !validSignInValue(c.ClientID, 256) || !strings.HasSuffix(c.ClientID, ".apps.googleusercontent.com") || !validSignInValue(c.ClientSecret, 256) {
		return googleOAuthCredential{}
	}
	return c
}
func googleOAuthHTTP(ctx context.Context, wire http.RoundTripper, method, endpoint string, form url.Values, target any) error {
	if wire == nil || !(method == "GET" && endpoint == googleCertsURL || method == "POST" && (endpoint == googleTokenURL || endpoint == googleRevokeURL)) {
		return errors.New("Google OAuth endpoint refused")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(form.Encode()))
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := http.Client{Transport: wire, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Google OAuth redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("Google OAuth unavailable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	defer clear(raw)
	if err != nil || len(raw) > 65536 || res.StatusCode != 200 || res.Header.Get("Content-Encoding") != "" {
		return errors.New("Google OAuth refused; reconnect or retry the credential operation")
	}
	if target == nil {
		return nil
	}
	kind, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" || !uniqueAIJSON(raw, 32768) || json.Unmarshal(raw, target) != nil {
		return errors.New("Google OAuth response refused")
	}
	return nil
}
func (s *Store) googleOAuthWire(c googleOAuthCredential) http.RoundTripper {
	if s.provider.signInTransport != nil {
		return s.provider.signInTransport
	}
	if s.providerFixture {
		f, _ := newGoogleOAuthFixture(c, "", "", s.now)
		return f
	}
	if providerActivationEnabled && s.provider.apiCredentials {
		return newNativeProviderTransport("google").wire
	}
	return nil
}
func (s *Store) startGoogleSignIn(c AIConnectionCommand) (AIConnectionView, error) {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if !s.providerSignInAvailableLocked() || s.provider.signIn != nil {
		return AIConnectionView{}, ErrConflict
	}
	client := s.googleClient()
	if client.ClientID == "" {
		return AIConnectionView{}, errors.New("Google API sign-in requires Fased's registered OAuth client; use an API key")
	}
	client.Project = c.GoogleProject
	a := &providerSignInAttempt{ref: c.Ref, name: strings.TrimSpace(c.Name), client: client.ClientID, fixture: s.providerFixture}
	if c.Ref != "" {
		env, err := s.readAIVault(c.Ref)
		state := s.credentialLocked(c.Ref)
		previous, ok := parseGoogleCredential(state.key)
		if err != nil || env.Version != 5 || env.Provider != "google" || env.Mode != "google-oauth" || !ok || state.expires <= s.now().Unix() || !s.aiVaultModeAllowed(env) {
			return AIConnectionView{}, errors.New("unlock the Google sign-in account before reconnecting")
		}
		if c.GoogleProject != "" && c.GoogleProject != previous.Project {
			return AIConnectionView{}, errors.New("add a separate Google project account")
		}
		configured := client
		client = previous
		if configured.ClientID == previous.ClientID {
			client.ClientSecret = configured.ClientSecret
		}
		a.client = client.ClientID
		encoded, _ := json.Marshal(env)
		a.digest = sha256.Sum256(encoded)
		a.epoch = state.epoch
		if a.name == "" {
			a.name = env.Name
		}
	}
	if !validGoogleProject(client.Project) {
		return AIConnectionView{}, errors.New("enter a valid Google Cloud project ID")
	}
	if a.name == "" {
		a.name = client.Project
	}
	a.google = &client
	var err error
	if a.state, err = randomToken(); err != nil {
		return AIConnectionView{}, err
	}
	if a.nonce, err = randomToken(); err != nil {
		return AIConnectionView{}, err
	}
	if a.verifier, err = randomToken(); err != nil {
		return AIConnectionView{}, err
	}
	id, err := randomToken()
	if err != nil {
		return AIConnectionView{}, err
	}
	a.view = ProviderSignInView{ID: id, Provider: "google", State: "waiting", ExpiresAt: s.now().Add(5 * time.Minute).Unix(), Remote: c.Remote}
	listener, err := s.listenProviderSignInLocked(a)
	if err != nil {
		return AIConnectionView{}, err
	}
	challenge := sha256.Sum256([]byte(a.verifier))
	query := url.Values{"client_id": {a.client}, "redirect_uri": {a.redirect}, "response_type": {"code"}, "scope": {googleScope}, "state": {a.state}, "nonce": {a.nonce}, "access_type": {"offline"}, "prompt": {"consent"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}
	a.view.URL = googleIssuer + "/o/oauth2/v2/auth?" + query.Encode()
	a.wire = s.provider.signInTransport
	if a.wire == nil && a.fixture {
		a.wire, err = newGoogleOAuthFixture(client, a.nonce, a.verifier, s.now)
		a.view.URL = a.redirect + "?" + url.Values{"code": {"fixture-google-code"}, "state": {a.state}}.Encode()
	} else if a.wire == nil {
		a.wire = s.googleOAuthWire(client)
	}
	if err != nil || a.wire == nil {
		listener.Close()
		return AIConnectionView{}, errors.New("Google sign-in unavailable")
	}
	s.provider.signIn = a
	a.timer = time.AfterFunc(5*time.Minute, func() {
		s.provider.mu.Lock()
		defer s.provider.mu.Unlock()
		if s.provider.signIn == a {
			s.cancelProviderSignInLocked()
		}
	})
	go a.server.Serve(listener)
	return s.aiConnectionLocked(c.Ref)
}
func googleSignInCallback(a *providerSignInAttempt, callback string) (string, error) {
	u, err := url.Parse(callback)
	base, _ := url.Parse(a.redirect)
	if err != nil || len(callback) > 4096 || u.Scheme != base.Scheme || u.Host != base.Host || u.Path != base.Path || u.RawPath != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.ForceQuery {
		return "", errors.New("Google callback refused")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["code"]) != 1 || len(q["state"]) != 1 || q.Get("state") != a.state || !validSignInValue(q.Get("code"), 2048) {
		return "", errors.New("Google callback state/code refused")
	}
	for key, values := range q {
		if len(values) != 1 || (key != "code" && key != "state" && key != "scope" && key != "authuser" && key != "prompt") {
			return "", errors.New("Google callback fields refused")
		}
	}
	return q.Get("code"), nil
}
func exchangeGoogle(ctx context.Context, wire http.RoundTripper, previous googleOAuthCredential, code, verifier, redirect, nonce string, now time.Time) (googleOAuthCredential, error) {
	form := url.Values{"client_id": {previous.ClientID}, "client_secret": {previous.ClientSecret}}
	if code != "" {
		form.Set("grant_type", "authorization_code")
		form.Set("code", code)
		form.Set("code_verifier", verifier)
		form.Set("redirect_uri", redirect)
	} else {
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", previous.RefreshToken)
	}
	var result struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		ID      string `json:"id_token"`
		Scope   string `json:"scope"`
		Kind    string `json:"token_type"`
		Expires int64  `json:"expires_in"`
	}
	if err := googleOAuthHTTP(ctx, wire, "POST", googleTokenURL, form, &result); err != nil {
		return previous, err
	}
	if result.Kind != "Bearer" || !validSignInValue(result.Access, 4096) || result.Expires < 60 || result.Expires > 86400 {
		return previous, errors.New("Google token response refused")
	}
	c := previous
	if code != "" || result.ID != "" {
		sub, err := validateOIDCIDToken(ctx, wire, googleCertsURL, result.ID, previous.ClientID, nonce, now.Unix(), googleIssuer, googleOAuthHTTP)
		if err != nil || previous.Subject != "" && sub != previous.Subject {
			return previous, errors.New("Google sign-in identity changed or invalid")
		}
		c.Subject = sub
	}
	if result.Refresh != "" {
		c.RefreshToken = result.Refresh
	}
	if result.Scope != "" {
		c.Scope = result.Scope
	}
	c.AccessToken, c.ExpiresAt, c.SignedOut = result.Access, now.Add(time.Duration(result.Expires)*time.Second).Unix(), false
	raw, _ := json.Marshal(c)
	defer clear(raw)
	if _, ok := parseGoogleCredential(raw); !ok {
		return previous, errors.New("Google credential scope or refresh token missing")
	}
	return c, nil
}
func (s *Store) saveGoogleCredentialLocked(c googleOAuthCredential, passphrase, ref, name string, fixture bool, expected [32]byte) (AIConnectionView, error) {
	raw, _ := json.Marshal(c)
	defer clear(raw)
	if _, ok := parseGoogleCredential(raw); !ok {
		return AIConnectionView{}, errors.New("Google credential refused")
	}
	return s.persistSignInCredentialLocked(raw, passphrase, ref, name, "google", "google-oauth", fixture, expected)
}
func (s *Store) completeGoogleSignIn(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	s.provider.mu.Lock()
	a := s.provider.signIn
	if a == nil || a.google == nil || a.view.Provider != "google" || a.view.ID != c.SignInID || a.busy || a.view.ExpiresAt <= s.now().Unix() {
		s.provider.mu.Unlock()
		return AIConnectionView{}, ErrConflict
	}
	callback := c.Callback
	if callback == "" {
		callback = a.callback
	}
	code, err := googleSignInCallback(a, callback)
	if err != nil {
		s.cancelProviderSignInLocked()
		s.provider.mu.Unlock()
		return AIConnectionView{}, err
	}
	a.busy = true
	previous, verifier, redirect, nonce, wire := *a.google, a.verifier, a.redirect, a.nonce, a.wire
	s.provider.mu.Unlock()
	credential, err := exchangeGoogle(ctx, wire, previous, code, verifier, redirect, nonce, s.now())
	if err != nil {
		s.provider.mu.Lock()
		if s.provider.signIn == a {
			s.cancelProviderSignInLocked()
		}
		s.provider.mu.Unlock()
		return AIConnectionView{}, err
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
	if s.provider.signIn != a || !s.providerSignInAvailableLocked() || a.view.ExpiresAt <= s.now().Unix() || a.ref != "" && s.credentialLocked(a.ref).epoch != a.epoch {
		s.provider.mu.Unlock()
		return AIConnectionView{}, ErrConflict
	}
	v, err := s.saveGoogleCredentialLocked(credential, c.Passphrase, a.ref, a.name, a.fixture, a.digest)
	s.cancelProviderSignInLocked()
	s.provider.mu.Unlock()
	if err != nil {
		return v, err
	}
	return s.refreshProviderModels(ctx, v.Ref, s.providerModelTransport(v.Ref))
}
func (s *Store) renewGoogleSignIn(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	s.provider.mu.Lock()
	if s.provider.signInRenewals == nil {
		s.provider.signInRenewals = map[string]bool{}
	}
	if s.provider.signInRenewals[c.Ref] {
		s.provider.mu.Unlock()
		return AIConnectionView{}, ErrConflict
	}
	env, err := s.readAIVault(c.Ref)
	if err != nil || env.Version != 5 || env.Provider != "google" || !s.aiVaultModeAllowed(env) {
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("Google sign-in account unavailable")
	}
	plain, err := s.openAIVault(env, c.Passphrase)
	credential, ok := parseGoogleCredential(plain)
	clear(plain)
	if err != nil || !ok || credential.SignedOut {
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("unlock or reconnect this Google account")
	}
	s.provider.signInRenewals[c.Ref] = true
	defer func() { s.provider.mu.Lock(); delete(s.provider.signInRenewals, c.Ref); s.provider.mu.Unlock() }()
	raw, _ := json.Marshal(env)
	expected := sha256.Sum256(raw)
	wire := s.googleOAuthWire(credential)
	s.clearAIConnectionLocked(c.Ref)
	epoch := s.credentialLocked(c.Ref).epoch
	s.provider.mu.Unlock()
	if c.Action == "sign-in-disconnect" {
		err = googleOAuthHTTP(ctx, wire, "POST", googleRevokeURL, url.Values{"token": {credential.RefreshToken}}, nil)
		credential.AccessToken, credential.RefreshToken, credential.Scope, credential.ExpiresAt, credential.SignedOut = "", "", "", 0, true
	} else {
		credential, err = exchangeGoogle(ctx, wire, credential, "", "", "", "", s.now())
	}
	if err != nil {
		return AIConnectionView{}, err
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
	if s.credentialLocked(c.Ref).epoch != epoch {
		s.provider.mu.Unlock()
		return AIConnectionView{}, ErrConflict
	}
	v, err := s.saveGoogleCredentialLocked(credential, c.Passphrase, c.Ref, env.Name, env.TestOnly, expected)
	if c.Action == "sign-in-disconnect" {
		s.clearAIConnectionLocked(c.Ref)
		v, _ = s.aiConnectionLocked(c.Ref)
	}
	s.provider.mu.Unlock()
	if err != nil || c.Action == "sign-in-disconnect" {
		return v, err
	}
	return s.refreshProviderModels(ctx, v.Ref, s.providerModelTransport(v.Ref))
}

// Access-token refresh confers no generation authority. It is allowed only while
// the account is unlocked or the exact current task grant permits this request.
// Refresh-token rotation needs the explicit passphrase-protected writer above;
// automatic refresh never rewrites the encrypted vault or extends a task grant.
func (a authenticatedAITransport) ensureGoogleAccess(request *http.Request) error {
	ctx := request.Context()
	s := a.store
	s.provider.mu.Lock()
	v, err := s.aiConnectionLocked(a.ref)
	if err != nil || v.AuthMethod != "oauth" {
		s.provider.mu.Unlock()
		return err
	}
	if !providerRequestAllowed("google", request) {
		s.provider.mu.Unlock()
		return errors.New("Google API route refused")
	}
	state := s.credentialLocked(a.ref)
	credential, ok := parseGoogleCredential(state.key)
	if !ok || credential.SignedOut {
		s.provider.mu.Unlock()
		return errors.New("Google credential unavailable")
	}
	if credential.ExpiresAt > s.now().Unix()+30 {
		s.provider.mu.Unlock()
		return nil
	}
	allowed := !v.Locked
	if a.request != nil && a.request.TaskID != "" {
		allowed = s.taskAccessMatches(*a.request, state.taskAccess, true, state)
	}
	if a.request != nil && (a.request.ConnectionRef != a.ref || policyProvider(a.request.Policy) != "google" || !currentModelRequest(*a.request)) {
		allowed = false
	}
	if !allowed || a.request == nil && !a.modelsOnly || s.provider.signInRenewals[a.ref] {
		s.provider.mu.Unlock()
		return errors.New("Google refresh requires current account permission")
	}
	if s.provider.signInRenewals == nil {
		s.provider.signInRenewals = map[string]bool{}
	}
	s.provider.signInRenewals[a.ref] = true
	digest, epoch, previous := state.digest, state.epoch, append([]byte(nil), state.key...)
	wire := s.googleOAuthWire(credential)
	s.provider.mu.Unlock()
	defer clear(previous)
	defer func() { s.provider.mu.Lock(); delete(s.provider.signInRenewals, a.ref); s.provider.mu.Unlock() }()
	renewed, err := exchangeGoogle(ctx, wire, credential, "", "", "", "", s.now())
	if err != nil {
		return err
	}
	if renewed.RefreshToken != credential.RefreshToken {
		return errors.New("Google refresh token rotated; use protected Refresh")
	}
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	state = s.credentialLocked(a.ref)
	if state.epoch != epoch || state.digest != digest || !bytes.Equal(state.key, previous) || ctx.Err() != nil {
		return ErrConflict
	}
	clear(state.key)
	state.key, _ = json.Marshal(renewed)
	return nil
}
