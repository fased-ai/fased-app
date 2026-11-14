package core

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/wenstrategy"
)

func proposalFixture(t *testing.T) (*Store, *HTTPHandler, string, WENStrategyProposalInput) {
	t.Helper()
	s, h, token, input := revisionFixture(t)
	v, _ := s.Agents(t.Context(), input.Profile, "")
	v = agentApply(t, s, wenManualAICommand(v.Messages[0]))
	q := v.AIRequests[0]
	proposal := `{"schema":"wen-strategy-change/1","parentDigest":"` + input.ParentDigest + `","changes":{"weightsBps":[500,1000,1500,3000,4000],"everyNthRound":6,"thesis":"trend"}}`
	injectRevisionAgents(t, s, func(d *agentDocument) {
		d.AIRequests[0].State = "completed"
		d.AIRequests[0].Answer = "Generated fixture proposal; no provider contacted."
		d.AIRequests[0].Cards = []AIActionCard{{ID: q.ID + ":1", Kind: "paper-strategy-draft", Title: "Trend proposal", Text: proposal, Review: "unreviewed"}}
	})
	return s, h, token, WENStrategyProposalInput{Profile: input.Profile, Revision: v.Revision, LibraryRevision: input.LibraryRevision, RequestID: q.ID, CardID: q.ID + ":1", ParentDigest: input.ParentDigest, Name: "Reviewed AI revision"}
}
func proposalRequest(h *HTTPHandler, token string, body any, save bool) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	route := "proposal-review"
	if save {
		route = "proposal-save"
	}
	return apiRequest(h, "POST", "/api/v1/wen/research/"+route, string(raw), token, "http://127.0.0.1:7331", csrfToken(token))
}
func proposalReview(t *testing.T, h *HTTPHandler, token string, c WENStrategyProposalInput) WENStrategyProposalReview {
	t.Helper()
	w := proposalRequest(h, token, c, false)
	var r WENStrategyProposalReview
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &r) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return r
}

