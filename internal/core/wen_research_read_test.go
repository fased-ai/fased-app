package core

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWENResearchReadHTTPReadOnly(t *testing.T) {
	s, h, token, c := revisionFixture(t)
	d, _, _ := readAgents(t.Context(), s.db)
	m := d.Messages[0]
	before := retainedRevisionState(t, s)
	body, _ := json.Marshal(map[string]any{
		"schema": "wen-research-read/1", "profile": c.Profile, "sessionId": m.SessionID,
		"revision": c.Revision, "libraryRevision": c.LibraryRevision,
		"sources": []any{map[string]any{"refId": "selected", "kind": "research-message", "messageId": m.ID, "textSHA256": wenHash([]byte(m.Text))}},
		"calls":   []any{map[string]any{"id": "read", "tool": "wen.research.snapshot", "sourceRefs": []string{"selected"}}},
	})
	w := apiRequest(h, "POST", "/api/v1/wen/research/read", string(body), token, "http://127.0.0.1:7331", csrfToken(token))
	if w.Code != 200 {
		t.Fatalf("selected read: HTTP %d: %s", w.Code, w.Body.String())
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("read changed retained records")
	}
}

func readBatchFixture(t *testing.T) (*Store, *HTTPHandler, string, WENResearchReadInput) {
	t.Helper()
	s, h, token, c := revisionFixture(t)
	injectRevisionAgents(t, s, func(d *agentDocument) {
		d.Messages[0].Text = "Frozen research\n{\"schema\":\"wen-research-chat-context/1\",\"quoteEventAt\":\"2026-10-22T11:59:58Z\",\"quoteReceivedAt\":\"2026-10-22T11:59:59Z\"}"
	})
	library, err := s.ChangeWENResearch(t.Context(), WENResearchCommand{Profile: c.Profile, Revision: c.LibraryRevision, Action: "save", Raw: c.Raw})
	if err != nil {
		t.Fatal(err)
	}
	d, rev, _ := readAgents(t.Context(), s.db)
	m := d.Messages[0]
	index := 0
	raw, _ := json.Marshal(library.Bundles[0])
	input := WENResearchReadInput{Schema: "wen-research-read/1", Profile: c.Profile, SessionID: m.SessionID, Revision: rev, LibraryRevision: library.Revision,
		Sources: []WENReadSource{{RefID: "snapshot", Kind: "research-message", MessageID: m.ID, TextSHA256: wenHash([]byte(m.Text))}, {RefID: "v1", Kind: "strategy", Digest: library.Versions[0].Digest}, {RefID: "v2", Kind: "strategy", Digest: library.Versions[1].Digest}, {RefID: "history", Kind: "history", BundleIndex: &index, StoredBundleSHA256: wenHash(raw)}},
		Calls:   []WENReadCall{{ID: "snapshot", Tool: "wen.research.snapshot", SourceRefs: []string{"snapshot"}}, {ID: "inspect", Tool: "wen.strategy.inspect", SourceRefs: []string{"v1"}}, {ID: "history", Tool: "wen.history.summary", SourceRefs: []string{"history"}, StrategyRef: "v1"}, {ID: "compare", Tool: "wen.strategy.compare", SourceRefs: []string{"v1", "v2"}}}}
	return s, h, token, input
}
func postRead(t *testing.T, h *HTTPHandler, token string, c WENResearchReadInput) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(c)
	return apiRequest(h, "POST", "/api/v1/wen/research/read", string(raw), token, "http://127.0.0.1:7331", csrfToken(token))
}
func TestWENResearchReadExactResultsNoWriteAndRestart(t *testing.T) {
	s, h, token, c := readBatchFixture(t)
	before := retainedRevisionState(t, s)
	w := postRead(t, h, token, c)
	var result WENResearchReadResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Status != "complete" {
		t.Fatal(w.Code, w.Body.String())
	}
	if result.Usage.Attempted != 4 || result.Usage.Completed != 4 || result.Usage.Failed != 0 || result.Usage.NotRun != 0 || len(w.Body.Bytes()) > 65536 {
		t.Fatal(result.Usage)
	}
	digest := result.ResultSHA256
	// Data is decoded as any here: re-marshalling would sort nested map keys
	// differently from the typed server payload. Verify exact wire bytes.
	wire := strings.TrimSpace(w.Body.String())
	suffix := `,"resultSHA256":"` + digest + `"}`
	if !strings.HasSuffix(wire, suffix) || wenHash([]byte(strings.TrimSuffix(wire, suffix)+"}")) != digest {
		t.Fatal("response hash differs from server bytes")
	}
	var raw []byte
	a := result.Sources[0]
	if a.Verified || a.Freshness != "historical" || a.ObservedAt == nil || *a.ObservedAt != "2026-10-22T11:59:58Z" || a.RetrievedAt == nil {
		t.Fatal("source time refreshed", a)
	}
	for _, r := range result.Results {
		encoded, _ := json.Marshal(r)
		if len(encoded) > 4000 {
			t.Fatal("result cap bypass")
		}
	}
	raw, _ = json.Marshal(result.Results[2].Data)
	var history savedChatHistory
	_ = json.Unmarshal(raw, &history)
	if history.Summary["netSOL"] != "-100001015" || history.RelevantRows != 8 || history.Reverted != 1 || len(history.Gaps) != 2 || history.Verified || history.Skipped != nil || history.PendingExposure != nil {
		t.Fatal(string(raw))
	}
	raw, _ = json.Marshal(result.Results[1].Data)
	var inspection struct {
		Strategy savedChatStrategy `json:"strategy"`
		Amounts  [5]string         `json:"amountsLamports"`
	}
	_ = json.Unmarshal(raw, &inspection)
	expected, _ := wenstrategy.CompileAllocation(inspection.Strategy.Document.Limits.PerRoundLamports, inspection.Strategy.Document.Allocation.WeightsBps)
	if inspection.Amounts != expected || inspection.Strategy.Digest != c.Sources[1].Digest {
		t.Fatal("F1 parity lost")
	}
	raw, _ = json.Marshal(result.Results[3].Data)
	if !strings.Contains(string(raw), `"kind":"settings-only"`) || !strings.Contains(string(raw), `"financialPreview":null`) || !strings.Contains(string(raw), `"everyNthRound":3`) {
		t.Fatal(string(raw))
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("read wrote product records")
	}
	s.now = func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) }
	again, err := s.WENResearchRead(t.Context(), c)
	if err != nil || *again.Sources[0].ObservedAt != *a.ObservedAt || again.PreparedAt == result.PreparedAt {
		t.Fatal("historical source changed on reopening", err)
	}
	dir := s.dir
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	recovered, err := n.WENResearchRead(t.Context(), c)
	if err != nil || recovered.Status != "complete" || retainedRevisionState(t, n) != before {
		t.Fatal("read/restart changed records", err)
	}
}
func TestWENResearchReadStrictSelectorsAndByteBound(t *testing.T) {
	_, _, _, c := readBatchFixture(t)
	raw, _ := json.Marshal(c)
	cases := map[string]string{
		"unknown-field": strings.Replace(string(raw), `"schema":`, `"unknown":1,"schema":`, 1), "duplicate": strings.Replace(string(raw), `"schema":`, `"schema":"wen-research-read/1","schema":`, 1),
		"alias": strings.Replace(string(raw), `"profile":`, `"Profile":`, 1), "null-field": strings.Replace(string(raw), `"revision":1`, `"revision":null`, 1),
		"null-optional": strings.Replace(string(raw), `"strategyRef":"v1"`, `"strategyRef":null`, 1), "null-sources": strings.Replace(string(raw), `"sourceRefs":["snapshot"]`, `"sourceRefs":null`, 1),
		"fractional": strings.Replace(string(raw), `"revision":1`, `"revision":1.2`, 1), "unknown-tool": strings.Replace(string(raw), "wen.strategy.inspect", "arbitrary.http", 1),
		"cross-conversation": strings.Replace(string(raw), `"messageId":`, `"sessionId":"other","messageId":`, 1), "url": strings.Replace(string(raw), `"refId":"snapshot"`, `"refId":"https://example.org"`, 1),
		"extra-source-field": strings.Replace(string(raw), `"kind":"strategy"`, `"kind":"strategy","messageId":"x"`, 1), "wrong-case-digest": strings.Replace(string(raw), c.Sources[1].Digest, strings.ToUpper(c.Sources[1].Digest), 1),
		"extra-json": string(raw) + "{}", "invalid-utf8": string(append(append([]byte{}, raw...), 255)),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if b == string(raw) {
				t.Fatal("bad fixture did not change request")
			}
			if _, err := DecodeWENResearchRead([]byte(b)); err == nil {
				t.Fatal("accepted", b)
			}
		})
	}
	mutate := func(name string, change func(*WENResearchReadInput)) {
		t.Run(name, func(t *testing.T) {
			var x WENResearchReadInput
			_ = json.Unmarshal(raw, &x)
			change(&x)
			b, _ := json.Marshal(x)
			if _, err := DecodeWENResearchRead(b); err == nil {
				t.Fatal("accepted", string(b))
			}
		})
	}
	mutate("duplicate-source", func(c *WENResearchReadInput) { c.Sources = append(c.Sources, c.Sources[0]) })
	mutate("duplicate-call", func(c *WENResearchReadInput) { c.Calls = append(c.Calls, c.Calls[0]) })
	mutate("unused-source", func(c *WENResearchReadInput) { c.Calls = c.Calls[1:] })
	mutate("wrong-kind", func(c *WENResearchReadInput) { c.Calls[0].SourceRefs = []string{"v1"} })
	mutate("duplicate-compare", func(c *WENResearchReadInput) { c.Calls[3].SourceRefs = []string{"v1", "v1"} })
	mutate("invalid-filter", func(c *WENResearchReadInput) { c.Calls[2].StrategyRef = "snapshot" })
	mutate("negative-index", func(c *WENResearchReadInput) { *c.Sources[3].BundleIndex = -1 })
	mutate("missing-refs", func(c *WENResearchReadInput) { c.Calls[0].SourceRefs = nil })
	mutate("ninth-call", func(c *WENResearchReadInput) {
		for len(c.Calls) < 9 {
			item := c.Calls[0]
			item.ID = fmt.Sprint(len(c.Calls))
			c.Calls = append(c.Calls, item)
		}
	})
	t.Run("required-zero-revision", func(t *testing.T) {
		var root map[string]any
		_ = json.Unmarshal(raw, &root)
		delete(root, "revision")
		b, _ := json.Marshal(root)
		if _, e := DecodeWENResearchRead(b); e == nil {
			t.Fatal("missing required revision became zero")
		}
	})
	t.Run("exact-64-KiB", func(t *testing.T) {
		b := append(append([]byte{}, raw...), []byte(strings.Repeat(" ", 65536-len(raw)))...)
		if _, e := DecodeWENResearchRead(b); e != nil {
			t.Fatal(e)
		}
		if _, e := DecodeWENResearchRead(append(b, ' ')); e == nil {
			t.Fatal("oversize accepted")
		}
	})
}
func TestWENResearchReadSourceAdmissionAndNoDisclosure(t *testing.T) {
	cases := []struct {
		name   string
		code   int
		change func(*Store, *WENResearchReadInput)
	}{
		{"stale-chat", 409, func(_ *Store, c *WENResearchReadInput) { c.Revision++ }},
		{"stale-library", 409, func(_ *Store, c *WENResearchReadInput) { c.LibraryRevision++ }},
		{"missing-message", 404, func(_ *Store, c *WENResearchReadInput) { c.Sources[0].MessageID = "missing" }},
		{"wrong-message-hash", 409, func(_ *Store, c *WENResearchReadInput) { c.Sources[0].TextSHA256 = strings.Repeat("a", 64) }},
		{"wrong-history-hash", 409, func(_ *Store, c *WENResearchReadInput) { c.Sources[3].StoredBundleSHA256 = strings.Repeat("b", 64) }},
		{"missing-history", 404, func(_ *Store, c *WENResearchReadInput) { *c.Sources[3].BundleIndex = 99 }},
		{"foreign-session", 404, func(_ *Store, c *WENResearchReadInput) { c.SessionID = "foreign" }},
		{"foreign-profile", 404, func(_ *Store, c *WENResearchReadInput) { c.Profile = "news-researcher" }},
		{"missing-strategy", 404, func(_ *Store, c *WENResearchReadInput) { c.Sources[1].Digest = strings.Repeat("b", 64) }},
		{"duplicate-message", 404, func(s *Store, _ *WENResearchReadInput) {
			injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages = append(d.Messages, d.Messages[0]) })
		}},
		{"duplicate-session", 404, func(s *Store, _ *WENResearchReadInput) {
			injectRevisionAgents(t, s, func(d *agentDocument) { d.Sessions = append(d.Sessions, d.Sessions[0]) })
		}},
		{"duplicate-profile", 404, func(s *Store, _ *WENResearchReadInput) {
			injectRevisionAgents(t, s, func(d *agentDocument) {
				for _, p := range d.Profiles {
					if p.ID == "wen-operator" {
						d.Profiles = append(d.Profiles, p)
						break
					}
				}
			})
		}},
		{"non-user", 404, func(s *Store, _ *WENResearchReadInput) {
			injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Role = "system" })
		}},
		{"source-text-changed", 409, func(s *Store, _ *WENResearchReadInput) {
			injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages[0].Text = "Changed" })
		}},
		{"deleted", 404, func(s *Store, _ *WENResearchReadInput) {
			injectRevisionAgents(t, s, func(d *agentDocument) { d.Messages = nil })
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, h, token, c := readBatchFixture(t)
			tc.change(s, &c)
			before := retainedRevisionState(t, s)
			w := postRead(t, h, token, c)
			if w.Code != tc.code || strings.Contains(w.Body.String(), `"results"`) || retainedRevisionState(t, s) != before {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
func TestWENResearchReadHTTPAuthority(t *testing.T) {
	s, h, token, c := readBatchFixture(t)
	raw, _ := json.Marshal(c)
	before := retainedRevisionState(t, s)
	cases := []struct {
		name, method, query, body, auth, origin, csrf string
		code                                          int
	}{
		{"unauthenticated", "POST", "", string(raw), "", "http://127.0.0.1:7331", "", 401},
		{"method", "GET", "", string(raw), token, "http://127.0.0.1:7331", csrfToken(token), 405},
		{"query", "POST", "?profile=other", string(raw), token, "http://127.0.0.1:7331", csrfToken(token), 400},
		{"csrf", "POST", "", string(raw), token, "http://127.0.0.1:7331", "", 403},
		{"origin", "POST", "", string(raw), token, "https://foreign.example", csrfToken(token), 403},
		{"oversize", "POST", "", strings.Repeat(" ", 65537), token, "http://127.0.0.1:7331", csrfToken(token), 413},
		{"bad-body", "POST", "", "{}", token, "http://127.0.0.1:7331", csrfToken(token), 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := apiRequest(h, tc.method, "/api/v1/wen/research/read"+tc.query, tc.body, tc.auth, tc.origin, tc.csrf)
			if w.Code != tc.code {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	if retainedRevisionState(t, s) != before {
		t.Fatal("refusal wrote state")
	}
}
func TestWENResearchReadLimitsCancellationAndPartial(t *testing.T) {
	s, _, _, c := readBatchFixture(t)
	prov, sources, err := resolveWENRead(t.Context(), s.db, c)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(10, 0)
	base := WENResearchReadResult{Sources: prov}
	makeCalls := func(n int) WENResearchReadInput {
		x := c
		x.Calls = nil
		for i := 0; i < n; i++ {
			call := c.Calls[0]
			call.ID = fmt.Sprint(i)
			x.Calls = append(x.Calls, call)
		}
		return x
	}
	t.Run("eight-attempts-errors-count", func(t *testing.T) {
		now := start
		calls := 0
		r := runWENRead(t.Context(), makeCalls(8), base, sources, start, func() time.Time { return now }, func(context.Context, WENReadCall, map[string]wenReadResolved) (any, string) {
			calls++
			return nil, "unsupported"
		})
		if calls != 8 || r.Status != "partial" || r.Usage.Failed != 8 || r.Usage.Attempted != 8 {
			t.Fatal(r)
		}
	})
	t.Run("cancelled-late-completion", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := 0
		r := runWENRead(ctx, makeCalls(8), base, sources, start, func() time.Time { return start }, func(context.Context, WENReadCall, map[string]wenReadResolved) (any, string) {
			calls++
			cancel()
			return "must not survive", ""
		})
		if calls != 1 || r.Status != "cancelled" || r.Usage.NotRun != 7 || r.Results[0].Data != nil || r.Results[0].Error != "cancelled" {
			t.Fatal(r)
		}
	})
	t.Run("call-deadline", func(t *testing.T) {
		now := start
		r := runWENRead(t.Context(), makeCalls(1), base, sources, start, func() time.Time { return now }, func(context.Context, WENReadCall, map[string]wenReadResolved) (any, string) {
			now = now.Add(3 * time.Second)
			return "late", ""
		})
		if r.Results[0].Error != "deadline" || r.Results[0].Data != nil {
			t.Fatal(r)
		}
	})
	t.Run("batch-deadline", func(t *testing.T) {
		now := start.Add(89 * time.Second)
		calls := 0
		r := runWENRead(t.Context(), makeCalls(8), base, sources, start, func() time.Time { return now }, func(context.Context, WENReadCall, map[string]wenReadResolved) (any, string) {
			calls++
			now = now.Add(time.Second)
			return "late", ""
		})
		if calls != 1 || r.Usage.NotRun != 7 || r.Results[0].Error != "deadline" {
			t.Fatal(r)
		}
	})
	t.Run("result-exact-limit-and-overflow", func(t *testing.T) {
		call := makeCalls(1).Calls[0]
		probe := WENReadResult{ID: call.ID, Tool: call.Tool, SourceRefs: call.SourceRefs, Sources: []WENReadProvenance{sources["snapshot"].provenance}, Status: "ok", Data: "", ElapsedMS: 0}
		b, _ := json.Marshal(probe)
		n := 4000 - len(b)
		for _, extra := range []int{0, 1} {
			r := runWENRead(t.Context(), makeCalls(1), base, sources, start, func() time.Time { return start }, func(context.Context, WENReadCall, map[string]wenReadResolved) (any, string) {
				return strings.Repeat("x", n+extra), ""
			})
			if extra == 0 && r.Status != "complete" {
				t.Fatal("exact limit refused", r)
			}
			if extra == 1 && (r.Results[0].Error != "too_large" || r.Results[0].Data != nil) {
				t.Fatal("overflow clipped", r)
			}
		}
	})
	t.Run("independent-later-result", func(t *testing.T) {
		count := 0
		r := runWENRead(t.Context(), makeCalls(2), base, sources, start, func() time.Time { return start }, func(context.Context, WENReadCall, map[string]wenReadResolved) (any, string) {
			count++
			if count == 1 {
				return strings.Repeat("<", 4000), ""
			}
			return "complete second result", ""
		})
		if r.Usage.Completed != 1 || r.Usage.Failed != 1 || r.Results[1].Data != "complete second result" {
			t.Fatal(r)
		}
	})
	t.Run("pre-cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		r := runWENRead(ctx, makeCalls(2), base, sources, start, func() time.Time { return start }, func(context.Context, WENReadCall, map[string]wenReadResolved) (any, string) {
			t.Fatal("invoked after cancel")
			return nil, ""
		})
		if r.Usage.NotRun != 2 {
			t.Fatal(r)
		}
	})
}

func TestWENResearchReadCorruptionAndMetadataBounds(t *testing.T) {
	for _, kind := range []string{"corrupt-strategy", "duplicate-strategy", "verified-import", "oversized-metadata"} {
		t.Run(kind, func(t *testing.T) {
			s, h, token, c := readBatchFixture(t)
			all, _, err := readWENLibraries(t.Context(), s.db)
			if err != nil {
				t.Fatal(err)
			}
			library := all[c.Profile]
			want := 404
			switch kind {
			case "corrupt-strategy":
				library.Versions[0].Raw = strings.Replace(library.Versions[0].Raw, "Fixture session draft", "Mutated without new digest", 1)
			case "duplicate-strategy":
				library.Versions = append(library.Versions, library.Versions[0])
			case "verified-import":
				library.Bundles[0].Verified = true
			case "oversized-metadata":
				for i := 0; i < 30; i++ {
					library.Bundles[0].Gaps = append(library.Bundles[0].Gaps, strings.Repeat("x", 200))
				}
				raw, _ := json.Marshal(library.Bundles[0])
				c.Sources[3].StoredBundleSHA256 = wenHash(raw)
				want = 413
			}
			all[c.Profile] = library
			raw, _ := json.Marshal(all)
			if _, err = s.db.Exec("UPDATE wen_research SET document=? WHERE id=1", string(raw)); err != nil {
				t.Fatal(err)
			}
			before := retainedRevisionState(t, s)
			w := postRead(t, h, token, c)
			if w.Code != want || strings.Contains(w.Body.String(), `"results"`) || retainedRevisionState(t, s) != before {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
