package core

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type googleOAuthFixture struct {
	credential      googleOAuthCredential
	nonce, verifier string
	private         *rsa.PrivateKey
	now             func() time.Time
}

func newGoogleOAuthFixture(c googleOAuthCredential, nonce, verifier string, now func() time.Time) (*googleOAuthFixture, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return &googleOAuthFixture{credential: c, nonce: nonce, verifier: verifier, private: key, now: now}, nil
}
func (f *googleOAuthFixture) RoundTrip(r *http.Request) (*http.Response, error) {
	switch r.URL.String() {
	case googleCertsURL:
		if r.Method != "GET" {
			return nil, errors.New("Google fixture method refused")
		}
		return fixtureAdmissionResponse(r, map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "fased-google-fixture", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(f.private.N.Bytes()), "e": "AQAB"}}}), nil
	case googleRevokeURL:
		if r.Method != "POST" || r.ParseForm() != nil || r.Form.Get("token") == "" {
			return nil, errors.New("Google fixture revoke refused")
		}
		return fixtureAdmissionResponse(r, map[string]any{}), nil
	case googleTokenURL:
		if r.Method != "POST" || r.ParseForm() != nil || r.Form.Get("client_id") != f.credential.ClientID || r.Form.Get("client_secret") != f.credential.ClientSecret {
			return nil, errors.New("Google fixture client refused")
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "fixture-google-code" || r.Form.Get("code_verifier") != f.verifier || r.Form.Get("redirect_uri") == "" {
				return nil, errors.New("Google fixture PKCE refused")
			}
		case "refresh_token":
			if r.Form.Get("refresh_token") != f.credential.RefreshToken {
				return nil, errors.New("Google fixture refresh refused")
			}
		default:
			return nil, errors.New("Google fixture grant refused")
		}
		subject := f.credential.Subject
		if subject == "" {
			subject = "fixture-google-subject"
		}
		header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "fased-google-fixture"})
		claims, _ := json.Marshal(map[string]any{"iss": googleIssuer, "sub": subject, "aud": f.credential.ClientID, "nonce": f.nonce, "iat": f.now().Unix(), "exp": f.now().Add(time.Hour).Unix()})
		text := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
		digest := sha256.Sum256([]byte(text))
		signature, _ := rsa.SignPKCS1v15(rand.Reader, f.private, crypto.SHA256, digest[:])
		refresh := f.credential.RefreshToken
		if refresh == "" {
			refresh = "fased-fixture-google-refresh-token"
		}
		return fixtureAdmissionResponse(r, map[string]any{"access_token": "fased-fixture-google-access-token", "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600, "scope": googleScope, "id_token": text + "." + base64.RawURLEncoding.EncodeToString(signature)}), nil
	}
	return nil, errors.New("Google fixture endpoint refused")
}
