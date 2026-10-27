package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func evidenceAICommand(t *testing.T, session string, evidence AgentEvidenceView) AgentCommand {
	t.Helper()
	c := memorySelectionCommand(t, session, map[string]any{"mode": "none"})
	b, _ := json.Marshal(c)
	var fields map[string]any
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	fields["evidenceSelection"] = map[string]string{"kind": evidence.Kind, "id": evidence.ID, "itemId": evidence.ItemID, "sha256": evidence.SHA256}
	b, _ = json.Marshal(fields)
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAgentAIEvidenceDisclosure(t *testing.T) {
	for _, kind := range []string{"memory", "headline", "reminder-run"} {
		for _, mode := range []string{"legacy", "all", "none", "selected"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				s, v, calls := evidenceFixture(t)
				session := v.Sessions[len(v.Sessions)-1].ID
				queries := evidenceQueries(v)
				var selected AgentEvidenceView
				for _, query := range queries {
					if query.kind == kind {
						var err error
						selected, err = s.AgentEvidence(t.Context(), v.ProfileID, query.kind, query.id, query.item)
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				before, revision, _ := readAgents(t.Context(), s.db)
				c := evidenceAICommand(t, session, selected)
				if mode == "legacy" {
					c.MemorySelection = nil
				} else {
					c.MemorySelection = &AIMemorySelection{Mode: mode}
					if mode == "selected" {
						c.MemorySelection.IDs = []string{v.Memories[0].ID}
					}
				}
				v = agentApply(t, s, c)
				q := v.AIRequests[len(v.AIRequests)-1]
				e, err := frozenAIEvidence(q)
				if err != nil || e.Evidence.SHA256 != selected.SHA256 || string(e.Evidence.Data) != string(selected.Data) || e.Evidence.Revision != revision || e.Evidence.ObservedAt != q.CreatedAt || e.Evidence.RecordedAt != selected.RecordedAt {
					t.Fatal("inexact server evidence", err, e)
				}
				if len(q.Context) > 8000 || q.News != nil || q.State != "prepared" || q.HeldMicrousd != 0 || v.ProviderCallsEnabled || calls.Load() != 1 {
					t.Fatal("preparation escaped scope")
				}
				wire, _ := aiWire(q)
				var w map[string]any
				json.Unmarshal(wire, &w)
				if q.Disclosure != string(wire) || q.Digest != aiDigest(q) || w["tool_choice"] != "none" || len(w["tools"].([]any)) != 0 {
					t.Fatal("consent/tools changed")
				}
				chosen, err := draftMemorySelection(q)
				if err != nil || (mode == "legacy" && chosen != nil) || (mode != "legacy" && (chosen == nil || chosen.Mode != mode)) {
					t.Fatal("memory choice changed", err)
				}
				after, _, _ := readAgents(t.Context(), s.db)
				after.AIRequests = before.AIRequests
				if !reflect.DeepEqual(before, after) {
					t.Fatal("Prepare changed source/tasks/history")
				}
				now := s.now().Add(301 * time.Second)
				s.now = func() time.Time { return now }
				agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: "news-researcher", Text: "FOREIGN-PRIVATE", Source: "owner"})
				d, _, _ := readAgents(t.Context(), s.db)
				current, err := aiRequestContext(d, q, s.now().Unix())
				if err != nil || current != q.Context || strings.Contains(q.Context, "FOREIGN-PRIVATE") {
					t.Fatal("stable source/time/revision not retained", err)
				}
				review, err := s.AgentAIRequestDraft(t.Context(), q.ProfileID, q.ID)
				if err != nil || review.EvidenceSelection == nil || !reflect.DeepEqual(*review.EvidenceSelection, e.Selection) {
					t.Fatal("draft lost evidence", err)
				}
			})
		}
	}
}

