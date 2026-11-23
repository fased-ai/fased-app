package onchain_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
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

func TestSwapReconcileSettlesDroppedTransactionPastLastValidHeight(t *testing.T) {
	t.Run("held at or below LastValidHeight", func(t *testing.T) {
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
		chain.Unknown = true
		chain.BlockHeight = q.LastValidHeight
		if _, e = client.Send(t.Context(), q, packet); e != nil {
			t.Fatal(e)
		}
		r, e := client.Reconcile(t.Context(), q, packet)
		if e != nil || r.State != "unknown" {
			t.Fatalf("expected unknown at LastValidHeight, got r=%+v err=%v", r, e)
		}
	})

	t.Run("settles failed past LastValidHeight", func(t *testing.T) {
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
		chain.Unknown = true
		if _, e = client.Send(t.Context(), q, packet); e != nil {
			t.Fatal(e)
		}
		chain.BlockHeight = q.LastValidHeight + 1
		r, e := client.Reconcile(t.Context(), q, packet)
		if e != nil || r.State != "failed" || r.Slot != q.ObservedSlot || chain.Submissions != 1 {
			t.Fatalf("expected expired failure anchored to ObservedSlot, got r=%+v err=%v", r, e)
		}
	})

	t.Run("recheck catches late confirmation at LastValidHeight boundary", func(t *testing.T) {
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
		chain.Unknown = true
		if _, e = client.Send(t.Context(), q, packet); e != nil {
			t.Fatal(e)
		}
		chain.BlockHeight = q.LastValidHeight + 1
		chain.RevealAfterReads = 2
		r, e := client.Reconcile(t.Context(), q, packet)
		if e != nil || r.State != "confirmed" || chain.Submissions != 1 || chain.Reads < 2 {
			t.Fatalf("expected late confirmation via recheck, got r=%+v err=%v reads=%d", r, e, chain.Reads)
		}
	})
}

func TestSwapSettlementRecognizesSolanaRuntimeErrors(t *testing.T) {
	accepted := []any{
		"AccountInUse",
		map[string]any{"InsufficientFundsForRent": map[string]any{"account_index": 1}},
		map[string]any{"ProgramExecutionTemporarilyRestricted": map[string]any{"account_index": 0}},
		map[string]any{"DuplicateInstruction": 0},
		map[string]any{"InstructionError": []any{0, "ArithmeticOverflow"}},
		map[string]any{"InstructionError": []any{1, "AccountNotRentExempt"}},
		map[string]any{"InstructionError": []any{2, map[string]any{"BorshIoError": "bad"}}},
	}
	for _, errVal := range accepted {
		chain, client, q, key := fixture(t)
		q, e := client.Prepare(t.Context(), q)
		if e != nil {
			t.Fatal(e)
		}
		message, _ := onchain.Message(q)
		packet, _ := onchain.Signed(q, ed25519.Sign(key, message))
		if _, e = client.Send(t.Context(), q, packet); e != nil {
			t.Fatal(e)
		}
		chain.SettlementMode = "custom-err"
		chain.CustomErr = errVal
		r, e := client.Reconcile(t.Context(), q, packet)
		if e != nil || r.State != "failed" {
			t.Fatalf("expected %v to settle as failed, got r=%+v err=%v", errVal, r, e)
		}
	}
	rejected := []any{
		"InsufficientFundsForRent",
		map[string]any{"UnknownFailure": true},
	}
	for _, errVal := range rejected {
		chain, client, q, key := fixture(t)
		q, e := client.Prepare(t.Context(), q)
		if e != nil {
			t.Fatal(e)
		}
		message, _ := onchain.Message(q)
		packet, _ := onchain.Signed(q, ed25519.Sign(key, message))
		if _, e = client.Send(t.Context(), q, packet); e != nil {
			t.Fatal(e)
		}
		chain.SettlementMode = "custom-err"
		chain.CustomErr = errVal
		r, e := client.Reconcile(t.Context(), q, packet)
		if e == nil && r.State == "failed" {
			t.Fatalf("expected %v not to settle as failed, got r=%+v err=%v", errVal, r, e)
		}
	}
}

