//go:build linux

package signer

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
)

func runSettledManagedSwap(t *testing.T, v *Vault, chain *onchaintest.Chain, seq int) string {
	t.Helper()
	ref := *v.Status().Account
	pub, _ := hex.DecodeString(ref.PublicKey)
	now := time.Now().Unix()
	amt := strconv.FormatUint(12_500_000+uint64(seq)*1_000, 10)
	q, err := onchaintest.Client(chain).Prepare(context.Background(), onchain.Quote{
		Version:        onchain.Version,
		ID:             randomID(),
		ProfileID:      "trading-analyst",
		InstanceID:     strings.Repeat("2", 32),
		AccountID:      strings.Repeat("3", 32),
		Network:        ref.Network,
		Owner:          onchain.Base58(pub),
		RPCSHA256:      strings.Repeat("4", 64),
		InputMint:      onchain.USDC,
		OutputMint:     onchain.SOL,
		Amount:         amt,
		SlippageBPS:    50,
		MaxFeeLamports: 5000,
		CreatedAt:      now,
		ExpiresAt:      now + 45,
	})
	if err != nil {
		t.Fatalf("Prepare swap %d: %v", seq, err)
	}
	p := NewSwapProposal(ref, q)
	if _, err := v.ApproveSwap(p); err != nil {
		t.Fatalf("ApproveSwap: %v", err)
	}
	signed, err := v.SignSwap(p.Quote.ID)
	if err != nil {
		t.Fatalf("SignSwap: %v", err)
	}
	if _, err := onchaintest.Client(chain).Send(context.Background(), p.Quote, signed.Signed); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := v.ReconcileSwap(context.Background(), p.Quote.ID); err != nil {
		t.Fatalf("ReconcileSwap: %v", err)
	}
	return p.Quote.ID
}