func evidenceReviewFixture(t *testing.T, s *Store, expires int64) (AgentView, AgentMemory, AgentCommand) {
	t.Helper()
	v := agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: "trading-analyst", Name: "Evidence review"})
	session := v.Sessions[len(v.Sessions)-1].ID
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, Text: "Explicit inspected evidence", Source: "owner", ExpiresAt: expires})
	note := v.Memories[len(v.Memories)-1]
	e, err := s.AgentEvidence(t.Context(), v.ProfileID, "memory", note.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return v, note, evidenceAICommand(t, session, e)
}
func TestAgentAIEvidenceRefusals(t *testing.T) {
	for _, name := range []string{"wrong-hash", "uppercase-hash", "missing-hash", "missing-source", "foreign-source", "ambiguous-source", "changed-source", "expired", "invalid-provenance", "foreign-conversation", "ambiguous-conversation", "unknown-kind", "extra-item", "missing-item", "other-action", "scheduled", "slots"} {
		t.Run(name, func(t *testing.T) {
			s := fixtureStore(t)
			v, note, c := evidenceReviewFixture(t, s, s.now().Unix()+300)
			switch name {
			case "wrong-hash":
				c.EvidenceSelection.SHA256 = strings.Repeat("0", 64)
			case "uppercase-hash":
				c.EvidenceSelection.SHA256 = strings.ToUpper(c.EvidenceSelection.SHA256)
			case "missing-hash":
				c.EvidenceSelection.SHA256 = ""
			case "missing-source":
				c.EvidenceSelection.ID = "missing"
			case "foreign-source":
				rewriteReminderFixture(t, s, func(d *agentDocument) { d.Memories[0].ProfileID = "news-researcher" })
			case "ambiguous-source":
				rewriteReminderFixture(t, s, func(d *agentDocument) { d.Memories = append(d.Memories, d.Memories[0]) })
			case "changed-source":
				agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: note.ID, Text: "corrected", Source: note.Source, ExpiresAt: note.ExpiresAt})
			case "expired":
				now := s.now().Add(301 * time.Second)
				s.now = func() time.Time { return now }
			case "invalid-provenance":
				rewriteReminderFixture(t, s, func(d *agentDocument) { d.Memories[0].Source = "chat-message-v1:bad" })
			case "foreign-conversation":
				rewriteReminderFixture(t, s, func(d *agentDocument) { d.Sessions[0].ProfileID = "news-researcher" })
			case "ambiguous-conversation":
				rewriteReminderFixture(t, s, func(d *agentDocument) { d.Sessions = append(d.Sessions, d.Sessions[0]) })
			case "unknown-kind":
				c.EvidenceSelection.Kind = "shell"
			case "extra-item":
				c.EvidenceSelection.ItemID = "extra"
			case "missing-item":
				c.EvidenceSelection.Kind = "headline"
			case "other-action":
				c.Action = "memory-put"
			case "scheduled":
				c.Action = "task-ai-prepare"
			case "slots":
				rewriteReminderFixture(t, s, func(d *agentDocument) { d.AIRequests = make([]AgentAIRequest, 10) })
			}
			selectionRefused(t, s, c, false)
		})
	}
}
func TestAgentAIEvidenceBounds(t *testing.T) {
	s := fixtureStore(t)
	v, note, c := evidenceReviewFixture(t, s, 0)
	// A complete valid inspected record may still exceed the smaller AI context.
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: note.ID, Text: strings.Repeat("<", 1400), Source: "owner"})
	e, err := s.AgentEvidence(t.Context(), v.ProfileID, "memory", note.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(e)
	if len(b) < 8000 || len(b)+1 > agentEvidenceLimit {
		t.Fatal("wrong bounded fixture", len(b))
	}
	c = evidenceAICommand(t, c.ID, e)
	selectionRefused(t, s, c, false)
	// Find and enforce the exact complete-envelope boundary without larger writes.
	v = agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: note.ID, Text: "Evidence", Source: "owner"})
	e, err = s.AgentEvidence(t.Context(), v.ProfileID, "memory", note.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	c = evidenceAICommand(t, c.ID, e)
	v = agentApply(t, s, c)
	q := v.AIRequests[0]
	f, err := frozenAIEvidence(q)
	if err != nil {
		t.Fatal(err)
	}
	fixed, _ := json.Marshal(f)
	pad := 8000 - len(fixed)
	if pad <= 0 {
		t.Fatal("no boundary space")
	}
	// Test the shared encoder with exact complete bytes; actual preparation above
	// proves source validity and refuses oversize before any request is saved.
	f.Context = json.RawMessage(`"` + strings.Repeat("x", len(f.Context)-2+pad) + `"`)
	exact, err := encodeAIEvidence(f)
	if err != nil || len(exact) != 8000 {
		t.Fatal("exact boundary", len(exact), err)
	}
	f.Context = json.RawMessage(`"` + strings.Repeat("x", len(f.Context)-1) + `"`)
	if out, err := encodeAIEvidence(f); err == nil || out != "" {
		t.Fatal("oversize returned partial context")
	}
	c = evidenceAICommand(t, c.ID, e)
	c.Limits.MaxInputTokens = 1000
	selectionRefused(t, s, c, false)
}
func TestAgentAIEvidenceConsentDispatchAndCompletion(t *testing.T) {
	for _, stage := range []string{"ordinary", "consent", "dispatch", "completion", "unknown", "inflight"} {
		t.Run(stage, func(t *testing.T) {
			s := fixtureStore(t)
			v, note, c := evidenceReviewFixture(t, s, 0)
			v = agentApply(t, s, c)
			q := v.AIRequests[0]
			if stage == "ordinary" {
				selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: q.ID, ConsentDigest: q.Digest}, false)
				return
			}
			mutate := func() {
				agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: note.ID, Text: "Owner correction", Source: note.Source})
			}
			if stage == "consent" {
				mutate()
				selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: q.ID, ConsentDigest: q.Digest}, true)
				return
			}
			v = aiStart(t, s, v)
			if stage == "dispatch" {
				mutate()
			}
			calls := 0
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if stage == "unknown" {
					return nil, errors.New("controlled unknown result")
				}
				response, err := (fixtureAITransport{}).RoundTrip(r)
				if stage == "completion" || stage == "inflight" {
					mutate()
				}
				if stage == "inflight" {
					select {
					case <-r.Context().Done():
					case <-time.After(2 * time.Second):
						t.Fatal("source change did not cancel in-flight call")
					}
					return nil, r.Context().Err()
				}
				return response, err
			})
			if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			got := v.AIRequests[0]
			expected := 1
			if stage == "dispatch" {
				expected = 0
			}
			if calls != expected || got.State == "completed" || got.Answer != "" || len(got.Cards) != 0 || len(v.Tasks) != 0 {
				t.Fatal("stale/unknown request escaped", stage, calls, got.State)
			}
			if stage == "completion" && (got.InputTokens <= 0 || got.SpentMicrousd <= 0 || got.HeldMicrousd != 0) {
				t.Fatal("completion lost usage", got)
			}
			if (stage == "unknown" || stage == "inflight") && got.HeldMicrousd == 0 {
				t.Fatal("unknown hold cleared")
			}
			if err := s.advanceAgentAI(t.Context(), rt, true); err != nil || calls != expected {
				t.Fatal("request replayed", err)
			}
		})
	}
}
func TestAgentAIEvidenceAdmissionBoundaries(t *testing.T) {
	for _, boundary := range []string{"access", "count", "create", "inflight"} {
		t.Run(boundary, func(t *testing.T) {
			s, connection := admissionStore(t)
			v, note, c := evidenceReviewFixture(t, s, 0)
			c.Model = admittedOpenAIModel
			c.ConnectionRef = connection.Ref
			v = agentApply(t, s, c)
			v = aiStart(t, s, v)
			calls := 0
			rt := aiTestTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				res, err := (fixtureAITransport{}).RoundTrip(r)
				mutate := (boundary == "access" && r.Method == "GET") || (boundary == "count" && strings.HasSuffix(r.URL.Path, "input_tokens")) || ((boundary == "create" || boundary == "inflight") && r.URL.Path == "/v1/responses")
				if mutate {
					agentApply(t, s, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: note.ID, Text: "Correction during operation", Source: note.Source})
					if boundary == "inflight" {
						select {
						case <-r.Context().Done():
						case <-time.After(2 * time.Second):
							t.Fatal("source change did not cancel admitted call")
						}
						return nil, r.Context().Err()
					}
				}
				return res, err
			})
			if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
				t.Fatal(err)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[len(v.AIRequests)-1]
			expected := map[string]int{"access": 1, "count": 2, "create": 3, "inflight": 3}[boundary]
			if calls != expected || q.State == "completed" || q.Answer != "" || len(q.Cards) != 0 {
				t.Fatal("stale evidence crossed operation", boundary, calls, q.State)
			}
			if err := s.advanceAgentAI(t.Context(), rt, true); err != nil || calls != expected {
				t.Fatal("admitted operation replayed", err)
			}
		})
	}
}
func TestAgentAIEvidenceRestartProviderDraftAndHolds(t *testing.T) {
	for _, state := range []string{"prepared", "unknown"} {
		t.Run(state, func(t *testing.T) {
			s := fixtureStore(t)
			v, note, c := evidenceReviewFixture(t, s, 0)
			v = agentApply(t, s, c)
			q := v.AIRequests[0]
			if state == "unknown" {
				v = aiStart(t, s, v)
				if err := s.advanceAgentAI(t.Context(), aiTestTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("unknown") }), true); err != nil {
					t.Fatal(err)
				}
				v, _ = s.Agents(t.Context(), v.ProfileID, "")
				q = v.AIRequests[0]
			}
			now := s.now()
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			n, err := OpenStore(s.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer n.Close()
			n.now = func() time.Time { return now }
			profile := v.Profiles[0]
			for _, p := range v.Profiles {
				if p.ID == v.ProfileID {
					profile = p
				}
			}
			agentApply(t, n, AgentCommand{Action: "profile-update", ProfileID: v.ProfileID, Name: profile.Name, Text: profile.Persona, Provider: "anthropic", Model: "preference-only"})
			draft, err := n.AgentAIRequestDraft(t.Context(), v.ProfileID, q.ID)
			if err != nil || draft.EvidenceSelection == nil || !reflect.DeepEqual(draft.EvidenceSelection, c.EvidenceSelection) {
				t.Fatal("restart/provider lost draft", err)
			}
			d, _, _ := readAgents(t.Context(), n.db)
			current, err := aiRequestContext(d, q, now.Unix())
			if err != nil || current != q.Context {
				t.Fatal("provider changed frozen request", err)
			}
			agentApply(t, n, AgentCommand{Action: "memory-put", ProfileID: v.ProfileID, ID: note.ID, Text: "Corrected after restart", Source: note.Source})
			draft, err = n.AgentAIRequestDraft(t.Context(), v.ProfileID, q.ID)
			if err != nil || !reflect.DeepEqual(draft.EvidenceSelection, c.EvidenceSelection) {
				t.Fatal("stale reference silently dropped", err)
			}
			selectionRefused(t, n, c, false)
			e, err := n.AgentEvidence(t.Context(), v.ProfileID, "memory", note.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			fresh := evidenceAICommand(t, c.ID, e)
			v = agentApply(t, n, fresh)
			if v.AIRequests[0].Context != q.Context {
				t.Fatal("original request changed")
			}
			if state == "unknown" {
				latest := v.AIRequests[len(v.AIRequests)-1]
				selectionRefused(t, n, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: latest.ID, ConsentDigest: latest.Digest}, true)
				if v.AIRequests[0].HeldMicrousd != q.HeldMicrousd || q.HeldMicrousd == 0 {
					t.Fatal("unknown hold lost")
				}
			}
		})
	}
}
func TestAgentAIEvidenceFrozenGrammar(t *testing.T) {
	s := fixtureStore(t)
	v, _, c := evidenceReviewFixture(t, s, 0)
	v = agentApply(t, s, c)
	q := v.AIRequests[0]
	e, err := frozenAIEvidence(q)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"version", "nested", "hash", "profile", "observation", "revision", "private-data", "unknown-field", "duplicate-field", "missing-reference", "inner-version"} {
		t.Run(name, func(t *testing.T) {
			var x aiEvidenceContext
			json.Unmarshal([]byte(q.Context), &x)
			bad := q
			switch name {
			case "version":
				x.Version = "manual-ai-evidence-v2"
			case "nested":
				x.Context = json.RawMessage(q.Context)
			case "hash":
				x.Selection.SHA256 = strings.Repeat("a", 64)
			case "profile":
				x.Evidence.ProfileID = "news-researcher"
			case "observation":
				x.Evidence.ObservedAt++
			case "revision":
				x.Evidence.Revision = -1
			case "private-data":
				var data map[string]any
				json.Unmarshal(x.Evidence.Data, &data)
				data["privateContext"] = "must not be accepted"
				x.Evidence.Data, _ = json.Marshal(data)
				hashed, _ := finishAgentEvidence(x.Evidence)
				x.Evidence.SHA256 = hashed.SHA256
				x.Selection.SHA256 = hashed.SHA256
			case "missing-reference":
				x.Selection = AIEvidenceSelection{}
			case "inner-version":
				x.Context = json.RawMessage(`{"version":"unsupported"}`)
			}
			b, _ := json.Marshal(x)
			bad.Context = string(b)
			if name == "unknown-field" {
				bad.Context = strings.TrimSuffix(bad.Context, "}") + `,"extra":true}`
			}
			if name == "duplicate-field" {
				bad.Context = strings.TrimSuffix(bad.Context, "}") + `,"version":"manual-ai-evidence-v1"}`
			}
			if _, err := frozenAIEvidence(bad); err == nil {
				t.Fatal("malformed source accepted", name)
			}
			if _, err := draftMemorySelection(bad); err == nil {
				t.Fatal("malformed draft accepted", name)
			}
		})
	}
	if e.Version != aiEvidenceContextVersion {
		t.Fatal("wrong version")
	}
}

