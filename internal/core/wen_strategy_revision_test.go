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

func revisionFixture(t *testing.T) (*Store, *HTTPHandler, string, WENStrategyRevisionInput) {
	t.Helper()
	s, h, token, library := savedChatFixture(t)
	review := readSavedReview(t, h, token, savedSelection(library))
	if w := postResearchChat(h, token, researchChatCommand(review)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	v, err := s.Agents(t.Context(), "wen-operator", "")
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Replace(researchRaw(t), "Fixture session draft", "Reviewed trend revision", 1)
	return s, h, token, WENStrategyRevisionInput{Profile: "wen-operator", Revision: v.Revision, LibraryRevision: library.Revision, SourceMessageID: v.Messages[0].ID, ParentDigest: library.Versions[0].Digest, Raw: raw}
}
func revisionRequest(h *HTTPHandler, token string, body any, save bool) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	route := "revision-review"
	if save {
		route = "revision-save"
	}
	return apiRequest(h, "POST", "/api/v1/wen/research/"+route, string(raw), token, "http://127.0.0.1:7331", csrfToken(token))
}
func revisionReview(t *testing.T, h *HTTPHandler, token string, c WENStrategyRevisionInput) WENStrategyRevisionReview {
	t.Helper()
	w := revisionRequest(h, token, c, false)
	var r WENStrategyRevisionReview
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &r) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return r
}
func retainedRevisionState(t *testing.T, s *Store) [2]string {
	t.Helper()
	var result [2]string
	if err := s.db.QueryRow("SELECT document || ':' || revision FROM agent_workspace WHERE id=1").Scan(&result[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT document || ':' || revision FROM wen_research WHERE id=1").Scan(&result[1]); err != nil {
		t.Fatal(err)
	}
	return result
}
func injectRevisionAgents(t *testing.T, s *Store, change func(*agentDocument)) {
	t.Helper()
	d, _, err := readAgents(t.Context(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	change(&d)
	raw, _ := json.Marshal(d)
	if _, err = s.db.Exec("UPDATE agent_workspace SET document=? WHERE id=1", string(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestWENStrategyRevisionSourceBytesAtomicSaveAndRestart(t *testing.T) {
	s, h, token, c := revisionFixture(t)
	before := retainedRevisionState(t, s)
	old, _, _ := readAgents(t.Context(), s.db)
	review := revisionReview(t, h, token, c)
	if retainedRevisionState(t, s) != before {
		t.Fatal("review wrote state")
	}
	if review.Source.Text != old.Messages[0].Text || review.SourceSHA256 != wenHash([]byte(old.Messages[0].Text)) || review.Candidate.Raw != c.Raw || review.Parent.Raw != researchRaw(t) {
		t.Fatal("source/original/candidate identity lost")
	}
	w := revisionRequest(h, token, review, true)
	var result WENStrategyRevisionResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if result.Library.Revision != c.LibraryRevision+1 || result.AgentRevision != c.Revision+1 || len(result.Library.Versions) != 2 || result.Library.Versions[0] != review.Parent || result.Library.Versions[1].Raw != c.Raw || result.Library.Versions[1].Digest != review.Candidate.Digest || result.Library.Versions[1].Origin != "wen-chat-revision-v1:"+result.Receipt.ID {
		t.Fatal("library lineage", result)
	}
	var receipt map[string]any
	if json.Unmarshal([]byte(strings.Split(result.Receipt.Text, "\n")[1]), &receipt) != nil || receipt["sourceMessageId"] != c.SourceMessageID || receipt["parentDigest"] != c.ParentDigest || receipt["candidateDigest"] != review.Candidate.Digest || receipt["sourceSha256"] != review.SourceSHA256 {
		t.Fatal("receipt lost source", receipt)
	}
	after, _, _ := readAgents(t.Context(), s.db)
	if len(after.Messages) != len(old.Messages)+1 || after.Messages[0] != old.Messages[0] || after.Messages[len(old.Messages)] != result.Receipt {
		t.Fatal("messages overwritten")
	}
	after.Messages = old.Messages
	if !reflect.DeepEqual(old, after) {
		t.Fatal("save changed tasks, permissions or other records")
	}
	if w = revisionRequest(h, token, review, true); w.Code != 409 {
		t.Fatal("duplicate saved", w.Code, w.Body.String())
	}
	dir := s.dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	l, err := n.WENResearch(t.Context(), c.Profile)
	if err != nil || l.Versions[0] != review.Parent || l.Versions[1] != result.Library.Versions[1] {
		t.Fatal("restart lineage", err)
	}
	v, _ := n.Agents(t.Context(), c.Profile, "")
	if v.Messages[len(v.Messages)-1] != result.Receipt || v.FinancialExecution || v.ProviderCallsEnabled || len(v.Tasks) != 0 || len(v.AIRequests) != 0 {
		t.Fatal("restart authority")
	}
	other, _ := n.WENResearch(t.Context(), "news-researcher")
	if len(other.Versions) != 0 {
		t.Fatal("foreign profile leak")
	}
}

func TestWENStrategyRevisionInputRefusals(t *testing.T) {
	for _, kind := range []string{"missing-source", "foreign-source", "system-source", "duplicate-source", "duplicate-session", "unknown-profile", "duplicate-profile", "foreign-parent", "parent-corrupt", "chat-revision", "library-revision", "invalid-document", "duplicate-document-field", "unchanged", "mandate", "automatic", "market"} {
		t.Run(kind, func(t *testing.T) {
			s, h, token, c := revisionFixture(t)
			switch kind {
			case "missing-source":
				c.SourceMessageID = "missing"
			case "foreign-source":
				c.Profile = "news-researcher"
			case "system-source":
				injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Role = "system" })
			case "duplicate-source":
				injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages = append(d.Messages, d.Messages[0]) })
			case "duplicate-session":
				injectRevisionAgents(t, s, func(d *agentDocument) { d.Sessions = append(d.Sessions, d.Sessions[0]) })
			case "unknown-profile":
				c.Profile = "missing"
			case "duplicate-profile":
				injectRevisionAgents(t, s, func(d *agentDocument) { d.Profiles = append(d.Profiles, d.Profiles[0]) })
			case "foreign-parent":
				c.ParentDigest = strings.Repeat("f", 64)
			case "parent-corrupt":
				all, rev, _ := readWENLibraries(t.Context(), s.db)
				l := all[c.Profile]
				l.Versions[0].Raw = strings.Replace(l.Versions[0].Raw, "Fixture session draft", "altered", 1)
				all[c.Profile] = l
				raw, _ := json.Marshal(all)
				if _, err := s.db.Exec("UPDATE wen_research SET document=?,revision=? WHERE id=1", string(raw), rev); err != nil {
					t.Fatal(err)
				}
			case "chat-revision":
				c.Revision--
			case "library-revision":
				c.LibraryRevision--
			case "invalid-document":
				c.Raw = "{}"
			case "duplicate-document-field":
				c.Raw = strings.Replace(c.Raw, `"schema":`, `"schema":"wrong","schema":`, 1)
			case "unchanged":
				c.Raw = researchRaw(t)
			case "mandate":
				c.Raw = strings.Replace(c.Raw, `"mandateId": null`, `"mandateId":"not-a-grant"`, 1)
			case "automatic":
				c.Raw = strings.Replace(c.Raw, `"mode": "review"`, `"mode":"automatic"`, 1)
			case "market":
				c.Raw = strings.Replace(c.Raw, "fixture-mint", "other-market", 1)
			}
			before := retainedRevisionState(t, s)
			w := revisionRequest(h, token, c, false)
			if w.Code != 400 && w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
			if retainedRevisionState(t, s) != before {
				t.Fatal("failed preparation wrote")
			}
		})
	}
}

func TestWENStrategyRevisionReviewRefusalsAndAtomicRollback(t *testing.T) {
	for _, kind := range []string{"candidate", "source", "profile", "witness", "expired", "new-core", "chat-changed", "library-changed", "source-bytes", "messages-full", "versions-full", "agent-document-full", "paused"} {
		t.Run(kind, func(t *testing.T) {
			s, h, token, c := revisionFixture(t)
			r := revisionReview(t, h, token, c)
			switch kind {
			case "candidate":
				r.Candidate.Raw = strings.Replace(r.Candidate.Raw, "Reviewed trend revision", "tampered", 1)
			case "source":
				r.Source.Text = "changed"
			case "profile":
				r.Profile = "news-researcher"
			case "witness":
				r.Witness = "forged"
			case "expired":
				later := s.now().Add(121 * time.Second)
				s.now = func() time.Time { return later }
			case "new-core":
				var err error
				h, err = NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
				if err != nil {
					t.Fatal(err)
				}
			case "chat-changed":
				_, err := s.ChangeAgents(t.Context(), AgentCommand{Action: "session-create", Revision: c.Revision, ProfileID: c.Profile, Name: "other"})
				if err != nil {
					t.Fatal(err)
				}
			case "library-changed":
				_, err := s.ChangeWENResearch(t.Context(), WENResearchCommand{Action: "save", Revision: c.LibraryRevision, Profile: c.Profile, Raw: strings.Replace(researchRaw(t), "Fixture session draft", "another", 1)})
				if err != nil {
					t.Fatal(err)
				}
			case "source-bytes":
				injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Text = "changed without revision" })
			case "messages-full":
				injectRevisionAgents(t, s, func(d *agentDocument) {
					for len(d.Messages) < 200 {
						d.Messages = append(d.Messages, AgentMessage{ID: fmt.Sprint("fill-", len(d.Messages)), ProfileID: c.Profile, SessionID: d.Sessions[0].ID, Role: "system", Text: "retained"})
					}
				})
			case "versions-full":
				all, rev, _ := readWENLibraries(t.Context(), s.db)
				l := all[c.Profile]
				for len(l.Versions) < 100 {
					raw := strings.Replace(researchRaw(t), "Fixture session draft", fmt.Sprint("fill-", len(l.Versions)), 1)
					a, err := wenstrategy.Decode([]byte(raw))
					if err != nil {
						t.Fatal(err)
					}
					l.Versions = append(l.Versions, WENVersion{a.Digest, raw, a.Document.Name, l.Versions[0].CreatedAt, "owner-import"})
				}
				all[c.Profile] = l
				raw, _ := json.Marshal(all)
				if _, err := s.db.Exec("UPDATE wen_research SET document=?,revision=? WHERE id=1", string(raw), rev); err != nil {
					t.Fatal(err)
				}
			case "agent-document-full":
				injectRevisionAgents(t, s, func(d *agentDocument) {
					raw, _ := json.Marshal(d)
					d.Profiles[0].Persona += strings.Repeat("x", agentDocumentLimit-len(raw)-80)
				})
			case "paused":
				p := pauseCommand(t, s, 1)
				if _, err := s.UpdatePause(t.Context(), p); err != nil {
					t.Fatal(err)
				}
			}
			before := retainedRevisionState(t, s)
			w := revisionRequest(h, token, r, true)
			if w.Code != 400 && w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
			if retainedRevisionState(t, s) != before {
				t.Fatal("partial strategy/receipt write")
			}
		})
	}
}

