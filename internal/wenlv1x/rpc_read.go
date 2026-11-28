package wenlv1x

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/onchain"
)

type cachedRPCSnapshot struct {
	fetchedAt int64
	cluster   string
	walletID  string
	data      rpcSnapshot
}

type clusterBinding struct {
	Cluster   string
	ProgramID string
	LedgerID  string
	Mint      string
	BTCMint   string
	Enabled   bool
	Manifest  *onchain.LV1XVerifiedManifest
}

type rpcSnapshot struct {
	Source         string
	ProgramID      string
	LedgerID       string
	Mint           string
	BTCMint        string
	MainnetActive  bool
	RoundSlot      uint64
	EndsAt         int64
	Runtime        *onchain.LV1XRuntimeAccount
	Round          *onchain.LV1XRoundAccount
	Shard          *onchain.LV1XRoundShardAccount
	Staking        *onchain.LV1XStakingStateAccount
	Treasury       *onchain.LV1XTreasuryStateAccount
	StakePos       *onchain.LV1XStakePositionAccount
	WalletSol      float64
	WalletSat      float64
	WalletBtc      float64
	ProgramLive    bool
	ClaimableItems []ClaimableRoundItem
}

func sanitizeRPCHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "rpc"
	}
	return u.Scheme + "://" + u.Host
}

func (e *Engine) resolveClusterBinding(ctx context.Context, cluster string) clusterBinding {
	c := normalizeCluster(cluster)
	devProg := custody.EffectiveDevnetProgramID()
	devLedger := custody.EffectiveDevnetLedgerID()
	devMint, _ := onchain.LV1XMintPDA(devProg, devLedger)
	out := clusterBinding{
		Cluster:   c,
		ProgramID: devProg,
		LedgerID:  devLedger,
		Mint:      devMint,
		BTCMint:   onchain.LV1XDevnetBTCMint,
		Enabled:   c == "devnet",
	}
	if e.custody == nil {
		return out
	}
	vm, err := e.custody.GetVerifiedMiningManifest(ctx)
	if err != nil || vm == nil {
		return out
	}
	out.Manifest = vm
	if c == "mainnet-beta" {
		out.Enabled = false
		if vm.Compatible {
			if cl, ok := vm.Payload.Clusters["mainnet-beta"]; ok && cl.Status == "active" && strings.TrimSpace(cl.ProgramID) != "" && strings.TrimSpace(cl.LedgerID) != "" {
				out.ProgramID = strings.TrimSpace(cl.ProgramID)
				out.LedgerID = strings.TrimSpace(cl.LedgerID)
				if strings.TrimSpace(cl.Mint) != "" {
					out.Mint = strings.TrimSpace(cl.Mint)
				} else {
					out.Mint, _ = onchain.LV1XMintPDA(out.ProgramID, out.LedgerID)
				}
				out.BTCMint = nonEmptyStr(cl.BTCMint, onchain.LV1XMainnetBTCMint)
				out.Enabled = true
			}
		}
		return out
	}
	if cl, ok := vm.Payload.Clusters["devnet"]; ok && cl.Status == "active" && strings.TrimSpace(cl.ProgramID) != "" && strings.TrimSpace(cl.LedgerID) != "" {
		// Honor explicit env override when set; otherwise prefer active verified manifest.
		if strings.TrimSpace(custody.EffectiveDevnetProgramID()) == onchain.LV1XDevnetProgramID {
			out.ProgramID = strings.TrimSpace(cl.ProgramID)
			out.LedgerID = strings.TrimSpace(cl.LedgerID)
			out.Mint = nonEmptyStr(cl.Mint, devMint)
			out.BTCMint = nonEmptyStr(cl.BTCMint, onchain.LV1XDevnetBTCMint)
		}
	}
	return out
}

func (e *Engine) callSolanaRPC(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if e.fixture || strings.TrimSpace(e.devnetRPC) == "" {
		return nil, errors.New("fixture mode active")
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.devnetRPC, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("rpc request init failed (%s)", sanitizeRPCHost(e.devnetRPC))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rpc unreachable (%s)", sanitizeRPCHost(e.devnetRPC))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rpc status %d (%s)", resp.StatusCode, sanitizeRPCHost(e.devnetRPC))
	}
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("rpc decode failed (%s)", sanitizeRPCHost(e.devnetRPC))
	}
	if out.Error != nil {
		return nil, fmt.Errorf("rpc error %d (%s)", out.Error.Code, sanitizeRPCHost(e.devnetRPC))
	}
	return out.Result, nil
}

