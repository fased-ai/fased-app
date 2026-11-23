package core

// Public RSS headlines are untrusted research data, never instructions, market
// facts or authority. No credentials, article crawling, model or service is used.
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/news"
)

const fixtureNewsURL = "https://news.fased.invalid/headlines.xml"
const newsResultReserve = 40 * 1024
const newsFetchLimit = 20

type NewsSource = news.Source
type NewsItem = news.Item
type NewsFetch = news.Fetch
type NewsEvidence = news.Evidence
type TaskNewsInput = news.TaskInput

func migrateAgentNews(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var phase string
	if e = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil {
		return e
	}
	if phase == "paused" {
		return errors.New("core metadata paused; finish recovery before news migration")
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA user_version=8"); e != nil {
		return e
	}
	return tx.Commit()
}
func newsHash(b []byte) string                  { return news.Hash(b) }
func newsURL(raw string) (*url.URL, error)      { return news.ValidateURL(raw) }
func newsPublicIP(a netip.Addr) bool            { return news.PublicIP(a) }
func newPublicNewsTransport() http.RoundTripper { return news.NewPublicTransport() }
func parseNewsRSS(b []byte, f NewsFetch, now int64) (NewsFetch, error) {
	return news.ParseFeed(b, f, now, func(title string) bool {
		return validAgentText(title, 240, true)
	})
}
func callNews(ctx context.Context, f NewsFetch, rt http.RoundTripper, now func() time.Time) NewsFetch {
	fail := func(reason string) NewsFetch {
		f.State = "failed"
		f.Failure = reason + "; dependent work stopped. No retry or cached fallback."
		if ctx.Err() != nil {
			f.State = "unknown"
		}
		return f
	}
	if rt == nil {
		return fail("HTTPS news request unavailable")
	}
	if _, e := newsURL(f.URL); e != nil {
		return fail("HTTPS news request unavailable")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", f.URL, nil)
	if e != nil {
		return fail("HTTPS news request unavailable")
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
	req.Header.Set("User-Agent", "Fased-News/1")
	c := http.Client{Transport: rt, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("news redirect refused") }}
	res, e := c.Do(req)
	if e != nil {
		return fail("HTTPS news request unavailable")
	}
	defer res.Body.Close()
	kind, _, e := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if e != nil || res.StatusCode != 200 || (kind != "application/rss+xml" && kind != "application/xml" && kind != "text/xml") || res.Header.Get("Content-Encoding") != "" {
		return fail(fmt.Sprintf("RSS HTTP/media/encoding refused (status %d)", res.StatusCode))
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, 65537))
	if e != nil {
		return fail("HTTPS news request unavailable")
	}
	parsed, e := parseNewsRSS(b, f, now().Unix())
	if e != nil {
		f = parsed
		return fail(e.Error())
	}
	return parsed
}
func newsSource(d agentDocument, profile, id string) (NewsSource, error) {
	for _, s := range d.NewsSources {
		if s.ID == id && s.ProfileID == profile && !s.Disabled {
			return s, nil
		}
	}
	return NewsSource{}, errors.New("active profile news source required")
}
func latestNews(d agentDocument, profile, source string) (NewsFetch, error) {
	for i := len(d.NewsFetches) - 1; i >= 0; i-- {
		f := d.NewsFetches[i]
		if f.ProfileID == profile && f.SourceID == source {
			return f, nil
		}
	}
	return NewsFetch{}, errors.New("news has not been fetched")
}
func newsEvidence(f NewsFetch) *NewsEvidence {
	b, _ := json.Marshal(struct {
		Source, Fetch, URL, Body string
		Age                      int64
		At                       int64
		Items                    []NewsItem
	}{f.SourceID, f.ID, f.URL, f.BodySHA256, f.MaxAgeSeconds, f.FetchedAt, f.Items})
	return &NewsEvidence{MaxAgeSeconds: f.MaxAgeSeconds, SourceID: f.SourceID, FetchID: f.ID, URL: f.URL, FetchedAt: f.FetchedAt, BodySHA256: f.BodySHA256, Digest: newsHash(b)}
}
func freshNews(d agentDocument, profile string, n *TaskNewsInput, now int64) (NewsFetch, error) {
	if n == nil {
		return NewsFetch{}, errors.New("news policy required")
	}
	s, e := newsSource(d, profile, n.SourceID)
	if e != nil || s.URL != n.URL || n.MaxAgeSeconds < 60 || n.MaxAgeSeconds > s.MaxAgeSeconds {
		return NewsFetch{}, errors.New("news source policy changed")
	}
	f, e := latestNews(d, profile, s.ID)
	if e != nil || f.State != "completed" || f.URL != s.URL || (n.FetchID != "" && n.FetchID != f.ID) || len(f.Items) == 0 || now < f.FetchedAt || now-f.FetchedAt > n.MaxAgeSeconds {
		return f, errors.New("latest news snapshot unavailable or stale")
	}
	for _, item := range f.Items {
		if now < item.PublishedAt || now-item.PublishedAt > n.MaxAgeSeconds {
			return f, errors.New("news publication stale or future")
		}
	}
	return f, nil
}
func newsBusy(d agentDocument, source string) bool {
	for _, f := range d.NewsFetches {
		if f.SourceID == source && (f.State == "queued" || f.State == "fetching") {
			return true
		}
	}
	return false
}
func newNewsFetch(id string, s NewsSource, now int64) NewsFetch {
	return NewsFetch{ID: id, ProfileID: s.ProfileID, SourceID: s.ID, URL: s.URL, MaxAgeSeconds: s.MaxAgeSeconds, CreatedAt: now, State: "queued", Items: []NewsItem{}}
}
func newsSlots(d agentDocument) int {
	n := len(d.NewsFetches)
	for _, t := range d.Tasks {
		if t.News != nil && (t.State == "scheduled" || t.State == "review" || t.State == "paused" || t.State == "waiting-news" || t.State == "waiting-ai") {
			left := t.MaxRuns - t.Runs
			if t.News.PendingID != "" || t.News.FetchID != "" || t.State == "waiting-ai" {
				left--
			}
			if left > 0 {
				n += left
			}
		}
	}
	return n
}
func newsStorage(d agentDocument) int {
	n := newsSlots(d) - len(d.NewsFetches)
	for _, f := range d.NewsFetches {
		if f.State == "queued" || f.State == "fetching" {
			n++
		}
	}
	return n * newsResultReserve
}
func changeNews(d *agentDocument, c AgentCommand, profile, id string, now int64) error {
	switch c.Action {
	case "news-source-add":
		if !validAgentText(c.Name, 80, true) || len(d.NewsSources) >= 10 || c.NewsMaxAgeSeconds < 60 || c.NewsMaxAgeSeconds > 86400 {
			return errors.New("source name, freshness 60–86400 seconds and available source capacity required")
		}
		if _, e := newsURL(c.Source); e != nil {
			return e
		}
		d.NewsSources = append(d.NewsSources, NewsSource{ID: id, ProfileID: profile, Name: c.Name, URL: c.Source, MaxAgeSeconds: c.NewsMaxAgeSeconds})
		return nil
	case "news-source-disable":
		for i, s := range d.NewsSources {
			if s.ID == c.ID && s.ProfileID == profile {
				d.NewsSources[i].Disabled = true
				return nil
			}
		}
		return errors.New("profile news source not found")
	case "news-fetch":
		s, e := newsSource(*d, profile, c.ID)
		if e != nil {
			return e
		}
		if newsSlots(*d) >= newsFetchLimit || newsBusy(*d, s.ID) {
			return errors.New("news fetch already pending or retained/reserved capacity reached")
		}
		d.NewsFetches = append(d.NewsFetches, newNewsFetch(id, s, now))
		return nil
	case "news-cancel":
		for i, f := range d.NewsFetches {
			if f.ID == c.ID && f.ProfileID == profile && (f.State == "queued" || f.State == "fetching") {
				d.NewsFetches[i].State = "cancelled"
				d.NewsFetches[i].Failure = "Cancelled by owner; no automatic retry."
				return settleNewsTask(d, d.NewsFetches[i], now, false)
			}
		}
	}
	return errors.New("profile news operation unavailable")
}
func (s *Store) advanceAgentNews(ctx context.Context, rt http.RoundTripper, fixture bool) error {
	if s.pilotSyntheticOnly() {
		return nil
	}
	if !s.newsMu.TryLock() {
		return nil
	}
	defer s.newsMu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var phase string
	if e = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil {
		return e
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	d, r, e := readAgents(ctx, tx)
	if e != nil {
		return e
	}
	beforeRuns := len(d.Runs)
	changed := false
	var dispatch *NewsFetch
	for i := range d.NewsFetches {
		f := &d.NewsFetches[i]
		if f.State == "fetching" {
			f.State = "unknown"
			f.Failure = "News dispatch was interrupted; outcome unknown, no replay."
			if e = settleNewsTask(&d, *f, s.now().Unix(), s.newsTaskAIAllowed(d, *f, fixture)); e != nil {
				return e
			}
			changed = true
		}
		if dispatch == nil && f.State == "queued" {
			source, err := newsSource(d, f.ProfileID, f.SourceID)
			if err != nil || source.URL != f.URL || !newsTaskPending(d, *f) {
				f.State = "cancelled"
				f.Failure = "News source disabled or dependent task stopped."
				if e = settleNewsTask(&d, *f, s.now().Unix(), s.newsTaskAIAllowed(d, *f, fixture)); e != nil {
					return e
				}
				changed = true
				continue
			}
			f.State = "fetching"
			f.DispatchBoot = s.bootID
			copy := *f
			dispatch = &copy
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if e = saveAgents(ctx, tx, d, r); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	mirrorTaskRunsToV2(ctx, s, d, beforeRuns)
	if dispatch == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fallback := time.NewTicker(time.Second)
		defer fallback.Stop()
		for {
			sig := storeChangeChan(s)
			select {
			case <-callCtx.Done():
				return
			case <-sig:
			case <-fallback.C:
			}
			current, _, err := readAgents(callCtx, s.db)
			active := false
			if err == nil {
				for _, f := range current.NewsFetches {
					if f.ID == dispatch.ID && f.State == "fetching" && newsTaskPending(current, f) {
						_, err = newsSource(current, f.ProfileID, f.SourceID)
						active = err == nil
					}
				}
			}
			var phase string
			if s.db.QueryRowContext(callCtx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase) != nil || phase == "paused" {
				active = false
			}
			if !active {
				cancel()
				return
			}
		}
	}()
	result := callNews(callCtx, *dispatch, rt, s.now)
	cancel()
	<-done
	finish, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	tx, e = s.db.BeginTx(finish, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	d, r, e = readAgents(finish, tx)
	if e != nil {
		return e
	}
	beforeRuns = len(d.Runs)
	for i, f := range d.NewsFetches {
		if f.ID == dispatch.ID && f.State == "fetching" && f.DispatchBoot == s.bootID {
			if _, err := newsSource(d, f.ProfileID, f.SourceID); err != nil || !newsTaskPending(d, f) {
				result.State = "cancelled"
				result.Failure = "News source disabled or task stopped."
			}
			d.NewsFetches[i] = result
			if e = settleNewsTask(&d, result, s.now().Unix(), s.newsTaskAIAllowed(d, result, fixture)); e != nil {
				return e
			}
			if e = saveAgents(finish, tx, d, r); e != nil {
				return e
			}
			if e = tx.Commit(); e != nil {
				return e
			}
			mirrorTaskRunsToV2(finish, s, d, beforeRuns)
			return nil
		}
	}
	return nil
}

type fixtureNewsTransport struct {
	now   func() time.Time
	store *Store
}

func (f fixtureNewsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Context().Err() != nil {
		return nil, r.Context().Err()
	}
	if r.URL.Host != "news.fased.invalid" || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
		return nil, errors.New("compiled news fixture only")
	}
	now := time.Now()
	if f.now != nil {
		now = f.now()
	}
	published := now.Add(-time.Minute)
	// Keep one compiled article's identity stable across fetches and restarts.
	// Only fixture mode supplies this Store; production transport is unchanged.
	if f.store != nil {
		d, _, err := readAgents(r.Context(), f.store.db)
		if err != nil {
			return nil, err
		}
		for _, saved := range d.NewsFetches {
			if saved.URL == r.URL.String() && saved.State == "completed" && len(saved.Items) > 0 {
				published = time.Unix(saved.Items[0].PublishedAt, 0)
				break
			}
		}
	}
	status := 200
	if r.URL.Path == "/unavailable.xml" {
		status = 503
	}
	if r.URL.Path == "/stale.xml" {
		published = now.Add(-48 * time.Hour)
	}
	if r.URL.Path != "/headlines.xml" && r.URL.Path != "/unavailable.xml" && r.URL.Path != "/stale.xml" {
		return nil, errors.New("unknown news fixture")
	}
	b := fmt.Sprintf(`<rss version="2.0"><channel><item><title>Controlled paper-news headline</title><link>https://example.com/paper-story</link><pubDate>%s</pubDate></item></channel></rss>`, published.UTC().Format(time.RFC3339))
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/rss+xml"}}, Body: io.NopCloser(strings.NewReader(b)), Request: r}, nil
}