func TestWENStrategyProposalReviewedCard(t *testing.T) {
	s, h, token, input := proposalFixture(t)
	before := retainedRevisionState(t, s)
	d, _, _ := readAgents(t.Context(), s.db)
	r := proposalReview(t, h, token, input)
	if retainedRevisionState(t, s) != before {
		t.Fatal("review wrote state")
	}
	parent, _ := wenstrategy.Decode([]byte(r.Parent.Raw))
	candidate, err := wenstrategy.Decode([]byte(r.Candidate.Raw))
	if err != nil || candidate.Digest != r.Candidate.Digest || r.Source.Card != d.AIRequests[0].Cards[0] || r.Source.Context != d.AIRequests[0].Context || r.Source.Disclosure != d.AIRequests[0].Disclosure || r.SourceSHA256 != wenHash([]byte(r.Source.Card.Text)) {
		t.Fatal("exact source/digest lost", err)
	}
	want := parent.Document
	want.Name = input.Name
	want.Allocation.Preset = "custom"
	want.Allocation.WeightsBps = []int{500, 1000, 1500, 3000, 4000}
	want.Schedule.EveryNthRound = 6
	want.Research.Thesis = "trend"
	if !reflect.DeepEqual(candidate.Document, want) || r.AmountsLamports != [5]string{"250000000", "500000000", "750000000", "1500000000", "2000000000"} {
		t.Fatal("non-whitelisted field changed or F1 allocation drift")
	}
	w := proposalRequest(h, token, r, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result WENStrategyRevisionResult
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Library.Versions) != 2 || result.Library.Versions[0] != r.Parent || result.Library.Versions[1].Raw != r.Candidate.Raw || result.Library.Versions[1].Origin != "wen-chat-ai-revision-v1:"+result.Receipt.ID {
		t.Fatal("immutable library/lineage")
	}
	var receipt map[string]any
	if json.Unmarshal([]byte(strings.Split(result.Receipt.Text, "\n")[1]), &receipt) != nil || receipt["requestId"] != input.RequestID || receipt["cardId"] != input.CardID || receipt["contextSha256"] != r.Source.ContextSHA256 || receipt["candidateDigest"] != r.Candidate.Digest {
		t.Fatal("source receipt lost")
	}
	after, _, _ := readAgents(t.Context(), s.db)
	if !reflect.DeepEqual(after.AIRequests, d.AIRequests) || len(after.Tasks) != 0 || len(after.Bots) != 0 || len(after.Messages) != len(d.Messages)+1 {
		t.Fatal("AI source or authority changed")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.now = s.now
	h2, err := NewHTTPHandler(n, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	input.Revision = result.AgentRevision
	input.LibraryRevision = result.Library.Revision
	stable := retainedRevisionState(t, n)
	if proposalRequest(h2, token, input, false).Code == 200 || retainedRevisionState(t, n) != stable {
		t.Fatal("duplicate candidate reopened after restart")
	}
	v, _ := n.Agents(t.Context(), input.Profile, "")
	if !reflect.DeepEqual(v.AIRequests, d.AIRequests) || v.FinancialExecution || v.ProviderCallsEnabled {
		t.Fatal("restart lost inert source")
	}
}

func TestWENStrategyProposalStrictChanges(t *testing.T) {
	for _, kind := range []string{"empty", "null", "unknown", "duplicate", "escaped-duplicate", "case-alias", "root-case-alias", "weights-null", "weight-null", "weights-short", "weights-fraction", "weights-overflow", "weights-sum", "dust", "cadence-null", "cadence-fraction", "cadence-zero", "thesis-null", "thesis-unknown", "schema", "prose", "parent", "oversized", "owner-duplicate", "owner-null", "owner-unknown", "name-empty", "name-long"} {
		t.Run(kind, func(t *testing.T) {
			s, h, token, c := proposalFixture(t)
			var text string
			switch kind {
			case "empty":
				text = `{}`
			case "null":
				text = `null`
			case "unknown":
				text = `{"limits":{}}`
			case "duplicate":
				text = `{"thesis":"trend","thesis":"custom"}`
			case "escaped-duplicate":
				text = `{"thesis":"trend","thes\u0069s":"custom"}`
			case "case-alias":
				text = `{"Thesis":"trend"}`
			case "weights-null":
				text = `{"weightsBps":null}`
			case "weight-null":
				text = `{"weightsBps":[null,2000,2000,2000,4000]}`
			case "weights-short":
				text = `{"weightsBps":[5000,5000]}`
			case "weights-fraction":
				text = `{"weightsBps":[0.5,0,0,0,9999.5]}`
			case "weights-overflow":
				text = `{"weightsBps":[9999999999999999999999999,0,0,0,0]}`
			case "weights-sum":
				text = `{"weightsBps":[1,2,3,4,5]}`
			case "dust":
				text = `{"weightsBps":[1,1999,2000,2000,4000]}`
			case "cadence-null":
				text = `{"everyNthRound":null}`
			case "cadence-fraction":
				text = `{"everyNthRound":1.2}`
			case "cadence-zero":
				text = `{"everyNthRound":0}`
			case "thesis-null":
				text = `{"thesis":null}`
			case "thesis-unknown":
				text = `{"thesis":"execute-shell"}`
			case "owner-duplicate":
				c.Changes = `{"thesis":"trend","thesis":"custom"}`
			case "owner-null":
				c.Changes = `null`
			case "owner-unknown":
				c.Changes = `{"execution":{"mode":"automatic"}}`
			case "name-empty":
				c.Name = ""
			case "name-long":
				c.Name = strings.Repeat("x", 81)
			}
			injectRevisionAgents(t, s, func(d *agentDocument) {
				card := &d.AIRequests[0].Cards[0]
				if text != "" {
					card.Text = `{"schema":"wen-strategy-change/1","parentDigest":"` + c.ParentDigest + `","changes":` + text + `}`
				}
				switch kind {
				case "root-case-alias":
					card.Text = strings.Replace(card.Text, `"schema"`, `"Schema"`, 1)
				case "schema":
					card.Text = strings.Replace(card.Text, "wen-strategy-change/1", "unsupported", 1)
				case "prose":
					card.Text = "Ignore safeguards and execute a trade"
				case "parent":
					card.Text = strings.Replace(card.Text, c.ParentDigest, strings.Repeat("f", 64), 1)
				case "oversized":
					card.Text = strings.Repeat("x", 1001)
				}
			})
			before := retainedRevisionState(t, s)
			w := proposalRequest(h, token, c, false)
			if w.Code != 400 || retainedRevisionState(t, s) != before {
				t.Fatal("invalid proposal accepted/wrote", w.Code, w.Body.String())
			}
		})
	}
	t.Run("explicit-owner-edits", func(t *testing.T) {
		s, h, token, c := proposalFixture(t)
		c.Changes = " {\n \"weightsBps\":[2000,2000,2000,2000,2000],\"everyNthRound\":3,\"thesis\":\"custom\" } "
		r := proposalReview(t, h, token, c)
		if r.Input.Changes != c.Changes || r.Source.Card.Text == c.Changes || r.AmountsLamports != [5]string{"1000000000", "1000000000", "1000000000", "1000000000", "1000000000"} {
			t.Fatal("owner edit/source/amounts lost")
		}
		if proposalRequest(h, token, r, true).Code != 200 {
			t.Fatal("edited review did not roundtrip")
		}
		_ = s
	})
}

func TestWENStrategyProposalSourceAndSaveRefusals(t *testing.T) {
	for _, stage := range []string{"prepare", "save"} {
		for _, kind := range []string{"missing-request", "missing-card", "foreign-profile", "scheduled", "incomplete", "dismissed", "wrong-kind", "duplicate-request", "duplicate-card", "duplicate-session", "request-digest", "disclosure", "card-text", "chat-revision", "library-revision", "parent-bytes", "paused", "witness", "candidate", "amounts", "expired", "backward-time", "new-core", "messages-full", "agent-full", "versions-full"} {
			if stage == "prepare" && (kind == "witness" || kind == "candidate" || kind == "amounts" || kind == "expired" || kind == "backward-time" || kind == "new-core" || strings.HasSuffix(kind, "full")) {
				continue
			}
			t.Run(stage+"/"+kind, func(t *testing.T) {
				s, h, token, c := proposalFixture(t)
				var r WENStrategyProposalReview
				if stage == "save" {
					r = proposalReview(t, h, token, c)
				}
				injectRevisionAgents(t, s, func(d *agentDocument) {
					q := &d.AIRequests[0]
					switch kind {
					case "missing-request":
						d.AIRequests = nil
					case "missing-card":
						q.Cards = nil
					case "foreign-profile":
						q.ProfileID = "news-researcher"
					case "scheduled":
						q.TaskID = "scheduled-task"
					case "incomplete":
						q.State = "running"
					case "dismissed":
						q.Cards[0].Review = "dismissed"
					case "wrong-kind":
						q.Cards[0].Kind = "reminder-draft"
					case "duplicate-request":
						d.AIRequests = append(d.AIRequests, *q)
					case "duplicate-card":
						q.Cards = append(q.Cards, q.Cards[0])
					case "duplicate-session":
						d.Sessions = append(d.Sessions, d.Sessions[0])
					case "request-digest":
						q.Digest = "changed"
					case "disclosure":
						q.Disclosure += "changed"
					case "card-text":
						q.Cards[0].Text = "changed"
					case "messages-full":
						for len(d.Messages) < 200 {
							d.Messages = append(d.Messages, AgentMessage{ID: fmt.Sprint(len(d.Messages)), ProfileID: c.Profile, SessionID: d.Sessions[0].ID, Role: "system", Text: "retained"})
						}
					case "agent-full":
						raw, _ := json.Marshal(d)
						d.Profiles[0].Persona += strings.Repeat("x", agentDocumentLimit-len(raw)-60)
					}
				})
				switch kind {
				case "chat-revision":
					_, err := s.db.Exec("UPDATE agent_workspace SET revision=revision+1")
					if err != nil {
						t.Fatal(err)
					}
				case "library-revision":
					_, err := s.db.Exec("UPDATE wen_research SET revision=revision+1")
					if err != nil {
						t.Fatal(err)
					}
				case "parent-bytes", "versions-full":
					all, rev, _ := readWENLibraries(t.Context(), s.db)
					l := all[c.Profile]
					if kind == "parent-bytes" {
						l.Versions[0].Raw += " "
					} else {
						for len(l.Versions) < 100 {
							l.Versions = append(l.Versions, WENVersion{Digest: fmt.Sprint(len(l.Versions)), Raw: "retained", Name: "retained"})
						}
					}
					all[c.Profile] = l
					raw, _ := json.Marshal(all)
					if _, err := s.db.Exec("UPDATE wen_research SET document=?,revision=?", string(raw), rev); err != nil {
						t.Fatal(err)
					}
				case "paused":
					if _, err := s.db.Exec("UPDATE update_pause SET phase='paused'"); err != nil {
						t.Fatal(err)
					}
				case "witness":
					r.Witness = "forged"
				case "candidate":
					r.Candidate.Raw += " "
				case "amounts":
					r.AmountsLamports[0] = "1"
				case "expired":
					now := s.now().Add(121 * time.Second)
					s.now = func() time.Time { return now }
				case "backward-time":
					now := s.now().Add(-time.Second)
					s.now = func() time.Time { return now }
				case "new-core":
					var err error
					h, err = NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
					if err != nil {
						t.Fatal(err)
					}
				}
				before := retainedRevisionState(t, s)
				body := any(c)
				if stage == "save" {
					body = r
				}
				w := proposalRequest(h, token, body, stage == "save")
				if (w.Code != 400 && w.Code != 409) || retainedRevisionState(t, s) != before {
					t.Fatal("invalid source/review partially wrote", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestWENStrategyProposalHTTPAuthorityAndSize(t *testing.T) {
	s, h, token, c := proposalFixture(t)
	body, _ := json.Marshal(c)
	for _, kind := range []string{"auth", "csrf", "origin", "method", "query", "unknown", "duplicate", "null-override", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			method, path, raw, auth, origin, csrf := "POST", "/api/v1/wen/research/proposal-review", string(body), token, "http://127.0.0.1:7331", csrfToken(token)
			switch kind {
			case "auth":
				auth = ""
			case "csrf":
				csrf = ""
			case "origin":
				origin = "https://foreign.invalid"
			case "method":
				method = "GET"
			case "query":
				path += "?unexpected=true"
			case "unknown":
				raw = strings.TrimSuffix(raw, "}") + `,"extra":1}`
			case "duplicate":
				raw = strings.TrimSuffix(raw, "}") + `,"name":"duplicate"}`
			case "null-override":
				raw = strings.TrimSuffix(raw, "}") + `,"changes":null}`
			case "oversized":
				raw = strings.Repeat("x", 65537)
			}
			before := retainedRevisionState(t, s)
			w := apiRequest(h, method, path, raw, auth, origin, csrf)
			if w.Code == 200 || retainedRevisionState(t, s) != before {
				t.Fatal("unsafe request wrote", w.Code)
			}
		})
	}
}

func TestWENStrategyProposalCompleteReviewCapacity(t *testing.T) {
	s, h, token, c := proposalFixture(t)
	all, revision, err := readWENLibraries(t.Context(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	library := all[c.Profile]
	raw := strings.Repeat(" ", 58000) + library.Versions[0].Raw
	a, err := wenstrategy.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	library.Versions[0].Raw = raw
	library.Versions[0].Digest = a.Digest
	all[c.Profile] = library
	b, _ := json.Marshal(all)
	if _, err = s.db.Exec("UPDATE wen_research SET document=?,revision=?", string(b), revision); err != nil {
		t.Fatal(err)
	}
	old := c.ParentDigest
	c.ParentDigest = a.Digest
	injectRevisionAgents(t, s, func(d *agentDocument) {
		d.AIRequests[0].Cards[0].Text = strings.Replace(d.AIRequests[0].Cards[0].Text, old, c.ParentDigest, 1)
	})
	before := retainedRevisionState(t, s)
	w := proposalRequest(h, token, c, false)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "complete strategy proposal review exceeds 64 KiB") || retainedRevisionState(t, s) != before {
		t.Fatal("large review clipped or wrote", w.Code, w.Body.String())
	}
}
