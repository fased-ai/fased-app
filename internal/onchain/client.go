package onchain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/fased-ai/fased-app/internal/netguard"
	"github.com/fased-ai/fased-app/internal/update"
)

const buildURL = "https://api.jup.ag/swap/v2/build"

type Client struct {
	rpc      string
	key      string
	http     *http.Client
	sequence atomic.Uint64
	network  string
	valid    func() error
}

// ControlledTransport is a Go-only test dependency. It has fixed inert RPC
// and disposable API credentials; there is no instance/CLI transport override.
type ControlledTransport interface {
	http.RoundTripper
	ControlledFixture() bool
}

func NewControlledClient(rt ControlledTransport) (*Client, error) {
	if rt == nil || !rt.ControlledFixture() {
		return nil, errors.New("controlled transport required")
	}
	c, e := NewClient("https://rpc.fased-fixture.invalid", "fased-test-onchain")
	if e != nil {
		return nil, e
	}
	c.http = &http.Client{Transport: rt, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect denied") }}
	return c, nil
}

func isPublicUnicastIP(ip netip.Addr) bool {
	return netguard.IsPublicUnicastIP(ip)
}

func interleavePublicIPs(ips []netip.Addr) ([]netip.Addr, error) {
	if len(ips) == 0 {
		return nil, errors.New("RPC address unavailable")
	}
	var v4, v6 []netip.Addr
	for _, ip := range ips {
		if !isPublicUnicastIP(ip) {
			return nil, errors.New("private network address denied")
		}
		if ip.Is4() {
			v4 = append(v4, ip)
		} else {
			v6 = append(v6, ip)
		}
	}
	out := make([]netip.Addr, 0, min(len(ips), 6))
	for i := 0; len(out) < 6 && (i < len(v4) || i < len(v6)); i++ {
		if i < len(v4) {
			out = append(out, v4[i])
		}
		if len(out) < 6 && i < len(v6) {
			out = append(out, v6[i])
		}
	}
	return out, nil
}

func dialPublicAddress(
	ctx context.Context,
	network, address string,
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	host, port, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	rawIPs, e := lookup(ctx, "ip", host)
	if e != nil {
		return nil, errors.New("RPC address unavailable")
	}
	targets, e := interleavePublicIPs(rawIPs)
	if e != nil {
		return nil, e
	}
	var lastErr error
	for _, ip := range targets {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, dErr := dial(attemptCtx, network, net.JoinHostPort(ip.String(), port))
		cancel()
		if dErr == nil {
			return conn, nil
		}
		lastErr = dErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("RPC address unavailable")
}

// NewClient has no transport/CA override. Core admission remains separate; a
// valid endpoint or API key never enables execution. Tests inject transport
// inside this package, never through instance settings, HTTP or CLI input.
func NewClient(rpc, key string) (*Client, error) {
	u, e := url.Parse(rpc)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(rpc) > 2048 || len(key) > 512 {
		return nil, errors.New("HTTPS RPC and bounded Jupiter credential required")
	}
	t := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:       tls.VersionTLS13,
			CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
		},
		Proxy:                 nil,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       60 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}
			return dialPublicAddress(ctx, network, address, net.DefaultResolver.LookupNetIP, dialer.DialContext)
		},
	}
	return &Client{rpc: rpc, key: key, http: &http.Client{Transport: t, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("swap redirects denied") }}}, nil
}
func (c *Client) request(ctx context.Context, method, target string, body []byte, key bool) ([]byte, error) {
	if c.valid != nil && c.valid() != nil {
		return nil, errors.New("swap credential access unavailable")
	}
	r, e := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	r.Header.Set("Content-Type", "application/json")
	if key && c.key != "" {
		r.Header.Set("x-api-key", c.key)
	}
	response, e := c.http.Do(r)
	if e != nil {
		return nil, errors.New("swap transport outcome unavailable")
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 512*1024+1))
	if e != nil || response.StatusCode != 200 || len(raw) > 512*1024 {
		clear(raw)
		return nil, errors.New("swap service rejected bounded request")
	}
	var fields map[string]json.RawMessage
	if update.StrictJSONBounded(raw, &fields, 512*1024) != nil {
		return nil, errors.New("invalid or ambiguous swap response")
	}
	if c.valid != nil && c.valid() != nil {
		clear(raw)
		return nil, errors.New("swap credential access unavailable")
	}
	return raw, nil
}
func (c *Client) rpcCall(ctx context.Context, method string, params any, out any) error {
	id := c.sequence.Add(1)
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	raw, e := c.request(ctx, "POST", c.rpc, body, false)
	if e != nil {
		return e
	}
	var r struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &r) != nil || r.JSONRPC != "2.0" || r.ID != id || (len(r.Error) > 0 && string(r.Error) != "null") || len(r.Result) == 0 {
		return errors.New("RPC result unavailable")
	}
	if json.Unmarshal(r.Result, out) != nil {
		return errors.New("invalid RPC result")
	}
	return nil
}

