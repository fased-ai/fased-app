package core

// Solana's documented getBalance/getTokenAccountsByOwner/getSignaturesForAddress
// responses are observations from one RPC, not a financial ledger or a proof.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
)

const token2022Program = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
const walletActivityLimit = 20

type WalletTokenBalance struct {
	Program  string `json:"program"`
	Mint     string `json:"mint"`
	Amount   string `json:"amount"`
	Decimals int    `json:"decimals"`
	Slot     string `json:"slot"`
}
type WalletActivity struct {
	Signature string `json:"signature"`
	Slot      string `json:"slot"`
	BlockTime *int64 `json:"blockTime"`
	Failed    bool   `json:"failed"`
}
type WalletSnapshot struct {
	Transaction   *WalletTransaction   `json:"transaction,omitempty"`
	Version       int                  `json:"version"`
	AccountID     string               `json:"accountId"`
	Revision      int64                `json:"revision"`
	Binding       string               `json:"binding"`
	CredentialRef string               `json:"credentialRef"`
	Address       string               `json:"address"`
	Network       string               `json:"network"`
	Endpoint      string               `json:"endpoint"`
	Commitment    string               `json:"commitment"`
	Slot          string               `json:"slot"`
	Lamports      string               `json:"lamports"`
	Tokens        []WalletTokenBalance `json:"tokens"`
	Activity      []WalletActivity     `json:"activity"`
	ActivityLimit int                  `json:"activityLimit"`
	FetchedAt     int64                `json:"fetchedAt"`
	ExpiresAt     int64                `json:"expiresAt"`
	FixtureOnly   bool                 `json:"fixtureOnly"`
	LiveRPC       bool                 `json:"liveRPC"`
}

func walletRPCCaller(ctx context.Context, endpoint string, rt http.RoundTripper, valid func() error) func(string, any, any) error {
	client := &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("RPC redirects refused") }}
	id := 0
	return func(method string, params any, dest any) error {
		if err := valid(); err != nil {
			return err
		}
		id++
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return errors.New("RPC unavailable")
		}
		req.Host = req.URL.Host
		req.Header.Set("Content-Type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return errors.New("RPC unavailable")
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 262145))
		if err != nil || len(data) > 262144 || response.StatusCode != 200 {
			return errors.New("RPC unavailable")
		}
		var envelope struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   json.RawMessage `json:"error"`
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&envelope) != nil || decoder.Decode(new(any)) != io.EOF || envelope.JSONRPC != "2.0" || envelope.ID != id || (len(envelope.Error) > 0 && string(envelope.Error) != "null") || len(envelope.Result) == 0 || string(envelope.Result) == "null" || json.Unmarshal(envelope.Result, dest) != nil {
			return errors.New("Invalid RPC response")
		}
		return valid()
	}
}

