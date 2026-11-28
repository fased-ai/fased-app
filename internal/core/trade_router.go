package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/cex"
	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/decision"
	"github.com/fased-ai/fased-app/internal/equities"
	"github.com/fased-ai/fased-app/internal/fasedscript"
	"github.com/fased-ai/fased-app/internal/hyperliquid"
	"github.com/fased-ai/fased-app/internal/marketdata"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/trade"
)

// TradeAccountOption describes a selectable execution account surfaced to the #trade ticket and bot runner.
type TradeAccountOption struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Kind          string `json:"kind"` // paper | solana | hardware | exchange
	Role          string `json:"role,omitempty"`
	Custody       string `json:"custody,omitempty"`
	Cluster       string `json:"cluster,omitempty"`
	ExecutionMode string `json:"executionMode"`
	Address       string `json:"address,omitempty"`
	Venue         string `json:"venue,omitempty"`
}

func (s *Store) tradeAccountsView(ctx context.Context) []TradeAccountOption {
	out := []TradeAccountOption{
		{
			ID:            trade.ExecutionModePaper,
			Label:         "Paper",
			Kind:          "paper",
			ExecutionMode: trade.ExecutionModePaper,
		},
	}
	if s == nil {
		return out
	}
	if cs, err := s.custodyBackend(); err == nil && cs != nil {
		if wallets, wErr := cs.ListWallets(ctx); wErr == nil {
			for _, w := range wallets {
				if w.Custody != custody.CustodyNativeSigner || w.Role == custody.RoleVault || w.Role == custody.RoleWatch || w.Role == custody.RoleMining || w.Policy.Frozen {
					continue
				}
				out = append(out, TradeAccountOption{
					ID:            w.Handle,
					Label:         w.Name,
					Kind:          "solana",
					Role:          w.Role,
					Custody:       w.Custody,
					Cluster:       w.RPC.Cluster,
					ExecutionMode: trade.ExecutionModeLive,
					Address:       w.PublicKey,
					Venue:         marketdata.VenueSolana,
				})
			}
		}
	}
	s.pluginsMu.Lock()
	doc, err := s.loadDataPluginsLocked()
	var plugins []dataPluginRecord
	if err == nil && doc != nil {
		plugins = append(plugins, doc.Plugins...)
	}
	s.pluginsMu.Unlock()
	for _, p := range plugins {
		if p.Category == PluginCatExchanges && p.Enabled && p.TradingEnabled {
			out = append(out, TradeAccountOption{
				ID:            "plugin:" + p.ID,
				Label:         p.Name,
				Kind:          "exchange",
				ExecutionMode: trade.ExecutionModeLive,
				Venue:         p.ID,
			})
		}
	}
	return out
}

func (s *Store) isTradingVenueEnabled(venue string) bool {
	if s == nil {
		return false
	}
	if s.providerFixture && !s.enforceDataPlugins {
		return true
	}
	cleanVenue := strings.ToLower(strings.TrimSpace(venue))
	if cleanVenue == marketdata.VenueSolana || cleanVenue == "onchain" {
		if cs, cErr := s.custodyBackend(); cErr == nil && cs != nil {
			if wallets, err := cs.ListWallets(context.Background()); err == nil && len(wallets) > 0 {
				return true
			}
		}
		if _, _, ok := s.resolveSolanaRPC("mainnet-beta"); ok {
			return true
		}
		if _, _, ok := s.resolveSolanaRPC("devnet"); ok {
			return true
		}
		return false
	}
	s.pluginsMu.Lock()
	defer s.pluginsMu.Unlock()
	doc, err := s.loadDataPluginsLocked()
	if err != nil || doc == nil {
		return false
	}
	candidates := pluginIDsForMarketVenue(cleanVenue)
	for _, p := range doc.Plugins {
		if p.Category != PluginCatExchanges || !p.Enabled || !p.TradingEnabled {
			continue
		}
		for _, want := range candidates {
			if p.ID == want {
				return true
			}
		}
	}
	return false
}

