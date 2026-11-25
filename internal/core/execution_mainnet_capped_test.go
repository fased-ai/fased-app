package core

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/fased-ai/fased-app/internal/cex"
	"github.com/fased-ai/fased-app/internal/equities"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/swarm"
)

func TestStage456MainnetCappedGateAndDefaultOffInvariants(t *testing.T) {
	dir := fixtureDir(t)
	srv, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer srv.Close(context.Background())

	token := claimFixture(t, srv.Store)
	h := srv.HTTP.Handler.(*HTTPHandler)

	// 1. Default runtime keeps health capabilities and Stage 4/5/6 locked OFF and WEN held.
	healthResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/health", "", "", "", "")
	if healthResp.Code != 200 {
		t.Fatalf("GET /api/v1/health status=%d body=%s", healthResp.Code, healthResp.Body.String())
	}
	var hv Health
	if err := json.Unmarshal(healthResp.Body.Bytes(), &hv); err != nil {
		t.Fatalf("decode health failed: %v", err)
	}
	if hv.Capabilities["walletSigner"] || hv.Capabilities["financialExecution"] || hv.Capabilities["updater"] {
		t.Fatalf("default runtime must keep live capabilities false, got %+v", hv.Capabilities)
	}

	execResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/execution/status", "", token, "", "")
	if execResp.Code != 200 {
		t.Fatalf("GET /api/v1/execution/status status=%d", execResp.Code)
	}
	var ev ExecutionStatusView
	if err := json.Unmarshal(execResp.Body.Bytes(), &ev); err != nil {
		t.Fatalf("decode execution status failed: %v", err)
	}
	if ev.Profile != signer.ExecutionProfileOff || !ev.MainnetLocked || !ev.WENHeld || ev.MainnetCapped {
		t.Fatalf("unexpected default execution status: %+v", ev)
	}

	// 2. Mainnet-capped fails closed without confirmation or with >$250 cap.
	badSigner := func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: signer.Status{
			MainnetCapped:           true,
			MainnetMaxOrderMicroUSD: 50_000_000,
			Account:                 &signer.AccountRef{Network: "mainnet-beta", PublicKey: "11111111111111111111111111111111"},
		}}, nil
	}
	if err := srv.ConfigureTestnetExecution(t.Context(), TestnetExecutionConfig{
		Profile:       signer.ExecutionProfileMainnetCapped,
		MainnetPolicy: signer.MainnetCappedPolicy{Confirmed: false, MaxOrderMicroUSD: 50_000_000, MaxDailyMicroUSD: 100_000_000},
		AgentVaultID:  "0123456789abcdef0123456789abcdef",
		SignerCall:    badSigner,
	}); err == nil {
		t.Fatal("expected unconfirmed mainnet-capped config to fail")
	}

	// 3. Valid opt-in mainnet-capped configuration unlocks bounded Stage 4/5 while keeping WEN & Swarm live copy locked.
	if err := srv.ConfigureTestnetExecution(t.Context(), TestnetExecutionConfig{
		Profile: signer.ExecutionProfileMainnetCapped,
		MainnetPolicy: signer.MainnetCappedPolicy{
			Confirmed:        true,
			MaxOrderMicroUSD: 50_000_000,
			MaxDailyMicroUSD: 200_000_000,
		},
		AgentVaultID: "0123456789abcdef0123456789abcdef",
		SignerCall:   badSigner,
	}); err != nil {
		t.Fatalf("valid ConfigureTestnetExecution(mainnet-capped) failed: %v", err)
	}
	defer srv.Store.clearStage4Runtime()

	// Execution status reflects mainnet-capped while wenHeld remains true.
	execResp2 := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/execution/status", "", token, "", "")
	if execResp2.Code != 200 {
		t.Fatalf("GET /api/v1/execution/status after enable status=%d", execResp2.Code)
	}
	var ev2 ExecutionStatusView
	if err := json.Unmarshal(execResp2.Body.Bytes(), &ev2); err != nil {
		t.Fatalf("decode execution status failed: %v", err)
	}
	if ev2.Profile != signer.ExecutionProfileMainnetCapped || ev2.MainnetLocked || !ev2.MainnetCapped || !ev2.WENHeld || ev2.MainnetMaxOrderMicroUSD != 50_000_000 {
		t.Fatalf("unexpected mainnet-capped execution status: %+v", ev2)
	}

	// Stage 5 CEX & Equities reflect capped limits cleanly.
	cexResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/cex/status", "", token, "", "")
	var csv cex.StatusView
	if cexResp.Code != 200 || json.Unmarshal(cexResp.Body.Bytes(), &csv) != nil || csv.MainnetLocked || !csv.CappedMode || csv.MaxOrderMicroUSD != 50_000_000 {
		t.Fatalf("unexpected cex capped status (%d): %s", cexResp.Code, cexResp.Body.String())
	}

	eqResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/equities/status", "", token, "", "")
	var esv equities.StatusView
	if eqResp.Code != 200 || json.Unmarshal(eqResp.Body.Bytes(), &esv) != nil || esv.LiveLocked || !esv.CappedMode || esv.MaxOrderMicroUSD != 50_000_000 {
		t.Fatalf("unexpected equities capped status (%d): %s", eqResp.Code, eqResp.Body.String())
	}

	// Stage 6 Swarm retains LiveCopyLocked=true and WENHeld=true even with CappedMode metadata.
	swResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/swarm/status", "", token, "", "")
	var ssv swarm.StatusView
	if swResp.Code != 200 || json.Unmarshal(swResp.Body.Bytes(), &ssv) != nil || !ssv.LiveCopyLocked || !ssv.WENHeld || !ssv.CappedMode {
		t.Fatalf("unexpected swarm status (%d): %s", swResp.Code, swResp.Body.String())
	}

	// Signer-status reports mainnetLocked:false & wenHeld:true.
	sigResp := stage4Req(h, http.MethodGet, srv.URL, "/api/v1/signer/status", "", token, "", "")
	if sigResp.Code != 200 {
		t.Fatalf("GET /api/v1/signer/status failed: %d", sigResp.Code)
	}
	var ss map[string]any
	_ = json.Unmarshal(sigResp.Body.Bytes(), &ss)
	if ss["mainnetCapped"] != true || ss["mainnetLocked"] != false || ss["wenHeld"] != true {
		t.Fatalf("unexpected signer-status: %+v", ss)
	}
}
