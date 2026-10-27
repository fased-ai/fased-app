package core

// Inert authentication for the existing --ai-fixture core. It signs disposable
// identities in process and never opens an external authorization/token endpoint.
import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type providerSignInFixture struct {
	private                                            *rsa.PrivateKey
	clientID, subject, nonce, verifier, refresh, scope string
	now                                                func() time.Time
}

type providerSignInModelFixture struct{}

func (providerSignInModelFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != "GET" || !providerRequestAllowed("openai", r) || r.URL.Path != "/v1/models" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fased-test-signin-access-") {
		return nil, errors.New("sign-in model fixture refused")
	}
	models := []any{}
	for _, m := range registryForProvider("openai").Models {
		models = append(models, map[string]string{"slug": m.ID, "display_name": m.Name, "visibility": "list"})
	}
	return fixtureAdmissionResponse(r, map[string]any{"models": models}), nil
}

func newProviderSignInFixture(a *providerSignInAttempt, now func() time.Time) (*providerSignInFixture, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	clientID, subject := "oaiapp_fased_disposable_fixture", "fased-disposable-user"
	if a.expected != nil {
		clientID, subject = a.expected.ClientID, a.expected.Subject
	}
	return &providerSignInFixture{private: key, clientID: clientID, subject: subject, nonce: a.nonce, verifier: a.verifier, scope: signInScope, now: now}, nil
}
func newProviderSignInRenewFixture(c providerSignInCredential, now func() time.Time) (*providerSignInFixture, error) {
	f, err := newProviderSignInFixture(&providerSignInAttempt{expected: &c}, now)
	if err == nil {
		f.refresh = c.RefreshToken
		f.scope = c.Scope
	}
	return f, err
}
func (f *providerSignInFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	if !signInEndpoint(r.URL.String()) {
		return nil, errors.New("fixture sign-in origin refused")
	}
	var result any
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		if r.Method != "GET" {
			return nil, errors.New("fixture method refused")
		}
		result = map[string]string{"issuer": signInIssuer, "jwks_uri": signInIssuer + "/.well-known/jwks.json", "revocation_endpoint": signInIssuer + "/api/accounts/oauth/revoke"}
	case "/.well-known/jwks.json":
		if r.Method != "GET" {
			return nil, errors.New("fixture method refused")
		}
		result = map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "fased-fixture", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(f.private.N.Bytes()), "e": "AQAB"}}}
	case "/api/accounts/oauth/token":
		if r.Method != "POST" || r.ParseForm() != nil || r.Form.Get("client_id") != f.clientID || r.Form.Get("resource") != signInResource {
			return nil, errors.New("fixture token binding refused")
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "fixture-authorization-code" || r.Form.Get("code_verifier") != f.verifier {
				return nil, errors.New("fixture PKCE refused")
			}
		case "refresh_token":
			if r.Form.Get("refresh_token") != f.refresh {
				return nil, errors.New("fixture refresh refused")
			}
		default:
			return nil, errors.New("fixture grant refused")
		}
		header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "fased-fixture"})
		claims, _ := json.Marshal(map[string]any{"iss": signInIssuer, "sub": f.subject, "aud": f.clientID, "nonce": f.nonce, "iat": f.now().Unix(), "exp": f.now().Add(time.Hour).Unix()})
		content := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
		digest := sha256.Sum256([]byte(content))
		signature, err := rsa.SignPKCS1v15(rand.Reader, f.private, crypto.SHA256, digest[:])
		if err != nil {
			return nil, err
		}
		suffix, err := randomToken()
		if err != nil {
			return nil, err
		}
		result = map[string]any{"access_token": "fased-test-signin-access-" + suffix, "refresh_token": "fased-test-signin-refresh-" + suffix, "id_token": content + "." + base64.RawURLEncoding.EncodeToString(signature), "scope": f.scope, "expires_in": 3600, "token_type": "Bearer"}
	case "/api/accounts/oauth/revoke":
		if r.Method != "POST" || r.ParseForm() != nil || r.Form.Get("client_id") != f.clientID || r.Form.Get("token") != f.refresh || r.Form.Get("token_type_hint") != "refresh_token" {
			return nil, errors.New("fixture revocation refused")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	default:
		return nil, errors.New("fixture sign-in path refused")
	}
	raw, _ := json.Marshal(result)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw))), Request: r}, nil
}
