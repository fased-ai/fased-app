package onchain_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
)

func fixture(t *testing.T) (*onchaintest.Chain, *onchain.Client, onchain.Quote, ed25519.PrivateKey) {
	t.Helper()
	key := ed25519.NewKeyFromSeed([]byte(strings.Repeat("d", 32)))
	owner := onchain.Base58(key.Public().(ed25519.PublicKey))
	c := &onchaintest.Chain{}
	client := onchaintest.Client(c)
	now := time.Now().Unix()
	q := onchain.Quote{Version: onchain.Version, ID: strings.Repeat("1", 32), ProfileID: "trading-analyst", InstanceID: strings.Repeat("2", 32), AccountID: "wallet", Network: "mainnet-beta", Owner: owner, RPCSHA256: strings.Repeat("3", 64), InputMint: onchain.USDC, OutputMint: onchain.SOL, Amount: "12500000", SlippageBPS: 50, MaxFeeLamports: 5000, CreatedAt: now, ExpiresAt: now + 45}
	return c, client, q, key
}
func TestSwapHTTPBuildSimulationSigningAndSettlement(t *testing.T) {
	c, client, q, key := fixture(t)
	q, e := client.Prepare(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	if q.Output != "100000000" || q.MinimumOutput != "99500000" {
		t.Fatal("incorrect integer quote", q.Output, q.MinimumOutput)
	}
	message, e := onchain.Message(q)
	if e != nil {
		t.Fatal(e)
	}
	packet, e := onchain.Signed(q, ed25519.Sign(key, message))
	if e != nil {
		t.Fatal(e)
	}
	attempted, e := client.Send(t.Context(), q, packet)
	if e != nil || !attempted {
		t.Fatal(e)
	}
	r, e := client.Reconcile(t.Context(), q, packet)
	if e != nil || r.State != "confirmed" || r.Input != q.Amount || r.Output != q.Output || c.Submissions != 1 {
		t.Fatal(r, e)
	}
	for _, call := range c.Calls {
		if call == "execute" {
			t.Fatal("managed retry transport used")
		}
	}
}
func TestSwapUnknownSubmissionOnlyReconcilesOriginal(t *testing.T) {
	c, client, q, key := fixture(t)
	q, e := client.Prepare(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	message, _ := onchain.Message(q)
	packet, _ := onchain.Signed(q, ed25519.Sign(key, message))
	c.DropSubmission = true
	attempted, e := client.Send(t.Context(), q, packet)
	if !attempted || e == nil {
		t.Fatal("uncertain submission lost")
	}
	r, e := client.Reconcile(t.Context(), q, packet)
	if e != nil || r.State != "confirmed" || c.Submissions != 1 {
		t.Fatal(r, e, c.Submissions)
	}
}
func TestSwapRefusesNetworkSimulationAndChangedBuild(t *testing.T) {
	for _, mode := range []string{"network", "simulation", "build"} {
		t.Run(mode, func(t *testing.T) {
			c, client, q, _ := fixture(t)
			c.WrongNetwork = mode == "network"
			c.SimulationFails = mode == "simulation"
			c.TamperBuild = mode == "build"
			if _, e := client.Prepare(context.Background(), q); e == nil {
				t.Fatal("unsafe quote accepted")
			}
			if c.Submissions != 0 {
				t.Fatal("quote sent transaction")
			}
		})
	}
}
func TestSwapWireRefusesChangedAmountsProgramsAndExtraSigners(t *testing.T) {
	_, client, q, _ := fixture(t)
	q, e := client.Prepare(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"amount", "program", "signer", "legacy", "expiry", "signature"} {
		t.Run(mode, func(t *testing.T) {
			altered := q
			altered.Instructions = append([]onchain.Instruction(nil), q.Instructions...)
			ix := altered.Instructions[0]
			ix.Accounts = append([]onchain.Meta(nil), ix.Accounts...)
			switch mode {
			case "amount":
				altered.Amount = "1"
			case "program":
				ix.ProgramID = onchain.System
			case "signer":
				ix.Accounts[1].IsSigner = true
			case "legacy":
				data, _ := base64.StdEncoding.DecodeString(ix.Data)
				data[0] = 1
				ix.Data = base64.StdEncoding.EncodeToString(data)
			case "expiry":
				altered.ExpiresAt = altered.CreatedAt
			case "signature":
				altered.TransactionSHA256 = strings.Repeat("0", 64)
			}
			altered.Instructions[0] = ix
			altered.Digest = onchain.QuoteDigest(altered)
			if onchain.ValidateQuote(altered, q.CreatedAt) == nil {
				t.Fatal("changed transaction accepted", mode)
			}
		})
	}
}

func TestSwapSettlementRequiresCompleteConsistentErrorEvidence(t *testing.T) {
	for _, mode := range []string{"missing-status", "missing-meta", "missing-fee", "unknown-error", "malformed", "mismatch", "failed"} {
		t.Run(mode, func(t *testing.T) {
			chain, client, q, key := fixture(t)
			q, e := client.Prepare(t.Context(), q)
			if e != nil {
				t.Fatal(e)
			}
			message, _ := onchain.Message(q)
			packet, e := onchain.Signed(q, ed25519.Sign(key, message))
			if e != nil {
				t.Fatal(e)
			}
			if _, e = client.Send(t.Context(), q, packet); e != nil {
				t.Fatal(e)
			}
			chain.SettlementMode = mode
			result, e := client.Reconcile(t.Context(), q, packet)
			if mode == "failed" {
				if e != nil || result.State != "failed" {
					t.Fatal("explicit failure not reconciled", result, e)
				}
			} else if e == nil || result.State == "failed" || result.State == "confirmed" {
				t.Fatal("incomplete settlement released hold", result, e)
			}
			if chain.Submissions != 1 {
				t.Fatal("reconciliation resubmitted")
			}
		})
	}
}
