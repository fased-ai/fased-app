package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const agentEvidenceLimit = 16 * 1024

// AgentEvidenceView is a transient read projection. Its hash identifies source
// data, never consent or permission. Time/freshness describe this inspection.
type AgentEvidenceView struct {
	Version    string          `json:"version"`
	Revision   int64           `json:"revision"`
	ProfileID  string          `json:"profileId"`
	Kind       string          `json:"kind"`
	ID         string          `json:"id"`
	ItemID     string          `json:"itemId,omitempty"`
	SHA256     string          `json:"sha256"`
	ObservedAt int64           `json:"observedAt"`
	RecordedAt int64           `json:"recordedAt"`
	Historical bool            `json:"historical"`
	Untrusted  bool            `json:"untrusted"`
	Freshness  string          `json:"freshness"`
	Data       json.RawMessage `json:"data"`
}

func (s *Store) AgentEvidence(ctx context.Context, profile, kind, id, item string) (AgentEvidenceView, error) {
	d, revision, err := readAgents(ctx, s.db)
	if err != nil {
		return AgentEvidenceView{}, err
	}
	return inspectAgentEvidence(d, revision, profile, kind, id, item, s.now().Unix())
}

func inspectAgentEvidence(d agentDocument, revision int64, profile, kind, id, item string, now int64) (AgentEvidenceView, error) {
	var empty AgentEvidenceView
	for _, v := range []string{profile, id} {
		if !validAgentText(v, 200, true) || !utf8.ValidString(v) {
			return empty, errors.New("one profile and source identity required")
		}
	}
	if kind != "memory" && kind != "headline" && kind != "reminder-run" {
		return empty, errors.New("select memory, headline or reminder-run")
	}
	if (kind == "headline" && (!validAgentText(item, 200, true) || !utf8.ValidString(item))) || (kind != "headline" && item != "") {
		return empty, errors.New("item identity is required only for a headline")
	}
	profiles := 0
	for _, p := range d.Profiles {
		if p.ID == profile {
			profiles++
		}
	}
	if profiles != 1 || now <= 0 {
		return empty, errors.New("profile or inspection clock unavailable")
	}
	v := AgentEvidenceView{Version: "agent-evidence-v1", Revision: revision, ProfileID: profile, Kind: kind, ID: id, ItemID: item, ObservedAt: now, Untrusted: true}
	var data any
	switch kind {
	case "memory":
		var note *AgentMemory
		for i := range d.Memories {
			if d.Memories[i].ID == id {
				if note != nil {
					return empty, errors.New("memory source ambiguous")
				}
				note = &d.Memories[i]
			}
		}
		if note == nil || note.ProfileID != profile || !validAgentText(note.Text, 4000, true) || !validAgentText(note.Source, 300, true) || note.UpdatedAt <= 0 || note.UpdatedAt > now || note.ExpiresAt < 0 {
			return empty, errors.New("memory source unavailable or invalid")
		}
		provenance := inspectMemorySource(d, *note)
		if provenance.Status != "free-form" && provenance.Status != "matched" {
			return empty, errors.New("memory provenance unavailable, changed or ambiguous")
		}
		data = struct {
			ID         string              `json:"id"`
			Text       string              `json:"text"`
			Source     string              `json:"source"`
			UpdatedAt  int64               `json:"updatedAt"`
			ExpiresAt  int64               `json:"expiresAt"`
			Provenance AgentMemoryEvidence `json:"provenance"`
		}{note.ID, note.Text, note.Source, note.UpdatedAt, note.ExpiresAt, provenance}
		v.RecordedAt = note.UpdatedAt
		v.Freshness = "active"
		if note.ExpiresAt != 0 && note.ExpiresAt <= now {
			v.Freshness = "expired"
		}
	case "headline":
		f, source, headline, err := retainedNewsItem(d, profile, id, item, now)
		if err != nil {
			return empty, err
		}
		data = struct {
			SourceName     string        `json:"sourceName"`
			SourceDisabled bool          `json:"sourceDisabled"`
			Headline       NewsItem      `json:"headline"`
			Evidence       *NewsEvidence `json:"evidence"`
		}{source.Name, source.Disabled, *headline, newsEvidence(*f)}
		v.RecordedAt = f.FetchedAt
		v.Historical = true
		v.Freshness = "fresh"
		if source.Disabled {
			v.Freshness = "disabled"
		} else if now-f.FetchedAt > f.MaxAgeSeconds || now-headline.PublishedAt > f.MaxAgeSeconds {
			v.Freshness = "stale"
		}
	case "reminder-run":
		r, task, _, err := retainedAgentRun(d, profile, id, now)
		if err != nil {
			return empty, err
		}
		if r.Strategy != "reminder-v1" || task.News != nil || r.News != nil {
			return empty, errors.New("select one plain reminder run without AI, news, mining or trading bindings")
		}
		type reminder struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Text string `json:"text"`
		}
		type run struct {
			ID       string `json:"id"`
			At       int64  `json:"at"`
			Strategy string `json:"strategy"`
			Outcome  string `json:"outcome"`
		}
		data = struct {
			Task reminder `json:"task"`
			Run  run      `json:"run"`
		}{reminder{task.ID, task.Name, task.Text}, run{r.ID, r.At, r.Strategy, r.Outcome}}
		v.RecordedAt = r.At
		v.Historical = true
		v.Freshness = "recorded"
	}
	var err error
	v.Data, err = json.Marshal(data)
	if err != nil {
		return empty, err
	}
	return finishAgentEvidence(v)
}

