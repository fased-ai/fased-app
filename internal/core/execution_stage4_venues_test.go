package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/kalshi"
	"github.com/fased-ai/fased-app/internal/polymarket"
	"github.com/fased-ai/fased-app/internal/signer"
)

type stage4VenueControlledRT struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m stage4VenueControlledRT) ControlledFixture() bool { return true }
func (m stage4VenueControlledRT) RoundTrip(r *http.Request) (*http.Response, error) {
	return m.fn(r)
}

func venueMockJSON(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestStage4PredictionVenuesDefaultOffAndControlledExecution(t *testing.T) {
	t.Parallel()
	offDir := fixtureDir(t)
	srvOff, err := Start(t.Context(), offDir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srvOff.Close(context.Background()) }()
	offToken := claimFixture(t, srvOff.Store)
	offH := srvOff.HTTP.Handler.(*HTTPHandler)

	// Default off reports unavailable and refuses rehearse mutations.
	for _, path := range []string{"/api/v1/execution/polymarket/status", "/api/v1/execution/kalshi/status"} {
		stResp := stage4Req(offH, "GET", srvOff.URL, path, "", offToken, srvOff.URL, "")
		if stResp.Code != 200 {
			t.Fatalf("%s returned %d: %s", path, stResp.Code, stResp.Body.String())
		}
		var doc map[string]any
		_ = json.Unmarshal(stResp.Body.Bytes(), &doc)
		if doc["available"] != false || doc["mainnetLocked"] != true || doc["wenHeld"] != true {
			t.Fatalf("expected locked unavailable status for %s, got %+v", path, doc)
		}
	}
	for _, path := range []string{"/api/v1/execution/polymarket/rehearse", "/api/v1/execution/kalshi/rehearse"} {
		rResp := stage4Req(offH, "POST", srvOff.URL, path, `{}`, offToken, srvOff.URL, csrfToken(offToken))
		if rResp.Code != 403 {
			t.Fatalf("expected 403 for off profile POST %s, got %d", path, rResp.Code)
		}
	}

	// Controlled testnet activation for both Polymarket Amoy & Kalshi Demo.
	dir := fixtureDir(t)
	signerDir := fixtureDir(t)
	if err := os.Chmod(signerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	signerID := strings.Repeat("5", 32)
	pass := []byte("stage4-prediction-venues-2026")
	vaultID := strings.Repeat("6", 32)

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
	kSt, err := v.GenerateKalshiDemoKey("stage4-kalshi-demo")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	if err := v.SetAgentVaultPolicy(signer.AgentVaultPolicy{
		Version:            1,
		VaultID:            vaultID,
		AgentID:            "prediction-agent",
		AllowedVenues:      []string{"polymarket", "kalshi"},
		AllowedAssets:      []string{"USDC", "USD"},
		MaxOrderMicroUSD:   5_000_000, // $5
		DailySpendMicroUSD: 7_000_000, // $7 shared daily cap
		DailyLossMicroUSD:  2_000_000,
		MaxLeverage:        1,
		ExpiresAt:          now + 3600,
	}); err != nil {
		t.Fatal(err)
	}

	pmClient, err := polymarket.NewControlledClient("amoy", stage4VenueControlledRT{fn: func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/book"):
			return venueMockJSON(200, `{"asset_id":"777","bids":[{"price":"0.39","size":"10"}],"asks":[{"price":"0.40","size":"10"}]}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/order":
			if r.Header.Get("POLY_SIGNATURE") == "" {
				return venueMockJSON(401, `{"errorMsg":"missing signature"}`), nil
			}
			return venueMockJSON(200, `{"success":true,"orderID":"pm-live-1","status":"live"}`), nil
		default:
			return venueMockJSON(404, `{}`), nil
		}
	}})
	if err != nil {
		t.Fatal(err)
	}

	kClient, err := kalshi.NewControlledClient("demo", stage4VenueControlledRT{fn: func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/orderbook"):
			return venueMockJSON(200, `{"orderbook_fp":{"yes_dollars":[["0.4000","20.00"]],"no_dollars":[["0.6000","20.00"]]}}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/trade-api/v2/portfolio/events/orders":
			if r.Header.Get("KALSHI-ACCESS-SIGNATURE") == "" || r.Header.Get("KALSHI-ACCESS-KEY") != kSt.KeyID {
				return venueMockJSON(401, `{"code":"unauthenticated"}`), nil
			}
			return venueMockJSON(201, `{"order":{"order_id":"k-live-1","client_order_id":"c1","status":"resting"}}`), nil
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
		Profile:          signer.ExecutionProfileTestnet,
		SignerCall:       stage4TestSignerRelay(v),
		AgentVaultID:     vaultID,
		PolymarketClient: pmClient,
		KalshiClient:     kClient,
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
	if !execView.PolymarketTestnetReady || execView.PolymarketSignerAddress == "" || !execView.KalshiDemoReady || !execView.KalshiKeyConfigured || execView.KalshiKeyID != kSt.KeyID || !execView.MainnetLocked || !execView.WENHeld {
		t.Fatalf("unexpected ExecutionStatusView: %+v", execView)
	}

	// CSRF required on POST rehearse.
	noCSRF := stage4Req(h, "POST", srv.URL, "/api/v1/execution/polymarket/rehearse", `{}`, token, srv.URL, "")
	if noCSRF.Code != 403 {
		t.Fatalf("expected 403 without CSRF, got %d", noCSRF.Code)
	}

	// Polymarket rehearse ($4.00 = 10 shares @ 0.40) + controlled L2 credentials.
	pmBody := `{"tokenId":"777","side":"BUY","price":"0.40","shares":"10","fetchBook":true,"submit":true,"credentials":{"address":"` + execView.PolymarketSignerAddress + `","apiKey":"demo-k","secret":"c2VjcmV0LWtleQ==","passphrase":"demo-p"}}`
	pmResp := stage4Req(h, "POST", srv.URL, "/api/v1/execution/polymarket/rehearse", pmBody, token, srv.URL, csrfToken(token))
	if pmResp.Code != 200 {
		t.Fatalf("polymarket rehearse failed (%d): %s", pmResp.Code, pmResp.Body.String())
	}
	var pmRes PolymarketRehearseResult
	if err := json.Unmarshal(pmResp.Body.Bytes(), &pmRes); err != nil {
		t.Fatal(err)
	}
	if !pmRes.EcrecoverValid || pmRes.NotionalMicroUSD != 4_000_000 || pmRes.BookBestBid != "0.39" || pmRes.Submission == nil || !pmRes.Submission.Submitted || pmRes.Submission.OrderID != "pm-live-1" {
		t.Fatalf("unexpected PolymarketRehearseResult: %+v", pmRes)
	}

	// Kalshi detached POST order signing MUST fail closed until signer-owned HTTPS egress exists.
	kBody := `{"ticker":"KXBTC-26","side":"bid","count":"5","price":"0.4000","fetchBook":true,"submit":true}`
	kResp := stage4Req(h, "POST", srv.URL, "/api/v1/execution/kalshi/rehearse", kBody, token, srv.URL, csrfToken(token))
	if kResp.Code != 400 || !strings.Contains(kResp.Body.String(), "signer-owned HTTPS egress required") {
		t.Fatalf("expected Kalshi detached POST order rehearse to fail closed, got %d: %s", kResp.Code, kResp.Body.String())
	}

	// AgentVault usage shows $4.00 spent on Polymarket; next $4.00 Polymarket order exceeds $7.00 daily cap.
	vResp := stage4Req(h, "GET", srv.URL, "/api/v1/vaults", "", token, srv.URL, "")
	if vResp.Code != 200 || !strings.Contains(vResp.Body.String(), `"dailySpentMicroUsd":"4000000"`) {
		t.Fatalf("expected 4000000 daily spend in /api/v1/vaults (%d): %s", vResp.Code, vResp.Body.String())
	}
	overCap := stage4Req(h, "POST", srv.URL, "/api/v1/execution/polymarket/rehearse", `{"tokenId":"777","side":"BUY","price":"0.40","shares":"10"}`, token, srv.URL, csrfToken(token))
	if overCap.Code != 400 || !strings.Contains(overCap.Body.String(), "daily spend limit exceeded") {
		t.Fatalf("expected 400 daily spend limit exceeded, got %d: %s", overCap.Code, overCap.Body.String())
	}
}