// Exact pre-evidence context guard from accepted 0946ff7 (function-level
// compatibility model, not a claim of installed historical-binary acceptance).
func preEvidenceContextReader(d agentDocument, q AgentAIRequest, now int64) (string, error) {
	var envelope struct {
		Version   string             `json:"version"`
		Selection *AIMemorySelection `json:"memorySelection"`
	}
	if json.Unmarshal([]byte(q.Context), &envelope) != nil {
		return "", errors.New("invalid retained AI context")
	}
	if envelope.Version != "" || envelope.Selection != nil {
		if envelope.Version != aiMemoryContextVersion || envelope.Selection == nil || q.TaskID != "" {
			return "", errors.New("unsupported manual AI memory context; prepare a fresh review")
		}
	}
	base, e := aiContextWithMemory(d, q.ProfileID, q.SessionID, q.Prompt, now, envelope.Selection)
	if e != nil || q.News == nil {
		return base, e
	}
	f, e := freshNewsEvidence(d, q.ProfileID, q.News, now)
	if e != nil {
		return "", e
	}
	return addNewsContext(base, f)
}
func TestAgentAIEvidenceOlderContextAndInterruptedHold(t *testing.T) {
	s := fixtureStore(t)
	v, _, c := evidenceReviewFixture(t, s, 0)
	v = agentApply(t, s, c)
	q := v.AIRequests[0]
	d, _, _ := readAgents(t.Context(), s.db)
	if _, err := preEvidenceContextReader(d, q, s.now().Unix()); err == nil {
		t.Fatal("older context reader admitted new version")
	}
	v = aiStart(t, s, v)
	rewriteReminderFixture(t, s, func(d *agentDocument) { d.AIRequests[0].State = "running"; d.AIRequests[0].DispatchBoot = s.bootID })
	now := s.now()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.now = func() time.Time { return now }
	calls := 0
	if err = n.advanceAgentAI(t.Context(), aiTestTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not call") }), true); err != nil {
		t.Fatal(err)
	}
	v, err = n.Agents(t.Context(), v.ProfileID, "")
	if err != nil || calls != 0 || v.AIRequests[0].HeldMicrousd == 0 || v.AIRequests[0].State != "unknown" || v.AIRequests[0].Context != q.Context {
		t.Fatal("interrupted request lost hold or replayed", err)
	}
}

