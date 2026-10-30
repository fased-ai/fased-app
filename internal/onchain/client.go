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
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

const buildURL = "https://api.jup.ag/swap/v2/build"

type Client struct {
	rpc      string
	key      string
	http     *http.Client
	sequence atomic.Uint64
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

// NewClient has no transport/CA override. Core admission remains separate; a
// valid endpoint or API key never enables execution. Tests inject transport
// inside this package, never through instance settings, HTTP or CLI input.
func NewClient(rpc, key string) (*Client, error) {
	u, e := url.Parse(rpc)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(rpc) > 2048 || len(key) > 512 {
		return nil, errors.New("HTTPS RPC and bounded Jupiter credential required")
	}
	t := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13}, Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, errors.New("RPC address unavailable")
		}
		for _, ip := range ips {
			if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				return nil, errors.New("private network address denied")
			}
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}}
	return &Client{rpc: rpc, key: key, http: &http.Client{Transport: t, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("swap redirects denied") }}}, nil
}
func (c *Client) request(ctx context.Context, method, target string, body []byte, key bool) ([]byte, error) {
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
		return nil, errors.New("swap service rejected bounded request")
	}
	var fields map[string]json.RawMessage
	if update.StrictJSONBounded(raw, &fields, 512*1024) != nil {
		return nil, errors.New("invalid or ambiguous swap response")
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
func (c *Client) Prepare(ctx context.Context, q Quote) (Quote, error) {
	if !ValidRequest(Request{q.AccountID, q.InputMint, q.OutputMint, q.Amount, q.SlippageBPS, q.MaxFeeLamports}) {
		return q, errors.New("invalid swap request")
	}
	genesis := map[string]string{"mainnet-beta": "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d", "devnet": "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"}[q.Network]
	var actualGenesis string
	if genesis == "" || c.rpcCall(ctx, "getGenesisHash", []any{}, &actualGenesis) != nil || actualGenesis != genesis {
		return q, errors.New("RPC network differs from wallet")
	}
	values := url.Values{"inputMint": {q.InputMint}, "outputMint": {q.OutputMint}, "amount": {q.Amount}, "taker": {q.Owner}, "slippageBps": {strconv.Itoa(int(q.SlippageBPS))}, "wrapAndUnwrapSol": {"false"}, "maxAccounts": {"24"}, "transactionVersion": {"0"}}
	raw, e := c.request(ctx, "GET", buildURL+"?"+values.Encode(), nil, true)
	if e != nil {
		return q, e
	}
	var build struct {
		InputMint          string        `json:"inputMint"`
		OutputMint         string        `json:"outputMint"`
		InAmount           string        `json:"inAmount"`
		OutAmount          string        `json:"outAmount"`
		SlippageBPS        uint16        `json:"slippageBps"`
		TransactionVersion *int          `json:"transactionVersion"`
		Setup              []Instruction `json:"setupInstructions"`
		Swap               Instruction   `json:"swapInstruction"`
		Cleanup            *Instruction  `json:"cleanupInstruction"`
		Other              []Instruction `json:"otherInstructions"`
		Tip                *Instruction  `json:"tipInstruction"`
	}
	if json.Unmarshal(raw, &build) != nil || build.TransactionVersion == nil || *build.TransactionVersion != 0 || build.InputMint != q.InputMint || build.OutputMint != q.OutputMint || build.InAmount != q.Amount || build.SlippageBPS != q.SlippageBPS || len(build.Setup) > 2 || build.Cleanup != nil || build.Tip != nil || len(build.Other) > 0 {
		return q, errors.New("unsupported Jupiter build or changed quote parameters")
	}
	q.Output = build.OutAmount
	q.MinimumOutput, e = Minimum(q.Output, q.SlippageBPS)
	if e != nil {
		return q, e
	}
	q.Instructions = append(build.Setup, build.Swap)
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
	if _, e = c.token(ctx, source, q.Owner, q.InputMint, amount); e != nil {
		return q, e
	}
	exists, e := c.token(ctx, dest, q.Owner, q.OutputMint, 0)
	if e != nil {
		return q, e
	}
	if !exists {
		found := false
		for _, ix := range build.Setup {
			if ix.ProgramID == AssociatedToken && len(ix.Accounts) > 1 && ix.Accounts[1].Pubkey == dest {
				found = true
			}
		}
		if !found {
			return q, errors.New("destination token setup missing")
		}
		if e = c.rpcCall(ctx, "getMinimumBalanceForRentExemption", []any{165, map[string]any{"commitment": "confirmed"}}, &q.RentLamports); e != nil {
			return q, e
		}
		if q.RentLamports > 6_000_000 {
			return q, errors.New("token rent exceeds supported bound")
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
	if balance.Value < q.FeeLamports+q.RentLamports {
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
	signature, e := VerifyPacket(q, packet)
	if e != nil {
		return Observation{}, e
	}
	var status struct {
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
			Err  json.RawMessage `json:"err"`
			Fee  uint64          `json:"fee"`
			Pre  []balance       `json:"preTokenBalances"`
			Post []balance       `json:"postTokenBalances"`
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
	preIn, e := value(tx.Meta.Pre, q.InputMint)
	if e != nil {
		return Observation{}, e
	}
	postIn, e := value(tx.Meta.Post, q.InputMint)
	if e != nil {
		return Observation{}, e
	}
	preOut, e := value(tx.Meta.Pre, q.OutputMint)
	if e != nil {
		return Observation{}, e
	}
	postOut, e := value(tx.Meta.Post, q.OutputMint)
	if e != nil {
		return Observation{}, e
	}
	amount, _ := Uint(q.Amount)
	minimum, _ := Uint(q.MinimumOutput)
	if preIn < postIn || preIn-postIn != amount || postOut < preOut || postOut-preOut < minimum {
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
		case "AccountInUse", "AccountLoadedTwice", "AccountNotFound", "ProgramAccountNotFound", "InsufficientFundsForFee", "InvalidAccountForFee", "AlreadyProcessed", "BlockhashNotFound", "SignatureFailure", "InvalidAccountIndex", "InvalidProgramForExecution", "SanitizeFailure", "UnsupportedVersion":
			return x, nil
		}
	case map[string]any:
		if len(x) != 1 {
			return nil, invalid
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
			case "InvalidArgument", "InvalidInstructionData", "InvalidAccountData", "InsufficientFunds", "MissingRequiredSignature", "ProgramFailedToComplete", "ComputationalBudgetExceeded":
				return x, nil
			}
		case map[string]any:
			code, ok := reason["Custom"].(float64)
			if len(reason) == 1 && ok && code >= 0 && code <= 4294967295 && code == float64(uint32(code)) {
				return x, nil
			}
		}
	}
	return nil, invalid
}
