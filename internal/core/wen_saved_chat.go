package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"sort"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

// References select retained private records, never executable strategy authority.
type WENSavedChatSelection struct {
	Profile        string `json:"profile"`
	Revision       int64  `json:"revision"` // private library revision, not Chat revision
	StrategyDigest string `json:"strategyDigest,omitempty"`
	BundleIndex    *int   `json:"bundleIndex,omitempty"`
}

const savedChatPrefix = "Frozen saved WEN strategy/history — unverified owner records, not a fresh task input. SOL amounts are lamports; BTC/SAT use stored base units. Net includes actual sales, excludes unsold rewards and overlays. No AI, task or financial authority.\n"

type savedChatProjection struct {
	Schema            string                `json:"schema"`
	WorkspaceRevision int64                 `json:"workspaceRevision"`
	PreparedAt        string                `json:"preparedAt"`
	Selection         WENSavedChatSelection `json:"selection"`
	Strategy          *savedChatStrategy    `json:"strategy,omitempty"`
	History           *savedChatHistory     `json:"history,omitempty"`
}
type savedChatStrategy struct {
	Digest    string               `json:"digest"`
	CreatedAt string               `json:"createdAt"`
	Origin    string               `json:"origin"`
	Document  wenstrategy.Document `json:"document"`
}
type savedChatHistory struct {
	SHA256          string                   `json:"storedBundleSHA256"`
	Source          string                   `json:"source"`
	Verified        bool                     `json:"verified"`
	Scope           wenstrategy.HistoryScope `json:"scope"`
	InitialBankroll *string                  `json:"initialBankroll"`
	Skipped         *int64                   `json:"skipped"`
	PendingExposure *string                  `json:"pendingExposure"`
	Gaps            []string                 `json:"gaps"`
	RawRows         int                      `json:"rawRows"`
	RelevantRows    int                      `json:"reconciledSelectedRows"`
	Reverted        int                      `json:"revertedSelectedRows"`
	Summary         map[string]any           `json:"summary"`
}

// Mirror the existing shared history accounting, using exact integers. These are
// descriptive imported results, not a payout calculator or a verified replay.
func savedHistorySummary(h wenstrategy.ImportedHistory, digest string) (map[string]any, int, int) {
	latest := map[string]wenstrategy.ImportedHistoryRow{}
	for _, r := range h.Rows {
		if old, ok := latest[r.ID]; !ok || r.Cursor > old.Cursor {
			latest[r.ID] = r
		}
	}
	rows := []wenstrategy.ImportedHistoryRow{}
	for _, r := range latest {
		if digest == "" || r.StrategyDigest == digest {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].At != rows[j].At {
			return rows[i].At < rows[j].At
		}
		return rows[i].ID < rows[j].ID
	})
	keys := []string{"committed", "returned", "fees", "forfeitures", "userCosts", "btcReceived", "btcPending", "satEarned", "satClaimed", "satSold", "btcSaleSOL", "satSaleSOL"}
	totals := map[string]*big.Int{}
	for _, k := range keys {
		totals[k] = new(big.Int)
	}
	entries, unsettled, reverted := 0, 0, 0
	overlays := map[string]string{}
	for _, r := range rows {
		if r.Finality == "reverted" {
			reverted++
			continue
		}
		if r.Finality != "finalized" {
			unsettled++
			continue
		}
		if r.Category != "mining" {
			n := new(big.Int)
			if old, ok := overlays[r.Category]; ok {
				n.SetString(old, 10)
			}
			v, _ := new(big.Int).SetString(r.Returned, 10)
			overlays[r.Category] = n.Add(n, v).String()
			continue
		}
		entries++
		values := []string{r.Committed, r.Returned, r.Fees, r.Forfeitures, r.UserCosts, r.BTCReceived, r.BTCPending, r.SATEarned, r.SATClaimed, r.SATSold, r.BTCSaleSOL, r.SATSaleSOL}
		for i, v := range values {
			n, _ := new(big.Int).SetString(v, 10)
			totals[keys[i]].Add(totals[keys[i]], n)
		}
	}
	result := map[string]any{"entries": entries, "unsettled": unsettled, "overlays": overlays}
	for _, k := range keys {
		result[k] = totals[k].String()
	}
	net := new(big.Int).Sub(totals["returned"], totals["committed"])
	net.Sub(net, totals["userCosts"]).Add(net, totals["btcSaleSOL"]).Add(net, totals["satSaleSOL"])
	result["netSOL"] = net.String()
	return result, len(rows), reverted
}

