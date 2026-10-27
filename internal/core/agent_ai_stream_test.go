package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func streamFixtureFrames(t *testing.T) string {
	t.Helper()
	text := `{"answer":"A bounded fixture answer","cards":[]}`
	response := map[string]any{"id": "fixture-response", "model": fixtureAIModel, "status": "completed", "service_tier": "default", "usage": map[string]int{"input_tokens": 100, "output_tokens": 20, "total_tokens": 120}, "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]string{"type": "output_text", "text": text}}}}}
	frames := []map[string]any{{"type": "response.created", "response": map[string]any{"id": "fixture-response", "model": fixtureAIModel}}, {"type": "response.output_text.delta", "delta": text, "output_index": 0, "content_index": 0}, {"type": "response.output_text.done", "text": text, "output_index": 0, "content_index": 0}, {"type": "response.completed", "response": response}}
	var b strings.Builder
	for i, frame := range frames {
		frame["sequence_number"] = i
		raw, _ := json.Marshal(frame)
		b.WriteString("event: " + frame["type"].(string) + "\ndata: " + string(raw) + "\n\n")
	}
	return b.String()
}
func TestAIStreamRejectsMalformedAndIncompleteEvents(t *testing.T) {
	good := streamFixtureFrames(t)
	cases := map[string]string{
		"valid":            good,
		"truncated":        good[:strings.Index(good, "event: response.completed")],
		"sequence":         strings.Replace(good, `"sequence_number":1`, `"sequence_number":0`, 1),
		"model":            strings.Replace(good, fixtureAIModel, "unreviewed-model", 1),
		"mismatched-event": strings.Replace(good, "event: response.output_text.delta", "event: error", 1),
		"identity":         strings.Replace(good, `"id":"fixture-response"`, `"id":"different"`, 1),
		"trailing-error":   good + "event: error\ndata: {\"type\":\"error\",\"sequence_number\":4}\n\n",
		"oversize":         good + strings.Repeat(":x\n", 140000),
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			seen := ""
			raw, e := readAIStream(strings.NewReader(wire), fixtureAIModel, func(v string) { seen = v })
			if name == "valid" {
				if e != nil || len(raw) == 0 || seen != "A bounded fixture answer" {
					t.Fatal("valid delta/final", seen, e)
				}
			} else if e == nil {
				t.Fatal("invalid stream accepted")
			}
		})
	}
}
func TestAIStreamPartialEscapesAndCredentialGuard(t *testing.T) {
	for raw, want := range map[string]string{`{"answer":"hello`: "hello", `{"answer":"line\nnext`: "line\nnext", `{"answer":"quoted\"`: "quoted\"", `{"answer":"backslash\`: "backslash", `{"cards":[],"answer":"later"}`: ""} {
		if got := partialAIAnswer(raw); got != want {
			t.Fatalf("%q => %q; want %q", raw, got, want)
		}
	}
	key := []byte("fased-test-stream-key")
	body := &guardedAIBody{body: io.NopCloser(io.MultiReader(strings.NewReader("before "), bytes.NewReader(key[:7]), bytes.NewReader(key[7:]), strings.NewReader(" after"))), key: append([]byte(nil), key...)}
	output, e := io.ReadAll(body)
	_ = body.Close()
	if e == nil || bytes.Contains(output, key) || strings.Contains(string(output), "fased-test-") {
		t.Fatal("split credential escaped", string(output), e)
	}
}
func TestAdmittedChatStreamsThroughControlledHTTPS(t *testing.T) {
	s := fixtureStore(t)
	connection := saveTestConnection(t, s)
	v := agentApply(t, s, AgentCommand{Action: "session-create", Name: "Admitted stream"})
	v = agentApply(t, s, AgentCommand{Action: "chat-review", ID: v.Sessions[0].ID, ConnectionRef: connection.Ref, Model: admittedOpenAIModel, MemorySelection: &AIMemorySelection{Mode: "relevant"}, Limits: &AILimits{MaxInputTokens: 24000, MaxOutputTokens: 1024, DeadlineSeconds: 5, BudgetMicrousd: 30000}, MaxRuns: 2, AuthorizationSeconds: 900, TotalBudgetMicrousd: 60000})
	p := *v.ChatReview
	v = agentApply(t, s, AgentCommand{Action: "chat-allow", ID: p.SessionID, ChatPermission: &p, ConsentDigest: p.ID})
	v = chatSend(t, s, v, p, "Explain the admitted output")
	paths := []string{}
	rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Error("credential missing")
		}
		copy := r.Clone(r.Context())
		copy.URL.Scheme = "https"
		copy.URL.Host = "api.openai.com"
		res, e := (fixtureAITransport{}).RoundTrip(copy)
		if e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		defer res.Body.Close()
		for key, value := range res.Header {
			w.Header()[key] = value
		}
		w.WriteHeader(res.StatusCode)
		b := make([]byte, 32)
		for {
			n, e := res.Body.Read(b)
			if n > 0 {
				if _, err := w.Write(b[:n]); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
			if e != nil {
				return
			}
		}
	})
	if e := s.advanceAgentAI(t.Context(), rt, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	q := v.AIRequests[0]
	if q.State != "completed" || q.Admission == nil || !q.Admission.GenerationAttempted || q.Admission.InputTokens != q.InputTokens || len(paths) != 3 {
		t.Fatal("admitted stream incomplete", q, paths)
	}
	count, e := openAICountWire(q)
	if e != nil || bytes.Contains(count, []byte(`"stream"`)) {
		t.Fatal("generation-only stream sent to token counter")
	}
}