func finishAgentEvidence(v AgentEvidenceView) (AgentEvidenceView, error) {
	source, err := json.Marshal(struct {
		Version, ProfileID, Kind, ID, ItemID string
		Data                                 json.RawMessage
	}{v.Version, v.ProfileID, v.Kind, v.ID, v.ItemID, v.Data})
	if err != nil {
		return AgentEvidenceView{}, err
	}
	sum := sha256.Sum256(source)
	v.SHA256 = hex.EncodeToString(sum[:])
	encoded, err := json.Marshal(v)
	if err != nil {
		return AgentEvidenceView{}, err
	}
	// Both HTTP and CLI JSON encoders append a newline.
	if len(encoded)+1 > agentEvidenceLimit {
		return AgentEvidenceView{}, errors.New("complete evidence exceeds 16 KiB; nothing truncated or returned")
	}
	return v, nil
}

type recordedAgentAI struct {
	ID, SessionID, Digest, State, Model, Answer                                          string
	Limits                                                                               AILimits
	CreatedAt, InputTokens, OutputTokens, CachedInputTokens, SpentMicrousd, HeldMicrousd int64
}

func retainedAgentRun(d agentDocument, profile, runID string, now int64) (*AgentRun, *AgentTask, *recordedAgentAI, error) {
	refused := errors.New("saved reminder run references missing, ambiguous, changed or unsupported")
	var r *AgentRun
	for i := range d.Runs {
		if d.Runs[i].ID == runID {
			if r != nil {
				return nil, nil, nil, refused
			}
			r = &d.Runs[i]
		}
	}
	if r == nil || r.ProfileID != profile || r.WEN != nil || r.BotID != "" || r.Quantity != 0 || r.PriceCents != 0 || r.CostCents != 0 || r.BudgetBefore != 0 || r.At <= 0 || r.At > now {
		return nil, nil, nil, refused
	}
	var task *AgentTask
	for i := range d.Tasks {
		if d.Tasks[i].ID == r.TaskID {
			if task != nil {
				return nil, nil, nil, refused
			}
			task = &d.Tasks[i]
		}
	}
	if task == nil || task.ProfileID != profile || task.WEN != nil || task.BotID != "" {
		return nil, nil, nil, refused
	}
	n, err := strconv.Atoi(strings.TrimPrefix(r.ID, task.ID+":"))
	if err != nil || n < 1 || n > task.Runs || r.ID != fmt.Sprintf("%s:%d", task.ID, n) {
		return nil, nil, nil, refused
	}
	plain := r.Strategy == "reminder-v1"
	success := r.Strategy == "ai-step-v1/reminder-v1"
	held := r.Strategy == "ai-step-v1" && strings.HasPrefix(r.Outcome, "ai-")
	if (!plain && !success && !held) || ((plain || success) && r.Outcome != "reminded") {
		return nil, nil, nil, refused
	}
	expectedNotice := task.Name + ": " + task.Text
	if held {
		expectedNotice = task.Name + ": " + r.Outcome + "; dependent step stopped. Review a new task plan."
	}
	var notice *AgentNotice
	for i := range d.Notices {
		if d.Notices[i].RunID == runID || d.Notices[i].ID == runID {
			if notice != nil {
				return nil, nil, nil, refused
			}
			notice = &d.Notices[i]
		}
	}
	if notice == nil || notice.ID != runID || notice.RunID != runID || notice.ProfileID != profile || notice.At != r.At || notice.Text != expectedNotice {
		return nil, nil, nil, refused
	}
	// Whitelist public historical fields; never serialize TaskAIStep or the
	// request itself (both contain private connection/context/disclosure data).
	var ai *recordedAgentAI
	if plain {
		if task.AI != nil || r.AIRequestID != "" || r.AIDigest != "" {
			return nil, nil, nil, refused
		}
	} else {
		if task.AI == nil || r.AIDigest == "" || r.AIDigest != task.AI.Digest || r.AIDigest != taskAIDigest(*task) {
			return nil, nil, nil, refused
		}
		if r.AIRequestID == "" {
			if !held {
				return nil, nil, nil, refused
			}
		} else {
			var q *AgentAIRequest
			for i := range d.AIRequests {
				if d.AIRequests[i].ID == r.AIRequestID {
					if q != nil {
						return nil, nil, nil, refused
					}
					q = &d.AIRequests[i]
				}
			}
			if q == nil || q.ProfileID != profile || q.TaskID != task.ID || q.TaskDigest != r.AIDigest || q.Prompt != task.Text || q.Digest != aiDigest(*q) || q.CreatedAt > r.At {
				return nil, nil, nil, refused
			}
			switch q.State {
			case "completed", "failed", "cancelled", "unknown":
			default:
				return nil, nil, nil, refused
			}
			if success && q.State != "completed" {
				return nil, nil, nil, refused
			}
			sessions := 0
			for _, s := range d.Sessions {
				if s.ID == q.SessionID {
					if s.ProfileID != profile {
						return nil, nil, nil, refused
					}
					sessions++
				}
			}
			if sessions != 1 {
				return nil, nil, nil, refused
			}
			ai = &recordedAgentAI{ID: q.ID, SessionID: q.SessionID, Digest: q.Digest, State: q.State, Model: q.Model, Limits: q.Limits, CreatedAt: q.CreatedAt, InputTokens: q.InputTokens, OutputTokens: q.OutputTokens, CachedInputTokens: q.CachedInputTokens, SpentMicrousd: q.SpentMicrousd, HeldMicrousd: q.HeldMicrousd}
			if q.State == "completed" {
				ai.Answer = q.Answer
			}
		}
	}
	return r, task, ai, nil
}

