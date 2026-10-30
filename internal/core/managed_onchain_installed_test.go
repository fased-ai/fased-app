//go:build linux

package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"github.com/fased-ai/fased-app/internal/signer"
)

// This fixed inert oracle exercises installed transport/identity, not real RPC
// acceptance. The verified RPC observer is covered by the coupled signer tests.
// No production executable includes this fixture implementation.
type installedSwapOracle struct{ network string }

func (o installedSwapOracle) Reconcile(_ context.Context, q onchain.Quote, packet []byte) (onchain.Observation, error) {
	if q.Network != o.network || q.Blockhash != onchain.ControlledBlockhash {
		return onchain.Observation{}, errors.New("controlled original required")
	}
	if _, e := onchain.VerifyPacket(q, packet); e != nil {
		return onchain.Observation{}, e
	}
	return onchain.Observation{State: "confirmed", Input: q.Amount, Output: q.Output, Slot: q.ObservedSlot + 1}, nil
}

// Invoked only by the existing disposable Ubuntu installer harness. The OS
// changes credentials before this test executable starts; project code is never root.
func TestOnchainInstalledAdapterDriver(t *testing.T) {
	root := os.Getenv("FASED_INSTALLED_ONCHAIN_ROOT")
	if root == "" {
		t.Skip("explicit disposable Ubuntu UID harness only")
	}
	if os.Getuid() <= 0 || os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Fatal("disposable CI only")
	}
	c, e := signer.LoadConfig(filepath.Join(root, "identity.json"))
	if e != nil {
		t.Fatal(e)
	}
	if c.Version != 2 {
		t.Fatal("network-bound installed fixture required")
	}
	enc := json.NewEncoder(os.Stdout)
	if os.Getenv("FASED_INSTALLED_ONCHAIN_ROLE") == "signer" {
		if os.Getuid() != c.SignerUID {
			t.Fatal("wrong signer UID")
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		server, e := signer.ServeControlledSwapFixture(ctx, c, installedSwapOracle{c.Network})
		if e != nil {
			t.Fatal(e)
		}
		defer server.Close()
		enc.Encode(map[string]any{"event": "ready", "uid": os.Getuid()})
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			if scanner.Text() == "stop" {
				return
			}
		}
		return
	}
	if os.Getenv("FASED_INSTALLED_ONCHAIN_ROLE") != "app" || os.Getuid() != c.AppUID {
		t.Fatal("wrong app UID")
	}
	s, e := OpenStore(filepath.Join(root, "app-state"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.configureSigner(filepath.Join(root, "identity.json")); e != nil {
		t.Fatal(e)
	}
	chain := &onchaintest.Chain{WrongNetwork: c.Network == "devnet"}
	if e = s.configureInstalledSwapFixture(filepath.Join(root, "identity.json"), func(Account) (swapVenue, error) { return onchaintest.Client(chain), nil }); e != nil {
		t.Fatal(e)
	}
	response, e := s.swapSigner(t.Context(), signer.Request{Action: "status"})
	if e != nil || response.Status.Account == nil || !response.Status.SwapReady {
		t.Fatal("installed signer unavailable", e)
	}
	ref := *response.Status.Account
	records, e := s.accountsRecord(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	id := ""
	for _, a := range records.Accounts {
		if a.Signer != nil && *a.Signer == ref {
			id = a.ID
			break
		}
	}
	if id == "" {
		id = randomTokenHex()
		records.Accounts = append(records.Accounts, Account{Version: 3, ID: id, Name: "Wallet", Mode: "fixture-managed", Network: ref.Network, PublicKey: ref.PublicKey, Signer: &ref})
		if _, e = s.UpdateAccounts(t.Context(), records); e != nil {
			t.Fatal(e)
		}
	}
	records, e = s.accountsRecord(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	endpoint := installedSwapRPCEndpoint(c.Network)
	s.rpcAdmission = true
	if _, e = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: id, Revision: records.Revision, Endpoint: endpoint, Network: c.Network}); e != nil {
		t.Fatal(e)
	}
	view, e := s.Agents(t.Context(), "trading-analyst", "")
	if e != nil {
		t.Fatal(e)
	}
	review, e := s.ChangeAgents(t.Context(), AgentCommand{Action: "swap-quote", ProfileID: "trading-analyst", Revision: view.Revision, Swap: &SwapCommand{Request: &onchain.Request{AccountID: id, InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000}}})
	if e != nil || review.Onchain == nil || review.Onchain.Review == nil {
		t.Fatal("installed quote", e)
	}
	r := review.Onchain.Review
	enc.Encode(map[string]any{"event": "proposal", "uid": os.Getuid(), "proposal": r.Proposal})
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || scanner.Text() != "approved" {
		t.Fatal("owner consent not received")
	}
	if _, e = s.ChangeAgents(t.Context(), AgentCommand{Action: "swap-submit", ProfileID: "trading-analyst", Revision: review.Revision, Swap: &SwapCommand{ID: r.Proposal.Quote.ID, Digest: r.Digest}}); e != nil {
		t.Fatal(e)
	}
	if e = s.advanceOnchain(t.Context()); e != nil {
		t.Fatal(e)
	}
	result, e := s.onchainView(t.Context(), "trading-analyst", "")
	if e != nil || len(result.Runs) == 0 {
		t.Fatal(e)
	}
	run := result.Runs[len(result.Runs)-1]
	if run.Swap.State != "confirmed" || chain.Submissions != 1 || run.Swap.ProposalDigest != r.Proposal.Digest {
		t.Fatal("installed result mismatch")
	}
	if e = s.advanceOnchain(t.Context()); e != nil || chain.Submissions != 1 {
		t.Fatal("duplicate submission", e)
	}
	enc.Encode(map[string]any{"event": "result", "uid": os.Getuid(), "submissions": chain.Submissions, "run": run, "liveExecution": false})
}

func installedSwapRPCEndpoint(network string) string {
	if network == "devnet" {
		return "https://devnet.helius-rpc.com"
	}
	return "https://mainnet.helius-rpc.com"
}
func TestInstalledSwapRPCSelectionUsesAdmittedProviders(t *testing.T) {
	for _, network := range []string{"devnet", "mainnet-beta"} {
		t.Run(network, func(t *testing.T) {
			s := fixtureStore(t)
			providerAccount(t, s, installedSwapRPCEndpoint(network), network)
		})
	}
}
