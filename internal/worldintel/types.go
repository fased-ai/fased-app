package worldintel

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"github.com/fased-ai/fased-app/internal/news"
)

const (
	CategoryMacro       = "macro"
	CategoryCrypto      = "crypto"
	CategoryGeopolitics = "geopolitics"
	CategoryConflict    = "conflict"
	CategoryEnergy      = "energy"
	CategorySeismic     = "seismic"
	CategoryPolicy      = "policy"
	CategoryPrediction  = "prediction"
	CategorySidecar     = "sidecar"

	RegionNA     = "na"
	RegionEU     = "eu"
	RegionAPAC   = "apac"
	RegionMENA   = "mena"
	RegionLATAM  = "latam"
	RegionAfrica = "africa"
	RegionGlobal = "global"

	KindRSS        = "rss"
	KindGDELT      = "gdelt"
	KindUSGS       = "usgs"
	KindPolymarket = "polymarket"
	KindKalshi     = "kalshi"
	KindSidecar    = "worldmonitor_sidecar"

	VenuePolymarket = "polymarket"
	VenueKalshi     = "kalshi"

	maxEventsRetained      = 2000
	maxPredictionsRetained = 500
	maxSources             = 64
	maxResponseBytes       = 2 << 20 // 2 MiB.
)

var (
	safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

	validCategories = map[string]bool{
		CategoryMacro:       true,
		CategoryCrypto:      true,
		CategoryGeopolitics: true,
		CategoryConflict:    true,
		CategoryEnergy:      true,
		CategorySeismic:     true,
		CategoryPolicy:      true,
		CategoryPrediction:  true,
		CategorySidecar:     true,
	}

	validRegions = map[string]bool{
		RegionNA:     true,
		RegionEU:     true,
		RegionAPAC:   true,
		RegionMENA:   true,
		RegionLATAM:  true,
		RegionAfrica: true,
		RegionGlobal: true,
	}

	validKinds = map[string]bool{
		KindRSS:        true,
		KindGDELT:      true,
		KindUSGS:       true,
		KindPolymarket: true,
		KindKalshi:     true,
		KindSidecar:    true,
	}
)

// SourceConfig describes a persistent clean-room intelligence or prediction feed source.
type SourceConfig struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Endpoint       string `json:"endpoint"`
	Category       string `json:"category"`
	Region         string `json:"region"`
	Enabled        bool   `json:"enabled"`
	AllowLocalhost bool   `json:"allowLocalhost"`
	BuiltIn        bool   `json:"builtIn"`
	LastSyncAt     int64  `json:"lastSyncAt,omitempty"`
	LastError      string `json:"lastError,omitempty"`
}

// SourceInput creates or updates a SourceConfig cleanly.
type SourceInput struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Endpoint       string `json:"endpoint"`
	Category       string `json:"category"`
	Region         string `json:"region"`
	Enabled        *bool  `json:"enabled,omitempty"`
	AllowLocalhost bool   `json:"allowLocalhost"`
}

// IntelEvent represents a normalized, geo-tagged, Jev/fallback-scored global event.
// External event text is strictly flagged untrusted=true.
type IntelEvent struct {
	ID            string   `json:"id"`
	SourceID      string   `json:"sourceId"`
	SourceName    string   `json:"sourceName"`
	Category      string   `json:"category"`
	Title         string   `json:"title"`
	Summary       string   `json:"summary,omitempty"`
	URL           string   `json:"url"`
	Region        string   `json:"region"`
	CountryCode   string   `json:"countryCode,omitempty"`
	Lat           float64  `json:"lat"`
	Lon           float64  `json:"lon"`
	Tickers       []string `json:"tickers,omitempty"`
	PublishedAt   int64    `json:"publishedAt"`
	IngestedAt    int64    `json:"ingestedAt"`
	Urgency       float64  `json:"urgency"`
	Sentiment     string   `json:"sentiment"`
	Confidence    float64  `json:"confidence"`
	ScoreProvider string   `json:"scoreProvider"`
	Verified      bool     `json:"verified"`
	Untrusted     bool     `json:"untrusted"`
}

