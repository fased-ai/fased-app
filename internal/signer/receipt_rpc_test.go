//go:build linux

package signer

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
)

func TestReceiptRPCCredentialsOwnerEncryptionLeaseAndRestart(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	chain := &onchaintest.Chain{}
	var borrowed []byte
	factory := func(a onchain.CredentialAccess) (SwapObserver, error) {
		key, e := a.Borrow(context.Background(), "rpc")
		if e != nil {
			return nil, e
		}
		borrowed = key
		if string(key) != strings.Repeat("x", 32) {
			t.Fatal("wrong secret")
		}
		clear(key)
		return onchaintest.Client(chain), nil
	}
	v, e := OpenManagedSwapCredentialsFixture(dir, strings.Repeat("a", 32), "mainnet-beta", factory)
	if e != nil {
		t.Fatal(e)
	}
	pw := []byte("disposable-reader-password")
	if e = v.Create(pw); e != nil {
		t.Fatal(e)
	}
	s, e := v.ProvisionReceiptRPC("https://mainnet.helius-rpc.com", []byte(strings.Repeat("x", 32)), pw)
	if e != nil {
		t.Fatal(e)
	}
	if !s.Saved || !s.Locked || s.Ref == "" {
		t.Fatal(s)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "operations.log"))
	if e != nil || bytes.Contains(raw, []byte(strings.Repeat("x", 32))) || bytes.Contains(raw, pw) {
		t.Fatal("plaintext journal", e)
	}
	if v.Status().Locked != true {
		t.Fatal("credential provisioning unlocked wallet")
	}
	if _, e = v.UnlockReceiptRPC([]byte("wrong-disposable-password"), time.Minute); e == nil {
		t.Fatal("wrong password")
	}
	s, e = v.UnlockReceiptRPC(pw, time.Minute)
	if e != nil || s.Locked || v.Status().Locked != true {
		t.Fatal("read-only lease unlocked wallet", s, e)
	}
	if e = v.Unlock(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	// Wallet unlock revokes the prior read lease; owner renews the read path separately.
	if _, e = v.UnlockReceiptRPC(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	public, _ := hex.DecodeString(ref.PublicKey)
	now := time.Now().Unix()
	q, e := onchaintest.Client(chain).Prepare(t.Context(), onchain.Quote{Version: onchain.Version, ID: strings.Repeat("1", 32), ProfileID: "trading-analyst", InstanceID: strings.Repeat("2", 32), AccountID: strings.Repeat("3", 32), Network: ref.Network, Owner: onchain.Base58(public), RPCSHA256: strings.Repeat("4", 64), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000, CreatedAt: now, ExpiresAt: now + 45})
	if e != nil {
		t.Fatal(e)
	}
	p := NewCredentialSwapProposal(ref, q, ReceiptRPCBinding{Version: 1, Ref: s.Ref, Origin: s.Origin})
	for _, kind := range []string{"ref", "origin", "version"} {
		forged := clone(p)
		switch kind {
		case "ref":
			forged.ReceiptRPC.Ref = strings.Repeat("9", 32)
		case "origin":
			forged.ReceiptRPC.Origin = "https://solana-mainnet.g.alchemy.com"
		case "version":
			forged.ReceiptRPC.Version = 2
		}
		forged.Digest = SwapProposalDigest(forged)
		if _, e = v.ApproveSwap(forged); e == nil {
			t.Fatal("wrong reader binding admitted", kind)
		}
	}
	if _, e = v.ApproveSwap(p); e != nil {
		t.Fatal(e)
	}
	signed, e := v.SignSwap(q.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = onchaintest.Client(chain).Send(t.Context(), q, signed.Signed); e != nil {
		t.Fatal(e)
	}
	v.Lock()
	if _, e = v.ReconcileSwap(t.Context(), q.ID); e == nil {
		t.Fatal("locked receipt reader accepted")
	}
	if _, e = v.UnlockReceiptRPC(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	result, e := v.ReconcileSwap(t.Context(), q.ID)
	if e != nil || result.State != "confirmed" {
		t.Fatal(result, e)
	}
	if !bytes.Equal(borrowed, make([]byte, len(borrowed))) {
		t.Fatal("borrowed bytes not wiped")
	}
	if e = v.Close(); e != nil {
		t.Fatal(e)
	}
	v, e = OpenManagedSwapCredentialsFixture(dir, strings.Repeat("a", 32), "mainnet-beta", factory)
	if e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	after := v.Status()
	if after.ReceiptRPC == nil || !after.ReceiptRPC.Saved || !after.ReceiptRPC.Locked || !after.Locked || !after.RecoveryRequired {
		t.Fatal(after)
	}
	if _, e = v.SignSwap(q.ID); e == nil {
		t.Fatal("restart restored signing")
	}
	if got := execute(v, Request{Action: "receipt-rpc-unlock", Password: pw, UnlockSeconds: 60}, false); got.Error == "" {
		t.Fatal("app renewed reader")
	}
}

func TestReceiptRPCControlledHTTPSVerifiesOriginalAndRetainsHold(t *testing.T) {
	fixture := onchaintest.NewHTTPS(t, "https://mainnet.helius-rpc.com")
	factory := func(a onchain.CredentialAccess) (SwapObserver, error) {
		return onchain.NewControlledAuthenticatedVerifier(fixture.Transport, a)
	}
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	v, e := OpenManagedSwapCredentialsFixture(dir, strings.Repeat("b", 32), "mainnet-beta", factory)
	if e != nil {
		t.Fatal(e)
	}
	pw := []byte("disposable-https-reader-password")
	if e = v.Create(pw); e != nil {
		t.Fatal(e)
	}
	binding, e := v.ProvisionReceiptRPC(fixture.RPCOrigin, []byte(fixture.RPCKey), pw)
	if e != nil {
		t.Fatal(e)
	}
	if e = v.Unlock(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	if _, e = v.UnlockReceiptRPC(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	public, _ := hex.DecodeString(ref.PublicKey)
	now := time.Now().Unix()
	app, e := onchain.NewControlledAuthenticatedClient(fixture.Transport, onchain.CredentialAccess{RPCOrigin: fixture.RPCOrigin, Network: ref.Network, Valid: func() error { return nil }, Borrow: func(ctx context.Context, purpose string) ([]byte, error) {
		if purpose == "rpc" {
			return []byte(fixture.RPCKey), nil
		}
		return []byte(fixture.JupiterKey), nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	q, e := app.Prepare(t.Context(), onchain.Quote{Version: onchain.Version, ID: strings.Repeat("5", 32), ProfileID: "trading-analyst", InstanceID: strings.Repeat("2", 32), AccountID: strings.Repeat("3", 32), Network: ref.Network, Owner: onchain.Base58(public), RPCSHA256: strings.Repeat("4", 64), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000, CreatedAt: now, ExpiresAt: now + 45})
	if e != nil {
		t.Fatal(e)
	}
	p := NewCredentialSwapProposal(ref, q, ReceiptRPCBinding{Version: 1, Ref: binding.Ref, Origin: binding.Origin})
	if _, e = v.ApproveSwap(p); e != nil {
		t.Fatal(e)
	}
	signed, e := v.SignSwap(q.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = app.Send(t.Context(), q, signed.Signed); e != nil {
		t.Fatal(e)
	}
	original := bytes.Clone(signed.Signed)
	for _, mode := range []string{"missing-status", "missing-meta", "missing-fee", "mismatch", "malformed"} {
		fixture.Chain.SettlementMode = mode
		if result, e := v.ReconcileSwap(t.Context(), q.ID); e == nil && result.State != "signed" {
			t.Fatalf("%s released hold", mode)
		}
		receipt, e := v.SwapReceipt(q.ID)
		if e != nil || receipt.State != "signed" || !bytes.Equal(receipt.Signed, original) {
			t.Fatal("lost original hold", mode, e)
		}
	}
	fixture.Chain.SettlementMode = ""
	if e = v.Close(); e != nil {
		t.Fatal(e)
	}
	fallback, e := OpenManagedSwapFixture(dir, strings.Repeat("b", 32), "mainnet-beta", onchaintest.Client(fixture.Chain))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = fallback.ReconcileSwap(t.Context(), q.ID); e == nil {
		t.Fatal("fixed oracle substituted for credential reader")
	}
	if fallback.Status().SwapReady {
		t.Fatal("fixed oracle restored readiness")
	}
	if e = fallback.Unlock(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	legacyQuote := q
	legacyQuote.ID = strings.Repeat("6", 32)
	legacyQuote.Digest = onchain.QuoteDigest(legacyQuote)
	legacy := NewSwapProposal(ref, legacyQuote)
	if legacy.ReceiptRPC != nil || ValidateSwapHistory(legacy) != nil {
		t.Fatal("invalid legacy refusal fixture")
	}
	if fallback.validReceiptRPCBinding(legacy) == nil {
		t.Fatal("credential history downgraded to legacy binding")
	}
	if _, e = fallback.ApproveSwap(legacy); e == nil {
		t.Fatal("legacy proposal bypassed reader factory")
	}
	if _, e = fallback.SignSwap(q.ID); e == nil {
		t.Fatal("fixed observer restored signing with retained credential history")
	}
	fallback.Close()
	v, e = OpenManagedSwapCredentialsFixture(dir, strings.Repeat("b", 32), "mainnet-beta", factory)
	if e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	if _, e = v.ReconcileSwap(t.Context(), q.ID); e == nil {
		t.Fatal("restart retained reader")
	}
	if _, e = v.UnlockReceiptRPC(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	before := len(fixture.Requests())
	result, e := v.ReconcileSwap(t.Context(), q.ID)
	if e != nil || result.State != "confirmed" || !bytes.Equal(result.Signed, original) {
		t.Fatal(result, e)
	}
	calls := fixture.Requests()[before:]
	if len(calls) != 2 || calls[0] != "getSignatureStatuses" || calls[1] != "getTransaction" {
		t.Fatal("signer called non-reader operations", calls)
	}
	if fixture.Chain.Submissions != 1 || !v.Status().Locked || !v.Status().RecoveryRequired {
		t.Fatal("reconciliation changed authority", v.Status())
	}
}

func TestReceiptRPCRefusesWrongBindingRotationAndExpiry(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	chain := &onchaintest.Chain{}
	factory := func(a onchain.CredentialAccess) (SwapObserver, error) { return onchaintest.Client(chain), nil }
	v, e := OpenManagedSwapCredentialsFixture(dir, strings.Repeat("d", 32), "mainnet-beta", factory)
	if e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	pw := []byte("disposable-reader-rotation-password")
	v.Create(pw)
	for _, origin := range []string{"https://devnet.helius-rpc.com", "http://mainnet.helius-rpc.com", "https://mainnet.helius-rpc.com?api-key=secret", "https://127.0.0.1"} {
		if _, e = v.ProvisionReceiptRPC(origin, []byte(strings.Repeat("x", 32)), pw); e == nil {
			t.Fatal("origin admitted", origin)
		}
	}
	first, e := v.ProvisionReceiptRPC("https://mainnet.helius-rpc.com", []byte(strings.Repeat("x", 32)), pw)
	if e != nil {
		t.Fatal(e)
	}
	if e = v.Unlock(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	if _, e = v.UnlockReceiptRPC(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	lease := v.receiptRPCLease
	second, e := v.ProvisionReceiptRPC("https://mainnet.helius-rpc.com", []byte(strings.Repeat("y", 32)), pw)
	if e != nil || second.Ref == first.Ref || lease.valid() == nil || !v.Status().Locked {
		t.Fatal("rotation retained authority", e)
	}
	if _, e = v.UnlockReceiptRPC(pw, MaxReceiptRPCLease+time.Second); e == nil {
		t.Fatal("unbounded lease")
	}
	now := time.Now()
	v.now = func() time.Time { return now }
	if _, e = v.UnlockReceiptRPC(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	lease = v.receiptRPCLease
	now = now.Add(2 * time.Minute)
	if lease.valid() == nil || len(lease.key) != 0 {
		t.Fatal("expired plaintext retained")
	}
	if got := execute(v, Request{Action: "receipt-rpc-save", RPCOrigin: "https://mainnet.helius-rpc.com", RPCKey: []byte(strings.Repeat("x", 32)), Password: bytes.Clone(pw)}, false); got.Error == "" {
		t.Fatal("app provisioned credential")
	}
	if got := execute(v, Request{Action: "receipt-rpc-unlock", Password: bytes.Clone(pw), UnlockSeconds: 60, PublicInput: PublicInput{OperationID: strings.Repeat("a", 32)}}, true); got.Error == "" {
		t.Fatal("mixed owner request")
	}
}

func TestReceiptRPCCiphertextBindingAndImportedKeyHold(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	chain := &onchaintest.Chain{}
	v, e := OpenManagedSwapCredentialsFixture(dir, strings.Repeat("f", 32), "mainnet-beta", func(onchain.CredentialAccess) (SwapObserver, error) { return onchaintest.Client(chain), nil })
	if e != nil {
		t.Fatal(e)
	}
	defer v.Close()
	pw := []byte("disposable-imported-reader-password")
	seed := bytes.Repeat([]byte{9}, 32)
	defer clear(seed)
	if e = v.importManagedSeed(seed, pw); e != nil {
		t.Fatal(e)
	}
	if _, e = v.ProvisionReceiptRPC("https://mainnet.helius-rpc.com", []byte(strings.Repeat("x", 32)), pw); e != nil {
		t.Fatal(e)
	}
	original := clone(v.ledger.receiptRPC)
	for _, kind := range []string{"purpose", "ref", "origin", "epoch", "ciphertext"} {
		altered := clone(original)
		switch kind {
		case "purpose":
			altered.Purpose = "jupiter"
		case "ref":
			altered.Ref = strings.Repeat("1", 32)
		case "origin":
			altered.Origin = "https://solana-mainnet.g.alchemy.com"
		case "epoch":
			altered.Account.Epoch = strings.Repeat("2", 32)
		case "ciphertext":
			altered.Ciphertext[0] ^= 1
		}
		v.ledger.receiptRPC = altered
		if _, e = v.UnlockReceiptRPC(pw, time.Minute); e == nil {
			t.Fatal("altered ciphertext context admitted", kind)
		}
	}
	v.ledger.receiptRPC = original
	if e = v.Unlock(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	if _, e = v.UnlockReceiptRPC(pw, time.Minute); e != nil {
		t.Fatal(e)
	}
	status := v.Status()
	if !status.RecoveryRequired || status.SwapReady {
		t.Fatal("read credential restored imported signing authority", status)
	}
	if _, e = v.SignSwap(strings.Repeat("1", 32)); e == nil {
		t.Fatal("imported signer became ready")
	}
}
