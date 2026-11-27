package signer

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPolymarketOrderDigestAndSigning(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		useV3Dev bool
	}{
		{name: "version1_controlled"},
		{name: "version3_devnet", useV3Dev: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := privateSignerTempDir(t)
			signerID := strings.Repeat("b", 32)
			pass := []byte("poly-amoy-vault-passphrase-2026")

			var v *Vault
			var err error
			if tc.useV3Dev {
				v, err = OpenOrdinaryCustody(dir, signerID, "devnet")
			} else {
				v, err = Open(dir, signerID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Create(pass); err != nil {
				_ = v.Close()
				t.Fatal(err)
			}
			if err := v.Unlock(pass, 10*time.Minute); err != nil {
				_ = v.Close()
				t.Fatal(err)
			}
			if err := v.ConfigureTestnetExecution(true); err != nil {
				_ = v.Close()
				t.Fatal(err)
			}

			pmStatus, err := v.PolymarketStatus()
			if err != nil {
				_ = v.Close()
				t.Fatalf("PolymarketStatus failed: %v", err)
			}
			hlStatus, err := v.HyperliquidStatus()
			if err != nil {
				_ = v.Close()
				t.Fatalf("HyperliquidStatus failed: %v", err)
			}
			if pmStatus.SignerAddress == "" || hlStatus.Address == "" || pmStatus.SignerAddress == hlStatus.Address {
				_ = v.Close()
				t.Fatalf("expected distinct non-empty domain-separated EVM addresses: pm=%q hl=%q", pmStatus.SignerAddress, hlStatus.Address)
			}
			if !pmStatus.MainnetLocked || pmStatus.ChainID != PolymarketAmoyChainID {
				_ = v.Close()
				t.Fatalf("unexpected PolymarketStatus: %+v", pmStatus)
			}

			now := time.Now().Unix()
			vaultID := strings.Repeat("c", 32)
			if err := v.SetAgentVaultPolicy(AgentVaultPolicy{
				Version:            1,
				VaultID:            vaultID,
				AgentID:            "poly-agent",
				AllowedVenues:      []string{"polymarket"},
				AllowedAssets:      []string{"USDC"},
				MaxOrderMicroUSD:   5_000_000, // $5
				DailySpendMicroUSD: 8_000_000, // $8
				DailyLossMicroUSD:  2_000_000,
				MaxLeverage:        1,
				ExpiresAt:          now + 3600,
			}); err != nil {
				_ = v.Close()
				t.Fatal(err)
			}

			// Mainnet Polygon (chainId 137 / network mainnet|polygon) must be refused fail-closed.
			for _, badNet := range []struct {
				net     string
				chainID uint64
			}{
				{net: "mainnet", chainID: PolymarketMainnetChainID},
				{net: "polygon", chainID: PolymarketMainnetChainID},
				{net: "amoy", chainID: PolymarketMainnetChainID},
			} {
				_, err := v.SignPolymarketOrderIPC(PolymarketOrderIntent{
					Network:         badNet.net,
					ChainID:         badNet.chainID,
					OperationID:     "pm-mainnet-denied",
					Asset:           "USDC",
					Salt:            "123456789",
					TokenID:         "1000001",
					MakerAmount:     "1000000",
					TakerAmount:     "2000000",
					Side:            0,
					SignatureType:   0,
					IntentExpiresAt: now + 300,
				}, vaultID)
				if !errors.Is(err, ErrUnavailable) {
					_ = v.Close()
					t.Fatalf("expected ErrUnavailable for %+v, got %v", badNet, err)
				}
			}

			// Wrong venue or invalid BUY implied probability (>= 1.0) rejected.
			if _, err := v.SignPolymarketOrderIPC(PolymarketOrderIntent{
				Network:         "amoy",
				ChainID:         PolymarketAmoyChainID,
				OperationID:     "pm-bad-prob",
				Asset:           "USDC",
				Salt:            "123",
				TokenID:         "1000001",
				MakerAmount:     "2000000",
				TakerAmount:     "1000000",
				Side:            0,
				IntentExpiresAt: now + 300,
			}, vaultID); err == nil {
				_ = v.Close()
				t.Fatal("expected invalid implied probability error")
			}

			// Valid BUY order ($4.00 maker USDC for 10 shares = 0.40 implied probability) signs cleanly.
			buyIntent := PolymarketOrderIntent{
				Network:         "amoy",
				ChainID:         PolymarketAmoyChainID,
				OperationID:     "pm-buy-1",
				Asset:           "USDC",
				Salt:            "987654321",
				TokenID:         "12345678901234567890",
				MakerAmount:     "4000000",
				TakerAmount:     "10000000",
				Expiration:      uint64(now + 600),
				Nonce:           1,
				FeeRateBps:      0,
				Side:            0,
				SignatureType:   0,
				IntentExpiresAt: now + 300,
			}
			signed, err := v.SignPolymarketOrderIPC(buyIntent, vaultID)
			if err != nil {
				_ = v.Close()
				t.Fatalf("SignPolymarketOrderIPC BUY failed: %v", err)
			}
			if signed.SignerAddress != pmStatus.SignerAddress || signed.NotionalMicroUSD != 4_000_000 {
				_ = v.Close()
				t.Fatalf("unexpected signed order metadata: %+v", signed)
			}
			sig, err := ParseECDSASignature(signed.SignatureHex)
			if err != nil {
				_ = v.Close()
				t.Fatal(err)
			}
			digestBytes, err := hex.DecodeString(strings.TrimPrefix(signed.TypedDataDigest, "0x"))
			if err != nil || len(digestBytes) != 32 {
				_ = v.Close()
				t.Fatalf("invalid typedDataDigest: %q", signed.TypedDataDigest)
			}
			var digest32 [32]byte
			copy(digest32[:], digestBytes)
			if err := VerifySecp256k1Signature(pmStatus.SignerAddress, digest32, sig); err != nil {
				_ = v.Close()
				t.Fatalf("VerifySecp256k1Signature failed: %v", err)
			}

			// Duplicate OperationID rejected.
			if _, err := v.SignPolymarketOrderIPC(buyIntent, vaultID); err == nil {
				_ = v.Close()
				t.Fatal("expected duplicate operationId rejection")
			}

			// NegRisk SELL order ($3.00 USDC collateral received for 5 shares = 0.60) uses NegRiskExchange verifyingContract.
			sellSigned, err := v.SignPolymarketOrderIPC(PolymarketOrderIntent{
				Network:         "amoy",
				ChainID:         PolymarketAmoyChainID,
				NegRisk:         true,
				OperationID:     "pm-sell-2",
				Asset:           "USDC",
				Salt:            "987654322",
				TokenID:         "12345678901234567890",
				MakerAmount:     "5000000",
				TakerAmount:     "3000000",
				Side:            1,
				IntentExpiresAt: now + 300,
			}, vaultID)
			if err != nil {
				_ = v.Close()
				t.Fatalf("SignPolymarketOrderIPC NegRisk SELL failed: %v", err)
			}
			canonNeg, _ := ValidateEthereumAddress(strings.ToLower(PolymarketAmoyNegRiskExchangeContract))
			if sellSigned.VerifyingContract != canonNeg || sellSigned.NotionalMicroUSD != 3_000_000 {
				_ = v.Close()
				t.Fatalf("expected NegRisk contract %s, got %+v", canonNeg, sellSigned)
			}

			// Third order ($2.00) would push daily spend to $9.00 > $8.00 cap -> rejected cleanly.
			if _, err := v.SignPolymarketOrderIPC(PolymarketOrderIntent{
				Network:         "amoy",
				ChainID:         PolymarketAmoyChainID,
				OperationID:     "pm-over-daily-3",
				Asset:           "USDC",
				Salt:            "987654323",
				TokenID:         "12345678901234567890",
				MakerAmount:     "2000000",
				TakerAmount:     "5000000",
				Side:            0,
				IntentExpiresAt: now + 300,
			}, vaultID); err == nil {
				_ = v.Close()
				t.Fatal("expected daily spend cap enforcement")
			}

			// Close and reopen to confirm durable journal replay enforces cumulative daily spend.
			if err := v.Close(); err != nil {
				t.Fatal(err)
			}
			if tc.useV3Dev {
				v, err = OpenOrdinaryCustody(dir, signerID, "devnet")
			} else {
				v, err = Open(dir, signerID)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer v.Close()
			if err := v.Unlock(pass, 10*time.Minute); err != nil {
				t.Fatal(err)
			}
			if err := v.ConfigureTestnetExecution(true); err != nil {
				t.Fatal(err)
			}
			if _, err := v.SignPolymarketOrderIPC(PolymarketOrderIntent{
				Network:         "amoy",
				ChainID:         PolymarketAmoyChainID,
				OperationID:     "pm-over-daily-reopen",
				Asset:           "USDC",
				Salt:            "987654324",
				TokenID:         "12345678901234567890",
				MakerAmount:     "2000000",
				TakerAmount:     "5000000",
				Side:            0,
				IntentExpiresAt: now + 300,
			}, vaultID); err == nil {
				t.Fatal("expected persisted daily spend limit after vault reopen")
			}
		})
	}
}

func TestKalshiDemoKeyCustodyAndPSSSigning(t *testing.T) {
	t.Parallel()
	dir := privateSignerTempDir(t)
	signerID := strings.Repeat("d", 32)
	pass := []byte("kalshi-demo-vault-passphrase-2026")

	v, err := OpenOrdinaryCustody(dir, signerID, "devnet")
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

	// Provision rejects prod env and <2048 RSA keys.
	weakKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	weakPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(weakKey)})
	if _, err := v.ProvisionKalshiDemoKey(KalshiKeyProvision{
		Environment:   "prod",
		KeyID:         "prod-forbidden",
		PrivateKeyPEM: weakPEM,
	}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable for prod provision, got %v", err)
	}
	weakPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(weakKey)})
	if _, err := v.ProvisionKalshiDemoKey(KalshiKeyProvision{
		Environment:   "demo",
		KeyID:         "weak-1024",
		PrivateKeyPEM: weakPEM,
	}); err == nil {
		t.Fatal("expected 1024-bit RSA key rejection")
	}

	// Valid RSA-2048 provisioning works and seals the key at 0600 without storing PEM private text.
	st, err := v.GenerateKalshiDemoKey("demo-key-uuid-01")
	if err != nil {
		t.Fatalf("GenerateKalshiDemoKey failed: %v", err)
	}
	if !st.Configured || st.KeyID != "demo-key-uuid-01" || !st.MainnetLocked || st.PublicKeyPEM == "" {
		t.Fatalf("unexpected KalshiStatus: %+v", st)
	}
	keyPath := filepath.Join(dir, kalshiDemoKeyFile)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 key file permissions, got %v", info.Mode().Perm())
	}
	diskBytes, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(diskBytes), "RSA PRIVATE KEY") || strings.Contains(string(diskBytes), "BEGIN PRIVATE KEY") {
		t.Fatal("plaintext PEM leaked into sealed key file")
	}

	now := time.Now().Unix()
	vaultID := strings.Repeat("e", 32)
	if err := v.SetAgentVaultPolicy(AgentVaultPolicy{
		Version:            1,
		VaultID:            vaultID,
		AgentID:            "kalshi-agent",
		AllowedVenues:      []string{"kalshi"},
		AllowedAssets:      []string{"USD"},
		MaxOrderMicroUSD:   5_000_000,
		DailySpendMicroUSD: 9_000_000,
		DailyLossMicroUSD:  2_000_000,
		MaxLeverage:        1,
		ExpiresAt:          now + 3600,
	}); err != nil {
		t.Fatal(err)
	}

	// Production request must be refused fail-closed.
	if _, err := v.SignKalshiRequestIPC(KalshiSignIntent{
		Environment:       "prod",
		Method:            "POST",
		Path:              "/trade-api/v2/portfolio/events/orders",
		TimestampMs:       time.Now().UnixMilli(),
		Ticker:            "FED-26DEC",
		Asset:             "USD",
		OperationID:       "k-prod-blocked",
		EstimatedMicroUSD: 1_000_000,
		ExpiresAt:         now + 300,
	}, vaultID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable for prod sign, got %v", err)
	}

	// Signed path containing query string must be rejected.
	if _, err := v.SignKalshiRequestIPC(KalshiSignIntent{
		Environment: "demo",
		Method:      "GET",
		Path:        "/trade-api/v2/portfolio/orders?limit=5",
		TimestampMs: time.Now().UnixMilli(),
		OperationID: "k-bad-query",
		ExpiresAt:   now + 300,
	}, vaultID); err == nil {
		t.Fatal("expected rejection of query string in signed path")
	}

	// Detached Kalshi POST order signing is disabled fail-closed because Kalshi V2 RSA-PSS
	// does not sign the JSON request body (requires signer-owned HTTPS egress).
	orderReq := KalshiSignIntent{
		Environment:       "demo",
		Method:            "POST",
		Path:              "/trade-api/v2/portfolio/events/orders",
		TimestampMs:       time.Now().UnixMilli(),
		Ticker:            "KXBTC-26DEC31-B100K",
		Asset:             "USD",
		OperationID:       "kalshi-ord-1",
		EstimatedMicroUSD: 4_500_000,
		ExpiresAt:         now + 300,
	}
	if _, err := v.SignKalshiRequestIPC(orderReq, vaultID); err == nil || !strings.Contains(err.Error(), "signer-owned HTTPS egress required") {
		t.Fatalf("expected detached Kalshi order signing to fail closed, got %v", err)
	}

	// Balance query & cancel order work cleanly without debiting spend.
	signedBal, err := v.SignKalshiRequestIPC(KalshiSignIntent{
		Environment: "demo",
		Method:      "GET",
		Path:        "/trade-api/v2/portfolio/balance",
		TimestampMs: time.Now().UnixMilli(),
		OperationID: "kalshi-bal-1",
		ExpiresAt:   now + 300,
	}, vaultID)
	if err != nil {
		t.Fatalf("SignKalshiRequestIPC balance failed: %v", err)
	}
	if err := VerifyKalshiPSSSignature(st.PublicKeyPEM, signedBal.TimestampMs, signedBal.Method, signedBal.Path, signedBal.SignatureBase64); err != nil {
		t.Fatalf("VerifyKalshiPSSSignature balance failed: %v", err)
	}
	signedCancel, err := v.SignKalshiRequestIPC(KalshiSignIntent{
		Environment: "demo",
		Method:      "DELETE",
		Path:        "/trade-api/v2/portfolio/events/orders/ord-uuid-1234",
		TimestampMs: time.Now().UnixMilli(),
		OperationID: "kalshi-can-1",
		ExpiresAt:   now + 300,
	}, vaultID)
	if err != nil {
		t.Fatalf("SignKalshiRequestIPC cancel failed: %v", err)
	}
	if err := VerifyKalshiPSSSignature(st.PublicKeyPEM, signedCancel.TimestampMs, signedCancel.Method, signedCancel.Path, signedCancel.SignatureBase64); err != nil {
		t.Fatalf("VerifyKalshiPSSSignature cancel failed: %v", err)
	}

	// Locked vault rejects signing with ErrLocked.
	v.Lock()
	if _, err := v.SignKalshiRequestIPC(KalshiSignIntent{
		Environment: "demo",
		Method:      "GET",
		Path:        "/trade-api/v2/portfolio/balance",
		TimestampMs: time.Now().UnixMilli(),
		OperationID: "kalshi-locked",
		ExpiresAt:   now + 300,
	}, vaultID); !errors.Is(err, ErrLocked) {
		t.Fatalf("expected ErrLocked when vault is locked, got %v", err)
	}
}
