package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/onchain"
)

func TestJupiterQuoteEndpointAndDataPluginQueryRedaction(t *testing.T) {
	ctx := t.Context()
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := openStoreWithTaskSecrets(stateDir, fixtureTaskSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.providerFixture = true

	handler, err := NewHTTPHandler(store, "http://127.0.0.1:18771", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	csrf := csrfToken(token)

	doReq := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1:18771"+path, strings.NewReader(body))
		req.Host = "127.0.0.1:18771"
		req.Header.Set("Origin", "http://127.0.0.1:18771")
		req.Header.Set("Authorization", "Bearer "+token)
		if method != http.MethodGet {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	// 1. Create a trading wallet in custody.
	createW := doReq(http.MethodPost, "/api/v1/wallets/create", `{"name":"SwapDesk","role":"trading","rpcUrl":"https://api.mainnet-beta.solana.com"}`)
	if createW.Code != 200 {
		t.Fatalf("create wallet failed: %d %s", createW.Code, createW.Body.String())
	}
	var created struct {
		Wallet custody.WalletPublic `json:"wallet"`
	}
	if err := json.Unmarshal(createW.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	wid := created.Wallet.ID

	// 2. Request live/fixture Jupiter quote via /api/v1/wallets/{id}/jupiter/quote.
	quoteBody, _ := json.Marshal(map[string]any{
		"inputMint":   onchain.SOL,
		"outputMint":  "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		"amountHuman": "0.5",
		"slippageBps": 50,
	})
	qResp := doReq(http.MethodPost, "/api/v1/wallets/"+wid+"/jupiter/quote", string(quoteBody))
	if qResp.Code != 200 {
		t.Fatalf("wallet jupiter quote failed: %d %s", qResp.Code, qResp.Body.String())
	}
	var qOut JupiterSwapQuoteResponse
	if err := json.Unmarshal(qResp.Body.Bytes(), &qOut); err != nil {
		t.Fatal(err)
	}
	if qOut.InputSymbol != "SOL" || qOut.OutputSymbol != "USDC" || qOut.OutAmountRaw == "" || len(qOut.RouteHops) == 0 {
		t.Fatalf("unexpected jupiter quote output: %+v", qOut)
	}

	// 3. Request via #trade alias /api/v1/trade/jupiter/quote with @wallet:<id>.
	tradeBody, _ := json.Marshal(map[string]any{
		"walletId":    created.Wallet.Handle,
		"inputMint":   "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		"outputMint":  onchain.SOL,
		"amountHuman": "18",
		"slippageBps": 50,
	})
	tqResp := doReq(http.MethodPost, "/api/v1/trade/jupiter/quote", string(tradeBody))
	if tqResp.Code != 200 {
		t.Fatalf("trade jupiter quote failed: %d %s", tqResp.Code, tqResp.Body.String())
	}

	// 4. Slippage exceeding wallet policy MaxSlippageBps (default 50) MUST be rejected!
	badSlipBody, _ := json.Marshal(map[string]any{
		"inputMint":   onchain.SOL,
		"outputMint":  "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		"amountHuman": "0.1",
		"slippageBps": 250,
	})
	badSlipResp := doReq(http.MethodPost, "/api/v1/wallets/"+wid+"/jupiter/quote", string(badSlipBody))
	if badSlipResp.Code != 403 {
		t.Fatalf("expected 403 for slippage > policy cap, got %d %s", badSlipResp.Code, badSlipResp.Body.String())
	}

	// 5. Verify Data Plugin query-string API key redaction (M12).
	redacted := redactedPluginEndpoint("https://mainnet.helius-rpc.com/?api-key=super-secret-helius-key&cluster=mainnet")
	if strings.Contains(redacted, "super-secret-helius-key") || !strings.Contains(redacted, "REDACTED") {
		t.Fatalf("expected api-key query param redacted, got %q", redacted)
	}
	if !bytes.Contains([]byte(redacted), []byte("cluster=mainnet")) {
		t.Fatalf("expected non-secret query params preserved, got %q", redacted)
	}
}
