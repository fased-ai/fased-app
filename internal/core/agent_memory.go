package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// These are read projections, never stored records or AI context selectors.
type AgentMemoryView struct {
	Revision  int64                `json:"revision"`
	ProfileID string               `json:"profileId"`
	Memories  []AgentMemory        `json:"memories"`
	Source    *AgentMemoryEvidence `json:"source,omitempty"`
}

type AgentMemoryEvidence struct {
	MemoryID  string `json:"memoryId"`
	Status    string `json:"status"`
	Kind      string `json:"kind,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	ID        string `json:"id,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Text      string `json:"text,omitempty"`
}

// A transient, owner-reviewed correction using the existing memory-put writer.
// The workspace revision binds the selected history entry until explicit save.
type AgentMemoryRestoreDraft struct {
	Version      string              `json:"version"`
	Revision     int64               `json:"revision"`
	ProfileID    string              `json:"profileId"`
	MemoryID     string              `json:"memoryId"`
	HistoryIndex int                 `json:"historyIndex"`
	Current      MemoryRevision      `json:"current"`
	Selected     MemoryRevision      `json:"selected"`
	Evidence     AgentMemoryEvidence `json:"evidence"`
	Command      AgentCommand        `json:"command"`
}

func (s *Store) AgentMemoryRestore(ctx context.Context, profile, id string, index int) (AgentMemoryRestoreDraft, error) {
	if !validAgentText(profile, 80, true) || !utf8.ValidString(profile) || !validAgentText(id, 80, true) || !utf8.ValidString(id) || index < 0 || index >= 8 {
		return AgentMemoryRestoreDraft{}, errors.New("select one profile memory and retained revision (0–7)")
	}
	d, revision, err := readAgents(ctx, s.db)
	if err != nil {
		return AgentMemoryRestoreDraft{}, err
	}
	profiles, matches := 0, 0
	var selected *AgentMemory
	for _, p := range d.Profiles {
		if p.ID == profile {
			profiles++
		}
	}
	for i, m := range d.Memories {
		if m.ID == id {
			matches++
			if m.ProfileID == profile {
				selected = &d.Memories[i]
			}
		}
	}
	if profiles != 1 || matches != 1 || selected == nil || len(d.Memories) > 100 {
		return AgentMemoryRestoreDraft{}, errors.New("profile memory unavailable or ambiguous")
	}
	if len(selected.History) > 8 || selected.HistoryOffset < 0 {
		return AgentMemoryRestoreDraft{}, errors.New("unsupported memory history; preserve records")
	}
	if index >= len(selected.History) {
		return AgentMemoryRestoreDraft{}, errors.New("retained memory revision unavailable")
	}
	prior := selected.History[index]
	current := MemoryRevision{Text: selected.Text, Source: selected.Source, At: selected.UpdatedAt, ExpiresAt: selected.ExpiresAt}
	for _, record := range []MemoryRevision{prior, current} {
		if !validAgentText(record.Text, 4000, true) || !utf8.ValidString(record.Text) || !validAgentText(record.Source, 300, true) || !utf8.ValidString(record.Source) || record.At < 0 || record.ExpiresAt < 0 {
			return AgentMemoryRestoreDraft{}, errors.New("memory revision invalid; preserve state")
		}
	}
	priorMemory := *selected
	priorMemory.Source = prior.Source
	if err := hydrateMemorySource(ctx, s.db, &d, priorMemory); err != nil {
		return AgentMemoryRestoreDraft{}, err
	}
	return AgentMemoryRestoreDraft{
		Version: "memory-restore-draft-v1", Revision: revision, ProfileID: profile, MemoryID: id, HistoryIndex: index,
		Current: current, Selected: prior, Evidence: inspectMemorySource(d, priorMemory),
		Command: AgentCommand{Action: "memory-put", Revision: revision, ProfileID: profile, ID: id, Text: prior.Text, Source: prior.Source, ExpiresAt: prior.ExpiresAt},
	}, nil
}

