package core

import (
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
)

// Selection describes an entry the caller inspected. It grants no authority:
// the reader refetches current finalized activity and requires an exact match.
type WalletSelection struct {
	WalletActivity
	FetchedAt int64  `json:"fetchedAt"`
	ExpiresAt int64  `json:"expiresAt"`
	Binding   string `json:"binding"`
}
type WalletTokenChange struct {
	Account  string `json:"account"`
	Program  string `json:"program"`
	Mint     string `json:"mint"`
	Decimals int    `json:"decimals"`
	Before   string `json:"before"`
	After    string `json:"after"`
	Change   string `json:"change"`
}
type WalletTransaction struct {
	WalletActivity
	FeeLamports          string              `json:"feeLamports"`
	Payer                string              `json:"payer"`
	NativeBefore         string              `json:"nativeBefore"`
	NativeAfter          string              `json:"nativeAfter"`
	NativeChange         string              `json:"nativeChange"`
	Tokens               []WalletTokenChange `json:"tokens"`
	TokenChangesComplete bool                `json:"tokenChangesComplete"`
}

func validWalletSelection(s *WalletSelection) bool {
	if s == nil || !validWalletSignature(s.Signature) || !hexValue(s.Binding, 32) {
		return false
	}
	n, err := strconv.ParseUint(s.Slot, 10, 64)
	now := time.Now().Unix()
	return err == nil && strconv.FormatUint(n, 10) == s.Slot && s.FetchedAt > 0 && s.FetchedAt <= now && s.ExpiresAt > s.FetchedAt && s.ExpiresAt-s.FetchedAt == 60 && s.ExpiresAt > now && (s.BlockTime == nil || (*s.BlockTime >= 0 && *s.BlockTime <= now+300))
}
func sameWalletActivity(a, b WalletActivity) bool {
	return a.Signature == b.Signature && a.Slot == b.Slot && a.Failed == b.Failed && ((a.BlockTime == nil && b.BlockTime == nil) || (a.BlockTime != nil && b.BlockTime != nil && *a.BlockTime == *b.BlockTime))
}
func walletDelta(before, after string) string {
	a, _ := new(big.Int).SetString(before, 10)
	b, _ := new(big.Int).SetString(after, 10)
	return b.Sub(b, a).String()
}

type transactionToken struct {
	Index   *uint8 `json:"accountIndex"`
	Mint    string `json:"mint"`
	Owner   string `json:"owner"`
	Program string `json:"programId"`
	Amount  struct {
		Amount   string `json:"amount"`
		Decimals *uint8 `json:"decimals"`
	} `json:"uiTokenAmount"`
}

