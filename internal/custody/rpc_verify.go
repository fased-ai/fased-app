package custody

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
)

const (
	SolanaMainnetGenesis = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
	SolanaDevnetGenesis  = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"
	SolanaTestnetGenesis = "4uhcVJyU9pJkvQyS88uRDiswHXSCkY3zQawwpjk2NsNY"

	OfficialMainnetRPC = "https://api.mainnet-beta.solana.com"
	OfficialDevnetRPC  = "https://api.devnet.solana.com"
	OfficialTestnetRPC = "https://api.testnet.solana.com"

	maxWitnessSlotSkew = 250
)

// ValidateSolanaRPCURL enforces strict HTTPS/SSRF rules (allowing loopback HTTP for local testing only).
func ValidateSolanaRPCURL(raw string) (canonicalURL string, hostOnly string, err error) {
	return ValidateSolanaRPCURLWithOptions(raw, true)
}

// ValidateSolanaRPCURLWithOptions enforces strict HTTPS/SSRF rules and only allows loopback URLs when allowLoopback is true.
func ValidateSolanaRPCURLWithOptions(raw string, allowLoopback bool) (canonicalURL string, hostOnly string, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", errors.New("Solana RPC URL is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return "", "", errors.New("invalid Solana RPC URL")
	}
	if parsed.User != nil {
		return "", "", errors.New("RPC URL must not contain userinfo")
	}
	if parsed.Fragment != "" {
		return "", "", errors.New("RPC URL must not contain fragments")
	}
	hostname := strings.ToLower(parsed.Hostname())
	isLoopback := isLoopbackHostname(hostname)
	if isLoopback && !allowLoopback {
		return "", "", errors.New("loopback Solana RPC URLs are disabled in production (enable --allow-loopback-rpc for localnet)")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && !(scheme == "http" && isLoopback) {
		return "", "", errors.New("Solana RPC URL must use HTTPS (or loopback HTTP for localnet)")
	}
	if ip, ipErr := netip.ParseAddr(hostname); ipErr == nil {
		if !isLoopback && !IsPublicUnicastIP(ip) {
			return "", "", errors.New("RPC URL cannot target private, CGNAT, link-local, multicast, or unspecified IPs")
		}
	}
	parsed.Scheme = scheme
	hostOnly = parsed.Scheme + "://" + parsed.Host
	return parsed.String(), hostOnly, nil
}

// ResolveClusterFromGenesis maps Solana genesis hashes to cluster names.
func ResolveClusterFromGenesis(genesis string) string {
	switch strings.TrimSpace(genesis) {
	case SolanaMainnetGenesis:
		return "mainnet-beta"
	case SolanaDevnetGenesis:
		return "devnet"
	case SolanaTestnetGenesis:
		return "testnet"
	default:
		return "custom"
	}
}

// WitnessOriginForCluster returns the official Solana RPC witness for verified clusters.
func WitnessOriginForCluster(cluster string) string {
	switch cluster {
	case "mainnet-beta":
		return OfficialMainnetRPC
	case "devnet":
		return OfficialDevnetRPC
	case "testnet":
		return OfficialTestnetRPC
	default:
		return ""
	}
}

func sanitizeRPCError(err error, rpcURL string) error {
	if err == nil {
		return nil
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		host := "rpc"
		if u, pErr := url.Parse(rpcURL); pErr == nil && u.Host != "" {
			host = u.Scheme + "://" + u.Host
		}
		return fmt.Errorf("%s %s: %v", ue.Op, host, ue.Err)
	}
	return err
}

func rpcJSONCall(ctx context.Context, transport http.RoundTripper, rpcURL, method string, params any, resultOut any) (int64, error) {
	if transport == nil {
		transport = NewSafeTransport(isLoopbackURL(rpcURL))
	}
	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return 0, err
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("RPC redirects blocked")
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, strings.NewReader(string(payload)))
	if err != nil {
		return 0, sanitizeRPCError(err, rpcURL)
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, sanitizeRPCError(err, rpcURL)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	latency := time.Since(start).Milliseconds()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return latency, fmt.Errorf("RPC HTTP %d", resp.StatusCode)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return latency, errors.New("invalid JSON-RPC response")
	}
	if env.Error != nil {
		return latency, fmt.Errorf("RPC error %d: %s", env.Error.Code, env.Error.Message)
	}
	if resultOut != nil {
		if err := json.Unmarshal(env.Result, resultOut); err != nil {
			return latency, fmt.Errorf("decode RPC result: %w", err)
		}
	}
	return latency, nil
}

