package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/lighter"
	"github.com/fased-ai/fased-app/internal/signer"
)

func TestStage4LighterDefaultOffAndControlledExecution(t *testing.T) {
	t.Parallel()
	offDir := fixtureDir(t)
	srvOff, err := Start(t.Context(), offDir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srvOff.Close(context.Background()) }()
	offToken := claimFixture(t, srvOff.Store)
	offH := srvOff.HTTP.Handler.(*HTTPHandler)

	stResp := stage4Req(offH, "GET", srvOff.URL, "/api/v1/execution/lighter/status", "", offToken, srvOff.URL, "")
	if stResp.Code != 200 {
		t.Fatalf("status returned %d: %s", stResp.Code, stResp.Body.String())
	}
	var stDoc map[string]any
	_ = json.Unmarshal(stResp.Body.Bytes(), &stDoc)
	if stDoc["available"] != false || stDoc["mainnetLocked"] != true || stDoc["wenHeld"] != true {
		t.Fatalf("expected locked unavailable status for lighter, got %+v", stDoc)
	}
	if rResp := stage4Req(offH, "POST", srvOff.URL, "/api/v1/execution/lighter/rehearse", `{}`, offToken, srvOff.URL, csrfToken(offToken)); rResp.Code != 403 {
		t.Fatalf("expected 403 for off profile POST rehearse, got %d", rResp.Code)
	}

	dir := fixtureDir(t)
	signerDir := fixtureDir(t)
	if err := os.Chmod(signerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	signerID := strings.Repeat("c", 32)
	pass := []byte("stage4-lighter-core-passphrase-2026")
	vaultID := strings.Repeat("d", 32)

	v, err := signer.Open(signerDir, signerID)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err := v.Create(pass); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock(pass, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := v.ConfigureTestnetExecution(true); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	if err := v.SetAgentVaultPolicy(signer.AgentVaultPolicy{
		Version:            1,
		VaultID:            vaultID,
		AgentID:            "lighter-core-agent",
		AllowedVenues:      []string{"lighter"},
		AllowedAssets:      []string{"ETH"},
		MaxOrderMicroUSD:   5_000_000, // $5
		DailySpendMicroUSD: 8_000_000, // $8
		DailyLossMicroUSD:  2_000_000,
		MaxLeverage:        4,
		ExpiresAt:          now + 3600,
	}); err != nil {
		t.Fatal(err)
	}

	lClient, err := lighter.NewControlledClient("testnet", stage4VenueControlledRT{fn: func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/orderBooks":
			return venueMockJSON(200, `{"code":200,"order_books":[{"symbol":"ETH","market_id":0,"supported_size_decimals":4,"supported_price_decimals":2}]}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sendTx":
			raw, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(raw))
			if vals.Get("tx_type") != "14" || vals.Get("tx_info") == "" {
				return venueMockJSON(400, `{"code":21701,"message":"bad payload"}`), nil
			}
			return venueMockJSON(200, `{"code":200,"message":"queued","tx_hash":"0xlighter777"}`), nil
		default:
			return venueMockJSON(404, `{}`), nil
		}
	}})
	if err != nil {
		t.Fatal(err)
	}

	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close(context.Background()) }()
	token := claimFixture(t, srv.Store)
	if err := srv.ConfigureTestnetExecution(t.Context(), TestnetExecutionConfig{
		Profile:       signer.ExecutionProfileTestnet,
		SignerCall:    stage4TestSignerRelay(v),
		AgentVaultID:  vaultID,
		LighterClient: lClient,
	}); err != nil {
		t.Fatalf("ConfigureTestnetExecution failed: %v", err)
	}

	h := srv.HTTP.Handler.(*HTTPHandler)
	execResp := stage4Req(h, "GET", srv.URL, "/api/v1/execution/status", "", token, srv.URL, "")
	if execResp.Code != 200 {
		t.Fatalf("GET execution status failed: %d", execResp.Code)
	}
	var execView ExecutionStatusView
	if err := json.Unmarshal(execResp.Body.Bytes(), &execView); err != nil {
		t.Fatal(err)
	}
	if !execView.LighterTestnetReady || len(strings.TrimPrefix(execView.LighterPublicKey, "0x")) != 80 || !execView.MainnetLocked || !execView.WENHeld {
		t.Fatalf("unexpected ExecutionStatusView: %+v", execView)
	}

	// CSRF required.
	noCSRF := stage4Req(h, "POST", srv.URL, "/api/v1/execution/lighter/rehearse", `{}`, token, srv.URL, "")
	if noCSRF.Code != 403 {
		t.Fatalf("expected 403 without CSRF, got %d", noCSRF.Code)
	}

	// Valid $4.50 ETH limit order (0.0025 ETH @ $1800.00) with leverage=3x.
	body := `{"symbol":"ETH","side":"BUY","price":"1800.00","size":"0.0025","leverage":3,"accountIndex":42,"nonce":1,"fetchMarket":true,"submit":true}`
	lResp := stage4Req(h, "POST", srv.URL, "/api/v1/execution/lighter/rehearse", body, token, srv.URL, csrfToken(token))
	if lResp.Code != 200 {
		t.Fatalf("lighter rehearse failed (%d): %s", lResp.Code, lResp.Body.String())
	}
	var res LighterRehearseResult
	if err := json.Unmarshal(lResp.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.SchnorrValid || res.EstimatedMicroUSD != 4_500_000 || res.Submission == nil || !res.Submission.Submitted || res.Submission.TxHash != "0xlighter777" {
		t.Fatalf("unexpected LighterRehearseResult: %+v", res)
	}

	// Over-leverage (6 > 4) rejected cleanly.
	overLev := stage4Req(h, "POST", srv.URL, "/api/v1/execution/lighter/rehearse", `{"symbol":"ETH","side":"BUY","price":"1800.00","size":"0.001","leverage":6,"accountIndex":42,"nonce":2}`, token, srv.URL, csrfToken(token))
	if overLev.Code != 400 || !strings.Contains(overLev.Body.String(), "leverage limit") {
		t.Fatalf("expected 400 leverage limit error, got %d: %s", overLev.Code, overLev.Body.String())
	}

	// Daily spend check: $4.50 spent, next $4.50 ($9.00 > $8.00) rejected cleanly.
	overCap := stage4Req(h, "POST", srv.URL, "/api/v1/execution/lighter/rehearse", `{"symbol":"ETH","side":"BUY","price":"1800.00","size":"0.0025","leverage":2,"accountIndex":42,"nonce":3}`, token, srv.URL, csrfToken(token))
	if overCap.Code != 400 || !strings.Contains(overCap.Body.String(), "daily spend limit exceeded") {
		t.Fatalf("expected 400 daily spend limit exceeded, got %d: %s", overCap.Code, overCap.Body.String())
	}
}
