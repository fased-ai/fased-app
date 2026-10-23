package core

import (
	"encoding/json"
	"errors"
	"fmt"
)

func taskNewsPolicy(d agentDocument, c AgentCommand, profile string) (*TaskNewsInput, error) {
	if c.NewsSourceID == "" {
		if c.NewsMaxAgeSeconds != 0 {
			return nil, errors.New("news freshness requires a selected source")
		}
		return nil, nil
	}
	s, e := newsSource(d, profile, c.NewsSourceID)
	if e != nil {
		return nil, e
	}
	if c.NewsMaxAgeSeconds < 60 || c.NewsMaxAgeSeconds > s.MaxAgeSeconds {
		return nil, errors.New("news freshness must be within the selected source limit")
	}
	return &TaskNewsInput{SourceID: s.ID, URL: s.URL, MaxAgeSeconds: c.NewsMaxAgeSeconds}, nil
}
func newsPolicyForDigest(n *TaskNewsInput) *TaskNewsInput {
	if n == nil {
		return nil
	}
	copy := *n
	copy.PendingID = ""
	copy.FetchID = ""
	return &copy
}
func newsTaskPending(d agentDocument, f NewsFetch) bool {
	if f.TaskID == "" {
		return true
	}
	for _, t := range d.Tasks {
		if t.ID == f.TaskID && t.ProfileID == f.ProfileID && t.State == "waiting-news" && t.News != nil && t.News.PendingID == f.ID {
			return true
		}
	}
	return false
}
func holdNewsTask(d *agentDocument, i int, f NewsFetch, now int64) error {
	if len(d.Runs) >= 200 || len(d.Notices) >= 200 {
		return errors.New("news task journal/inbox capacity reached")
	}
	t := &d.Tasks[i]
	id := fmt.Sprintf("%s:%d", t.ID, t.Runs+1)
	run := AgentRun{ID: id, ProfileID: t.ProfileID, TaskID: t.ID, BotID: t.BotID, Strategy: "news-gate-v1", At: now, Outcome: "news-" + f.State}
	if f.ID != "" {
		run.News = newsEvidence(f)
	}
	d.Runs = append(d.Runs, run)
	d.Notices = append(d.Notices, AgentNotice{ID: id, ProfileID: t.ProfileID, RunID: id, At: now, Text: t.Name + ": news unavailable or stale; dependent work held. Review the source and resume explicitly."})
	t.Runs++
	t.State = "held"
	t.NextAt = 0
	t.News.PendingID = ""
	t.News.FetchID = ""
	if t.AI != nil {
		t.AI.PendingID = ""
	}
	return nil
}
func queueTaskNews(d *agentDocument, i int, now int64) error {
	t := &d.Tasks[i]
	source, e := newsSource(*d, t.ProfileID, t.News.SourceID)
	if e != nil || source.URL != t.News.URL {
		return holdNewsTask(d, i, NewsFetch{State: "source-disabled"}, now)
	}
	if newsBusy(*d, source.ID) {
		t.NextAt = now + 1
		return nil
	}
	if len(d.NewsFetches) >= newsFetchLimit {
		return errors.New("retained news capacity reached")
	}
	id := fmt.Sprintf("%s:%d-news", t.ID, t.Runs+1)
	for _, f := range d.NewsFetches {
		if f.ID == id {
			return errors.New("news request identity already exists; preserve history")
		}
	}
	f := newNewsFetch(id, source, now)
	f.TaskID = t.ID
	f.MaxAgeSeconds = t.News.MaxAgeSeconds
	d.NewsFetches = append(d.NewsFetches, f)
	t.State = "waiting-news"
	t.News.PendingID = id
	t.News.FetchID = ""
	return nil
}
func evaluateAgentTask(d *agentDocument, i int, now int64, fixture bool) error {
	t := d.Tasks[i]
	if t.WEN != nil {
		return runWENPaper(d, i, now)
	}
	if t.AI != nil {
		if !fixture {
			return taskAIHeld(d, i, "", "ai-disabled", now)
		}
		if validateTaskAI(*d, t, now) != nil {
			return taskAIHeld(d, i, "", "ai-consent-invalid", now)
		}
	}
	if t.News != nil {
		if t.News.FetchID != "" {
			if _, e := freshNews(*d, t.ProfileID, t.News, now); e != nil {
				return holdNewsTask(d, i, NewsFetch{State: "stale"}, now)
			}
			return evaluateTaskAfterNews(d, i, now, fixture)
		}
		return queueTaskNews(d, i, now)
	}
	return evaluateTaskAfterNews(d, i, now, fixture)
}
func settleNewsTask(d *agentDocument, f NewsFetch, now int64, fixture bool) error {
	if f.TaskID == "" {
		return nil
	}
	for i, t := range d.Tasks {
		if t.ID != f.TaskID || t.ProfileID != f.ProfileID || t.News == nil || t.State != "waiting-news" || t.News.PendingID != f.ID {
			continue
		}
		if f.State != "completed" {
			return holdNewsTask(d, i, f, now)
		}
		d.Tasks[i].News.PendingID = ""
		d.Tasks[i].News.FetchID = f.ID
		if _, e := freshNews(*d, t.ProfileID, d.Tasks[i].News, now); e != nil {
			f.State = "stale"
			return holdNewsTask(d, i, f, now)
		}
		d.Tasks[i].State = "scheduled"
		return evaluateTaskAfterNews(d, i, now, fixture)
	}
	return nil
}
func cancelNewsTask(d *agentDocument, i int, now int64) error {
	t := d.Tasks[i]
	for qi, f := range d.NewsFetches {
		if f.ID == t.News.PendingID && (f.State == "queued" || f.State == "fetching") {
			d.NewsFetches[qi].State = "cancelled"
			d.NewsFetches[qi].Failure = "Owner stopped news-dependent work."
			return holdNewsTask(d, i, d.NewsFetches[qi], now)
		}
	}
	return errors.New("pending news request missing")
}
func freshNewsEvidence(d agentDocument, profile string, evidence *NewsEvidence, now int64) (NewsFetch, error) {
	if evidence == nil {
		return NewsFetch{}, errors.New("news evidence required")
	}
	source, e := newsSource(d, profile, evidence.SourceID)
	if e != nil {
		return NewsFetch{}, e
	}
	f, e := freshNews(d, profile, &TaskNewsInput{SourceID: source.ID, URL: source.URL, MaxAgeSeconds: evidence.MaxAgeSeconds, FetchID: evidence.FetchID}, now)
	f.MaxAgeSeconds = evidence.MaxAgeSeconds
	if e != nil || *newsEvidence(f) != *evidence {
		return f, errors.New("news evidence changed or expired")
	}
	return f, nil
}
func addNewsContext(base string, f NewsFetch) (string, error) {
	var data map[string]any
	if json.Unmarshal([]byte(base), &data) != nil {
		return "", errors.New("invalid base AI context")
	}
	data["news"] = struct {
		Evidence  *NewsEvidence `json:"evidence"`
		Items     []NewsItem    `json:"headlines"`
		Untrusted bool          `json:"untrusted"`
	}{newsEvidence(f), f.Items, true}
	b, e := json.Marshal(data)
	if len(b) > 16000 {
		return "", errors.New("news context exceeds bound")
	}
	return string(b), e
}
func attachAINews(d agentDocument, q *AgentAIRequest, n *TaskNewsInput, now int64) error {
	f, e := freshNews(d, q.ProfileID, n, now)
	if e != nil {
		return e
	}
	f.MaxAgeSeconds = n.MaxAgeSeconds
	q.News = newsEvidence(f)
	q.Context, e = addNewsContext(q.Context, f)
	if e != nil {
		return e
	}
	wire, e := aiWire(*q)
	if e != nil || !aiWireWithinBound(*q, wire) {
		return errors.New("news exceeds reviewed input limit")
	}
	q.Disclosure = string(wire)
	q.Digest = aiDigest(*q)
	return nil
}
func aiRequestContext(d agentDocument, q AgentAIRequest, now int64) (string, error) {
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