func (s *Store) savedWENChatText(ctx context.Context, r snapshotReader, selection WENSavedChatSelection, workspaceRevision int64, prepared string) (string, string, error) {
	bad := errors.New("select a retained strategy/history in this profile and reload the library")
	if selection.Profile == "" || selection.Revision < 0 || (selection.StrategyDigest == "" && selection.BundleIndex == nil) {
		return "", "", bad
	}
	d, revision, err := readAgents(ctx, r)
	if err != nil {
		return "", "", err
	}
	if revision != workspaceRevision {
		return "", "", ErrConflict
	}
	if agentProfile(d, selection.Profile) < 0 {
		return "", "", bad
	}
	all, libraryRevision, err := readWENLibraries(ctx, r)
	if err != nil {
		return "", "", err
	}
	if selection.Revision != libraryRevision {
		return "", "", ErrConflict
	}
	library := libraryFor(all, selection.Profile, libraryRevision)
	p := savedChatProjection{Schema: "wen-saved-chat-context/1", WorkspaceRevision: workspaceRevision, PreparedAt: prepared, Selection: selection}
	title := "History"
	if selection.StrategyDigest != "" {
		for _, v := range library.Versions {
			if v.Digest != selection.StrategyDigest {
				continue
			}
			a, err := wenstrategy.Decode([]byte(v.Raw))
			if err != nil || a.Digest != v.Digest || a.Document.Execution.Mode != "review" || a.Document.Execution.MandateID != nil {
				return "", "", bad
			}
			p.Strategy = &savedChatStrategy{v.Digest, v.CreatedAt, v.Origin, a.Document}
			title = "Strategy: " + a.Document.Name
			break
		}
		if p.Strategy == nil {
			return "", "", bad
		}
	}
	if selection.BundleIndex != nil {
		i := *selection.BundleIndex
		if i < 0 || i >= len(library.Bundles) {
			return "", "", bad
		}
		h := library.Bundles[i]
		raw, err := json.Marshal(h)
		if err != nil {
			return "", "", err
		}
		if _, err = wenstrategy.ParseHistory(raw); err != nil {
			return "", "", err
		}
		summary, relevant, reverted := savedHistorySummary(h, selection.StrategyDigest)
		if selection.StrategyDigest != "" && relevant == 0 {
			return "", "", errors.New("selected history does not contain this version")
		}
		p.History = &savedChatHistory{wenHash(raw), h.Source, false, h.Scope, h.InitialBankroll, h.Skipped, h.PendingExposure, h.Gaps, len(h.Rows), relevant, reverted, summary}
		if p.Strategy == nil {
			title = "History: " + h.Scope.Dataset
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", "", err
	}
	text := savedChatPrefix + string(raw)
	text += "\nSnapshot SHA-256: " + wenHash([]byte(text))
	if !validAgentText(text, 4000, true) {
		return "", "", errors.New("selected context exceeds 4,000 bytes; review strategy or history separately or import an explicitly smaller scope; no records were omitted or saved")
	}
	for len(title) > 80 {
		runes := []rune(title)
		title = string(runes[:len(runes)-1])
	}
	return text, title, nil
}

func (h *HTTPHandler) wenSavedChatReviewAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, 400, "unexpected query")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	var selection WENSavedChatSelection
	if err != nil || update.StrictJSON(raw, &selection) != nil {
		writeError(w, 400, "invalid saved-context selection")
		return
	}
	tx, err := h.store.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 400, "private records unavailable")
		return
	}
	defer tx.Rollback()
	_, revision, err := readAgents(r.Context(), tx)
	if err != nil {
		writeError(w, 400, "private records unavailable")
		return
	}
	now := h.store.now()
	text, _, err := h.store.savedWENChatText(r.Context(), tx, selection, revision, now.UTC().Format(time.RFC3339Nano))
	if errors.Is(err, ErrConflict) {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	review := WENResearchChatReview{Schema: "wen-research-chat-review/1", ProfileID: selection.Profile, Revision: revision, ExpiresAt: now.Add(120 * time.Second).Unix(), Text: text}
	review.Source = h.researchChatSignature(review.ProfileID, revision, review.ExpiresAt, text)
	writeJSON(w, 200, review)
}
