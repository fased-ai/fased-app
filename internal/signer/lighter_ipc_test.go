package signer

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLighterIPCSigningAndAgentVaultCustody(t *testing.T) {
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
			signerID := strings.Repeat("a", 32)
			pass := []byte("lighter-stage4-passphrase-2026")

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

			lSt, err := v.LighterStatus()
			if err != nil {
				_ = v.Close()
				t.Fatalf("LighterStatus failed: %v", err)
			}
			if !lSt.Enabled || !lSt.MainnetLocked || lSt.ChainID != LighterTestnetChainID || len(strings.TrimPrefix(lSt.PublicKeyHex, "0x")) != 80 {
				_ = v.Close()
				t.Fatalf("unexpected LighterStatus: %+v", lSt)
			}

			now := time.Now().Unix()
			vaultID := strings.Repeat("9", 32)
			if err := v.SetAgentVaultPolicy(AgentVaultPolicy{
				Version:            1,
				VaultID:            vaultID,
				AgentID:            "lighter-agent",
				AllowedVenues:      []string{"lighter"},
				AllowedAssets:      []string{"ETH", "SOL"},
				MaxOrderMicroUSD:   5_000_000, // $5
				DailySpendMicroUSD: 8_000_000, // $8
				DailyLossMicroUSD:  2_000_000,
				MaxLeverage:        5,
				ExpiresAt:          now + 3600,
			}); err != nil {
				_ = v.Close()
				t.Fatal(err)
			}

			// Mainnet chainId 304 / network mainnet MUST fail closed with ErrUnavailable.
			for _, badNet := range []struct {
				net     string
				chainID uint32
			}{
				{net: "mainnet", chainID: LighterMainnetChainID},
				{net: "testnet", chainID: LighterMainnetChainID},
			} {
				if _, err := v.SignLighterOrderIPC(LighterOrderIntent{
					Network:           badNet.net,
					ChainID:           badNet.chainID,
					OperationID:       "l-mainnet-denied",
					Asset:             "ETH",
					EstimatedMicroUSD: 1_000_000,
					Leverage:          2,
					AccountIndex:      10,
					MarketIndex:       0,
					ClientOrderIndex:  101,
					BaseAmount:        100,
					Price:             200000,
					OrderType:         0,
					TimeInForce:       1,
					OrderExpiry:       (now + 3600) * 1000,
					Nonce:             1,
					ExpiredAt:         (now + 300) * 1000,
					IntentExpiresAt:   now + 300,
				}, vaultID); !errors.Is(err, ErrUnavailable) {
					_ = v.Close()
					t.Fatalf("expected ErrUnavailable for %+v, got %v", badNet, err)
				}
			}

			// Leverage above vault limit (6 > 5) rejected cleanly.
			if _, err := v.SignLighterOrderIPC(LighterOrderIntent{
				Network:           "testnet",
				ChainID:           LighterTestnetChainID,
				OperationID:       "l-over-lev",
				Asset:             "ETH",
				EstimatedMicroUSD: 1_000_000,
				Leverage:          6,
				AccountIndex:      10,
				MarketIndex:       0,
				ClientOrderIndex:  102,
				BaseAmount:        100,
				Price:             200000,
				OrderType:         0,
				TimeInForce:       1,
				OrderExpiry:       (now + 3600) * 1000,
				Nonce:             1,
				ExpiredAt:         (now + 300) * 1000,
				IntentExpiresAt:   now + 300,
			}, vaultID); err == nil {
				_ = v.Close()
				t.Fatal("expected leverage limit rejection")
			}

			// Valid $4.50 ETH GTT limit order signs and verifies with ECgFp5 Schnorr.
			signedOrd, err := v.SignLighterOrderIPC(LighterOrderIntent{
				Network:           "testnet",
				ChainID:           LighterTestnetChainID,
				OperationID:       "l-ord-1",
				Asset:             "ETH",
				EstimatedMicroUSD: 4_500_000,
				Leverage:          3,
				AccountIndex:      42,
				ApiKeyIndex:       1,
				MarketIndex:       0,
				ClientOrderIndex:  1001,
				BaseAmount:        200,
				Price:             225000,
				SizeDecimals:      5,
				PriceDecimals:     2,
				IsAsk:             0,
				OrderType:         0,
				TimeInForce:       1,
				OrderExpiry:       (now + 3600) * 1000,
				Nonce:             7,
				ExpiredAt:         (now + 300) * 1000,
				IntentExpiresAt:   now + 300,
			}, vaultID)
			if err != nil {
				_ = v.Close()
				t.Fatalf("SignLighterOrderIPC failed: %v", err)
			}
			pkB, _ := hex.DecodeString(strings.TrimPrefix(signedOrd.PublicKeyHex, "0x"))
			txB, _ := hex.DecodeString(strings.TrimPrefix(signedOrd.TxHashHex, "0x"))
			sigB, _ := hex.DecodeString(strings.TrimPrefix(signedOrd.SignatureHex, "0x"))
			if err := LighterVerifySignature(pkB, txB, sigB); err != nil {
				_ = v.Close()
				t.Fatalf("LighterVerifySignature failed: %v", err)
			}
			var wire map[string]any
			if err := json.Unmarshal([]byte(signedOrd.TxInfoJSON), &wire); err != nil || wire["AccountIndex"] != float64(42) || wire["Sig"] == "" {
				_ = v.Close()
				t.Fatalf("unexpected TxInfoJSON: %s (%v)", signedOrd.TxInfoJSON, err)
			}

			// UpdateLeverage & Cancel sign cleanly too.
			levSigned, err := v.SignLighterUpdateLeverageIPC(LighterUpdateLeverageIntent{
				Network:         "testnet",
				ChainID:         LighterTestnetChainID,
				OperationID:     "l-lev-1",
				Asset:           "ETH",
				AccountIndex:    42,
				ApiKeyIndex:     1,
				MarketIndex:     0,
				Leverage:        4,
				MarginMode:      1,
				Nonce:           8,
				ExpiredAt:       (now + 300) * 1000,
				IntentExpiresAt: now + 300,
			}, vaultID)
			if err != nil || levSigned.TxType != LighterTxTypeUpdateLev {
				_ = v.Close()
				t.Fatalf("SignLighterUpdateLeverageIPC failed: %+v err=%v", levSigned, err)
			}
			canSigned, err := v.SignLighterCancelIPC(LighterCancelIntent{
				Network:         "testnet",
				ChainID:         LighterTestnetChainID,
				OperationID:     "l-can-1",
				AccountIndex:    42,
				ApiKeyIndex:     1,
				MarketIndex:     0,
				OrderIndex:      1001,
				Nonce:           9,
				ExpiredAt:       (now + 300) * 1000,
				IntentExpiresAt: now + 300,
			}, vaultID)
			if err != nil || canSigned.TxType != LighterTxTypeCancelOrder {
				_ = v.Close()
				t.Fatalf("SignLighterCancelIPC failed: %+v err=%v", canSigned, err)
			}

			// Reopen preserves cumulative $4.50 daily spend: another $4.00 ($8.50 > $8.00) must fail.
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
			if _, err := v.SignLighterOrderIPC(LighterOrderIntent{
				Network:           "testnet",
				ChainID:           LighterTestnetChainID,
				OperationID:       "l-underreported-notional",
				Asset:             "ETH",
				EstimatedMicroUSD: 1, // spoofed $0.000001 for huge order
				Leverage:          2,
				AccountIndex:      42,
				ApiKeyIndex:       1,
				MarketIndex:       0,
				ClientOrderIndex:  1099,
				BaseAmount:        10_000_000,
				Price:             999_999,
				SizeDecimals:      4,
				PriceDecimals:     2,
				OrderType:         0,
				TimeInForce:       1,
				OrderExpiry:       (now + 3600) * 1000,
				Nonce:             11,
				ExpiredAt:         (now + 300) * 1000,
				IntentExpiresAt:   now + 300,
			}, vaultID); err == nil {
				t.Fatal("expected signer-derived notional cap rejection when caller spoofs EstimatedMicroUSD=1")
			}

			if _, err := v.SignLighterOrderIPC(LighterOrderIntent{
				Network:           "testnet",
				ChainID:           LighterTestnetChainID,
				OperationID:       "l-over-daily-reopen",
				Asset:             "ETH",
				EstimatedMicroUSD: 4_000_000,
				Leverage:          2,
				AccountIndex:      42,
				ApiKeyIndex:       1,
				MarketIndex:       0,
				ClientOrderIndex:  1002,
				BaseAmount:        200,
				Price:             200000,
				SizeDecimals:      5,
				PriceDecimals:     2,
				OrderType:         0,
				TimeInForce:       1,
				OrderExpiry:       (now + 3600) * 1000,
				Nonce:             10,
				ExpiredAt:         (now + 300) * 1000,
				IntentExpiresAt:   now + 300,
			}, vaultID); err == nil {
				t.Fatal("expected daily spend limit failure after vault reopen")
			}
		})
	}
}