type rpcAccount struct {
	Owner      string            `json:"owner"`
	Data       []json.RawMessage `json:"data"`
	Executable bool              `json:"executable"`
}

func (c *Client) token(ctx context.Context, address, owner, mint string, minimum uint64) (bool, error) {
	var r struct {
		Value *rpcAccount `json:"value"`
	}
	if e := c.rpcCall(ctx, "getAccountInfo", []any{address, map[string]any{"encoding": "base64", "commitment": "confirmed"}}, &r); e != nil {
		return false, e
	}
	if r.Value == nil {
		if minimum != 0 {
			return false, errors.New("input token account is missing")
		}
		return false, nil
	}
	a := r.Value
	if a.Owner != Token || a.Executable || len(a.Data) != 2 {
		return false, errors.New("unsupported token account")
	}
	var data, encoding string
	if json.Unmarshal(a.Data[0], &data) != nil || json.Unmarshal(a.Data[1], &encoding) != nil || encoding != "base64" {
		return false, errors.New("invalid token account data")
	}
	b, e := base64.StdEncoding.Strict().DecodeString(data)
	m, _ := Key(mint)
	o, _ := Key(owner)
	if e != nil || len(b) != 165 || !bytes.Equal(b[:32], m) || !bytes.Equal(b[32:64], o) || b[108] != 1 || binary.LittleEndian.Uint64(b[64:72]) < minimum {
		return false, errors.New("token owner, mint, state or balance mismatch")
	}
	return true, nil
}

// ResolveAddressLookupTable fetches and validates an active on-chain Solana
// AddressLookupTab1e account at confirmed commitment.
func (c *Client) ResolveAddressLookupTable(ctx context.Context, address string) (AddressLookupTable, error) {
	if _, e := Key(address); e != nil {
		return AddressLookupTable{}, e
	}
	var r struct {
		Value *rpcAccount `json:"value"`
	}
	if e := c.rpcCall(ctx, "getAccountInfo", []any{address, map[string]any{"encoding": "base64", "commitment": "confirmed"}}, &r); e != nil {
		return AddressLookupTable{}, e
	}
	if r.Value == nil {
		return AddressLookupTable{}, errors.New("address lookup table missing")
	}
	a := r.Value
	if a.Owner != AddressLookupTableProgram || a.Executable || len(a.Data) != 2 {
		return AddressLookupTable{}, errors.New("unsupported address lookup table account")
	}
	var data, encoding string
	if json.Unmarshal(a.Data[0], &data) != nil || json.Unmarshal(a.Data[1], &encoding) != nil || encoding != "base64" {
		return AddressLookupTable{}, errors.New("invalid address lookup table encoding")
	}
	raw, e := base64.StdEncoding.Strict().DecodeString(data)
	if e != nil || len(raw) < 56 || (len(raw)-56)%32 != 0 {
		return AddressLookupTable{}, errors.New("invalid address lookup table data")
	}
	if binary.LittleEndian.Uint32(raw[0:4]) != 1 || binary.LittleEndian.Uint64(raw[4:12]) != math.MaxUint64 {
		return AddressLookupTable{}, errors.New("address lookup table inactive or uninitialized")
	}
	count := (len(raw) - 56) / 32
	if count > 256 {
		return AddressLookupTable{}, errors.New("address lookup table exceeds 256 entries")
	}
	addrs := make([]string, count)
	for i := 0; i < count; i++ {
		start := 56 + i*32
		addrs[i] = Base58(raw[start : start+32])
	}
	return AddressLookupTable{AccountKey: address, Addresses: addrs}, nil
}

