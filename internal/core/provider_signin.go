package core

// The public-client flow follows OpenAI's Sign in with ChatGPT specification.
// Authentication and model discovery never authorize generation. Ordinary live
// admission stays held; compiled development fixtures exercise the same lifecycle.
import (
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
const providerSignInMaxPlaintext = 16000
const providerSignInMaxCiphertext = providerSignInMaxPlaintext + 16 // AES-GCM authentication tag

type ProviderSignInView struct {
	ID        string `json:"id"`
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
	if !validSignInValue(c.AccessToken, 4096) || !validSignInValue(c.RefreshToken, 4096) || !validSignInValue(c.IDToken, 8192) || len(c.Scope) > 1024 || strings.ContainsAny(c.Scope, "\r\n\t") || c.ExpiresAt <= 0 {
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
		if c.Ref != "" || c.Provider != "" || c.Name != "" || c.Remote || !validPassword(c.Passphrase) {
			return AIConnectionView{}, errors.New("finish with the pending sign-in and vault passphrase")
		}
		return s.completeProviderSignIn(ctx, c)
	case "sign-in-refresh", "sign-in-disconnect":
		if c.SignInID != "" || c.Callback != "" || c.Provider != "" || c.Name != "" || c.Remote || !validConnectionRef(c.Ref) || !validPassword(c.Passphrase) {
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
	a.view = ProviderSignInView{ID: id, Provider: "openai", State: "waiting", ExpiresAt: s.now().Add(5 * time.Minute).Unix(), Remote: c.Remote}
	listener, err := s.listenProviderSignInLocked(a)
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
			listener.Close()
			return AIConnectionView{}, err
		}
		a.wire = fixture
		a.view.URL = a.redirect + "?" + url.Values{"code": {"fixture-authorization-code"}, "state": {a.state}, "client_id": {fixture.clientID}}.Encode()
	} else if a.wire == nil && providerActivationEnabled {
		a.wire = newOpenAITransport().wire
	}
	if a.wire == nil {
		listener.Close()
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
	go a.server.Serve(listener)
	return s.aiConnectionLocked(c.Ref)
}

// One callback implementation for each supported public-client flow.
func (s *Store) listenProviderSignInLocked(a *providerSignInAttempt) (net.Listener, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("private sign-in callback unavailable")
	}
	a.redirect = "http://" + listener.Addr().String() + "/auth/callback"
	a.server = &http.Server{ReadHeaderTimeout: 2 * time.Second, MaxHeaderBytes: 4096, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if r.Method != "GET" || r.URL.Path != "/auth/callback" || r.Host != listener.Addr().String() || len(r.URL.RawQuery) > 4096 {
			http.Error(w, "Invalid callback", 400)
			return
		}
		s.provider.mu.Lock()
		defer s.provider.mu.Unlock()
		if s.provider.signIn != a || a.busy || a.view.Remote && a.view.Provider != "google" || a.view.ExpiresAt <= s.now().Unix() || a.callback != "" {
			http.Error(w, "Return to Fased", 409)
			return
		}
		callback := a.redirect + "?" + r.URL.RawQuery
		if _, _, err := providerSignInCallback(a, callback); err != nil {
			http.Error(w, "Return to Fased and retry sign-in", 400)
			return
		}
		a.callback, a.view.State = callback, "callback-ready"
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "Return to Fased to finish connecting your account.")
	})}
	return listener, nil
}

func providerSignInCallback(a *providerSignInAttempt, callback string) (string, string, error) {
	if a.view.Provider == "google" {
		code, err := googleSignInCallback(a, callback)
		return code, a.client, err
	}
	if a.view.Provider == "openrouter" {
		code, err := routerSignInCallback(a, callback)
		return code, "", err
	}
	u, err := url.Parse(callback)
	base, _ := url.Parse(a.redirect)
	if err != nil || len(callback) > 4096 || u.Scheme != base.Scheme || u.Host != base.Host || u.Path != base.Path || u.RawPath != "" || u.User != nil || u.Fragment != "" {
		return "", "", errors.New("invalid sign-in callback")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || q.Get("state") != a.state || len(q["state"]) != 1 {
		return "", "", errors.New("sign-in state mismatch")
	}
	for key, values := range q {
		if len(values) != 1 || (key != "code" && key != "state" && key != "client_id" && key != "scope" && key != "error" && key != "error_description") {
			return "", "", errors.New("invalid sign-in callback")
		}
	}
	if q.Get("error") != "" {
		return "", "", errors.New("sign-in was not authorized")
	}
	client := q.Get("client_id")
	if a.client != "dynamic_agent_client" {
		if client != "" && client != a.client {
			return "", "", errors.New("sign-in registration changed")
		}
		client = a.client
	}
	if !validSignInValue(q.Get("code"), 2048) || !validSignInValue(client, 256) || client == "dynamic_agent_client" {
		return "", "", errors.New("sign-in code or issued client missing")
	}
	return q.Get("code"), client, nil
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
	callback := c.Callback
	if callback == "" {
		callback = a.callback
	}
	if callback == "" {
		s.provider.mu.Unlock()
		return AIConnectionView{}, errors.New("complete sign-in in your browser first")
	}
	code, client, err := providerSignInCallback(a, callback)
	if err != nil {
		s.cancelProviderSignInLocked()
		s.provider.mu.Unlock()
		return AIConnectionView{}, err
	}
	a.busy = true
	nonce, verifier, redirect, wire, expected := a.nonce, a.verifier, a.redirect, a.wire, a.expected
	s.provider.mu.Unlock()
	credential, err := exchangeProviderSignIn(ctx, wire, url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "resource": {signInResource}}, nonce, client, s.now())
	if err == nil && expected != nil && (credential.Issuer != expected.Issuer || credential.Subject != expected.Subject || credential.ClientID != expected.ClientID) {
		err = errors.New("sign-in account identity changed")
	}
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
	v, err := s.saveProviderSignInLocked(credential, c.Passphrase, a.ref, a.name, a.fixture, a.digest)
	s.cancelProviderSignInLocked()
	s.provider.mu.Unlock()
	if err != nil {
		return v, err
	}
	if !credential.planUse() {
		return s.AIConnection(v.Ref)
	}
	return s.refreshProviderModels(ctx, v.Ref, s.providerModelTransport(v.Ref))
}