func TestAgentAIEvidenceNewsDependencyAndExpiry(t *testing.T) {
	t.Run("fresh-news-remains-independent", func(t *testing.T) {
		s, v, calls := evidenceFixture(t)
		source := v.NewsSources[0]
		session := v.Sessions[len(v.Sessions)-1].ID
		e, err := s.AgentEvidence(t.Context(), v.ProfileID, "memory", v.Memories[0].ID, "")
		if err != nil {
			t.Fatal(err)
		}
		c := evidenceAICommand(t, session, e)
		c.NewsSourceID = source.ID
		c.NewsMaxAgeSeconds = source.MaxAgeSeconds
		v = agentApply(t, s, c)
		q := v.AIRequests[len(v.AIRequests)-1]
		d, _, _ := readAgents(t.Context(), s.db)
		current, err := aiRequestContext(d, q, s.now().Unix())
		if err != nil || current != q.Context || q.News == nil {
			t.Fatal("evidence/news did not round trip", err)
		}
		draft, err := s.AgentAIRequestDraft(t.Context(), v.ProfileID, q.ID)
		if err != nil || draft.NewsSourceID != source.ID || draft.EvidenceSelection == nil {
			t.Fatal("draft lost news/evidence", err)
		}
		v = aiStart(t, s, v)
		if err = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
			t.Fatal(err)
		}
		v, _ = s.Agents(t.Context(), v.ProfileID, "")
		if v.AIRequests[len(v.AIRequests)-1].State != "completed" || calls.Load() != 1 {
			t.Fatal("fixture failed or refetched")
		}
		v = agentApply(t, s, c)
		q = v.AIRequests[len(v.AIRequests)-1]
		agentApply(t, s, AgentCommand{Action: "news-source-disable", ProfileID: v.ProfileID, ID: source.ID})
		selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: q.ID, ConsentDigest: q.Digest}, true)
	})
	t.Run("expiry-with-unchanged-content-hash", func(t *testing.T) {
		s := fixtureStore(t)
		v, note, c := evidenceReviewFixture(t, s, s.now().Unix()+300)
		v = agentApply(t, s, c)
		q := v.AIRequests[0]
		now := s.now().Add(301 * time.Second)
		s.now = func() time.Time { return now }
		e, err := s.AgentEvidence(t.Context(), v.ProfileID, "memory", note.ID, "")
		if err != nil || e.SHA256 != c.EvidenceSelection.SHA256 || e.Freshness != "expired" {
			t.Fatal("wrong expiry boundary", err)
		}
		selectionRefused(t, s, AgentCommand{Action: "ai-start", ProfileID: v.ProfileID, ID: q.ID, ConsentDigest: q.Digest}, true)
	})
}