func (c *Client) Prepare(ctx context.Context, q Quote) (Quote, error) {
	if e := c.admitNetwork(q); e != nil {
		return q, e
	}
	req := Request{
		AccountID:        q.AccountID,
		InputMint:        q.InputMint,
		OutputMint:       q.OutputMint,
		Amount:           q.Amount,
		SlippageBPS:      q.SlippageBPS,
		MaxFeeLamports:   q.MaxFeeLamports,
		WrapAndUnwrapSol: q.WrapAndUnwrapSol,
	}
	if !ValidRequest(req) {
		return q, errors.New("invalid swap request")
	}
	genesis := map[string]string{"mainnet-beta": "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d", "devnet": "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"}[q.Network]
	var actualGenesis string
	if genesis == "" || c.rpcCall(ctx, "getGenesisHash", []any{}, &actualGenesis) != nil || actualGenesis != genesis {
		return q, errors.New("RPC network differs from wallet")
	}
	values := url.Values{"inputMint": {q.InputMint}, "outputMint": {q.OutputMint}, "amount": {q.Amount}, "taker": {q.Owner}, "slippageBps": {strconv.Itoa(int(q.SlippageBPS))}, "wrapAndUnwrapSol": {strconv.FormatBool(q.WrapAndUnwrapSol)}, "maxAccounts": {"24"}, "transactionVersion": {"0"}}
	raw, e := c.request(ctx, "GET", buildURL+"?"+values.Encode(), nil, true)
	if e != nil {
		return q, e
	}
	var build struct {
		InputMint           string        `json:"inputMint"`
		OutputMint          string        `json:"outputMint"`
		InAmount            string        `json:"inAmount"`
		OutAmount           string        `json:"outAmount"`
		SlippageBPS         uint16        `json:"slippageBps"`
		TransactionVersion  *int          `json:"transactionVersion"`
		Setup               []Instruction `json:"setupInstructions"`
		Swap                Instruction   `json:"swapInstruction"`
		Cleanup             *Instruction  `json:"cleanupInstruction"`
		Other               []Instruction `json:"otherInstructions"`
		Tip                 *Instruction  `json:"tipInstruction"`
		AddressLookupTables []string      `json:"addressLookupTableAddresses"`
	}
	maxSetup := 2
	if q.WrapAndUnwrapSol {
		maxSetup = 4
	}
	if json.Unmarshal(raw, &build) != nil || build.TransactionVersion == nil || *build.TransactionVersion != 0 || build.InputMint != q.InputMint || build.OutputMint != q.OutputMint || build.InAmount != q.Amount || build.SlippageBPS != q.SlippageBPS || len(build.Setup) > maxSetup || (!q.WrapAndUnwrapSol && build.Cleanup != nil) || build.Tip != nil || len(build.Other) > 0 || len(build.AddressLookupTables) > 4 {
		return q, errors.New("unsupported Jupiter build or changed quote parameters")
	}
	q.Output = build.OutAmount
	q.MinimumOutput, e = Minimum(q.Output, q.SlippageBPS)
	if e != nil {
		return q, e
	}
	q.Instructions = append(append([]Instruction{}, build.Setup...), build.Swap)
	if build.Cleanup != nil {
		q.Instructions = append(q.Instructions, *build.Cleanup)
	}
	if len(build.AddressLookupTables) > 0 {
		tables := make([]AddressLookupTable, 0, len(build.AddressLookupTables))
		seenTables := make(map[string]bool, len(build.AddressLookupTables))
		for _, addr := range build.AddressLookupTables {
			if seenTables[addr] {
				return q, errors.New("duplicate address lookup table in swap build")
			}
			seenTables[addr] = true
			tbl, rErr := c.ResolveAddressLookupTable(ctx, addr)
			if rErr != nil {
				return q, rErr
			}
			tables = append(tables, tbl)
		}
		q.AddressLookupTables = tables
	}
	var latest struct {
		Context struct {
			Slot uint64 `json:"slot"`
		} `json:"context"`
		Value struct {
			Blockhash            string `json:"blockhash"`
			LastValidBlockHeight uint64 `json:"lastValidBlockHeight"`
		} `json:"value"`
	}
	if e = c.rpcCall(ctx, "getLatestBlockhash", []any{map[string]any{"commitment": "confirmed"}}, &latest); e != nil {
		return q, e
	}
	q.Blockhash = latest.Value.Blockhash
	q.LastValidHeight = latest.Value.LastValidBlockHeight
	q.ObservedSlot = latest.Context.Slot
	source, e := TokenAccount(q.Owner, q.InputMint)
	if e != nil {
		return q, e
	}
	dest, _ := TokenAccount(q.Owner, q.OutputMint)
	amount, _ := Uint(q.Amount)
	hasATASetup := func(ata string) bool {
		for _, ix := range build.Setup {
			if ix.ProgramID == AssociatedToken && len(ix.Accounts) > 1 && ix.Accounts[1].Pubkey == ata {
				return true
			}
		}
		return false
	}
	var needsRentCount uint64
	if q.WrapAndUnwrapSol && q.InputMint == SOL {
		srcExists, sErr := c.token(ctx, source, q.Owner, SOL, 0)
		if sErr != nil {
			return q, sErr
		}
		if !srcExists {
			if !hasATASetup(source) {
				return q, errors.New("source wSOL setup missing")
			}
			needsRentCount++
		}
	} else {
		if _, e = c.token(ctx, source, q.Owner, q.InputMint, amount); e != nil {
			return q, e
		}
	}
	exists, e := c.token(ctx, dest, q.Owner, q.OutputMint, 0)
	if e != nil {
		return q, e
	}
	if !exists {
		if !hasATASetup(dest) {
			return q, errors.New("destination token setup missing")
		}
		needsRentCount++
	}
	if needsRentCount > 0 {
		var perAccountRent uint64
		if e = c.rpcCall(ctx, "getMinimumBalanceForRentExemption", []any{165, map[string]any{"commitment": "confirmed"}}, &perAccountRent); e != nil {
			return q, e
		}
		q.RentLamports = perAccountRent * needsRentCount
		if q.RentLamports > 6_000_000 {
			return q, errors.New("token rent exceeds supported bound")
		}
	}
	// Pre-simulate at default CU budget to tighten ComputeUnitLimit and choose
	// a bounded median priority fee without exceeding MaxFeeLamports.
	q.ComputeUnits = 0
	q.PriorityFeeMicroLamports = 0
	preSim, e := c.Simulate(ctx, q)
	if e != nil {
		return q, e
	}
	units := (preSim.Units*12 + 9) / 10
	if units < 50_000 {
		units = 50_000
	}
	if units > 1_400_000 {
		units = 1_400_000
	}
	q.ComputeUnits = uint32(units)
	if q.MaxFeeLamports > 5000 {
		var rows []struct {
			Slot     uint64 `json:"slot"`
			Priority uint64 `json:"prioritizationFee"`
		}
		if c.rpcCall(ctx, "getRecentPrioritizationFees", []any{[]string{q.Owner, Jupiter}}, &rows) == nil && len(rows) > 0 {
			fees := make([]uint64, len(rows))
			for i, row := range rows {
				fees[i] = row.Priority
			}
			slices.Sort(fees)
			median := fees[len(fees)/2]
			maxMicro := (q.MaxFeeLamports - 5000) * 1_000_000 / uint64(q.ComputeUnits)
			if median > maxMicro {
				median = maxMicro
			}
			q.PriorityFeeMicroLamports = median
		}
	}
	message, e := Message(q)
	if e != nil {
		return q, e
	}
	h := sha256.Sum256(message)
	q.TransactionSHA256 = hex.EncodeToString(h[:])
	var fee struct {
		Value *uint64 `json:"value"`
	}
	if e = c.rpcCall(ctx, "getFeeForMessage", []any{base64.StdEncoding.EncodeToString(message), map[string]any{"commitment": "confirmed"}}, &fee); e != nil || fee.Value == nil {
		return q, errors.New("transaction fee unavailable")
	}
	q.FeeLamports = *fee.Value
	if q.FeeLamports < 5000 || q.FeeLamports > q.MaxFeeLamports {
		return q, errors.New("transaction fee exceeds cap")
	}
	var balance struct {
		Value uint64 `json:"value"`
	}
	if e = c.rpcCall(ctx, "getBalance", []any{q.Owner, map[string]any{"commitment": "confirmed"}}, &balance); e != nil {
		return q, e
	}
	requiredSOL := q.FeeLamports + q.RentLamports
	if q.WrapAndUnwrapSol && q.InputMint == SOL {
		if amount > ^uint64(0)-requiredSOL {
			return q, errors.New("required SOL exceeds supported range")
		}
		requiredSOL += amount
	}
	if balance.Value < requiredSOL {
		return q, errors.New("insufficient SOL for fee and rent")
	}
	q.Digest = QuoteDigest(q)
	if e = ValidateQuote(q, q.CreatedAt); e != nil {
		return q, e
	}
	if _, e = c.Simulate(ctx, q); e != nil {
		return q, e
	}
	return q, nil
}
func (c *Client) Simulate(ctx context.Context, q Quote) (Simulation, error) {
	if e := c.admitNetwork(q); e != nil {
		return Simulation{}, e
	}
	packet, e := Unsigned(q)
	if e != nil {
		return Simulation{}, e
	}
	var r struct {
		Context struct {
			Slot uint64 `json:"slot"`
		} `json:"context"`
		Value struct {
			Err   json.RawMessage `json:"err"`
			Units uint64          `json:"unitsConsumed"`
		} `json:"value"`
	}
	e = c.rpcCall(ctx, "simulateTransaction", []any{base64.StdEncoding.EncodeToString(packet), map[string]any{"encoding": "base64", "sigVerify": false, "replaceRecentBlockhash": false, "commitment": "confirmed", "minContextSlot": q.ObservedSlot}}, &r)
	if e != nil || len(r.Value.Err) == 0 || string(r.Value.Err) != "null" || r.Value.Units == 0 || r.Value.Units > 1_400_000 || r.Context.Slot < q.ObservedSlot {
		return Simulation{}, errors.New("swap simulation failed or is stale")
	}
	return Simulation{r.Context.Slot, r.Value.Units, q.FeeLamports}, nil
}
func VerifyPacket(q Quote, packet []byte) (string, error) {
	if len(packet) < 66 || packet[0] != 1 {
		return "", errors.New("one signed swap transaction required")
	}
	expected, e := Signed(q, packet[1:65])
	if e != nil || !bytes.Equal(expected, packet) {
		return "", errors.New("signed swap transaction changed")
	}
	return Base58(packet[1:65]), nil
}
func (c *Client) Send(ctx context.Context, q Quote, packet []byte) (bool, error) {
	if e := c.admitNetwork(q); e != nil {
		return false, e
	}
	signature, e := VerifyPacket(q, packet)
	if e != nil {
		return false, e
	}
	var height uint64
	if e = c.rpcCall(ctx, "getBlockHeight", []any{map[string]any{"commitment": "confirmed"}}, &height); e != nil {
		return false, e
	}
	if height > q.LastValidHeight {
		return false, errors.New("swap blockhash expired")
	}
	var actual string
	e = c.rpcCall(ctx, "sendTransaction", []any{base64.StdEncoding.EncodeToString(packet), map[string]any{"encoding": "base64", "skipPreflight": false, "preflightCommitment": "confirmed", "maxRetries": 0, "minContextSlot": q.ObservedSlot}}, &actual)
	if e != nil || actual != signature {
		return true, errors.New("submission uncertain; reconcile the original signature")
	}
	return true, nil
}
func (c *Client) Reconcile(ctx context.Context, q Quote, packet []byte) (Observation, error) {
	if e := c.admitNetwork(q); e != nil {
		return Observation{}, e
	}
	signature, e := VerifyPacket(q, packet)
	if e != nil {
		return Observation{}, e
	}
	var status struct {
		Context struct {
			Slot uint64 `json:"slot"`
		} `json:"context"`
		Value []*struct {
			Err          json.RawMessage `json:"err"`
			Confirmation string          `json:"confirmationStatus"`
			Slot         uint64          `json:"slot"`
		} `json:"value"`
	}
	if e = c.rpcCall(ctx, "getSignatureStatuses", []any{[]string{signature}, map[string]any{"searchTransactionHistory": true}}, &status); e != nil {
		return Observation{}, e
	}
	if len(status.Value) != 1 {
		return Observation{}, errors.New("invalid transaction status")
	}
	if status.Value[0] == nil {
		var height uint64
		if e = c.rpcCall(ctx, "getBlockHeight", []any{map[string]any{"commitment": "finalized"}}, &height); e == nil && height > q.LastValidHeight {
			var recheck struct {
				Context struct {
					Slot uint64 `json:"slot"`
				} `json:"context"`
				Value []*struct {
					Err          json.RawMessage `json:"err"`
					Confirmation string          `json:"confirmationStatus"`
					Slot         uint64          `json:"slot"`
				} `json:"value"`
			}
			if e = c.rpcCall(ctx, "getSignatureStatuses", []any{[]string{signature}, map[string]any{"searchTransactionHistory": true}}, &recheck); e != nil {
				return Observation{}, e
			}
			if len(recheck.Value) != 1 {
				return Observation{}, errors.New("invalid transaction status")
			}
			if recheck.Value[0] == nil {
				if recheck.Context.Slot < q.ObservedSlot {
					return Observation{State: "unknown"}, nil
				}
				// Finalized block height is strictly past LastValidHeight and the
				// signature is absent from finalized history, so the tx can never land.
				// Anchor Slot to q.ObservedSlot so core and signer produce identical
				// deterministic Observations across independent RPC reads.
				return Observation{State: "failed", Slot: q.ObservedSlot}, nil
			}
			status = recheck
		}
	}
	if status.Value[0] == nil || (status.Value[0].Confirmation != "confirmed" && status.Value[0].Confirmation != "finalized") {
		return Observation{State: "unknown"}, nil
	}
	type balance struct {
		AccountIndex int    `json:"accountIndex"`
		Mint         string `json:"mint"`
		Owner        string `json:"owner"`
		Amount       struct {
			Amount string `json:"amount"`
		} `json:"uiTokenAmount"`
	}
	var tx *struct {
		Slot        uint64            `json:"slot"`
		Transaction []json.RawMessage `json:"transaction"`
		Meta        struct {
			Err          json.RawMessage `json:"err"`
			Fee          uint64          `json:"fee"`
			PreBalances  []uint64        `json:"preBalances"`
			PostBalances []uint64        `json:"postBalances"`
			Pre          []balance       `json:"preTokenBalances"`
			Post         []balance       `json:"postTokenBalances"`
		} `json:"meta"`
	}
	if e = c.rpcCall(ctx, "getTransaction", []any{signature, map[string]any{"encoding": "base64", "commitment": "confirmed", "maxSupportedTransactionVersion": 0}}, &tx); e != nil {
		return Observation{}, e
	}
	if tx == nil {
		return Observation{State: "unknown"}, nil
	}
	if len(tx.Transaction) != 2 || tx.Slot < q.ObservedSlot || tx.Slot != status.Value[0].Slot || tx.Meta.Fee < q.FeeLamports || tx.Meta.Fee > q.MaxFeeLamports {
		return Observation{}, errors.New("confirmation evidence differs from review")
	}
	var data, encoding string
	if json.Unmarshal(tx.Transaction[0], &data) != nil || json.Unmarshal(tx.Transaction[1], &encoding) != nil || encoding != "base64" {
		return Observation{}, errors.New("invalid confirmed transaction")
	}
	original, e := base64.StdEncoding.Strict().DecodeString(data)
	if e != nil || !bytes.Equal(original, packet) {
		return Observation{}, errors.New("confirmation is for different transaction bytes")
	}
	statusError, e := settlementError(status.Value[0].Err)
	if e != nil {
		return Observation{}, e
	}
	transactionError, e := settlementError(tx.Meta.Err)
	if e != nil {
		return Observation{}, e
	}
	if !reflect.DeepEqual(statusError, transactionError) {
		return Observation{}, errors.New("transaction status and receipt disagree; preserve original")
	}
	if statusError != nil {
		return Observation{State: "failed", Slot: tx.Slot}, nil
	}
	message, _ := Message(q)
	indexOf := func(mint string) int {
		ata, _ := TokenAccount(q.Owner, mint)
		key, _ := Key(ata)
		for i := 0; i < int(message[4]); i++ {
			if bytes.Equal(message[5+i*32:5+(i+1)*32], key) {
				return i
			}
		}
		return -1
	}
	value := func(rows []balance, mint string) (uint64, error) {
		found := 0
		var n uint64
		for _, b := range rows {
			if b.AccountIndex == indexOf(mint) {
				if b.Owner != q.Owner || b.Mint != mint {
					return 0, errors.New("settlement wallet/mint differs")
				}
				found++
				var e error
				n, e = strconv.ParseUint(b.Amount.Amount, 10, 64)
				if e != nil || strconv.FormatUint(n, 10) != b.Amount.Amount {
					return 0, errors.New("invalid settlement amount")
				}
			}
		}
		if found > 1 {
			return 0, errors.New("ambiguous settlement account")
		}
		return n, nil
	}
	amount, _ := Uint(q.Amount)
	minimum, _ := Uint(q.MinimumOutput)
	if q.WrapAndUnwrapSol && q.InputMint == SOL {
		if len(tx.Meta.PreBalances) == 0 || len(tx.Meta.PostBalances) == 0 {
			return Observation{}, errors.New("native SOL balance metadata missing")
		}
		pre0, post0 := tx.Meta.PreBalances[0], tx.Meta.PostBalances[0]
		if pre0 < post0 {
			return Observation{}, errors.New("settlement exceeds reviewed swap limits")
		}
		spent := pre0 - post0
		if spent < amount+tx.Meta.Fee || spent > amount+tx.Meta.Fee+q.RentLamports {
			return Observation{}, errors.New("native SOL settlement exceeds reviewed limits")
		}
		preOut, e := value(tx.Meta.Pre, q.OutputMint)
		if e != nil {
			return Observation{}, e
		}
		postOut, e := value(tx.Meta.Post, q.OutputMint)
		if e != nil {
			return Observation{}, e
		}
		if postOut < preOut || postOut-preOut < minimum {
			return Observation{}, errors.New("settlement exceeds reviewed swap limits")
		}
		return Observation{State: "confirmed", Slot: tx.Slot, Input: fmt.Sprint(amount), Output: fmt.Sprint(postOut - preOut)}, nil
	}
	preIn, e := value(tx.Meta.Pre, q.InputMint)
	if e != nil {
		return Observation{}, e
	}
	postIn, e := value(tx.Meta.Post, q.InputMint)
	if e != nil {
		return Observation{}, e
	}
	if preIn < postIn || preIn-postIn != amount {
		return Observation{}, errors.New("settlement exceeds reviewed swap limits")
	}
	if q.WrapAndUnwrapSol && q.OutputMint == SOL {
		if len(tx.Meta.PreBalances) == 0 || len(tx.Meta.PostBalances) == 0 {
			return Observation{}, errors.New("native SOL balance metadata missing")
		}
		pre0, post0 := tx.Meta.PreBalances[0], tx.Meta.PostBalances[0]
		if post0+tx.Meta.Fee < pre0 {
			return Observation{}, errors.New("native SOL output missing")
		}
		netOut := post0 + tx.Meta.Fee - pre0
		if netOut < minimum {
			return Observation{}, errors.New("settlement exceeds reviewed swap limits")
		}
		return Observation{State: "confirmed", Slot: tx.Slot, Input: fmt.Sprint(amount), Output: fmt.Sprint(netOut)}, nil
	}
	preOut, e := value(tx.Meta.Pre, q.OutputMint)
	if e != nil {
		return Observation{}, e
	}
	postOut, e := value(tx.Meta.Post, q.OutputMint)
	if e != nil {
		return Observation{}, e
	}
	if postOut < preOut || postOut-preOut < minimum {
		return Observation{}, errors.New("settlement exceeds reviewed swap limits")
	}
	return Observation{State: "confirmed", Slot: tx.Slot, Input: fmt.Sprint(amount), Output: fmt.Sprint(postOut - preOut)}, nil
}

