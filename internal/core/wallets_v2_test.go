package core

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWalletsV2HTTPRoundTripAndRPCBalance(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	s.providerFixture = true

	ts, token, csrf := startV2HTTPTestServer(t, s)

	// 1. Unauthenticated access denied.
	unauth := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/wallets", "", "", "", "", "application/json")
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on GET /api/v1/wallets, got %d", unauth.StatusCode)
	}

	// 2. Create Solana wallet -> returns 12-word mnemonic once + Cache-Control: no-store.
	createResp := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/create", ts.URL, token, csrf, `{"name":"Phantom 1","role":"personal","words":12,"passphrase":"secret-pass-123"}`, "application/json")
	rawCreate, _ := io.ReadAll(createResp.Body)
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusOK || createResp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("POST /api/v1/wallets/create failed: code=%d cc=%q body=%s", createResp.StatusCode, createResp.Header.Get("Cache-Control"), rawCreate)
	}
	var created struct {
		Wallet struct {
			ID        string `json:"id"`
			PublicKey string `json:"publicKey"`
		} `json:"wallet"`
		Mnemonic  string `json:"mnemonic"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.Unmarshal(rawCreate, &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if len(strings.Fields(created.Mnemonic)) != 12 || created.PublicKey == "" {
		t.Fatalf("unexpected created payload: %+v", created)
	}

	// 3. List wallets NEVER leaks mnemonic or secret.
	listResp := doV2JSONRequest(t, http.MethodGet, ts.URL+"/api/v1/wallets", "", token, "", "", "application/json")
	rawList, _ := io.ReadAll(listResp.Body)
	listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK || strings.Contains(string(rawList), created.Mnemonic) || !strings.Contains(string(rawList), created.PublicKey) {
		t.Fatalf("GET /api/v1/wallets leaked or missed wallet: %s", rawList)
	}

	// 4. Balance check without configured RPC returns rpcRequired=true cleanly.
	balMissing := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/balance", ts.URL, token, csrf, `{"cluster":"mainnet-beta"}`, "application/json")
	rawBalMissing, _ := io.ReadAll(balMissing.Body)
	balMissing.Body.Close()
	if balMissing.StatusCode != http.StatusOK || !strings.Contains(string(rawBalMissing), `"rpcRequired":true`) {
		t.Fatalf("expected rpcRequired=true before RPC setup, got: %s", rawBalMissing)
	}

	// Enable Solana Mainnet RPC -> balance returns SOL cleanly.
	rpcEnable := doV2JSONRequest(t, http.MethodPut, ts.URL+"/api/v1/data-plugins/solana-mainnet", ts.URL, token, csrf, `{"enabled":true}`, "application/json")
	rpcEnable.Body.Close()
	balOK := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/balance", ts.URL, token, csrf, `{"cluster":"mainnet-beta"}`, "application/json")
	rawBalOK, _ := io.ReadAll(balOK.Body)
	balOK.Body.Close()
	if balOK.StatusCode != http.StatusOK || !strings.Contains(string(rawBalOK), `"rpcRequired":false`) {
		t.Fatalf("expected balance result after RPC enable, got: %s", rawBalOK)
	}

	// 5. Export wrong passphrase -> 403; correct passphrase -> returns mnemonic & keypairJson.
	expWrong := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/export", ts.URL, token, csrf, `{"passphrase":"bad"}`, "application/json")
	expWrong.Body.Close()
	if expWrong.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 wrong passphrase export, got %d", expWrong.StatusCode)
	}
	expOK := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/"+created.Wallet.ID+"/export", ts.URL, token, csrf, `{"passphrase":"secret-pass-123"}`, "application/json")
	rawExp, _ := io.ReadAll(expOK.Body)
	expOK.Body.Close()
	if expOK.StatusCode != http.StatusOK || !strings.Contains(string(rawExp), created.Mnemonic) || !strings.Contains(string(rawExp), `"keypairJson"`) {
		t.Fatalf("export failed: %d %s", expOK.StatusCode, rawExp)
	}

	// 6. Import watch-only + delete works cleanly.
	impWatch := doV2JSONRequest(t, http.MethodPost, ts.URL+"/api/v1/wallets/import", ts.URL, token, csrf, `{"name":"Watch 1","watchAddress":"`+created.PublicKey+`"}`, "application/json")
	impWatch.Body.Close()
	if impWatch.StatusCode != http.StatusOK {
		t.Fatalf("watch import expected 200, got %d", impWatch.StatusCode)
	}

	delResp := doV2JSONRequest(t, http.MethodDelete, ts.URL+"/api/v1/wallets/"+created.Wallet.ID, ts.URL, token, csrf, "", "application/json")
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE wallet failed: %d", delResp.StatusCode)
	}
}
