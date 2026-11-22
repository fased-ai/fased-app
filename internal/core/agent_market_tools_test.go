package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func marketJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestAtomicAmountHelpers(t *testing.T) {
	t.Parallel()

	formatCases := []struct {
		atomic   string
		decimals int
		want     string
	}{
		{"0", 9, "0"},
		{"", 6, "0"},
		{"1000000000", 9, "1"},
		{"1500000000", 9, "1.5"},
		{"1000", 6, "0.001"},
		{"70059912", 6, "70.059912"},
		{"123", 0, "123"},
	}
	for _, tc := range formatCases {
		if got := formatAtomicUnits(tc.atomic, tc.decimals); got != tc.want {
			t.Fatalf("formatAtomicUnits(%q, %d) = %q, want %q", tc.atomic, tc.decimals, got, tc.want)
		}
	}

	parseCases := []struct {
		amount   string
		decimals int
		want     string
		wantErr  bool
	}{
		{"1", 9, "1000000000", false},
		{"1.5", 9, "1500000000", false},
		{"0.001", 6, "1000", false},
		{".25", 6, "250000", false},
		{"0", 6, "", true},
		{"-1", 6, "", true},
		{"1.1234567", 6, "", true},
		{"1e5", 6, "", true},
		{"12345678901234567890", 6, "", true},
	}
	for _, tc := range parseCases {
		got, err := parseDecimalToAtomic(tc.amount, tc.decimals)
		if (err != nil) != tc.wantErr {
			t.Fatalf("parseDecimalToAtomic(%q, %d) err = %v, wantErr=%v", tc.amount, tc.decimals, err, tc.wantErr)
		}
		if !tc.wantErr && got != tc.want {
			t.Fatalf("parseDecimalToAtomic(%q, %d) = %q, want %q", tc.amount, tc.decimals, got, tc.want)
		}
	}
}

func TestPublicMarketTransportGuard(t *testing.T) {
	t.Parallel()
	rt := &publicMarketRoundTripper{
		next: aiTestTransport(func(r *http.Request) (*http.Response, error) {
			return marketJSONResponse(http.StatusOK, `{}`), nil
		}),
	}
	badReqs := []func() *http.Request{
		func() *http.Request {
			req, _ := http.NewRequest(http.MethodPost, jupiterPriceV3URL, nil)
			return req
		},
		func() *http.Request {
			req, _ := http.NewRequest(http.MethodGet, "http://lite-api.jup.ag/price/v3", nil)
			return req
		},
		func() *http.Request {
			req, _ := http.NewRequest(http.MethodGet, "https://127.0.0.1/price", nil)
			return req
		},
		func() *http.Request {
			req, _ := http.NewRequest(http.MethodGet, jupiterPriceV3URL, nil)
			req.Header.Set("Authorization", "Bearer secret")
			return req
		},
	}
	for i, mk := range badReqs {
		if _, err := rt.RoundTrip(mk()); err == nil {
			t.Fatalf("case %d: expected disallowed market request to be rejected", i)
		}
	}
}

