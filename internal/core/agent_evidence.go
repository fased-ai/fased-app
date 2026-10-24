package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
