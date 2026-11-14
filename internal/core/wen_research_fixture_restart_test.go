package core

import (
	"net/http"
	"testing"
)

func TestWENResearchFixtureQueuedRestart(t *testing.T) {
	s, _, _, selection := readBatchFixture(t)
	r := preparedResearch(t, s, researchInput(t, selection))
	if _, err := s.StartWENResearchAI(t.Context(), fixtureResearchConsent(t, s, r), true); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	n.now = s.now
	if n.bootID == s.bootID {
		t.Fatal("restart did not change boot identity")
	}
	calls := 0
	rt := aiTestTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		return (fixtureAITransport{}).RoundTrip(req)
	})
	if err := n.advanceAgentAI(t.Context(), rt, true); err != nil {
		t.Fatal(err)
	}
	d, _, err := readAgents(t.Context(), n.db)
	if err != nil {
		t.Fatal(err)
	}
	q, turn := d.AIRequests[0], d.ResearchTurns[0]
	if calls != 0 || q.State != "cancelled" || q.HeldMicrousd != 0 || q.SpentMicrousd != 0 || turn.Models[0].Attempted || turn.StartedAt != 0 || turn.DeadlineAt != 0 || taskAIRequestSlots(d) != 1 {
		t.Fatalf("queued approval crossed restart: calls=%d request=%s turn=%s hold=%d attempted=%t", calls, q.State, turn.State, q.HeldMicrousd, turn.Models[0].Attempted)
	}
	before := retainedRevisionState(t, n)
	if err := n.advanceAgentAI(t.Context(), rt, true); err != nil || calls != 0 || before != retainedRevisionState(t, n) {
		t.Fatal("restart cancellation replayed or changed state", err)
	}
	if _, err := n.StartWENResearchAI(t.Context(), fixtureResearchConsent(t, n, r), true); err == nil {
		t.Fatal("old approval was reusable after restart")
	}
}