func walletRPC(ctx context.Context, endpoint string, rt http.RoundTripper, valid func() error, fixture bool, address, network string, selection *WalletSelection) (WalletSnapshot, error) {
	var out WalletSnapshot
	if _, err := onchain.Key(address); err != nil || solanaGenesis(network) == "" || rt == nil || valid == nil {
		return out, errors.New("Wallet unavailable")
	}
	call := walletRPCCaller(ctx, endpoint, rt, valid)
	var genesis string
	if err := call("getGenesisHash", []any{}, &genesis); err != nil {
		return out, err
	}
	if genesis != solanaGenesis(network) {
		return out, errors.New("Wrong network")
	}
	var balance struct {
		Context struct {
			Slot *uint64 `json:"slot"`
		} `json:"context"`
		Value *uint64 `json:"value"`
	}
	if err := call("getBalance", []any{address, map[string]any{"commitment": "finalized"}}, &balance); err != nil {
		return out, err
	}
	if balance.Context.Slot == nil || balance.Value == nil {
		return out, errors.New("Invalid RPC response")
	}
	out = WalletSnapshot{Version: 1, Address: address, Network: network, Endpoint: endpoint, Commitment: "finalized", Slot: strconv.FormatUint(*balance.Context.Slot, 10), Lamports: strconv.FormatUint(*balance.Value, 10), Tokens: []WalletTokenBalance{}, Activity: []WalletActivity{}, ActivityLimit: walletActivityLimit, FixtureOnly: fixture, LiveRPC: !fixture}
	seenAccounts := map[string]bool{}
	tokens := map[string]WalletTokenBalance{}
	for _, program := range []string{onchain.Token, token2022Program} {
		var result struct {
			Context struct {
				Slot *uint64 `json:"slot"`
			} `json:"context"`
			Value []struct {
				Pubkey  string `json:"pubkey"`
				Account struct {
					Owner      string `json:"owner"`
					Executable *bool  `json:"executable"`
					Data       struct {
						Parsed struct {
							Type string `json:"type"`
							Info struct {
								Owner       string `json:"owner"`
								Mint        string `json:"mint"`
								State       string `json:"state"`
								TokenAmount struct {
									Amount   string `json:"amount"`
									Decimals *uint8 `json:"decimals"`
								} `json:"tokenAmount"`
							} `json:"info"`
						} `json:"parsed"`
					} `json:"data"`
				} `json:"account"`
			} `json:"value"`
		}
		if err := call("getTokenAccountsByOwner", []any{address, map[string]any{"programId": program}, map[string]any{"commitment": "finalized", "encoding": "jsonParsed", "minContextSlot": *balance.Context.Slot}}, &result); err != nil {
			return WalletSnapshot{}, err
		}
		if result.Context.Slot == nil || *result.Context.Slot < *balance.Context.Slot || result.Value == nil || len(result.Value) > 64 {
			return WalletSnapshot{}, errors.New("Invalid token response")
		}
		for _, entry := range result.Value {
			info := entry.Account.Data.Parsed.Info
			if _, err := onchain.Key(entry.Pubkey); err != nil {
				return WalletSnapshot{}, errors.New("Invalid token response")
			}
			if _, err := onchain.Key(info.Mint); err != nil {
				return WalletSnapshot{}, errors.New("Invalid token response")
			}
			amount, err := strconv.ParseUint(info.TokenAmount.Amount, 10, 64)
			if err != nil || strconv.FormatUint(amount, 10) != info.TokenAmount.Amount || info.TokenAmount.Decimals == nil || info.Owner != address || entry.Account.Owner != program || entry.Account.Executable == nil || *entry.Account.Executable || entry.Account.Data.Parsed.Type != "account" || (info.State != "initialized" && info.State != "frozen") || seenAccounts[entry.Pubkey] {
				return WalletSnapshot{}, errors.New("Invalid token response")
			}
			seenAccounts[entry.Pubkey] = true
			key := program + ":" + info.Mint
			current, ok := tokens[key]
			if ok && current.Decimals != int(*info.TokenAmount.Decimals) {
				return WalletSnapshot{}, errors.New("Invalid token response")
			}
			total := new(big.Int).SetUint64(amount)
			if ok {
				old, _ := new(big.Int).SetString(current.Amount, 10)
				total.Add(total, old)
			}
			tokens[key] = WalletTokenBalance{Program: program, Mint: info.Mint, Amount: total.String(), Decimals: int(*info.TokenAmount.Decimals), Slot: strconv.FormatUint(*result.Context.Slot, 10)}
		}
	}
	for _, token := range tokens {
		out.Tokens = append(out.Tokens, token)
	}
	sort.Slice(out.Tokens, func(i, j int) bool {
		return out.Tokens[i].Program+out.Tokens[i].Mint < out.Tokens[j].Program+out.Tokens[j].Mint
	})
	var signatures []struct {
		Signature string          `json:"signature"`
		Slot      *uint64         `json:"slot"`
		Err       json.RawMessage `json:"err"`
		BlockTime *int64          `json:"blockTime"`
		Status    string          `json:"confirmationStatus"`
	}
	if err := call("getSignaturesForAddress", []any{address, map[string]any{"commitment": "finalized", "limit": walletActivityLimit, "minContextSlot": *balance.Context.Slot}}, &signatures); err != nil {
		return WalletSnapshot{}, err
	}
	if signatures == nil || len(signatures) > walletActivityLimit {
		return WalletSnapshot{}, errors.New("Invalid activity response")
	}
	seenSignatures := map[string]bool{}
	var last uint64 = ^uint64(0)
	for _, signature := range signatures {
		if !validWalletSignature(signature.Signature) || signature.Slot == nil || *signature.Slot > last || signature.Status != "finalized" || len(signature.Err) == 0 || seenSignatures[signature.Signature] || (signature.BlockTime != nil && (*signature.BlockTime < 0 || *signature.BlockTime > time.Now().Unix()+300)) {
			return WalletSnapshot{}, errors.New("Invalid activity response")
		}
		last = *signature.Slot
		seenSignatures[signature.Signature] = true
		out.Activity = append(out.Activity, WalletActivity{Signature: signature.Signature, Slot: strconv.FormatUint(*signature.Slot, 10), BlockTime: signature.BlockTime, Failed: string(signature.Err) != "null"})
	}
	if selection != nil {
		found := false
		for _, entry := range out.Activity {
			if sameWalletActivity(entry, selection.WalletActivity) {
				found = true
				break
			}
		}
		if !found {
			return WalletSnapshot{}, errors.New("Transaction unavailable")
		}
		var raw json.RawMessage
		if err := call("getTransaction", []any{selection.Signature, map[string]any{"commitment": "finalized", "encoding": "json", "maxSupportedTransactionVersion": 0}}, &raw); err != nil {
			return WalletSnapshot{}, err
		}
		detail, err := parseWalletTransaction(raw, address, selection)
		if err != nil {
			return WalletSnapshot{}, err
		}
		out.Transaction = &detail
	}
	if err := valid(); err != nil {
		return WalletSnapshot{}, err
	}
	now := time.Now().UTC().Unix()
	out.FetchedAt = now
	out.ExpiresAt = now + 60
	return out, nil
}

func validWalletSignature(value string) bool {
	// Canonical signature is 64 bytes, not merely a plausible base58 string.
	if len(value) < 64 || len(value) > 88 {
		return false
	}
	n := new(big.Int)
	for _, c := range value {
		i := bytes.IndexRune([]byte("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"), c)
		if i < 0 {
			return false
		}
		n.Mul(n, big.NewInt(58))
		n.Add(n, big.NewInt(int64(i)))
	}
	raw := n.Bytes()
	zeros := 0
	for _, c := range value {
		if c != '1' {
			break
		}
		zeros++
	}
	raw = append(make([]byte, zeros), raw...)
	return len(raw) == 64 && onchain.Base58(raw) == value
}
