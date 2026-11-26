package core

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/providers"
)

func TestWaveS1DraftTradeOrderStrictlyPaperOnly(t *testing.T) {
	// C3: Supplying live accountId or executionMode MUST be rejected by DisallowUnknownFields.
	evilArgs := json.RawMessage(`{"venue":"solana","symbol":"SOL-USD","side":"buy","quantity":1.5,"accountId":"@wallet:w-live","executionMode":"live"}`)
	if _, err := previewDraftTradeOrder(t.Context(), nil, "", evilArgs); err == nil {
		t.Fatal("expected previewDraftTradeOrder to reject live accountId/executionMode fields")
	}
	if _, err := executeDraftTradeOrder(t.Context(), nil, "", evilArgs); err == nil {
		t.Fatal("expected executeDraftTradeOrder to reject live accountId/executionMode fields")
	}

	// Valid paper order succeeds and reflects actual account/execution in Summary.
	goodArgs := json.RawMessage(`{"venue":"hyperliquid","symbol":"BTC-PERP","side":"buy","quantity":0.1,"strategy":"market"}`)
	draft, err := previewDraftTradeOrder(t.Context(), nil, "", goodArgs)
	if err != nil {
		t.Fatalf("valid draft_trade_order failed: %v", err)
	}
	if !strings.Contains(draft.Summary, "account=paper") || !strings.Contains(draft.Summary, "execution=paper") {
		t.Fatalf("unexpected draft summary: %q", draft.Summary)
	}
}

func TestWaveS1CLIBinaryAndLaunchArgsAllowlist(t *testing.T) {
	// C6: Arbitrary host binary path MUST be rejected.
	for _, bad := range []string{"/bin/sh", "/usr/bin/python3", "/tmp/evil", "/usr/local/bin/claude"} {
		if providers.ValidCustomCLIBinaryPathForVendor(bad, "codex") {
			t.Fatalf("expected arbitrary/mismatched binary %q rejected for codex", bad)
		}
	}
	if !providers.ValidCustomCLIBinaryPathForVendor("/usr/local/bin/codex", "codex") {
		t.Fatal("expected matching /usr/local/bin/codex accepted for codex")
	}

	// C6: Dangerous Claude/Codex flags outside strict allowlist MUST be rejected.
	for _, badFlag := range []string{
		"--settings /tmp/hooks.json",
		"--plugin-dir /tmp/plug",
		"--agents /tmp/a.json",
		"--setting-sources user",
		"--unknown-exec-flag foo",
	} {
		if _, err := providers.ParseSafeCLILaunchArgs(badFlag); err == nil {
			t.Fatalf("expected dangerous flag %q rejected", badFlag)
		}
	}
}

func TestWaveS1DataPluginHostStrictSuffixes(t *testing.T) {
	// H9: Substring lookalikes and HTTP MUST NOT receive Helius/Alchemy API keys.
	badHelius := []string{"https://helius.attacker.example/", "https://my-helius-rpc.com/", "http://mainnet.helius-rpc.com/"}
	for _, raw := range badHelius {
		u, _ := url.Parse(raw)
		if isTrustedHeliusHost(u) {
			t.Fatalf("expected %q rejected as Helius host", raw)
		}
	}
	uGoodH, _ := url.Parse("https://mainnet.helius-rpc.com/")
	if !isTrustedHeliusHost(uGoodH) {
		t.Fatal("expected https://mainnet.helius-rpc.com/ accepted")
	}

	badAlchemy := []string{"https://alchemy.attacker.example/", "https://evil-g.alchemy.com/", "http://solana-mainnet.g.alchemy.com/"}
	for _, raw := range badAlchemy {
		u, _ := url.Parse(raw)
		if isTrustedAlchemyHost(u) {
			t.Fatalf("expected %q rejected as Alchemy host", raw)
		}
	}
	uGoodA, _ := url.Parse("https://solana-mainnet.g.alchemy.com/")
	if !isTrustedAlchemyHost(uGoodA) {
		t.Fatal("expected https://solana-mainnet.g.alchemy.com/ accepted")
	}
}