func TestSwapJournalCompactionRemovesLifetimeCap(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	v := managedFixture(t, dir, "mainnet-beta")
	if err := v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	chain := &onchaintest.Chain{}
	if err := v.ConfigureSwapObserver(onchaintest.Client(chain)); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock(fixturePassword, 10*time.Minute); err != nil {
		t.Fatal(err)
	}

	// Execute 72 full settled swaps — previously bricked permanently at 64.
	var swapIDs []string
	for i := 0; i < 72; i++ {
		id := runSettledManagedSwap(t, v, chain, i+1)
		swapIDs = append(swapIDs, id)
	}

	if got := len(v.ledger.swaps); got >= MaxRetainedSwaps {
		t.Fatalf("expected compacted swap count < %d, got %d", MaxRetainedSwaps, got)
	}

	// Close & reopen to verify compacted journal + anchor hash replay cleanly across restart.
	signerID := v.signerID
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openVaultNetwork(dir, signerID, false, "mainnet-beta")
	if err != nil {
		t.Fatalf("reopen compacted managed vault: %v", err)
	}
	defer reopened.Close()
	if err := reopened.ConfigureSwapObserver(onchaintest.Client(chain)); err != nil {
		t.Fatal(err)
	}
	if _, records, err := reopened.store.readJournal("operations.log"); err != nil {
		t.Fatal(err)
	} else {
		hasAnchor := false
		for _, r := range records {
			if r.Kind == "journal-compact" {
				hasAnchor = true
			}
		}
		if !hasAnchor {
			t.Fatal("expected cryptographic journal-compact anchor record in compacted log")
		}
	}

	// Newest swap receipts must still be queryable; pruned oldest swap is no longer held.
	if _, err := reopened.SwapReceipt(swapIDs[len(swapIDs)-1]); err != nil {
		t.Fatalf("latest retained swap missing after compaction restart: %v", err)
	}
	if _, err := reopened.SwapReceipt(swapIDs[0]); err == nil {
		t.Fatalf("oldest pruned swap still present in active map")
	}

	// Signer still works after restart for swap #73.
	if err := reopened.Unlock(fixturePassword, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	id73 := runSettledManagedSwap(t, reopened, chain, 73)
	if _, err := reopened.SwapReceipt(id73); err != nil {
		t.Fatalf("swap 73 failed after compacted restart: %v", err)
	}
}

func TestSwapJournalCompactionHoldAndCrashRecovery(t *testing.T) {
	t.Parallel()
	dir := fixtureDir(t)
	v := managedFixture(t, dir, "mainnet-beta")
	if err := v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	chain := &onchaintest.Chain{}
	if err := v.ConfigureSwapObserver(onchaintest.Client(chain)); err != nil {
		t.Fatal(err)
	}
	if err := v.Unlock(fixturePassword, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		_ = runSettledManagedSwap(t, v, chain, i+1)
	}

	// Active signed hold must block compaction with ErrHeld.
	heldProposal := managedSwapQuote(t, v, chain)
	if _, err := v.ApproveSwap(heldProposal); err != nil {
		t.Fatal(err)
	}
	signed, err := v.SignSwap(heldProposal.Quote.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.CompactOperationsJournal(8); !errors.Is(err, ErrHeld) {
		t.Fatalf("compaction during signed hold got %v, want ErrHeld", err)
	}
	// Settle the hold so compaction is permitted again.
	if _, err := onchaintest.Client(chain).Send(context.Background(), heldProposal.Quote, signed.Signed); err != nil {
		t.Fatal(err)
	}
	if _, err := v.ReconcileSwap(context.Background(), heldProposal.Quote.ID); err != nil {
		t.Fatal(err)
	}

	// Case 1: crash at journal:compact:staged -> reopen preserves original journal and cleans pending file.
	signerID := v.signerID
	crashErr := errors.New("simulated power loss after staging")
	v.store.crash = func(phase string) error {
		if phase == "journal:compact:staged" {
			return crashErr
		}
		return nil
	}
	if _, err := v.CompactOperationsJournal(8); !errors.Is(err, crashErr) {
		t.Fatalf("expected injected staged error, got %v", err)
	}
	_ = v.Close()

	// Cold read-only open must refuse leftover pending compaction.
	if _, err := openVaultNetwork(dir, signerID, true, "mainnet-beta"); err == nil {
		t.Fatal("cold read-only open accepted pending compaction file")
	}
	if _, err := os.Stat(filepath.Join(dir, compactionPendingName)); err != nil {
		t.Fatalf("expected pending compaction file before recovery: %v", err)
	}

	// Writer open cleans pending file cleanly and preserves all 21 swaps.
	recovered, err := openVaultNetwork(dir, signerID, false, "mainnet-beta")
	if err != nil {
		t.Fatalf("reopen after staged crash: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, compactionPendingName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending compaction file survived recovery: %v", err)
	}
	if len(recovered.ledger.swaps) != 21 {
		t.Fatalf("expected all 21 swaps preserved after staged crash, got %d", len(recovered.ledger.swaps))
	}

	// Case 2: crash right after atomic rename (journal:compact:published) -> compacted state is durable.
	recovered.store.crash = func(phase string) error {
		if phase == "journal:compact:published" {
			return crashErr
		}
		return nil
	}
	if _, err := recovered.CompactOperationsJournal(8); !errors.Is(err, crashErr) {
		t.Fatalf("expected injected published error, got %v", err)
	}
	_ = recovered.Close()

	afterPub, err := openVaultNetwork(dir, signerID, false, "mainnet-beta")
	if err != nil {
		t.Fatalf("reopen after published compaction crash: %v", err)
	}
	defer afterPub.Close()
	if len(afterPub.ledger.swaps) != 8 {
		t.Fatalf("expected 8 swaps retained after published compaction, got %d", len(afterPub.ledger.swaps))
	}

	// Tampered journal-compact record must fail replay.
	bad := journalRecord{
		Kind:         "journal-compact",
		At:           afterPub.ledger.lastTime + 1,
		OriginalHash: strings.Repeat("z", 64),
		Outcome:      fmt.Sprintf("compacted:%d:%d", 10, 5),
	}
	if err := afterPub.ledger.apply(bad); err == nil {
		t.Fatal("accepted invalid compaction anchor hash")
	}
}
