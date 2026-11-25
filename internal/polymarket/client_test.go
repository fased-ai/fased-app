package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

type mockControlledTransport struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m mockControlledTransport) ControlledFixture() bool { return true }
func (m mockControlledTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return m.fn(r)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestSSRFPrivateIPDenied(t *testing.T) {
	t.Parallel()
	_, err := dialPublicAddress(
		t.Context(),
		"tcp",
		"clob.polymarket.com:443",
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("dial invoked for loopback address")
			return nil, nil
		},
	)
	if err == nil {
		t.Fatal("expected loopback denial")
	}
}

func TestBuildOrderIntentAndL2AuthHeaders(t *testing.T) {
	t.Parallel()
	now := int64(1_760_000_000)

	// Valid BUY: 10 shares @ $0.45 => maker=4_500_000 USDC, taker=10_000_000 shares
	buyIntent, err := BuildOrderIntent(OrderDraft{
		TokenID: "1234567890",
		Side:    "BUY",
		Price:   "0.45",
		Shares:  "10",
		Salt:    "42",
	}, now)
	if err != nil {
		t.Fatalf("BuildOrderIntent BUY failed: %v", err)
	}
	if buyIntent.ChainID != signer.PolymarketAmoyChainID || buyIntent.MakerAmount != "4500000" || buyIntent.TakerAmount != "10000000" || buyIntent.Side != 0 {
		t.Fatalf("unexpected BUY intent: %+v", buyIntent)
	}

	// Valid SELL: 5 shares @ $0.62 => maker=5_000_000 shares, taker=3_100_000 USDC
	sellIntent, err := BuildOrderIntent(OrderDraft{
		TokenID: "1234567890",
		Side:    "SELL",
		Price:   "0.62",
		Shares:  "5",
		Salt:    "43",
	}, now)
	if err != nil {
		t.Fatalf("BuildOrderIntent SELL failed: %v", err)
	}
	if sellIntent.MakerAmount != "5000000" || sellIntent.TakerAmount != "3100000" || sellIntent.Side != 1 {
		t.Fatalf("unexpected SELL intent: %+v", sellIntent)
	}

	// Out-of-bounds probability price rejected.
	for _, badPx := range []string{"0", "0.0005", "1.0", "1.05"} {
		if _, err := BuildOrderIntent(OrderDraft{TokenID: "1", Side: "BUY", Price: badPx, Shares: "5"}, now); err == nil {
			t.Fatalf("expected invalid price rejection for %q", badPx)
		}
	}

	// Deterministic L2 HMAC-SHA256 headers.
	headers, err := BuildL2AuthHeaders(L2Credentials{
		Address:    "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
		APIKey:     "api-key-1",
		Secret:     "c2VjcmV0LWtleS1iYXNlNjQ=",
		Passphrase: "pass",
	}, 1_760_000_000, "POST", "/order", `{"ok":true}`)
	if err != nil {
		t.Fatalf("BuildL2AuthHeaders failed: %v", err)
	}
	if headers["POLY_SIGNATURE"] == "" || headers["POLY_TIMESTAMP"] != "1760000000" || headers["POLY_ADDRESS"] != "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266" {
		t.Fatalf("unexpected L2 headers: %+v", headers)
	}
}

func TestMainnetLockedAndControlledRoundTrip(t *testing.T) {
	t.Parallel()
	liveTestnet := NewTestnetExecutionClient()
	_, err := liveTestnet.SubmitSignedOrder(
		t.Context(),
		signer.PolymarketOrderIntent{Network: "amoy", ChainID: signer.PolymarketAmoyChainID},
		&signer.PolymarketSignedOrder{SignatureHex: "0xdeadbeef"},
		"GTC",
		L2Credentials{Address: "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266", APIKey: "k", Secret: "c2VjcmV0", Passphrase: "p"},
	)
	if !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("expected ErrExecutionDisabled on live origin submit while locked, got %v", err)
	}

	var gotSig string
	c, err := NewControlledClient("amoy", mockControlledTransport{fn: func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/book"):
			return jsonResponse(200, `{"asset_id":"1001","bids":[{"price":"0.45","size":"20"}],"asks":[{"price":"0.47","size":"15"}]}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/order":
			gotSig = r.Header.Get("POLY_SIGNATURE")
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["owner"] != "demo-key" {
				return jsonResponse(400, `{"errorMsg":"bad owner"}`), nil
			}
			return jsonResponse(200, `{"success":true,"orderID":"pm-ord-123","status":"live"}`), nil
		case r.Method == http.MethodDelete && r.URL.Path == "/order":
			return jsonResponse(200, `{"canceled":["pm-ord-123"]}`), nil
		default:
			return jsonResponse(404, `{}`), nil
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	book, err := c.FetchOrderBook(t.Context(), "1001")
	if err != nil || len(book.Bids) != 1 || book.Bids[0].Price != "0.45" {
		t.Fatalf("FetchOrderBook unexpected: book=%+v err=%v", book, err)
	}
	intent, err := BuildOrderIntent(OrderDraft{TokenID: "1001", Side: "BUY", Price: "0.45", Shares: "10", Salt: "77"}, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	signed := &signer.PolymarketSignedOrder{
		Network:           "amoy",
		ChainID:           signer.PolymarketAmoyChainID,
		VerifyingContract: signer.PolymarketAmoyExchangeContract,
		SignerAddress:     "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
		MakerAddress:      "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
		TakerAddress:      "0x0000000000000000000000000000000000000000",
		SignatureHex:      "0x" + strings.Repeat("11", 65),
	}
	creds := L2Credentials{Address: signed.SignerAddress, APIKey: "demo-key", Secret: "c2VjcmV0LWtleQ==", Passphrase: "pw"}
	subRes, err := c.SubmitSignedOrder(t.Context(), intent, signed, "GTC", creds)
	if err != nil || !subRes.Submitted || subRes.OrderID != "pm-ord-123" || gotSig == "" {
		t.Fatalf("SubmitSignedOrder unexpected: res=%+v err=%v sig=%q", subRes, err, gotSig)
	}
	canRes, err := c.CancelOrder(t.Context(), "pm-ord-123", creds)
	if err != nil || !canRes.Submitted {
		t.Fatalf("CancelOrder unexpected: res=%+v err=%v", canRes, err)
	}
}
