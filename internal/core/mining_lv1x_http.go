package core

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/httpapi"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/wenlv1x"
)

var (
	wenLV1XMu      sync.RWMutex
	wenLV1XEngines = map[*Store]*wenlv1x.Engine{}
)

func init() {
	registerStoreCloseHook(func(s *Store) {
		wenLV1XMu.Lock()
		eng := wenLV1XEngines[s]
		delete(wenLV1XEngines, s)
		wenLV1XMu.Unlock()
		if eng != nil {
			_ = eng.Close()
		}
	})
}

func (s *Store) loadedWENLV1XEngine() *wenlv1x.Engine {
	if s == nil {
		return nil
	}
	wenLV1XMu.RLock()
	eng := wenLV1XEngines[s]
	wenLV1XMu.RUnlock()
	return eng
}

func (s *Store) wenLV1XCandleProvider() wenlv1x.CandleProvider {
	return func(ctx context.Context, interval string, limit int) ([]wenlv1x.CandleBar, string, error) {
		svc, err := s.marketDataService()
		if err != nil || svc == nil {
			return nil, "", errors.New("marketdata unavailable")
		}
		if interval == "10m" {
			rawLimit := min(1800, max(60, limit*10))
			series, gErr := svc.GetCandles(ctx, marketdata.VenueBinance, "BTCUSDT", "1m", marketdata.TransformOptions{Style: marketdata.StyleStandard}, rawLimit)
			if gErr != nil || len(series.Candles) == 0 {
				return nil, "", gErr
			}
			grouped := aggregate1mTo10m(series.Candles, limit)
			return grouped, series.Source, nil
		}
		if interval == "10s" {
			series, gErr := svc.GetCandles(ctx, marketdata.VenueBinance, "BTCUSDT", "1m", marketdata.TransformOptions{Style: marketdata.StyleStandard}, max(20, limit/6+2))
			if gErr != nil || len(series.Candles) == 0 {
				return nil, "", gErr
			}
			out := make([]wenlv1x.CandleBar, 0, len(series.Candles)*6)
			for _, c := range series.Candles {
				stepDelta := (c.Close - c.Open) / 6.0
				for k := range 6 {
					o := c.Open + stepDelta*float64(k)
					cl := c.Open + stepDelta*float64(k+1)
					hi := math.Max(o, cl) + (c.High-math.Max(c.Open, c.Close))/6.0
					lo := math.Min(o, cl) - (math.Min(c.Open, c.Close)-c.Low)/6.0
					out = append(out, wenlv1x.CandleBar{
						Time:   c.Time + int64(k*10),
						Open:   o,
						High:   hi,
						Low:    lo,
						Close:  cl,
						Volume: c.Volume / 6.0,
					})
				}
			}
			if len(out) > limit {
				out = out[len(out)-limit:]
			}
			return out, series.Source, nil
		}
		series, gErr := svc.GetCandles(ctx, marketdata.VenueBinance, "BTCUSDT", interval, marketdata.TransformOptions{Style: marketdata.StyleStandard}, limit)
		if gErr != nil || len(series.Candles) == 0 {
			return nil, "", gErr
		}
		out := make([]wenlv1x.CandleBar, len(series.Candles))
		for i, c := range series.Candles {
			out[i] = wenlv1x.CandleBar{
				Time:   c.Time,
				Open:   c.Open,
				High:   c.High,
				Low:    c.Low,
				Close:  c.Close,
				Volume: c.Volume,
			}
		}
		return out, series.Source, nil
	}
}