func (s *Store) executeLiveTradeRoute(ctx context.Context, req trade.OrderRequest, refPrice float64) (trade.LiveExecReceipt, error) {
	if s.isExternalKillActiveForTrade(ctx) || s.isCustodyGlobalFrozen(ctx) {
		return trade.LiveExecReceipt{}, errGlobalKillSwitchActive
	}
	if ts := s.loadedOrExistingTradeService(); ts != nil {
		if snap, sErr := ts.GetSnapshot(ctx); sErr == nil && snap.KillSwitchActive {
			return trade.LiveExecReceipt{}, errGlobalKillSwitchActive
		}
	}
	isBotOrAuto := req.Mode == trade.ModeBot || req.Mode == trade.ModeAIAgent
	var (
		custodyBackend custody.Backend
		resolvedWallet *custody.WalletPublic
	)
	if strings.HasPrefix(req.AccountID, "@wallet:") || strings.HasPrefix(req.AccountID, "wallet:") {
		cs, cErr := s.custodyBackend()
		if cErr != nil || cs == nil {
			return trade.LiveExecReceipt{}, errors.New("custody signer unavailable")
		}
		resolved, rErr := cs.ResolveWallet(ctx, custody.WalletSelector{
			WalletHandle: strings.TrimSpace(req.AccountID),
			AgentID:      req.AgentID,
			IsAutonomous: isBotOrAuto,
			Venue:        req.Venue,
		})
		if rErr != nil {
			return trade.LiveExecReceipt{}, rErr
		}
		if !isBotOrAuto && !resolved.Connections.AllowTradeManual {
			return trade.LiveExecReceipt{}, fmt.Errorf("%w: manual #trade execution is disabled on %s", custody.ErrPolicyDenied, resolved.Handle)
		}
		if resolved.Custody == custody.CustodyHardware {
			return trade.LiveExecReceipt{}, fmt.Errorf("hardware wallet %s requires 2-step manual review & on-device signing in Wallets", resolved.Handle)
		}
		custodyBackend = cs
		resolvedWallet = &resolved
	}
	if !s.isTradingVenueEnabled(req.Venue) {
		return trade.LiveExecReceipt{}, fmt.Errorf("trading is disabled for venue %q; enable Trading in Settings -> Data & Feeds", req.Venue)
	}
	rt := s.stage4Runtime()
	if rt == nil || (rt.profile != signer.ExecutionProfileTestnet && rt.profile != signer.ExecutionProfileMainnetCapped) {
		return trade.LiveExecReceipt{}, errors.New("live execution disabled (--execution-profile=off); switch account to Paper or restart with --execution-profile=testnet|mainnet-capped")
	}

	var (
		rec    trade.LiveExecReceipt
		err    error
		estUSD float64
	)

	effPrice := req.EntryPrice
	if effPrice <= 0 {
		effPrice = refPrice
	}

	switch strings.ToLower(strings.TrimSpace(req.Venue)) {
	case marketdata.VenueHyperliquid:
		rec, err = s.executeLiveHyperliquidOrder(ctx, req, refPrice)

	case marketdata.VenueSolana, "onchain":
		if resolvedWallet != nil && custodyBackend != nil {
			// Never silently translate a #trade buy/sell signal into an outbound solana.nativeTransfer.
			// Live Solana trading on @wallet: requires a signer-verified jupiter.swap quote.
			return trade.LiveExecReceipt{}, fmt.Errorf("%w: live Solana trading on %s requires a verified Jupiter swap quote (direct outbound SOL transfers are disabled in #trade)", custody.ErrPolicyDenied, resolvedWallet.Handle)
		}
		estMicro := uint64(math.Max(1000, math.Round(req.Quantity*refPrice*1_000_000)))
		memo := fmt.Sprintf("fased-%s-%s-%s", req.Symbol, req.Side, strconv.FormatFloat(req.Quantity, 'f', -1, 64))
		if len(memo) > 64 {
			memo = memo[:64]
		}
		if rt.profile == signer.ExecutionProfileMainnetCapped && rt.mainnetExecutor != nil {
			res, mErr := s.executeSolanaMainnet(ctx, SolanaMainnetExecuteRequest{
				Asset:             req.Symbol,
				Memo:              memo,
				EstimatedMicroUSD: estMicro,
			})
			if mErr != nil {
				return trade.LiveExecReceipt{}, mErr
			}
			rec = trade.LiveExecReceipt{
				VenueOrderID: res.OperationID,
				TxSignature:  res.Signature,
				FilledPrice:  refPrice,
				Immediate:    res.Confirmed || res.Broadcasted,
			}
		} else {
			res, dErr := s.rehearseSolanaDevnet(ctx, SolanaDevnetRehearseRequest{
				Asset:             req.Symbol,
				Memo:              memo,
				EstimatedMicroUSD: estMicro,
			})
			if dErr != nil {
				return trade.LiveExecReceipt{}, dErr
			}
			rec = trade.LiveExecReceipt{
				VenueOrderID: res.OperationID,
				TxSignature:  res.Signature,
				FilledPrice:  refPrice,
				Immediate:    res.Confirmed || res.Broadcasted,
			}
		}

	case marketdata.VenueLighter:
		res, lErr := s.rehearseLighter(ctx, LighterRehearseRequest{
			Symbol:    req.Symbol,
			Side:      strings.ToUpper(req.Side),
			Price:     strconv.FormatFloat(refPrice, 'f', -1, 64),
			Size:      strconv.FormatFloat(req.Quantity, 'f', -1, 64),
			OrderType: strings.ToUpper(req.Strategy),
			Submit:    true,
		})
		if lErr != nil {
			return trade.LiveExecReceipt{}, lErr
		}
		rec = trade.LiveExecReceipt{
			VenueOrderID: res.OperationID,
			FilledPrice:  refPrice,
			Immediate:    res.Submission != nil,
		}

	case "polymarket":
		res, pErr := s.rehearsePolymarket(ctx, PolymarketRehearseRequest{
			TokenID: req.Symbol,
			Side:    strings.ToUpper(req.Side),
			Price:   strconv.FormatFloat(refPrice, 'f', -1, 64),
			Shares:  strconv.FormatFloat(req.Quantity, 'f', -1, 64),
			Submit:  true,
		})
		if pErr != nil {
			return trade.LiveExecReceipt{}, pErr
		}
		rec = trade.LiveExecReceipt{
			VenueOrderID: res.OperationID,
			FilledPrice:  refPrice,
			Immediate:    res.Submission != nil,
		}

	case "kalshi":
		res, kErr := s.rehearseKalshi(ctx, KalshiRehearseRequest{
			Ticker: req.Symbol,
			Side:   strings.ToLower(req.Side),
			Count:  strconv.FormatFloat(math.Max(1, math.Round(req.Quantity)), 'f', 0, 64),
			Price:  strconv.FormatFloat(refPrice, 'f', -1, 64),
			Submit: true,
		})
		if kErr != nil {
			return trade.LiveExecReceipt{}, kErr
		}
		rec = trade.LiveExecReceipt{
			VenueOrderID: res.OperationID,
			FilledPrice:  refPrice,
			Immediate:    res.Submission != nil,
		}

	case marketdata.VenueBinance, marketdata.VenueCoinbase, marketdata.VenueKraken, marketdata.VenueBybit, marketdata.VenueOKX:
		st5, sErr := s.ensureStage5Runtime()
		if sErr != nil {
			return trade.LiveExecReceipt{}, sErr
		}
		env := "testnet"
		orderType := "MARKET"
		if req.Strategy == trade.StrategyLimit || rt.profile == signer.ExecutionProfileMainnetCapped {
			orderType = "LIMIT"
		}
		if rt.profile == signer.ExecutionProfileMainnetCapped {
			maxOrd := uint64(250_000_000)
			if rt.mainnetPolicy.MaxOrderMicroUSD > 0 && rt.mainnetPolicy.MaxOrderMicroUSD < maxOrd {
				maxOrd = rt.mainnetPolicy.MaxOrderMicroUSD
			}
			if cErr := st5.cex.ConfigureMainnetCapped(maxOrd); cErr != nil {
				return trade.LiveExecReceipt{}, cErr
			}
			env = "mainnet"
		}
		sym := strings.ToUpper(strings.TrimSpace(req.Symbol))
		if sym != "" && !strings.ContainsAny(sym, "-/") && !strings.HasSuffix(sym, "USD") && !strings.HasSuffix(sym, "USDT") && !strings.HasSuffix(sym, "USDC") {
			sym += "-USD"
		}
		cexRec, cErr := st5.cex.SubmitOrder(ctx, cex.OrderRequest{
			Venue:       strings.ToLower(strings.TrimSpace(req.Venue)),
			Symbol:      sym,
			Side:        strings.ToUpper(req.Side),
			OrderType:   orderType,
			Quantity:    req.Quantity,
			Price:       effPrice,
			Environment: env,
		})
		if cErr != nil {
			return trade.LiveExecReceipt{}, cErr
		}
		fillPx := cexRec.Price
		if fillPx <= 0 {
			fillPx = effPrice
		}
		estUSD = cexRec.Quantity * fillPx
		rec = trade.LiveExecReceipt{
			VenueOrderID: cexRec.ID,
			TxSignature:  cexRec.Digest,
			FilledPrice:  fillPx,
			Immediate:    true,
		}

	case marketdata.VenueAlpaca, "ibkr":
		st5, sErr := s.ensureStage5Runtime()
		if sErr != nil {
			return trade.LiveExecReceipt{}, sErr
		}
		env := "paper"
		orderType := "MARKET"
		if req.Strategy == trade.StrategyLimit || rt.profile == signer.ExecutionProfileMainnetCapped {
			orderType = "LIMIT"
		}
		if rt.profile == signer.ExecutionProfileMainnetCapped {
			maxOrd := uint64(250_000_000)
			if rt.mainnetPolicy.MaxOrderMicroUSD > 0 && rt.mainnetPolicy.MaxOrderMicroUSD < maxOrd {
				maxOrd = rt.mainnetPolicy.MaxOrderMicroUSD
			}
			if cErr := st5.equities.ConfigureMainnetCapped(maxOrd); cErr != nil {
				return trade.LiveExecReceipt{}, cErr
			}
			env = "live"
		}
		acct := strings.TrimSpace(req.AccountID)
		if strings.EqualFold(req.Venue, "ibkr") {
			if acct == "" || strings.HasPrefix(strings.ToLower(acct), "plugin:") {
				if env == "live" {
					acct = "U0000001"
				} else {
					acct = "DU0000001"
				}
			}
		} else if strings.HasPrefix(strings.ToLower(acct), "plugin:") {
			acct = ""
		}
		eqRec, eErr := st5.equities.SubmitOrder(ctx, equities.OrderRequest{
			Venue:       strings.ToLower(strings.TrimSpace(req.Venue)),
			AccountID:   acct,
			Symbol:      strings.ToUpper(strings.TrimSpace(req.Symbol)),
			Side:        strings.ToUpper(req.Side),
			OrderType:   orderType,
			Quantity:    req.Quantity,
			LimitPrice:  effPrice,
			Environment: env,
		})
		if eErr != nil {
			return trade.LiveExecReceipt{}, eErr
		}
		fillPx := eqRec.LimitPrice
		if fillPx <= 0 {
			fillPx = effPrice
		}
		estUSD = eqRec.Quantity * fillPx
		rec = trade.LiveExecReceipt{
			VenueOrderID: eqRec.ID,
			FilledPrice:  fillPx,
			Immediate:    true,
		}

	default:
		return trade.LiveExecReceipt{}, fmt.Errorf("live execution is not supported for venue %q; switch account to Paper", req.Venue)
	}
	if err != nil {
		return trade.LiveExecReceipt{}, err
	}
	if eng := s.loadedTasksEngine(); eng != nil {
		if payload, mErr := json.Marshal(map[string]any{
			"venue":        req.Venue,
			"symbol":       req.Symbol,
			"side":         req.Side,
			"quantity":     req.Quantity,
			"filledPrice":  rec.FilledPrice,
			"venueOrderId": rec.VenueOrderID,
			"notionalUsd":  estUSD,
		}); mErr == nil {
			eng.EmitEvent(ctx, "trade.fill", string(payload))
		}
	}
	return rec, nil
}