func (s *Store) saveProviderSignInLocked(c providerSignInCredential, passphrase, ref, name string, fixture bool, expected [32]byte) (AIConnectionView, error) {
	raw, _ := json.Marshal(c)
	defer clear(raw)
	if _, ok := parseProviderSignInCredential(raw); !ok {
		return AIConnectionView{}, errors.New("invalid sign-in credential")
	}
	return s.persistSignInCredentialLocked(raw, passphrase, ref, name, "openai", "openai-signin", fixture, expected)
}
func (s *Store) persistSignInCredentialLocked(raw []byte, passphrase, ref, name, provider, mode string, fixture bool, expected [32]byte) (AIConnectionView, error) {
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
		plain, err := s.openAIVault(env, passphrase)
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
	key := passwordHash(passphrase, env.Salt)
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
	state.ref, state.key, state.expires = env.Ref, append([]byte(nil), raw...), s.now().Add(15*time.Minute).Unix()
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
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("sign-in request unavailable")
	}
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := http.Client{Transport: wire, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("sign-in redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("sign-in provider unavailable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	defer clear(raw)
	if err != nil || len(raw) > 65536 || res.StatusCode != http.StatusOK || res.Header.Get("Content-Encoding") != "" {
		return errors.New("sign-in provider refused request")
	}
	if target == nil {
		if len(raw) != 0 {
			return errors.New("invalid revocation response")
		}
		return nil
	}
	kind, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" || !uniqueAIJSON(raw, 65536) || json.Unmarshal(raw, target) != nil {
		return errors.New("invalid sign-in provider response")
	}
	return nil
}
func readSignInDiscovery(ctx context.Context, wire http.RoundTripper) (signInDiscovery, error) {
	var d signInDiscovery
	err := signInHTTP(ctx, wire, "GET", signInIssuer+"/.well-known/openid-configuration", nil, &d)
	if err != nil || d.Issuer != signInIssuer || !signInEndpoint(d.JWKS) || !signInEndpoint(d.Revoke) {
		return signInDiscovery{}, errors.New("untrusted sign-in discovery")
	}
	return d, nil
}