func aggregate1mTo10m(bars []marketdata.Candle, limit int) []wenlv1x.CandleBar {
	if len(bars) == 0 {
		return nil
	}
	var out []wenlv1x.CandleBar
	var cur *wenlv1x.CandleBar
	var curSlot int64 = -1
	for _, b := range bars {
		slot := b.Time / onchain.LV1XRoundIntervalSec
		slotTs := slot * onchain.LV1XRoundIntervalSec
		if cur == nil || slot != curSlot {
			if cur != nil {
				out = append(out, *cur)
			}
			curSlot = slot
			cur = &wenlv1x.CandleBar{
				Time:      slotTs,
				Open:      b.Open,
				High:      b.High,
				Low:       b.Low,
				Close:     b.Close,
				Volume:    b.Volume,
				RoundSlot: uint64(slot),
			}
		} else {
			if b.High > cur.High {
				cur.High = b.High
			}
			if b.Low < cur.Low {
				cur.Low = b.Low
			}
			cur.Close = b.Close
			cur.Volume += b.Volume
		}
		if cur.Open > 0 {
			cur.MoveBps = int(math.Round(((cur.Close - cur.Open) / cur.Open) * 10000.0))
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (s *Store) wenLV1XEngine() (*wenlv1x.Engine, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	if existing := s.loadedWENLV1XEngine(); existing != nil {
		return existing, nil
	}
	cust, _ := s.custodyBackend()
	candleFn := s.wenLV1XCandleProvider()
	wenLV1XMu.Lock()
	defer wenLV1XMu.Unlock()
	if existing := wenLV1XEngines[s]; existing != nil {
		return existing, nil
	}
	eng, err := wenlv1x.NewEngine(wenlv1x.Config{
		DataDir:        s.dir,
		Custody:        cust,
		CandleProvider: candleFn,
		FixtureMode:    s.fixture != nil || !s.rpcAdmission,
	})
	if err != nil {
		return nil, err
	}
	wenLV1XEngines[s] = eng
	return eng, nil
}

func writeMiningErr(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, custody.ErrPolicyDenied) || errors.Is(err, custody.ErrRoleDenied) {
		code = http.StatusForbidden
	}
	writeError(w, code, err.Error())
}

func (h *HTTPHandler) miningLV1XAPI(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "mining", 180) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	eng, err := h.store.wenLV1XEngine()
	if err != nil {
		writeError(w, 500, "LV1X mining engine unavailable")
		return
	}
	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/mining"), "/")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	switch {
	case rel == "" || rel == "status":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		st, sErr := eng.Status(ctx, r.URL.Query().Get("cluster"))
		if sErr != nil {
			writeError(w, 500, sErr.Error())
			return
		}
		writeJSON(w, 200, st)

	case rel == "round":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		writeJSON(w, 200, eng.CurrentRound(ctx, r.URL.Query().Get("cluster")))

	case rel == "presets":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		writeJSON(w, 200, map[string]any{"presets": wenlv1x.BuiltInPresets()})

	case rel == "candles":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		resp, cErr := eng.GetCandles(ctx, r.URL.Query().Get("interval"), limit)
		if cErr != nil {
			writeError(w, 500, cErr.Error())
			return
		}
		writeJSON(w, 200, resp)

	case rel == "protocol-stats":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		resp, pErr := eng.ProtocolStats(ctx, r.URL.Query().Get("cluster"))
		if pErr != nil {
			writeError(w, 500, pErr.Error())
			return
		}
		writeJSON(w, 200, resp)

	case rel == "leaderboard":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		resp, lErr := eng.Leaderboard(ctx, r.URL.Query().Get("cluster"), r.URL.Query().Get("window"))
		if lErr != nil {
			writeError(w, 500, lErr.Error())
			return
		}
		writeJSON(w, 200, resp)

	case rel == "portfolio":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		resp, pErr := eng.Portfolio(ctx, r.URL.Query().Get("cluster"), r.URL.Query().Get("walletId"))
		if pErr != nil {
			writeError(w, 500, pErr.Error())
			return
		}
		writeJSON(w, 200, resp)

	case rel == "staking":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		resp, sErr := eng.StakingOverview(ctx, r.URL.Query().Get("cluster"), r.URL.Query().Get("walletId"))
		if sErr != nil {
			writeError(w, 500, sErr.Error())
			return
		}
		writeJSON(w, 200, resp)

	case rel == "history":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		streak, sErr := eng.ListStreak(ctx, limit)
		if sErr != nil {
			writeError(w, 500, sErr.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"streak": streak})

	case rel == "strategies":
		switch r.Method {
		case http.MethodGet:
			list, sErr := eng.ListSavedStrategies(ctx)
			if sErr != nil {
				writeError(w, 500, sErr.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"strategies": list, "presets": wenlv1x.BuiltInPresets()})
		case http.MethodPost:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in wenlv1x.SavedStrategy
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			saved, sErr := eng.SaveStrategy(ctx, in)
			if sErr != nil {
				writeError(w, 400, sErr.Error())
				return
			}
			writeJSON(w, 201, map[string]any{"strategy": saved})
		default:
			writeError(w, 405, "method not allowed")
		}

	case rel == "strategies/simulate":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in wenlv1x.SimulateRequest
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		sim, sErr := eng.SimulateStrategy(ctx, in)
		if sErr != nil {
			writeError(w, 400, sErr.Error())
			return
		}
		writeJSON(w, 200, sim)

	case strings.HasPrefix(rel, "strategies/"):
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		id := strings.TrimPrefix(rel, "strategies/")
		if err := eng.DeleteStrategy(ctx, id); err != nil {
			writeError(w, 404, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": id})

	case rel == "preview":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in wenlv1x.CommitRequest
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		writeJSON(w, 200, eng.PreviewCommit(ctx, in))

	case rel == "commit":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in wenlv1x.CommitRequest
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		if strings.TrimSpace(in.WalletID) == "" {
			writeError(w, 400, "walletId is required")
			return
		}
		if !in.DryRunPreview && !in.Autonomous {
			if hasPW, _ := h.store.hasPassword(ctx); hasPW {
				if _, ok := h.requireStepUpCtx(w, r, token, "mining-commit", in.WalletID); !ok {
					return
				}
			}
		}
		rev, led, cErr := eng.SubmitCommit(ctx, in)
		if cErr != nil {
			writeMiningErr(w, cErr)
			return
		}
		writeJSON(w, 200, map[string]any{"review": rev, "ledger": led})

	case rel == "reveal":
		if r.Method != http.MethodPost || !h.v2Mutation(w, r, token, true) {
			return
		}
		var in wenlv1x.RevealRequest
		if !httpapi.DecodeStrictJSON(w, r, 32*1024, &in) {
			return
		}
		if strings.TrimSpace(in.WalletID) == "" {
			writeError(w, 400, "walletId is required")
			return
		}
		rev, led, rErr := eng.SubmitReveal(ctx, in)
		if rErr != nil {
			writeMiningErr(w, rErr)
			return
		}
		writeJSON(w, 200, map[string]any{"review": rev, "ledger": led})

	case rel == "claim":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in wenlv1x.ClaimRequest
		if !httpapi.DecodeStrictJSON(w, r, 32*1024, &in) {
			return
		}
		if strings.TrimSpace(in.WalletID) == "" {
			writeError(w, 400, "walletId is required")
			return
		}
		rev, led, cErr := eng.SubmitClaim(ctx, in)
		if cErr != nil {
			writeMiningErr(w, cErr)
			return
		}
		writeJSON(w, 200, map[string]any{"review": rev, "ledger": led})

	case rel == "claim-all":
		if r.Method != http.MethodPost || !h.v2Mutation(w, r, token, true) {
			return
		}
		var in wenlv1x.ClaimAllRequest
		if !httpapi.DecodeStrictJSON(w, r, 32*1024, &in) {
			return
		}
		if strings.TrimSpace(in.WalletID) == "" {
			writeError(w, 400, "walletId is required")
			return
		}
		ledgers, cErr := eng.SubmitClaimAll(ctx, in)
		if cErr != nil {
			writeMiningErr(w, cErr)
			return
		}
		writeJSON(w, 200, map[string]any{"ledgers": ledgers, "count": len(ledgers)})

	case rel == "stake", rel == "activate-stake", rel == "unstake", rel == "claim-staking":
		if r.Method != http.MethodPost || !h.v2Mutation(w, r, token, true) {
			return
		}
		var in wenlv1x.StakeActionRequest
		if !httpapi.DecodeStrictJSON(w, r, 32*1024, &in) {
			return
		}
		if strings.TrimSpace(in.WalletID) == "" {
			writeError(w, 400, "walletId is required")
			return
		}
		if rel == "stake" || rel == "unstake" {
			if hasPW, _ := h.store.hasPassword(ctx); hasPW {
				if _, ok := h.requireStepUpCtx(w, r, token, "mining-"+rel, in.WalletID); !ok {
					return
				}
			}
		}
		var rev custody.WalletReview
		var led *custody.LedgerEntry
		var sErr error
		switch rel {
		case "stake":
			rev, led, sErr = eng.SubmitStake(ctx, in)
		case "activate-stake":
			rev, led, sErr = eng.SubmitActivateStake(ctx, in)
		case "unstake":
			rev, led, sErr = eng.SubmitUnstake(ctx, in)
		case "claim-staking":
			rev, led, sErr = eng.SubmitClaimStaking(ctx, in)
		}
		if sErr != nil {
			writeMiningErr(w, sErr)
			return
		}
		writeJSON(w, 200, map[string]any{"review": rev, "ledger": led})

	case rel == "sessions":
		switch r.Method {
		case http.MethodGet:
			list, lErr := eng.ListSessions(ctx)
			if lErr != nil {
				writeError(w, 500, lErr.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"sessions": list})
		case http.MethodPost:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in wenlv1x.SessionRecord
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			if in.Mode == "auto" {
				if hasPW, _ := h.store.hasPassword(ctx); hasPW {
					if _, ok := h.requireStepUpCtx(w, r, token, "mining-session", nonEmpty(in.WalletID, "session")); !ok {
						return
					}
				}
			}
			sess, sErr := eng.CreateSession(ctx, in)
			if sErr != nil {
				writeMiningErr(w, sErr)
				return
			}
			writeJSON(w, 201, map[string]any{"session": sess})
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasPrefix(rel, "sessions/"):
		parts := strings.Split(rel, "/")
		if len(parts) != 3 || (parts[2] != "stop" && parts[2] != "close" && parts[2] != "deposit") {
			writeError(w, 404, "not found")
			return
		}
		if r.Method != http.MethodPost || !h.v2Mutation(w, r, token, true) {
			return
		}
		if parts[2] == "deposit" {
			var body struct {
				DepositSOL float64 `json:"depositSol"`
			}
			if !httpapi.DecodeStrictJSON(w, r, 16*1024, &body) {
				return
			}
			sess, dErr := eng.DepositSession(ctx, parts[1], body.DepositSOL)
			if dErr != nil {
				writeError(w, 400, dErr.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"session": sess})
			return
		}
		nextStatus := "stopped"
		if parts[2] == "close" {
			nextStatus = "closed"
		}
		sess, sErr := eng.SetSessionStatus(ctx, parts[1], nextStatus)
		if sErr != nil {
			writeError(w, 404, sErr.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"session": sess})

	case rel == "policy":
		cust, cErr := h.store.custodyBackend()
		if cErr != nil || cust == nil {
			writeError(w, 503, "custody signer unavailable")
			return
		}
		walletID := strings.TrimSpace(r.URL.Query().Get("walletId"))
		switch r.Method {
		case http.MethodGet:
			if walletID == "" {
				writeError(w, 400, "walletId required")
				return
			}
			wPub, gErr := cust.GetWallet(ctx, walletID)
			if gErr != nil {
				writeError(w, 404, gErr.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"wallet": wPub})
		case http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in struct {
				WalletID           string               `json:"walletId"`
				ApprovalMode       string               `json:"approvalMode"`
				AutoApproveMaxUSD  float64              `json:"autoApproveMaxUsd"`
				MaxPerTxLamports   uint64               `json:"maxPerTxLamports,string"`
				MaxDailyLamports   uint64               `json:"maxDailyLamports,string"`
				Mining             custody.MiningPolicy `json:"mining"`
				ConfirmLoosen      bool                 `json:"confirmLoosen"`
				ExpectedPolicyHash string               `json:"expectedPolicyHash"`
			}
			if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
				return
			}
			if strings.TrimSpace(in.WalletID) == "" {
				writeError(w, 400, "walletId is required")
				return
			}
			if hasPW, _ := h.store.hasPassword(ctx); hasPW {
				if _, ok := h.requireStepUpCtx(w, r, token, "mining-policy", in.WalletID); !ok {
					return
				}
			}
			cur, gErr := cust.GetWallet(ctx, in.WalletID)
			if gErr != nil {
				writeError(w, 404, gErr.Error())
				return
			}
			nextPol := cur.Policy
			if in.ApprovalMode != "" {
				nextPol.ApprovalMode = in.ApprovalMode
			}
			if in.AutoApproveMaxUSD > 0 {
				nextPol.AutoApproveMaxUSD = in.AutoApproveMaxUSD
			}
			nextPol.Operations = custody.AllMiningOperations()
			prog := nonEmpty(in.Mining.ProgramID, custody.EffectiveDevnetProgramID())
			nextPol.Programs = []string{custody.SolanaSystemProgram, custody.SolanaComputeBudget, custody.SolanaTokenProgram, custody.SolanaATAProgram, prog}
			if in.MaxPerTxLamports > 0 {
				nextPol.SOLCap.MaxPerTxRaw = in.MaxPerTxLamports
			}
			if in.MaxDailyLamports > 0 {
				nextPol.SOLCap.MaxDailyRaw = in.MaxDailyLamports
			}
			nextPol.Mining = &in.Mining
			expHash := nonEmpty(in.ExpectedPolicyHash, cur.Policy.PolicyHash)
			upW, uErr := cust.UpdatePolicy(ctx, in.WalletID, nextPol, in.ConfirmLoosen, expHash, "owner")
			if uErr != nil {
				writeMiningErr(w, uErr)
				return
			}
			raw, _ := json.Marshal(upW)
			var out map[string]any
			_ = json.Unmarshal(raw, &out)
			writeJSON(w, 200, map[string]any{"wallet": out})
		default:
			writeError(w, 405, "method not allowed")
		}

	default:
		writeError(w, 404, "not found")
	}
}