func (s *Store) executeLiveHyperliquidOrder(ctx context.Context, req trade.OrderRequest, refPrice float64) (trade.LiveExecReceipt, error) {
	s.ensureHyperliquidIPC(ctx)
	hlRT := s.hyperliquidRuntime()
	if hlRT == nil {
		return trade.LiveExecReceipt{}, signer.ErrUnavailable
	}
	hlRT.mu.Lock()
	needRefresh := len(hlRT.markets.Markets) == 0
	hlRT.mu.Unlock()
	if needRefresh {
		if err := s.refreshHyperliquidState(ctx, hlRT); err != nil {
			return trade.LiveExecReceipt{}, err
		}
	}

	var cloid [16]byte
	if _, err := rand.Read(cloid[:]); err != nil {
		return trade.LiveExecReceipt{}, err
	}
	now := s.now().Unix()
	tif := "Gtc"
	if req.Strategy == trade.StrategyMarket {
		tif = "Ioc"
	}
	draft := hyperliquid.OrderDraft{
		Asset:         strings.ToUpper(strings.TrimSuffix(req.Symbol, "-PERP")),
		IsBuy:         req.Side == trade.SideBuy,
		LimitPrice:    strconv.FormatFloat(refPrice, 'g', 5, 64),
		Size:          strconv.FormatFloat(req.Quantity, 'f', -1, 64),
		Leverage:      1,
		TimeInForce:   tif,
		ClientOrderID: "0x" + hex.EncodeToString(cloid[:]),
	}

	hlRT.mu.Lock()
	floor := uint64(s.now().UnixMilli())
	if hlRT.lastNonce >= floor {
		floor = hlRT.lastNonce + 1
	}
	if hlRT.nextNonce < floor {
		hlRT.nextNonce = floor
	} else {
		hlRT.nextNonce++
	}
	levNonce := hlRT.nextNonce
	orderNonce := levNonce + 1
	hlRT.nextNonce = orderNonce
	markets := hlRT.markets
	policy := hlRT.policy
	signerBackend := hlRT.signer
	client := hlRT.client
	hlRT.mu.Unlock()

	cappedProfile, previewMaxOrder := s.stage4MainnetCapped()
	previewMainnetCapped := cappedProfile && policy.Network == "mainnet"
	if !previewMainnetCapped {
		previewMaxOrder = 0
	}
	preview, _, err := hyperliquid.PreviewOrderForProfile(markets, policy, draft, levNonce, orderNonce, now, previewMainnetCapped, previewMaxOrder)
	if err != nil {
		return trade.LiveExecReceipt{}, err
	}

	signCtx, cancelSign := context.WithTimeout(ctx, 5*time.Second)
	signed, err := signerBackend.SignOrder(signCtx, preview.Intent, policy)
	cancelSign()
	if err != nil {
		return trade.LiveExecReceipt{}, err
	}

	subCtx, cancelSub := context.WithTimeout(ctx, 5*time.Second)
	res, subErr := client.SubmitSignedOrder(subCtx, preview.Intent, signed, policy.VaultAddress)
	cancelSub()
	if subErr != nil {
		return trade.LiveExecReceipt{}, subErr
	}
	if res.State != "" && res.State != "ok" && res.State != "resting" && res.State != "filled" {
		return trade.LiveExecReceipt{}, fmt.Errorf("hyperliquid rejected order (%s): %s", res.State, res.Message)
	}
	filledPx := refPrice
	if res.AvgPrice != "" {
		if parsed, pErr := strconv.ParseFloat(res.AvgPrice, 64); pErr == nil && parsed > 0 {
			filledPx = parsed
		}
	}
	return trade.LiveExecReceipt{
		VenueOrderID: strconv.FormatUint(res.OrderID, 10),
		FilledPrice:  filledPx,
		Immediate:    res.State == "filled" || req.Strategy == trade.StrategyMarket,
	}, nil
}

