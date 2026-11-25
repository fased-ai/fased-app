package onchain

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync/atomic"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

const (
	// SolanaDevnetGenesisHash identifies official Solana Devnet, preventing accidental
	// Mainnet or Testnet execution even if an operator supplies the wrong RPC endpoint.
	SolanaDevnetGenesisHash = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"

	// MemoProgram is SPL Memo v2, used for non-monetary Devnet execution proofs.
	MemoProgram = "MemoSq4gqABAXKb96qnH8TysNcWxMyWCqXgDLGmfcHr"

	// DevnetPriorityFeeCapMicroLamports caps computed P75 priority fees on Devnet.
	DevnetPriorityFeeCapMicroLamports uint64 = 1_000_000
)

// AllowedDevnetProgramID reports whether id is in the strict non-WEN Solana
// program allowlist admitted for Stage 4 Devnet execution.
func AllowedDevnetProgramID(id string) bool {
	switch id {
	case System, Compute, MemoProgram, Token, AssociatedToken, Jupiter:
		return true
	default:
		return false
	}
}

func decodeShort(b []byte) (val int, consumed int, err error) {
	if len(b) == 0 {
		return 0, 0, errors.New("truncated compact-u16")
	}
	var out int
	for i := 0; i < 3; i++ {
		if i >= len(b) {
			return 0, 0, errors.New("truncated compact-u16")
		}
		elem := int(b[i])
		if i > 0 && elem == 0 {
			return 0, 0, errors.New("noncanonical compact-u16")
		}
		if i == 2 && (elem&^0x03) != 0 {
			return 0, 0, errors.New("compact-u16 overflow")
		}
		out |= (elem & 0x7f) << (7 * i)
		if (elem & 0x80) == 0 {
			return out, i + 1, nil
		}
	}
	return 0, 0, errors.New("invalid compact-u16")
}

// ValidateDevnetMessage parses a legacy Solana transaction message wire payload and
// guarantees single fee-payer signing and strict non-WEN Devnet program allowlisting.
func ValidateDevnetMessage(messageBytes []byte, expectedFeePayer [32]byte) error {
	if len(messageBytes) < 3+1+64+32+1 || len(messageBytes)+65 > 1232 {
		return errors.New("solana devnet message length out of bounds")
	}
	if (messageBytes[0] & 0x80) != 0 {
		return errors.New("versioned message requires explicit quote review")
	}
	numReq := int(messageBytes[0])
	numROSigned := int(messageBytes[1])
	numROUnsigned := int(messageBytes[2])
	if numReq != 1 || numROSigned != 0 {
		return errors.New("devnet message must require exactly one writable fee-payer signer")
	}

	pos := 3
	accountCount, n, err := decodeShort(messageBytes[pos:])
	if err != nil {
		return err
	}
	pos += n
	if accountCount < 2 || accountCount > 48 || numROUnsigned > accountCount-numReq {
		return errors.New("devnet message account header out of bounds")
	}
	if pos+accountCount*32+32+1 > len(messageBytes) {
		return errors.New("truncated devnet message accounts or blockhash")
	}
	accounts := make([][32]byte, accountCount)
	for i := 0; i < accountCount; i++ {
		copy(accounts[i][:], messageBytes[pos:pos+32])
		pos += 32
	}
	if accounts[0] != expectedFeePayer {
		return errors.New("devnet message fee payer mismatch")
	}

	var zeroHash [32]byte
	if bytes.Equal(messageBytes[pos:pos+32], zeroHash[:]) {
		return errors.New("non-zero recent blockhash required")
	}
	pos += 32

	ixCount, n, err := decodeShort(messageBytes[pos:])
	if err != nil {
		return err
	}
	pos += n
	if ixCount < 1 || ixCount > 12 {
		return errors.New("devnet message instruction count out of bounds")
	}
	for i := 0; i < ixCount; i++ {
		if pos >= len(messageBytes) {
			return errors.New("truncated devnet instruction")
		}
		progIdx := int(messageBytes[pos])
		pos++
		if progIdx < numReq || progIdx >= accountCount {
			return errors.New("devnet instruction program index invalid")
		}
		progID := Base58(accounts[progIdx][:])
		if !AllowedDevnetProgramID(progID) {
			return errors.New("program not permitted on devnet executor")
		}
		acctIdxCount, n, err := decodeShort(messageBytes[pos:])
		if err != nil {
			return err
		}
		pos += n
		if acctIdxCount > 32 || pos+acctIdxCount > len(messageBytes) {
			return errors.New("truncated devnet instruction accounts")
		}
		for j := 0; j < acctIdxCount; j++ {
			if int(messageBytes[pos+j]) >= accountCount {
				return errors.New("devnet instruction account index out of range")
			}
		}
		pos += acctIdxCount
		dataLen, n, err := decodeShort(messageBytes[pos:])
		if err != nil {
			return err
		}
		pos += n
		if dataLen > 512 || pos+dataLen > len(messageBytes) {
			return errors.New("truncated devnet instruction data")
		}
		pos += dataLen
	}
	if pos != len(messageBytes) {
		return errors.New("unexpected trailing bytes in devnet message")
	}
	return nil
}

