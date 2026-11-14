package wenstrategy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"time"
)

type HistoryScope struct {
	Market    string `json:"market"`
	Terms     string `json:"terms"`
	Evidence  string `json:"evidence"`
	From      string `json:"from"`
	To        string `json:"to"`
	Dataset   string `json:"dataset"`
	Valuation string `json:"valuation"`
	Costs     string `json:"costs"`
	Coverage  string `json:"coverage"`
}
type ImportedHistoryRow struct {
	ID             string `json:"id"`
	Cursor         int64  `json:"cursor"`
	StrategyDigest string `json:"strategyDigest"`
	At             string `json:"at"`
	Finality       string `json:"finality"`
	Category       string `json:"category"`
	Committed      string `json:"committed"`
	Returned       string `json:"returned"`
	Fees           string `json:"fees"`
	Forfeitures    string `json:"forfeitures"`
	UserCosts      string `json:"userCosts"`
	BTCSaleSOL     string `json:"btcSaleSOL"`
	SATSaleSOL     string `json:"satSaleSOL"`
	BTCPending     string `json:"btcPending"`
	BTCReceived    string `json:"btcReceived"`
	SATEarned      string `json:"satEarned"`
	SATClaimed     string `json:"satClaimed"`
	SATSold        string `json:"satSold"`
}
type ImportedHistory struct {
	Schema          string               `json:"schema"`
	Source          string               `json:"source"`
	Verified        bool                 `json:"verified"`
	Scope           HistoryScope         `json:"scope"`
	InitialBankroll *string              `json:"initialBankroll"`
	Skipped         *int64               `json:"skipped"`
	PendingExposure *string              `json:"pendingExposure"`
	Rows            []ImportedHistoryRow `json:"rows"`
	Gaps            []string             `json:"gaps"`
}

func ParseHistory(raw []byte) (ImportedHistory, error) {
	var h ImportedHistory
	bad := errors.New("invalid unverified history import")
	if len(raw) == 0 || len(raw) > 65536 {
		return h, bad
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	tree, e := readValue(d, 0)
	if e != nil {
		return h, e
	}
	if _, e = d.Token(); e != io.EOF {
		return h, bad
	}
	if e = checkShape(tree, reflect.TypeOf(h), "history"); e != nil {
		return h, e
	}
	if e = json.Unmarshal(raw, &h); e != nil {
		return h, e
	}
	if h.Schema != "wen-history-import/1" || h.Verified || len(h.Source) == 0 || len(h.Source) > 256 || len(h.Rows) > 1000 || len(h.Gaps) > 1000 || !member(h.Scope.Evidence, "actual", "paper", "historical-replay") {
		return h, bad
	}
	for _, s := range []string{h.Scope.Market, h.Scope.Terms, h.Scope.Dataset, h.Scope.Valuation, h.Scope.Costs, h.Scope.Coverage} {
		if len(s) == 0 || len(s) > 256 {
			return h, bad
		}
	}
	from, e := time.Parse(time.RFC3339, h.Scope.From)
	if e != nil || from.UTC().Format("2006-01-02T15:04:05Z") != h.Scope.From {
		return h, bad
	}
	to, e := time.Parse(time.RFC3339, h.Scope.To)
	if e != nil || to.UTC().Format("2006-01-02T15:04:05Z") != h.Scope.To || !from.Before(to) {
		return h, bad
	}
	validAmount := func(s string) bool { return len(s) <= 30 && decimal.MatchString(s) }
	for _, v := range []*string{h.InitialBankroll, h.PendingExposure} {
		if v != nil && !validAmount(*v) {
			return h, bad
		}
	}
	if h.Skipped != nil && (*h.Skipped < 0 || *h.Skipped > 9007199254740991) {
		return h, bad
	}
	seen := map[string]map[int64]ImportedHistoryRow{}
	for _, r := range h.Rows {
		at, e := time.Parse(time.RFC3339, r.At)
		if e != nil || at.UTC().Format("2006-01-02T15:04:05Z") != r.At || at.Before(from) || !at.Before(to) || len(r.ID) == 0 || len(r.ID) > 256 || r.Cursor < 1 || r.Cursor > 9007199254740991 || !seed.MatchString(r.StrategyDigest) || !member(r.Finality, "provisional", "finalized", "reverted") || !member(r.Category, "mining", "staking", "prize", "carry", "transfer") {
			return h, bad
		}
		for _, v := range []string{r.Committed, r.Returned, r.Fees, r.Forfeitures, r.UserCosts, r.BTCSaleSOL, r.SATSaleSOL, r.BTCPending, r.BTCReceived, r.SATEarned, r.SATClaimed, r.SATSold} {
			if !validAmount(v) {
				return h, bad
			}
		}
		if seen[r.ID] == nil {
			seen[r.ID] = map[int64]ImportedHistoryRow{}
		}
		if old, ok := seen[r.ID][r.Cursor]; ok && old != r {
			return h, bad
		}
		seen[r.ID][r.Cursor] = r
	}
	for _, g := range h.Gaps {
		if len(g) == 0 || len(g) > 256 {
			return h, bad
		}
	}
	return h, nil
}
