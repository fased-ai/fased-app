package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

const wenReadLimit = 65536
const wenReadResultLimit = 4000

type WENReadSource struct {
	RefID              string `json:"refId"`
	Kind               string `json:"kind"`
	MessageID          string `json:"messageId,omitempty"`
	TextSHA256         string `json:"textSHA256,omitempty"`
	Digest             string `json:"digest,omitempty"`
	BundleIndex        *int   `json:"bundleIndex,omitempty"`
	StoredBundleSHA256 string `json:"storedBundleSHA256,omitempty"`
}
type WENReadCall struct {
	ID          string   `json:"id"`
	Tool        string   `json:"tool"`
	SourceRefs  []string `json:"sourceRefs"`
	StrategyRef string   `json:"strategyRef,omitempty"`
}
type WENResearchReadInput struct {
	Schema          string          `json:"schema"`
	Profile         string          `json:"profile"`
	SessionID       string          `json:"sessionId"`
	Revision        int64           `json:"revision"`
	LibraryRevision int64           `json:"libraryRevision"`
	Sources         []WENReadSource `json:"sources"`
	Calls           []WENReadCall   `json:"calls"`
}
type WENReadProvenance struct {
	Selector    WENReadSource             `json:"selector"`
	SHA256      string                    `json:"sha256"`
	CapturedAt  *string                   `json:"capturedAt"`
	ObservedAt  *string                   `json:"observedAt"`
	RetrievedAt *string                   `json:"retrievedAt"`
	TimeReason  string                    `json:"timeReason"`
	Evidence    string                    `json:"evidence"`
	Verified    bool                      `json:"verified"`
	Freshness   string                    `json:"freshness"`
	Scope       *wenstrategy.HistoryScope `json:"scope,omitempty"`
	Gaps        []string                  `json:"gaps,omitempty"`
}
type WENReadResult struct {
	ID         string              `json:"id"`
	Tool       string              `json:"tool"`
	SourceRefs []string            `json:"sourceRefs"`
	Sources    []WENReadProvenance `json:"sources"`
	Status     string              `json:"status"`
	Data       any                 `json:"data,omitempty"`
	Error      string              `json:"error,omitempty"`
	ElapsedMS  int64               `json:"elapsedMs"`
}
type WENReadUsage struct {
	Attempted int   `json:"attempted"`
	Completed int   `json:"completed"`
	Failed    int   `json:"failed"`
	NotRun    int   `json:"notRun"`
	ElapsedMS int64 `json:"elapsedMs"`
}
type WENResearchReadResult struct {
	Schema          string              `json:"schema"`
	Profile         string              `json:"profile"`
	SessionID       string              `json:"sessionId"`
	PreparedAt      string              `json:"preparedAt"`
	Revision        int64               `json:"revision"`
	LibraryRevision int64               `json:"libraryRevision"`
	Sources         []WENReadProvenance `json:"sources"`
	Results         []WENReadResult     `json:"results"`
	Usage           WENReadUsage        `json:"usage"`
	Status          string              `json:"status"`
	ResultSHA256    string              `json:"resultSHA256,omitempty"`
}
type wenReadError struct {
	status int
	code   string
}

func (e wenReadError) Error() string { return e.code }
func readRefValid(s string) bool {
	return validAgentText(s, 80, true) && !strings.ContainsAny(s, "/\\*\n\r\t")
}

