package core

// The public-client flow follows OpenAI's Sign in with ChatGPT specification.
// Authentication and model discovery never authorize generation. Ordinary live
// admission stays held; compiled development fixtures exercise the same lifecycle.
import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const signInIssuer = "https://auth.openai.com"
const signInResource = "https://api.openai.com/v1"
const signInScope = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
const providerSignInMaxPlaintext = 24000
const providerSignInMaxCiphertext = providerSignInMaxPlaintext + 16 // AES-GCM authentication tag

func safeEndpointPath(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host + u.Path
}

func sanitizeDiagValue(s string, max int) string {
	if len(s) > max {
		s = s[:max]
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 32 && r <= 126 {
			b.WriteRune(r)
		} else if r == '\n' || r == '\r' || r == '\t' {
			b.WriteByte(' ')
		}
	}
	out := strings.TrimSpace(b.String())
	for _, marker := range []string{"Bearer ", "sk-", "eyJ"} {
		if idx := strings.Index(out, marker); idx >= 0 {
			out = strings.TrimSpace(out[:idx]) + " [redacted]"
		}
	}
	return out
}

func extractOAuthError(raw []byte) (string, string) {
	var body struct {
		Error string `json:"error"`
		Desc  string `json:"error_description"`
	}
	if len(raw) == 0 || len(raw) > 65536 || json.Unmarshal(raw, &body) != nil {
		return "", ""
	}
	return sanitizeDiagValue(body.Error, 64), sanitizeDiagValue(body.Desc, 160)
}