func (s *Store) AgentMemories(ctx context.Context, profile, query, id string) (AgentMemoryView, error) {
	if !validAgentText(query, 200, false) || !utf8.ValidString(query) || !validAgentText(id, 80, false) || !utf8.ValidString(id) {
		return AgentMemoryView{}, errors.New("memory search or identity is invalid or too long")
	}
	d, revision, err := readAgents(ctx, s.db)
	if err != nil {
		return AgentMemoryView{}, err
	}
	profiles := 0
	for _, p := range d.Profiles {
		if p.ID == profile {
			profiles++
		}
	}
	if profile == "" || profiles != 1 || len(d.Memories) > 100 {
		return AgentMemoryView{}, errors.New("select one available profile; preserve state")
	}
	v := AgentMemoryView{Revision: revision, ProfileID: profile, Memories: []AgentMemory{}}
	query = strings.ToLower(strings.TrimSpace(query))
	var selected *AgentMemory
	matches := 0
	now := s.now().Unix()
	for _, m := range d.Memories {
		if id != "" && m.ID == id {
			matches++
			if m.ProfileID == profile {
				copy := m
				selected = &copy
			}
		}
		if m.ProfileID == profile && (query == "" || strings.Contains(strings.ToLower(m.Text), query) || strings.Contains(strings.ToLower(m.Source), query)) {
			m.Expired = m.ExpiresAt > 0 && m.ExpiresAt <= now
			v.Memories = append(v.Memories, m)
		}
	}
	if id != "" {
		if matches != 1 || selected == nil {
			return AgentMemoryView{}, errors.New("profile memory unavailable or ambiguous")
		}
		if err := hydrateMemorySource(ctx, s.db, &d, *selected); err != nil {
			return AgentMemoryView{}, err
		}
		evidence := inspectMemorySource(d, *selected)
		v.Source = &evidence
	}
	return v, nil
}

func inspectMemorySource(d agentDocument, m AgentMemory) AgentMemoryEvidence {
	e := AgentMemoryEvidence{MemoryID: m.ID, Status: "free-form"}
	if !strings.HasPrefix(m.Source, "chat-memory-v1") {
		return e
	}
	e.Status = "invalid"
	p := strings.Split(m.Source, " ")
	if len(p) != 6 {
		return e
	}
	kind, profile := strings.TrimPrefix(p[1], "kind="), strings.TrimPrefix(p[2], "profile=")
	session, id := strings.TrimPrefix(p[3], "session="), strings.TrimPrefix(p[4], "id=")
	hash := strings.TrimPrefix(p[5], "sha256=")
	canonical := fmt.Sprintf("chat-memory-v1 kind=%s profile=%s session=%s id=%s sha256=%s", kind, profile, session, id, hash)
	if len(hash) != sha256.Size*2 {
		return e
	}
	decoded, err := hex.DecodeString(hash)
	if p[0] != "chat-memory-v1" || canonical != m.Source || (kind != "message" && kind != "answer") || !validAgentText(session, 80, true) || !validAgentText(id, 80, true) || strings.ContainsAny(session+id, "\t\r\n") || err != nil || len(decoded) != sha256.Size || strings.ToLower(hash) != hash {
		return e
	}
	e.Status = "unavailable"
	if profile != m.ProfileID {
		return e
	}
	sessions, sources := 0, 0
	available := false
	for _, s := range d.Sessions {
		if s.ID == session {
			sessions++
			available = s.ProfileID == profile
		}
	}
	if sessions > 1 {
		e.Status = "ambiguous"
		return e
	}
	if sessions != 1 || !available {
		return e
	}
	var text string
	available = false
	if kind == "message" {
		for _, source := range d.Messages {
			if source.ID == id {
				sources++
				available = source.ProfileID == profile && source.SessionID == session && source.Role == "user"
				text = source.Text
			}
		}
	} else {
		for _, source := range d.AIRequests {
			if source.ID == id {
				sources++
				available = source.ProfileID == profile && source.SessionID == session && source.State == "completed" && source.TaskID == ""
				text = source.Answer
			}
		}
	}
	if sources > 1 {
		e.Status = "ambiguous"
		return e
	}
	if sources != 1 || !available || !validAgentText(text, 4000, true) || !utf8.ValidString(text) {
		return e
	}
	if fmt.Sprintf("%x", sha256.Sum256([]byte(text))) != hash {
		e.Status = "changed"
		return e
	}
	return AgentMemoryEvidence{MemoryID: m.ID, Status: "matched", Kind: kind, SessionID: session, ID: id, SHA256: hash, Text: text}
}