func TestWENStrategyRevisionHTTPAuthorityAndBounds(t *testing.T) {
	s, h, token, c := revisionFixture(t)
	raw, _ := json.Marshal(c)
	for _, tc := range []struct {
		name, method, path, body, auth, origin, csrf string
		status                                       int
	}{
		{"unauthenticated", "POST", "", string(raw), "", "http://127.0.0.1:7331", "", 401},
		{"csrf", "POST", "", string(raw), token, "http://127.0.0.1:7331", "", 403},
		{"origin", "POST", "", string(raw), token, "https://elsewhere.test", csrfToken(token), 403},
		{"query", "POST", "?bad=1", string(raw), token, "http://127.0.0.1:7331", csrfToken(token), 400},
		{"method", "GET", "", "", token, "http://127.0.0.1:7331", "", 405},
		{"unknown", "POST", "", strings.TrimSuffix(string(raw), "}") + `,"grant":true}`, token, "http://127.0.0.1:7331", csrfToken(token), 400},
		{"duplicate", "POST", "", strings.TrimSuffix(string(raw), "}") + `,"profile":"wen-operator"}`, token, "http://127.0.0.1:7331", csrfToken(token), 400},
		{"too-large", "POST", "", strings.Repeat("x", 512*1024+1), token, "http://127.0.0.1:7331", csrfToken(token), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := retainedRevisionState(t, s)
			w := apiRequest(h, tc.method, "/api/v1/wen/research/revision-review"+tc.path, tc.body, tc.auth, tc.origin, tc.csrf)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if retainedRevisionState(t, s) != before {
				t.Fatal("authority refusal wrote")
			}
		})
	}
	t.Run("escaped-response", func(t *testing.T) {
		injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Text = strings.Repeat("<", 3990) })
		parent := researchRaw(t)
		parent += strings.Repeat("\t", 65536-len(parent))
		l, err := s.ChangeWENResearch(t.Context(), WENResearchCommand{Action: "save", Profile: c.Profile, Revision: c.LibraryRevision, Raw: parent})
		if err != nil {
			t.Fatal(err)
		}
		c.ParentDigest = l.Versions[len(l.Versions)-1].Digest
		c.LibraryRevision = l.Revision
		c.Raw = strings.Replace(researchRaw(t), "Fixture session draft", "large candidate", 1)
		before := retainedRevisionState(t, s)
		w := revisionRequest(h, token, c, false)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "64 KiB") {
			t.Fatal(w.Code, w.Body.String())
		}
		if retainedRevisionState(t, s) != before {
			t.Fatal("response bound wrote")
		}
	})
}
