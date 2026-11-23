package news

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Source struct {
	ID            string `json:"id"`
	ProfileID     string `json:"profileId"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	MaxAgeSeconds int64  `json:"maxAgeSeconds"`
	Disabled      bool   `json:"disabled"`
}

type Item struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	PublishedAt int64  `json:"publishedAt"`
}

type Fetch struct {
	Fresh         bool   `json:"fresh"`
	ID            string `json:"id"`
	ProfileID     string `json:"profileId"`
	SourceID      string `json:"sourceId"`
	TaskID        string `json:"taskId,omitempty"`
	URL           string `json:"url"`
	MaxAgeSeconds int64  `json:"maxAgeSeconds"`
	CreatedAt     int64  `json:"createdAt"`
	FetchedAt     int64  `json:"fetchedAt"`
	State         string `json:"state"`
	DispatchBoot  string `json:"dispatchBoot"`
	BodySHA256    string `json:"bodySha256"`
	Bytes         int    `json:"bytes"`
	Items         []Item `json:"items"`
	Failure       string `json:"failure"`
}

// Evidence refers to the retained immutable snapshot, avoiding copies of articles.
type Evidence struct {
	MaxAgeSeconds int64  `json:"maxAgeSeconds"`
	SourceID      string `json:"sourceId"`
	FetchID       string `json:"fetchId"`
	URL           string `json:"url"`
	FetchedAt     int64  `json:"fetchedAt"`
	BodySHA256    string `json:"bodySha256"`
	Digest        string `json:"digest"`
}

type TaskInput struct {
	SourceID      string `json:"sourceId"`
	URL           string `json:"url"`
	MaxAgeSeconds int64  `json:"maxAgeSeconds"`
	PendingID     string `json:"pendingId"`
	FetchID       string `json:"fetchId"`
}

// Hash computes the lowercase SHA-256 hex digest of raw feed bytes or evidence payloads.
func Hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ValidateURL parses and admits only public HTTPS feed URLs without credentials, queries, fragments, or private suffixes.
func ValidateURL(raw string) (*url.URL, error) {
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

// PublicIP verifies that resolved feed IP addresses are publicly routable unicast addresses.
func PublicIP(a netip.Addr) bool {
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

// PublicTransport wraps the restricted HTTPS transport used for public RSS/Atom feeds.
type PublicTransport struct{ Transport *http.Transport }

// NewPublicTransport builds a restricted SSRF-safe HTTPS RoundTripper for RSS/Atom feed fetching.
func NewPublicTransport() http.RoundTripper {
	return &PublicTransport{Transport: &http.Transport{
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
				if !PublicIP(ip) {
					return nil, errors.New("private news destination refused")
				}
			}
			d := net.Dialer{Timeout: time.Second}
			return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}}
}

func (p *PublicTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if _, e := ValidateURL(r.URL.String()); e != nil || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || strings.HasSuffix(strings.ToLower(r.URL.Hostname()), ".invalid") {
		return nil, errors.New("news request refused")
	}
	return p.Transport.RoundTrip(r)
}

// ParseFeed parses and strictly bounds an RSS 2.0 or Atom 1.0 feed payload.
func ParseFeed(b []byte, f Fetch, now int64, validTitle func(string) bool) (Fetch, error) {
	if len(b) > 65536 || len(b) == 0 {
		return f, errors.New("news body exceeds bound")
	}
	f.BodySHA256, f.Bytes, f.FetchedAt = Hash(b), len(b), now
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
	type feedParsedItem struct {
		title     string
		link      string
		published string
	}
	var parsedItems []feedParsedItem

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
	if xml.Unmarshal(b, &rss) == nil && rss.XMLName.Local == "rss" {
		if rss.Version != "2.0" || len(rss.Channels) != 1 || len(rss.Channels[0].Items) == 0 || len(rss.Channels[0].Items) > 32 {
			return f, errors.New("bounded RSS 2.0 channel required")
		}
		for _, x := range rss.Channels[0].Items {
			parsedItems = append(parsedItems, feedParsedItem{title: x.Title, link: x.Link, published: x.Published})
		}
	} else {
		var atom struct {
			XMLName xml.Name `xml:"feed"`
			Entries []struct {
				Title     string `xml:"title"`
				Published string `xml:"published"`
				Updated   string `xml:"updated"`
				Links     []struct {
					Href string `xml:"href,attr"`
					Rel  string `xml:"rel,attr"`
				} `xml:"link"`
			} `xml:"entry"`
		}
		if xml.Unmarshal(b, &atom) != nil || atom.XMLName.Local != "feed" || (atom.XMLName.Space != "" && atom.XMLName.Space != "http://www.w3.org/2005/Atom") || len(atom.Entries) == 0 || len(atom.Entries) > 32 {
			return f, errors.New("bounded RSS 2.0 or Atom 1.0 feed required")
		}
		for _, e := range atom.Entries {
			link := ""
			for _, l := range e.Links {
				rel := strings.ToLower(strings.TrimSpace(l.Rel))
				if rel == "alternate" {
					link = l.Href
					break
				}
				if rel == "" && link == "" {
					link = l.Href
				}
			}
			pub := strings.TrimSpace(e.Published)
			if pub == "" {
				pub = strings.TrimSpace(e.Updated)
			}
			parsedItems = append(parsedItems, feedParsedItem{title: e.Title, link: link, published: pub})
		}
	}

	items := []Item{}
	seen := map[string]bool{}
	for _, x := range parsedItems {
		title, link := strings.TrimSpace(x.title), strings.TrimSpace(x.link)
		if validTitle == nil || !validTitle(title) {
			return f, errors.New("RSS headline outside bound")
		}
		if _, e := ValidateURL(link); e != nil {
			return f, errors.New("RSS item requires public HTTPS link")
		}
		var published time.Time
		var e error
		for _, layout := range []string{time.RFC3339, time.RFC3339Nano, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
			if (layout == time.RFC1123 || layout == time.RFC822) && !strings.HasSuffix(x.published, " GMT") && !strings.HasSuffix(x.published, " UTC") {
				continue
			}
			published, e = time.Parse(layout, strings.TrimSpace(x.published))
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
		item := Item{Title: title, URL: link, PublishedAt: published.Unix()}
		encoded, _ := json.Marshal(item)
		item.ID = Hash(append([]byte(f.SourceID+":"), encoded...))
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
	f.Items, f.BodySHA256, f.Bytes, f.FetchedAt, f.State = items, Hash(b), len(b), now, "completed"
	f.Failure = ""
	return f, nil
}