func TestArchivedMemoryEvidenceJourney(t *testing.T) {
	s := fixtureStore(t)
	v := aiStart(t, s, aiPrepare(t, s))
	if err := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q := v.AIRequests[0]
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, ID: q.ID, Source: "answer", Text: "Retained preference"})
	note := v.Memories[0]
	v = agentApply(t, s, AgentCommand{Action: "session-archive", ProfileID: v.ProfileID, ID: q.SessionID})
	dir := s.dir
	s.Close()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v = agentApply(t, s, AgentCommand{Action: "session-create", ProfileID: v.ProfileID, Name: "New review"})
	before := retainedRevisionState(t, s)
	evidence, err := s.AgentEvidence(t.Context(), v.ProfileID, "memory", note.ID, "")
	if err != nil {
		t.Fatal("archived evidence", err)
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("inspection mutated state")
	}
	if _, err = s.AgentEvidence(t.Context(), "news-researcher", "memory", note.ID, ""); err == nil {
		t.Fatal("foreign memory admitted")
	}
	v = agentApply(t, s, evidenceAICommand(t, v.Sessions[0].ID, evidence))
	v = aiStart(t, s, v)
	if err = s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	if len(v.Sessions) != 1 || len(v.AIRequests) != 1 || v.AIRequests[0].State != "completed" {
		t.Fatal("archive restored or request not completed", v.AIRequests)
	}
	// Simulate disk corruption in this disposable store; ordinary writes are immutable.
	if _, err = s.db.ExecContext(t.Context(), "DROP TRIGGER task_history_immutable_update"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(t.Context(), "UPDATE agent_history SET record=record || ' ' WHERE source_id=?", q.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AgentEvidence(t.Context(), v.ProfileID, "memory", note.ID, ""); err == nil {
		t.Fatal("tampered archive accepted")
	}
	c := evidenceAICommand(t, v.Sessions[0].ID, evidence)
	c.Revision = v.Revision
	if _, err = s.ChangeAgents(t.Context(), c); err == nil {
		t.Fatal("tampered archive prepared")
	}
}
