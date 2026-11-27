package wenlv1x

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/onchain"
	_ "modernc.org/sqlite"
)

const MainnetGateMessage = "Mainnet LV1X mining is strictly gated off until official Mainnet LV1X program/mint IDs are published, BPF Loader ELF SHA-256 verified, and explicitly enabled by owner policy."

// Engine manages LV1X round telemetry, streak history, local/managed sessions,
// staking, saved strategies, candles, and Custody RoleMining execution.
type Engine struct {
	dir            string
	db             *sql.DB
	custody        custody.Backend
	devnetRPC      string
	candleProvider CandleProvider
	fixture        bool
	httpClient     *http.Client
	now            func() time.Time

	cacheMu  sync.RWMutex
	rpcCache map[string]cachedRPCSnapshot

	mu     sync.RWMutex
	subs   map[chan MiningEvent]struct{}
	closed bool
}

// MiningEvent is fanned out to WebSocket "mining" listeners on state changes.
type MiningEvent struct {
	Event     string `json:"event"` // round.updated | commit.submitted | reveal.submitted | claim.submitted | stake.updated | session.updated
	Cluster   string `json:"cluster"`
	RoundSlot uint64 `json:"roundSlot,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

func NewEngine(cfg Config) (*Engine, error) {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, errors.New("wenlv1x DataDir required")
	}
	dir := filepath.Join(cfg.DataDir, "mining")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, "mining.sqlite")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	schema := `
CREATE TABLE IF NOT EXISTS lv1x_sessions (
	id TEXT PRIMARY KEY,
	wallet_id TEXT NOT NULL,
	wallet_handle TEXT NOT NULL,
	cluster TEXT NOT NULL,
	strategy_id TEXT NOT NULL,
	mode TEXT NOT NULL,
	status TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS lv1x_streak (
	round_slot INTEGER PRIMARY KEY,
	winning_range INTEGER NOT NULL,
	code TEXT NOT NULL,
	move_bps INTEGER NOT NULL,
	pool_sol REAL NOT NULL,
	user_staked INTEGER NOT NULL DEFAULT 0,
	user_won INTEGER NOT NULL DEFAULT 0,
	sat_reward REAL NOT NULL DEFAULT 0,
	settled_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS lv1x_saved_strategies (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL,
	mode TEXT NOT NULL,
	buckets_json TEXT NOT NULL,
	curve_json TEXT NOT NULL,
	source TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS lv1x_entries (
	id TEXT PRIMARY KEY,
	wallet_id TEXT NOT NULL,
	cluster TEXT NOT NULL,
	round_slot INTEGER NOT NULL,
	shard_id INTEGER NOT NULL DEFAULT 0,
	page INTEGER NOT NULL DEFAULT 0,
	entry_row INTEGER NOT NULL DEFAULT 0,
	kind TEXT NOT NULL,
	selection INTEGER,
	strategy_id TEXT NOT NULL DEFAULT '',
	amount_sol REAL NOT NULL DEFAULT 0,
	amount_sat REAL NOT NULL DEFAULT 0,
	amount_btc REAL NOT NULL DEFAULT 0,
	salt_hex TEXT NOT NULL DEFAULT '',
	sol_claimed INTEGER NOT NULL DEFAULT 0,
	btc_claimed INTEGER NOT NULL DEFAULT 0,
	tx_signature TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	created_at INTEGER NOT NULL
);`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	_ = os.Chmod(dbPath, 0o600)
	nowFn := time.Now
	if fn, ok := cfg.Now.(func() time.Time); ok && fn != nil {
		nowFn = fn
	}
	hc := &http.Client{Timeout: 6 * time.Second}
	if customHC, ok := cfg.HTTPClient.(*http.Client); ok && customHC != nil {
		hc = customHC
	}
	rpcURL := strings.TrimSpace(cfg.DevnetRPCURL)
	if rpcURL == "" {
		rpcURL = strings.TrimSpace(os.Getenv("FASED_WEN_DEVNET_RPC_URL"))
	}
	if rpcURL == "" {
		rpcURL = "https://api.devnet.solana.com"
	}
	eng := &Engine{
		dir:            dir,
		db:             db,
		custody:        cfg.Custody,
		devnetRPC:      rpcURL,
		candleProvider: cfg.CandleProvider,
		fixture:        cfg.FixtureMode,
		httpClient:     hc,
		now:            nowFn,
		rpcCache:       make(map[string]cachedRPCSnapshot),
		subs:           make(map[chan MiningEvent]struct{}),
	}
	eng.seedInitialStreakIfEmpty(context.Background())
	return eng, nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	e.closed = true
	for ch := range e.subs {
		close(ch)
		delete(e.subs, ch)
	}
	e.mu.Unlock()
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

func (e *Engine) Subscribe() (<-chan MiningEvent, func()) {
	ch := make(chan MiningEvent, 32)
	e.mu.Lock()
	if e.closed {
		close(ch)
		e.mu.Unlock()
		return ch, func() {}
	}
	e.subs[ch] = struct{}{}
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		if _, ok := e.subs[ch]; ok {
			delete(e.subs, ch)
			close(ch)
		}
		e.mu.Unlock()
	}
}

func (e *Engine) publish(ev MiningEvent) {
	if ev.Timestamp == 0 {
		ev.Timestamp = e.now().UTC().Unix()
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	for ch := range e.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (e *Engine) seedInitialStreakIfEmpty(ctx context.Context) {
	var cnt int
	if e.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM lv1x_streak`).Scan(&cnt) != nil || cnt > 0 {
		return
	}
	now := e.now().UTC().Unix()
	currentSlot := uint64(now / onchain.LV1XRoundIntervalSec)
	pattern := []struct {
		r    uint8
		move int
		pool float64
	}{
		{2, 2, 1.85}, {3, 11, 2.40}, {1, -9, 2.10}, {2, -1, 1.95},
		{4, 24, 3.15}, {2, 3, 2.05}, {0, -23, 2.80}, {3, 14, 2.55},
		{2, 0, 1.75}, {1, -12, 2.25}, {2, 4, 1.90}, {3, 8, 2.30},
		{1, -7, 2.15}, {2, -3, 1.95}, {4, 29, 3.40}, {2, 1, 2.00},
		{3, 9, 2.20}, {1, -16, 2.50}, {0, -22, 2.95}, {2, 2, 1.85},
		{3, 15, 2.60}, {2, -2, 1.90}, {1, -8, 2.10}, {2, 1, 2.05},
	}
	for i, item := range pattern {
		slot := currentSlot - uint64(len(pattern)-i)
		settledAt := int64(slot+1) * onchain.LV1XRoundIntervalSec
		_, _ = e.db.ExecContext(
			ctx,
			`INSERT OR IGNORE INTO lv1x_streak(round_slot, winning_range, code, move_bps, pool_sol, user_staked, user_won, sat_reward, settled_at) VALUES (?, ?, ?, ?, ?, 0, 0, 0, ?)`,
			int64(slot), int(item.r), BucketCodes[item.r], item.move, item.pool, settledAt,
		)
	}
}

func (e *Engine) liveBTCPrices(ctx context.Context) (openPrice float64, markPrice float64, moveBps int, ok bool) {
	if e.candleProvider == nil || e.fixture {
		return 0, 0, 0, false
	}
	bars, _, err := e.candleProvider(ctx, "10m", 2)
	if err != nil || len(bars) == 0 {
		bars, _, err = e.candleProvider(ctx, "1m", 10)
	}
	if err != nil || len(bars) == 0 {
		return 0, 0, 0, false
	}
	latest := bars[len(bars)-1]
	openP := latest.Open
	markP := latest.Close
	if openP <= 0 || markP <= 0 {
		return 0, 0, 0, false
	}
	bps := int(math.Round(((markP - openP) / openP) * 10000.0))
	return math.Round(openP*100) / 100, math.Round(markP*100) / 100, bps, true
}

func (e *Engine) CurrentRound(ctx context.Context, cluster string) RoundTelemetry {
	c := normalizeCluster(cluster)
	now := e.now().UTC().Unix()

	snap := e.readSnapshot(ctx, c, "")
	prog := nonEmptyStr(snap.ProgramID, custody.EffectiveDevnetProgramID())
	ledger := nonEmptyStr(snap.LedgerID, custody.EffectiveDevnetLedgerID())
	slot := snap.RoundSlot
	endsAt := snap.EndsAt
	if endsAt == 0 {
		slot, endsAt = deriveLV1XSlot(now, snap.Runtime)
	}
	rem := max(0, endsAt-now)

	phase := "commit"
	if rem <= 45 {
		phase = "reveal"
	}

	source := snap.Source
	if source == "" {
		source = "simulation"
	}
	bpfVerified := snap.ProgramLive && c == "devnet"

	openPrice := 94250.0
	markPrice := 94265.0
	moveBps := 2
	if liveOpen, liveMark, liveBps, ok := e.liveBTCPrices(ctx); ok {
		openPrice = liveOpen
		markPrice = liveMark
		moveBps = liveBps
	}

	crowdPool := [5]float64{0.34, 0.58, 0.92, 0.64, 0.38}
	modelProb := [5]float64{0.14, 0.22, 0.30, 0.21, 0.13}
	totalPool := 0.0
	for _, p := range crowdPool {
		totalPool += p
	}
	minerCount := uint32(18)
	innerBps := 5
	outerBps := 20
	btcVaultSat := uint64(125_000)

	if snap.Round != nil {
		if snap.Round.ActiveStake > 0 {
			totalPool = float64(snap.Round.ActiveStake) / 1e9
			// Proportional bucket distribution aligned to pool size when on-chain round is populated.
			for i := range 5 {
				crowdPool[i] = modelProb[i] * totalPool
			}
		}
		minerCount = snap.Round.Count
		if snap.Round.RangeInnerBps >= 4 {
			innerBps = int(snap.Round.RangeInnerBps)
		}
		if snap.Round.RangeOuterBps >= uint16(innerBps*2) {
			outerBps = int(snap.Round.RangeOuterBps)
		}
		if snap.Round.MinerBtcTotal > 0 {
			btcVaultSat = snap.Round.MinerBtcTotal
		}
	}

	buckets := make([]RoundBucket, 5)
	for i := range 5 {
		share := crowdPool[i] / maxFloat(totalPool, 1e-9)
		ev := 0.0
		if share > 0 {
			ev = modelProb[i] / share
		}
		buckets[i] = RoundBucket{
			Index:      uint8(i),
			Code:       BucketCodes[i],
			Label:      BucketLabels[i],
			PoolSOL:    crowdPool[i],
			CrowdShare: share,
			ModelProb:  modelProb[i],
			EVMultiple: float64(int(ev*100)) / 100.0,
		}
	}

	return RoundTelemetry{
		Cluster:        c,
		ProgramID:      prog,
		LedgerID:       ledger,
		RoundSlot:      slot,
		Phase:          phase,
		EndsAt:         endsAt,
		RemainingSec:   rem,
		OpenPriceUSD:   openPrice,
		MarkPriceUSD:   markPrice,
		MoveBps:        moveBps,
		InnerBps:       innerBps,
		OuterBps:       outerBps,
		TotalPoolSOL:   totalPool,
		MinerCount:     minerCount,
		BTCVaultSat:    btcVaultSat,
		Buckets:        buckets,
		Source:         source,
		BPFVerified:    bpfVerified,
		DeployedSHA256: onchain.LV1XDevnetELFSHA256,
	}
}

func (e *Engine) ListStreak(ctx context.Context, limit int) ([]StreakEntry, error) {
	if limit <= 0 || limit > 48 {
		limit = 24
	}
	rows, err := e.db.QueryContext(
		ctx,
		`SELECT round_slot, winning_range, code, move_bps, pool_sol, user_staked, user_won, sat_reward, settled_at FROM lv1x_streak ORDER BY round_slot DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rev []StreakEntry
	for rows.Next() {
		var item StreakEntry
		var staked, won int
		if err := rows.Scan(&item.RoundSlot, &item.WinningRange, &item.Code, &item.MoveBps, &item.PoolSOL, &staked, &won, &item.SatReward, &item.SettledAt); err != nil {
			return nil, err
		}
		item.UserStaked = staked == 1
		item.UserWon = won == 1
		item.InnerBps = 5
		item.OuterBps = 20
		rev = append(rev, item)
	}
	out := make([]StreakEntry, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out, rows.Err()
}

func (e *Engine) Status(ctx context.Context, cluster string) (MiningStatus, error) {
	c := normalizeCluster(cluster)
	round := e.CurrentRound(ctx, c)
	streak, err := e.ListStreak(ctx, 24)
	if err != nil {
		return MiningStatus{}, err
	}
	sessions, err := e.ListSessions(ctx)
	if err != nil {
		return MiningStatus{}, err
	}
	saved, _ := e.ListSavedStrategies(ctx)
	return MiningStatus{
		Cluster:         c,
		MainnetEnabled:  onchain.LV1XMainnetProgramID != "",
		MainnetGateNote: MainnetGateMessage,
		Round:           round,
		Streak:          streak,
		Presets:         BuiltInPresets(),
		SavedStrategies: saved,
		Sessions:        sessions,
	}, nil
}

func (e *Engine) PreviewCommit(ctx context.Context, req CommitRequest) CommitPreview {
	c := normalizeCluster(req.Cluster)
	round := e.CurrentRound(ctx, c)
	weights := BuiltInPresetWeights[CanonicalPresetID(req.StrategyID)]
	if len(req.Curve) == CurveBarCount {
		weights = BucketsFromCurve(req.Curve)
	}
	dom := DominantBucket(weights)
	if req.Selection != nil && *req.Selection <= 4 {
		dom = *req.Selection
	}
	lamports := uint64(req.AmountSOL * 1e9)
	if lamports < onchain.LV1XMinCommitLamports {
		lamports = onchain.LV1XMinCommitLamports
	}
	prev := CommitPreview{
		Cluster:        c,
		RoundSlot:      round.RoundSlot,
		AmountSOL:      float64(lamports) / 1e9,
		AmountLamports: lamports,
		DominantBucket: dom,
		DominantCode:   BucketCodes[dom],
		BucketWeights:  weights,
		ExpectedEVMult: ExpectedEVMultiple(weights, round.Buckets),
	}
	if c == "mainnet-beta" && onchain.LV1XMainnetProgramID == "" {
		prev.MainnetBlocked = true
		prev.MainnetReason = MainnetGateMessage
	}
	return prev
}

func actorForAutonomous(auto bool, taskID string) string {
	if !auto {
		return "owner"
	}
	if clean := strings.TrimSpace(taskID); clean != "" {
		return "task:" + clean
	}
	return "mining-engine"
}

func (e *Engine) recordEntry(ctx context.Context, walletID, cluster string, slot uint64, shardID uint8, page uint32, row uint8, kind string, sel *uint8, strat string, sol, sat, btc float64, saltHex, sig, status string) {
	var b [6]byte
	_, _ = rand.Read(b[:])
	id := "ment-" + hex.EncodeToString(b[:])
	nowSec := e.now().UTC().Unix()
	var selVal any
	if sel != nil {
		selVal = int(*sel)
	}
	_, _ = e.db.ExecContext(ctx, `
INSERT INTO lv1x_entries(id, wallet_id, cluster, round_slot, shard_id, page, entry_row, kind, selection, strategy_id, amount_sol, amount_sat, amount_btc, salt_hex, tx_signature, status, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, walletID, cluster, int64(slot), int(shardID), int(page), int(row), kind, selVal, strat, sol, sat, btc, saltHex, sig, status, nowSec,
	)
}

func (e *Engine) ListEntries(ctx context.Context, walletID string, limit int) ([]EntryHistoryItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT id, wallet_id, cluster, round_slot, kind, selection, strategy_id, amount_sol, amount_sat, amount_btc, tx_signature, status, created_at FROM lv1x_entries`
	var args []any
	if clean := strings.TrimSpace(walletID); clean != "" {
		query += ` WHERE wallet_id = ?`
		args = append(args, clean)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]EntryHistoryItem, 0)
	for rows.Next() {
		var item EntryHistoryItem
		var sel sql.NullInt64
		if err := rows.Scan(&item.ID, &item.WalletID, &item.Cluster, &item.RoundSlot, &item.Kind, &sel, &item.StrategyID, &item.AmountSOL, &item.AmountSAT, &item.AmountBTC, &item.TxSignature, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		if sel.Valid && sel.Int64 >= 0 && sel.Int64 <= 4 {
			v := uint8(sel.Int64)
			item.Selection = &v
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (e *Engine) ListClaimableItems(ctx context.Context, walletID string) ([]ClaimableRoundItem, error) {
	query := `SELECT round_slot, shard_id, page, entry_row, COALESCE(selection, 2), amount_sol, sol_claimed, btc_claimed FROM lv1x_entries WHERE kind = 'commit'`
	var args []any
	if clean := strings.TrimSpace(walletID); clean != "" {
		query += ` AND wallet_id = ?`
		args = append(args, clean)
	}
	query += ` AND (sol_claimed = 0 OR btc_claimed = 0) ORDER BY round_slot DESC LIMIT 25`
	rows, err := e.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ClaimableRoundItem, 0)
	for rows.Next() {
		var slot uint64
		var shardID, row, sel uint8
		var page uint32
		var amtSOL float64
		var solC, btcC int
		if err := rows.Scan(&slot, &shardID, &page, &row, &sel, &amtSOL, &solC, &btcC); err != nil {
			return nil, err
		}
		mask := uint8(1 << (row % 8))
		lamports := uint64(amtSOL * 0.90 * 1e9)
		satAtoms := uint64(amtSOL * 200.0 * 1e9)
		btcAtoms := uint64(amtSOL * 1200.0)
		out = append(out, ClaimableRoundItem{
			RoundSlot:    slot,
			ShardID:      shardID,
			Page:         page,
			EntryRow:     row,
			EntriesMask:  mask,
			Selection:    sel,
			SelectionCod: BucketCodes[sel%5],
			SolLamports:  lamports,
			SatAtoms:     satAtoms,
			VestedAtoms:  satAtoms,
			BtcAtoms:     btcAtoms,
			SolClaimed:   solC == 1,
			BtcClaimed:   btcC == 1,
			StakedSOL:    amtSOL,
		})
	}
	return out, rows.Err()
}

// SubmitCommit prepares and (when owner/auto-authorized) executes a RoleMining LV1X commit.
func (e *Engine) SubmitCommit(ctx context.Context, req CommitRequest) (custody.WalletReview, *custody.LedgerEntry, error) {
	c := normalizeCluster(req.Cluster)
	if c == "mainnet-beta" && onchain.LV1XMainnetProgramID == "" {
		return custody.WalletReview{}, nil, fmt.Errorf("%w: %s", custody.ErrPolicyDenied, MainnetGateMessage)
	}
	if e.custody == nil {
		return custody.WalletReview{}, nil, errors.New("custody backend unavailable")
	}
	round := e.CurrentRound(ctx, c)
	snap := e.readSnapshot(ctx, c, "")
	preview := e.PreviewCommit(ctx, req)

	shardID := req.ShardID
	var entryIndex uint32
	if snap.Round != nil {
		if snap.Round.ShardCount == 0 {
			shardID = onchain.LV1XUnshardedShardID
			entryIndex = snap.Round.Count
		} else {
			if shardID >= snap.Round.ShardCount {
				shardID = shardID % snap.Round.ShardCount
			}
			if snap.Shard != nil && snap.Shard.ShardID == shardID {
				entryIndex = snap.Shard.Count
			}
		}
	}
	nonce := req.Nonce
	if nonce == 0 {
		var seq int64
		_ = e.db.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM lv1x_entries WHERE wallet_id = ? AND round_slot = ? AND kind = 'commit'`,
			req.WalletID, int64(round.RoundSlot),
		).Scan(&seq)
		nonce = uint64(seq + 1)
	}

	saltHex := strings.TrimSpace(req.SaltHex)
	if saltHex == "" {
		var salt [32]byte
		_, _ = rand.Read(salt[:])
		saltHex = hex.EncodeToString(salt[:])
	}

	rev, err := e.custody.PrepareReview(ctx, custody.PrepareReviewRequest{
		WalletID:    req.WalletID,
		Operation:   custody.MiningOpCommit,
		AmountRaw:   preview.AmountLamports,
		IsBotOrAuto: req.Autonomous,
		Venue:       "wen-mining",
		MiningLV1X: &custody.MiningLV1XRequest{
			Cluster:        c,
			ProgramID:      round.ProgramID,
			LedgerID:       round.LedgerID,
			RoundSlot:      round.RoundSlot,
			ShardID:        shardID,
			EntryIndex:     entryIndex,
			Page:           entryIndex / onchain.LV1XPageEntries,
			EntryRow:       uint8(entryIndex % onchain.LV1XPageEntries),
			Nonce:          nonce,
			Selection:      preview.DominantBucket,
			AmountLamports: preview.AmountLamports,
			SaltHex:        saltHex,
			StrategyID:     CanonicalPresetID(req.StrategyID),
		},
	})
	if err != nil {
		return custody.WalletReview{}, nil, err
	}
	if req.DryRunPreview {
		return rev, nil, nil
	}
	entry, execErr := e.custody.ExecuteNativeReview(ctx, req.WalletID, rev.ID, actorForAutonomous(req.Autonomous, req.TaskID))
	if execErr != nil {
		return rev, nil, execErr
	}
	_, _ = e.db.ExecContext(ctx, `UPDATE lv1x_streak SET user_staked = 1 WHERE round_slot = ?`, int64(round.RoundSlot))
	sel := preview.DominantBucket
	e.recordEntry(ctx, req.WalletID, c, round.RoundSlot, shardID, entryIndex/onchain.LV1XPageEntries, uint8(entryIndex%onchain.LV1XPageEntries), "commit", &sel, CanonicalPresetID(req.StrategyID), preview.AmountSOL, 0, 0, saltHex, entry.TxSignature, entry.State)
	e.publish(MiningEvent{Event: "commit.submitted", Cluster: c, RoundSlot: round.RoundSlot})
	return rev, &entry, nil
}

// SubmitReveal prepares and executes an LV1X reveal transaction.
func (e *Engine) SubmitReveal(ctx context.Context, req RevealRequest) (custody.WalletReview, *custody.LedgerEntry, error) {
	c := normalizeCluster(req.Cluster)
	if c == "mainnet-beta" && onchain.LV1XMainnetProgramID == "" {
		return custody.WalletReview{}, nil, fmt.Errorf("%w: %s", custody.ErrPolicyDenied, MainnetGateMessage)
	}
	if e.custody == nil {
		return custody.WalletReview{}, nil, errors.New("custody backend unavailable")
	}
	rev, err := e.custody.PrepareReview(ctx, custody.PrepareReviewRequest{
		WalletID:    req.WalletID,
		Operation:   custody.MiningOpReveal,
		IsBotOrAuto: req.Autonomous,
		Venue:       "wen-mining",
		MiningLV1X: &custody.MiningLV1XRequest{
			Cluster:   c,
			ProgramID: custody.EffectiveDevnetProgramID(),
			LedgerID:  custody.EffectiveDevnetLedgerID(),
			RoundSlot: req.RoundSlot,
			ShardID:   req.ShardID,
			Page:      req.Page,
			EntryRow:  req.EntryRow,
			Selection: req.Selection,
			SaltHex:   req.SaltHex,
		},
	})
	if err != nil {
		return custody.WalletReview{}, nil, err
	}
	entry, execErr := e.custody.ExecuteNativeReview(ctx, req.WalletID, rev.ID, actorForAutonomous(req.Autonomous, req.TaskID))
	if execErr != nil {
		return rev, nil, execErr
	}
	sel := req.Selection
	e.recordEntry(ctx, req.WalletID, c, req.RoundSlot, req.ShardID, req.Page, req.EntryRow, "reveal", &sel, "", 0, 0, 0, req.SaltHex, entry.TxSignature, entry.State)
	e.publish(MiningEvent{Event: "reveal.submitted", Cluster: c, RoundSlot: req.RoundSlot})
	return rev, &entry, nil
}

// SubmitClaim prepares and executes an LV1X SOL, SAT, or cbBTC claim transaction via Custody V2.
func (e *Engine) SubmitClaim(ctx context.Context, req ClaimRequest) (custody.WalletReview, *custody.LedgerEntry, error) {
	c := normalizeCluster(req.Cluster)
	if c == "mainnet-beta" && onchain.LV1XMainnetProgramID == "" {
		return custody.WalletReview{}, nil, fmt.Errorf("%w: %s", custody.ErrPolicyDenied, MainnetGateMessage)
	}
	if e.custody == nil {
		return custody.WalletReview{}, nil, errors.New("custody backend unavailable")
	}
	op := custody.MiningOpClaim
	kind := "claim_sol"
	switch strings.ToLower(strings.TrimSpace(req.Asset)) {
	case "sat":
		op = custody.MiningOpClaimSat
		kind = "claim_sat"
	case "btc", "cbbtc":
		op = custody.MiningOpClaimBtc
		kind = "claim_btc"
	}
	rev, err := e.custody.PrepareReview(ctx, custody.PrepareReviewRequest{
		WalletID:    req.WalletID,
		Operation:   op,
		IsBotOrAuto: req.Autonomous,
		Venue:       "wen-mining",
		MiningLV1X: &custody.MiningLV1XRequest{
			Cluster:     c,
			ProgramID:   custody.EffectiveDevnetProgramID(),
			LedgerID:    custody.EffectiveDevnetLedgerID(),
			RoundSlot:   req.RoundSlot,
			ShardID:     req.ShardID,
			Page:        req.Page,
			EntryRow:    req.EntryRow,
			EntriesMask: uint8(1 << (req.EntryRow % 8)),
			BtcMint:     onchain.LV1XDevnetBTCMint,
			CreateATA:   req.CreateATA,
		},
	})
	if err != nil {
		return custody.WalletReview{}, nil, err
	}
	entry, execErr := e.custody.ExecuteNativeReview(ctx, req.WalletID, rev.ID, actorForAutonomous(req.Autonomous, req.TaskID))
	if execErr != nil {
		return rev, nil, execErr
	}
	if kind == "claim_sol" {
		_, _ = e.db.ExecContext(ctx, `UPDATE lv1x_entries SET sol_claimed = 1 WHERE wallet_id = ? AND round_slot = ?`, req.WalletID, int64(req.RoundSlot))
	} else if kind == "claim_btc" {
		_, _ = e.db.ExecContext(ctx, `UPDATE lv1x_entries SET btc_claimed = 1 WHERE wallet_id = ? AND round_slot = ?`, req.WalletID, int64(req.RoundSlot))
	}
	e.recordEntry(ctx, req.WalletID, c, req.RoundSlot, req.ShardID, req.Page, req.EntryRow, kind, nil, "", 0, 0, 0, "", entry.TxSignature, entry.State)
	e.publish(MiningEvent{Event: "claim.submitted", Cluster: c, RoundSlot: req.RoundSlot})
	return rev, &entry, nil
}

// SubmitClaimAll claims across claimable rounds for requested assets (default SOL, SAT, BTC).
func (e *Engine) SubmitClaimAll(ctx context.Context, req ClaimAllRequest) ([]custody.LedgerEntry, error) {
	items, err := e.ListClaimableItems(ctx, req.WalletID)
	if err != nil {
		return nil, err
	}
	assets := req.Assets
	if len(assets) == 0 {
		assets = []string{"sol", "sat", "btc"}
	}
	var out []custody.LedgerEntry
	for _, it := range items {
		for _, a := range assets {
			norm := strings.ToLower(strings.TrimSpace(a))
			if norm == "sol" && it.SolClaimed {
				continue
			}
			if norm == "btc" && it.BtcClaimed {
				continue
			}
			_, led, err := e.SubmitClaim(ctx, ClaimRequest{
				WalletID:  req.WalletID,
				Cluster:   req.Cluster,
				Asset:     norm,
				RoundSlot: it.RoundSlot,
				ShardID:   it.ShardID,
				Page:      it.Page,
				EntryRow:  it.EntryRow,
				CreateATA: true,
			})
			if err != nil {
				return out, err
			}
			if led != nil {
				out = append(out, *led)
			}
		}
	}
	return out, nil
}

func (e *Engine) SubmitStake(ctx context.Context, req StakeActionRequest) (custody.WalletReview, *custody.LedgerEntry, error) {
	return e.submitStakingOp(ctx, req, custody.MiningOpStake, "stake")
}

func (e *Engine) SubmitActivateStake(ctx context.Context, req StakeActionRequest) (custody.WalletReview, *custody.LedgerEntry, error) {
	return e.submitStakingOp(ctx, req, custody.MiningOpActivateStake, "activate_stake")
}

func (e *Engine) SubmitUnstake(ctx context.Context, req StakeActionRequest) (custody.WalletReview, *custody.LedgerEntry, error) {
	return e.submitStakingOp(ctx, req, custody.MiningOpUnstake, "unstake")
}

func (e *Engine) SubmitClaimStaking(ctx context.Context, req StakeActionRequest) (custody.WalletReview, *custody.LedgerEntry, error) {
	return e.submitStakingOp(ctx, req, custody.MiningOpClaimStaking, "claim_staking")
}

func (e *Engine) submitStakingOp(ctx context.Context, req StakeActionRequest, op, kind string) (custody.WalletReview, *custody.LedgerEntry, error) {
	c := normalizeCluster(req.Cluster)
	if c == "mainnet-beta" && onchain.LV1XMainnetProgramID == "" {
		return custody.WalletReview{}, nil, fmt.Errorf("%w: %s", custody.ErrPolicyDenied, MainnetGateMessage)
	}
	if e.custody == nil {
		return custody.WalletReview{}, nil, errors.New("custody backend unavailable")
	}
	atoms := uint64(req.AmountSAT * 1e9)
	rev, err := e.custody.PrepareReview(ctx, custody.PrepareReviewRequest{
		WalletID:  req.WalletID,
		Operation: op,
		Venue:     "wen-mining",
		MiningLV1X: &custody.MiningLV1XRequest{
			Cluster:        c,
			ProgramID:      custody.EffectiveDevnetProgramID(),
			LedgerID:       custody.EffectiveDevnetLedgerID(),
			SatAmountAtoms: atoms,
			BtcMint:        onchain.LV1XDevnetBTCMint,
			CreateATA:      req.CreateATA,
		},
	})
	if err != nil {
		return custody.WalletReview{}, nil, err
	}
	entry, execErr := e.custody.ExecuteNativeReview(ctx, req.WalletID, rev.ID, "owner")
	if execErr != nil {
		return rev, nil, execErr
	}
	e.recordEntry(ctx, req.WalletID, c, 0, 0, 0, 0, kind, nil, "", 0, req.AmountSAT, 0, "", entry.TxSignature, entry.State)
	e.publish(MiningEvent{Event: "stake.updated", Cluster: c})
	return rev, &entry, nil
}

// CreateSession opens and persists a local or managed LV1X mining session.
func (e *Engine) CreateSession(ctx context.Context, in SessionRecord) (SessionRecord, error) {
	c := normalizeCluster(in.Cluster)
	if c == "mainnet-beta" && onchain.LV1XMainnetProgramID == "" {
		return SessionRecord{}, fmt.Errorf("%w: %s", custody.ErrPolicyDenied, MainnetGateMessage)
	}
	now := e.now().UTC().Unix()
	var b [6]byte
	_, _ = rand.Read(b[:])
	roundsCount := in.RoundsCount
	if roundsCount <= 0 {
		roundsCount = max(1, len(in.SelectedSlots))
	}
	perRound := in.PerRoundSOL
	if perRound <= 0 {
		perRound = 0.02
	}
	deposit := in.DepositSOL
	if deposit <= 0 {
		deposit = float64(roundsCount) * perRound
	}
	schedMode := normalizeScheduleMode(in.ScheduleMode)
	skipWarn := ""
	if deposit < float64(roundsCount)*perRound*0.99 {
		skipWarn = "Deposit below total scheduled commitment; later rounds will rely on rolling SOL returns."
	}
	rec := SessionRecord{
		ID:              "msess-" + hex.EncodeToString(b[:]),
		WalletID:        strings.TrimSpace(in.WalletID),
		WalletHandle:    strings.TrimSpace(in.WalletHandle),
		Cluster:         c,
		StrategyID:      CanonicalPresetID(in.StrategyID),
		Mode:            normalizeMode(in.Mode),
		Status:          "active",
		ScheduleMode:    schedMode,
		HorizonLabel:    nonEmptyStr(in.HorizonLabel, "4h"),
		StartMode:       normalizeStartMode(in.StartMode),
		SelectedSlots:   in.SelectedSlots,
		RoundsCount:     roundsCount,
		PerRoundSOL:     perRound,
		DepositSOL:      deposit,
		RollingBudget:   in.RollingBudget,
		SkipRiskWarning: skipWarn,
		AutoClaim:       in.AutoClaim,
		StopOnReversal:  in.StopOnReversal,
		Curve:           NormalizeCurve(in.Curve),
		SessionRef:      in.SessionRef,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	raw, _ := json.Marshal(rec)
	if _, err := e.db.ExecContext(
		ctx,
		`INSERT INTO lv1x_sessions(id, wallet_id, wallet_handle, cluster, strategy_id, mode, status, payload_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.WalletID, rec.WalletHandle, rec.Cluster, rec.StrategyID, rec.Mode, rec.Status, string(raw), rec.CreatedAt, rec.UpdatedAt,
	); err != nil {
		return SessionRecord{}, err
	}
	e.publish(MiningEvent{Event: "session.updated", Cluster: rec.Cluster, SessionID: rec.ID})
	return rec, nil
}

// DepositSession tops up an existing managed LV1X mining session budget.
func (e *Engine) DepositSession(ctx context.Context, id string, depositSOL float64) (SessionRecord, error) {
	if depositSOL <= 0 {
		return SessionRecord{}, errors.New("depositSol must be positive")
	}
	var raw string
	if err := e.db.QueryRowContext(ctx, `SELECT payload_json FROM lv1x_sessions WHERE id = ?`, strings.TrimSpace(id)).Scan(&raw); err != nil {
		return SessionRecord{}, errors.New("mining session not found")
	}
	var rec SessionRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return SessionRecord{}, err
	}
	rec.DepositSOL += depositSOL
	rec.SkipRiskWarning = ""
	rec.UpdatedAt = e.now().UTC().Unix()
	up, _ := json.Marshal(rec)
	if _, err := e.db.ExecContext(ctx, `UPDATE lv1x_sessions SET payload_json = ?, updated_at = ? WHERE id = ?`, string(up), rec.UpdatedAt, rec.ID); err != nil {
		return SessionRecord{}, err
	}
	e.publish(MiningEvent{Event: "session.updated", Cluster: rec.Cluster, SessionID: rec.ID})
	return rec, nil
}

func (e *Engine) ListSessions(ctx context.Context) ([]SessionRecord, error) {
	rows, err := e.db.QueryContext(ctx, `SELECT payload_json FROM lv1x_sessions ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SessionRecord, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var rec SessionRecord
		if json.Unmarshal([]byte(raw), &rec) == nil {
			out = append(out, rec)
		}
	}
	return out, rows.Err()
}

func (e *Engine) SetSessionStatus(ctx context.Context, id, nextStatus string) (SessionRecord, error) {
	var raw string
	if err := e.db.QueryRowContext(ctx, `SELECT payload_json FROM lv1x_sessions WHERE id = ?`, strings.TrimSpace(id)).Scan(&raw); err != nil {
		return SessionRecord{}, errors.New("mining session not found")
	}
	var rec SessionRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return SessionRecord{}, err
	}
	rec.Status = nextStatus
	rec.UpdatedAt = e.now().UTC().Unix()
	up, _ := json.Marshal(rec)
	if _, err := e.db.ExecContext(ctx, `UPDATE lv1x_sessions SET status = ?, payload_json = ?, updated_at = ? WHERE id = ?`, rec.Status, string(up), rec.UpdatedAt, rec.ID); err != nil {
		return SessionRecord{}, err
	}
	e.publish(MiningEvent{Event: "session.updated", Cluster: rec.Cluster, SessionID: rec.ID})
	return rec, nil
}

func normalizeCluster(raw string) string {
	c := strings.ToLower(strings.TrimSpace(raw))
	if c == "mainnet" || c == "mainnet-beta" {
		return "mainnet-beta"
	}
	return "devnet"
}

func normalizeMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "auto", "bounded_auto":
		return "auto"
	case "semi_auto", "semi-auto":
		return "semi_auto"
	default:
		return "manual"
	}
}

func normalizeScheduleMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "choose", "cycle", "random":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "every"
	}
}

func normalizeStartMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "next", "plus2":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "now"
	}
}

func nonEmptyStr(a, fallback string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return fallback
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
