package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/swarm"
)

func postSwarmRelay(h http.Handler, host string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/v1/swarm/relay", bytes.NewReader(body))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestStage6SwarmRelayEndpointAuthAndReplay(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close(context.Background())

	token := claimFixture(t, srv.Store)
	h := srv.HTTP.Handler.(*HTTPHandler)
	parsedURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := parsedURL.Host

	peerPub, peerPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, untrustedPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	makeSignedBody := func(priv ed25519.PrivateKey, id, nonce string) []byte {
		env := swarm.Envelope{
			EnvelopeID: id,
			Nonce:      nonce,
			Kind:       "chat",
			Thesis:     "Peer relay check",
			Confidence: 0.75,
			Timestamp:  time.Now().UTC().Unix(),
			Untrusted:  false,
		}
		if err := swarm.SignEnvelope(priv, &env); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(env)
		return b
	}

	// 1. Swarm disabled by default -> relay rejects cleanly with 403.
	disabledBody := makeSignedBody(peerPriv, "chat-disabled-1", "nonce-disabled-1")
	if rec := postSwarmRelay(h, host, disabledBody); rec.Code != 403 || !strings.Contains(rec.Body.String(), "opt-in") {
		t.Fatalf("expected 403 opt-in disabled while swarm disabled, got %d: %s", rec.Code, rec.Body.String())
	}

	// Enable Swarm and register trusted peer key via operator routes.
	if rec := stage4Req(h, http.MethodPut, srv.URL, "/api/v1/swarm/config", `{"enabled":true}`, token, srv.URL, csrfToken(token)); rec.Code != 200 {
		t.Fatalf("enable swarm failed (%d): %s", rec.Code, rec.Body.String())
	}
	addPeerPayload, _ := json.Marshal(swarm.PeerInput{
		PeerID:    "peer-trusted-node",
		Alias:     "Trusted Tailnet Peer",
		PubKeyHex: hex.EncodeToString(peerPub),
		Endpoint:  "https://trusted.tail1234.ts.net",
	})
	if rec := stage4Req(h, http.MethodPost, srv.URL, "/api/v1/swarm/peers", string(addPeerPayload), token, srv.URL, csrfToken(token)); rec.Code != 200 {
		t.Fatalf("add peer failed (%d): %s", rec.Code, rec.Body.String())
	}

	// 2. Unregistered key -> 403.
	unknownBody := makeSignedBody(untrustedPriv, "chat-unknown-1", "nonce-unknown-1")
	if rec := postSwarmRelay(h, host, unknownBody); rec.Code != 403 || !strings.Contains(rec.Body.String(), "registered peer") {
		t.Fatalf("expected 403 unknown peer for unknown signer, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Trusted peer without browser session cookie -> 200 and untrusted=true.
	validBody := makeSignedBody(peerPriv, "chat-trusted-1", "nonce-trusted-1")
	okRec := postSwarmRelay(h, host, validBody)
	if okRec.Code != 200 {
		t.Fatalf("expected 200 for trusted relay, got %d: %s", okRec.Code, okRec.Body.String())
	}
	var ack map[string]any
	if err := json.Unmarshal(okRec.Body.Bytes(), &ack); err != nil || ack["accepted"] != true || ack["untrusted"] != true {
		t.Fatalf("unexpected relay ack: %s err=%v", okRec.Body.String(), err)
	}

	// 4. Duplicate nonce replay -> 403.
	if repRec := postSwarmRelay(h, host, validBody); repRec.Code != 403 || !strings.Contains(repRec.Body.String(), "replay") {
		t.Fatalf("expected 403 replay nonce, got %d: %s", repRec.Code, repRec.Body.String())
	}
}
