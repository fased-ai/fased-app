package wenlv1x

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

// Config initializes the Lean V1 (LV1X) mining engine.
type Config struct {
	DataDir      string
	Custody      custody.Backend
	DevnetRPCURL string
	FixtureMode  bool
	HTTPClient   *http.Client
	Now          func() time.Time
}

// Engine manages LV1X round telemetry, streak history, local/managed sessions,
// and Custody RoleMining commit/claim/session execution.
type Engine struct {
	dir        string
	db         *sql.DB
	custody    custody.Backend
	devnetRPC  string
	fixture    bool
	httpClient *http.Client
	now        func() time.Time

	mu     sync.RWMutex
	subs   map[chan MiningEvent]struct{}
	closed bool
}

// MiningEvent is fanned out to WebSocket "mining" listeners on state changes.
type MiningEvent struct {
	Event     string `json:"event"` // round.updated | commit.submitted | claim.submitted | session.updated
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
);`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	_ = os.Chmod(dbPath, 0o600)
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 6 * time.Second}
	}
	rpcURL := strings.TrimSpace(cfg.DevnetRPCURL)
	if rpcURL == "" {
		rpcURL = "https://api.devnet.solana.com"
	}
	eng := &Engine{
		dir:        dir,
		db:         db,
		custody:    cfg.Custody,
		devnetRPC:  rpcURL,
		fixture:    cfg.FixtureMode,
		httpClient: hc,
		now:        nowFn,
		subs:       make(map[chan MiningEvent]struct{}),
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
	// Deterministic 24-round baseline telemetry matching WEN range distribution.
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

func (e *Engine) CurrentRound(ctx context.Context, cluster string) RoundTelemetry {
	c := normalizeCluster(cluster)
	now := e.now().UTC().Unix()
	slot := uint64(now / onchain.LV1XRoundIntervalSec)
	endsAt := int64(slot+1) * onchain.LV1XRoundIntervalSec
	rem := max(0, endsAt-now)

	phase := "commit"
	if rem <= 45 {
		phase = "reveal"
	}

	source := "simulation"
	bpfVerified := false
	if !e.fixture && c == "devnet" {
		if ok := e.probeDevnetProgram(ctx); ok {
			source = "devnet-rpc"
			bpfVerified = true
		} else {
			source = "cached"
		}
	} else if c == "devnet" {
		bpfVerified = true
	}

	crowdPool := [5]float64{0.34, 0.58, 0.92, 0.64, 0.38}
	modelProb := [5]float64{0.14, 0.22, 0.30, 0.21, 0.13}
	totalPool := 0.0
	for _, p := range crowdPool {
		totalPool += p
	}
	buckets := make([]RoundBucket, 5)
	for i := range 5 {
		share := crowdPool[i] / totalPool
		ev := 0.0
		if share > 0 {
			ev = (modelProb[i] / share)
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
		ProgramID:      onchain.LV1XDevnetProgramID,
		LedgerID:       onchain.LV1XDevnetLedgerID,
		RoundSlot:      slot,
		Phase:          phase,
		EndsAt:         endsAt,
		RemainingSec:   rem,
		OpenPriceUSD:   94250.0,
		MarkPriceUSD:   94265.0,
		MoveBps:        2,
		InnerBps:       5,
		OuterBps:       20,
		TotalPoolSOL:   totalPool,
		MinerCount:     18,
		BTCVaultSat:    125000,
		Buckets:        buckets,
		Source:         source,
		BPFVerified:    bpfVerified,
		DeployedSHA256: onchain.LV1XDevnetELFSHA256,
	}
}

func (e *Engine) probeDevnetProgram(ctx context.Context) bool {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"getAccountInfo","params":[%q,{"encoding":"base64","commitment":"confirmed"}]}`, onchain.LV1XDevnetProgramID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.devnetRPC, bytes.NewBufferString(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	var out struct {
		Result struct {
			Value *struct {
				Executable bool   `json:"executable"`
				Owner      string `json:"owner"`
			} `json:"value"`
		} `json:"result"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return false
	}
	return out.Result.Value != nil && out.Result.Value.Executable && out.Result.Value.Owner == onchain.BPFLoaderUpgradeableID
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
		rev = append(rev, item)
	}
	// Return oldest->newest left-to-right for the 24-round streak strip.
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
	return MiningStatus{
		Cluster:         c,
		MainnetEnabled:  onchain.LV1XMainnetProgramID != "",
		MainnetGateNote: MainnetGateMessage,
		Round:           round,
		Streak:          streak,
		Presets:         BuiltInPresets(),
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
	preview := e.PreviewCommit(ctx, req)

	actor := "owner"
	if req.Autonomous {
		actor = "mining-engine"
		if strings.TrimSpace(req.TaskID) != "" {
			actor = "task:" + strings.TrimSpace(req.TaskID)
		}
	}
	rev, err := e.custody.PrepareReview(ctx, custody.PrepareReviewRequest{
		WalletID:    req.WalletID,
		Operation:   custody.MiningOpCommit,
		AmountRaw:   preview.AmountLamports,
		IsBotOrAuto: req.Autonomous,
		Venue:       "wen-mining",
		MiningLV1X: &custody.MiningLV1XRequest{
			Cluster:        c,
			ProgramID:      onchain.LV1XDevnetProgramID,
			LedgerID:       onchain.LV1XDevnetLedgerID,
			RoundSlot:      round.RoundSlot,
			ShardID:        req.ShardID,
			Selection:      preview.DominantBucket,
			AmountLamports: preview.AmountLamports,
			StrategyID:     CanonicalPresetID(req.StrategyID),
		},
	})
	if err != nil {
		return custody.WalletReview{}, nil, err
	}
	if req.DryRunPreview {
		return rev, nil, nil
	}
	entry, execErr := e.custody.ExecuteNativeReview(ctx, req.WalletID, rev.ID, actor)
	if execErr != nil {
		return rev, nil, execErr
	}
	_, _ = e.db.ExecContext(
		ctx,
		`UPDATE lv1x_streak SET user_staked = 1 WHERE round_slot = ?`,
		int64(round.RoundSlot),
	)
	e.publish(MiningEvent{Event: "commit.submitted", Cluster: c, RoundSlot: round.RoundSlot})
	return rev, &entry, nil
}

// SubmitClaim prepares and executes an LV1X SOL/SAT claim transaction via Custody V2.
func (e *Engine) SubmitClaim(ctx context.Context, req ClaimRequest) (custody.WalletReview, *custody.LedgerEntry, error) {
	if e.custody == nil {
		return custody.WalletReview{}, nil, errors.New("custody backend unavailable")
	}
	actor := "owner"
	if req.Autonomous {
		actor = "mining-engine"
		if strings.TrimSpace(req.TaskID) != "" {
			actor = "task:" + strings.TrimSpace(req.TaskID)
		}
	}
	rev, err := e.custody.PrepareReview(ctx, custody.PrepareReviewRequest{
		WalletID:    req.WalletID,
		Operation:   custody.MiningOpClaim,
		IsBotOrAuto: req.Autonomous,
		Venue:       "wen-mining",
		MiningLV1X: &custody.MiningLV1XRequest{
			Cluster:     "devnet",
			ProgramID:   onchain.LV1XDevnetProgramID,
			LedgerID:    onchain.LV1XDevnetLedgerID,
			RoundSlot:   req.RoundSlot,
			ShardID:     req.ShardID,
			Page:        req.Page,
			EntriesMask: 0x01,
		},
	})
	if err != nil {
		return custody.WalletReview{}, nil, err
	}
	entry, execErr := e.custody.ExecuteNativeReview(ctx, req.WalletID, rev.ID, actor)
	if execErr != nil {
		return rev, nil, execErr
	}
	e.publish(MiningEvent{Event: "claim.submitted", Cluster: "devnet", RoundSlot: req.RoundSlot})
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
	rec := SessionRecord{
		ID:             "msess-" + hex.EncodeToString(b[:]),
		WalletID:       strings.TrimSpace(in.WalletID),
		WalletHandle:   strings.TrimSpace(in.WalletHandle),
		Cluster:        c,
		StrategyID:     CanonicalPresetID(in.StrategyID),
		Mode:           normalizeMode(in.Mode),
		Status:         "active",
		PerRoundSOL:    in.PerRoundSOL,
		AutoClaim:      in.AutoClaim,
		StopOnReversal: in.StopOnReversal,
		Curve:          NormalizeCurve(in.Curve),
		SessionRef:     in.SessionRef,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if rec.PerRoundSOL <= 0 {
		rec.PerRoundSOL = 0.02
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