// PredictionMarket represents a normalized public prediction contract from Polymarket or Kalshi.
type PredictionMarket struct {
	ID        string   `json:"id"`
	Venue     string   `json:"venue"`
	Slug      string   `json:"slug"`
	Question  string   `json:"question"`
	Category  string   `json:"category"`
	YesProb   float64  `json:"yesProb"`
	NoProb    float64  `json:"noProb"`
	Volume24h float64  `json:"volume24h"`
	Liquidity float64  `json:"liquidity"`
	Change24h float64  `json:"change24h"`
	URL       string   `json:"url"`
	EndDate   string   `json:"endDate,omitempty"`
	Tickers   []string `json:"tickers,omitempty"`
	Region    string   `json:"region"`
	Lat       float64  `json:"lat"`
	Lon       float64  `json:"lon"`
	UpdatedAt int64    `json:"updatedAt"`
}

// CorrelatedCluster groups multi-source or high-urgency regional/ticker events.
type CorrelatedCluster struct {
	ID         string   `json:"id"`
	Topic      string   `json:"topic"`
	Category   string   `json:"category"`
	Region     string   `json:"region"`
	Lat        float64  `json:"lat"`
	Lon        float64  `json:"lon"`
	EventCount int      `json:"eventCount"`
	Sources    []string `json:"sources"`
	AvgUrgency float64  `json:"avgUrgency"`
	Sentiment  string   `json:"sentiment"`
	Tickers    []string `json:"tickers,omitempty"`
	EventIDs   []string `json:"eventIds"`
}

// PredictionArbitrage flags probability divergences between Polymarket and Kalshi contracts.
type PredictionArbitrage struct {
	ID             string   `json:"id"`
	Topic          string   `json:"topic"`
	PolymarketID   string   `json:"polymarketId"`
	KalshiID       string   `json:"kalshiId"`
	PolymarketProb float64  `json:"polymarketProb"`
	KalshiProb     float64  `json:"kalshiProb"`
	SpreadPct      float64  `json:"spreadPct"`
	Similarity     float64  `json:"similarity"`
	PolymarketURL  string   `json:"polymarketUrl"`
	KalshiURL      string   `json:"kalshiUrl"`
	Tickers        []string `json:"tickers,omitempty"`
}

// EventFilter narrows IntelEvent queries.
type EventFilter struct {
	Category   string
	Region     string
	Ticker     string
	Query      string
	MinUrgency float64
	Since      int64
	Limit      int
}

// PredictionFilter narrows PredictionMarket queries.
type PredictionFilter struct {
	Venue    string
	Category string
	Query    string
	Limit    int
}

// PredictionList bundles active prediction markets and detected cross-venue spreads.
type PredictionList struct {
	Markets   []PredictionMarket    `json:"markets"`
	Arbitrage []PredictionArbitrage `json:"arbitrage"`
	Count     int                   `json:"count"`
}

// MapSummary provides geo-pinned events, multi-source clusters, and regional breakdown.
type MapSummary struct {
	Events       []IntelEvent        `json:"events"`
	Clusters     []CorrelatedCluster `json:"clusters"`
	RegionCounts map[string]int      `json:"regionCounts"`
	UpdatedAt    int64               `json:"updatedAt"`
}

// SyncResult reports per-sync metrics and sanitized per-source errors.
type SyncResult struct {
	SyncedSources   int               `json:"syncedSources"`
	EventCount      int               `json:"eventCount"`
	PredictionCount int               `json:"predictionCount"`
	Errors          map[string]string `json:"errors,omitempty"`
	SyncedAt        int64             `json:"syncedAt"`
}

// StreamFilter filters SSE updates on the WorldIntel bus.
type StreamFilter struct {
	Category string
	Region   string
	Ticker   string
}

// StreamEvent wraps live event or prediction updates published on the Bus.
type StreamEvent struct {
	Type       string            `json:"type"`
	Timestamp  int64             `json:"timestamp"`
	Event      *IntelEvent       `json:"event,omitempty"`
	Prediction *PredictionMarket `json:"prediction,omitempty"`
}

// NormalizeCategory maps raw category names onto canonical supported categories.
func NormalizeCategory(raw string) string {
	c := strings.ToLower(strings.TrimSpace(raw))
	if validCategories[c] {
		return c
	}
	return ""
}

// NormalizeRegion maps raw region names onto canonical supported regions.
func NormalizeRegion(raw string) string {
	r := strings.ToLower(strings.TrimSpace(raw))
	if validRegions[r] {
		return r
	}
	return ""
}

