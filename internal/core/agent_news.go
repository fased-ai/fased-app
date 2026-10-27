package core

// Public RSS headlines are untrusted research data, never instructions, market
// facts or authority. No credentials, article crawling, model or service is used.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
)

const fixtureNewsURL = "https://news.fased.invalid/headlines.xml"
const newsResultReserve = 40 * 1024
const newsFetchLimit = 20

type NewsSource struct {
	ID            string `json:"id"`
	ProfileID     string `json:"profileId"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	MaxAgeSeconds int64  `json:"maxAgeSeconds"`
	Disabled      bool   `json:"disabled"`
}
type NewsItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	PublishedAt int64  `json:"publishedAt"`
}
type NewsFetch struct {
	Fresh         bool       `json:"fresh"`
	ID            string     `json:"id"`
	ProfileID     string     `json:"profileId"`
	SourceID      string     `json:"sourceId"`
	TaskID        string     `json:"taskId,omitempty"`
	URL           string     `json:"url"`
	MaxAgeSeconds int64      `json:"maxAgeSeconds"`
	CreatedAt     int64      `json:"createdAt"`
	FetchedAt     int64      `json:"fetchedAt"`
	State         string     `json:"state"`
	DispatchBoot  string     `json:"dispatchBoot"`
	BodySHA256    string     `json:"bodySha256"`
	Bytes         int        `json:"bytes"`
	Items         []NewsItem `json:"items"`
	Failure       string     `json:"failure"`
}

// Evidence refers to the retained immutable snapshot, avoiding copies of articles.
type NewsEvidence struct {
	MaxAgeSeconds int64  `json:"maxAgeSeconds"`
	SourceID      string `json:"sourceId"`
	FetchID       string `json:"fetchId"`
	URL           string `json:"url"`
	FetchedAt     int64  `json:"fetchedAt"`
	BodySHA256    string `json:"bodySha256"`
	Digest        string `json:"digest"`
}
type TaskNewsInput struct {
	SourceID      string `json:"sourceId"`
	URL           string `json:"url"`
	MaxAgeSeconds int64  `json:"maxAgeSeconds"`
	PendingID     string `json:"pendingId"`
	FetchID       string `json:"fetchId"`
}

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
func newsHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func newsURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 512 || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || u.Hostname() == "" || u.String() != raw {
		return nil, errors.New("public HTTPS feed without credentials, query or fragment required")
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") || strings.HasSuffix(host, ".") {
		return nil, errors.New("public DNS feed host required")
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".home", ".lan"} {
		if strings.HasSuffix(host, suffix) {
			return nil, errors.New("private feed host refused")
		}
	}
	for _, part := range strings.Split(host, ".") {
		if len(part) == 0 || len(part) > 63 || part[0] == '-' || part[len(part)-1] == '-' {
			return nil, errors.New("invalid feed host")
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return nil, errors.New("ASCII DNS host required")
			}
		}
	}
	if len(u.Host) > 253 {
		return nil, errors.New("feed host too long")
	}
	return u, nil
}
func newsPublicIP(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2002::/16", "2001::/32", "2001:2::/48", "2001:10::/28", "2001:20::/28"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return false
		}
	}
	return a.Is4() || netip.MustParsePrefix("2000::/3").Contains(a)
}

type publicNewsTransport struct{ transport *http.Transport }

func newPublicNewsTransport() http.RoundTripper {
	return &publicNewsTransport{transport: &http.Transport{
		Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 8192,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: time.Second, ResponseHeaderTimeout: 2 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, e := net.SplitHostPort(address)
			if e != nil || port != "443" {
				return nil, errors.New("news destination refused")
			}
			ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if e != nil || len(ips) == 0 {
				return nil, errors.New("news DNS unavailable")
			}
			// Validate all answers, then pin the dial to one checked IP; never resolve twice.
			for _, ip := range ips {
				if !newsPublicIP(ip) {
					return nil, errors.New("private news destination refused")
				}
			}
			d := net.Dialer{Timeout: time.Second}
			return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}}
}
func (p *publicNewsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if _, e := newsURL(r.URL.String()); e != nil || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || strings.HasSuffix(strings.ToLower(r.URL.Hostname()), ".invalid") {
		return nil, errors.New("news request refused")
	}
	return p.transport.RoundTrip(r)
}
func parseNewsRSS(b []byte, f NewsFetch, now int64) (NewsFetch, error) {
	if len(b) > 65536 || len(b) == 0 {
		return f, errors.New("news body exceeds bound")
	}
	f.BodySHA256, f.Bytes, f.FetchedAt = newsHash(b), len(b), now
	// Forbid DTDs/entities/processing instructions and excessive nesting before decode.
	decoder := xml.NewDecoder(bytes.NewReader(b))
	depth, tokens, roots := 0, 0, 0
	for {
		tok, e := decoder.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return f, errors.New("invalid RSS XML")
		}
		tokens++
		if tokens > 4096 {
			return f, errors.New("RSS token bound exceeded")
		}
		switch x := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
			if depth > 16 {
				return f, errors.New("RSS nesting bound exceeded")
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return f, errors.New("RSS directives refused")
		case xml.ProcInst:
			if x.Target != "xml" {
				return f, errors.New("RSS processing instruction refused")
			}
		}
	}
	if roots != 1 || depth != 0 {
		return f, errors.New("one complete RSS document required")
	}
	var rss struct {
		XMLName  xml.Name `xml:"rss"`
		Version  string   `xml:"version,attr"`
		Channels []struct {
			Items []struct {
				Title     string `xml:"title"`
				Link      string `xml:"link"`
				Published string `xml:"pubDate"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if xml.Unmarshal(b, &rss) != nil || rss.Version != "2.0" || len(rss.Channels) != 1 || len(rss.Channels[0].Items) == 0 || len(rss.Channels[0].Items) > 32 {
		return f, errors.New("bounded RSS 2.0 channel required")
	}
	items := []NewsItem{}
	seen := map[string]bool{}
	for _, x := range rss.Channels[0].Items {
		title, link := strings.TrimSpace(x.Title), strings.TrimSpace(x.Link)
		if !validAgentText(title, 240, true) {
			return f, errors.New("RSS headline outside bound")
		}
		if _, e := newsURL(link); e != nil {
			return f, errors.New("RSS item requires public HTTPS link")
		}
		var published time.Time
		var e error
		for _, layout := range []string{time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
			if (layout == time.RFC1123 || layout == time.RFC822) && !strings.HasSuffix(x.Published, " GMT") && !strings.HasSuffix(x.Published, " UTC") {
				continue
			}
			published, e = time.Parse(layout, strings.TrimSpace(x.Published))
			if e == nil {
				break
			}
		}
		if e != nil || published.Unix() > now {
			return f, errors.New("RSS publication time missing, invalid or future")
		}
		if now-published.Unix() > f.MaxAgeSeconds {
			continue
		}
		if seen[link] {
			continue
		}
		seen[link] = true
		item := NewsItem{Title: title, URL: link, PublishedAt: published.Unix()}
		encoded, _ := json.Marshal(item)
		item.ID = newsHash(append([]byte(f.SourceID+":"), encoded...))
		items = append(items, item)
	}
	if len(items) == 0 {
		return f, errors.New("RSS has no fresh headlines")
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].PublishedAt == items[j].PublishedAt {
			return items[i].URL < items[j].URL
		}
		return items[i].PublishedAt > items[j].PublishedAt
	})
	if len(items) > 5 {
		items = items[:5]
	}
	f.Items, f.BodySHA256, f.Bytes, f.FetchedAt, f.State = items, newsHash(b), len(b), now, "completed"
	f.Failure = ""
	return f, nil
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
	changed := false
	var dispatch *NewsFetch
	for i := range d.NewsFetches {
		f := &d.NewsFetches[i]
		if f.State == "fetching" {
			f.State = "unknown"
			f.Failure = "News dispatch was interrupted; outcome unknown, no replay."
			if e = settleNewsTask(&d, *f, s.now().Unix(), fixture); e != nil {
				return e
			}
			changed = true
		}
		if dispatch == nil && f.State == "queued" {
			source, err := newsSource(d, f.ProfileID, f.SourceID)
			if err != nil || source.URL != f.URL || !newsTaskPending(d, *f) {
				f.State = "cancelled"
				f.Failure = "News source disabled or dependent task stopped."
				if e = settleNewsTask(&d, *f, s.now().Unix(), fixture); e != nil {
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
	if dispatch == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-callCtx.Done():
				return
			case <-tick.C:
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
	for i, f := range d.NewsFetches {
		if f.ID == dispatch.ID && f.State == "fetching" && f.DispatchBoot == s.bootID {
			if _, err := newsSource(d, f.ProfileID, f.SourceID); err != nil || !newsTaskPending(d, f) {
				result.State = "cancelled"
				result.Failure = "News source disabled or task stopped."
			}
			d.NewsFetches[i] = result
			if e = settleNewsTask(&d, result, s.now().Unix(), fixture); e != nil {
				return e
			}
			if e = saveAgents(finish, tx, d, r); e != nil {
				return e
			}
			return tx.Commit()
		}
	}
	return nil
}

type fixtureNewsTransport struct{ now func() time.Time }

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