// Only public pre/post observations are interpreted. Instructions, logs, memos,
// UI floating-point amounts and arbitrary metadata never become displayed facts.
func parseWalletTransaction(raw json.RawMessage, address string, selected *WalletSelection) (WalletTransaction, error) {
	var empty WalletTransaction
	unavailable := errors.New("Transaction unavailable")
	if selected == nil {
		return empty, unavailable
	}
	var tx struct {
		Slot        *uint64         `json:"slot"`
		BlockTime   *int64          `json:"blockTime"`
		Version     json.RawMessage `json:"version"`
		Transaction struct {
			Signatures []string `json:"signatures"`
			Message    struct {
				Keys   []string `json:"accountKeys"`
				Header struct {
					Required         *uint8 `json:"numRequiredSignatures"`
					ReadonlySigned   *uint8 `json:"numReadonlySignedAccounts"`
					ReadonlyUnsigned *uint8 `json:"numReadonlyUnsignedAccounts"`
				} `json:"header"`
				Lookups []struct {
					Key      string  `json:"accountKey"`
					Writable []uint8 `json:"writableIndexes"`
					Readonly []uint8 `json:"readonlyIndexes"`
				} `json:"addressTableLookups"`
			} `json:"message"`
		} `json:"transaction"`
		Meta *struct {
			Err        json.RawMessage    `json:"err"`
			Fee        *uint64            `json:"fee"`
			Before     []*uint64          `json:"preBalances"`
			After      []*uint64          `json:"postBalances"`
			PreTokens  []transactionToken `json:"preTokenBalances"`
			PostTokens []transactionToken `json:"postTokenBalances"`
			Loaded     *struct {
				Writable []string `json:"writable"`
				Readonly []string `json:"readonly"`
			} `json:"loadedAddresses"`
		} `json:"meta"`
	}
	if len(raw) > 262144 || json.Unmarshal(raw, &tx) != nil || tx.Meta == nil || tx.Slot == nil || tx.Meta.Fee == nil || len(tx.Meta.Err) == 0 {
		return empty, unavailable
	}
	activity := WalletActivity{Signature: selected.Signature, Slot: strconv.FormatUint(*tx.Slot, 10), BlockTime: tx.BlockTime, Failed: string(tx.Meta.Err) != "null"}
	if !sameWalletActivity(activity, selected.WalletActivity) {
		return empty, unavailable
	}
	m := tx.Transaction.Message
	meta := tx.Meta
	h := m.Header
	if h.Required == nil || h.ReadonlySigned == nil || h.ReadonlyUnsigned == nil || len(m.Keys) == 0 || len(m.Keys) > 256 || *h.Required == 0 || int(*h.Required) > len(m.Keys) || *h.ReadonlySigned >= *h.Required || int(*h.ReadonlyUnsigned) > len(m.Keys)-int(*h.Required) || len(tx.Transaction.Signatures) != int(*h.Required) || tx.Transaction.Signatures[0] != selected.Signature {
		return empty, unavailable
	}
	for _, sig := range tx.Transaction.Signatures {
		if !validWalletSignature(sig) {
			return empty, unavailable
		}
	}
	keys := append([]string{}, m.Keys...)
	switch string(tx.Version) {
	case `"legacy"`:
		if len(m.Lookups) != 0 || (meta.Loaded != nil && len(meta.Loaded.Writable)+len(meta.Loaded.Readonly) != 0) {
			return empty, unavailable
		}
	case "0":
		if meta.Loaded == nil || meta.Loaded.Writable == nil || meta.Loaded.Readonly == nil {
			return empty, unavailable
		}
		w, r := 0, 0
		for _, lookup := range m.Lookups {
			if _, err := onchain.Key(lookup.Key); err != nil {
				return empty, unavailable
			}
			seen := map[uint8]bool{}
			for _, group := range [][]uint8{lookup.Writable, lookup.Readonly} {
				for _, i := range group {
					if seen[i] {
						return empty, unavailable
					}
					seen[i] = true
				}
			}
			w += len(lookup.Writable)
			r += len(lookup.Readonly)
		}
		if w != len(meta.Loaded.Writable) || r != len(meta.Loaded.Readonly) {
			return empty, unavailable
		}
		keys = append(keys, meta.Loaded.Writable...)
		keys = append(keys, meta.Loaded.Readonly...)
	default:
		return empty, unavailable
	}
	if len(keys) > 256 || len(meta.Before) != len(keys) || len(meta.After) != len(keys) {
		return empty, unavailable
	}
	index := -1
	seen := map[string]bool{}
	for i, key := range keys {
		if _, err := onchain.Key(key); err != nil || seen[key] || meta.Before[i] == nil || meta.After[i] == nil {
			return empty, unavailable
		}
		seen[key] = true
		if key == address {
			index = i
		}
	}
	if index < 0 {
		return empty, unavailable
	}
	before, after := strconv.FormatUint(*meta.Before[index], 10), strconv.FormatUint(*meta.After[index], 10)
	out := WalletTransaction{WalletActivity: activity, FeeLamports: strconv.FormatUint(*meta.Fee, 10), Payer: keys[0], NativeBefore: before, NativeAfter: after, NativeChange: walletDelta(before, after), Tokens: []WalletTokenChange{}, TokenChangesComplete: meta.PreTokens != nil && meta.PostTokens != nil}
	if len(meta.PreTokens) > 256 || len(meta.PostTokens) > 256 {
		return empty, unavailable
	}
	groups := []map[uint8]transactionToken{{}, {}}
	for side, rows := range [][]transactionToken{meta.PreTokens, meta.PostTokens} {
		for _, row := range rows {
			if row.Index == nil || int(*row.Index) >= len(keys) {
				return empty, unavailable
			}
			if _, exists := groups[side][*row.Index]; exists {
				return empty, unavailable
			}
			if _, err := onchain.Key(row.Mint); err != nil {
				return empty, unavailable
			}
			n, err := strconv.ParseUint(row.Amount.Amount, 10, 64)
			if err != nil || strconv.FormatUint(n, 10) != row.Amount.Amount || row.Amount.Decimals == nil {
				return empty, unavailable
			}
			if row.Owner != "" {
				if _, err := onchain.Key(row.Owner); err != nil {
					return empty, unavailable
				}
			}
			if row.Program != "" {
				if _, err := onchain.Key(row.Program); err != nil {
					return empty, unavailable
				}
			}
			groups[side][*row.Index] = row
		}
	}
	for i := 0; i < len(keys); i++ {
		a, pre := groups[0][uint8(i)]
		b, post := groups[1][uint8(i)]
		if !pre && !post {
			continue
		}
		if (pre && a.Owner == "") || (post && b.Owner == "") {
			out.TokenChangesComplete = false
			continue
		}
		if a.Owner != address && b.Owner != address {
			continue
		}
		if !pre || !post || a.Owner != address || b.Owner != address || a.Program == "" || (a.Program != onchain.Token && a.Program != token2022Program) || b.Program == "" || (b.Program != onchain.Token && b.Program != token2022Program) {
			out.TokenChangesComplete = false
			continue
		}
		if a.Program != b.Program || a.Mint != b.Mint || *a.Amount.Decimals != *b.Amount.Decimals {
			return empty, unavailable
		}
		change := walletDelta(a.Amount.Amount, b.Amount.Amount)
		if change != "0" {
			out.Tokens = append(out.Tokens, WalletTokenChange{Account: keys[i], Program: a.Program, Mint: a.Mint, Decimals: int(*a.Amount.Decimals), Before: a.Amount.Amount, After: b.Amount.Amount, Change: change})
		}
	}
	return out, nil
}
