package wenlv1x

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/onchain"
)

type testRT func(*http.Request) (*http.Response, error)

func (f testRT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func encodeFrame(magic string, space int, body []byte) []byte {
	out := make([]byte, space)
	copy(out[:8], magic)
	binary.LittleEndian.PutUint32(out[8:12], uint32(len(body)))
	copy(out[12:], body)
	return out
}

func writeKey(buf *bytes.Buffer, b58 string) {
	k, _ := onchain.Key(b58)
	buf.Write(k)
}

func writeU16(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func writeU64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

func buildTestRuntimeFrame(ledger, mint, btcMint string, epoch int64, phase uint8) []byte {
	var b bytes.Buffer
	writeKey(&b, ledger)
	writeU64(&b, uint64(epoch))
	for range 3 {
		writeKey(&b, ledger)
	}
	for range 3 {
		writeKey(&b, ledger)
	}
	b.WriteByte(2)
	writeKey(&b, mint)
	writeKey(&b, btcMint)
	b.WriteByte(phase)
	b.Write([]byte{0, 0})
	writeU64(&b, 50_000_000_000) // awarded
	writeU64(&b, 0)
	writeU64(&b, 1_000_000_000) // boostAwarded
	writeU64(&b, 0)
	b.WriteByte(0)
	b.WriteByte(0)
	b.WriteByte(0)
	writeU64(&b, 0)
	writeU64(&b, 40_000_000_000)
	writeU64(&b, 50_000_000_000_000)
	writeU64(&b, 0)
	writeU64(&b, 0)
	writeU64(&b, 0)
	writeKey(&b, ledger)
	writeKey(&b, ledger)
	writeU64(&b, onchain.LV1XDefaultLPSolMin)
	writeU64(&b, onchain.LV1XDefaultLPSatMin)
	writeU16(&b, onchain.LV1XDefaultTGEBps)
	writeU32(&b, onchain.LV1XBootstrapVestingSec)
	writeU32(&b, uint32(onchain.LV1XDefaultBoostBudget))
	b.WriteByte(uint8(onchain.LV1XDefaultBoostMult))
	b.WriteByte(0)
	return encodeFrame(onchain.LV1XMagicRuntime, onchain.LV1XSpaceRuntime, b.Bytes())
}

func buildTestRoundFrame(ledger string, slot uint64, start int64, stage uint8, totalStake, minerSat, minerBtcSol, minerBtcTotal uint64, openP, closeP uint64) []byte {
	var b bytes.Buffer
	writeKey(&b, ledger)
	writeKey(&b, ledger)
	writeU64(&b, slot)
	b.WriteByte(0) // previousSlot None
	writeU64(&b, uint64(start))
	b.WriteByte(1) // normal phase (instant SAT claimable without bootstrap vest lock)
	writeU64(&b, slot)
	writeU64(&b, 0)
	b.WriteByte(0)
	b.WriteByte(0)
	writeU64(&b, totalStake)
	writeU32(&b, 4)
	writeU64(&b, totalStake)
	b.WriteByte(stage)
	if openP > 0 {
		b.WriteByte(1)
		writeU64(&b, openP)
	} else {
		b.WriteByte(0)
	}
	if closeP > 0 {
		b.WriteByte(1)
		writeU64(&b, closeP)
	} else {
		b.WriteByte(0)
	}
	b.WriteByte(0) // OpenVotes
	b.WriteByte(0)
	b.WriteByte(0) // CloseVotes
	b.WriteByte(0)
	writeU32(&b, 1) // scanned
	writeU32(&b, 1) // allocated
	writeU32(&b, 0) // closedPages
	writeU64(&b, totalStake)
	writeU64(&b, 0)
	writeU64(&b, 0)
	b.Write(make([]byte, 16)) // creditWeight u128
	for range 3 {
		writeU64(&b, 0)
	}
	writeU64(&b, minerSat)
	writeU64(&b, minerSat)
	for range 3 {
		writeU64(&b, 0)
	}
	writeU64(&b, minerBtcSol)
	for range 4 {
		writeU64(&b, 0)
	}
	writeU32(&b, 0)
	b.Write(make([]byte, 16))
	for range 10 {
		writeU64(&b, 0)
	}
	writeU64(&b, minerBtcTotal)
	writeU64(&b, 0)
	b.Write(make([]byte, 16))
	writeU64(&b, 0)
	b.WriteByte(1) // shardCount=1
	for range 5 {
		writeU32(&b, 0)
	}
	writeU32(&b, 1) // totalPages=1
	writeU16(&b, 5)
	writeU16(&b, 20)
	return encodeFrame(onchain.LV1XMagicRound, onchain.LV1XSpaceRound, b.Bytes())
}

func buildTestPageFrame(ledger, rkAddr, owner string, slot, nonce, amount, principal, sat uint64) []byte {
	var b bytes.Buffer
	writeKey(&b, ledger)
	writeKey(&b, rkAddr)
	writeU64(&b, slot)
	writeU32(&b, 0)
	b.WriteByte(1) // 1 row
	writeKey(&b, owner)
	writeU64(&b, nonce)
	writeU64(&b, amount)
	b.Write(make([]byte, 32)) // commitment
	b.WriteByte(1)            // selection Some(2)
	b.WriteByte(2)
	writeU64(&b, principal)
	writeU64(&b, sat)
	writeU64(&b, 0) // satClaimed
	writeU64(&b, 0) // stakingRefund
	writeU64(&b, 0) // lpSol
	writeU64(&b, 0) // lpSat
	writeU64(&b, 1_000_000)
	b.WriteByte(1) // exact
	writeU64(&b, 0)
	writeU64(&b, 0)
	writeU64(&b, 0)
	b.Write([]byte{0, 0, 0}) // solClaimed, btcClaimed, lpReclaimed
	return encodeFrame(onchain.LV1XMagicPage, onchain.LV1XSpacePage, b.Bytes())
}

func buildTestStakingFrame(ledger string) []byte {
	var b bytes.Buffer
	writeKey(&b, ledger)
	writeU64(&b, 100_000_000_000)
	writeU64(&b, 5_000_000_000)
	b.Write(make([]byte, 16))
	writeU64(&b, 0)
	writeU64(&b, 100_000)
	writeU64(&b, 50_000)
	writeU64(&b, 1_795_800_000)
	writeU64(&b, 1_795_814_400)
	return encodeFrame(onchain.LV1XMagicStakingState, onchain.LV1XSpaceStakingState, b.Bytes())
}

func buildTestStakePosFrame(ledger, owner string, active, pending uint64, matureAt int64, unclaimedBtc uint64) []byte {
	var b bytes.Buffer
	writeKey(&b, ledger)
	writeKey(&b, owner)
	writeU64(&b, active)
	writeU64(&b, pending)
	writeU64(&b, uint64(matureAt))
	b.Write(make([]byte, 16))
	writeU64(&b, unclaimedBtc)
	return encodeFrame(onchain.LV1XMagicStakePosition, onchain.LV1XSpaceStakePosition, b.Bytes())
}

func buildTestTreasuryFrame(ledger string, btcBought, lpBurned uint64) []byte {
	var b bytes.Buffer
	writeKey(&b, ledger)
	writeU64(&b, 1_000_000)
	writeU64(&b, 2_000_000)
	writeU64(&b, btcBought)
	writeU64(&b, lpBurned)
	return encodeFrame(onchain.LV1XMagicTreasuryState, onchain.LV1XSpaceTreasuryState, b.Bytes())
}

func buildTestSPLAccount(amount uint64) []byte {
	out := make([]byte, 165)
	binary.LittleEndian.PutUint64(out[64:72], amount)
	return out
}

func TestSelfKeeperCommitRevealClaimCloseNonceLifecycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	epoch := int64(1795800000)
	var curNow atomic.Int64
	curNow.Store(epoch + 21*600 + 100) // slot 22, rem = 500s

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	kh := sha256.Sum256(pub)
	keyID := hex.EncodeToString(kh[:])

	progID := onchain.LV1XDevnetProgramID
	ledgerID := onchain.LV1XDevnetLedgerID
	mintID, _ := onchain.LV1XMintPDA(progID, ledgerID)
	btcMint := onchain.LV1XDevnetBTCMint
	rtAddr, _ := onchain.LV1XRuntimePDA(progID, ledgerID)
	stAddr, _ := onchain.LV1XStakingStatePDA(progID, ledgerID)
	trAddr, _ := onchain.LV1XTreasuryStatePDA(progID, ledgerID)
	bh := custody.Base58Encode([]byte{9, 8, 7, 6, 5, 4, 3, 2, 1, 9, 8, 7, 6, 5, 4, 3, 2, 1, 9, 8, 7, 6, 5, 4, 3, 2, 1, 9, 8, 7, 6, 5})

	var sendCount atomic.Int32
	var walletPub string
	var posAddr, satATA, btcATA string
	settledSlot := uint64(20)
	stalledSlot := uint64(21)
	settledRkAddr, _ := onchain.LV1XRoundPDA(progID, ledgerID, settledSlot)
	settledPageAddr, _ := onchain.LV1XShardPagePDA(progID, settledRkAddr, 0, 0)
	stalledRkAddr, _ := onchain.LV1XRoundPDA(progID, ledgerID, stalledSlot)

	transport := testRT(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(raw, &req)
		var res any
		switch req.Method {
		case "getGenesisHash":
			res = custody.SolanaDevnetGenesis
		case "getBalance":
			res = map[string]any{"value": 8_000_000_000}
		case "getLatestBlockhash":
			res = map[string]any{"value": map[string]any{"blockhash": bh}}
		case "simulateTransaction":
			res = map[string]any{"value": map[string]any{"err": nil}}
		case "sendTransaction":
			sendCount.Add(1)
			res = "5xSelfKeeperSig11111111111111111111111111111"
		case "getSignatureStatuses":
			res = map[string]any{"value": []any{map[string]any{"confirmationStatus": "finalized", "err": nil}}}
		case "getMultipleAccounts":
			var addrs []string
			if len(req.Params) > 0 {
				_ = json.Unmarshal(req.Params[0], &addrs)
			}
			vals := make([]any, 0, len(addrs))
			for _, a := range addrs {
				var acct []byte
				owner := progID
				var lamps uint64 = 1_000_000
				switch a {
				case progID:
					acct = []byte{2, 0, 0, 0}
					owner = onchain.BPFLoaderUpgradeableID
				case rtAddr:
					acct = buildTestRuntimeFrame(ledgerID, mintID, btcMint, epoch, 1)
				case stAddr:
					acct = buildTestStakingFrame(ledgerID)
				case trAddr:
					acct = buildTestTreasuryFrame(ledgerID, 55_000, 12_500_000_000)
				case posAddr:
					if walletPub != "" {
						acct = buildTestStakePosFrame(ledgerID, walletPub, 10_000_000_000, 5_000_000_000, epoch+10, 1_500)
					}
				case walletPub:
					owner = custody.SolanaSystemProgram
					lamps = 8_000_000_000
					acct = []byte{0}
				case satATA:
					owner = custody.SolanaTokenProgram
					acct = buildTestSPLAccount(2_500_000_000) // 2.5 WENSAT
				case btcATA:
					owner = custody.SolanaTokenProgram
					acct = buildTestSPLAccount(5_000)
				case settledRkAddr:
					// Settled round with sol_escape (MinerBtcTotal=0, MinerBtcSol>0) and +15bps win.
					acct = buildTestRoundFrame(ledgerID, settledSlot, epoch+19*600, 3, 2_000_000_000, 50_000_000_000, 100_000_000, 0, 6_000_000, 6_009_000)
				case settledPageAddr:
					if walletPub != "" {
						acct = buildTestPageFrame(ledgerID, settledRkAddr, walletPub, settledSlot, 9, 40_000_000, 38_000_000, 4_000_000_000)
					}
				case stalledRkAddr:
					acct = buildTestRoundFrame(ledgerID, stalledSlot, epoch+20*600, 0, 1_000_000_000, 0, 0, 0, 0, 0)
				default:
					acct = nil
				}
				if acct == nil {
					vals = append(vals, nil)
				} else {
					vals = append(vals, map[string]any{
						"owner":    owner,
						"lamports": lamps,
						"data":     []string{base64.StdEncoding.EncodeToString(acct), "base64"},
					})
				}
			}
			res = map[string]any{"value": vals}
		}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	payloadBytes, _ := json.Marshal(onchain.LV1XManifestPayload{
		Schema:             onchain.LV1XManifestSchema,
		Sequence:           "1",
		IssuedAt:           curNow.Load() - 60,
		ExpiresAt:          curNow.Load() + 86400,
		MinFasedVersion:    "0.5.0",
		ProtocolSchemaHash: onchain.LV1XProtocolSchemaHash,
		Clusters: map[string]onchain.LV1XManifestCluster{
			"devnet": {
				Status:            "active",
				GenesisHash:       custody.SolanaDevnetGenesis,
				ProgramID:         progID,
				LedgerID:          ledgerID,
				Mint:              mintID,
				BTCMint:           btcMint,
				ExpectedELFBytes:  onchain.LV1XDevnetELFBytes,
				ExpectedELFSHA256: onchain.LV1XDevnetELFSHA256,
			},
		},
	})
	msg := append([]byte(onchain.LV1XManifestDomain), payloadBytes...)
	envBytes, _ := json.Marshal(onchain.LV1XManifestEnvelope{
		Version:   1,
		KeyID:     keyID,
		Payload:   json.RawMessage(payloadBytes),
		Signature: ed25519.Sign(priv, msg),
	})
	manifestSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(envBytes)
	}))
	defer manifestSrv.Close()

	custSvc, err := custody.NewService(custody.Config{
		DataDir:            t.TempDir(),
		FastKDF:            true,
		RPCTransport:       transport,
		ManifestVerifyKeys: map[string]string{keyID: hex.EncodeToString(pub)},
		Now:                func() time.Time { return time.Unix(curNow.Load(), 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer custSvc.Close()

	w, err := custSvc.CreateNativeWallet(ctx, "SelfKeeper Miner", custody.RoleMining, "https://devnet.helius.test/?api-key=secret-token")
	if err != nil {
		t.Fatal(err)
	}
	walletPub = w.PublicKey
	posAddr, _ = onchain.LV1XStakePositionPDA(progID, ledgerID, walletPub)
	satATA, _ = onchain.LV1XAssociatedTokenAddress(walletPub, mintID)
	btcATA, _ = onchain.LV1XAssociatedTokenAddress(walletPub, btcMint)

	pol := custody.WalletPolicy{
		Role:              custody.RoleMining,
		ApprovalMode:      custody.ApprovalBoundedAuto,
		AutoApproveMaxUSD: 50.0,
		Operations:        custody.AllMiningOperations(),
		Programs:          []string{custody.SolanaSystemProgram, custody.SolanaComputeBudget, custody.SolanaTokenProgram, custody.SolanaATAProgram, progID},
		SOLCap: custody.AssetCap{
			Asset:       "solana:native",
			Symbol:      "SOL",
			Decimals:    9,
			MaxPerTxRaw: 200_000_000,
			MaxDailyRaw: 500_000_000,
		},
		Mining: &custody.MiningPolicy{
			Cluster:               "devnet",
			ProgramID:             progID,
			LedgerID:              ledgerID,
			ExpectedELFSHA256:     onchain.LV1XDevnetELFSHA256,
			MaxSOLPerRoundLamport: 200_000_000,
			MaxDailySOLLamports:   500_000_000,
			AllowedStrategies:     []string{"wide", "center"},
			AutoClaim:             true,
			AutoStakeWensat:       false,
			AllowFallbackCrank:    true,
		},
	}
	w, err = custSvc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatal(err)
	}

	eng, err := NewEngine(Config{
		DataDir:              t.TempDir(),
		Custody:              custSvc,
		FixtureMode:          false,
		ManifestURLs:         []string{manifestSrv.URL},
		KeeperInterval:       40 * time.Millisecond,
		ManifestSyncInterval: time.Hour,
		HTTPClient:           manifestSrv.Client(),
		Now:                  func() time.Time { return time.Unix(curNow.Load(), 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	evCh, unsub := eng.Subscribe()
	defer unsub()

	// 1. Multi-bucket commit split into 4 buckets (30/20/30/20%) & sealed salts at rest.
	_, _, err = eng.SubmitCommit(ctx, CommitRequest{
		WalletID:      w.ID,
		Cluster:       "devnet",
		AmountSOL:     0.10,
		StrategyID:    "wide",
		BucketWeights: &[5]uint16{3000, 2000, 3000, 2000, 0},
		Autonomous:    true,
	})
	if err != nil {
		t.Fatalf("multi-bucket SubmitCommit failed: %v", err)
	}

	// Verify salts in SQLite use FASED-SEALED-V1: (zero raw plaintext hex).
	var commitRows int
	rows, err := eng.db.QueryContext(ctx, `SELECT salt_hex FROM lv1x_entries WHERE wallet_id = ? AND kind = 'commit' AND round_slot = 22`, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var sh string
		_ = rows.Scan(&sh)
		commitRows++
		if !strings.HasPrefix(sh, sealedSaltPrefix) {
			t.Fatalf("expected sealed salt prefix %s, got %q", sealedSaltPrefix, sh)
		}
	}
	_ = rows.Close()
	if commitRows != 4 {
		t.Fatalf("expected 4 split bucket commits, got %d", commitRows)
	}

	// Verify NextRevealAt == endsAt - 55 before reveal window.
	sk0 := eng.SelfKeeperStatus(ctx)
	if sk0.NextRevealAt != epoch+22*600-55 || sk0.ActiveRoundCommits != 4 {
		t.Fatalf("unexpected SelfKeeperStatus before reveal window: %+v", sk0)
	}

	// Record local unsettled entry for settled round #20 (nonce 9) & stalled round #21 so SelfKeeper handles them.
	eng.recordEntry(ctx, w.ID, "devnet", settledSlot, 0, 0, 0, 9, true, false, "commit", nil, "wide", 0.04, 0, 0, "", "sig-20", custody.LedgerConfirmed)
	eng.recordEntry(ctx, w.ID, "devnet", stalledSlot, 0, 0, 0, 1, true, false, "commit", nil, "wide", 0.02, 0, 0, "", "sig-21", custody.LedgerConfirmed)

	// Verify ListClaimableItemsForCluster decodes real on-chain Page + Round (#20 -> SolEscape=true, 0.038 SOL, 4 WENSAT).
	claimables, err := eng.ListClaimableItemsForCluster(ctx, "devnet", w.ID)
	if err != nil || len(claimables) != 1 || !claimables[0].SolEscape || claimables[0].SolLamports != 38_000_000 || claimables[0].VestedAtoms != 4_000_000_000 {
		t.Fatalf("unexpected on-chain decoded claimables: %+v err=%v", claimables, err)
	}

	// 2. Advance clock into Auto-Reveal window (endsAt - 48) and run sweep.
	curNow.Store(epoch + 22*600 - 48)
	sk1, err := eng.TriggerSelfKeeperSweep(ctx)
	if err != nil {
		t.Fatalf("TriggerSelfKeeperSweep failed: %v", err)
	}
	if !strings.Contains(sk1.LastSweepSummary, "revealed 4") ||
		!strings.Contains(sk1.LastSweepSummary, "claimed 3") ||
		!strings.Contains(sk1.LastSweepSummary, "closed 1 nonces") ||
		!strings.Contains(sk1.LastSweepSummary, "activated stake") ||
		!strings.Contains(sk1.LastSweepSummary, "claimed staking btc") ||
		!strings.Contains(sk1.LastSweepSummary, "fallback begin #21") ||
		strings.Contains(sk1.LastSweepSummary, "auto-staked") {
		t.Fatalf("unexpected sweep summary (AutoStakeWensat=false): %q (err=%q)", sk1.LastSweepSummary, sk1.LastError)
	}
	if !sk1.ManifestCompatible || sk1.ManifestSequence != 1 || sk1.ManifestStatus != "pinned" {
		t.Fatalf("expected pinned manifest on SelfKeeperStatus, got %+v", sk1)
	}

	// 3. Opt in AutoStakeWensat=true and run sweep -> Auto-Stakes >= 1 WENSAT without ever calling Unstake.
	pol.Mining.AutoStakeWensat = true
	w, err = custSvc.UpdatePolicy(ctx, w.ID, pol, true, w.Policy.PolicyHash, "owner")
	if err != nil {
		t.Fatal(err)
	}
	sk2, err := eng.TriggerSelfKeeperSweep(ctx)
	if err != nil || !strings.Contains(sk2.LastSweepSummary, "auto-staked 2 SAT") {
		t.Fatalf("expected AutoStakeWensat=true sweep to auto-stake 2 SAT, got %+v err=%v", sk2, err)
	}

	// 4. Verify real on-chain Streak from settled round #20, Balanced-A Treasury LP burned, and SessionState op.
	streak, err := eng.ListStreak(ctx, 24)
	if err != nil || len(streak) == 0 || streak[len(streak)-1].RoundSlot != settledSlot || streak[len(streak)-1].MoveBps != 15 {
		t.Fatalf("expected real decoded settled streak for slot 20 (+15bps), got %+v err=%v", streak, err)
	}
	pStats, err := eng.ProtocolStats(ctx, "devnet")
	if err != nil || pStats.LPBurnedSOL != 12.5 || pStats.BoostBudgetSat != 400_000 || pStats.BoostMultiplier != 24 || pStats.FeeSplit != [4]int{72, 13, 10, 5} {
		t.Fatalf("unexpected Balanced-A ProtocolStats: %+v err=%v", pStats, err)
	}
	if _, _, sErr := eng.SubmitSessionOp(ctx, w.ID, "devnet", "stop", 7, 0, 0, 0, 0, 0, 0, "", true, false); sErr != nil {
		t.Fatalf("SubmitSessionOp(stop) failed: %v", sErr)
	}

	// 5. Background Start + clean StopKeeper; collect lifecycle events.
	eng.Start(ctx)
	deadline := time.After(500 * time.Millisecond)
	seenEvents := make(map[string]bool)
loop:
	for {
		select {
		case ev := <-evCh:
			seenEvents[ev.Event] = true
			if seenEvents["round.reveal_window"] && seenEvents["stake.matured"] && seenEvents["manifest.updated"] {
				break loop
			}
		case <-deadline:
			break loop
		}
	}
	if !seenEvents["round.opened"] || !seenEvents["round.reveal_window"] || !seenEvents["stake.matured"] || !seenEvents["manifest.updated"] {
		t.Fatalf("missing expected lifecycle events, saw=%v", seenEvents)
	}
	eng.StopKeeper()
	if stStop := eng.SelfKeeperStatus(ctx); stStop.Running {
		t.Fatal("expected SelfKeeper stopped cleanly")
	}
}