// BuildDevnetComputeMemoMessage constructs a canonical legacy Solana transaction message
// with SetComputeUnitLimit, SetComputeUnitPrice, and SPL Memo v2 instructions.
func BuildDevnetComputeMemoMessage(feePayer [32]byte, recentBlockhash [32]byte, cuLimit uint32, cuPriceMicroLamports uint64, memo string) ([]byte, error) {
	var zero [32]byte
	if feePayer == zero || recentBlockhash == zero {
		return nil, errors.New("fee payer and recent blockhash must be non-zero")
	}
	if cuLimit == 0 {
		cuLimit = 200_000
	}
	if cuLimit < 5_000 || cuLimit > 1_400_000 {
		return nil, errors.New("compute unit limit out of bounds")
	}
	if cuPriceMicroLamports > DevnetPriorityFeeCapMicroLamports {
		return nil, errors.New("priority fee exceeds devnet cap")
	}
	if len(memo) == 0 || len(memo) > 128 {
		return nil, errors.New("memo must be 1..128 bytes")
	}
	for i := 0; i < len(memo); i++ {
		if memo[i] < 0x20 || memo[i] > 0x7e {
			return nil, errors.New("memo must be printable ASCII")
		}
	}

	computeKey, err := Key(Compute)
	if err != nil {
		return nil, err
	}
	memoProgKey, err := Key(MemoProgram)
	if err != nil {
		return nil, err
	}

	cuData := make([]byte, 5)
	cuData[0] = 2
	binary.LittleEndian.PutUint32(cuData[1:], cuLimit)

	priceData := make([]byte, 9)
	priceData[0] = 3
	binary.LittleEndian.PutUint64(priceData[1:], cuPriceMicroLamports)

	msg := make([]byte, 0, 256)
	// Header: 1 signer (writable), 0 readonly signed, 2 readonly unsigned (Compute, MemoProgram)
	msg = append(msg, 1, 0, 2)
	msg = append(msg, short(3)...)
	msg = append(msg, feePayer[:]...)
	msg = append(msg, computeKey...)
	msg = append(msg, memoProgKey...)
	msg = append(msg, recentBlockhash[:]...)

	// 3 instructions: ComputeBudget Limit, ComputeBudget Price, SPL Memo v2
	msg = append(msg, short(3)...)
	// Ix 1: ComputeLimit
	msg = append(msg, 1, 0)
	msg = append(msg, short(len(cuData))...)
	msg = append(msg, cuData...)
	// Ix 2: ComputePrice
	msg = append(msg, 1, 0)
	msg = append(msg, short(len(priceData))...)
	msg = append(msg, priceData...)
	// Ix 3: MemoProgram (signed by account 0 = feePayer)
	msg = append(msg, 2, 1, 0)
	msg = append(msg, short(len(memo))...)
	msg = append(msg, memo...)

	if err := ValidateDevnetMessage(msg, feePayer); err != nil {
		return nil, err
	}
	return msg, nil
}