// VerifyAndPinRPC verifies genesis hash and slot freshness against an independent witness RPC
// whenever available, refusing any cluster change or forked/stale primary.
func VerifyAndPinRPC(ctx context.Context, transport http.RoundTripper, primaryURL, fallbackURL, expectedGenesis string, fixtureMode bool) (WalletRPCStatus, string, string, error) {
	canonPrimary, primaryHost, err := ValidateSolanaRPCURL(primaryURL)
	if err != nil {
		return WalletRPCStatus{}, "", "", err
	}
	var canonFallback, fallbackHost string
	if strings.TrimSpace(fallbackURL) != "" {
		cf, fh, fErr := ValidateSolanaRPCURL(fallbackURL)
		if fErr != nil {
			return WalletRPCStatus{}, "", "", fErr
		}
		if fh == primaryHost {
			return WalletRPCStatus{}, "", "", errors.New("fallback execution RPC must use a distinct origin")
		}
		canonFallback = cf
		fallbackHost = fh
	}

	var genesis string
	var latency int64
	if fixtureMode && transport == nil {
		if strings.Contains(strings.ToLower(canonPrimary), "devnet") {
			genesis = SolanaDevnetGenesis
		} else {
			genesis = SolanaMainnetGenesis
		}
		latency = 4
	} else {
		lat, gErr := rpcJSONCall(ctx, transport, canonPrimary, "getGenesisHash", []any{}, &genesis)
		if gErr != nil {
			return WalletRPCStatus{}, "", "", fmt.Errorf("primary RPC genesis check failed: %w", gErr)
		}
		latency = lat
	}
	if expectedGenesis != "" && genesis != expectedGenesis {
		return WalletRPCStatus{}, "", "", fmt.Errorf("RPC genesis hash %s does not match wallet pinned cluster %s", genesis, expectedGenesis)
	}

	cluster := ResolveClusterFromGenesis(genesis)
	witnessOrigin := WitnessOriginForCluster(cluster)
	witnessStatus := "custom"
	if witnessOrigin != "" && primaryHost == witnessOrigin {
		witnessStatus = "self-public-no-witness"
	}

	if canonFallback != "" && !(fixtureMode && transport == nil) {
		var fbGenesis string
		if _, fbErr := rpcJSONCall(ctx, transport, canonFallback, "getGenesisHash", []any{}, &fbGenesis); fbErr != nil {
			return WalletRPCStatus{}, "", "", fmt.Errorf("fallback RPC genesis check failed: %w", fbErr)
		}
		if fbGenesis != genesis {
			return WalletRPCStatus{}, "", "", errors.New("fallback RPC genesis hash disagrees with primary execution RPC")
		}
	}

	// Real independent witness verification: compare genesis hash and live slot skew.
	witnessTarget := ""
	witnessExplicit := false
	if canonFallback != "" {
		witnessTarget = canonFallback
		witnessExplicit = true
	} else if witnessOrigin != "" && primaryHost != witnessOrigin {
		witnessTarget = witnessOrigin
	}
	if witnessTarget != "" && !(fixtureMode && transport == nil) {
		wCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var wGenesis string
		if _, wErr := rpcJSONCall(wCtx, transport, witnessTarget, "getGenesisHash", []any{}, &wGenesis); wErr != nil {
			if witnessExplicit {
				return WalletRPCStatus{}, "", "", fmt.Errorf("witness RPC check failed: %w", wErr)
			}
			witnessStatus = "witness-unreachable"
		} else if wGenesis != genesis {
			return WalletRPCStatus{}, "", "", fmt.Errorf("witness RPC genesis %s disagrees with primary %s", wGenesis, genesis)
		} else {
			var pSlot, wSlot uint64
			_, pErr := rpcJSONCall(wCtx, transport, canonPrimary, "getSlot", []any{}, &pSlot)
			_, wSlotErr := rpcJSONCall(wCtx, transport, witnessTarget, "getSlot", []any{}, &wSlot)
			if pErr != nil || wSlotErr != nil {
				if witnessExplicit {
					return WalletRPCStatus{}, "", "", errors.New("witness slot freshness check failed")
				}
				witnessStatus = "slot-unverified"
			} else {
				diff := pSlot - wSlot
				if wSlot > pSlot {
					diff = wSlot - pSlot
				}
				if diff > maxWitnessSlotSkew {
					return WalletRPCStatus{}, "", "", fmt.Errorf("primary RPC slot (%d) diverged from witness slot (%d) by %d slots", pSlot, wSlot, diff)
				}
				witnessStatus = "verified"
			}
		}
	} else if fixtureMode && witnessOrigin != "" && primaryHost != witnessOrigin {
		witnessStatus = "verified"
	}

	return WalletRPCStatus{
		Configured:    true,
		Host:          primaryHost,
		Cluster:       cluster,
		GenesisHash:   genesis,
		WitnessHost:   witnessOrigin,
		WitnessStatus: witnessStatus,
		FallbackHost:  fallbackHost,
		LatencyMs:     latency,
		Version:       1,
	}, canonPrimary, canonFallback, nil
}

