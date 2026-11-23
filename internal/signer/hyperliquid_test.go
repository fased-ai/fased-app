package signer

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
	"time"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex decode %q: %v", s, err)
	}
	return b
}

func TestSecp256k1GoldenVectorsAndECRECOVER(t *testing.T) {
	vectors := []struct {
		name string
		priv string
		addr string
	}{
		{
			name: "scalar-one",
			priv: "0000000000000000000000000000000000000000000000000000000000000001",
			addr: "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf",
		},
		{
			name: "web3-standard-key",
			priv: "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318",
			addr: "0x2c7536E3605D9C16a7a3D7b1898e529396a65c23",
		},
	}

	for _, tc := range vectors {
		priv := mustHex(t, tc.priv)
		pubU, pubC, err := Secp256k1PublicKey(priv)
		if err != nil {
			t.Fatalf("%s public key: %v", tc.name, err)
		}
		if len(pubU) != 65 || len(pubC) != 33 {
			t.Fatalf("%s unexpected key sizes: %d, %d", tc.name, len(pubU), len(pubC))
		}
		addr, err := EthereumAddressFromPublicKey(pubU)
		if err != nil {
			t.Fatalf("%s address: %v", tc.name, err)
		}
		if addr != tc.addr {
			t.Fatalf("%s address mismatch: got %s want %s", tc.name, addr, tc.addr)
		}
		canon, err := ValidateEthereumAddress(addr)
		if err != nil || canon != tc.addr {
			t.Fatalf("%s ValidateEthereumAddress(%q) = %q, %v", tc.name, addr, canon, err)
		}
		// Reject corrupted EIP-55 mixed-case checksum.
		flipped := addr[:2] + "e" + addr[3:]
		if flipped != addr {
			if _, err := ValidateEthereumAddress(flipped); err == nil {
				t.Fatalf("%s accepted corrupted EIP-55 checksum %s", tc.name, flipped)
			}
		}

		digest := Keccak256([]byte("fased-hyperliquid-ecdsa-test:" + tc.name))
		sig1, err := SignSecp256k1Digest(priv, digest)
		if err != nil {
			t.Fatalf("%s sign 1: %v", tc.name, err)
		}
		sig2, err := SignSecp256k1Digest(priv, digest)
		if err != nil {
			t.Fatalf("%s sign 2: %v", tc.name, err)
		}
		if sig1.Hex() != sig2.Hex() {
			t.Fatalf("%s RFC 6979 non-deterministic: %s != %s", tc.name, sig1.Hex(), sig2.Hex())
		}
		if sig1.V != 27 && sig1.V != 28 {
			t.Fatalf("%s unexpected recovery id V=%d", tc.name, sig1.V)
		}
		if err := VerifySecp256k1Signature(tc.addr, digest, sig1); err != nil {
			t.Fatalf("%s ecrecover verify failed: %v", tc.name, err)
		}
		parsed, err := ParseECDSASignature(sig1.Hex())
		if err != nil || parsed != sig1 {
			t.Fatalf("%s ParseECDSASignature failed: %v", tc.name, err)
		}

		// Tampered digest fails recovery match.
		badDigest := digest
		badDigest[0] ^= 0xff
		if err := VerifySecp256k1Signature(tc.addr, badDigest, sig1); err == nil {
			t.Fatalf("%s verified tampered digest", tc.name)
		}

		// High-S malleability rejected.
		sBig := new(big.Int).SetBytes(sig1.S[:])
		highS := new(big.Int).Sub(secp256k1N, sBig)
		malleated := sig1
		highS.FillBytes(malleated.S[:])
		if _, err := RecoverSecp256k1PublicKey(digest, malleated); err == nil {
			t.Fatalf("%s accepted non-canonical high-S signature", tc.name)
		}
	}

	// Reject zero and >= curve order keys.
	if err := ValidateSecp256k1PrivateKey(make([]byte, 32)); err == nil {
		t.Fatal("accepted zero secp256k1 private key")
	}
	nBytes := make([]byte, 32)
	secp256k1N.FillBytes(nBytes)
	if err := ValidateSecp256k1PrivateKey(nBytes); err == nil {
		t.Fatal("accepted curve-order secp256k1 private key")
	}
}