// AssembleSignedLegacyTransaction builds a raw wire Solana transaction packet from an
// Ed25519 signature and message bytes, returning the packet and Base58 tx signature.
func AssembleSignedLegacyTransaction(sig [64]byte, messageBytes []byte) ([]byte, string) {
	pkt := make([]byte, 0, 1+64+len(messageBytes))
	pkt = append(pkt, 1)
	pkt = append(pkt, sig[:]...)
	pkt = append(pkt, messageBytes...)
	return pkt, Base58(sig[:])
}

// DevnetExecutor talks strictly to a verified Solana Devnet JSON-RPC endpoint
// (verified against SolanaDevnetGenesisHash) for opt-in Stage 4 testing.
type DevnetExecutor struct {
	rpcURL        string
	sanitizedHost string
	http          *http.Client
	seq           atomic.Uint64
}

// NewDevnetExecutor constructs a Devnet JSON-RPC client. In production (rt == nil),
// only HTTPS endpoints resolving to public unicast IPs are accepted.
func NewDevnetExecutor(rpcURL string, rt http.RoundTripper) (*DevnetExecutor, error) {
	u, err := url.Parse(rpcURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(rpcURL) > 2048 {
		return nil, errors.New("valid Solana Devnet RPC URL required")
	}
	if rt == nil && u.Scheme != "https" {
		return nil, errors.New("Solana Devnet RPC requires HTTPS")
	}
	if rt != nil && u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("invalid Solana Devnet RPC scheme")
	}
	sanitizedHost := u.Scheme + "://" + u.Host
	var clientHTTP *http.Client
	if rt != nil {
		clientHTTP = &http.Client{
			Transport:     rt,
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("devnet RPC redirects denied") },
		}
	} else {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:       tls.VersionTLS12,
				CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
			},
			Proxy:                 nil,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 5 * time.Second,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          8,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       60 * time.Second,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}
				return dialPublicAddress(ctx, network, address, net.DefaultResolver.LookupNetIP, dialer.DialContext)
			},
		}
		clientHTTP = &http.Client{
			Transport:     tr,
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("devnet RPC redirects denied") },
		}
	}
	return &DevnetExecutor{
		rpcURL:        rpcURL,
		sanitizedHost: sanitizedHost,
		http:          clientHTTP,
	}, nil
}

// SanitizedHost returns scheme://host without query parameters or API credentials.
func (d *DevnetExecutor) SanitizedHost() string {
	if d == nil {
		return ""
	}
	return d.sanitizedHost
}

func (d *DevnetExecutor) rpcCall(ctx context.Context, method string, params any, out any) error {
	if d == nil || d.http == nil {
		return errors.New("devnet RPC client unavailable")
	}
	id := d.seq.Add(1)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.rpcURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.http.Do(req)
	if err != nil {
		return errors.New("devnet RPC transport unavailable")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024+1))
	if err != nil || resp.StatusCode != http.StatusOK || len(raw) > 512*1024 {
		clear(raw)
		return errors.New("devnet RPC rejected bounded request")
	}
	var fields map[string]json.RawMessage
	if update.StrictJSONBounded(raw, &fields, 512*1024) != nil {
		return errors.New("invalid devnet RPC JSON response")
	}
	var rpcResp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &rpcResp) != nil || rpcResp.JSONRPC != "2.0" || rpcResp.ID != id || (len(rpcResp.Error) > 0 && string(rpcResp.Error) != "null") || len(rpcResp.Result) == 0 {
		return errors.New("devnet RPC result error")
	}
	if json.Unmarshal(rpcResp.Result, out) != nil {
		return errors.New("invalid devnet RPC result payload")
	}
	return nil
}

