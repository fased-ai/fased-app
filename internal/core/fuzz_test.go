package core

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var (
	fuzzRSAKey    *rsa.PrivateKey
	fuzzRSAJWKS   []byte
	fuzzSignedJWT string
	fuzzJWTNow    = int64(1795400000)
)

func init() {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	fuzzRSAKey = key
	jwk := map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": "fuzz-kid-1",
			"alg": "RS256",
			"use": "sig",
			"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01}),
		}},
	}
	fuzzRSAJWKS, _ = json.Marshal(jwk)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"fuzz-kid-1"}`))
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://auth.openai.com","sub":"user_fuzz_1","aud":"client_fuzz","nonce":"nonce_fuzz","iat":1795399900,"exp":1795403600}`))
	sum := sha256.Sum256([]byte(header + "." + claims))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	fuzzSignedJWT = header + "." + claims + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func FuzzUniqueAIJSON(f *testing.F) {
	seeds := []string{
		`{}`,
		`[]`,
		`{"id":"resp_1","usage":{"input_tokens":10,"output_tokens":5}}`,
		`{"a":1,"a":2}`,
		`{"\u0061":1,"a":2}`,
		`{"a":1} {"b":2}`,
		strings.Repeat("[", 25) + strings.Repeat("]", 25),
		`{"nested":{"x":true,"y":null,"z":[1,2,3]}}`,
		"",
		"{",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		okFull := uniqueAIJSON(data)
		okSmall := uniqueAIJSON(data, 64)
		if okFull && len(bytes.TrimSpace(data)) == 0 {
			t.Fatal("uniqueAIJSON accepted whitespace-only input")
		}
		if okSmall && !okFull && len(data) <= 4096 {
			// A stricter budget can reject, never accept more when well within default.
			t.Fatalf("uniqueAIJSON accepted at budget 64 but rejected at default 4096")
		}
	})
}

func FuzzPartialAIAnswerAndReadAIStream(f *testing.F) {
	validStream := strings.Join([]string{
		`event: response.created`,
		`data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_fuzz","model":"gpt-5.4"}}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","sequence_number":1,"output_index":0,"content_index":0,"delta":"{\"answer\":\"Hello"}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","sequence_number":2,"output_index":0,"content_index":0,"delta":" world\"}"}`,
		``,
		`event: response.output_text.done`,
		`data: {"type":"response.output_text.done","sequence_number":3,"output_index":0,"content_index":0,"text":"{\"answer\":\"Hello world\"}"}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","sequence_number":4,"response":{"id":"resp_fuzz","model":"gpt-5.4","status":"completed","output":[{"type":"message","content":[{"text":"{\"answer\":\"Hello world\"}"}]}]}}`,
		``,
		``,
	}, "\n")
	seeds := []string{
		validStream,
		"event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"r1\",\"model\":\"gpt-5.4\"}}\n\n",
		"data: {\"type\":\"response.failed\",\"sequence_number\":1,\"response\":{\"id\":\"r1\",\"model\":\"gpt-5.4\",\"status\":\"failed\"}}\n\n",
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":5,\"delta\":\"bad\"}\n\n",
		`{"answer":"partial\nline`,
		`{"status":"need_approval","answer":"ok"}`,
		"data: [DONE]\n\n",
		"",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = partialAIAnswer(string(data))
		completed, err := readAIStream(bytes.NewReader(data), "gpt-5.4", func(delta string) {
			_ = len(delta)
		}, true)
		if err == nil && completed != nil {
			if !uniqueAIJSON(completed, 16384) {
				t.Fatalf("readAIStream accepted non-unique/invalid JSON completion")
			}
		}
	})
}