func TestEIP712AndHyperliquidMsgpackVectors(t *testing.T) {
	// EIP-712 domain separator must match Hyperliquid's Exchange v1 chainId=1337 contract=0x0.
	domainSep, err := EIP712DomainSeparator(HyperliquidExchangeDomain())
	if err != nil {
		t.Fatalf("domain separator: %v", err)
	}
	expectedDomainType := Keccak256([]byte("EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	nameH := Keccak256([]byte("Exchange"))
	verH := Keccak256([]byte("1"))
	var chainWord, addrWord [32]byte
	big.NewInt(1337).FillBytes(chainWord[:])
	manualSep := Keccak256(expectedDomainType[:], nameH[:], verH[:], chainWord[:], addrWord[:])
	if domainSep != manualSep {
		t.Fatalf("domain separator mismatch: %x != %x", domainSep, manualSep)
	}

	// Verify Keccak256("") equals Ethereum empty Keccak-256 golden constant c5d246...
	emptyDigest := Keccak256(nil)
	emptyKeccak := hex.EncodeToString(emptyDigest[:])
	if emptyKeccak != "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470" {
		t.Fatalf("unexpected Keccak256(empty): %s", emptyKeccak)
	}

	intent := HyperliquidOrderIntent{
		Network:            "testnet",
		Asset:              "ETH",
		AssetIndex:         1,
		IsBuy:              true,
		LimitPrice:         "3200.50",
		Size:               "0.2500",
		ReferenceMarkPrice: "3200",
		Leverage:           5,
		ReduceOnly:         false,
		TimeInForce:        "Gtc",
		Grouping:           "na",
		ClientOrderID:      "0x11111111111111111111111111111111",
		Nonce:              1700000001000,
		ExpiresAt:          1800000000,
	}
	packed, err := EncodeHyperliquidOrderAction(intent)
	if err != nil {
		t.Fatalf("encode order msgpack: %v", err)
	}
	// Check canonical field keys and trimmed trailing zeros ("3200.5", "0.25").
	if !bytes.Contains(packed, []byte("3200.5")) || bytes.Contains(packed, []byte("3200.50")) {
		t.Fatalf("expected trimmed limitPrice in msgpack: %x", packed)
	}
	if !bytes.Contains(packed, []byte("0.25")) || bytes.Contains(packed, []byte("0.2500")) {
		t.Fatalf("expected trimmed size in msgpack: %x", packed)
	}

	connA, err := HyperliquidActionConnectionID(packed, intent.Nonce, "")
	if err != nil {
		t.Fatalf("connection id no vault: %v", err)
	}
	connB, err := HyperliquidActionConnectionID(packed, intent.Nonce, "0x0000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("connection id with vault: %v", err)
	}
	if connA == connB {
		t.Fatal("vault address did not alter connectionId")
	}

	dTest, err := HyperliquidAgentDigest("testnet", connA)
	if err != nil {
		t.Fatalf("testnet digest: %v", err)
	}
	dMain, err := HyperliquidAgentDigest("mainnet", connA)
	if err != nil {
		t.Fatalf("mainnet digest: %v", err)
	}
	if dTest == dMain {
		t.Fatal("testnet and mainnet agent sources collided")
	}
}

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

	// Locking vault blocks further signing and address reads.
	v.Lock()
	if _, err := v.HyperliquidAddress(); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked vault HyperliquidAddress got %v, want ErrLocked", err)
	}
	cancel.Nonce = 102
	if _, err := v.SignHyperliquidCancel(cancel, policy); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked vault SignHyperliquidCancel got %v, want ErrLocked", err)
	}
}

func FuzzHyperliquidDecimalAndAddress(f *testing.F) {
	f.Add("3200.50", "0.2500", "3200", "0x2c7536E3605D9C16a7a3D7b1898e529396a65c23")
	f.Add("96500", "0.01", "96000", "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf")
	f.Add("0", "-1", "0.000000001", "0xnothex")
	f.Fuzz(func(t *testing.T, price, size, mark, addr string) {
		_, _ = ComputeNotionalMicroUSD(price, size)
		_, _, _ = PriceDeviationBps(price, mark)
		if canon, err := ValidateEthereumAddress(addr); err == nil {
			if _, err2 := ValidateEthereumAddress(canon); err2 != nil {
				t.Fatalf("canonical address rejected: %v", err2)
			}
		}
	})
}
