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
	SettlementMode  string
	mu              sync.Mutex
	SimulationFails bool
	DropSubmission  bool
	Unknown         bool
	WrongNetwork    bool
	TamperBuild     bool
	RejectSubmit    bool
	Submissions     int
	Reads           int
	Quote           onchain.Quote
	Packet          []byte
	Calls           []string
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
		if v.Get("wrapAndUnwrapSol") != "false" || v.Get("transactionVersion") != "0" {
			return nil, errors.New("unsupported fixture request")
		}
		bps, _ := strconv.Atoi(v.Get("slippageBps"))
		q := Quote(v.Get("taker"), v.Get("inputMint"), v.Get("outputMint"), v.Get("amount"), uint16(bps))
		if c.TamperBuild {
			q.OutputMint = q.InputMint
		}
		c.Quote = q
		c.Calls = append(c.Calls, "build")
		result = map[string]any{"inputMint": q.InputMint, "outputMint": q.OutputMint, "inAmount": q.Amount, "outAmount": q.Output, "slippageBps": q.SlippageBPS, "setupInstructions": []any{}, "swapInstruction": q.Instructions[0], "cleanupInstruction": nil, "otherInstructions": []any{}, "tipInstruction": nil, "transactionVersion": 0}
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
	case "getFeeForMessage":
		result = map[string]any{"context": context, "value": 5000}
	case "getMinimumBalanceForRentExemption":
		result = 2_039_280
	case "simulateTransaction":
		var failure any
		if c.SimulationFails {
			failure = map[string]any{"InstructionError": []any{0, "Custom"}}
		}
		result = map[string]any{"context": context, "value": map[string]any{"err": failure, "unitsConsumed": 210000}}
	case "getBlockHeight":
		result = 20
	case "sendTransaction":
		var encoded string
		json.Unmarshal(call.Params[0], &encoded)
		packet, e := base64.StdEncoding.Strict().DecodeString(encoded)
		if e != nil || len(packet) < 65 {
			return nil, errors.New("invalid signed packet")
		}
		q := c.Quote
		q.MaxFeeLamports = 5000
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
		if len(c.Packet) > 0 && !c.Unknown {
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
		result = map[string]any{"slot": 11, "transaction": []string{base64.StdEncoding.EncodeToString(c.Packet), "base64"}, "meta": map[string]any{"fee": 5000, "err": nil, "preTokenBalances": []any{balance(q.InputMint, 1_000_000_000_000, keyIndex(source)), balance(q.OutputMint, 0, keyIndex(dest))}, "postTokenBalances": []any{balance(q.InputMint, 1_000_000_000_000-amount, keyIndex(source)), balance(q.OutputMint, output, keyIndex(dest))}}}
		c.setErrorEvidence(result.(map[string]any)["meta"].(map[string]any), "meta")
	default:
		return nil, errors.New("unexpected fixture RPC method")
	}
	return response(r, map[string]any{"jsonrpc": "2.0", "id": id, "result": result}), nil
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
	}
}
