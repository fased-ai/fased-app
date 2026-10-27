package core

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestTaskReviewIsReadOnlyAndBounded(t *testing.T) {
	s := fixtureStore(t)
	c := AgentCommand{Action: "task-review", ProfileID: "trading-analyst", Name: "Journal review", Text: "Review saved results", IntervalSeconds: 3600, MaxRuns: 3}
	before, _ := s.Agents(t.Context(), c.ProfileID, "")
	c.Revision = before.Revision
	v, err := s.ChangeAgents(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := s.Agents(t.Context(), c.ProfileID, "")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("review wrote records")
	}
	b, _ := json.Marshal(v)
	var wire map[string]json.RawMessage
	_ = json.Unmarshal(b, &wire)
	if len(wire["taskReview"]) == 0 {
		t.Fatal("missing shared task review")
	}
}

func TestTaskReviewedCreateRejectsChangedExpiredAndForeignPlans(t *testing.T) {
	s := fixtureStore(t)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	c := AgentCommand{Action: "task-review", ProfileID: "trading-analyst", Name: "Journal review", Text: "Review saved results", IntervalSeconds: 3600, MaxRuns: 3}
	v := agentApply(t, s, c)
	b, _ := json.Marshal(v)
	var wire struct {
		Review struct {
			Digest    string `json:"digest"`
			ExpiresAt int64  `json:"expiresAt"`
		} `json:"taskReview"`
	}
	_ = json.Unmarshal(b, &wire)
	c.Action, c.Revision, c.ConsentDigest, c.ExpiresAt = "task-reviewed-create", v.Revision, wire.Review.Digest, wire.Review.ExpiresAt
	for _, alter := range []func(*AgentCommand){
		func(x *AgentCommand) { x.Text = "Changed job" },
		func(x *AgentCommand) { x.IntervalSeconds = 60 },
		func(x *AgentCommand) { x.MaxRuns++ },
		func(x *AgentCommand) { x.ProfileID = "news-researcher" },
		func(x *AgentCommand) { x.ConnectionRef = "unexpected-ai" },
		func(x *AgentCommand) { x.ExpiresAt = now.Unix() },
	} {
		bad := c
		alter(&bad)
		if _, err := s.ChangeAgents(t.Context(), bad); err == nil {
			t.Fatal("accepted altered review", bad)
		}
	}
	v, err := s.ChangeAgents(t.Context(), c)
	if err != nil || len(v.Tasks) != 1 || len(v.Runs) != 0 {
		t.Fatal("creation", v, err)
	}
	if _, err = s.ChangeAgents(t.Context(), c); err == nil {
		t.Fatal("replayed review")
	}
	if v.Tasks[0].NextAt != now.Unix()+3600 {
		t.Fatal("wrong first interval")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := OpenStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	v, err = n.Agents(t.Context(), c.ProfileID, "")
	if err != nil || len(v.Tasks) != 1 || len(v.Runs) != 0 {
		t.Fatal("lost task on restart", err)
	}
}

func TestTaskReviewCapacityExpiryAndExecutionBindings(t *testing.T) {
	d := initialAgents()
	c := AgentCommand{Action: "task-review", ProfileID: "trading-analyst", Name: "Fixture reminder", Text: "Review result", IntervalSeconds: 60, MaxRuns: 1}
	r, err := reviewAgentTask(d, c, c.ProfileID, 7, 1000)
	if err != nil || r.ExpiresAt != 1600 {
		t.Fatal(r, err)
	}
	c.Action, c.ExpiresAt = "task-reviewed-create", r.ExpiresAt
	if _, err = reviewAgentTask(d, c, c.ProfileID, 7, 1601); err == nil {
		t.Fatal("accepted expired review")
	}
	for _, alter := range []func(*AgentCommand){
		func(x *AgentCommand) { x.Limits = &AILimits{} },
		func(x *AgentCommand) { x.Model = "unexpected" },
		func(x *AgentCommand) { x.TotalBudgetMicrousd = 1 },
		func(x *AgentCommand) { x.WEN = &WENPaperInput{} },
		func(x *AgentCommand) { x.ID = "foreign-task" },
		func(x *AgentCommand) { x.SessionID = "foreign-session" },
	} {
		bad := c
		alter(&bad)
		if _, err = reviewAgentTask(d, bad, c.ProfileID, 7, 1000); err == nil {
			t.Fatal("accepted execution binding", bad)
		}
	}
	for _, fill := range []func(*agentDocument){
		func(x *agentDocument) { x.Tasks = make([]AgentTask, 100) },
		func(x *agentDocument) { x.Tasks = []AgentTask{{MaxRuns: 200}} },
		func(x *agentDocument) { x.Runs = make([]AgentRun, 200) },
		func(x *agentDocument) { x.Notices = make([]AgentNotice, 200) },
	} {
		full := initialAgents()
		fill(&full)
		if _, err = reviewAgentTask(full, c, c.ProfileID, 7, 1000); err == nil {
			t.Fatal("accepted full capacity")
		}
	}
}
