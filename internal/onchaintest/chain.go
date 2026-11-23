// Package onchaintest supplies reconstructible fixtures, not a runtime adapter.
// Production packages never construct Chain. Both peers use real swap wire,
// HTTP encoding, signature and receipt verification against this controlled chain.
package onchaintest

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/fased-ai/fased-app/internal/onchain"
)

type Chain struct {
	SettlementMode     string
	CustomErr          any
	BlockHeight        uint64
	RevealAfterReads   int
	SimulateUnits      uint64
	RecentPriorityFees []uint64
	MissingInputATA    bool
	mu                 sync.Mutex
	SimulationFails    bool
	DropSubmission     bool
	Unknown            bool
	WrongNetwork       bool
	TamperBuild        bool
	RejectSubmit       bool
	Submissions        int
	Reads              int
	Quote              onchain.Quote
	Packet             []byte
	Calls              []string
	lastFee            uint64
}

func (*Chain) ControlledFixture() bool { return true }
func Client(c *Chain) *onchain.Client {
	x, e := onchain.NewControlledClient(c)
	if e != nil {
		panic(e)
	}
	return x
}
func Quote(owner string, in, out, amount string, bps uint16) onchain.Quote {
	n, _ := onchain.Uint(amount)
	output := n / 8
	if in == onchain.USDC {
		output = n * 8
	}
	source, _ := onchain.TokenAccount(owner, in)
	dest, _ := onchain.TokenAccount(owner, out)
	data := make([]byte, 39)
	copy(data, []byte{187, 100, 250, 204, 49, 196, 175, 20})
	binary.LittleEndian.PutUint64(data[8:], n)
	binary.LittleEndian.PutUint64(data[16:], output)
	binary.LittleEndian.PutUint16(data[24:], bps)
	binary.LittleEndian.PutUint32(data[30:], 1)
	copy(data[34:], []byte{0, 16, 39, 0, 1})
	accounts := []onchain.Meta{{owner, true, false}, {source, false, true}, {dest, false, true}, {in, false, false}, {out, false, false}, {onchain.Token, false, false}, {onchain.Token, false, false}, {onchain.Jupiter, false, false}, {onchain.EventAuthority, false, false}, {onchain.Jupiter, false, false}}
	return onchain.Quote{Version: onchain.Version, Owner: owner, InputMint: in, OutputMint: out, Amount: amount, Output: strconv.FormatUint(output, 10), SlippageBPS: bps, Instructions: []onchain.Instruction{{ProgramID: onchain.Jupiter, Accounts: accounts, Data: base64.StdEncoding.EncodeToString(data)}}}
}
func (c *Chain) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result any
	var id uint64
	if r.URL.Host == "api.jup.ag" && r.URL.Path == "/swap/v2/build" && r.Method == "GET" {
		if r.Header.Get("x-api-key") != "fased-test-onchain" {
			return nil, errors.New("fixture Jupiter credential missing")
		}
		v := r.URL.Query()
		wrap := v.Get("wrapAndUnwrapSol") == "true"
		if (v.Get("wrapAndUnwrapSol") != "false" && !wrap) || v.Get("transactionVersion") != "0" {
			return nil, errors.New("unsupported fixture request")
		}
		bps, _ := strconv.Atoi(v.Get("slippageBps"))
		q := Quote(v.Get("taker"), v.Get("inputMint"), v.Get("outputMint"), v.Get("amount"), uint16(bps))
		q.WrapAndUnwrapSol = wrap
		if c.TamperBuild {
			q.OutputMint = q.InputMint
		}
		setup := []onchain.Instruction{}
		var cleanup any
		if wrap {
			wsolATA, _ := onchain.TokenAccount(q.Owner, onchain.SOL)
			createATA := onchain.Instruction{
				ProgramID: onchain.AssociatedToken,
				Accounts: []onchain.Meta{
					{Pubkey: q.Owner, IsSigner: true, IsWritable: true},
					{Pubkey: wsolATA, IsSigner: false, IsWritable: true},
					{Pubkey: q.Owner, IsSigner: false, IsWritable: false},
					{Pubkey: onchain.SOL, IsSigner: false, IsWritable: false},
					{Pubkey: onchain.System, IsSigner: false, IsWritable: false},
					{Pubkey: onchain.Token, IsSigner: false, IsWritable: false},
				},
				Data: base64.StdEncoding.EncodeToString([]byte{1}),
			}
			setup = append(setup, createATA)
			if q.InputMint == onchain.SOL {
				amt, _ := onchain.Uint(q.Amount)
				transferData := make([]byte, 12)
				binary.LittleEndian.PutUint32(transferData[:4], 2)
				binary.LittleEndian.PutUint64(transferData[4:12], amt)
				setup = append(setup,
					onchain.Instruction{
						ProgramID: onchain.System,
						Accounts: []onchain.Meta{
							{Pubkey: q.Owner, IsSigner: true, IsWritable: true},
							{Pubkey: wsolATA, IsSigner: false, IsWritable: true},
						},
						Data: base64.StdEncoding.EncodeToString(transferData),
					},
					onchain.Instruction{
						ProgramID: onchain.Token,
						Accounts: []onchain.Meta{
							{Pubkey: wsolATA, IsSigner: false, IsWritable: true},
						},
						Data: base64.StdEncoding.EncodeToString([]byte{17}),
					},
				)
			}
			closeIx := onchain.Instruction{
				ProgramID: onchain.Token,
				Accounts: []onchain.Meta{
					{Pubkey: wsolATA, IsSigner: false, IsWritable: true},
					{Pubkey: q.Owner, IsSigner: false, IsWritable: true},
					{Pubkey: q.Owner, IsSigner: true, IsWritable: false},
				},
				Data: base64.StdEncoding.EncodeToString([]byte{9}),
			}
			cleanup = closeIx
			q.Instructions = append(append([]onchain.Instruction{}, setup...), q.Instructions[0], closeIx)
		}
		c.Quote = q
		c.Calls = append(c.Calls, "build")
		result = map[string]any{"inputMint": q.InputMint, "outputMint": q.OutputMint, "inAmount": q.Amount, "outAmount": q.Output, "slippageBps": q.SlippageBPS, "setupInstructions": setup, "swapInstruction": q.Instructions[len(setup)], "cleanupInstruction": cleanup, "otherInstructions": []any{}, "tipInstruction": nil, "transactionVersion": 0}
		return response(r, result), nil
	}
	if r.URL.Host != "rpc.fased-fixture.invalid" || r.Method != "POST" {
		return nil, errors.New("external fixture request denied")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 65536))
	if e != nil {
		return nil, e
	}
	var call struct {
		ID     uint64            `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if json.Unmarshal(raw, &call) != nil {
		return nil, errors.New("invalid fixture RPC")
	}
	id = call.ID
	c.Calls = append(c.Calls, call.Method)
	context := map[string]any{"slot": 10}
	switch call.Method {
	case "getGenesisHash":
		result = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
		if c.WrongNetwork {
			result = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"
		}
	case "getLatestBlockhash":
		result = map[string]any{"context": context, "value": map[string]any{"blockhash": onchain.ControlledBlockhash, "lastValidBlockHeight": 100}}
	case "getAccountInfo":
		var address string
		json.Unmarshal(call.Params[0], &address)
		q := c.Quote
		mint := q.OutputMint
		source, _ := onchain.TokenAccount(q.Owner, q.InputMint)
		if c.MissingInputATA && address == source {
			result = map[string]any{"context": context, "value": nil}
			break
		}
		if address == source {
			mint = q.InputMint
		}
		m, _ := onchain.Key(mint)
		o, _ := onchain.Key(q.Owner)
		data := make([]byte, 165)
		copy(data, m)
		copy(data[32:], o)
		binary.LittleEndian.PutUint64(data[64:], 1_000_000_000_000)
		data[108] = 1
		result = map[string]any{"context": context, "value": map[string]any{"owner": onchain.Token, "executable": false, "data": []string{base64.StdEncoding.EncodeToString(data), "base64"}}}
	case "getBalance":
		result = map[string]any{"context": context, "value": 10_000_000_000}
	case "getRecentPrioritizationFees":
		rows := make([]map[string]any, 0, len(c.RecentPriorityFees))
		for _, fee := range c.RecentPriorityFees {
			rows = append(rows, map[string]any{"slot": 10, "prioritizationFee": fee})
		}
		result = rows
	case "getFeeForMessage":
		fee := uint64(5000)
		var encoded string
		if len(call.Params) > 0 && json.Unmarshal(call.Params[0], &encoded) == nil {
			if msg, decErr := base64.StdEncoding.Strict().DecodeString(encoded); decErr == nil {
				if cuLimit, microPrice, ok := decodeComputeBudget(msg); ok {
					c.Quote.ComputeUnits = cuLimit
					c.Quote.PriorityFeeMicroLamports = microPrice
					fee = 5000 + (uint64(cuLimit)*microPrice+999_999)/1_000_000
				}
			}
		}
		c.lastFee = fee
		result = map[string]any{"context": context, "value": fee}
	case "getMinimumBalanceForRentExemption":
		result = 2_039_280
	case "simulateTransaction":
		var failure any
		if c.SimulationFails {
			failure = map[string]any{"InstructionError": []any{0, "Custom"}}
		}
		units := c.SimulateUnits
		if units == 0 {
			units = 210000
		}
		result = map[string]any{"context": context, "value": map[string]any{"err": failure, "unitsConsumed": units}}
	case "getBlockHeight":
		result = 20
		if c.BlockHeight > 0 {
			result = c.BlockHeight
		}
	case "sendTransaction":
		var encoded string
		json.Unmarshal(call.Params[0], &encoded)
		packet, e := base64.StdEncoding.Strict().DecodeString(encoded)
		if e != nil || len(packet) < 65 {
			return nil, errors.New("invalid signed packet")
		}
		q := c.Quote
		fee := uint64(5000)
		if cuLimit, microPrice, ok := decodeComputeBudget(packet[65:]); ok {
			q.ComputeUnits = cuLimit
			q.PriorityFeeMicroLamports = microPrice
			computed := 5000 + (uint64(cuLimit)*microPrice+999_999)/1_000_000
			if computed > fee {
				fee = computed
			}
		} else if c.lastFee > fee {
			fee = c.lastFee
		}
		q.MaxFeeLamports = fee
		q.Blockhash = onchain.ControlledBlockhash
		if _, e := onchain.VerifyPacket(q, packet); e != nil {
			return nil, e
		}
		c.Submissions++
		c.Packet = packet
		if c.DropSubmission {
			return nil, errors.New("controlled connection lost after acceptance")
		}
		if c.RejectSubmit {
			return response(r, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32002}}), nil
		}
		result = onchain.Base58(packet[1:65])
	case "getSignatureStatuses":
		c.Reads++
		var value any
		unknown := c.Unknown
		if unknown && c.RevealAfterReads > 0 && c.Reads >= c.RevealAfterReads {
			unknown = false
		}
		if len(c.Packet) > 0 && !unknown {
			value = map[string]any{"slot": 11, "err": nil, "confirmationStatus": "confirmed"}
			c.setErrorEvidence(value.(map[string]any), "status")
		}
		result = map[string]any{"context": context, "value": []any{value}}
	case "getTransaction":
		if len(c.Packet) == 0 {
			return response(r, map[string]any{"jsonrpc": "2.0", "id": id, "result": nil}), nil
		}
		q := c.Quote
		source, _ := onchain.TokenAccount(q.Owner, q.InputMint)
		dest, _ := onchain.TokenAccount(q.Owner, q.OutputMint)
		keyIndex := func(address string) int {
			key, _ := onchain.Key(address)
			message := c.Packet[65:]
			n := int(message[4])
			for i := 0; i < n; i++ {
				if bytes.Equal(message[5+i*32:5+(i+1)*32], key) {
					return i
				}
			}
			return -1
		}
		amount, _ := onchain.Uint(q.Amount)
		output, _ := onchain.Uint(q.Output)
		balance := func(mint string, n uint64, index int) any {
			return map[string]any{"accountIndex": index, "mint": mint, "owner": q.Owner, "uiTokenAmount": map[string]any{"amount": strconv.FormatUint(n, 10)}}
		}
		fee := uint64(5000)
		if c.lastFee > fee {
			fee = c.lastFee
		}
		nKeys := int(c.Packet[65+4])
		preBalances := make([]uint64, nKeys)
		postBalances := make([]uint64, nKeys)
		preBalances[0] = 10_000_000_000
		postBalances[0] = preBalances[0] - fee
		if q.WrapAndUnwrapSol && q.InputMint == onchain.SOL {
			postBalances[0] -= amount
		} else if q.WrapAndUnwrapSol && q.OutputMint == onchain.SOL {
			postBalances[0] += output
		}
		result = map[string]any{"slot": 11, "transaction": []string{base64.StdEncoding.EncodeToString(c.Packet), "base64"}, "meta": map[string]any{"fee": fee, "err": nil, "preBalances": preBalances, "postBalances": postBalances, "preTokenBalances": []any{balance(q.InputMint, 1_000_000_000_000, keyIndex(source)), balance(q.OutputMint, 0, keyIndex(dest))}, "postTokenBalances": []any{balance(q.InputMint, 1_000_000_000_000-amount, keyIndex(source)), balance(q.OutputMint, output, keyIndex(dest))}}}
		c.setErrorEvidence(result.(map[string]any)["meta"].(map[string]any), "meta")
	default:
		return nil, errors.New("unexpected fixture RPC method")
	}
	return response(r, map[string]any{"jsonrpc": "2.0", "id": id, "result": result}), nil
}

func decodeComputeBudget(msg []byte) (uint32, uint64, bool) {
	if len(msg) < 6 {
		return 0, 0, false
	}
	off := 5 + int(msg[4])*32 + 32 + 1
	if len(msg) < off+20 {
		return 0, 0, false
	}
	if msg[off+1] != 0 || msg[off+2] != 5 || msg[off+3] != 2 {
		return 0, 0, false
	}
	cuLimit := binary.LittleEndian.Uint32(msg[off+4 : off+8])
	off += 8
	if msg[off+1] != 0 || msg[off+2] != 9 || msg[off+3] != 3 {
		return 0, 0, false
	}
	microPrice := binary.LittleEndian.Uint64(msg[off+4 : off+12])
	return cuLimit, microPrice, true
}
func response(r *http.Request, v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(b)), Request: r}
}

func (c *Chain) RecordEvidence(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, e := json.MarshalIndent(map[string]any{"calls": c.Calls, "submissions": c.Submissions, "signatureReads": c.Reads, "externalRequests": 0}, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, b, 0600)
}

func (c *Chain) setErrorEvidence(row map[string]any, where string) {
	switch c.SettlementMode {
	case "missing-status":
		if where == "status" {
			delete(row, "err")
		}
	case "missing-meta":
		if where == "meta" {
			delete(row, "err")
		}
	case "missing-fee":
		if where == "meta" {
			delete(row, "fee")
		}
	case "unknown-error":
		row["err"] = map[string]any{"UnknownFailure": true}
	case "malformed":
		row["err"] = true
	case "mismatch":
		if where == "status" {
			row["err"] = "AccountNotFound"
		}
	case "failed":
		row["err"] = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 1}}}
	case "custom-err":
		if c.CustomErr != nil {
			row["err"] = c.CustomErr
		}
	}
}
