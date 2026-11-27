package wenlv1x

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
)

type cachedRPCSnapshot struct {
	fetchedAt int64
	cluster   string
	walletPub string
	data      rpcSnapshot
}

type rpcSnapshot struct {
	Source         string
	Runtime        *onchain.LV1XRuntimeAccount
	Round          *onchain.LV1XRoundAccount
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

func (e *Engine) getMultipleAccounts(ctx context.Context, addrs []string) ([][]byte, error) {
	raw, err := e.callSolanaRPC(ctx, "getMultipleAccounts", []any{
		addrs,
		map[string]any{"encoding": "base64", "commitment": "confirmed"},
	})
	if err != nil {
		return nil, err
	}
	var res struct {
		Value []*struct {
			Data [2]string `json:"data"`
		} `json:"value"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	out := make([][]byte, len(addrs))
	for i := 0; i < len(addrs) && i < len(res.Value); i++ {
		if res.Value[i] == nil || res.Value[i].Data[0] == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(res.Value[i].Data[0])
		if err == nil {
			out[i] = b
		}
	}
	return out, nil
}

func (e *Engine) fetchWalletBalancesRPC(ctx context.Context, owner, satMint, btcMint string) (sol float64, sat float64, btc float64) {
	if owner == "" {
		return 0, 0, 0
	}
	if rawBal, err := e.callSolanaRPC(ctx, "getBalance", []any{owner, map[string]any{"commitment": "confirmed"}}); err == nil {
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
		ata, err := onchain.LV1XAssociatedTokenAddress(owner, mint)
		if err != nil {
			return 0
		}
		rawTok, err := e.callSolanaRPC(ctx, "getTokenAccountBalance", []any{ata, map[string]any{"commitment": "confirmed"}})
		if err != nil {
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

func (e *Engine) readSnapshot(ctx context.Context, cluster, walletPub string) rpcSnapshot {
	c := normalizeCluster(cluster)
	if c == "mainnet-beta" && onchain.LV1XMainnetProgramID == "" {
		return rpcSnapshot{Source: "gated"}
	}
	if e.fixture {
		return rpcSnapshot{Source: "simulation", ProgramLive: true}
	}
	nowSec := e.now().UTC().Unix()
	key := c + ":" + walletPub
	e.cacheMu.RLock()
	cached, hasCache := e.rpcCache[key]
	e.cacheMu.RUnlock()
	if hasCache && nowSec-cached.fetchedAt <= 3 {
		return cached.data
	}

	prog := onchain.LV1XDevnetProgramID
	ledger := onchain.LV1XDevnetLedgerID
	slot := uint64(nowSec / onchain.LV1XRoundIntervalSec)

	rtAddr, _ := onchain.LV1XRuntimePDA(prog, ledger)
	rkAddr, _ := onchain.LV1XRoundPDA(prog, ledger, slot)
	stAddr, _ := onchain.LV1XStakingStatePDA(prog, ledger)
	trAddr, _ := onchain.LV1XTreasuryStatePDA(prog, ledger)
	mintAddr, _ := onchain.LV1XMintPDA(prog, ledger)

	addrs := []string{prog, rtAddr, rkAddr, stAddr, trAddr}
	var posIdx int = -1
	if walletPub != "" {
		if posAddr, err := onchain.LV1XStakePositionPDA(prog, ledger, walletPub); err == nil {
			posIdx = len(addrs)
			addrs = append(addrs, posAddr)
		}
	}

	rCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	raws, err := e.getMultipleAccounts(rCtx, addrs)
	if err != nil {
		if hasCache {
			out := cached.data
			out.Source = "cached"
			return out
		}
		return rpcSnapshot{Source: "cached"}
	}

	snap := rpcSnapshot{
		Source:      "devnet-rpc",
		ProgramLive: len(raws[0]) > 0,
	}
	if len(raws[1]) == onchain.LV1XSpaceRuntime {
		if rt, dErr := onchain.DecodeLV1XRuntime(raws[1]); dErr == nil {
			snap.Runtime = &rt
		}
	}
	if len(raws[2]) == onchain.LV1XSpaceRound {
		if rk, dErr := onchain.DecodeLV1XRound(raws[2]); dErr == nil {
			snap.Round = &rk
		}
	}
	if len(raws[3]) == onchain.LV1XSpaceStakingState {
		if st, dErr := onchain.DecodeLV1XStakingState(raws[3]); dErr == nil {
			snap.Staking = &st
		}
	}
	if len(raws[4]) == onchain.LV1XSpaceTreasuryState {
		if tr, dErr := onchain.DecodeLV1XTreasuryState(raws[4]); dErr == nil {
			snap.Treasury = &tr
		}
	}
	if posIdx >= 0 && posIdx < len(raws) && len(raws[posIdx]) == onchain.LV1XSpaceStakePosition {
		if pos, dErr := onchain.DecodeLV1XStakePosition(raws[posIdx]); dErr == nil {
			snap.StakePos = &pos
		}
	}

	if walletPub != "" {
		btcMint := onchain.LV1XDevnetBTCMint
		if snap.Runtime != nil && snap.Runtime.BTCMint != "" {
			btcMint = snap.Runtime.BTCMint
		}
		snap.WalletSol, snap.WalletSat, snap.WalletBtc = e.fetchWalletBalancesRPC(rCtx, walletPub, mintAddr, btcMint)
	}

	e.cacheMu.Lock()
	e.rpcCache[key] = cachedRPCSnapshot{fetchedAt: nowSec, cluster: c, walletPub: walletPub, data: snap}
	e.cacheMu.Unlock()
	return snap
}