// VerifyGenesis refuses any RPC endpoint that does not match SolanaDevnetGenesisHash.
func (d *DevnetExecutor) VerifyGenesis(ctx context.Context) error {
	var genesis string
	if err := d.rpcCall(ctx, "getGenesisHash", []any{}, &genesis); err != nil {
		return err
	}
	if genesis != SolanaDevnetGenesisHash {
		return errors.New("RPC endpoint is not Solana Devnet")
	}
	return nil
}

// LatestBlockhash queries getLatestBlockhash on a verified Devnet RPC endpoint.
func (d *DevnetExecutor) LatestBlockhash(ctx context.Context) (blockhash string, rawHash [32]byte, lastValidHeight uint64, slot uint64, err error) {
	if err := d.VerifyGenesis(ctx); err != nil {
		return "", rawHash, 0, 0, err
	}
	var resp struct {
		Context struct {
			Slot uint64 `json:"slot"`
		} `json:"context"`
		Value struct {
			Blockhash            string `json:"blockhash"`
			LastValidBlockHeight uint64 `json:"lastValidBlockHeight"`
		} `json:"value"`
	}
	if err := d.rpcCall(ctx, "getLatestBlockhash", []any{map[string]any{"commitment": "confirmed"}}, &resp); err != nil {
		return "", rawHash, 0, 0, err
	}
	raw, err := Key(resp.Value.Blockhash)
	if err != nil {
		return "", rawHash, 0, 0, errors.New("invalid devnet blockhash")
	}
	copy(rawHash[:], raw)
	return resp.Value.Blockhash, rawHash, resp.Value.LastValidBlockHeight, resp.Context.Slot, nil
}

// EstimatePriorityFee fetches recent prioritization fees and returns the P75 fee
// capped at DevnetPriorityFeeCapMicroLamports.
func (d *DevnetExecutor) EstimatePriorityFee(ctx context.Context) (p75Micro uint64, sampleCount int, err error) {
	var samples []struct {
		Slot              uint64 `json:"slot"`
		PrioritizationFee uint64 `json:"prioritizationFee"`
	}
	if err := d.rpcCall(ctx, "getRecentPrioritizationFees", []any{[]string{}}, &samples); err != nil {
		return 0, 0, err
	}
	if len(samples) == 0 {
		return 0, 0, nil
	}
	fees := make([]uint64, 0, len(samples))
	for _, s := range samples {
		fees = append(fees, s.PrioritizationFee)
	}
	slices.Sort(fees)
	idx := (len(fees) - 1) * 3 / 4
	p75 := fees[idx]
	if p75 > DevnetPriorityFeeCapMicroLamports {
		p75 = DevnetPriorityFeeCapMicroLamports
	}
	return p75, len(samples), nil
}

// SimulateTransaction runs simulateTransaction with sigVerify=true on Devnet.
func (d *DevnetExecutor) SimulateTransaction(ctx context.Context, signedTx []byte) (slot uint64, simErr string, err error) {
	if err := d.VerifyGenesis(ctx); err != nil {
		return 0, "", err
	}
	var resp struct {
		Context struct {
			Slot uint64 `json:"slot"`
		} `json:"context"`
		Value struct {
			Err json.RawMessage `json:"err"`
		} `json:"value"`
	}
	txB64 := base64.StdEncoding.EncodeToString(signedTx)
	if err := d.rpcCall(ctx, "simulateTransaction", []any{
		txB64,
		map[string]any{
			"encoding":               "base64",
			"sigVerify":              true,
			"replaceRecentBlockhash": false,
			"commitment":             "processed",
		},
	}, &resp); err != nil {
		return 0, "", err
	}
	if len(resp.Value.Err) > 0 && string(resp.Value.Err) != "null" {
		simErr = string(resp.Value.Err)
	}
	return resp.Context.Slot, simErr, nil
}

