package news

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestValidateURLAndPublicIP(t *testing.T) {
	t.Parallel()
	if _, err := ValidateURL("https://news.example.com/feed.xml"); err != nil {
		t.Fatalf("unexpected rejection of valid public HTTPS feed: %v", err)
	}
	for _, bad := range []string{
		"http://news.example.com/feed.xml",
		"https://1.1.1.1/feed.xml",
		"https://feed.local/rss.xml",
		"https://news.internal/rss.xml",
		"https://user:pass@news.example.com/feed.xml",
		"https://news.example.com/feed.xml?token=1",
		"https://news.example.com/feed.xml#frag",
		"https://news.example.com:8443/feed.xml",
	} {
		if _, err := ValidateURL(bad); err == nil {
			t.Fatalf("admitted disallowed feed URL %q", bad)
		}
	}
	if !PublicIP(netip.MustParseAddr("1.1.1.1")) || !PublicIP(netip.MustParseAddr("2606:4700:4700::1111")) {
		t.Fatal("rejected valid public unicast IPs")
	}
	for _, priv := range []string{"127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "::1", "fc00::1", "fe80::1"} {
		if PublicIP(netip.MustParseAddr(priv)) {
			t.Fatalf("admitted non-public IP %s", priv)
		}
	}
	rt := NewPublicTransport()
	req, _ := http.NewRequest(http.MethodGet, "https://test.fased.invalid/rss.xml", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("transport admitted .invalid host")
	}
}

func TestParseFeedRSSAtomAndSecurityLimits(t *testing.T) {
	t.Parallel()
	now := int64(1735689600) // 2025-01-01T00:00:00Z
	validTitle := func(s string) bool { return len(s) > 0 && len(s) <= 240 }

	rssXML := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Feed</title>
    <item><title>First Headline</title><link>https://news.example.com/1</link><pubDate>Tue, 31 Dec 2024 23:55:00 GMT</pubDate></item>
    <item><title>Second Headline</title><link>https://news.example.com/2</link><pubDate>Tue, 31 Dec 2024 23:58:00 GMT</pubDate></item>
    <item><title>Dup Headline</title><link>https://news.example.com/2</link><pubDate>Tue, 31 Dec 2024 23:59:00 GMT</pubDate></item>
    <item><title>Stale Headline</title><link>https://news.example.com/old</link><pubDate>Tue, 31 Dec 2024 10:00:00 GMT</pubDate></item>
  </channel>
</rss>`)
	out, err := ParseFeed(rssXML, Fetch{SourceID: "src-1", MaxAgeSeconds: 3600}, now, validTitle)
	if err != nil {
		t.Fatalf("ParseFeed RSS failed: %v", err)
	}
	if out.State != "completed" || len(out.Items) != 2 || out.Items[0].URL != "https://news.example.com/2" {
		t.Fatalf("unexpected parsed RSS output: %+v", out)
	}

	atomXML := []byte(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <title>Atom Story</title>
    <link rel="enclosure" href="https://news.example.com/enclosure" />
    <link rel="alternate" href="https://news.example.com/atom-1" />
    <updated>2024-12-31T23:59:00Z</updated>
  </entry>
</feed>`)
	outAtom, err := ParseFeed(atomXML, Fetch{SourceID: "src-2", MaxAgeSeconds: 3600}, now, validTitle)
	if err != nil || len(outAtom.Items) != 1 || outAtom.Items[0].URL != "https://news.example.com/atom-1" {
		t.Fatalf("ParseFeed Atom failed: %+v err=%v", outAtom, err)
	}

	// Refuse XXE DTDs and future publication timestamps.
	xxe := []byte(`<?xml version="1.0"?><!DOCTYPE foo [<!ENTITY xxe "bad">]><rss version="2.0"><channel></channel></rss>`)
	if _, err := ParseFeed(xxe, Fetch{MaxAgeSeconds: 3600}, now, validTitle); err == nil {
		t.Fatal("admitted XML DOCTYPE")
	}
	future := []byte(`<rss version="2.0"><channel><item><title>Future</title><link>https://news.example.com/f</link><pubDate>2099-01-01T00:00:00Z</pubDate></item></channel></rss>`)
	if _, err := ParseFeed(future, Fetch{MaxAgeSeconds: 3600}, now, validTitle); err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("expected future refusal, got %v", err)
	}
}
