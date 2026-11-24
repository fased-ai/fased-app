//go:build linux

package signer

import (
	"errors"
	"testing"
	"time"
)

func TestVaultHyperliquidFailClosedAndControlledSigning(t *testing.T) {
	if HyperliquidActivationEnabled {
		t.Fatal("HyperliquidActivationEnabled must stay false until explicit owner authorization")
	}
	dir := fixtureDir(t)
	id := "0123456789abcdef0123456789abcdef"
	v, err := Open(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = v.ConfigureHyperliquidControlled(false)
		_ = v.Close()
	}()

	pass := []byte("owner-vault-passphrase-2026")
	if err := v.Create(pass); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock(pass, 10*time.Minute); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	policy := HyperliquidPolicy{
		Version:                   1,
		Network:                   "testnet",
		AllowedAssets:             []string{"BTC", "ETH", "SOL"},
		MaxLeverage:               10,
		MaxNotionalMicroUSD:       5_000_000_000, // $5,000
		MaxOrderPriceBpsDeviation: 500,           // 5%
		ExpiresAt:                 now + 3600,
	}
	goodOrder := HyperliquidOrderIntent{
		Network:            "testnet",
		Asset:              "ETH",
		AssetIndex:         1,
		IsBuy:              true,
		LimitPrice:         "3200.5",
		Size:               "0.5",
		ReferenceMarkPrice: "3200",
		Leverage:           5,
		ReduceOnly:         false,
		TimeInForce:        "Gtc",
		Grouping:           "na",
		Nonce:              100,
		ExpiresAt:          now + 300,
	}

	// Ordinary vault (controlled Hyperliquid NOT enabled) must fail closed even when unlocked.
	if _, err := v.HyperliquidAddress(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ordinary vault HyperliquidAddress got %v, want ErrUnavailable", err)
	}
	if _, err := v.SignHyperliquidOrder(goodOrder, policy); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ordinary vault SignHyperliquidOrder got %v, want ErrUnavailable", err)
	}

	// Enable controlled mode.
	if err := v.ConfigureHyperliquidControlled(true); err != nil {
		t.Fatal(err)
	}
	addr, err := v.HyperliquidAddress()
	if err != nil {
		t.Fatalf("HyperliquidAddress: %v", err)
	}
	if _, err := ValidateEthereumAddress(addr); err != nil {
		t.Fatalf("derived address invalid: %v", err)
	}

	// Mainnet stays disabled while HyperliquidActivationEnabled == false.
	mainnetPolicy := policy
	mainnetPolicy.Network = "mainnet"
	mainnetOrder := goodOrder
	mainnetOrder.Network = "mainnet"
	if _, err := v.SignHyperliquidOrder(mainnetOrder, mainnetPolicy); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mainnet order while activation=false got %v, want ErrUnavailable", err)
	}

	// Policy violations must fail before signing.
	badLeverage := goodOrder
	badLeverage.Leverage = 11
	if _, err := v.SignHyperliquidOrder(badLeverage, policy); err == nil {
		t.Fatal("accepted order exceeding MaxLeverage")
	}
	badNotional := goodOrder
	badNotional.Size = "2" // $6,401 > $5,000
	if _, err := v.SignHyperliquidOrder(badNotional, policy); err == nil {
		t.Fatal("accepted order exceeding MaxNotionalMicroUSD")
	}
	fatFinger := goodOrder
	fatFinger.LimitPrice = "3600" // > 5% above 3200
	if _, err := v.SignHyperliquidOrder(fatFinger, policy); err == nil {
		t.Fatal("accepted order exceeding fat-finger price collar")
	}
	unallowlisted := goodOrder
	unallowlisted.Asset = "DOGE"
	if _, err := v.SignHyperliquidOrder(unallowlisted, policy); err == nil {
		t.Fatal("accepted unallowlisted asset")
	}

	// Happy path sign + ecrecover check.
	signed, err := v.SignHyperliquidOrder(goodOrder, policy)
	if err != nil {
		t.Fatalf("SignHyperliquidOrder: %v", err)
	}
	if signed.SignerAddress != addr {
		t.Fatalf("signer address mismatch: %s != %s", signed.SignerAddress, addr)
	}
	if signed.NotionalMicroUSD != 1_600_250_000 {
		t.Fatalf("notional mismatch: got %d want 1600250000", signed.NotionalMicroUSD)
	}
	sig, err := ParseECDSASignature(signed.SignatureHex)
	if err != nil {
		t.Fatalf("parse signed signature: %v", err)
	}
	digestBytes := mustHex(t, signed.TypedDataDigest[2:])
	var digest32 [32]byte
	copy(digest32[:], digestBytes)
	if err := VerifySecp256k1Signature(addr, digest32, sig); err != nil {
		t.Fatalf("ecrecover failed on signed Hyperliquid order: %v", err)
	}

	// Nonce replay must be rejected.
	if _, err := v.SignHyperliquidOrder(goodOrder, policy); err == nil {
		t.Fatal("accepted replayed nonce")
	}

	// Valid cancel with strictly higher nonce succeeds.
	cancel := HyperliquidCancelIntent{
		Network:    "testnet",
		Asset:      "ETH",
		AssetIndex: 1,
		OrderID:    99887766,
		Nonce:      101,
		ExpiresAt:  now + 300,
	}
	signedCancel, err := v.SignHyperliquidCancel(cancel, policy)
	if err != nil {
		t.Fatalf("SignHyperliquidCancel: %v", err)
	}
	if signedCancel.SignerAddress != addr {
		t.Fatalf("cancel signer mismatch: %s != %s", signedCancel.SignerAddress, addr)
	}

	// Valid updateLeverage fails on policy leverage violation before touching nonce.
	badLevIntent := HyperliquidUpdateLeverageIntent{
		Network:    "testnet",
		Asset:      "ETH",
		AssetIndex: 1,
		Leverage:   11,
		IsCross:    false,
		Nonce:      102,
		ExpiresAt:  now + 300,
	}
	if _, err := v.SignHyperliquidUpdateLeverage(badLevIntent, policy); err == nil {
		t.Fatal("accepted updateLeverage exceeding MaxLeverage")
	}

	// Valid updateLeverage succeeds and advances nonce.
	levIntent := badLevIntent
	levIntent.Leverage = 5
	signedLev, err := v.SignHyperliquidUpdateLeverage(levIntent, policy)
	if err != nil {
		t.Fatalf("SignHyperliquidUpdateLeverage: %v", err)
	}
	if signedLev.SignerAddress != addr {
		t.Fatalf("updateLeverage signer mismatch: %s != %s", signedLev.SignerAddress, addr)
	}
	levSig, err := ParseECDSASignature(signedLev.SignatureHex)
	if err != nil {
		t.Fatalf("parse updateLeverage sig: %v", err)
	}
	levDigestBytes := mustHex(t, signedLev.TypedDataDigest[2:])
	var levDigest32 [32]byte
	copy(levDigest32[:], levDigestBytes)
	if err := VerifySecp256k1Signature(addr, levDigest32, levSig); err != nil {
		t.Fatalf("ecrecover failed on signed Hyperliquid updateLeverage: %v", err)
	}
	// Replay nonce for updateLeverage must be rejected.
	if _, err := v.SignHyperliquidUpdateLeverage(levIntent, policy); err == nil {
		t.Fatal("accepted replayed updateLeverage nonce")
	}

	// Locking vault blocks further signing and address reads.
	v.Lock()
	if _, err := v.HyperliquidAddress(); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked vault HyperliquidAddress got %v, want ErrLocked", err)
	}
	cancel.Nonce = 103
	if _, err := v.SignHyperliquidCancel(cancel, policy); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked vault SignHyperliquidCancel got %v, want ErrLocked", err)
	}
}

func TestVaultCloseClearsHyperliquidState(t *testing.T) {
	dir := fixtureDir(t)
	id := "fedcba9876543210fedcba9876543210"
	v, err := Open(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	pass := []byte("owner-vault-close-test-2026")
	if err := v.Create(pass); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock(pass, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := v.ConfigureHyperliquidControlled(true); err != nil {
		t.Fatal(err)
	}
	hyperliquidVaultMu.Lock()
	_, existsBefore := hyperliquidVaultStates[v]
	hyperliquidVaultMu.Unlock()
	if !existsBefore {
		t.Fatal("expected hyperliquidVaultStates entry after ConfigureHyperliquidControlled(true)")
	}
	if err := v.Close(); err != nil {
		t.Fatalf("Vault.Close: %v", err)
	}
	hyperliquidVaultMu.Lock()
	_, existsAfter := hyperliquidVaultStates[v]
	hyperliquidVaultMu.Unlock()
	if existsAfter {
		t.Fatal("Vault.Close leaked hyperliquidVaultStates entry")
	}
	if _, err := v.HyperliquidAddress(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed vault HyperliquidAddress got %v, want ErrUnavailable", err)
	}
}