func FuzzProviderSignInCallbacks(f *testing.F) {
	seeds := []string{
		"http://127.0.0.1:1455/auth/callback?state=state_fuzz&code=code_123&client_id=app_123&iss=https%3A%2F%2Fauth.openai.com",
		"http://127.0.0.1:1455/oauth2callback?state=state_fuzz&code=gcode_123&iss=https%3A%2F%2Faccounts.google.com",
		"http://127.0.0.1:3000/?state=state_fuzz&code=rcode_123",
		"rcode_direct_123",
		"http://127.0.0.1:1455/auth/callback?state=state_fuzz&state=state_fuzz&code=code_123",
		"http://user:pass@127.0.0.1:1455/auth/callback?state=state_fuzz&code=code_123",
		"http://127.0.0.1:1455/auth/callback?state=state_fuzz&code=dynamic_agent_client&client_id=dynamic_agent_client",
		"https://evil.example/callback?state=state_fuzz&code=1",
		"http://127.0.0.1:1455/auth/callback?state=state_fuzz&code=1#fragment",
		"",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	openaiAttempt := &providerSignInAttempt{
		view:     ProviderSignInView{Provider: "openai", Remote: false},
		state:    "state_fuzz",
		redirect: "http://127.0.0.1:1455/auth/callback",
		client:   "dynamic_agent_client",
	}
	googleAttempt := &providerSignInAttempt{
		view:     ProviderSignInView{Provider: "google", Remote: false},
		state:    "state_fuzz",
		redirect: "http://127.0.0.1:1455/oauth2callback",
		client:   "123-fuzz.apps.googleusercontent.com",
	}
	routerLocal := &providerSignInAttempt{
		view:     ProviderSignInView{Provider: "openrouter", Remote: false},
		state:    "state_fuzz",
		redirect: "http://127.0.0.1:3000/",
	}
	routerRemote := &providerSignInAttempt{
		view:     ProviderSignInView{Provider: "openrouter", Remote: true},
		state:    "state_fuzz",
		redirect: "http://127.0.0.1:3000/",
	}
	f.Fuzz(func(t *testing.T, callback string) {
		if code, client, _, err := providerSignInCallbackWithScope(openaiAttempt, callback); err == nil {
			if !validSignInValue(code, 2048) || !validSignInValue(client, 256) || client == "dynamic_agent_client" {
				t.Fatalf("openai callback returned invalid output code=%q client=%q", code, client)
			}
		}
		if code, err := googleSignInCallback(googleAttempt, callback); err == nil {
			if !validSignInValue(code, 2048) {
				t.Fatalf("google callback returned invalid code=%q", code)
			}
		}
		if code, err := routerSignInCallback(routerLocal, callback); err == nil {
			if !validSignInValue(code, 2048) {
				t.Fatalf("router local callback returned invalid code=%q", code)
			}
		}
		if code, err := routerSignInCallback(routerRemote, callback); err == nil {
			if !validSignInValue(code, 2048) {
				t.Fatalf("router remote callback returned invalid code=%q", code)
			}
		}
	})
}

func FuzzValidateOIDCIDToken(f *testing.F) {
	f.Add(fuzzSignedJWT, "client_fuzz", "nonce_fuzz", true, []byte(`{}`))
	f.Add("eyJhbGciOiJub25lIn0.e30.", "client_fuzz", "nonce_fuzz", true, []byte(`{}`))
	f.Add("a.b.c", "client_fuzz", "", false, fuzzRSAJWKS)
	f.Add(strings.Repeat("x.", 4), "c", "n", false, []byte(`{"keys":[]}`))
	f.Fuzz(func(t *testing.T, token, client, nonce string, useValidJWKS bool, jwksBytes []byte) {
		fetch := func(_ context.Context, _ http.RoundTripper, method, endpoint string, _ url.Values, target any) error {
			if method != "GET" || endpoint == "" {
				return errors.New("bad request")
			}
			raw := jwksBytes
			if useValidJWKS {
				raw = fuzzRSAJWKS
			}
			if len(raw) > 65536 || !uniqueAIJSON(raw, 65536) {
				return errors.New("invalid jwks")
			}
			return json.Unmarshal(raw, target)
		}
		sub, err := validateOIDCIDToken(context.Background(), nil, "https://auth.openai.com/.well-known/jwks.json", token, client, nonce, fuzzJWTNow, "https://auth.openai.com", fetch)
		if err == nil {
			if !validSignInValue(sub, 512) || strings.Count(token, ".") != 2 {
				t.Fatalf("validateOIDCIDToken accepted invalid subject=%q or token=%q", sub, token)
			}
		}
	})
}

func FuzzVaultCiphertextBoundAndFormats(f *testing.F) {
	f.Add(byte(5), true, []byte(`{"version":1,"issuer":"https://auth.openai.com","subject":"sub1","clientId":"app1","accessToken":"tok","refreshToken":"ref","idToken":"id","scope":"openid","expiresAt":1800000000}`))
	f.Add(byte(8), false, []byte(`{"version":3,"portable":true,"keyVersion":2,"network":"mainnet-beta","endpoint":"https://api.mainnet-beta.solana.com","vault":{"version":2,"ref":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","binding":"`+strings.Repeat("0", 64)+`","salt":"MTIzNDU2Nzg5MDEyMzQ1Ng==","nonce":"MTIzNDU2Nzg5MDEy","ciphertext":"MTIzNDU2Nzg5MDEyMzQ1Njc4OTA="}}`))
	f.Add(byte(7), false, []byte(`{"version":1,"purpose":"jupiter","ref":"test-ref","binding":"`+strings.Repeat("a", 64)+`"}`))
	f.Add(byte(6), true, []byte(`not-json`))
	f.Fuzz(func(t *testing.T, version byte, testOnly bool, data []byte) {
		bound := aiVaultCiphertextBound(aiVaultEnvelope{Version: int(version), TestOnly: testOnly})
		if bound <= 0 {
			t.Fatalf("non-positive ciphertext bound %d", bound)
		}
		if c, ok := parseProviderSignInCredential(data); ok {
			if c.Version != 1 || c.Issuer != signInIssuer {
				t.Fatalf("invalid parsed openai credential: %+v", c)
			}
		}
		if gc, ok := parseGoogleCredential(data); ok {
			if gc.Version != 1 || gc.Issuer != googleIssuer {
				t.Fatalf("invalid parsed google credential: %+v", gc)
			}
		}
		var col aiVaultCollection
		if json.Unmarshal(data, &col) == nil {
			_ = validateAIVaultCollection(col)
		}
		var rpcB RPCCredentialBackup
		if json.Unmarshal(data, &rpcB) == nil {
			if validPortableRPCBackup(rpcB) && (!rpcB.Portable || rpcB.Version != 3) {
				t.Fatalf("validPortableRPCBackup accepted non-portable backup")
			}
		}
		var vEnv venueEnvelope
		if json.Unmarshal(data, &vEnv) == nil {
			_ = validVenueEnvelope(vEnv)
		}
		var vBack VenueCredentialBackup
		if json.Unmarshal(data, &vBack) == nil {
			_ = validVenueBackup(vBack)
		}
	})
}

func FuzzPublicRPCDestinationAndTargetOrdering(f *testing.F) {
	f.Add("mainnet-beta", "https://api.mainnet-beta.solana.com", "example.com:443", []byte{8, 8, 8, 8, 127, 0, 0, 1})
	f.Add("devnet", "https://api.devnet.solana.com/", "127.0.0.1:443", []byte{10, 0, 0, 1})
	f.Add("testnet", "http://169.254.169.254/latest", "host.local:443", []byte{169, 254, 169, 254})
	f.Add("", "https://example.com:8443/path?x=1", "[::1]:443", []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	f.Fuzz(func(t *testing.T, network, rawURL, address string, ipBytes []byte) {
		g := solanaGenesis(network)
		if g != "" && network != "mainnet-beta" && network != "devnet" {
			t.Fatalf("unexpected genesis for unknown network %q: %q", network, g)
		}
		if parsed, err := publicRPCURL(rawURL); err == nil {
			if parsed.Scheme != "https" || !utf8.ValidString(parsed.String()) {
				t.Fatalf("publicRPCURL admitted unsafe URL %q", parsed.String())
			}
		}
		var ips []netip.Addr
		for i := 0; i+4 <= len(ipBytes) && len(ips) < 8; {
			if i+16 <= len(ipBytes) && ipBytes[i]&1 == 1 {
				if addr, ok := netip.AddrFromSlice(ipBytes[i : i+16]); ok {
					ips = append(ips, addr)
				}
				i += 16
				continue
			}
			if addr, ok := netip.AddrFromSlice(ipBytes[i : i+4]); ok {
				ips = append(ips, addr)
			}
			i += 4
		}
		_ = orderPublicRPCTargets(ips)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, _ = dialPublicRPC(ctx, "tcp", address, func(context.Context, string, string) ([]netip.Addr, error) {
			if len(ips) == 0 {
				return nil, io.EOF
			}
			return ips, nil
		}, func(_ context.Context, _ string, target string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(target)
			if err != nil {
				t.Fatalf("dial called with invalid host:port %q: %v", target, err)
			}
			addr, err := netip.ParseAddr(host)
			if err != nil || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() || addr.Is4In6() {
				t.Fatalf("SSRF guard bypass: dialed non-public address %q", target)
			}
			return nil, errors.New("fuzz dial stop")
		})
	})
}
