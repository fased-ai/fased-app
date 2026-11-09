package core

import (
	"bytes"
	"context"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"github.com/fased-ai/fased-app/internal/signer"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOrdinaryOnchainReviewedTaskReceiptAndRestart(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ordinary owner custody is Linux-only")
	}
	s := fixtureStore(t)
	dir := filepath.Join(t.TempDir(), "signer")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	v, err := signer.OpenControlledOwnerCustody(dir, strings.Repeat("a", 32), "mainnet-beta")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { v.Close() }()
	pw := []byte("disposable ordinary core trade password")
	chain := &onchaintest.Chain{}
	if err = v.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(chain)); err != nil {
		t.Fatal(err)
	}
	if err = v.Create(pw); err != nil {
		t.Fatal(err)
	}
	ref := *v.Status().Account
	attach := func(s *Store) {
		s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
			return signer.Response{Status: v.Status()}, nil
		}
		s.custodyOwnerSupported = true
		s.custodyNetwork = ref.Network
	}
	attach(s)
	records, err := s.accountsRecord(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	records.Accounts = append(records.Accounts, Account{Version: 4, ID: strings.Repeat("c", 32), Name: "Wallet", Mode: "managed", Network: ref.Network, PublicKey: ref.PublicKey, Signer: &ref})
	s.rpcAdmission = true
	saved, err := s.UpdateAccounts(t.Context(), records)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: strings.Repeat("c", 32), Revision: saved.Record.Revision, Endpoint: "https://mainnet.helius-rpc.com", Network: ref.Network}); err != nil {
		t.Fatal(err)
	}
	h := &swapHarness{s, v, chain, pw, "trading-analyst"}
	h.connect()
	p := h.quote(t)
	if p.Proposal.Version != signer.OrdinarySwapVersion || p.Proposal.Account != ref {
		t.Fatal("ordinary proposal lost identity")
	}
	if _, err = v.ApproveSwap(p.Proposal); err == nil {
		t.Fatal("locked owner approved")
	}
	if err = v.Unlock(pw, time.Minute); err != nil {
		t.Fatal(err)
	}
	h.submit(t, p)
	chain.Unknown = true
	if err = h.store.advanceOnchain(t.Context()); err != nil {
		t.Fatal(err)
	}
	result := h.command(t, "swap-view", SwapCommand{})
	original := append([]byte(nil), result.Runs[0].Swap.Signed...)
	if result.Runs[0].Swap.State != "unknown" || chain.Submissions != 1 {
		t.Fatal("unknown original not held")
	}
	stateDir := h.store.dir
	h.store.Close()
	v.Close()
	v, err = signer.OpenControlledOwnerCustody(dir, ref.SignerID, ref.Network)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.ConfigureControlledOrdinarySwapObserver(onchaintest.Client(chain)); err != nil {
		t.Fatal(err)
	}
	h.vault = v
	h.store, err = OpenStore(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer h.store.Close()
	attach(h.store)
	h.connect()
	chain.Unknown = false
	settled := h.command(t, "swap-reconcile", SwapCommand{ID: result.Tasks[0].ID})
	if settled.Runs[0].Swap.State != "confirmed" || !bytes.Equal(original, settled.Runs[0].Swap.Signed) || chain.Submissions != 1 || !v.Status().Locked || !v.Status().RecoveryRequired {
		t.Fatal("ordinary receipt/restart changed authority or resubmitted")
	}
	if err = h.store.advanceOnchain(t.Context()); err != nil || chain.Submissions != 1 {
		t.Fatal("replayed original", err)
	}
}