// DecodeWENResearchRead is also used by the CLI. This selects records, never authority.
func DecodeWENResearchRead(raw []byte) (WENResearchReadInput, error) {
	var c WENResearchReadInput
	bad := errors.New("invalid selected research-read request")
	if update.StrictJSON(raw, &c) != nil {
		return c, bad
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 7 {
		return c, bad
	}
	var value any
	_ = json.Unmarshal(raw, &value)
	var nonnull func(any) bool
	nonnull = func(v any) bool {
		if v == nil {
			return false
		}
		switch x := v.(type) {
		case map[string]any:
			for _, n := range x {
				if !nonnull(n) {
					return false
				}
			}
		case []any:
			for _, n := range x {
				if !nonnull(n) {
					return false
				}
			}
		}
		return true
	}
	if !nonnull(value) || !validWENRead(c) {
		return c, bad
	}
	return c, nil
}
func validWENRead(c WENResearchReadInput) bool {
	if c.Schema != "wen-research-read/1" || !readRefValid(c.Profile) || !readRefValid(c.SessionID) || c.Revision < 0 || c.LibraryRevision < 0 || len(c.Sources) < 1 || len(c.Sources) > 8 || len(c.Calls) < 1 || len(c.Calls) > 8 {
		return false
	}
	sources := map[string]WENReadSource{}
	identities := map[string]bool{}
	for _, s := range c.Sources {
		if !readRefValid(s.RefID) {
			return false
		}
		if _, ok := sources[s.RefID]; ok {
			return false
		}
		identity := ""
		switch s.Kind {
		case "research-message":
			if !readRefValid(s.MessageID) || !researchDigest.MatchString(s.TextSHA256) || s.Digest != "" || s.BundleIndex != nil || s.StoredBundleSHA256 != "" {
				return false
			}
			identity = s.Kind + ":" + s.MessageID
		case "strategy":
			if !researchDigest.MatchString(s.Digest) || s.MessageID != "" || s.TextSHA256 != "" || s.BundleIndex != nil || s.StoredBundleSHA256 != "" {
				return false
			}
			identity = s.Kind + ":" + s.Digest
		case "history":
			if s.BundleIndex == nil || *s.BundleIndex < 0 || !researchDigest.MatchString(s.StoredBundleSHA256) || s.Digest != "" || s.MessageID != "" || s.TextSHA256 != "" {
				return false
			}
			identity = s.Kind + ":" + s.StoredBundleSHA256
		default:
			return false
		}
		if identities[identity] {
			return false
		}
		identities[identity] = true
		sources[s.RefID] = s
	}
	used := map[string]bool{}
	ids := map[string]bool{}
	for _, call := range c.Calls {
		if !readRefValid(call.ID) || ids[call.ID] {
			return false
		}
		ids[call.ID] = true
		want := ""
		min, max := 1, 1
		switch call.Tool {
		case "wen.research.snapshot":
			want = "research-message"
		case "wen.strategy.inspect":
			want = "strategy"
		case "wen.history.summary":
			want = "history"
		case "wen.strategy.compare":
			want = "strategy"
			min, max = 2, 4
		default:
			return false
		}
		if len(call.SourceRefs) < min || len(call.SourceRefs) > max || (call.StrategyRef != "" && call.Tool != "wen.history.summary") {
			return false
		}
		seen := map[string]bool{}
		for _, id := range call.SourceRefs {
			s, ok := sources[id]
			if !ok || s.Kind != want || seen[id] {
				return false
			}
			seen[id] = true
			used[id] = true
		}
		if call.StrategyRef != "" {
			s, ok := sources[call.StrategyRef]
			if !ok || s.Kind != "strategy" {
				return false
			}
			used[call.StrategyRef] = true
		}
	}
	return len(used) == len(sources)
}

type wenReadResolved struct {
	provenance WENReadProvenance
	message    AgentMessage
	version    WENVersion
	strategy   *wenstrategy.Accepted
	history    wenstrategy.ImportedHistory
}

func recordedTime(v string) *string {
	if _, e := time.Parse(time.RFC3339Nano, v); e != nil {
		return nil
	}
	return &v
}

func resolveWENRead(ctx context.Context, r snapshotReader, c WENResearchReadInput) ([]WENReadProvenance, map[string]wenReadResolved, error) {
	unavailable := wenReadError{404, "source_unavailable"}
	d, revision, err := readAgents(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	profiles, sessions := 0, 0
	for _, p := range d.Profiles {
		if p.ID == c.Profile {
			profiles++
		}
	}
	for _, s := range d.Sessions {
		if s.ID == c.SessionID {
			sessions++
			if s.ProfileID != c.Profile {
				return nil, nil, unavailable
			}
		}
	}
	if profiles != 1 || sessions != 1 {
		return nil, nil, unavailable
	}
	all, libRevision, err := readWENLibraries(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	if revision != c.Revision || libRevision != c.LibraryRevision {
		return nil, nil, wenReadError{409, "source_changed"}
	}
	library := libraryFor(all, c.Profile, libRevision)
	out := []WENReadProvenance{}
	resolved := map[string]wenReadResolved{}
	for _, s := range c.Sources {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		v := wenReadResolved{provenance: WENReadProvenance{Selector: s, Evidence: "owner-record", Freshness: "historical", TimeReason: "Observation/retrieval time not retained; read time does not refresh this source."}}
		switch s.Kind {
		case "research-message":
			count := 0
			for _, m := range d.Messages {
				if m.ID == s.MessageID {
					count++
					v.message = m
				}
			}
			m := v.message
			if count != 1 || m.ProfileID != c.Profile || m.SessionID != c.SessionID || m.Role != "user" || !validAgentText(m.Text, 4000, true) {
				return nil, nil, unavailable
			}
			v.provenance.SHA256 = wenHash([]byte(m.Text))
			if v.provenance.SHA256 != s.TextSHA256 {
				return nil, nil, wenReadError{409, "source_changed"}
			}
			if m.CreatedAt > 0 {
				stamp := time.Unix(m.CreatedAt, 0).UTC().Format(time.RFC3339)
				v.provenance.CapturedAt = &stamp
			}
			// The exact text retains declared timestamps; it cannot authenticate a venue.
			v.provenance.Evidence = "unverified-saved-message"
			lines := strings.Split(m.Text, "\n")
			if len(lines) > 1 {
				var captured struct {
					Schema   string `json:"schema"`
					Event    string `json:"quoteEventAt"`
					Received string `json:"quoteReceivedAt"`
				}
				if json.Unmarshal([]byte(lines[1]), &captured) == nil && captured.Schema == "wen-research-chat-context/1" {
					v.provenance.ObservedAt = recordedTime(captured.Event)
					v.provenance.RetrievedAt = recordedTime(captured.Received)
					v.provenance.TimeReason = "Declared timestamps from unverified saved text; they are not refreshed or authenticated by this read."
				}
			}
		case "strategy":
			v.version, v.strategy, err = wenRevisionParent(library, s.Digest)
			if err != nil {
				return nil, nil, unavailable
			}
			v.provenance.SHA256 = s.Digest
			v.provenance.CapturedAt = recordedTime(v.version.CreatedAt)
			v.provenance.Evidence = "validated-owner-strategy"
		case "history":
			if *s.BundleIndex >= len(library.Bundles) {
				return nil, nil, unavailable
			}
			v.history = library.Bundles[*s.BundleIndex]
			raw, e := json.Marshal(v.history)
			if e != nil {
				return nil, nil, unavailable
			}
			if _, e = wenstrategy.ParseHistory(raw); e != nil {
				return nil, nil, unavailable
			}
			v.provenance.SHA256 = wenHash(raw)
			if v.provenance.SHA256 != s.StoredBundleSHA256 {
				return nil, nil, wenReadError{409, "source_changed"}
			}
			scope := v.history.Scope
			v.provenance.Scope = &scope
			v.provenance.Gaps = v.history.Gaps
			v.provenance.Evidence = scope.Evidence
			v.provenance.TimeReason = "Declared import period is scope, not an authenticated observation/retrieval time; original upload bytes/time are not retained."
		}
		out = append(out, v.provenance)
		resolved[s.RefID] = v
	}
	return out, resolved, nil
}
func projectWENRead(ctx context.Context, call WENReadCall, sources map[string]wenReadResolved) (any, string) {
	if ctx.Err() != nil {
		return nil, "cancelled"
	}
	v := sources[call.SourceRefs[0]]
	switch call.Tool {
	case "wen.research.snapshot":
		return v.message, ""
	case "wen.strategy.inspect":
		d := v.strategy.Document
		amounts, e := wenstrategy.CompileAllocation(d.Limits.PerRoundLamports, d.Allocation.WeightsBps)
		if e != nil || len(d.Allocation.Rules) > 0 {
			return nil, "unsupported"
		}
		return struct {
			Strategy savedChatStrategy `json:"strategy"`
			Amounts  [5]string         `json:"amountsLamports"`
		}{savedChatStrategy{v.version.Digest, v.version.CreatedAt, v.version.Origin, d}, amounts}, ""
	case "wen.history.summary":
		digest := ""
		if call.StrategyRef != "" {
			digest = sources[call.StrategyRef].version.Digest
		}
		summary, relevant, reverted := savedHistorySummary(v.history, digest)
		if digest != "" && relevant == 0 {
			return nil, "source_unavailable"
		}
		h := v.history
		return savedChatHistory{v.provenance.SHA256, h.Source, false, h.Scope, h.InitialBankroll, h.Skipped, h.PendingExposure, h.Gaps, len(h.Rows), relevant, reverted, summary}, ""
	case "wen.strategy.compare":
		type settings struct {
			Digest    string             `json:"digest"`
			Name      string             `json:"name"`
			Market    wenstrategy.Market `json:"market"`
			Horizon   int                `json:"horizonMinutes"`
			Weights   []int              `json:"weightsBps"`
			Amounts   [5]string          `json:"amountsLamports"`
			Cadence   int                `json:"everyNthRound"`
			Turnover  string             `json:"totalTurnoverLamports"`
			Unsettled string             `json:"maxUnsettledLamports"`
			MaxRounds int                `json:"maxRoundsTotal"`
		}
		items := []settings{}
		for _, ref := range call.SourceRefs {
			s := sources[ref]
			d := s.strategy.Document
			amounts, e := wenstrategy.CompileAllocation(d.Limits.PerRoundLamports, d.Allocation.WeightsBps)
			if e != nil || len(d.Allocation.Rules) > 0 {
				return nil, "unsupported"
			}
			items = append(items, settings{s.version.Digest, d.Name, d.Market, d.Research.HorizonMinutes, d.Allocation.WeightsBps, amounts, d.Schedule.EveryNthRound, d.Limits.TotalTurnoverLamports, d.Limits.MaxUnsettledLamports, d.Schedule.MaxRoundsTotal})
		}
		return struct {
			Kind             string     `json:"kind"`
			Items            []settings `json:"items"`
			FinancialPreview any        `json:"financialPreview"`
			Reason           string     `json:"reason"`
		}{"settings-only", items, nil, "No participation-backed financial replay supplied; mismatched settings are descriptive, not ranked performance."}, ""
	}
	return nil, "unsupported"
}
func readElapsed(start, end time.Time) int64 {
	n := end.Sub(start).Milliseconds()
	if n < 0 {
		return 0
	}
	return n
}

// The clock/project arguments are internal pure-test seams, not runtime configuration.
func runWENRead(ctx context.Context, c WENResearchReadInput, result WENResearchReadResult, sources map[string]wenReadResolved, start time.Time, clock func() time.Time, project func(context.Context, WENReadCall, map[string]wenReadResolved) (any, string)) WENResearchReadResult {
	result.Status = "complete"
	result.Results = []WENReadResult{}
	for _, call := range c.Calls {
		item := WENReadResult{ID: call.ID, Tool: call.Tool, SourceRefs: call.SourceRefs, Sources: []WENReadProvenance{}, Status: "not_run"}
		for _, ref := range call.SourceRefs {
			item.Sources = append(item.Sources, sources[ref].provenance)
		}
		if call.StrategyRef != "" {
			item.Sources = append(item.Sources, sources[call.StrategyRef].provenance)
		}
		now := clock()
		reason := ""
		if ctx.Err() != nil {
			reason = "cancelled"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				reason = "deadline"
			}
		} else if now.Sub(start) >= 90*time.Second {
			reason = "deadline"
		}
		if reason != "" {
			item.Error = reason
			result.Usage.NotRun++
			if reason == "cancelled" {
				result.Status = "cancelled"
			} else if result.Status != "cancelled" {
				result.Status = "partial"
			}
		} else {
			result.Usage.Attempted++
			callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			data, failure := project(callCtx, call, sources)
			end := clock()
			item.ElapsedMS = readElapsed(now, end)
			if ctx.Err() != nil {
				failure = "cancelled"
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					failure = "deadline"
				}
			} else if callCtx.Err() != nil || end.Sub(now) >= 3*time.Second || end.Sub(start) >= 90*time.Second {
				failure = "deadline"
			}
			cancel()
			item.Status = "ok"
			item.Data = data
			if failure != "" {
				item.Status = "error"
				item.Error = failure
				item.Data = nil
			}
			raw, e := json.Marshal(item)
			if e != nil || len(raw) > wenReadResultLimit {
				item.Status = "error"
				item.Error = "too_large"
				item.Data = nil
			}
			if item.Status == "ok" {
				result.Usage.Completed++
			} else {
				result.Usage.Failed++
				if item.Error == "cancelled" {
					result.Status = "cancelled"
				} else if result.Status != "cancelled" {
					result.Status = "partial"
				}
			}
		}
		result.Results = append(result.Results, item)
	}
	result.Usage.ElapsedMS = readElapsed(start, clock())
	return result
}

func (s *Store) WENResearchRead(ctx context.Context, c WENResearchReadInput) (WENResearchReadResult, error) {
	var result WENResearchReadResult
	if !validWENRead(c) {
		return result, wenReadError{400, "invalid_selection"}
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	provenance, resolved, err := resolveWENRead(ctx, tx, c)
	if err != nil {
		return result, err
	}
	result = WENResearchReadResult{Schema: "wen-research-result/1", Profile: c.Profile, SessionID: c.SessionID, PreparedAt: s.now().UTC().Format(time.RFC3339Nano), Revision: c.Revision, LibraryRevision: c.LibraryRevision, Sources: provenance, Results: []WENReadResult{}, Status: "cancelled", ResultSHA256: strings.Repeat("0", 64)}
	// Reserve the full worst-case response before evaluating a single tool.
	meta, _ := json.Marshal(result)
	if len(meta)+len(c.Calls)*(wenReadResultLimit+1)+256+1 > wenReadLimit {
		return WENResearchReadResult{}, wenReadError{413, "too_large"}
	}
	// Per-call provenance alone must leave room for a bounded error result.
	for _, call := range c.Calls {
		p := []WENReadProvenance{}
		for _, ref := range call.SourceRefs {
			p = append(p, resolved[ref].provenance)
		}
		if call.StrategyRef != "" {
			p = append(p, resolved[call.StrategyRef].provenance)
		}
		b, _ := json.Marshal(WENReadResult{ID: call.ID, Tool: call.Tool, SourceRefs: call.SourceRefs, Sources: p, Status: "not_run", Error: "source_unavailable", ElapsedMS: 9223372036854775807})
		if len(b) > wenReadResultLimit {
			return WENResearchReadResult{}, wenReadError{413, "too_large"}
		}
	}
	if err = tx.Rollback(); err != nil {
		return WENResearchReadResult{}, err
	}
	result = runWENRead(ctx, c, result, resolved, start, time.Now, projectWENRead)
	result.ResultSHA256 = ""
	raw, err := json.Marshal(result)
	if err != nil {
		return WENResearchReadResult{}, err
	}
	result.ResultSHA256 = wenHash(raw)
	raw, err = json.Marshal(result)
	if err != nil || len(raw)+1 > wenReadLimit {
		return WENResearchReadResult{}, wenReadError{413, "too_large"}
	}
	return result, nil
}
func (h *HTTPHandler) wenResearchReadAPI(w http.ResponseWriter, r *http.Request, token string) {
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
	r.Body = http.MaxBytesReader(w, r.Body, wenReadLimit)
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 413, "too_large")
		return
	}
	c, err := DecodeWENResearchRead(raw)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	result, err := h.store.WENResearchRead(r.Context(), c)
	if err != nil {
		var failure wenReadError
		if errors.As(err, &failure) {
			writeError(w, failure.status, failure.code)
		} else {
			writeError(w, 503, "research_read_unavailable")
		}
		return
	}
	writeJSON(w, 200, result)
}