// Only explicit null or supported Solana failure shapes can settle an original.
// Missing fields and unknown variants stay held; never infer failure from a
// zero-value JSON field or guess the meaning of a future RPC error variant.
func settlementError(raw json.RawMessage) (any, error) {
	var value any
	invalid := errors.New("transaction error evidence incomplete or unsupported; preserve original")
	if len(raw) == 0 || len(raw) > 4096 || json.Unmarshal(raw, &value) != nil {
		return nil, invalid
	}
	switch x := value.(type) {
	case nil:
		return nil, nil
	case string:
		switch x {
		case "AccountInUse", "AccountLoadedTwice", "AccountNotFound", "ProgramAccountNotFound", "InsufficientFundsForFee", "InvalidAccountForFee", "AlreadyProcessed", "BlockhashNotFound", "CallChainTooDeep", "MissingSignatureForFee", "InvalidAccountIndex", "SignatureFailure", "InvalidProgramForExecution", "SanitizeFailure", "ClusterMaintenance", "AccountBorrowOutstanding", "WouldExceedMaxBlockCostLimit", "UnsupportedVersion", "InvalidWritableAccount", "WouldExceedMaxAccountCostLimit", "WouldExceedAccountDataBlockLimit", "TooManyAccountLocks", "AddressLookupTableNotFound", "InvalidAddressLookupTableOwner", "InvalidAddressLookupTableData", "InvalidAddressLookupTableIndex", "InvalidRentPayingAccount", "WouldExceedMaxVoteCostLimit", "WouldExceedAccountDataTotalSize", "MaxLoadedAccountsDataSizeExceeded", "ResanitizationNeeded":
			return x, nil
		}
	case map[string]any:
		if len(x) != 1 {
			return nil, invalid
		}
		if idx, present := x["DuplicateInstruction"]; present {
			n, ok := idx.(float64)
			if !ok || n < 0 || n > 255 || n != float64(uint8(n)) {
				return nil, invalid
			}
			return x, nil
		}
		for _, accountKey := range []string{"InsufficientFundsForRent", "ProgramExecutionTemporarilyRestricted"} {
			if rawDetails, present := x[accountKey]; present {
				details, ok := rawDetails.(map[string]any)
				if !ok || len(details) != 1 {
					return nil, invalid
				}
				n, ok := details["account_index"].(float64)
				if !ok || n < 0 || n > 255 || n != float64(uint8(n)) {
					return nil, invalid
				}
				return x, nil
			}
		}
		parts, ok := x["InstructionError"].([]any)
		if !ok || len(parts) != 2 {
			return nil, invalid
		}
		index, ok := parts[0].(float64)
		if !ok || index < 0 || index > 255 || index != float64(uint8(index)) {
			return nil, invalid
		}
		switch reason := parts[1].(type) {
		case string:
			switch reason {
			case "GenericError", "InvalidArgument", "InvalidInstructionData", "InvalidAccountData", "AccountDataTooSmall", "InsufficientFunds", "IncorrectProgramId", "MissingRequiredSignature", "AccountAlreadyInitialized", "UninitializedAccount", "UnbalancedInstruction", "ModifiedProgramId", "ExternalAccountLamportSpend", "ExternalAccountDataModified", "ReadonlyLamportChange", "ReadonlyDataModified", "DuplicateAccountIndex", "ExecutableModified", "RentEpochModified", "NotEnoughAccountKeys", "AccountDataSizeChanged", "AccountNotExecutable", "AccountBorrowFailed", "AccountBorrowOutstanding", "DuplicateAccountOutOfSync", "InvalidError", "ExecutableDataModified", "ExecutableLamportChange", "ExecutableAccountNotRentExempt", "UnsupportedProgramId", "CallDepth", "MissingAccount", "ReentrancyNotAllowed", "MaxSeedLengthExceeded", "InvalidSeeds", "InvalidRealloc", "ComputationalBudgetExceeded", "PrivilegeEscalation", "ProgramEnvironmentSetupFailure", "ProgramFailedToComplete", "ProgramFailedToCompile", "Immutable", "IncorrectAuthority", "BorshIoError", "AccountNotRentExempt", "InvalidAccountOwner", "ArithmeticOverflow", "UnsupportedSysvar", "IllegalOwner", "MaxAccountsDataAllocationsExceeded", "AccountsDataAllocationTooSmall", "MaxInstructionTraceLengthExceeded", "BuiltinProgramsMustConsumeComputeUnits":
				return x, nil
			}
		case map[string]any:
			if len(reason) == 1 {
				if code, ok := reason["Custom"].(float64); ok && code >= 0 && code <= 4294967295 && code == float64(uint32(code)) {
					return x, nil
				}
				if msg, ok := reason["BorshIoError"].(string); ok && len(msg) > 0 && len(msg) <= 256 {
					return x, nil
				}
			}
		}
	}
	return nil, invalid
}