func logProviderDiag(stage string, fields map[string]any) {
	payload := map[string]any{"stage": stage}
	for k, v := range fields {
		if s, ok := v.(string); ok {
			if s == "" {
				continue
			}
			v = sanitizeDiagValue(s, 180)
		}
		payload[k] = v
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	log.Printf("fased provider diag: %s", encoded)
}

type ProviderSignInView struct {
	ID        string `json:"id"`
	Ref       string `json:"ref,omitempty"`
	Provider  string `json:"provider"`
	State     string `json:"state"`
	URL       string `json:"url"`
	ExpiresAt int64  `json:"expiresAt"`
	Remote    bool   `json:"remote"`
}

type providerSignInCredential struct {
	SignedOut    bool   `json:"signedOut,omitempty"`
	Version      int    `json:"version"`
	Issuer       string `json:"issuer"`
	Subject      string `json:"subject"`
	ClientID     string `json:"clientId"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	IDToken      string `json:"idToken"`
	Scope        string `json:"scope"`
	ExpiresAt    int64  `json:"expiresAt"`
}

func (c providerSignInCredential) planUse() bool {
	scopes := " " + c.Scope + " "
	return strings.Contains(scopes, " chatgpt.tokens.use.direct ") && strings.Contains(scopes, " resource.invoke ")
}

func parseProviderSignInCredential(raw []byte) (providerSignInCredential, bool) {
	var c providerSignInCredential
	if len(raw) > providerSignInMaxPlaintext || !uniqueAIJSON(raw, providerSignInMaxPlaintext) || json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.Issuer != signInIssuer || !validSignInValue(c.Subject, 512) || !validSignInValue(c.ClientID, 256) || c.ClientID == "dynamic_agent_client" {
		return providerSignInCredential{}, false
	}
	if c.SignedOut {
		return c, c.AccessToken == "" && c.RefreshToken == "" && c.IDToken == "" && c.Scope == "" && c.ExpiresAt == 0
	}
	if !validSignInValue(c.AccessToken, 8192) || (c.RefreshToken != "" && !validSignInValue(c.RefreshToken, 8192)) || !validSignInValue(c.IDToken, 12288) || len(c.Scope) > 1024 || strings.ContainsAny(c.Scope, "\r\n\t") || c.ExpiresAt <= 0 {
		return providerSignInCredential{}, false
	}
	return c, true
}
func validSignInValue(value string, bound int) bool {
	if value == "" || len(value) > bound {
		return false
	}
	for _, b := range []byte(value) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
func aiVaultCiphertextBound(v aiVaultEnvelope) int {
	if v.Version == 7 {
		return 1024 + 16
	}
	if v.Version == 6 {
		return 2048 + 16
	}
	if v.Version == 5 {
		return providerSignInMaxCiphertext
	}
	return 512
}
func (s *Store) aiVaultModeAllowed(v aiVaultEnvelope) bool {
	if v.Version == 6 {
		return v.TestOnly && s.providerFixture || !v.TestOnly && s.provider.apiCredentials
	}
	if v.Version == 5 {
		return v.TestOnly && s.providerFixture || !v.TestOnly && s.provider.apiCredentials || s.provider.signInTransport != nil
	}
	return s.aiCredentialModeAllowed(aiVaultMode(v))
}

type providerSignInAttempt struct {
	view                                                          ProviderSignInView
	state, nonce, verifier, redirect, client, ref, name, callback string
	google                                                        *googleOAuthCredential
	expected                                                      *providerSignInCredential
	digest                                                        [32]byte
	epoch                                                         uint64
	server                                                        *http.Server
	timer                                                         *time.Timer
	wire                                                          http.RoundTripper
	busy                                                          bool
	fixture                                                       bool
}

func (s *Store) providerSignInAvailableLocked() bool {
	return s.providerFixture || s.provider.signInTransport != nil || providerActivationEnabled && s.provider.apiCredentials
}
func (s *Store) cancelProviderSignInLocked() {
	if a := s.provider.signIn; a != nil {
		if a.timer != nil {
			a.timer.Stop()
		}
		if a.server != nil {
			a.server.Close()
		}
		a.verifier, a.nonce, a.callback, a.expected, a.google = "", "", "", nil, nil
		s.provider.signIn = nil
	}
}
func (s *Store) providerSignInViewLocked() *ProviderSignInView {
	a := s.provider.signIn
	if a == nil {
		return nil
	}
	if a.view.ExpiresAt <= s.now().Unix() {
		s.cancelProviderSignInLocked()
		return nil
	}
	v := a.view
	return &v
}

func (s *Store) changeProviderSignIn(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	if c.Action == "sign-in-start" && c.Provider == "anthropic" {
		return AIConnectionView{}, errors.New("Claude subscription sign-in is not supported in third-party apps; use an Anthropic API key")
	}
	if c.Action == "sign-in-start" && (c.Provider == "moonshot" || c.Provider == "moonshot-intl" || c.Provider == "kimi-code") {
		return AIConnectionView{}, errors.New("Kimi sign-in needs a supported Fased client contract; use a Platform API or Code membership key")
	}
	// The official current Qwen auth menu removed portal OAuth on 2026-04-15.
	// Legacy OpenClaw/Fased plugin code is not proof of a supported auth route.
	// Keep API credentials, any unrelated pending login and work records intact.
	// https://github.com/QwenLM/qwen-code/blob/main/docs/users/configuration/auth.md
	if c.Action == "sign-in-start" && c.Provider == "qwen" {
		return AIConnectionView{}, errors.New("Qwen portal sign-in was discontinued on 2026-04-15; use an API key.")
	}

	// The public Grok Build OAuth flow does not establish a supported Fased
	// client/inference entitlement. API keys and all existing login state remain
	// unchanged; never substitute a borrowed CLI registration or API tariff.
	if c.Action == "sign-in-start" && c.Provider == "xai" {
		return AIConnectionView{}, errors.New("xAI sign-in requires a supported Fased client and inference contract; use an API key.")
	}

	if c.GoogleProject != "" && (c.Action != "sign-in-start" || c.Provider != "google") {
		return AIConnectionView{}, errors.New("project selection requires Google sign-in")
	}
	if c.Key != "" || c.CredentialMode != "" || c.Pilot != nil || c.Allowance != nil || c.Revision != 0 || c.ProfileID != "" || len(c.TaskIDs) > 0 || c.AuthorizationSeconds != 0 || c.ConsentDigest != "" {
		return AIConnectionView{}, errors.New("sign-in accepts no API key, task grant or pilot")
	}
	var phase string
	if err := s.db.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); err != nil {
		return AIConnectionView{}, err
	}
	if phase == "paused" {
		return AIConnectionView{}, ErrCorePaused
	}
	s.provider.mu.Lock()
	if !s.providerSignInAvailableLocked() {
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("provider sign-in is held in this build")
	}
	s.provider.mu.Unlock()
	switch c.Action {
	case "sign-in-start":
		if c.SignInID != "" || c.Callback != "" || c.Passphrase != "" || (c.Provider != "openai" && c.Provider != "openrouter" && c.Provider != "google") || !validAgentText(c.Name, 80, false) {
			return AIConnectionView{}, errors.New("select a supported sign-in provider")
		}
		return s.startProviderSignIn(c)
	case "sign-in-cancel":
		if c.Ref != "" || c.Provider != "" || c.Name != "" || c.Passphrase != "" || c.Callback != "" || c.Remote {
			return AIConnectionView{}, errors.New("cancel accepts the pending sign-in only")
		}
		s.provider.mu.Lock()
		if a := s.provider.signIn; a == nil || a.view.ID != c.SignInID {
			s.provider.mu.Unlock()
			return AIConnectionView{}, ErrConflict
		}
		s.cancelProviderSignInLocked()
		s.provider.mu.Unlock()
		return s.AIConnection()
	case "sign-in-complete":
		if c.Ref != "" || c.Provider != "" || c.Name != "" || c.Remote || !s.validPassphraseOrOS(ctx, c.Passphrase) {
			return AIConnectionView{}, errors.New("finish with the pending sign-in and vault passphrase")
		}
		return s.completeProviderSignIn(ctx, c)
	case "sign-in-refresh", "sign-in-disconnect":
		if c.SignInID != "" || c.Callback != "" || c.Provider != "" || c.Name != "" || c.Remote || !validConnectionRef(c.Ref) || !s.validPassphraseOrOS(ctx, c.Passphrase) {
			return AIConnectionView{}, errors.New("select the sign-in account and enter its vault passphrase")
		}
		return s.renewProviderSignIn(ctx, c)
	default:
		return AIConnectionView{}, errors.New("unknown sign-in operation")
	}
}

func (s *Store) startProviderSignIn(c AIConnectionCommand) (AIConnectionView, error) {
	if c.Provider == "google" {
		return s.startGoogleSignIn(c)
	}
	if c.Provider == "openrouter" {
		return s.startRouterSignIn(c)
	}
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if !s.providerSignInAvailableLocked() {
		return AIConnectionView{}, ErrConflict
	}
	if s.provider.signIn != nil {
		return AIConnectionView{}, errors.New("finish or cancel the pending sign-in first")
	}
	a := &providerSignInAttempt{ref: c.Ref, name: strings.TrimSpace(c.Name), client: "dynamic_agent_client", fixture: s.providerFixture}
	if c.Ref != "" {
		env, err := s.readAIVault(c.Ref)
		if err != nil || env.Version != 5 || !s.aiVaultModeAllowed(env) {
			return AIConnectionView{}, errors.New("select an existing sign-in account")
		}
		v, err := s.aiConnectionLocked(c.Ref)
		credential, ok := parseProviderSignInCredential(s.credentialLocked(c.Ref).key)
		if err != nil || v.Locked || !ok {
			return AIConnectionView{}, errors.New("unlock this account before reconnecting")
		}
		a.expected, a.client = &credential, credential.ClientID
		encoded, _ := json.Marshal(env)
		a.digest = sha256.Sum256(encoded)
		a.epoch = s.credentialLocked(c.Ref).epoch
		if a.name == "" {
			a.name = env.Name
		}
	}
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
	a.view = ProviderSignInView{ID: id, Ref: c.Ref, Provider: "openai", State: "waiting", ExpiresAt: s.now().Add(10 * time.Minute).Unix(), Remote: c.Remote}
	listener, err := s.prepareSignInCallbackLocked(a)
	if err != nil {
		return AIConnectionView{}, err
	}
	hostHash := sha256.Sum256([]byte("fased-provider-host:" + s.instanceID))
	hostID := fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", hostHash[0:4], hostHash[4:6], hostHash[6:8], hostHash[8:10], hostHash[10:16])
	challenge := sha256.Sum256([]byte(a.verifier))
	q := url.Values{"client_id": {a.client}, "ext_agent_host_id": {hostID}, "response_type": {"code"}, "redirect_uri": {a.redirect}, "scope": {signInScope}, "resource": {signInResource}, "state": {a.state}, "nonce": {a.nonce}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}
	if a.expected == nil {
		q.Set("agent_name_hint", "Fased")
	}
	a.view.URL = signInIssuer + "/api/accounts/authorize?" + q.Encode()
	a.wire = s.provider.signInTransport
	if a.wire == nil && a.fixture {
		fixture, err := newProviderSignInFixture(a, s.now)
		if err != nil {
			if listener != nil {
				listener.Close()
			}
			return AIConnectionView{}, err
		}
		a.wire = fixture
		a.view.URL = a.redirect + "?" + url.Values{"code": {"fixture-authorization-code"}, "state": {a.state}, "client_id": {fixture.clientID}}.Encode()
	} else if a.wire == nil && providerActivationEnabled {
		a.wire = newOpenAITransport().wire
	}
	if a.wire == nil {
		if listener != nil {
			listener.Close()
		}
		return AIConnectionView{}, errors.New("sign-in transport unavailable")
	}
	s.provider.signIn = a
	a.timer = time.AfterFunc(10*time.Minute, func() {
		s.provider.mu.Lock()
		defer s.provider.mu.Unlock()
		if s.provider.signIn == a {
			s.cancelProviderSignInLocked()
		}
	})
	if listener != nil && a.server != nil {
		go a.server.Serve(listener)
	}
	return s.aiConnectionLocked(c.Ref)
}

func randomRemoteCallbackRedirect() (string, error) {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	port := 32768 + int(uint16(b[0])<<8|uint16(b[1]))%(61000-32768)
	return fmt.Sprintf("http://127.0.0.1:%d/auth/callback", port), nil
}

func (s *Store) prepareSignInCallbackLocked(a *providerSignInAttempt) (net.Listener, error) {
	if a.view.Remote {
		redirect, err := randomRemoteCallbackRedirect()
		if err != nil {
			return nil, err
		}
		a.redirect = redirect
		return nil, nil
	}
	return s.listenProviderSignInLocked(a)
}

// One callback implementation for each supported public-client flow.
func (s *Store) listenProviderSignInLocked(a *providerSignInAttempt) (net.Listener, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("private sign-in callback unavailable")
	}
	a.redirect = "http://" + listener.Addr().String() + "/auth/callback"
	a.server = &http.Server{ReadHeaderTimeout: 2 * time.Second, MaxHeaderBytes: 8192, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if r.Method != "GET" || r.URL.Path != "/auth/callback" || r.Host != listener.Addr().String() || len(r.URL.RawQuery) > 8192 {
			http.Error(w, "Invalid callback", 400)
			return
		}
		s.provider.mu.Lock()
		defer s.provider.mu.Unlock()
		if s.provider.signIn != a || a.busy || a.view.Remote || a.view.ExpiresAt <= s.now().Unix() || a.callback != "" {
			http.Error(w, "Return to Fased", 409)
			return
		}
		callback := a.redirect + "?" + r.URL.RawQuery
		if _, _, err := providerSignInCallback(a, callback); err != nil {
			http.Error(w, "Return to Fased and retry sign-in", 400)
			return
		}
		a.callback, a.view.State = callback, "callback-ready"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
		io.WriteString(w, "<!doctype html><meta charset=\"utf-8\"><title>Fased</title><body style=\"font-family:system-ui,sans-serif;background:#0d1014;color:#f3f5f8;display:grid;place-items:center;min-height:100vh;margin:0\"><p>Return to Fased — your account is connecting. You can close this tab.</p></body>")
	})}
	return listener, nil
}

func providerSignInCallback(a *providerSignInAttempt, callback string) (string, string, error) {
	code, client, _, err := providerSignInCallbackWithScope(a, callback)
	return code, client, err
}

func providerSignInCallbackWithScope(a *providerSignInAttempt, callback string) (string, string, string, error) {
	if a.view.Provider == "google" {
		code, err := googleSignInCallback(a, callback)
		return code, a.client, "", err
	}
	if a.view.Provider == "openrouter" {
		code, err := routerSignInCallback(a, callback)
		return code, "", "", err
	}
	callback = strings.TrimSpace(callback)
	u, err := url.Parse(callback)
	if u != nil {
		u.Fragment = ""
	}
	base, _ := url.Parse(a.redirect)
	if err != nil || len(callback) > 8192 || u.Scheme != base.Scheme || u.Host != base.Host || u.Path != base.Path || u.RawPath != "" || u.User != nil {
		logProviderDiag("callback", map[string]any{"provider": "openai", "remote": a.view.Remote, "reason": "url_parse"})
		return "", "", "", errors.New("invalid sign-in callback")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || q.Get("state") != a.state || len(q["state"]) != 1 {
		logProviderDiag("callback", map[string]any{"provider": "openai", "remote": a.view.Remote, "reason": "state_mismatch"})
		return "", "", "", errors.New("sign-in state mismatch")
	}
	for _, key := range []string{"code", "state", "client_id", "scope", "iss", "error", "error_description"} {
		if values, present := q[key]; present && len(values) != 1 {
			logProviderDiag("callback", map[string]any{"provider": "openai", "remote": a.view.Remote, "reason": "duplicate_param", "param": key})
			return "", "", "", errors.New("invalid sign-in callback")
		}
	}
	if iss := strings.TrimSuffix(strings.TrimSpace(q.Get("iss")), "/"); iss != "" && iss != signInIssuer {
		logProviderDiag("callback", map[string]any{"provider": "openai", "remote": a.view.Remote, "reason": "issuer_mismatch"})
		return "", "", "", errors.New("sign-in issuer mismatch")
	}
	if oauthErr := q.Get("error"); oauthErr != "" {
		logProviderDiag("callback", map[string]any{
			"provider": "openai", "remote": a.view.Remote, "reason": "provider_error",
			"oauthError": oauthErr, "oauthErrorDesc": q.Get("error_description"),
		})
		return "", "", "", errors.New("sign-in was not authorized")
	}
	client := q.Get("client_id")
	if a.client != "dynamic_agent_client" {
		if client != "" && client != a.client {
			logProviderDiag("callback", map[string]any{"provider": "openai", "remote": a.view.Remote, "reason": "client_changed"})
			return "", "", "", errors.New("sign-in registration changed")
		}
		client = a.client
	}
	if !validSignInValue(q.Get("code"), 2048) || !validSignInValue(client, 256) || client == "dynamic_agent_client" {
		logProviderDiag("callback", map[string]any{"provider": "openai", "remote": a.view.Remote, "reason": "missing_code_or_client"})
		return "", "", "", errors.New("sign-in code or issued client missing")
	}
	return q.Get("code"), client, strings.TrimSpace(q.Get("scope")), nil
}

func (s *Store) completeProviderSignIn(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	s.provider.mu.Lock()
	a := s.provider.signIn
	if a == nil || a.view.ID != c.SignInID || a.busy || a.view.ExpiresAt <= s.now().Unix() {
		s.provider.mu.Unlock()
		return AIConnectionView{}, ErrConflict
	}
	if a.view.Provider == "google" {
		s.provider.mu.Unlock()
		return s.completeGoogleSignIn(ctx, c)
	}
	if a.view.Provider == "openrouter" {
		s.provider.mu.Unlock()
		return s.completeRouterSignIn(ctx, c)
	}
	callback := strings.TrimSpace(c.Callback)
	if callback == "" {
		callback = a.callback
	}
	if callback == "" {
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("complete sign-in in your browser first")
	}
	code, client, cbScope, err := providerSignInCallbackWithScope(a, callback)
	if err != nil {
		s.provider.mu.Unlock()
		return AIConnectionView{}, err
	}
	fallbackScope := cbScope
	if fallbackScope == "" {
		fallbackScope = signInScope
	}
	a.busy = true
	settled := false
	nonce, verifier, redirect, wire, expected := a.nonce, a.verifier, a.redirect, a.wire, a.expected
	s.provider.mu.Unlock()
	defer func() {
		if !settled {
			s.provider.mu.Lock()
			if s.provider.signIn == a {
				a.busy = false
			}
			s.provider.mu.Unlock()
		}
	}()
	credential, err := exchangeProviderSignIn(ctx, wire, url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "resource": {signInResource}}, nonce, client, s.now(), fallbackScope, "", "", "")
	if err == nil && expected != nil && (credential.Issuer != expected.Issuer || credential.Subject != expected.Subject || credential.ClientID != expected.ClientID) {
		logProviderDiag("sign-in-complete", map[string]any{"provider": "openai", "reason": "account_identity_changed"})
		err = errors.New("sign-in account identity changed")
	}
	if err != nil {
		logProviderDiag("sign-in-complete", map[string]any{"provider": "openai", "reason": "token_exchange_failed", "err": err.Error()})
		s.provider.mu.Lock()
		if s.provider.signIn == a {
			s.cancelProviderSignInLocked()
		}
		settled = true
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
		if s.provider.signIn == a && (a.view.ExpiresAt <= s.now().Unix() || a.ref != "" && s.credentialLocked(a.ref).epoch != a.epoch) {
			s.cancelProviderSignInLocked()
			settled = true
		}
		s.provider.mu.Unlock()
		return AIConnectionView{}, ErrConflict
	}
	v, err := s.saveProviderSignInLocked(ctx, credential, c.Passphrase, a.ref, a.name, a.fixture, a.digest)
	if err != nil {
		logProviderDiag("sign-in-complete", map[string]any{"provider": "openai", "reason": "vault_save_failed", "err": err.Error()})
	}
	s.cancelProviderSignInLocked()
	settled = true
	s.provider.mu.Unlock()
	if err != nil {
		return v, err
	}
	tx.Rollback()
	if !credential.planUse() {
		return s.AIConnection(v.Ref)
	}
	return s.refreshProviderModels(ctx, v.Ref, s.providerModelTransport(v.Ref))
}

func (s *Store) saveProviderSignInLocked(ctx context.Context, c providerSignInCredential, passphrase, ref, name string, fixture bool, expected [32]byte) (AIConnectionView, error) {
	raw, _ := json.Marshal(c)
	defer clear(raw)
	if _, ok := parseProviderSignInCredential(raw); !ok {
		return AIConnectionView{}, errors.New("invalid sign-in credential")
	}
	return s.persistSignInCredentialLocked(ctx, raw, passphrase, ref, name, "openai", "openai-signin", fixture, expected)
}
func (s *Store) persistSignInCredentialLocked(ctx context.Context, raw []byte, passphrase, ref, name, provider, mode string, fixture bool, expected [32]byte) (AIConnectionView, error) {
	col, err := s.readAIVaultCollection()
	if errors.Is(err, os.ErrNotExist) {
		col = aiVaultCollection{Version: 3}
	} else if err != nil {
		return AIConnectionView{}, err
	}
	if ref == "" && len(col.Accounts) >= aiMaxAccounts {
		return AIConnectionView{}, errors.New("connection limit reached")
	}
	if ref != "" {
		env, err := s.readAIVault(ref)
		if err != nil || env.Version != 5 || env.Provider != provider || env.Mode != mode {
			return AIConnectionView{}, ErrConflict
		}
		encoded, _ := json.Marshal(env)
		if sha256.Sum256(encoded) != expected {
			return AIConnectionView{}, ErrConflict
		}
		plain, err := s.openAIVaultWithContext(ctx, env, passphrase)
		clear(plain)
		if err != nil {
			return AIConnectionView{}, err
		}
	}
	env := aiVaultEnvelope{Version: 5, Provider: provider, Mode: mode, TestOnly: fixture, Ref: ref, Name: name, Salt: make([]byte, 16), Nonce: make([]byte, 12)}
	if ref == "" {
		env.Ref, err = randomToken()
		if err != nil {
			return AIConnectionView{}, err
		}
	}
	if _, err = rand.Read(env.Salt); err != nil {
		return AIConnectionView{}, err
	}
	if _, err = rand.Read(env.Nonce); err != nil {
		return AIConnectionView{}, err
	}
	key, osProtected, err := s.vaultSealKey(ctx, passphrase, env.Salt, true)
	if err != nil {
		return AIConnectionView{}, err
	}
	env.OSProtected = osProtected
	block, _ := aes.NewCipher(key)
	clear(key)
	gcm, _ := cipher.NewGCM(block)
	env.Ciphertext = gcm.Seal(nil, env.Nonce, raw, s.aiVaultAAD(env))
	replaced := false
	for i, v := range col.Accounts {
		if v.Ref == ref {
			col.Accounts[i] = env
			replaced = true
		}
	}
	if !replaced {
		col.Accounts = append(col.Accounts, env)
	}
	if err = s.writeAIVaultCollection(col); err != nil {
		return AIConnectionView{}, errors.New("sign-in credential storage failed; preserve vault before retry")
	}
	s.clearAIConnectionLocked(env.Ref)
	state := s.credentialLocked(env.Ref)
	encoded, _ := json.Marshal(env)
	state.digest = sha256.Sum256(encoded)
	state.ref, state.key = env.Ref, append([]byte(nil), raw...)
	state.osProtected = env.OSProtected
	state.manuallyLocked = false
	if env.OSProtected {
		state.expires = 0
	} else {
		state.expires = s.now().Add(15 * time.Minute).Unix()
	}
	return s.aiConnectionLocked(env.Ref)
}

type signInDiscovery struct {
	Issuer string `json:"issuer"`
	JWKS   string `json:"jwks_uri"`
	Revoke string `json:"revocation_endpoint"`
}

func signInEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "auth.openai.com" && u.User == nil && u.Opaque == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawPath == "" && strings.HasPrefix(u.Path, "/")
}
func signInHTTP(ctx context.Context, wire http.RoundTripper, method, endpoint string, form url.Values, target any) error {
	if wire == nil || !signInEndpoint(endpoint) {
		return errors.New("untrusted sign-in endpoint")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("sign-in request unavailable")
	}
	req.Header.Set("User-Agent", "Fased/"+Version)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := http.Client{Transport: wire, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("sign-in redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		logProviderDiag("signin-http", map[string]any{"method": method, "endpoint": safeEndpointPath(endpoint), "reason": "transport", "err": err.Error()})
		return errors.New("sign-in provider unavailable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	defer clear(raw)
	if err != nil || len(raw) > 65536 || res.StatusCode != http.StatusOK || res.Header.Get("Content-Encoding") != "" {
		oauthErr, oauthDesc := extractOAuthError(raw)
		readErrStr := ""
		if err != nil {
			readErrStr = err.Error()
		}
		logProviderDiag("signin-http", map[string]any{
			"method": method, "endpoint": safeEndpointPath(endpoint),
			"status": res.StatusCode, "contentType": res.Header.Get("Content-Type"),
			"contentEncoding": res.Header.Get("Content-Encoding"), "bodyBytes": len(raw),
			"cfRay": res.Header.Get("CF-Ray"), "requestId": res.Header.Get("x-request-id"),
			"oauthError": oauthErr, "oauthErrorDesc": oauthDesc, "reason": "response_rejected", "err": readErrStr,
		})
		return errors.New("sign-in provider refused request")
	}
	if target == nil {
		if len(raw) != 0 {
			logProviderDiag("signin-http", map[string]any{"method": method, "endpoint": safeEndpointPath(endpoint), "bodyBytes": len(raw), "reason": "revoke_nonempty"})
			return errors.New("invalid revocation response")
		}
		return nil
	}
	kind, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" || !uniqueAIJSON(raw, 65536) || json.Unmarshal(raw, target) != nil {
		logProviderDiag("signin-http", map[string]any{
			"method": method, "endpoint": safeEndpointPath(endpoint),
			"status": res.StatusCode, "contentType": res.Header.Get("Content-Type"),
			"bodyBytes": len(raw), "reason": "invalid_json",
		})
		return errors.New("invalid sign-in provider response")
	}
	return nil
}
func readSignInDiscovery(ctx context.Context, wire http.RoundTripper) (signInDiscovery, error) {
	var d signInDiscovery
	err := signInHTTP(ctx, wire, "GET", signInIssuer+"/.well-known/openid-configuration", nil, &d)
	if err != nil || d.Issuer != signInIssuer || !signInEndpoint(d.JWKS) || !signInEndpoint(d.Revoke) {
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		logProviderDiag("oidc-discovery", map[string]any{"reason": "discovery_invalid", "err": errText})
		return signInDiscovery{}, errors.New("untrusted sign-in discovery")
	}
	return d, nil
}

func parseProviderSignInModels(raw []byte, fetchedAt int64) ([]discoveredModel, error) {
	var envelope struct {
		Object string `json:"object"`
		Models []struct {
			Slug       string `json:"slug"`
			Name       string `json:"display_name"`
			Visibility string `json:"visibility"`
		} `json:"models"`
		Data []discoveredModel `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		logProviderDiag("models-parse", map[string]any{"reason": "invalid_json", "bytes": len(raw)})
		return nil, errors.New("invalid ChatGPT model catalogue")
	}
	if envelope.Models != nil {
		if len(envelope.Models) > 2048 {
			logProviderDiag("models-parse", map[string]any{"reason": "too_many_models", "count": len(envelope.Models)})
			return nil, errors.New("invalid ChatGPT model catalogue")
		}
		rows := []discoveredModel{}
		for _, model := range envelope.Models {
			if len(model.Visibility) > 32 || (model.Visibility != "" && !validSignInValue(model.Visibility, 32)) {
				return nil, errors.New("invalid ChatGPT model entry")
			}
			if model.Visibility != "" && model.Visibility != "list" {
				continue
			}
			if !validProviderModelID(model.Slug) {
				return nil, errors.New("invalid ChatGPT model entry")
			}
			name := model.Name
			if name == "" || !validAgentText(name, 256, true) {
				name = model.Slug
			}
			// This route provides no creation timestamp. The retained value is the
			// verified catalogue observation time, never evidence of model age.
			rows = append(rows, discoveredModel{ID: model.Slug, Name: name, Object: "model", OwnedBy: "openai", Created: fetchedAt})
		}
		return rows, nil
	}
	if envelope.Data != nil {
		if (envelope.Object != "" && envelope.Object != "list") || len(envelope.Data) > 2048 {
			logProviderDiag("models-parse", map[string]any{"reason": "invalid_data_list", "object": envelope.Object, "count": len(envelope.Data)})
			return nil, errors.New("invalid ChatGPT model catalogue")
		}
		rows := make([]discoveredModel, 0, len(envelope.Data))
		for _, item := range envelope.Data {
			if !validProviderModelID(item.ID) {
				return nil, errors.New("invalid ChatGPT model entry")
			}
			if item.Object == "" {
				item.Object = "model"
			}
			if item.OwnedBy == "" {
				item.OwnedBy = "openai"
			}
			if item.Created <= 0 {
				item.Created = fetchedAt
			}
			if item.Name == "" || !validAgentText(item.Name, 256, true) {
				item.Name = item.ID
			}
			rows = append(rows, item)
		}
		return rows, nil
	}
	var obj map[string]json.RawMessage
	keys := []string{}
	if json.Unmarshal(raw, &obj) == nil {
		for k := range obj {
			keys = append(keys, sanitizeDiagValue(k, 40))
		}
	}
	logProviderDiag("models-parse", map[string]any{"reason": "unrecognized_shape", "keys": strings.Join(keys, ",")})
	return nil, errors.New("invalid ChatGPT model catalogue")
}

type signInTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Type         string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
}

func exchangeProviderSignIn(ctx context.Context, wire http.RoundTripper, form url.Values, nonce, client string, now time.Time, fallbackScope, fallbackRefresh, fallbackIDToken, fallbackSubject string) (providerSignInCredential, error) {
	var result signInTokenResponse
	if err := signInHTTP(ctx, wire, "POST", signInIssuer+"/api/accounts/oauth/token", form, &result); err != nil {
		return providerSignInCredential{}, err
	}
	if !strings.EqualFold(result.Type, "Bearer") || result.ExpiresIn < 1 || result.ExpiresIn > 31*86400 {
		logProviderDiag("token-exchange", map[string]any{"reason": "invalid_token_type_or_ttl", "expiresIn": result.ExpiresIn})
		return providerSignInCredential{}, errors.New("invalid sign-in token grant")
	}
	scope := strings.TrimSpace(result.Scope)
	if scope == "" {
		scope = strings.TrimSpace(fallbackScope)
	}
	refresh := result.RefreshToken
	if refresh == "" && fallbackRefresh != "" {
		refresh = fallbackRefresh
	}
	idToken := result.IDToken
	var subject string
	if nonce != "" || idToken != "" {
		d, err := readSignInDiscovery(ctx, wire)
		if err != nil {
			return providerSignInCredential{}, err
		}
		subject, err = validateSignInIDToken(ctx, wire, d.JWKS, idToken, client, nonce, now.Unix())
		if err != nil {
			return providerSignInCredential{}, err
		}
	} else {
		idToken = fallbackIDToken
		subject = fallbackSubject
	}
	c := providerSignInCredential{Version: 1, Issuer: signInIssuer, Subject: subject, ClientID: client, AccessToken: result.AccessToken, RefreshToken: refresh, IDToken: idToken, Scope: scope, ExpiresAt: now.Unix() + result.ExpiresIn}
	raw, _ := json.Marshal(c)
	defer clear(raw)
	if _, ok := parseProviderSignInCredential(raw); !ok {
		logProviderDiag("token-exchange", map[string]any{
			"reason": "credential_validation_failed", "accessLen": len(result.AccessToken),
			"refreshLen": len(refresh), "idLen": len(idToken), "scopeLen": len(scope),
		})
		return providerSignInCredential{}, errors.New("invalid sign-in credential grant")
	}
	return c, nil
}

