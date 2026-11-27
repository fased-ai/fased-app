package onchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestLV1XPDACommitmentWireAndBPFLoader(t *testing.T) {
	t.Parallel()

	rt, err := LV1XRuntimePDA(LV1XDevnetProgramID, LV1XDevnetLedgerID)
	if err != nil || rt == "" {
		t.Fatalf("LV1XRuntimePDA failed: %q err=%v", rt, err)
	}
	if _, e := Key(rt); e != nil {
		t.Fatalf("invalid runtime PDA %q: %v", rt, e)
	}
	// Deterministic repeat call matches exactly.
	rt2, _ := LV1XRuntimePDA(LV1XDevnetProgramID, LV1XDevnetLedgerID)
	if rt != rt2 {
		t.Fatalf("LV1XRuntimePDA non-deterministic: %s != %s", rt, rt2)
	}

	pdas := make([]string, 0, 5)
	for _, fn := range []func() (string, error){
		func() (string, error) { return LV1XMintPDA(LV1XDevnetProgramID, LV1XDevnetLedgerID) },
		func() (string, error) { return LV1XIssuancePDA(LV1XDevnetProgramID, LV1XDevnetLedgerID) },
		func() (string, error) { return LV1XMinerBTCVaultPDA(LV1XDevnetProgramID, LV1XDevnetLedgerID) },
		func() (string, error) { return LV1XRoundPDA(LV1XDevnetProgramID, LV1XDevnetLedgerID, 42) },
		func() (string, error) {
			return LV1XSessionPDA(LV1XDevnetProgramID, LV1XDevnetLedgerID, LV1XDevnetLedgerID, 7)
		},
	} {
		addr, e := fn()
		if e != nil || addr == "" {
			t.Fatalf("PDA derivation failed: %v", e)
		}
		pdas = append(pdas, addr)
	}
	if pdas[0] == pdas[1] || pdas[3] == rt {
		t.Fatalf("expected distinct LV1X PDAs: %v", pdas)
	}

	var owner, blockhash, salt, root [32]byte
	owner[0], blockhash[0], salt[0], root[0] = 9, 8, 7, 6

	commit, err := LV1XCommitmentHash(LV1XDevnetProgramID, LV1XDevnetLedgerID, 42, Base58(owner[:]), 25_000_000, 1, 2, salt)
	if err != nil {
		t.Fatalf("LV1XCommitmentHash: %v", err)
	}
	if _, err := LV1XCommitmentHash(LV1XDevnetProgramID, LV1XDevnetLedgerID, 42, Base58(owner[:]), 25_000_000, 1, 5, salt); err == nil {
		t.Fatal("expected selection > 4 error")
	}

	// Unsharded commit wire message builds cleanly.
	msg, err := BuildLV1XCommitMessage(LV1XCommitParams{
		ProgramID:  LV1XDevnetProgramID,
		LedgerID:   LV1XDevnetLedgerID,
		Owner:      owner,
		Blockhash:  blockhash,
		Slot:       42,
		ShardID:    LV1XUnshardedShardID,
		EntryIndex: 0,
		Amount:     LV1XMinCommitLamports,
		Nonce:      0,
		Commitment: commit,
	})
	if err != nil || len(msg) < 100 || len(msg)+65 > 1232 {
		t.Fatalf("BuildLV1XCommitMessage failed: len=%d err=%v", len(msg), err)
	}
	if !bytes.Contains(msg, []byte{'L', 'V', '1', 'X', LV1XVersion, LV1XTagCommit}) {
		t.Fatalf("expected LV1X wire prefix on unsharded Commit message")
	}

	// Sharded CommitToShard wire message starts with "LV1X\x01" + tag 35 and has exact 63-byte ix payload.
	shardMsg, err := BuildLV1XCommitMessage(LV1XCommitParams{
		ProgramID:  LV1XDevnetProgramID,
		LedgerID:   LV1XDevnetLedgerID,
		Owner:      owner,
		Blockhash:  blockhash,
		Slot:       42,
		ShardID:    0,
		EntryIndex: 0,
		Amount:     LV1XMinCommitLamports,
		Nonce:      1,
		Commitment: commit,
	})
	if err != nil {
		t.Fatalf("BuildLV1XCommitMessage sharded failed: %v", err)
	}
	wantCommitToShardHdr := []byte{63, 'L', 'V', '1', 'X', LV1XVersion, LV1XTagCommitToShard}
	if !bytes.Contains(shardMsg, wantCommitToShardHdr) {
		t.Fatalf("expected sharded CommitToShard length+header %x in message", wantCommitToShardHdr)
	}

	// Below minimum commit rejected.
	if _, err := BuildLV1XCommitMessage(LV1XCommitParams{
		ProgramID:  LV1XDevnetProgramID,
		LedgerID:   LV1XDevnetLedgerID,
		Owner:      owner,
		Blockhash:  blockhash,
		Slot:       42,
		ShardID:    0,
		Amount:     1000,
		Commitment: commit,
	}); err == nil {
		t.Fatal("expected min commit rejection")
	}

	// Claim SOL batch + Session Open/Stop/Close messages build cleanly.
	claimMsg, err := BuildLV1XClaimSolBatchMessage(LV1XClaimSolParams{
		ProgramID:   LV1XDevnetProgramID,
		LedgerID:    LV1XDevnetLedgerID,
		FeePayer:    owner,
		Destination: owner,
		Blockhash:   blockhash,
		Slot:        42,
		ShardID:     0,
		Page:        0,
		EntriesMask: 0x01,
	})
	if err != nil || len(claimMsg) < 80 {
		t.Fatalf("BuildLV1XClaimSolBatchMessage failed: len=%d err=%v", len(claimMsg), err)
	}

	for _, act := range []string{"open", "deposit", "stop", "close"} {
		sessMsg, sErr := BuildLV1XSessionMessage(LV1XSessionParams{
			ProgramID:    LV1XDevnetProgramID,
			LedgerID:     LV1XDevnetLedgerID,
			Owner:        owner,
			Blockhash:    blockhash,
			Reference:    99,
			StrategyRoot: root,
			FirstSlot:    42,
			LastSlot:     50,
			ExpiresAt:    1800000000,
			MaxEntry:     50_000_000,
			MaxTurnover:  200_000_000,
			Deposit:      100_000_000,
			Action:       act,
		})
		if sErr != nil || len(sessMsg) < 60 {
			t.Fatalf("BuildLV1XSessionMessage(%s): len=%d err=%v", act, len(sessMsg), sErr)
		}
	}

	// Reveal, ClaimSAT, ClaimBTC, Stake/Activate/ClaimStaking/Unstake builders + Staking/Treasury PDAs.
	for _, fn := range []func() (string, error){
		func() (string, error) { return LV1XStakingStatePDA(LV1XDevnetProgramID, LV1XDevnetLedgerID) },
		func() (string, error) { return LV1XStakingVaultPDA(LV1XDevnetProgramID, LV1XDevnetLedgerID) },
		func() (string, error) { return LV1XStakingBTCVaultPDA(LV1XDevnetProgramID, LV1XDevnetLedgerID) },
		func() (string, error) {
			return LV1XStakePositionPDA(LV1XDevnetProgramID, LV1XDevnetLedgerID, LV1XDevnetLedgerID)
		},
		func() (string, error) { return LV1XTreasuryStatePDA(LV1XDevnetProgramID, LV1XDevnetLedgerID) },
		func() (string, error) { return LV1XTreasuryBTCVaultPDA(LV1XDevnetProgramID, LV1XDevnetLedgerID) },
	} {
		addr, e := fn()
		if e != nil || addr == "" {
			t.Fatalf("staking/treasury PDA derivation failed: %v", e)
		}
	}

	revMsg, err := BuildLV1XRevealMessage(LV1XRevealParams{
		ProgramID: LV1XDevnetProgramID,
		LedgerID:  LV1XDevnetLedgerID,
		FeePayer:  owner,
		Blockhash: blockhash,
		Slot:      42,
		ShardID:   LV1XUnshardedShardID,
		Page:      0,
		Items:     []LV1XRevealItem{{EntryRow: 0, Selection: 2, Salt: salt}},
	})
	if err != nil || len(revMsg) < 90 {
		t.Fatalf("BuildLV1XRevealMessage failed: len=%d err=%v", len(revMsg), err)
	}
	satMsg, err := BuildLV1XClaimSatMessage(LV1XClaimTokenParams{
		ProgramID: LV1XDevnetProgramID,
		LedgerID:  LV1XDevnetLedgerID,
		Owner:     owner,
		Blockhash: blockhash,
		Slot:      42,
		ShardID:   LV1XUnshardedShardID,
		Page:      0,
		EntryRow:  0,
		CreateATA: true,
	})
	if err != nil || len(satMsg) < 100 {
		t.Fatalf("BuildLV1XClaimSatMessage failed: len=%d err=%v", len(satMsg), err)
	}
	btcMsg, err := BuildLV1XClaimBtcMessage(LV1XClaimTokenParams{
		ProgramID: LV1XDevnetProgramID,
		LedgerID:  LV1XDevnetLedgerID,
		Owner:     owner,
		Blockhash: blockhash,
		Slot:      42,
		ShardID:   0,
		Page:      1,
		EntryRow:  2,
		BTCMint:   LV1XDevnetBTCMint,
		CreateATA: true,
	})
	if err != nil || len(btcMsg) < 100 {
		t.Fatalf("BuildLV1XClaimBtcMessage failed: len=%d err=%v", len(btcMsg), err)
	}
	for _, act := range []string{"stake", "activate", "claim_reward", "unstake"} {
		stMsg, stErr := BuildLV1XStakeMessage(LV1XStakeParams{
			ProgramID: LV1XDevnetProgramID,
			LedgerID:  LV1XDevnetLedgerID,
			Owner:     owner,
			Blockhash: blockhash,
			Action:    act,
			Amount:    LV1XMinStakeAtoms,
			BTCMint:   LV1XDevnetBTCMint,
			CreateATA: true,
		})
		if stErr != nil || len(stMsg) < 70 {
			t.Fatalf("BuildLV1XStakeMessage(%s) failed: len=%d err=%v", act, len(stMsg), stErr)
		}
	}
	if _, err := BuildLV1XStakeMessage(LV1XStakeParams{
		ProgramID: LV1XDevnetProgramID,
		LedgerID:  LV1XDevnetLedgerID,
		Owner:     owner,
		Blockhash: blockhash,
		Action:    "stake",
		Amount:    100,
	}); err == nil {
		t.Fatal("expected min stake atoms error")
	}

	// BPF Loader Upgradeable ELF SHA-256 verification.
	loaderKey, _ := Key(BPFLoaderUpgradeableID)
	progDataAddr, err := BPFLoaderProgramDataPDA(LV1XDevnetProgramID)
	if err != nil {
		t.Fatalf("BPFLoaderProgramDataPDA: %v", err)
	}
	progDataKey, _ := Key(progDataAddr)
	progAcctData := make([]byte, 36)
	binary.LittleEndian.PutUint32(progAcctData[:4], 2)
	copy(progAcctData[4:36], progDataKey)

	elfPayload := []byte("ELF-LV1X-TEST-BINARY-PAYLOAD")
	elfSum := sha256.Sum256(elfPayload)
	elfHex := hex.EncodeToString(elfSum[:])
	pdAcctData := make([]byte, 45+len(elfPayload))
	binary.LittleEndian.PutUint32(pdAcctData[:4], 3)
	copy(pdAcctData[45:], elfPayload)

	if err := VerifyBPFLoaderProgramData(LV1XDevnetProgramID, loaderKey, progAcctData, loaderKey, pdAcctData, elfHex); err != nil {
		t.Fatalf("VerifyBPFLoaderProgramData expected pass, got %v", err)
	}
	if err := VerifyBPFLoaderProgramData(LV1XDevnetProgramID, loaderKey, progAcctData, loaderKey, pdAcctData, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("expected ELF SHA-256 mismatch failure")
	}

	// All remaining builders emit canonical "LV1X\x01" + tag prefix.
	for _, tc := range []struct {
		name string
		raw  []byte
		tag  uint8
	}{
		{"ClaimShardSolBatch", claimMsg, LV1XTagClaimShardSolBatch},
		{"RevealBatch", revMsg, LV1XTagRevealBatch},
		{"ClaimSat", satMsg, LV1XTagClaimSat},
		{"ClaimShardBtc", btcMsg, LV1XTagClaimShardBtc},
	} {
		if !bytes.Contains(tc.raw, []byte{'L', 'V', '1', 'X', LV1XVersion, tc.tag}) {
			t.Fatalf("%s missing LV1X\\x01 prefix with tag %d", tc.name, tc.tag)
		}
	}
}