func (e *Engine) getMultipleAccountsRaw(ctx context.Context, addrs []string) ([][]byte, []uint64, error) {
	raw, err := e.callSolanaRPC(ctx, "getMultipleAccounts", []any{
		addrs,
		map[string]any{"encoding": "base64", "commitment": "confirmed"},
	})
	if err != nil {
		return nil, nil, err
	}
	var res struct {
		Value []*struct {
			Lamports uint64    `json:"lamports"`
			Data     [2]string `json:"data"`
		} `json:"value"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, nil, err
	}
	out := make([][]byte, len(addrs))
	lamps := make([]uint64, len(addrs))
	for i := 0; i < len(addrs) && i < len(res.Value); i++ {
		if res.Value[i] == nil {
			continue
		}
		lamps[i] = res.Value[i].Lamports
		if res.Value[i].Data[0] != "" {
			if b, dErr := base64.StdEncoding.DecodeString(res.Value[i].Data[0]); dErr == nil {
				out[i] = b
			}
		}
	}
	return out, lamps, nil
}

func (e *Engine) resolveMiningWallet(ctx context.Context, cluster, preferredID string) *custody.WalletPublic {
	if e.custody == nil {
		return nil
	}
	c := normalizeCluster(cluster)
	if clean := strings.TrimSpace(preferredID); clean != "" {
		if w, err := e.custody.GetWallet(ctx, clean); err == nil && w.Role == custody.RoleMining && w.Custody == custody.CustodyNativeSigner {
			return &w
		}
	}
	wallets, err := e.custody.ListWallets(ctx)
	if err != nil {
		return nil
	}
	var firstMining *custody.WalletPublic
	for i := range wallets {
		w := wallets[i]
		if w.Role != custody.RoleMining || w.Custody != custody.CustodyNativeSigner {
			continue
		}
		if firstMining == nil {
			cp := w
			firstMining = &cp
		}
		wCluster := normalizeCluster(w.RPC.Cluster)
		if w.Policy.Mining != nil && strings.TrimSpace(w.Policy.Mining.Cluster) != "" {
			wCluster = normalizeCluster(w.Policy.Mining.Cluster)
		}
		if wCluster == c {
			cp := w
			return &cp
		}
	}
	return firstMining
}

// fetchAccounts routes on-chain RPC reads through the custody daemon's encrypted wallet RPC
// whenever a mining wallet is bound, ensuring Mainnet/Devnet API keys never leave fased-signer.
func (e *Engine) fetchAccounts(ctx context.Context, cluster, walletID string, addrs []string) ([][]byte, []uint64, string, error) {
	if len(addrs) == 0 {
		return nil, nil, normalizeCluster(cluster), nil
	}
	c := normalizeCluster(cluster)
	if !e.fixture && e.custody != nil {
		wid := strings.TrimSpace(walletID)
		if wid == "" {
			if w := e.resolveMiningWallet(ctx, c, ""); w != nil {
				wid = w.ID
			}
		}
		if wid != "" {
			res, err := e.custody.ReadWalletAccountsRPC(ctx, wid, addrs)
			if err == nil {
				src := "devnet-rpc"
				if normalizeCluster(res.Cluster) == "mainnet-beta" || c == "mainnet-beta" {
					src = "mainnet-rpc"
				}
				return res.Accounts, res.Lamports, src, nil
			}
			// Do not fall back from mainnet to public devnet RPC on mainnet cluster.
			if c == "mainnet-beta" {
				return nil, nil, "mainnet-rpc", err
			}
		}
	}
	if e.fixture {
		return nil, nil, "simulation", errors.New("fixture mode")
	}
	if c == "mainnet-beta" {
		return nil, nil, "mainnet-rpc", errors.New("mainnet RPC requires bound mining wallet")
	}
	accts, lamps, err := e.getMultipleAccountsRaw(ctx, addrs)
	return accts, lamps, "devnet-rpc", err
}

func parseSPLTokenAmountLE(data []byte) uint64 {
	// Standard SPL Token Account layout: mint[32] + owner[32] + amount[8] (offsets 64..72).
	if len(data) < 72 {
		return 0
	}
	return binary.LittleEndian.Uint64(data[64:72])
}

func (e *Engine) fetchWalletBalances(ctx context.Context, cluster, walletID, owner, satMint, btcMint string) (sol float64, sat float64, btc float64) {
	if owner == "" {
		return 0, 0, 0
	}
	addrs := []string{owner}
	satIdx, btcIdx := -1, -1
	if satMint != "" {
		if ata, err := onchain.LV1XAssociatedTokenAddress(owner, satMint); err == nil {
			satIdx = len(addrs)
			addrs = append(addrs, ata)
		}
	}
	if btcMint != "" {
		if ata, err := onchain.LV1XAssociatedTokenAddress(owner, btcMint); err == nil {
			btcIdx = len(addrs)
			addrs = append(addrs, ata)
		}
	}
	accts, lamps, _, err := e.fetchAccounts(ctx, cluster, walletID, addrs)
	if err == nil && len(lamps) > 0 {
		sol = float64(lamps[0]) / 1e9
		if satIdx > 0 && satIdx < len(accts) {
			sat = float64(parseSPLTokenAmountLE(accts[satIdx])) / 1e9
		}
		if btcIdx > 0 && btcIdx < len(accts) {
			btc = float64(parseSPLTokenAmountLE(accts[btcIdx])) / 1e8
		}
		return sol, sat, btc
	}
	if normalizeCluster(cluster) == "mainnet-beta" {
		return 0, 0, 0
	}
	// Legacy direct fallback when ReadWalletAccountsRPC is not configured cleanly on devnet.
	if rawBal, rErr := e.callSolanaRPC(ctx, "getBalance", []any{owner, map[string]any{"commitment": "confirmed"}}); rErr == nil {
		var bRes struct {
			Value uint64 `json:"value"`
		}
		if json.Unmarshal(rawBal, &bRes) == nil {
			sol = float64(bRes.Value) / 1e9
		}
	}
	readToken := func(mint string, decimals float64) float64 {
		if mint == "" {
			return 0
		}
		ata, aErr := onchain.LV1XAssociatedTokenAddress(owner, mint)
		if aErr != nil {
			return 0
		}
		rawTok, tErr := e.callSolanaRPC(ctx, "getTokenAccountBalance", []any{ata, map[string]any{"commitment": "confirmed"}})
		if tErr != nil {
			return 0
		}
		var tRes struct {
			Value *struct {
				Amount string `json:"amount"`
			} `json:"value"`
		}
		if json.Unmarshal(rawTok, &tRes) == nil && tRes.Value != nil {
			if atoms, pErr := strconv.ParseUint(tRes.Value.Amount, 10, 64); pErr == nil {
				return float64(atoms) / decimals
			}
		}
		return 0
	}
	sat = readToken(satMint, 1e9)
	btc = readToken(btcMint, 1e8)
	return sol, sat, btc
}

func deriveLV1XSlot(nowSec int64, rt *onchain.LV1XRuntimeAccount) (uint64, int64) {
	if rt != nil && rt.Epoch > 0 && nowSec+onchain.LV1XRoundIntervalSec >= rt.Epoch {
		slot := uint64((nowSec - rt.Epoch + onchain.LV1XRoundIntervalSec) / onchain.LV1XRoundIntervalSec)
		endsAt := rt.Epoch + int64(slot)*onchain.LV1XRoundIntervalSec
		return slot, endsAt
	}
	slot := uint64((nowSec + onchain.LV1XRoundIntervalSec) / onchain.LV1XRoundIntervalSec)
	endsAt := int64(slot) * onchain.LV1XRoundIntervalSec
	return slot, endsAt
}

func winningBucketFromMoveBps(moveBps, innerBps, outerBps int) uint8 {
	if innerBps < 1 {
		innerBps = 5
	}
	if outerBps <= innerBps {
		outerBps = 20
	}
	switch {
	case moveBps < -outerBps:
		return 0
	case moveBps < -innerBps:
		return 1
	case moveBps <= innerBps:
		return 2
	case moveBps <= outerBps:
		return 3
	default:
		return 4
	}
}

func (e *Engine) syncRecentRoundsOnChain(ctx context.Context, cluster, walletID, prog, ledger string, currentSlot uint64, limit int) {
	if e.fixture || currentSlot <= 1 || limit <= 0 {
		return
	}
	nowSec := e.now().UTC().Unix()
	key := "streak:" + cluster + ":" + prog + ":" + ledger
	e.cacheMu.RLock()
	lastSync := e.streakSyncAt[key]
	e.cacheMu.RUnlock()
	if nowSec-lastSync < 15 {
		return
	}
	e.cacheMu.Lock()
	e.streakSyncAt[key] = nowSec
	e.cacheMu.Unlock()

	if limit > 24 {
		limit = 24
	}
	startSlot := uint64(1)
	if currentSlot > uint64(limit) {
		startSlot = currentSlot - uint64(limit)
	}
	slots := make([]uint64, 0, limit)
	addrs := make([]string, 0, limit)
	for s := startSlot; s < currentSlot; s++ {
		if rkAddr, err := onchain.LV1XRoundPDA(prog, ledger, s); err == nil {
			slots = append(slots, s)
			addrs = append(addrs, rkAddr)
		}
	}
	if len(addrs) == 0 {
		return
	}
	raws, _, _, err := e.fetchAccounts(ctx, cluster, walletID, addrs)
	if err != nil {
		return
	}
	for i := range slots {
		if i >= len(raws) || len(raws[i]) != onchain.LV1XSpaceRound {
			continue
		}
		rk, dErr := onchain.DecodeLV1XRound(raws[i])
		if dErr != nil || (rk.Stage < 3 && (rk.Open == nil || rk.Close == nil)) {
			continue
		}
		var moveBps int
		if rk.Open != nil && rk.Close != nil && *rk.Open > 0 {
			moveBps = int(math.Round(((float64(*rk.Close) - float64(*rk.Open)) / float64(*rk.Open)) * 10000.0))
		}
		win := winningBucketFromMoveBps(moveBps, int(rk.RangeInnerBps), int(rk.RangeOuterBps))
		pool := float64(rk.TotalStake) / 1e9
		if pool <= 0 {
			pool = float64(rk.ActiveStake) / 1e9
		}
		settledAt := rk.Start + onchain.LV1XRoundIntervalSec
		if rk.Start <= 0 {
			settledAt = int64(slots[i]+1) * onchain.LV1XRoundIntervalSec
		}
		_, _ = e.db.ExecContext(ctx, `
INSERT INTO lv1x_streak(round_slot, winning_range, code, move_bps, pool_sol, user_staked, user_won, sat_reward, settled_at)
VALUES (?, ?, ?, ?, ?, 0, 0, 0, ?)
ON CONFLICT(round_slot) DO UPDATE SET
  winning_range = excluded.winning_range,
  code = excluded.code,
  move_bps = excluded.move_bps,
  pool_sol = excluded.pool_sol,
  settled_at = excluded.settled_at`,
			int64(slots[i]), int(win), BucketCodes[win], moveBps, pool, settledAt,
		)
	}
}

func (e *Engine) readSnapshot(ctx context.Context, cluster, walletID string) rpcSnapshot {
	c := normalizeCluster(cluster)
	bind := e.resolveClusterBinding(ctx, c)
	prog := bind.ProgramID
	ledger := bind.LedgerID
	mintAddr := bind.Mint
	btcMint := bind.BTCMint
	nowSec := e.now().UTC().Unix()

	if c == "mainnet-beta" && !bind.Enabled {
		return rpcSnapshot{Source: "gated", ProgramID: prog, LedgerID: ledger, Mint: mintAddr, BTCMint: btcMint}
	}
	if e.fixture {
		slot, endsAt := deriveLV1XSlot(nowSec, nil)
		return rpcSnapshot{
			Source:        "simulation",
			ProgramID:     prog,
			LedgerID:      ledger,
			Mint:          mintAddr,
			BTCMint:       btcMint,
			MainnetActive: bind.Enabled && c == "mainnet-beta",
			RoundSlot:     slot,
			EndsAt:        endsAt,
			ProgramLive:   true,
		}
	}

	var walletPub string
	if w := e.resolveMiningWallet(ctx, c, walletID); w != nil {
		if walletID == "" {
			walletID = w.ID
		}
		walletPub = w.PublicKey
	}

	key := c + ":" + prog + ":" + ledger + ":" + walletID
	e.cacheMu.RLock()
	cached, hasCache := e.rpcCache[key]
	e.cacheMu.RUnlock()
	if hasCache && nowSec-cached.fetchedAt <= 3 {
		return cached.data
	}

	rtAddr, _ := onchain.LV1XRuntimePDA(prog, ledger)
	stAddr, _ := onchain.LV1XStakingStatePDA(prog, ledger)
	trAddr, _ := onchain.LV1XTreasuryStatePDA(prog, ledger)

	addrs := []string{prog, rtAddr, stAddr, trAddr}
	posIdx := -1
	if walletPub != "" {
		if posAddr, err := onchain.LV1XStakePositionPDA(prog, ledger, walletPub); err == nil {
			posIdx = len(addrs)
			addrs = append(addrs, posAddr)
		}
	}

	rCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raws, _, rpcSource, err := e.fetchAccounts(rCtx, c, walletID, addrs)
	if err != nil {
		if hasCache {
			out := cached.data
			out.Source = "cached"
			return out
		}
		slot, endsAt := deriveLV1XSlot(nowSec, nil)
		return rpcSnapshot{
			Source:        "cached",
			ProgramID:     prog,
			LedgerID:      ledger,
			Mint:          mintAddr,
			BTCMint:       btcMint,
			MainnetActive: bind.Enabled && c == "mainnet-beta",
			RoundSlot:     slot,
			EndsAt:        endsAt,
		}
	}

	snap := rpcSnapshot{
		Source:        rpcSource,
		ProgramID:     prog,
		LedgerID:      ledger,
		Mint:          mintAddr,
		BTCMint:       btcMint,
		MainnetActive: bind.Enabled && c == "mainnet-beta",
		ProgramLive:   len(raws) > 0 && len(raws[0]) > 0,
	}
	if len(raws) > 1 && len(raws[1]) == onchain.LV1XSpaceRuntime {
		if rt, dErr := onchain.DecodeLV1XRuntime(raws[1]); dErr == nil {
			snap.Runtime = &rt
			if strings.TrimSpace(rt.BTCMint) != "" {
				snap.BTCMint = rt.BTCMint
			}
			if strings.TrimSpace(rt.Mint) != "" {
				snap.Mint = rt.Mint
			}
		}
	}
	if len(raws) > 2 && len(raws[2]) == onchain.LV1XSpaceStakingState {
		if st, dErr := onchain.DecodeLV1XStakingState(raws[2]); dErr == nil {
			snap.Staking = &st
		}
	}
	if len(raws) > 3 && len(raws[3]) == onchain.LV1XSpaceTreasuryState {
		if tr, dErr := onchain.DecodeLV1XTreasuryState(raws[3]); dErr == nil {
			snap.Treasury = &tr
		}
	}
	if posIdx >= 0 && posIdx < len(raws) && len(raws[posIdx]) == onchain.LV1XSpaceStakePosition {
		if pos, dErr := onchain.DecodeLV1XStakePosition(raws[posIdx]); dErr == nil {
			snap.StakePos = &pos
		}
	}

	slot, endsAt := deriveLV1XSlot(nowSec, snap.Runtime)
	snap.RoundSlot = slot
	snap.EndsAt = endsAt

	if rkAddr, rkErr := onchain.LV1XRoundPDA(prog, ledger, slot); rkErr == nil {
		sh0Addr, _ := onchain.LV1XShardPDA(prog, rkAddr, 0)
		roundAddrs := []string{rkAddr}
		if sh0Addr != "" {
			roundAddrs = append(roundAddrs, sh0Addr)
		}
		if r2, _, _, r2Err := e.fetchAccounts(rCtx, c, walletID, roundAddrs); r2Err == nil && len(r2) > 0 {
			if len(r2[0]) == onchain.LV1XSpaceRound {
				if rk, dErr := onchain.DecodeLV1XRound(r2[0]); dErr == nil {
					snap.Round = &rk
				}
			}
			if len(r2) > 1 && len(r2[1]) == onchain.LV1XSpaceRoundShard {
				if sh, dErr := onchain.DecodeLV1XRoundShard(r2[1]); dErr == nil {
					snap.Shard = &sh
				}
			}
		}
	}

	e.syncRecentRoundsOnChain(rCtx, c, walletID, prog, ledger, slot, 24)

	if walletPub != "" {
		snap.WalletSol, snap.WalletSat, snap.WalletBtc = e.fetchWalletBalances(rCtx, c, walletID, walletPub, snap.Mint, snap.BTCMint)
	}

	e.cacheMu.Lock()
	e.rpcCache[key] = cachedRPCSnapshot{fetchedAt: nowSec, cluster: c, walletID: walletID, data: snap}
	e.cacheMu.Unlock()
	return snap
}
