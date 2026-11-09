package ownerclient

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"github.com/fased-ai/fased-app/internal/signer"
	"os"
	"strings"
	"testing"
	"time"
)

func tradeIntent(t *testing.T) signer.OwnerIntent {
	t.Helper()
	ref := signer.AccountRef{Version: 3, SignerID: strings.Repeat("a", 32), WalletID: strings.Repeat("b", 32), Epoch: strings.Repeat("c", 32), Network: "mainnet-beta", PublicKey: strings.Repeat("d", 64)}
	pub, _ := hex.DecodeString(ref.PublicKey)
	now := time.Now().Unix() - 90
	q, e := onchaintest.Client(&onchaintest.Chain{}).Prepare(t.Context(), onchain.Quote{Version: onchain.Version, ID: strings.Repeat("1", 32), ProfileID: "trading-analyst", InstanceID: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)), AccountID: strings.Repeat("3", 32), Network: ref.Network, Owner: onchain.Base58(pub), RPCSHA256: strings.Repeat("4", 64), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000, CreatedAt: now, ExpiresAt: now + 45})
	if e != nil {
		t.Fatal(e)
	}
	i, e := signer.NewTradeOwnerIntent(signer.NewSwapProposal(ref, q))
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func TestTradeResumeExpiredOriginalAndSeparateKeyDraft(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	i := tradeIntent(t)
	p, _ := i.TradeProposal()
	link, e := TradeLink(p, "")
	if e != nil {
		t.Fatal(e)
	}
	got, _, e := Resume(root, link)
	if e != nil || got != i {
		t.Fatal("expired original cannot reopen", e)
	}
	if e = MarkDispatched(root, got); e != nil {
		t.Fatal(e)
	}
	got, _, e = Resume(root, link)
	if e != nil || got != i {
		t.Fatal("ambiguous draft cannot resume", e)
	}
	other := p
	other.Quote.ID = strings.Repeat("9", 32)
	other.Quote.Digest = onchain.QuoteDigest(other.Quote)
	other.Digest = signer.SwapProposalDigest(other)
	next, _ := TradeLink(other, "")
	if _, _, e = Resume(root, next); e == nil {
		t.Fatal("overwrote ambiguous dispatch")
	}
	if e = FinishTrade(root, i); e != nil {
		t.Fatal(e)
	}
	if _, _, e = Resume(root, next); e != nil {
		t.Fatal(e)
	}
	key, _ := Link(i.InstanceID, i.Network, "setup", "")
	if _, _, e = Resume(root, key); e != nil {
		t.Fatal("trade blocks key recovery", e)
	}
	for _, bad := range []string{link + "&proposal=x", strings.Replace(link, "action=approve-swap", "action=setup", 1), strings.Replace(link, "network=mainnet-beta", "network=devnet", 1)} {
		if _, _, e = ParseLink(bad); e == nil {
			t.Fatal("foreign/noncanonical intent admitted")
		}
	}
}
func TestTradeResponseRejectsAuthorityAndMutation(t *testing.T) {
	i := tradeIntent(t)
	p, _ := i.TradeProposal()
	st := signer.Status{Custody: &signer.CustodyAdmission{Version: 1, Class: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.ControlledCustodyProfile, Origin: "generated"}, Account: &p.Account, FixtureOnly: true, AdmissionHeld: true, RecoveryRequired: true, Locked: true}
	c := &Client{Intent: i, Peer: Peer{SignerID: p.Account.SignerID}}
	in := signer.OwnerInput{Intent: i, SignerID: p.Account.SignerID, Phase: "inspect"}
	result := signer.OwnerResult{Intent: i, SignerID: p.Account.SignerID, Status: st.PublicView(), ReadOnly: true}
	raw, _ := json.Marshal(result)
	if _, e := c.validateTradeResponse(raw, in); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*signer.OwnerResult){func(r *signer.OwnerResult) { r.ReadOnly = false }, func(r *signer.OwnerResult) { r.Status.WalletSigner = true }, func(r *signer.OwnerResult) { r.Backup = []byte("private") }, func(r *signer.OwnerResult) { r.Intent.InstanceID = "foreign" }} {
		bad := result
		change(&bad)
		raw, _ = json.Marshal(bad)
		if _, e := c.validateTradeResponse(raw, in); e == nil {
			t.Fatal("unsafe completion")
		}
	}
	in.Unlock = []byte("not allowed in inspect")
	if e := in.Validate(); e == nil {
		t.Fatal("inspect accepts secret")
	}
	in.Unlock = nil
	in.Phase = "approve"
	in.Unlock = []byte("disposable password")
	in.Confirmation = p.Digest
	in.BackupPassword = []byte("no backup")
	if e := in.Validate(); e == nil {
		t.Fatal("mixed wallet/trade credentials")
	}
}