func (s *Store) evaluateFasedScriptBot(ctx context.Context, bot trade.TradeBot, candles []marketdata.Candle) (string, string, string, error) {
	ql, err := s.quantLabService()
	if err != nil {
		return "", "", "", err
	}
	rec, ok, err := ql.GetScript(ctx, bot.ScriptID)
	if err != nil {
		return "", "", "", err
	}
	if !ok {
		return "", "", "", fmt.Errorf("fasedscript %q not found", bot.ScriptID)
	}
	prog, err := fasedscript.Compile(rec.Source)
	if err != nil {
		return "", "", "", err
	}
	res, err := prog.Eval(candles, nil)
	if err != nil {
		return "", "", "", err
	}
	last := len(candles) - 1
	if last < 0 {
		return "hold", "", "", nil
	}
	if last < len(res.Strategy.Long) && res.Strategy.Long[last] {
		return "enter_long", trade.SideBuy, "FasedScript bot " + bot.Name + " long signal", nil
	}
	if last < len(res.Strategy.Short) && res.Strategy.Short[last] {
		return "enter_short", trade.SideSell, "FasedScript bot " + bot.Name + " short signal", nil
	}
	return "hold", "", "", nil
}

func (s *Store) evaluateAIAgentBot(ctx context.Context, bot trade.TradeBot, candles []marketdata.Candle) (string, string, string, error) {
	if len(candles) < 3 {
		return "hold", "", "", nil
	}
	first := candles[0].Close
	last := candles[len(candles)-1].Close
	pctMove := 0.0
	if first > 0 {
		pctMove = ((last - first) / first) * 100
	}
	req := decision.EvaluateRequest{
		State: fmt.Sprintf("bot=%s agent=%s venue=%s symbol=%s interval=%s close=%.4f pctMove=%.2f",
			bot.Name, bot.AgentID, bot.Venue, bot.Symbol, bot.Interval, last, pctMove),
		Questions: map[string]decision.QuestionSpec{
			"bullish": {
				Type:         decision.TypeNoul,
				Instructions: "Probability that price continues upward with disciplined risk/reward.",
			},
		},
	}
	var resp decision.EvaluateResponse
	if decSvc, dErr := s.decisionService(); dErr == nil && decSvc != nil {
		resp, dErr = decSvc.Evaluate(ctx, req)
		if dErr != nil {
			resp, _ = decision.EvaluateFallback(req, s.now())
		}
	} else {
		resp, _ = decision.EvaluateFallback(req, s.now())
	}
	ans, ok := resp.Answers["bullish"]
	if ok && ans.Noul != nil && ans.Confidence >= 0.70 {
		if *ans.Noul >= 0.72 {
			return "ai_long", trade.SideBuy, fmt.Sprintf("AI bot %s long (p=%.2f)", bot.Name, *ans.Noul), nil
		}
		if *ans.Noul <= 0.28 {
			return "ai_short", trade.SideSell, fmt.Sprintf("AI bot %s short (p=%.2f)", bot.Name, *ans.Noul), nil
		}
	}
	return "hold", "", "", nil
}

func (s *Store) evaluateTradeBots(ctx context.Context, ts *trade.Service) {
	if s == nil || ts == nil {
		return
	}
	if _, err := ts.EvaluateBots(ctx); err != nil && !errors.Is(err, context.Canceled) {
		coreLogger.Warn("trade bots sweep held", "err", sanitizeDiagValue(err.Error(), 200))
	}
}