// NormalizeTicker normalizes a single symbol tag without spaces or unsafe chars.
func NormalizeTicker(raw string) string {
	t := strings.ToUpper(strings.TrimSpace(raw))
	t = strings.TrimPrefix(t, "$")
	if t == "" || len(t) > 12 {
		return ""
	}
	for _, r := range t {
		if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return ""
		}
	}
	return t
}

func isLoopbackHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	if addr, err := netip.ParseAddr(h); err == nil {
		return addr.Unmap().IsLoopback()
	}
	return false
}

// ValidateSourceEndpoint enforces strict SSRF safety on feed and optional sidecar URLs.
func ValidateSourceEndpoint(rawURL, kind string, allowLocalhost bool) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" || len(trimmed) > 1024 {
		return "", errors.New("source endpoint must be 1-1024 characters")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid source endpoint: %w", err)
	}
	if u.User != nil {
		return "", errors.New("source endpoint must not embed credentials")
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("source endpoint requires a hostname")
	}

	isSidecar := kind == KindSidecar
	if scheme == "http" {
		if !isSidecar || !allowLocalhost || !isLoopbackHostname(host) {
			return "", errors.New("plaintext http:// source allowed only for worldmonitor_sidecar with allowLocalhost=true and loopback host")
		}
		return u.String(), nil
	}
	if scheme != "https" {
		return "", fmt.Errorf("unsupported source scheme %q", u.Scheme)
	}
	if ip := net.ParseIP(host); ip != nil {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			return "", errors.New("invalid source IP")
		}
		addr = addr.Unmap()
		if addr.IsLoopback() {
			if !isSidecar || !allowLocalhost {
				return "", errors.New("loopback IP allowed only for worldmonitor_sidecar with allowLocalhost=true")
			}
			return u.String(), nil
		}
		if !news.PublicIP(addr) {
			return "", fmt.Errorf("blocked non-public source IP %s", addr)
		}
	} else {
		for _, suffix := range []string{".localhost", ".local", ".internal", ".home", ".lan"} {
			if strings.HasSuffix(host, suffix) && (!isSidecar || !allowLocalhost) {
				return "", fmt.Errorf("blocked private suffix in host %q", host)
			}
		}
	}
	return u.String(), nil
}

// ValidateEventURL returns a sanitized public HTTP(S) URL for untrusted item links.
func ValidateEventURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 1024 {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.User != nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || isLoopbackHostname(host) {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		if addr, ok := netip.AddrFromSlice(ip); !ok || !news.PublicIP(addr.Unmap()) {
			return ""
		}
	}
	return u.String()
}

// ValidateSourceInput normalizes and validates a SourceInput payload.
func ValidateSourceInput(in SourceInput) (SourceInput, error) {
	id := strings.TrimSpace(in.ID)
	if !safeIDPattern.MatchString(id) {
		return SourceInput{}, errors.New("source id must match [A-Za-z0-9._-] (1-64 chars)")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 80 {
		return SourceInput{}, errors.New("source name must be 1-80 characters")
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if !validKinds[kind] {
		return SourceInput{}, fmt.Errorf("unsupported source kind %q", in.Kind)
	}
	allowLocalhost := in.AllowLocalhost && kind == KindSidecar
	if in.AllowLocalhost && kind != KindSidecar {
		return SourceInput{}, errors.New("allowLocalhost is restricted to worldmonitor_sidecar sources")
	}
	endpoint, err := ValidateSourceEndpoint(in.Endpoint, kind, allowLocalhost)
	if err != nil {
		return SourceInput{}, err
	}
	cat := NormalizeCategory(in.Category)
	if cat == "" {
		return SourceInput{}, fmt.Errorf("invalid category %q", in.Category)
	}
	reg := NormalizeRegion(in.Region)
	if reg == "" {
		reg = RegionGlobal
	}
	enabledVal := true
	if in.Enabled != nil {
		enabledVal = *in.Enabled
	}
	return SourceInput{
		ID:             id,
		Name:           name,
		Kind:           kind,
		Endpoint:       endpoint,
		Category:       cat,
		Region:         reg,
		Enabled:        &enabledVal,
		AllowLocalhost: allowLocalhost,
	}, nil
}
