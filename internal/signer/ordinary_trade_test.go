package signer

import (
	"encoding/hex"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"strings"
	"testing"
	"time"
)

func ordinaryTradeQuote(t *testing.T, ref AccountRef, chain *onchaintest.Chain) SwapProposal {
	t.Helper()
	key, err := hex.DecodeString(ref.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	q, err := onchaintest.Client(chain).Prepare(t.Context(), onchain.Quote{Version: onchain.Version, ID: strings.Repeat("1", 32), ProfileID: "trading-analyst", InstanceID: strings.Repeat("2", 32), AccountID: strings.Repeat("3", 32), Network: ref.Network, Owner: onchain.Base58(key), RPCSHA256: strings.Repeat("4", 64), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000, CreatedAt: now, ExpiresAt: now + 45})
	if err != nil {
		t.Fatal(err)
	}
	return NewSwapProposal(ref, q)
}
func TestOrdinaryTradeTypedProposal(t *testing.T) {
	ref := AccountRef{Version: 3, SignerID: strings.Repeat("a", 32), WalletID: strings.Repeat("b", 32), Epoch: strings.Repeat("c", 32), Network: "mainnet-beta", PublicKey: strings.Repeat("d", 64)}
	p := ordinaryTradeQuote(t, ref, &onchaintest.Chain{})
	if err := ValidateSwapHistory(p); err != nil {
		t.Fatal("ordinary exact-wallet proposal unavailable", err)
	}
}