func TestSwapDynamicComputeUnitsAndBoundedPriorityFee(t *testing.T) {
	// Case 1: median priority fee (5000) within MaxFeeLamports, 210000 CU -> 252000 CU.
	c, client, q, key := fixture(t)
	c.SimulateUnits = 210000
	c.RecentPriorityFees = []uint64{2000, 10000, 5000}
	q.MaxFeeLamports = 15000
	q, e := client.Prepare(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	if q.ComputeUnits != 252000 {
		t.Fatalf("ComputeUnits=%d, want 252000", q.ComputeUnits)
	}
	if q.PriorityFeeMicroLamports != 5000 {
		t.Fatalf("PriorityFeeMicroLamports=%d, want 5000", q.PriorityFeeMicroLamports)
	}
	msg, e := onchain.Message(q)
	if e != nil {
		t.Fatal(e)
	}
	pkt, e := onchain.Signed(q, ed25519.Sign(key, msg))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.Send(t.Context(), q, pkt); e != nil {
		t.Fatal(e)
	}
	if r, e := client.Reconcile(t.Context(), q, pkt); e != nil || r.State != "confirmed" {
		t.Fatalf("reconcile failed: r=%+v err=%v", r, e)
	}

	// Case 2: low clamp (50_000) & high clamp (1_400_000) + spike priority clamping.
	cLow, clLow, qLow, _ := fixture(t)
	cLow.SimulateUnits = 1000
	qLow, e = clLow.Prepare(t.Context(), qLow)
	if e != nil || qLow.ComputeUnits != 50_000 {
		t.Fatalf("expected low clamp 50000, got %d err=%v", qLow.ComputeUnits, e)
	}

	cHigh, clHigh, qHigh, _ := fixture(t)
	cHigh.SimulateUnits = 1_300_000
	qHigh, e = clHigh.Prepare(t.Context(), qHigh)
	if e != nil || qHigh.ComputeUnits != 1_400_000 {
		t.Fatalf("expected high clamp 1400000, got %d err=%v", qHigh.ComputeUnits, e)
	}

	cSpike, clSpike, qSpike, _ := fixture(t)
	cSpike.SimulateUnits = 210000
	cSpike.RecentPriorityFees = []uint64{999_999_999}
	qSpike.MaxFeeLamports = 10000
	qSpike, e = clSpike.Prepare(t.Context(), qSpike)
	if e != nil {
		t.Fatal(e)
	}
	wantMaxMicro := (uint64(10000) - 5000) * 1_000_000 / uint64(qSpike.ComputeUnits)
	if qSpike.PriorityFeeMicroLamports != wantMaxMicro || qSpike.FeeLamports > 10000 {
		t.Fatalf("expected priority clamped to %d and fee<=10000, got micro=%d fee=%d", wantMaxMicro, qSpike.PriorityFeeMicroLamports, qSpike.FeeLamports)
	}
}

func TestSwapWrapAndUnwrapNativeSOLAndNegativeChecks(t *testing.T) {
	// SOL -> USDC with MissingInputATA + WrapAndUnwrapSol.
	c, client, q, key := fixture(t)
	c.MissingInputATA = true
	q.InputMint = onchain.SOL
	q.OutputMint = onchain.USDC
	q.Amount = "100000000"
	q.WrapAndUnwrapSol = true
	q, e := client.Prepare(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	if len(q.Instructions) != 5 || q.RentLamports != 2_039_280 {
		t.Fatalf("expected 5 instructions (ATA, Transfer, SyncNative, Swap, Close) and 1 ATA rent, got %d ixs rent=%d", len(q.Instructions), q.RentLamports)
	}
	msg, e := onchain.Message(q)
	if e != nil {
		t.Fatal(e)
	}
	pkt, e := onchain.Signed(q, ed25519.Sign(key, msg))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.Send(t.Context(), q, pkt); e != nil {
		t.Fatal(e)
	}
	if r, e := client.Reconcile(t.Context(), q, pkt); e != nil || r.State != "confirmed" || r.Input != q.Amount || r.Output != q.Output {
		t.Fatalf("native SOL->USDC reconcile mismatch: r=%+v err=%v", r, e)
	}

	// USDC -> SOL with WrapAndUnwrapSol.
	c2, client2, q2, key2 := fixture(t)
	q2.InputMint = onchain.USDC
	q2.OutputMint = onchain.SOL
	q2.Amount = "12500000"
	q2.WrapAndUnwrapSol = true
	q2, e = client2.Prepare(t.Context(), q2)
	if e != nil {
		t.Fatal(e)
	}
	msg2, _ := onchain.Message(q2)
	pkt2, _ := onchain.Signed(q2, ed25519.Sign(key2, msg2))
	if _, e = client2.Send(t.Context(), q2, pkt2); e != nil {
		t.Fatal(e)
	}
	if r, e := client2.Reconcile(t.Context(), q2, pkt2); e != nil || r.State != "confirmed" || r.Input != q2.Amount || r.Output != q2.Output || c2.Submissions != 1 {
		t.Fatalf("USDC->native SOL reconcile mismatch: r=%+v err=%v", r, e)
	}

	// Negative instruction checks.
	attacker := onchain.Base58(make([]byte, 32))
	wsolATA, _ := onchain.TokenAccount(q.Owner, onchain.SOL)
	swapIx := q.Instructions[3]

	// 1. System transfer to non-wSOL destination.
	badTransfer := make([]byte, 12)
	binary.LittleEndian.PutUint32(badTransfer[:4], 2)
	binary.LittleEndian.PutUint64(badTransfer[4:12], 100000000)
	badQ := q
	badQ.Instructions = []onchain.Instruction{
		{
			ProgramID: onchain.System,
			Accounts: []onchain.Meta{
				{Pubkey: q.Owner, IsSigner: true, IsWritable: true},
				{Pubkey: attacker, IsSigner: false, IsWritable: true},
			},
			Data: base64.StdEncoding.EncodeToString(badTransfer),
		},
		swapIx,
	}
	if onchain.ValidateInstructions(badQ) == nil {
		t.Fatal("accepted System transfer to non-wSOL destination")
	}

	// 2. Wrong CloseAccount recipient.
	badClose := q
	badClose.Instructions = []onchain.Instruction{
		swapIx,
		{
			ProgramID: onchain.Token,
			Accounts: []onchain.Meta{
				{Pubkey: wsolATA, IsSigner: false, IsWritable: true},
				{Pubkey: attacker, IsSigner: false, IsWritable: true},
				{Pubkey: q.Owner, IsSigner: true, IsWritable: false},
			},
			Data: base64.StdEncoding.EncodeToString([]byte{9}),
		},
	}
	if onchain.ValidateInstructions(badClose) == nil {
		t.Fatal("accepted CloseAccount sending rent to non-owner")
	}

	// 3. System / Token CloseAccount rejected when WrapAndUnwrapSol == false.
	noWrap := q
	noWrap.WrapAndUnwrapSol = false
	if onchain.ValidateInstructions(noWrap) == nil {
		t.Fatal("accepted System/Token instructions with WrapAndUnwrapSol=false")
	}
}
