package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"github.com/fased-ai/fased-app/internal/signer"
)

func setupManagedSwapHarness(t *testing.T, s *Store) *swapHarness {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("installed wallet custody is Linux-only")
	}
	dir := filepath.Join(t.TempDir(), "signer")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	chain := &onchaintest.Chain{}
	v, e := signer.OpenManagedSwapFixture(dir, strings.Repeat("a", 32), "mainnet-beta", onchaintest.Client(chain))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { v.Close() })
	pw := []byte("disposable-managed-onchain-password")
	if e = v.Create(pw); e != nil {
		t.Fatal(e)
	}
	ref := *v.Status().Account
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: v.Status()}, nil
	}
	s.custodyNetwork = ref.Network
	r, e := s.accountsRecord(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r.Accounts = append(r.Accounts, Account{Version: 3, ID: strings.Repeat("c", 32), Name: "Wallet", Mode: "fixture-managed", Network: ref.Network, PublicKey: ref.PublicKey, Signer: &ref})
	s.rpcAdmission = true
	if _, e = s.UpdateAccounts(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: strings.Repeat("c", 32), Revision: r.Revision + 1, Endpoint: "https://mainnet.helius-rpc.com", Network: ref.Network}); e != nil {
		t.Fatal(e)
	}
	h := &swapHarness{s, v, chain, pw, "trading-analyst"}
	h.connect()
	return h
}
func TestManagedOnchainWalletJoinsReviewedTaskResult(t *testing.T) {
	h := setupManagedSwapHarness(t, fixtureStore(t))
	review := h.quote(t)
	if review.Proposal.Account.Version != 2 || review.Proposal.Version != signer.ManagedSwapVersion {
		t.Fatal("wrong wallet authority")
	}
	if _, e := h.vault.ApproveSwap(review.Proposal); e == nil {
		t.Fatal("locked key approved")
	}
	if e := h.vault.Unlock(h.password, time.Minute); e != nil {
		t.Fatal(e)
	}
	h.submit(t, review)
	if e := h.store.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	got := h.command(t, "swap-view", SwapCommand{})
	if len(got.Runs) != 1 || got.Runs[0].Swap.State != "confirmed" || got.Tasks[0].State != "completed" || h.chain.Submissions != 1 {
		t.Fatal("joined result", got)
	}
	saveSwapEvidence(t, "managed-confirmed", got)
	if e := h.store.advanceOnchain(t.Context()); e != nil || h.chain.Submissions != 1 {
		t.Fatal("duplicate submission", e)
	}
	if !h.vault.Status().RecoveryRequired {
		t.Fatal("unrelated recovery hold released")
	}
}
func TestManagedOnchainUnknownRestartAndAccountBinding(t *testing.T) {
	h := setupManagedSwapHarness(t, fixtureStore(t))
	review := h.quote(t)
	if e := h.vault.Unlock(h.password, time.Minute); e != nil {
		t.Fatal(e)
	}
	h.submit(t, review)
	h.chain.DropSubmission = true
	h.chain.Unknown = true
	if e := h.store.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	unknown := h.command(t, "swap-view", SwapCommand{})
	if unknown.Runs[0].Swap.State != "unknown" || len(unknown.Runs[0].Swap.Signed) == 0 {
		t.Fatal("unknown original lost")
	}
	before, _ := json.Marshal(unknown.Runs[0].Swap.Signed)
	dir := h.store.dir
	h.store.Close()
	next, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	h.store = next
	h.connect()
	recovered := h.command(t, "swap-view", SwapCommand{})
	after, _ := json.Marshal(recovered.Runs[0].Swap.Signed)
	if string(before) != string(after) || h.chain.Submissions != 1 {
		t.Fatal("restart lost/replayed original")
	}
	h.chain.Unknown = false
	h.command(t, "swap-reconcile", SwapCommand{ID: unknown.Tasks[0].ID})
	if h.chain.Submissions != 1 {
		t.Fatal("reconcile resent")
	}
	saveSwapEvidence(t, "managed-restarted", h.command(t, "swap-view", SwapCommand{}))
}
func TestManagedOnchainDeclinesWatchOnlyAndChangedEpoch(t *testing.T) {
	for _, change := range []string{"watch", "epoch", "network"} {
		t.Run(change, func(t *testing.T) {
			h := setupManagedSwapHarness(t, fixtureStore(t))
			r, e := h.store.accountsRecord(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			a := &r.Accounts[len(r.Accounts)-1]
			switch change {
			case "watch":
				a.Version = 2
				a.Mode = "watch"
				a.Signer = nil
			case "epoch":
				a.Signer.Epoch = strings.Repeat("b", 32)
			case "network":
				a.Network = "devnet"
				a.Signer.Network = "devnet"
				a.RPC.Network = "devnet"
			}
			b, _ := json.Marshal(r)
			if _, e = h.store.db.ExecContext(t.Context(), "UPDATE accounts SET document=? WHERE id=1", string(b)); e != nil {
				t.Fatal(e)
			}
			view, e := h.store.Agents(t.Context(), h.profile, "")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = h.store.ChangeAgents(t.Context(), AgentCommand{Action: "swap-quote", ProfileID: h.profile, Revision: view.Revision, Swap: &SwapCommand{Request: &onchain.Request{AccountID: strings.Repeat("c", 32), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000}}}); e == nil {
				t.Fatal("changed authority accepted")
			}
			if h.chain.Submissions != 0 {
				t.Fatal("refusal submitted")
			}
		})
	}
}

func TestManagedOnchainSchema31MigrationPreservesDataAndRefusesUnprotectedState(t *testing.T) {
	s := fixtureStore(t)
	var raw string
	var rev int64
	if e := s.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&raw, &rev); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec("PRAGMA user_version=31"); e != nil {
		t.Fatal(e)
	}
	dir := s.dir
	s.Close()
	next, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	var got string
	var revision int64
	var schema int
	next.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&got, &revision)
	next.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if got != raw || revision != rev || schema != 32 {
		t.Fatal("migration changed owner records/revision")
	}
	h := setupManagedSwapHarness(t, fixtureStore(t))
	review := h.quote(t)
	if e = h.vault.Unlock(h.password, time.Minute); e != nil {
		t.Fatal(e)
	}
	h.submit(t, review)
	if _, e = h.store.db.Exec("PRAGMA user_version=31"); e != nil {
		t.Fatal(e)
	}
	unprotected := h.store.dir
	h.store.Close()
	if old, e := OpenStore(unprotected); e == nil {
		old.Close()
		t.Fatal("unprotected managed policy migrated")
	}
}

func TestManagedOnchainInstalledBridgeRejectsUntrustedConfiguration(t *testing.T) {
	s := fixtureStore(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.json")
	c := signer.Config{Version: 2, Mode: signer.ManagedFixtureMode, Network: "mainnet-beta", SignerID: strings.Repeat("a", 32), OwnerUID: os.Getuid() + 1, AppUID: os.Getuid(), SignerUID: os.Getuid() + 2, StateDir: filepath.Join(dir, "state"), SocketDir: filepath.Join(dir, "ipc")}
	b, _ := json.Marshal(c)
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	s.custodyNetwork = c.Network
	if e := s.configureInstalledSwapFixture(path, func(Account) (swapVenue, error) { return onchaintest.Client(&onchaintest.Chain{}), nil }); e == nil || s.onchain != nil {
		t.Fatal("untrusted identity became a connected wallet")
	}
}