func TestGetTokenPriceTool(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)

	// Fixture mode returns deterministic prices with zero network calls.
	fixOut, err := toolGetTokenPrice(t.Context(), s, "", json.RawMessage(`{"tokens":["SOL","USDC","BTC"]}`))
	if err != nil {
		t.Fatal(err)
	}
	fixMap := fixOut.(map[string]any)
	if fixMap["fixtureOnly"] != true {
		t.Fatalf("expected fixtureOnly=true, got %#v", fixMap)
	}
	fixPrices := fixMap["prices"].([]map[string]any)
	if len(fixPrices) != 3 || fixPrices[0]["symbol"] != "SOL" || fixPrices[0]["source"] != "fixture" {
		t.Fatalf("unexpected fixture prices: %#v", fixPrices)
	}

	// Mock live Jupiter Price v3 + CoinGecko responses.
	var seenURLs []string
	unknownMint := "7vfCXTUXx5WJV5JADk17DUJ4ksgau7utNKj4b963voxs"
	s.marketDataTransport = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		seenURLs = append(seenURLs, r.URL.String())
		switch r.URL.Hostname() {
		case "lite-api.jup.ag":
			return marketJSONResponse(http.StatusOK, `{
				"So11111111111111111111111111111111111111112":{"usdPrice":71.25,"priceChange24h":2.4,"liquidity":5000000,"decimals":9}
			}`), nil
		case "api.coingecko.com":
			return marketJSONResponse(http.StatusOK, `{
				"bitcoin":{"usd":61200,"usd_24h_change":-0.75}
			}`), nil
		default:
			t.Fatalf("unexpected host: %s", r.URL.String())
			return nil, nil
		}
	})

	rawArgs, _ := json.Marshal(map[string]any{"tokens": []string{"SOL", "BTC", unknownMint}})
	liveOut, err := toolGetTokenPrice(t.Context(), s, "", rawArgs)
	if err != nil {
		t.Fatal(err)
	}
	liveMap := liveOut.(map[string]any)
	livePrices := liveMap["prices"].([]map[string]any)
	if len(livePrices) != 2 || livePrices[0]["usdPrice"] != 71.25 || livePrices[1]["usdPrice"] != 61200.0 {
		t.Fatalf("unexpected live prices: %#v", livePrices)
	}
	missing := liveMap["missing"].([]string)
	if len(missing) != 1 || missing[0] != unknownMint {
		t.Fatalf("unexpected missing list: %#v", missing)
	}
	if len(seenURLs) != 2 {
		t.Fatalf("expected 2 batched requests (Jupiter + CoinGecko), got %v", seenURLs)
	}

	if _, err := toolGetTokenPrice(t.Context(), s, "", json.RawMessage(`{"token":"NOT_A_REAL_TOKEN"}`)); err == nil {
		t.Fatal("expected unknown token error")
	}

	s.marketDataTransport = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		return marketJSONResponse(http.StatusBadGateway, `{"error":"down"}`), nil
	})
	if _, err := toolGetTokenPrice(t.Context(), s, "", json.RawMessage(`{"token":"SOL"}`)); err == nil {
		t.Fatal("expected upstream error when zero prices resolve")
	}
}

