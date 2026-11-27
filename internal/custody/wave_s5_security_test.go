package custody

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

func TestWaveS5GlobalFreezeAndSkillGrantAndBoundedAutoEnforcement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, err := NewService(Config{
		DataDir:          t.TempDir(),
		FixtureMode:      true,
		FastKDF:          true,
		RequireOwnerAuth: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	if err := svc.ConfigureOwnerAuth(ctx, "WaveS5OwnerPass!2026"); err != nil {
		t.Fatal(err)
	}
	sock, err := StartSocketServer(svc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sock.Close(context.Background()) })

	client, err := NewSocketClient(sock.SocketPath())
	if err != nil {
		t.Fatal(err)
	}

	w, err := svc.CreateNativeWallet(ctx, "WaveS5 Bot", RoleAgent, OfficialMainnetRPC)
	if err != nil {
		t.Fatal(err)
	}
	destPub, _, _ := ed25519.GenerateKey(rand.Reader)
	destAddr := Base58Encode(destPub)

	pol := w.Policy
	pol.ApprovalMode = ApprovalBoundedAuto
	pol.AutoApproveMaxUSD = 10 // $10 cap; 0.1 SOL (~$25 conservative) must exceed it
	pol.DailyLossCapUSD = 200
	pol.Operations = []string{"solana.nativeTransfer"}
	pol.Programs = []string{SolanaSystemProgram, SolanaComputeBudget}
	pol.Destinations = []string{destAddr}
	pol.SOLCap.MaxPerTxRaw = 500_000_000
	pol.SOLCap.MaxDailyRaw = 1_000_000_000
	w, err = svc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatal(err)
	}
	w, err = svc.UpdateConnections(ctx, w.ID, WalletConnections{
		AllowTradeBots: true,
		AgentIDs:       []string{"agent-1"},
		SkillGrants: []SkillGrant{
			{SkillID: "empty-actions", Actions: []string{}, ScheduledAllowed: true},
			{SkillID: "manual-only", Actions: []string{"solana.nativeTransfer"}, ScheduledAllowed: false},
			{SkillID: "tight-usd", Actions: []string{"solana.nativeTransfer"}, MaxPerTxUSD: 5, ScheduledAllowed: true},
		},
	}, "owner")
	if err != nil {
		t.Fatal(err)
	}

	// Empty SkillGrant.Actions MUST fail closed.
	if _, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:     w.ID,
		Operation:    "solana.nativeTransfer",
		Destination:  destAddr,
		AmountHuman:  "0.01",
		IsBotOrAuto:  true,
		SkillGrantID: "empty-actions",
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied for empty SkillGrant.Actions, got %v", err)
	}

	// SkillGrant.ScheduledAllowed == false MUST fail closed in PrepareReview when IsBotOrAuto == true.
	if _, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:     w.ID,
		Operation:    "solana.nativeTransfer",
		Destination:  destAddr,
		AmountHuman:  "0.01",
		IsBotOrAuto:  true,
		SkillGrantID: "manual-only",
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied for ScheduledAllowed=false, got %v", err)
	}

	// Review exceeding AutoApproveMaxUSD ($25 > $10) MUST require owner proof even in bounded_auto.
	overCapRev, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.nativeTransfer",
		Destination: destAddr,
		AmountHuman: "0.1",
		IsBotOrAuto: true,
		AgentID:     "agent-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExecuteNativeReview(ctx, w.ID, overCapRev.ID, "agent:agent-1"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected ErrOwnerAuthRequired when EstimatedUSD exceeds AutoApproveMaxUSD, got %v", err)
	}

	// Post-prepare connection revocation invalidates in-flight review via ConnectionsHash.
	smallRev, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.nativeTransfer",
		Destination: destAddr,
		AmountHuman: "0.01", // $2.50 <= $10
		IsBotOrAuto: true,
		AgentID:     "agent-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateConnections(ctx, w.ID, WalletConnections{
		AllowTradeBots: true,
		AgentIDs:       []string{"agent-1"},
		AllowedVenues:  []string{"jupiter"}, // changed connections
	}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExecuteNativeReview(ctx, w.ID, smallRev.ID, "agent:agent-1"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected ErrOwnerAuthRequired when ConnectionsHash changed post-prepare, got %v", err)
	}

	// Global emergency freeze works immediately without proof, blocks PrepareReview, and requires owner proof to clear.
	gf, err := client.SetGlobalFreeze(ctx, true, "panic stop", "owner")
	if err != nil || !gf.Active {
		t.Fatalf("SetGlobalFreeze(true) failed: %+v err=%v", gf, err)
	}
	if _, err := client.PrepareReview(ctx, PrepareReviewRequest{
		WalletID:    w.ID,
		Operation:   "solana.nativeTransfer",
		Destination: destAddr,
		AmountHuman: "0.01",
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied while global freeze is active, got %v", err)
	}
	if _, err := client.SetGlobalFreeze(ctx, false, "try clear without proof", "owner"); !errors.Is(err, ErrOwnerAuthRequired) {
		t.Fatalf("expected ErrOwnerAuthRequired when clearing global freeze without proof, got %v", err)
	}
	proof, _, err := client.AuthorizeOwnerAction(ctx, "WaveS5OwnerPass!2026", "unfreeze", "global")
	if err != nil {
		t.Fatal(err)
	}
	gfCleared, err := client.SetGlobalFreeze(WithOwnerProof(ctx, proof), false, "owner verified clear", "owner")
	if err != nil || gfCleared.Active {
		t.Fatalf("expected authorized SetGlobalFreeze(false) to clear freeze, got %+v err=%v", gfCleared, err)
	}
}