func retainedNewsItem(d agentDocument, profile, fetchID, itemID string, now int64) (*NewsFetch, *NewsSource, *NewsItem, error) {
	refused := errors.New("saved news references missing, ambiguous, changed, unfinished or invalid")
	var f *NewsFetch
	for i := range d.NewsFetches {
		if d.NewsFetches[i].ID == fetchID {
			if f != nil {
				return nil, nil, nil, refused
			}
			f = &d.NewsFetches[i]
		}
	}
	if f == nil || f.ProfileID != profile || f.State != "completed" || f.FetchedAt <= 0 || f.FetchedAt > now || f.CreatedAt > f.FetchedAt || f.MaxAgeSeconds < 60 || f.MaxAgeSeconds > 86400 || f.Bytes < 1 || f.Bytes > 65536 || len(f.BodySHA256) != 64 {
		return nil, nil, nil, refused
	}
	for _, c := range f.BodySHA256 {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return nil, nil, nil, refused
		}
	}
	var source *NewsSource
	for i := range d.NewsSources {
		if d.NewsSources[i].ID == f.SourceID {
			if source != nil {
				return nil, nil, nil, refused
			}
			source = &d.NewsSources[i]
		}
	}
	if source == nil || source.ProfileID != profile || source.URL != f.URL || source.MaxAgeSeconds != f.MaxAgeSeconds || !validAgentText(source.Name, 80, true) {
		return nil, nil, nil, refused
	}
	if _, err := newsURL(f.URL); err != nil {
		return nil, nil, nil, refused
	} // syntax only; no DNS or network
	var item *NewsItem
	for i := range f.Items {
		if f.Items[i].ID == itemID {
			if item != nil {
				return nil, nil, nil, refused
			}
			item = &f.Items[i]
		}
	}
	if item == nil || !validAgentText(item.Title, 240, true) || item.PublishedAt <= 0 || item.PublishedAt > f.FetchedAt {
		return nil, nil, nil, refused
	}
	if _, err := newsURL(item.URL); err != nil {
		return nil, nil, nil, refused
	}
	original := *item
	original.ID = ""
	b, err := json.Marshal(original)
	if err != nil {
		return nil, nil, nil, err
	}
	if item.ID != newsHash(append([]byte(f.SourceID+":"), b...)) {
		return nil, nil, nil, refused
	}
	return f, source, item, nil
}