func parseProviderSignInModels(raw []byte, fetchedAt int64) ([]discoveredModel, error) {
	var response struct {
		Models []struct {
			Slug       string `json:"slug"`
			Name       string `json:"display_name"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Models == nil || len(response.Models) > 512 {
		return nil, errors.New("invalid ChatGPT model catalogue")
	}
	rows := []discoveredModel{}
	for _, model := range response.Models {
		if !validProviderModelID(model.Slug) || !validAgentText(model.Name, 256, true) || (model.Visibility != "list" && model.Visibility != "hide") {
			return nil, errors.New("invalid ChatGPT model entry")
		}
		if model.Visibility != "list" {
			continue
		}
		// This route provides no creation timestamp. The retained value is the
		// verified catalogue observation time, never evidence of model age.
		rows = append(rows, discoveredModel{ID: model.Slug, Object: "model", OwnedBy: "openai", Created: fetchedAt})
	}
	return rows, nil
}

type signInTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Type         string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
}

func exchangeProviderSignIn(ctx context.Context, wire http.RoundTripper, form url.Values, nonce, client string, now time.Time) (providerSignInCredential, error) {
	var result signInTokenResponse
	if err := signInHTTP(ctx, wire, "POST", signInIssuer+"/api/accounts/oauth/token", form, &result); err != nil {
		return providerSignInCredential{}, err
	}
	if !strings.EqualFold(result.Type, "Bearer") || result.ExpiresIn < 1 || result.ExpiresIn > 86400 {
		return providerSignInCredential{}, errors.New("invalid sign-in token grant")
	}
	d, err := readSignInDiscovery(ctx, wire)
	if err != nil {
		return providerSignInCredential{}, err
	}
	subject, err := validateSignInIDToken(ctx, wire, d.JWKS, result.IDToken, client, nonce, now.Unix())
	if err != nil {
		return providerSignInCredential{}, err
	}
	c := providerSignInCredential{Version: 1, Issuer: d.Issuer, Subject: subject, ClientID: client, AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, IDToken: result.IDToken, Scope: result.Scope, ExpiresAt: now.Unix() + result.ExpiresIn}
	raw, _ := json.Marshal(c)
	defer clear(raw)
	if _, ok := parseProviderSignInCredential(raw); !ok {
		return providerSignInCredential{}, errors.New("invalid sign-in credential grant")
	}
	return c, nil
}

func validateSignInIDToken(ctx context.Context, wire http.RoundTripper, jwks, token, client, nonce string, now int64) (string, error) {
	return validateOIDCIDToken(ctx, wire, jwks, token, client, nonce, now, signInIssuer, signInHTTP)
}
func validateOIDCIDToken(ctx context.Context, wire http.RoundTripper, jwks, token, client, nonce string, now int64, issuer string, fetch func(context.Context, http.RoundTripper, string, string, url.Values, any) error) (string, error) {
	refused := errors.New("sign-in identity verification failed")
	if len(token) > 16384 {
		return "", refused
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", refused
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || !uniqueAIJSON(header, 4096) {
		return "", refused
	}
	var h struct {
		Alg, Kid string
		Crit     []string
		JKU      string
		JWK      json.RawMessage
	}
	if json.Unmarshal(header, &h) != nil || h.Alg != "RS256" || !validSignInValue(h.Kid, 256) || len(h.Crit) != 0 || h.JKU != "" || len(h.JWK) != 0 {
		return "", refused
	}
	var keys struct {
		Keys []struct{ Kty, Kid, Alg, Use, N, E string }
	}
	if fetch(ctx, wire, "GET", jwks, nil, &keys) != nil || len(keys.Keys) > 32 {
		return "", refused
	}
	var public *rsa.PublicKey
	for _, key := range keys.Keys {
		if key.Kid != h.Kid {
			continue
		}
		if public != nil || key.Kty != "RSA" || key.Alg != "RS256" || key.Use != "sig" {
			return "", refused
		}
		n, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil || len(n) < 256 || len(n) > 512 {
			return "", refused
		}
		e, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil || len(e) > 4 || len(e) == 0 {
			return "", refused
		}
		exponent := 0
		for _, b := range e {
			exponent = exponent*256 + int(b)
		}
		if exponent < 3 || exponent%2 == 0 {
			return "", refused
		}
		public = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exponent}
	}
	if public == nil {
		return "", refused
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", refused
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(public, crypto.SHA256, digest[:], signature) != nil {
		return "", refused
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !uniqueAIJSON(claims, 12288) {
		return "", refused
	}
	var c struct {
		Iss, Sub, Nonce string
		Aud             json.RawMessage
		Exp, Iat        int64
	}
	if json.Unmarshal(claims, &c) != nil || (c.Iss != issuer && !(issuer == googleIssuer && c.Iss == "accounts.google.com")) || !validSignInValue(c.Sub, 512) || c.Exp <= now || c.Iat > now+30 || c.Iat < now-86400 || nonce != "" && c.Nonce != nonce {
		return "", refused
	}
	var audience string
	if json.Unmarshal(c.Aud, &audience) != nil {
		var audiences []string
		if json.Unmarshal(c.Aud, &audiences) != nil || len(audiences) != 1 {
			return "", refused
		}
		audience = audiences[0]
	}
	if audience != client {
		return "", refused
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
	plain, err := s.openAIVault(env, c.Passphrase)
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
		d, err := readSignInDiscovery(ctx, wire)
		if err != nil {
			return AIConnectionView{}, err
		}
		if err = signInHTTP(ctx, wire, "POST", d.Revoke, url.Values{"token": {credential.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {credential.ClientID}}, nil); err != nil {
			return AIConnectionView{}, errors.New("account locked; remote revocation not confirmed, retry disconnect or revoke in ChatGPT settings")
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
		if _, err = s.saveProviderSignInLocked(credential, c.Passphrase, c.Ref, env.Name, env.TestOnly, expected); err != nil {
			return AIConnectionView{}, err
		}
		s.clearAIConnectionLocked(c.Ref)
		return s.aiConnectionLocked(c.Ref)
	}
	renewed, err := exchangeProviderSignIn(ctx, wire, url.Values{"grant_type": {"refresh_token"}, "client_id": {credential.ClientID}, "refresh_token": {credential.RefreshToken}, "resource": {signInResource}}, "", credential.ClientID, s.now())
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
	v, err := s.saveProviderSignInLocked(renewed, c.Passphrase, c.Ref, env.Name, env.TestOnly, expected)
	s.provider.mu.Unlock()
	if err != nil {
		return v, err
	}
	if !renewed.planUse() {
		return s.AIConnection(v.Ref)
	}
	return s.refreshProviderModels(ctx, v.Ref, s.providerModelTransport(v.Ref))
}
