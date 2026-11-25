package lighter

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
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
		"api.testnet.lighter.xyz:443",
		func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("dial invoked for loopback")
			return nil, nil
		},
	)
	if err == nil {
		t.Fatal("expected loopback denial")
	}
}

func TestBuildCreateOrderIntentAndMainnetLock(t *testing.T) {
	t.Parallel()
	now := int64(1_760_000_000)
	meta := OrderBookMeta{
		Symbol:                 "ETH",
		MarketID:               0,
		SupportedSizeDecimals:  4,
		SupportedPriceDecimals: 2,
	}
	// 0.0150 ETH @ $2000.00 = $30.00 = 30_000_000 microUSD; base=150, price=200000
	intent, err := BuildCreateOrderIntent(OrderDraft{
		Symbol:       "ETH",
		Side:         "BUY",
		Price:        "2000.00",
		Size:         "0.015",
		AccountIndex: 42,
		Nonce:        5,
	}, meta, now)
	if err != nil {
		t.Fatalf("BuildCreateOrderIntent failed: %v", err)
	}
	if intent.BaseAmount != 150 || intent.Price != 200000 || intent.EstimatedMicroUSD != 30_000_000 || intent.ChainID != signer.LighterTestnetChainID {
		t.Fatalf("unexpected intent: %+v", intent)
	}

	// Mainnet read-only client MUST refuse SubmitSignedTx fail closed.
	ro := NewReadOnlyMainnetClient()
	_, err = ro.SubmitSignedTx(t.Context(), &signer.LighterSignedTx{
		Network:    "mainnet",
		ChainID:    signer.LighterMainnetChainID,
		TxType:     signer.LighterTxTypeCreateOrder,
		TxInfoJSON: `{}`,
	})
	if !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("expected ErrExecutionDisabled for mainnet submit, got %v", err)
	}
}

func TestControlledOrderBooksAndSignedSubmission(t *testing.T) {
	t.Parallel()
	var postedTxType, postedTxInfo string
	c, err := NewControlledClient("testnet", mockControlledTransport{fn: func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/orderBooks":
			return jsonResponse(200, `{"code":200,"order_books":[{"symbol":"ETH","market_id":0,"market_type":"perp","status":"active","supported_size_decimals":4,"supported_price_decimals":2}]}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/nextNonce":
			return jsonResponse(200, `{"code":200,"nonce":11}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sendTx":
			raw, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(raw))
			postedTxType = vals.Get("tx_type")
			postedTxInfo = vals.Get("tx_info")
			if postedTxType != "14" || !strings.Contains(postedTxInfo, `"AccountIndex":42`) {
				return jsonResponse(400, `{"code":21701,"message":"bad tx"}`), nil
			}
			return jsonResponse(200, `{"code":200,"message":"queued","tx_hash":"0xabc123"}`), nil
		default:
			return jsonResponse(404, `{}`), nil
		}
	}})
	if err != nil {
		t.Fatal(err)
	}

	books, err := c.FetchOrderBooks(t.Context())
	if err != nil || len(books) != 1 || books[0].Symbol != "ETH" {
		t.Fatalf("FetchOrderBooks unexpected: %+v err=%v", books, err)
	}
	nonce, err := c.FetchNextNonce(t.Context(), 42, 0)
	if err != nil || nonce != 11 {
		t.Fatalf("FetchNextNonce unexpected: nonce=%d err=%v", nonce, err)
	}

	now := time.Now().Unix()
	intent, err := BuildCreateOrderIntent(OrderDraft{
		Symbol:       "ETH",
		Side:         "BUY",
		Price:        "1800.00",
		Size:         "0.002",
		AccountIndex: 42,
		Nonce:        nonce,
	}, books[0], now)
	if err != nil {
		t.Fatal(err)
	}
	sk, err := signer.DeriveLighterScalarFromSeed([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	defer sk.SetInt64(0)
	pk := signer.LighterPubKeyFromScalar(sk)
	txDigest, txLimbs, err := signer.LighterCreateOrderHash(intent.ChainID, intent.Nonce, intent.ExpiredAt, intent.AccountIndex, intent.ApiKeyIndex, intent.MarketIndex, intent.ClientOrderIndex, intent.BaseAmount, intent.Price, intent.IsAsk, intent.OrderType, intent.TimeInForce, intent.ReduceOnly, intent.TriggerPrice, intent.OrderExpiry)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signer.LighterSignHashedMessage(txLimbs, sk)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.LighterVerifySignature(pk[:], txDigest[:], sig[:]); err != nil {
		t.Fatal(err)
	}

	res, err := c.SubmitSignedTx(t.Context(), &signer.LighterSignedTx{
		Network:      "testnet",
		ChainID:      signer.LighterTestnetChainID,
		TxType:       signer.LighterTxTypeCreateOrder,
		PublicKeyHex: "0x" + hex.EncodeToString(pk[:]),
		TxHashHex:    "0x" + hex.EncodeToString(txDigest[:]),
		SignatureHex: "0x" + hex.EncodeToString(sig[:]),
		TxInfoJSON:   `{"AccountIndex":42,"MarketIndex":0}`,
		Nonce:        nonce,
	})
	if err != nil || !res.Submitted || res.TxHash != "0xabc123" || postedTxType != "14" || postedTxInfo == "" {
		t.Fatalf("SubmitSignedTx unexpected: res=%+v err=%v", res, err)
	}
}