func simulateRPCMessage(ctx context.Context, transport http.RoundTripper, rpcURL string, unsignedPacket []byte) (bool, string) {
	b64 := base64.StdEncoding.EncodeToString(unsignedPacket)
	var out struct {
		Value struct {
			Err any `json:"err"`
		} `json:"value"`
	}
	_, err := rpcJSONCall(ctx, transport, rpcURL, "simulateTransaction", []any{
		b64,
		map[string]any{
			"encoding":   "base64",
			"sigVerify":  false,
			"commitment": "processed",
		},
	}, &out)
	if err != nil {
		return false, err.Error()
	}
	if out.Value.Err != nil {
		b, _ := json.Marshal(out.Value.Err)
		return false, string(b)
	}
	return true, ""
}

func sendSignedRPCTx(ctx context.Context, transport http.RoundTripper, rpcURL string, signedPacket []byte) (string, error) {
	b64 := base64.StdEncoding.EncodeToString(signedPacket)
	var sig string
	_, err := rpcJSONCall(ctx, transport, rpcURL, "sendTransaction", []any{
		b64,
		map[string]any{
			"encoding":            "base64",
			"preflightCommitment": "confirmed",
			"maxRetries":          2,
		},
	}, &sig)
	return sig, err
}

func checkSignatureStatusRPC(ctx context.Context, transport http.RoundTripper, rpcURL, sig string) (string, string, error) {
	var out struct {
		Value []struct {
			ConfirmationStatus string `json:"confirmationStatus"`
			Err                any    `json:"err"`
		} `json:"value"`
	}
	_, err := rpcJSONCall(ctx, transport, rpcURL, "getSignatureStatuses", []any{[]string{sig}}, &out)
	if err != nil {
		return LedgerUnknown, err.Error(), err
	}
	if len(out.Value) == 0 || (out.Value[0].ConfirmationStatus == "" && out.Value[0].Err == nil) {
		return LedgerUnknown, "transaction not yet observed on chain", nil
	}
	if out.Value[0].Err != nil {
		b, _ := json.Marshal(out.Value[0].Err)
		return LedgerFailed, string(b), nil
	}
	if out.Value[0].ConfirmationStatus == "confirmed" || out.Value[0].ConfirmationStatus == "finalized" {
		return LedgerConfirmed, "", nil
	}
	return LedgerBroadcast, "", nil
}

const (
	solanaALTProgramID         = "AddressLookupTab1e1111111111111111111111111"
	solanaALTHeaderBytes       = 56
	minPriorityFeeMicroLamport = 1_000
	maxPriorityFeeMicroLamport = 100_000
)

// verifyAddressLookupTablesRPC fetches each referenced Address Lookup Table account from the
// wallet's pinned execution RPC and verifies every caller-supplied address matches the actual
// on-chain table contents byte-for-byte, preventing v0 ALT account substitution.
func verifyAddressLookupTablesRPC(ctx context.Context, transport http.RoundTripper, rpcURL string, tables []onchain.AddressLookupTable) error {
	for _, tbl := range tables {
		if _, _, err := ValidateSolanaPublicAddress(tbl.AccountKey); err != nil {
			return fmt.Errorf("invalid ALT account key %q", tbl.AccountKey)
		}
		var out struct {
			Value *struct {
				Owner      string   `json:"owner"`
				Executable bool     `json:"executable"`
				Data       []string `json:"data"`
			} `json:"value"`
		}
		if _, err := rpcJSONCall(ctx, transport, rpcURL, "getAccountInfo", []any{
			tbl.AccountKey,
			map[string]any{"encoding": "base64", "commitment": "confirmed"},
		}, &out); err != nil {
			return fmt.Errorf("verify ALT %s failed: %w", tbl.AccountKey, err)
		}
		if out.Value == nil || out.Value.Owner != solanaALTProgramID || out.Value.Executable || len(out.Value.Data) < 1 || (len(out.Value.Data) >= 2 && out.Value.Data[1] != "base64") {
			return fmt.Errorf("ALT %s is missing, executable, or not owned by AddressLookupTable program", tbl.AccountKey)
		}
		raw, err := base64.StdEncoding.DecodeString(out.Value.Data[0])
		if err != nil || len(raw) < solanaALTHeaderBytes || (len(raw)-solanaALTHeaderBytes)%32 != 0 {
			return fmt.Errorf("ALT %s has invalid on-chain data layout", tbl.AccountKey)
		}
		// Verify Solana AddressLookupTableState header: type == 1 (LookupTable) and active (deactivation_slot == u64::MAX).
		if binary.LittleEndian.Uint32(raw[0:4]) != 1 || binary.LittleEndian.Uint64(raw[4:12]) != math.MaxUint64 {
			return fmt.Errorf("ALT %s is uninitialized or deactivating", tbl.AccountKey)
		}
		onchainCount := (len(raw) - solanaALTHeaderBytes) / 32
		if len(tbl.Addresses) > onchainCount {
			return fmt.Errorf("ALT %s caller address count (%d) exceeds on-chain table (%d)", tbl.AccountKey, len(tbl.Addresses), onchainCount)
		}
		for pos, expectedAddr := range tbl.Addresses {
			offset := solanaALTHeaderBytes + pos*32
			actualAddr := Base58Encode(raw[offset : offset+32])
			if actualAddr != expectedAddr {
				return fmt.Errorf("ALT %s index %d mismatch: caller=%s onchain=%s", tbl.AccountKey, pos, expectedAddr, actualAddr)
			}
		}
	}
	return nil
}