func TestGetSwapQuoteTool(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)

	// Fixture mode quote runs locally without network.
	fixOut, err := toolGetSwapQuote(t.Context(), s, "", json.RawMessage(`{"fromToken":"SOL","toToken":"USDC","amount":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	fixMap := fixOut.(map[string]any)
	if fixMap["inAmount"] != "1000000000" || fixMap["executionEnabled"] != false || fixMap["fixtureOnly"] != true {
		t.Fatalf("unexpected fixture swap quote: %#v", fixMap)
	}

	// Mock live Jupiter swap quote + unknown Base58 mint metadata lookup.
	customMint := "7vfCXTUXx5WJV5JADk17DUJ4ksgau7utNKj4b963voxs"
	s.marketDataTransport = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/price/v3" {
			return marketJSONResponse(http.StatusOK, `{
				"`+customMint+`":{"usdPrice":1.2,"decimals":8}
			}`), nil
		}
		if r.URL.Path == "/swap/v1/quote" {
			q := r.URL.Query()
			if q.Get("inputMint") != "So11111111111111111111111111111111111111112" || q.Get("amount") != "1500000000" || q.Get("slippageBps") != "75" {
				t.Fatalf("unexpected quote query: %v", q)
			}
			outMint := q.Get("outputMint")
			if outMint == "" {
				outMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
			}
			return marketJSONResponse(http.StatusOK, `{
				"inputMint":"So11111111111111111111111111111111111111112",
				"inAmount":"1500000000",
				"outputMint":"`+outMint+`",
				"outAmount":"105089868",
				"otherAmountThreshold":"104307600",
				"swapMode":"ExactIn",
				"slippageBps":75,
				"priceImpactPct":"0.00012",
				"routePlan":[{"swapInfo":{"label":"HumidiFi"},"percent":100}]
			}`), nil
		}
		t.Fatalf("unexpected path: %s", r.URL.Path)
		return nil, nil
	})

	liveOut, err := toolGetSwapQuote(t.Context(), s, "", json.RawMessage(`{"fromToken":"SOL","toToken":"USDC","amount":"1.5","slippageBps":75}`))
	if err != nil {
		t.Fatal(err)
	}
	liveMap := liveOut.(map[string]any)
	if liveMap["inUiAmount"] != "1.5" || liveMap["outUiAmount"] != "105.089868" || liveMap["minOutUiAmount"] != "104.3076" || liveMap["executionEnabled"] != false {
		t.Fatalf("unexpected live swap quote: %#v", liveMap)
	}
	routes := liveMap["routePlan"].([]map[string]any)
	if len(routes) != 1 || routes[0]["label"] != "HumidiFi" {
		t.Fatalf("unexpected routePlan: %#v", routes)
	}

	// Custom mint resolves decimals from Price v3 before quoting.
	customOut, err := toolGetSwapQuote(t.Context(), s, "", json.RawMessage(`{"fromToken":"SOL","toToken":"`+customMint+`","amount":"1.5","slippageBps":75}`))
	if err != nil {
		t.Fatal(err)
	}
	if customOut.(map[string]any)["source"] != "jupiter-lite-v1" {
		t.Fatalf("unexpected custom mint quote: %#v", customOut)
	}

	for _, bad := range []string{
		`{"fromToken":"SOL","toToken":"SOL","amount":"1"}`,
		`{"fromToken":"BTC","toToken":"USDC","amount":"1"}`,
		`{"fromToken":"SOL","toToken":"USDC","amount":"0"}`,
		`{"fromToken":"SOL","toToken":"USDC","amount":"1","slippageBps":9000}`,
	} {
		if _, err := toolGetSwapQuote(t.Context(), s, "", json.RawMessage(bad)); err == nil {
			t.Fatalf("expected error for invalid quote args %s", bad)
		}
	}
}

func TestGetWalletBalancesTool(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)

	// Empty accounts response.
	emptyOut, err := toolGetWalletBalances(t.Context(), s, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if emptyOut.(map[string]any)["count"] != 0 {
		t.Fatalf("expected count=0, got %#v", emptyOut)
	}

	// Seed a watch wallet without RPC -> rpc_not_configured.
	v, err := s.Accounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s.rpcAdmission = true
	acctID := "0123456789abcdef0123456789abcdef"
	v, err = s.UpdateAccounts(t.Context(), AccountsRecord{
		Version:  v.Record.Version,
		Revision: v.Record.Revision,
		Accounts: []Account{{
			Version:   2,
			ID:        acctID,
			Name:      "Trading Watch",
			Mode:      "watch",
			Network:   "devnet",
			PublicKey: strings.Repeat("02", 32),
		}},
		Proposals: []ProposalDraft{},
	})
	if err != nil {
		t.Fatal(err)
	}
	noRPCOut, err := toolGetWalletBalances(t.Context(), s, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if noRPCOut.(map[string]any)["status"] != "rpc_not_configured" {
		t.Fatalf("expected rpc_not_configured, got %#v", noRPCOut)
	}

	// Configure public devnet RPC endpoint and exercise both fixture fallback and ReadWallet fixture.
	v, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{
		AccountID: acctID,
		Revision:  v.Record.Revision,
		Endpoint:  "https://api.devnet.solana.com",
		Network:   "devnet",
	})
	if err != nil {
		t.Fatal(err)
	}

	s.rpcAdmission = false
	fixOut, err := toolGetWalletBalances(t.Context(), s, "", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if fixOut.(map[string]any)["status"] != "fixture_mode" {
		t.Fatalf("expected fixture_mode, got %#v", fixOut)
	}

	s.walletReadFixture = func(_ context.Context, a Account, addr string, _ []byte, _ func() error, _ *WalletSelection) (WalletSnapshot, error) {
		return WalletSnapshot{
			Version:   2,
			AccountID: a.ID,
			Revision:  v.Record.Revision,
			Address:   addr,
			Network:   a.Network,
			Endpoint:  a.RPC.Endpoint,
			Slot:      "420001",
			Lamports:  "2500000000",
			Tokens: []WalletTokenBalance{
				{Program: "spl-token", Mint: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", Amount: "12500000", Decimals: 6},
				{Program: token2022Program, Mint: "So11111111111111111111111111111111111111112", Amount: "500000000", Decimals: 9},
			},
			Activity:  []WalletActivity{{Signature: "sig1", Slot: "420000"}},
			FetchedAt: 1795300000,
			LiveRPC:   true,
		}, nil
	}
	balOut, err := toolGetWalletBalances(t.Context(), s, "", json.RawMessage(`{"accountId":"Trading Watch"}`))
	if err != nil {
		t.Fatal(err)
	}
	balMap := balOut.(map[string]any)
	if balMap["sol"] != "2.5" || balMap["recentActivityCount"] != 1 || balMap["signingEnabled"] != false {
		t.Fatalf("unexpected wallet balance output: %#v", balMap)
	}
	toks := balMap["tokens"].([]map[string]any)
	if len(toks) != 2 || toks[0]["symbol"] != "USDC" || toks[0]["uiAmount"] != "12.5" {
		t.Fatalf("unexpected formatted tokens: %#v", toks)
	}
}

func TestAgentV2MarketToolsLoop(t *testing.T) {
	t.Parallel()
	s := fixtureStore(t)
	conn := saveTestConnection(t, s)

	s.marketDataTransport = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/price/v3":
			return marketJSONResponse(http.StatusOK, `{"So11111111111111111111111111111111111111112":{"usdPrice":70.5,"priceChange24h":1.9,"decimals":9}}`), nil
		case "/swap/v1/quote":
			return marketJSONResponse(http.StatusOK, `{"inputMint":"So11111111111111111111111111111111111111112","inAmount":"1000000000","outputMint":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v","outAmount":"70450000","otherAmountThreshold":"70097750","swapMode":"ExactIn","slippageBps":50,"priceImpactPct":"0.0001","routePlan":[{"swapInfo":{"label":"HumidiFi"},"percent":100}]}`), nil
		default:
			t.Fatalf("unexpected market path: %s", r.URL.Path)
			return nil, nil
		}
	})

	var mu sync.Mutex
	round := 0
	s.agentV2TransportOverride = aiTestTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		round++
		step := round
		mu.Unlock()
		switch step {
		case 1:
			return sseHTTPResponse(
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"resp_mkt_1","output":[{"type":"function_call","id":"fc_p","call_id":"call_p","name":"get_token_price","arguments":"{\"tokens\":[\"SOL\"]}"},{"type":"function_call","id":"fc_q","call_id":"call_q","name":"get_swap_quote","arguments":"{\"fromToken\":\"SOL\",\"toToken\":\"USDC\",\"amount\":\"1\"}"}],"usage":{"input_tokens":20,"output_tokens":12}}}`,
			), nil
		case 2:
			return sseHTTPResponse(
				`event: response.output_text.delta`,
				`data: {"type":"response.output_text.delta","delta":"SOL is $70.50 and 1 SOL quotes 70.45 USDC."}`,
				``,
				`event: response.completed`,
				`data: {"type":"response.completed","response":{"id":"resp_mkt_2","output":[],"usage":{"input_tokens":95,"output_tokens":14}}}`,
			), nil
		default:
			t.Fatalf("unexpected extra round %d", step)
			return nil, nil
		}
	})

	thread, err := s.CreateThread(t.Context(), "trading-analyst", "Market check", conn.Ref, "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.RunAgentV2Turn(t.Context(), thread.ID, AgentV2PromptInput{Content: "Price and quote SOL"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.AwaitingApproval || res.LastMessage == nil || !strings.Contains(res.LastMessage.Content, "70.45 USDC") {
		t.Fatalf("unexpected market tool loop outcome: %+v", res)
	}
	if len(res.Detail.Messages[1].ToolCalls) != 2 {
		t.Fatalf("expected 2 completed Tier-1 market tool calls, got %#v", res.Detail.Messages[1].ToolCalls)
	}
}

type marketTransportFunc func(*http.Request) (*http.Response, error)

func (f marketTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMarketToolsSafetyFilteringAndRetry(t *testing.T) {
	t.Parallel()

	// 1. isBase58Mint requires valid 32-byte decoded key, not just 32-44 base58 chars.
	if !isBase58Mint("So11111111111111111111111111111111111111112") || !isBase58Mint("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v") {
		t.Fatal("expected valid Solana mints accepted")
	}
	if isBase58Mint(strings.Repeat("2", 32)) || isBase58Mint(strings.Repeat("z", 44)) || isBase58Mint("So1111111111111111111111111111111111111111O") {
		t.Fatal("expected invalid decoded-length or non-base58 strings rejected")
	}

	// 2. toolGetWalletBalances filters zero balances, orders known first, caps at 25, reports tokenCount/tokensTruncated.
	s := fixtureStore(t)
	s.rpcAdmission = true
	acctID := strings.Repeat("d", 32)
	rec, err := s.accountsRecord(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rec.Accounts = append(rec.Accounts, Account{
		Version:   2,
		ID:        acctID,
		Name:      "Watch Wallet",
		Mode:      "watch",
		Network:   "devnet",
		PublicKey: strings.Repeat("02", 32),
	})
	vRec, err := s.UpdateAccounts(t.Context(), rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfigureAccountRPC(t.Context(), RPCConfigInput{
		AccountID: acctID,
		Revision:  vRec.Record.Revision,
		Endpoint:  "https://api.devnet.solana.com",
		Network:   "devnet",
	}); err != nil {
		t.Fatal(err)
	}
	tokens := make([]WalletTokenBalance, 0, 32)
	tokens = append(tokens,
		WalletTokenBalance{Mint: "ZeroMint1111111111111111111111111111111111", Amount: "0", Decimals: 6, Program: "spl-token"},
		WalletTokenBalance{Mint: "EmptyMint111111111111111111111111111111111", Amount: "0000", Decimals: 6, Program: "spl-token"},
	)
	for i := 0; i < 28; i++ {
		tokens = append(tokens, WalletTokenBalance{
			Mint:     "UnkMint" + strings.Repeat(string(rune('a'+i%26)), 36),
			Amount:   "5000",
			Decimals: 6,
			Program:  "spl-token",
		})
	}
	// Put known USDC late in input to verify known tokens sort ahead of unknown tokens.
	tokens = append(tokens, WalletTokenBalance{
		Mint:     "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		Amount:   "2500000",
		Decimals: 6,
		Program:  "spl-token",
	})
	s.walletReadFixture = func(_ context.Context, a Account, addr string, _ []byte, _ func() error, _ *WalletSelection) (WalletSnapshot, error) {
		return WalletSnapshot{
			Version:   2,
			AccountID: a.ID,
			Network:   a.Network,
			Address:   addr,
			Slot:      "42",
			Lamports:  "1000000000",
			Tokens:    tokens,
		}, nil
	}
	balOut, err := toolGetWalletBalances(t.Context(), s, "", json.RawMessage(`{"accountId":"`+acctID+`"}`))
	if err != nil {
		t.Fatalf("toolGetWalletBalances failed: %v", err)
	}
	balMap := balOut.(map[string]any)
	gotTokens := balMap["tokens"].([]map[string]any)
	if len(gotTokens) != 25 || balMap["tokenCount"] != 29 || balMap["tokensTruncated"] != true {
		t.Fatalf("unexpected wallet balance capping/counts: len=%d count=%v truncated=%v", len(gotTokens), balMap["tokenCount"], balMap["tokensTruncated"])
	}
	if gotTokens[0]["symbol"] != "USDC" {
		t.Fatalf("expected known token USDC prioritized first, got %#v", gotTokens[0])
	}

	// 3. resolveSolanaSwapToken rejects missing decimals field in Jupiter Price v3 response.
	const customMint = "9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin"
	s2 := fixtureStore(t)
	s2.providerFixture = false
	s2.marketDataTransport = marketTransportFunc(func(r *http.Request) (*http.Response, error) {
		return marketJSONResponse(http.StatusOK, `{"`+customMint+`":{"usdPrice":1.25}}`), nil
	})
	if _, err := resolveSolanaSwapToken(t.Context(), newPublicMarketClient(s2), customMint, false); err == nil {
		t.Fatal("expected resolveSolanaSwapToken to reject omitted decimals field")
	}

	// 4. toolGetSwapQuote validates InputMint and InAmount against request.
	s2.marketDataTransport = marketTransportFunc(func(r *http.Request) (*http.Response, error) {
		return marketJSONResponse(http.StatusOK, `{
			"inputMint":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
			"inAmount":"1000000000",
			"outputMint":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
			"outAmount":"70000000"
		}`), nil
	})
	if _, err := toolGetSwapQuote(t.Context(), s2, "", json.RawMessage(`{"fromToken":"SOL","toToken":"USDC","amount":"1"}`)); err == nil || !strings.Contains(err.Error(), "input mint mismatch") {
		t.Fatalf("expected input mint mismatch error, got %v", err)
	}
	s2.marketDataTransport = marketTransportFunc(func(r *http.Request) (*http.Response, error) {
		return marketJSONResponse(http.StatusOK, `{
			"inputMint":"So11111111111111111111111111111111111111112",
			"inAmount":"999",
			"outputMint":"EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
			"outAmount":"70000000"
		}`), nil
	})
	if _, err := toolGetSwapQuote(t.Context(), s2, "", json.RawMessage(`{"fromToken":"SOL","toToken":"USDC","amount":"1"}`)); err == nil || !strings.Contains(err.Error(), "input amount mismatch") {
		t.Fatalf("expected input amount mismatch error, got %v", err)
	}

	// 5. fetchMarketJSON retries once on HTTP 429 and succeeds.
	var mu sync.Mutex
	attempts := 0
	client := &http.Client{Transport: marketTransportFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n == 1 {
			return marketJSONResponse(http.StatusTooManyRequests, `{"error":"rate limited"}`), nil
		}
		return marketJSONResponse(http.StatusOK, `{"ok":true}`), nil
	})}
	var payload map[string]bool
	if err := fetchMarketJSON(t.Context(), client, "https://lite-api.jup.ag/price/v3", &payload); err != nil || !payload["ok"] || attempts != 2 {
		t.Fatalf("expected single 429 retry to succeed in 2 attempts, attempts=%d payload=%v err=%v", attempts, payload, err)
	}
}
