package cex

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestCEXCompileTimeLockAndPermissions(t *testing.T) {
	t.Parallel()
	if CEXActivationEnabled {
		t.Fatal("CEXActivationEnabled must remain false")
	}

	for _, badPerm := range []string{
		"withdraw",
		"enable_withdrawals",
		"universal_transfer",
		"internal_transfer",
		"payout_send",
		"earn_redeem",
	} {
		if _, err := VerifyTradeOnlyPermissions(VenueBinance, []string{"trade", badPerm}); !errors.Is(err, ErrWithdrawalScopeDenied) {
			t.Fatalf("expected ErrWithdrawalScopeDenied for %q, got %v", badPerm, err)
		}
	}

	perms, err := VerifyTradeOnlyPermissions(VenueOKX, []string{"read_trade", "futures_trade"})
	if err != nil || len(perms) != 2 {
		t.Fatalf("expected valid trade-only permissions, got perms=%v err=%v", perms, err)
	}
	if _, err := VerifyTradeOnlyPermissions(VenueBybit, []string{"read_only"}); err == nil {
		t.Fatal("expected missing trade scope rejection")
	}
}

func TestCEXSigningAllSixVenues(t *testing.T) {
	t.Parallel()
	if s := SignBinanceHMAC("sec", "symbol=BTCUSDT&timestamp=1700000000000"); len(s) != 64 {
		t.Fatalf("unexpected binance sig len %d", len(s))
	}
	if s := SignBybitV5("sec", "1700000000000", "key", "5000", "{}"); len(s) != 64 {
		t.Fatalf("unexpected bybit sig len %d", len(s))
	}
	if s := SignOKXV5("sec", "2026-11-25T00:00:00Z", "POST", "/api/v5/trade/order", "{}"); s == "" {
		t.Fatal("empty okx sig")
	}
	kSig, err := SignKraken(base64.StdEncoding.EncodeToString([]byte("kraken-secret-key")), "/0/private/AddOrder", "1700000000000", "nonce=1700000000000")
	if err != nil || kSig == "" {
		t.Fatalf("SignKraken failed: %v", err)
	}
	if s := SignCoinbaseHMAC("sec", "1700000000", "POST", "/api/v3/brokerage/orders", "{}"); len(s) != 64 {
		t.Fatalf("unexpected coinbase sig len %d", len(s))
	}

	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	dSigB64, err := SignDeribitEd25519(hex.EncodeToString(seed), "1700000000000", "n1", "POST", "/api/v2/private/buy", `{"instrument_name":"BTC-PERPETUAL"}`)
	if err != nil {
		t.Fatalf("SignDeribitEd25519 failed: %v", err)
	}
	dSig, err := base64.RawURLEncoding.DecodeString(dSigB64)
	if err != nil || !ed25519.Verify(pub, []byte("1700000000000\nn1\nPOST\n/api/v2/private/buy\n{\"instrument_name\":\"BTC-PERPETUAL\"}\n"), dSig) {
		t.Fatalf("Deribit Ed25519 verification failed")
	}
}

func TestCEXServiceLifecycleEncryptionAndSSRF(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Unix(1795600000, 0)
	svc, err := NewService(Config{
		DataDir:     dir,
		FixtureMode: true,
		Now:         func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer svc.Close()

	// Mainnet credential storage is blocked at compile time.
	if _, err := svc.SaveCredential(context.Background(), CredentialInput{
		Venue:       VenueBinance,
		Environment: "mainnet",
		APIKey:      "binance-key-123456",
		APISecret:   "binance-secret-123456",
		Permissions: []string{"spot_trade"},
	}); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("expected ErrExecutionDisabled for mainnet credential, got %v", err)
	}

	// Withdrawal-enabled credentials must be rejected.
	if _, err := svc.SaveCredential(context.Background(), CredentialInput{
		Venue:       VenueBinance,
		Environment: "testnet",
		APIKey:      "binance-key-123456",
		APISecret:   "binance-secret-123456",
		Permissions: []string{"spot_trade", "withdraw"},
	}); !errors.Is(err, ErrWithdrawalScopeDenied) {
		t.Fatalf("expected ErrWithdrawalScopeDenied, got %v", err)
	}

	saved, err := svc.SaveCredential(context.Background(), CredentialInput{
		Venue:       VenueKraken,
		Label:       "kraken-sandbox",
		Environment: "sandbox",
		APIKey:      "kraken-public-key-123456",
		APISecret:   base64.StdEncoding.EncodeToString([]byte("kraken-super-secret-value")),
		Permissions: []string{"derivatives_trade"},
	})
	if err != nil {
		t.Fatalf("SaveCredential failed: %v", err)
	}
	if !saved.WithdrawalDisabled || !strings.HasPrefix(saved.KeyFingerprint, "sha256:") {
		t.Fatalf("unexpected saved credential status: %+v", saved)
	}

	// Verify status never exposes secrets.
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	rawSt, _ := json.Marshal(st)
	if strings.Contains(string(rawSt), "kraken-super-secret-value") || strings.Contains(string(rawSt), "kraken-public-key-123456") {
		t.Fatalf("status leaked secret material: %s", rawSt)
	}
	if !st.MainnetLocked || st.ActivationEnabled || len(st.SupportedVenues) != 6 {
		t.Fatalf("unexpected StatusView: %+v", st)
	}

	// Mainnet order fails closed.
	if _, err := svc.SubmitOrder(context.Background(), OrderRequest{
		Venue:       VenueKraken,
		Symbol:      "PI_XBTUSD",
		Side:        "BUY",
		OrderType:   "LIMIT",
		Quantity:    1,
		Price:       62000,
		Environment: "mainnet",
	}); !errors.Is(err, ErrExecutionDisabled) {
		t.Fatalf("expected ErrExecutionDisabled for mainnet order, got %v", err)
	}

	rec, err := svc.SubmitOrder(context.Background(), OrderRequest{
		Venue:       VenueKraken,
		Symbol:      "PI_XBTUSD",
		Side:        "BUY",
		OrderType:   "LIMIT",
		Quantity:    2,
		Price:       61500,
		Environment: "sandbox",
	})
	if err != nil {
		t.Fatalf("SubmitOrder sandbox failed: %v", err)
	}
	if !rec.Simulated || rec.Status != "testnet_signed_dryrun" || !strings.HasPrefix(rec.Digest, "0x") {
		t.Fatalf("unexpected OrderReceipt: %+v", rec)
	}

	// SSRF protection blocks loopback / metadata / private targets.
	for _, rawIP := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "100.64.0.5"} {
		addr := netip.MustParseAddr(rawIP)
		if isPublicUnicastIP(addr) {
			t.Fatalf("expected private/CGNAT ip %s to be rejected", rawIP)
		}
	}
	if err := ValidateTestnetURL(VenueBinance, "https://api.binance.com/api/v3/time"); err == nil {
		t.Fatal("expected mainnet binance URL to be denied by ValidateTestnetURL")
	}
	if err := ValidateTestnetURL(VenueBinance, "https://testnet.binance.vision/api/v3/time"); err != nil {
		t.Fatalf("expected testnet binance URL to pass: %v", err)
	}
}