// SendWithRebroadcast submits an already-signed Devnet transaction with bounded retries,
// pre-retry signature confirmation short-circuit, and LastValidHeight expiry detection.
func (d *DevnetExecutor) SendWithRebroadcast(ctx context.Context, signedTx []byte, expectedSig string, lastValidHeight uint64, cfg RebroadcastConfig) (RebroadcastResult, error) {
	if cfg.MaxAttempts < 0 || cfg.MaxAttempts > MaxRebroadcastAttempts {
		return RebroadcastResult{}, errors.New("rebroadcast attempts outside supported bounds")
	}
	if err := d.VerifyGenesis(ctx); err != nil {
		return RebroadcastResult{}, err
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = DefaultRebroadcastMaxAttempts
	}
	delay := cfg.InitialDelay
	if delay <= 0 {
		delay = DefaultRebroadcastInitialDelay
	}
	maxDelay := cfg.MaxDelay
	if maxDelay <= 0 {
		maxDelay = DefaultRebroadcastMaxDelay
	}
	if maxDelay < delay {
		maxDelay = delay
	}
	sleepFn := cfg.Sleep
	if sleepFn == nil {
		sleepFn = defaultRebroadcastSleep
	}

	res := RebroadcastResult{Signature: expectedSig}
	txB64 := base64.StdEncoding.EncodeToString(signedTx)

	checkStatus := func() (confirmed, failedOnChain bool, statusErr error) {
		var status struct {
			Value []*struct {
				Err          json.RawMessage `json:"err"`
				Confirmation string          `json:"confirmationStatus"`
			} `json:"value"`
		}
		if callErr := d.rpcCall(ctx, "getSignatureStatuses", []any{
			[]string{expectedSig},
			map[string]any{"searchTransactionHistory": true},
		}, &status); callErr != nil {
			return false, false, callErr
		}
		if len(status.Value) != 1 || status.Value[0] == nil {
			return false, false, nil
		}
		v := status.Value[0]
		if len(v.Err) > 0 && string(v.Err) != "null" {
			return false, true, nil
		}
		if string(v.Err) == "null" && (v.Confirmation == "confirmed" || v.Confirmation == "finalized") {
			return true, false, nil
		}
		return false, false, nil
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if attempt > 1 {
			confirmed, failedOnChain, statusErr := checkStatus()
			if statusErr == nil {
				if confirmed {
					res.Confirmed = true
					res.LastError = ""
					return res, nil
				}
				if failedOnChain {
					res.LastError = "devnet transaction failed on-chain"
					return res, errors.New(res.LastError)
				}
			}
		}

		var height uint64
		if err := d.rpcCall(ctx, "getBlockHeight", []any{map[string]any{"commitment": "confirmed"}}, &height); err != nil {
			res.LastError = err.Error()
			return res, err
		}
		if lastValidHeight > 0 && height > lastValidHeight {
			res.Expired = true
			res.LastError = "devnet blockhash expired"
			return res, errors.New(res.LastError)
		}

		var actual string
		sendErr := d.rpcCall(ctx, "sendTransaction", []any{
			txB64,
			map[string]any{
				"encoding":            "base64",
				"skipPreflight":       false,
				"preflightCommitment": "processed",
				"maxRetries":          0,
			},
		}, &actual)
		res.Attempted = true
		res.Attempts = attempt
		if sendErr == nil {
			if actual != "" && expectedSig != "" && actual != expectedSig {
				return res, errors.New("RPC returned unexpected transaction signature")
			}
			confirmed, failedOnChain, statusErr := checkStatus()
			if statusErr == nil {
				if confirmed {
					res.Confirmed = true
					res.LastError = ""
					return res, nil
				}
				if failedOnChain {
					res.LastError = "devnet transaction failed on-chain"
					return res, errors.New(res.LastError)
				}
			}
			res.LastError = ""
		} else {
			res.LastError = sendErr.Error()
		}

		if attempt == maxAttempts {
			if res.LastError != "" {
				return res, errors.New(res.LastError)
			}
			return res, errors.New("devnet transaction confirmation pending after bounded retries")
		}
		if err := sleepFn(ctx, delay); err != nil {
			return res, err
		}
		if delay*2 <= maxDelay {
			delay *= 2
		} else {
			delay = maxDelay
		}
	}
	return res, errors.New("devnet transaction confirmation pending")
}