// estimatePriorityFeeRPC queries recent prioritization fees and returns a bounded median
// clamped to [1_000, 100_000] micro-lamports/CU so a malicious RPC cannot force fee drains.
func estimatePriorityFeeRPC(ctx context.Context, transport http.RoundTripper, rpcURL string) uint64 {
	var fees []struct {
		PrioritizationFee uint64 `json:"prioritizationFee"`
	}
	if _, err := rpcJSONCall(ctx, transport, rpcURL, "getRecentPrioritizationFees", []any{[]string{}}, &fees); err != nil || len(fees) == 0 {
		return minPriorityFeeMicroLamport
	}
	vals := make([]uint64, 0, len(fees))
	for _, f := range fees {
		if f.PrioritizationFee > 0 {
			vals = append(vals, f.PrioritizationFee)
		}
	}
	if len(vals) == 0 {
		return minPriorityFeeMicroLamport
	}
	slices.Sort(vals)
	med := vals[len(vals)/2]
	if med < minPriorityFeeMicroLamport {
		return minPriorityFeeMicroLamport
	}
	if med > maxPriorityFeeMicroLamport {
		return maxPriorityFeeMicroLamport
	}
	return med
}

// fetchSPLBalancesRPC queries non-zero SPL token balances for ownerPubkey via the wallet's pinned RPC.
func fetchSPLBalancesRPC(ctx context.Context, transport http.RoundTripper, rpcURL, ownerPubkey string) ([]TokenBalance, error) {
	var out struct {
		Value []struct {
			Account struct {
				Data struct {
					Parsed struct {
						Info struct {
							Mint        string `json:"mint"`
							TokenAmount struct {
								Amount         string `json:"amount"`
								Decimals       uint8  `json:"decimals"`
								UIAmountString string `json:"uiAmountString"`
							} `json:"tokenAmount"`
						} `json:"info"`
					} `json:"parsed"`
				} `json:"data"`
			} `json:"account"`
		} `json:"value"`
	}
	if _, err := rpcJSONCall(ctx, transport, rpcURL, "getTokenAccountsByOwner", []any{
		ownerPubkey,
		map[string]string{"programId": SolanaTokenProgram},
		map[string]string{"encoding": "jsonParsed"},
	}, &out); err != nil {
		return nil, err
	}
	byMint := make(map[string]*TokenBalance)
	for _, item := range out.Value {
		info := item.Account.Data.Parsed.Info
		if info.Mint == "" {
			continue
		}
		amt, pErr := strconv.ParseUint(strings.TrimSpace(info.TokenAmount.Amount), 10, 64)
		if pErr != nil || amt == 0 {
			continue
		}
		if cur, ok := byMint[info.Mint]; ok {
			cur.AmountRaw += amt
			cur.AmountHuman = FormatRawAmount(cur.AmountRaw, cur.Decimals)
		} else {
			human := strings.TrimSpace(info.TokenAmount.UIAmountString)
			if human == "" {
				human = FormatRawAmount(amt, info.TokenAmount.Decimals)
			}
			byMint[info.Mint] = &TokenBalance{
				Mint:        info.Mint,
				AmountRaw:   amt,
				Decimals:    info.TokenAmount.Decimals,
				AmountHuman: human,
			}
		}
	}
	tokens := make([]TokenBalance, 0, len(byMint))
	for _, tb := range byMint {
		tokens = append(tokens, *tb)
	}
	slices.SortFunc(tokens, func(a, b TokenBalance) int { return strings.Compare(a.Mint, b.Mint) })
	return tokens, nil
}