func TestCEXMainnetCappedPolicyAndCeiling(t *testing.T) {
	svc, err := NewService(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer svc.Close()

	// Default keeps mainnet locked and CappedMode off.
	st, err := svc.Status(context.Background())
	if err != nil || !st.MainnetLocked || st.CappedMode {
		t.Fatalf("expected default locked status, got %+v err=%v", st, err)
	}
	if err := svc.ConfigureMainnetCapped(0); err == nil {
		t.Fatal("expected 0 max order to fail")
	}
	if err := svc.ConfigureMainnetCapped(250_000_001); err == nil {
		t.Fatal("expected >$250 max order to fail")
	}
	if err := svc.ConfigureMainnetCapped(50_000_000); err != nil {
		t.Fatalf("ConfigureMainnetCapped($50) failed: %v", err)
	}
	st, err = svc.Status(context.Background())
	if err != nil || st.MainnetLocked || !st.CappedMode || st.MaxOrderMicroUSD != 50_000_000 {
		t.Fatalf("unexpected capped status: %+v err=%v", st, err)
	}

	// Withdrawal permissions still rejected under mainnet-capped.
	if _, err := svc.SaveCredential(context.Background(), CredentialInput{
		Venue:       VenueBinance,
		Environment: "mainnet",
		APIKey:      "key-123456",
		APISecret:   "secret-12345678",
		Permissions: []string{"SPOT_TRADE", "WITHDRAW"},
	}); !errors.Is(err, ErrWithdrawalScopeDenied) {
		t.Fatalf("expected ErrWithdrawalScopeDenied, got %v", err)
	}

	// Valid trade-only mainnet credential succeeds.
	if _, err := svc.SaveCredential(context.Background(), CredentialInput{
		Venue:       VenueBinance,
		Environment: "mainnet",
		APIKey:      "key-123456",
		APISecret:   "secret-12345678",
		Permissions: []string{"SPOT_TRADE"},
	}); err != nil {
		t.Fatalf("SaveCredential mainnet trade-only failed: %v", err)
	}

	// Over $50 order rejected; <= $50 LIMIT order accepted.
	if _, err := svc.SubmitOrder(context.Background(), OrderRequest{
		Venue:       VenueBinance,
		Symbol:      "BTCUSDT",
		Side:        "BUY",
		OrderType:   "LIMIT",
		Quantity:    0.002,
		Price:       60000, // $120 > $50 cap
		Environment: "mainnet",
	}); err == nil {
		t.Fatal("expected >$50 CEX mainnet order to fail")
	}
	rec, err := svc.SubmitOrder(context.Background(), OrderRequest{
		Venue:       VenueBinance,
		Symbol:      "BTCUSDT",
		Side:        "BUY",
		OrderType:   "LIMIT",
		Quantity:    0.0005,
		Price:       60000, // $30 <= $50 cap
		Environment: "mainnet",
	})
	if err != nil || rec.Status != "mainnet_capped_signed" {
		t.Fatalf("expected mainnet_capped_signed, got %+v err=%v", rec, err)
	}
}