func validateSignInIDToken(ctx context.Context, wire http.RoundTripper, jwks, token, client, nonce string, now int64) (string, error) {
	return validateOIDCIDToken(ctx, wire, jwks, token, client, nonce, now, signInIssuer, signInHTTP)
}
func validateOIDCIDToken(ctx context.Context, wire http.RoundTripper, jwks, token, client, nonce string, now int64, issuer string, fetch func(context.Context, http.RoundTripper, string, string, url.Values, any) error) (string, error) {
	refused := errors.New("sign-in identity verification failed")
	fail := func(stage string) (string, error) {
		logProviderDiag("oidc-id-token", map[string]any{"issuer": issuer, "reason": stage})
		return "", refused
	}
	if len(token) > 16384 {
		return fail("token_len")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fail("jwt_parts")
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || !uniqueAIJSON(header, 4096) {
		return fail("header_decode")
	}
	var h struct {
		Alg, Kid string
		Crit     []string
		JKU      string
		JWK      json.RawMessage
	}
	if json.Unmarshal(header, &h) != nil || h.Alg != "RS256" || !validSignInValue(h.Kid, 256) || len(h.Crit) != 0 || h.JKU != "" {
		return fail("header_alg_or_fields")
	}
	if jwk := bytes.TrimSpace(h.JWK); len(jwk) != 0 && string(jwk) != "null" {
		return fail("header_jwk_inline")
	}
	var keys struct {
		Keys []struct{ Kty, Kid, Alg, Use, N, E string }
	}
	if fetch(ctx, wire, "GET", jwks, nil, &keys) != nil || len(keys.Keys) > 32 {
		return fail("jwks_fetch")
	}
	var public *rsa.PublicKey
	for _, key := range keys.Keys {
		if key.Kid != h.Kid {
			continue
		}
		if public != nil || key.Kty != "RSA" || (key.Alg != "" && key.Alg != "RS256") || (key.Use != "" && key.Use != "sig") {
			return fail("jwks_key_invalid")
		}
		n, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil || len(n) < 256 || len(n) > 512 {
			return fail("jwks_modulus_invalid")
		}
		e, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil || len(e) > 4 || len(e) == 0 {
			return fail("jwks_exponent_invalid")
		}
		exponent := 0
		for _, b := range e {
			exponent = exponent*256 + int(b)
		}
		if exponent < 3 || exponent%2 == 0 {
			return fail("jwks_exponent_value")
		}
		public = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}
	}
	if public == nil {
		return fail("kid_not_found")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fail("signature_decode")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(public, crypto.SHA256, digest[:], signature) != nil {
		return fail("signature_invalid")
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !uniqueAIJSON(claims, 32768) {
		return fail("claims_decode")
	}
	var c struct {
		Iss, Sub, Nonce, Azp string
		Aud                  json.RawMessage
		Exp, Iat             int64
	}
	if json.Unmarshal(claims, &c) != nil || (c.Iss != issuer && !(issuer == googleIssuer && c.Iss == "accounts.google.com")) || !validSignInValue(c.Sub, 512) || c.Exp <= now || c.Iat > now+300 || c.Iat < now-86400 || nonce != "" && c.Nonce != nonce {
		return fail("claims_iss_sub_time_nonce")
	}
	if c.Azp != "" && c.Azp != client {
		return fail("claims_azp")
	}
	var audience string
	if json.Unmarshal(c.Aud, &audience) == nil {
		if audience != client {
			return fail("claims_aud")
		}
		return c.Sub, nil
	}
	var audiences []string
	if json.Unmarshal(c.Aud, &audiences) != nil || len(audiences) == 0 || len(audiences) > 16 {
		return fail("claims_aud_array")
	}
	matched := false
	for _, entry := range audiences {
		if entry == client {
			matched = true
			break
		}
	}
	if !matched {
		return fail("claims_aud")
	}
	return c.Sub, nil
}

func (s *Store) renewProviderSignIn(ctx context.Context, c AIConnectionCommand) (AIConnectionView, error) {
	s.provider.mu.Lock()
	envelope, googleErr := s.readAIVault(c.Ref)
	s.provider.mu.Unlock()
	if googleErr == nil && envelope.Version == 5 && envelope.Provider == "google" {
		return s.renewGoogleSignIn(ctx, c)
	}
	s.provider.mu.Lock()
	if s.provider.signInRenewals == nil {
		s.provider.signInRenewals = map[string]bool{}
	}
	if s.provider.signInRenewals[c.Ref] {
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("sign-in credential operation already in progress")
	}
	env, err := s.readAIVault(c.Ref)
	if err != nil || env.Version != 5 || !s.aiVaultModeAllowed(env) {
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("sign-in account unavailable")
	}
	plain, err := s.openAIVaultWithContext(ctx, env, c.Passphrase)
	credential, ok := parseProviderSignInCredential(plain)
	clear(plain)
	if err != nil || !ok {
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("vault unlock failed")
	}
	if credential.SignedOut {
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("reconnect this signed-out account")
	}
	if err = s.removeDurableTaskAccess(c.Ref); err != nil {
		s.clearAIConnectionLocked(c.Ref)
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("background revocation could not be saved; account remains locked")
	}
	s.provider.signInRenewals[c.Ref] = true
	defer func() { s.provider.mu.Lock(); delete(s.provider.signInRenewals, c.Ref); s.provider.mu.Unlock() }()
	encoded, _ := json.Marshal(env)
	expected := sha256.Sum256(encoded)
	wire := s.provider.signInTransport
	if wire == nil && env.TestOnly && s.providerFixture {
		wire, err = newProviderSignInRenewFixture(credential, s.now)
	} else if wire == nil && providerActivationEnabled {
		wire = newOpenAITransport().wire
	}
	s.clearAIConnectionLocked(c.Ref) // revoke Chat/task access before refresh or remote revocation
	epoch := s.credentialLocked(c.Ref).epoch
	s.provider.mu.Unlock()
	if err != nil || wire == nil {
		return AIConnectionView{}, errors.New("sign-in transport unavailable")
	}
	if c.Action == "sign-in-disconnect" {
		if credential.RefreshToken != "" {
			d, err := readSignInDiscovery(ctx, wire)
			if err != nil {
				return AIConnectionView{}, err
			}
			if err = signInHTTP(ctx, wire, "POST", d.Revoke, url.Values{"token": {credential.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {credential.ClientID}}, nil); err != nil {
				return AIConnectionView{}, errors.New("account locked; remote revocation not confirmed, retry disconnect or revoke in ChatGPT settings")
			}
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
		defer s.provider.mu.Unlock()
		current, err := s.readAIVault(c.Ref)
		if err != nil {
			return AIConnectionView{}, ErrConflict
		}
		raw, _ := json.Marshal(current)
		if sha256.Sum256(raw) != expected {
			return AIConnectionView{}, ErrConflict
		}
		credential.AccessToken, credential.RefreshToken, credential.IDToken, credential.Scope = "", "", "", ""
		credential.SignedOut, credential.ExpiresAt = true, 0
		if _, err = s.saveProviderSignInLocked(ctx, credential, c.Passphrase, c.Ref, env.Name, env.TestOnly, expected); err != nil {
			return AIConnectionView{}, err
		}
		s.clearAIConnectionLocked(c.Ref)
		return s.aiConnectionLocked(c.Ref)
	}
	if credential.RefreshToken == "" {
		return AIConnectionView{}, errors.New("sign-in account has no refresh token; reconnect to renew")
	}
	renewed, err := exchangeProviderSignIn(ctx, wire, url.Values{"grant_type": {"refresh_token"}, "client_id": {credential.ClientID}, "refresh_token": {credential.RefreshToken}, "resource": {signInResource}}, "", credential.ClientID, s.now(), credential.Scope, credential.RefreshToken, credential.IDToken, credential.Subject)
	if err != nil {
		return AIConnectionView{}, err
	}
	if renewed.Subject != credential.Subject || renewed.Issuer != credential.Issuer || renewed.ClientID != credential.ClientID {
		return AIConnectionView{}, errors.New("renewed sign-in identity changed")
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
	if !s.providerSignInAvailableLocked() || s.credentialLocked(c.Ref).epoch != epoch {
		s.provider.mu.Unlock()
		return AIConnectionView{}, ErrConflict
	}
	v, err := s.saveProviderSignInLocked(ctx, renewed, c.Passphrase, c.Ref, env.Name, env.TestOnly, expected)
	s.provider.mu.Unlock()
	if err != nil {
		return v, err
	}
	if !renewed.planUse() {
		return s.AIConnection(v.Ref)
	}
	return s.refreshProviderModels(ctx, v.Ref, s.providerModelTransport(v.Ref))
}

func (s *Store) openAISignInWire() http.RoundTripper {
	if s.provider.signInTransport != nil {
		return s.provider.signInTransport
	}
	if providerActivationEnabled && s.provider.apiCredentials {
		return newOpenAITransport().wire
	}
	return nil
}

func (s *Store) persistOSProtectedSignInLocked(ctx context.Context, ref string, raw []byte, expected [32]byte) ([32]byte, error) {
	return s.persistOSProtectedOAuthLocked(ctx, ref, "openai", "openai-signin", raw, expected)
}

func (s *Store) persistOSProtectedOAuthLocked(ctx context.Context, ref, provider, mode string, raw []byte, expected [32]byte) ([32]byte, error) {
	col, err := s.readAIVaultCollection()
	if err != nil {
		return [32]byte{}, err
	}
	for i, env := range col.Accounts {
		if env.Ref != ref || env.Version != 5 || env.Provider != provider || env.Mode != mode || !env.OSProtected {
			continue
		}
		encoded, _ := json.Marshal(env)
		if sha256.Sum256(encoded) != expected {
			return [32]byte{}, ErrConflict
		}
		salt := make([]byte, 16)
		nonce := make([]byte, 12)
		if _, err = rand.Read(salt); err != nil {
			return [32]byte{}, err
		}
		if _, err = rand.Read(nonce); err != nil {
			return [32]byte{}, err
		}
		updated := env
		updated.Salt = salt
		updated.Nonce = nonce
		key, osProtected, err := s.vaultSealKey(ctx, "", updated.Salt, false)
		if err != nil || !osProtected {
			clear(key)
			if err == nil {
				err = errors.New("system credential protection unavailable")
			}
			return [32]byte{}, err
		}
		block, _ := aes.NewCipher(key)
		clear(key)
		gcm, _ := cipher.NewGCM(block)
		updated.Ciphertext = gcm.Seal(nil, updated.Nonce, raw, s.aiVaultAAD(updated))
		col.Accounts[i] = updated
		if err = s.writeAIVaultCollection(col); err != nil {
			return [32]byte{}, err
		}
		nextEncoded, _ := json.Marshal(updated)
		return sha256.Sum256(nextEncoded), nil
	}
	return [32]byte{}, ErrConflict
}

// Automatically refresh expiring OpenAI sign-in access tokens without breaking
// the active account epoch or granting new task authority. When OS vault custody
// protects the entry, updated single-use refresh tokens are persisted on disk.
func (a authenticatedAITransport) ensureSignInAccess(request *http.Request) error {
	ctx := request.Context()
	s := a.store
	s.provider.mu.Lock()
	v, err := s.aiConnectionLocked(a.ref)
	if err != nil || v.AuthMethod != "sign-in" {
		s.provider.mu.Unlock()
		return err
	}
	state := s.credentialLocked(a.ref)
	credential, ok := parseProviderSignInCredential(state.key)
	if !ok || credential.SignedOut {
		s.provider.mu.Unlock()
		return errors.New("sign-in credential unavailable")
	}
	if credential.ExpiresAt > s.now().Unix()+60 {
		s.provider.mu.Unlock()
		return nil
	}
	routeAllowed := (request.Method == "GET" && request.URL.Path == "/v1/models") || (request.Method == "POST" && request.URL.Path == "/v1/responses")
	if !routeAllowed || !providerRequestAllowed("openai", request) {
		s.provider.mu.Unlock()
		return errors.New("sign-in route refused")
	}
	allowed := !v.Locked
	if a.request != nil && a.request.TaskID != "" {
		allowed = s.taskAccessMatches(*a.request, state.taskAccess, true, state)
	}
	if a.request != nil && (a.request.ConnectionRef != a.ref || policyProvider(a.request.Policy) != "openai" || !signInRequest(*a.request)) {
		allowed = false
	}
	if !allowed || (a.request == nil && !a.modelsOnly && !a.agentV2) {
		s.provider.mu.Unlock()
		return errors.New("sign-in refresh requires current account permission")
	}
	if credential.RefreshToken == "" {
		s.provider.mu.Unlock()
		return errors.New("sign-in expired; reconnect to renew")
	}
	if s.provider.signInRenewals[a.ref] {
		s.provider.mu.Unlock()
		return errors.New("sign-in credential operation already in progress")
	}
	wire := s.openAISignInWire()
	if wire == nil {
		s.provider.mu.Unlock()
		return nil
	}
	if s.provider.signInRenewals == nil {
		s.provider.signInRenewals = map[string]bool{}
	}
	s.provider.signInRenewals[a.ref] = true
	digest, epoch, osProt, previous := state.digest, state.epoch, state.osProtected, append([]byte(nil), state.key...)
	s.provider.mu.Unlock()
	defer clear(previous)
	defer func() {
		s.provider.mu.Lock()
		delete(s.provider.signInRenewals, a.ref)
		s.provider.mu.Unlock()
	}()
	renewed, err := exchangeProviderSignIn(ctx, wire, url.Values{"grant_type": {"refresh_token"}, "client_id": {credential.ClientID}, "refresh_token": {credential.RefreshToken}, "resource": {signInResource}}, "", credential.ClientID, s.now(), credential.Scope, credential.RefreshToken, credential.IDToken, credential.Subject)
	if err != nil {
		logProviderDiag("auto-refresh", map[string]any{"ref": a.ref, "err": err.Error()})
		return err
	}
	if renewed.Subject != credential.Subject || renewed.Issuer != credential.Issuer || renewed.ClientID != credential.ClientID {
		logProviderDiag("auto-refresh", map[string]any{"ref": a.ref, "reason": "identity_changed"})
		return errors.New("renewed sign-in identity changed")
	}
	raw, _ := json.Marshal(renewed)
	defer clear(raw)
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	state = s.credentialLocked(a.ref)
	if state.epoch != epoch || state.digest != digest || !bytes.Equal(state.key, previous) || ctx.Err() != nil {
		return ErrConflict
	}
	if osProt {
		nextDigest, err := s.persistOSProtectedSignInLocked(ctx, a.ref, raw, digest)
		if err != nil {
			logProviderDiag("auto-refresh-os-vault", map[string]any{"ref": a.ref, "err": err.Error()})
			return err
		}
		state.digest = nextDigest
	}
	clear(state.key)
	state.key = append([]byte(nil), raw...)
	return nil
}
