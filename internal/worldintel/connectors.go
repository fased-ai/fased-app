package worldintel

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/news"
)

// DefaultSources returns Fased's built-in clean-room intelligence and prediction market feeds.
// The optional local WorldMonitor sidecar starts disabled until opted in.
func DefaultSources() []SourceConfig {
	return []SourceConfig{
		{
			ID:       "gdelt-global",
			Name:     "GDELT Global Macro & Geo",
			Kind:     KindGDELT,
			Endpoint: "https://api.gdeltproject.org/api/v2/doc/doc?query=crypto%20OR%20central%20bank%20OR%20oil%20OR%20tariff&mode=ArtList&maxrecords=30&format=json",
			Category: CategoryGeopolitics,
			Region:   RegionGlobal,
			Enabled:  true,
			BuiltIn:  true,
		},
		{
			ID:       "usgs-quakes",
			Name:     "USGS M4.5+ Seismic",
			Kind:     KindUSGS,
			Endpoint: "https://earthquake.usgs.gov/earthquakes/feed/v1.0/summary/4.5_day.geojson",
			Category: CategorySeismic,
			Region:   RegionGlobal,
			Enabled:  true,
			BuiltIn:  true,
		},
		{
			ID:       "polymarket-top",
			Name:     "Polymarket Active Markets",
			Kind:     KindPolymarket,
			Endpoint: "https://gamma-api.polymarket.com/markets?active=true&closed=false&order=volume24hr&ascending=false&limit=40",
			Category: CategoryPrediction,
			Region:   RegionGlobal,
			Enabled:  true,
			BuiltIn:  true,
		},
		{
			ID:       "kalshi-open",
			Name:     "Kalshi Open Markets",
			Kind:     KindKalshi,
			Endpoint: "https://api.elections.kalshi.com/trade-api/v2/markets?status=open&limit=40",
			Category: CategoryPrediction,
			Region:   RegionNA,
			Enabled:  true,
			BuiltIn:  true,
		},
		{
			ID:       "fed-press",
			Name:     "Federal Reserve Press",
			Kind:     KindRSS,
			Endpoint: "https://www.federalreserve.gov/feeds/press_all.xml",
			Category: CategoryMacro,
			Region:   RegionNA,
			Enabled:  true,
			BuiltIn:  true,
		},
		{
			ID:       "ecb-press",
			Name:     "ECB Press Releases",
			Kind:     KindRSS,
			Endpoint: "https://www.ecb.europa.eu/rss/press.html",
			Category: CategoryMacro,
			Region:   RegionEU,
			Enabled:  true,
			BuiltIn:  true,
		},
		{
			ID:             "worldmonitor-local",
			Name:           "WorldMonitor Local Sidecar",
			Kind:           KindSidecar,
			Endpoint:       "http://127.0.0.1:3000/api/events",
			Category:       CategorySidecar,
			Region:         RegionGlobal,
			Enabled:        false,
			AllowLocalhost: true,
			BuiltIn:        true,
		},
	}
}

func makeEventID(sourceID, urlOrKey, title string) string {
	return news.Hash([]byte(sourceID + ":" + urlOrKey + ":" + title))[:24]
}

func trimText(s string, max int) string {
	cleaned := strings.TrimSpace(s)
	if len(cleaned) <= max {
		return cleaned
	}
	return strings.TrimSpace(cleaned[:max])
}

func parseFlexibleTimestamp(raw string, fallback int64) int64 {
	s := strings.TrimSpace(raw)
	if s == "" {
		return fallback
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"20060102T150405Z",
		"20060102150405",
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			u := t.Unix()
			if u > fallback {
				return fallback
			}
			return u
		}
	}
	return fallback
}

// ParseGDELTJSON parses GDELT v2 Doc ArtList JSON payloads into untrusted IntelEvents.
func ParseGDELTJSON(raw []byte, src SourceConfig, now int64) ([]IntelEvent, error) {
	var doc struct {
		Articles []struct {
			URL           string `json:"url"`
			Title         string `json:"title"`
			SeenDate      string `json:"seendate"`
			Domain        string `json:"domain"`
			SourceCountry string `json:"sourcecountry"`
		} `json:"articles"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode GDELT JSON: %w", err)
	}
	events := make([]IntelEvent, 0, len(doc.Articles))
	for i, art := range doc.Articles {
		if i >= 40 {
			break
		}
		title := trimText(art.Title, 280)
		if title == "" {
			continue
		}
		safeURL := ValidateEventURL(art.URL)
		pubAt := parseFlexibleTimestamp(art.SeenDate, now)
		locRegion, country, lat, lon := InferLocation(title+" "+art.SourceCountry, src.Region)
		events = append(events, IntelEvent{
			ID:          makeEventID(src.ID, safeURL, title),
			SourceID:    src.ID,
			SourceName:  src.Name,
			Category:    src.Category,
			Title:       title,
			Summary:     trimText(art.Domain, 120),
			URL:         safeURL,
			Region:      locRegion,
			CountryCode: country,
			Lat:         lat,
			Lon:         lon,
			Tickers:     ExtractTickers(title),
			PublishedAt: pubAt,
			IngestedAt:  now,
			Untrusted:   true,
		})
	}
	return events, nil
}

// ParseUSGSGeoJSON parses USGS earthquake GeoJSON feature collections.
func ParseUSGSGeoJSON(raw []byte, src SourceConfig, now int64) ([]IntelEvent, error) {
	var fc struct {
		Features []struct {
			ID         string `json:"id"`
			Properties struct {
				Mag   float64 `json:"mag"`
				Place string  `json:"place"`
				Title string  `json:"title"`
				Time  int64   `json:"time"`
				URL   string  `json:"url"`
			} `json:"properties"`
			Geometry struct {
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &fc); err != nil {
		return nil, fmt.Errorf("decode USGS GeoJSON: %w", err)
	}
	out := make([]IntelEvent, 0, len(fc.Features))
	for i, feat := range fc.Features {
		if i >= 40 {
			break
		}
		title := trimText(feat.Properties.Title, 280)
		if title == "" {
			title = trimText(feat.Properties.Place, 280)
		}
		if title == "" {
			continue
		}
		var lat, lon float64
		if len(feat.Geometry.Coordinates) >= 2 {
			lon = feat.Geometry.Coordinates[0]
			lat = feat.Geometry.Coordinates[1]
		}
		reg, country, infLat, infLon := InferLocation(feat.Properties.Place, RegionAPAC)
		if math.Abs(lat) <= 90 && math.Abs(lon) <= 180 && (lat != 0 || lon != 0) {
			lat, lon = sanitizeCoord(lat, lon, reg)
		} else {
			lat, lon = infLat, infLon
		}
		pubAt := now
		if feat.Properties.Time > 0 {
			sec := feat.Properties.Time / 1000
			if sec <= now {
				pubAt = sec
			}
		}
		mag := feat.Properties.Mag
		if math.IsNaN(mag) || math.IsInf(mag, 0) {
			mag = 0
		}
		summary := fmt.Sprintf("M%.1f %s", mag, trimText(feat.Properties.Place, 160))
		safeURL := ValidateEventURL(feat.Properties.URL)
		out = append(out, IntelEvent{
			ID:          makeEventID(src.ID, feat.ID, title),
			SourceID:    src.ID,
			SourceName:  src.Name,
			Category:    CategorySeismic,
			Title:       title,
			Summary:     summary,
			URL:         safeURL,
			Region:      reg,
			CountryCode: country,
			Lat:         lat,
			Lon:         lon,
			Tickers:     ExtractTickers(title + " " + summary),
			PublishedAt: pubAt,
			IngestedAt:  now,
			Untrusted:   true,
		})
	}
	return out, nil
}

// ParsePolymarketJSON parses public Polymarket Gamma markets API arrays or {data:[]} envelopes.
func ParsePolymarketJSON(raw []byte, now int64) ([]PredictionMarket, error) {
	type rawPoly struct {
		ID            string          `json:"id"`
		Slug          string          `json:"slug"`
		Question      string          `json:"question"`
		Category      string          `json:"category"`
		OutcomePrices json.RawMessage `json:"outcomePrices"`
		Volume24hr    any             `json:"volume24hr"`
		Liquidity     any             `json:"liquidity"`
		OneDayChange  any             `json:"oneDayPriceChange"`
		EndDate       string          `json:"endDate"`
	}
	var items []rawPoly
	if err := json.Unmarshal(raw, &items); err != nil {
		var env struct {
			Data []rawPoly `json:"data"`
		}
		if err2 := json.Unmarshal(raw, &env); err2 != nil {
			return nil, fmt.Errorf("decode Polymarket JSON: %w", err)
		}
		items = env.Data
	}

	out := make([]PredictionMarket, 0, len(items))
	for i, it := range items {
		if i >= 60 {
			break
		}
		q := trimText(it.Question, 280)
		if q == "" {
			continue
		}
		yesProb := parsePolymarketYesProb(it.OutcomePrices)
		noProb := clamp01(1 - yesProb)
		slug := trimText(it.Slug, 120)
		id := trimText(it.ID, 64)
		if id == "" {
			id = news.Hash([]byte("poly:" + slug + ":" + q))[:16]
		}
		cat := inferPredictionCategory(q, it.Category)
		reg, _, lat, lon := InferLocation(q, RegionGlobal)
		url := ""
		if slug != "" {
			url = ValidateEventURL("https://polymarket.com/event/" + slug)
		}
		vol := math.Max(0, parseFlexibleFloat(it.Volume24hr))
		liq := math.Max(0, parseFlexibleFloat(it.Liquidity))
		chg := parseFlexibleFloat(it.OneDayChange)
		if math.IsNaN(chg) || math.IsInf(chg, 0) {
			chg = 0
		}
		out = append(out, PredictionMarket{
			ID:        "poly-" + id,
			Venue:     VenuePolymarket,
			Slug:      slug,
			Question:  q,
			Category:  cat,
			YesProb:   clamp01(yesProb),
			NoProb:    noProb,
			Volume24h: math.Round(vol*100) / 100,
			Liquidity: math.Round(liq*100) / 100,
			Change24h: math.Round(chg*1000) / 1000,
			URL:       url,
			EndDate:   trimText(it.EndDate, 64),
			Tickers:   ExtractTickers(q),
			Region:    reg,
			Lat:       lat,
			Lon:       lon,
			UpdatedAt: now,
		})
	}
	return out, nil
}

func parsePolymarketYesProb(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0.5
	}
	var arr []any
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		return clamp01(parseFlexibleFloat(arr[0]))
	}
	var encodedStr string
	if err := json.Unmarshal(raw, &encodedStr); err == nil && strings.TrimSpace(encodedStr) != "" {
		var inner []any
		if err2 := json.Unmarshal([]byte(encodedStr), &inner); err2 == nil && len(inner) > 0 {
			return clamp01(parseFlexibleFloat(inner[0]))
		}
	}
	return 0.5
}

// ParseKalshiJSON parses public Kalshi /markets responses and normalizes cent prices to [0,1].
func ParseKalshiJSON(raw []byte, now int64) ([]PredictionMarket, error) {
	var doc struct {
		Markets []struct {
			Ticker         string  `json:"ticker"`
			EventTicker    string  `json:"event_ticker"`
			Title          string  `json:"title"`
			Subtitle       string  `json:"subtitle"`
			Category       string  `json:"category"`
			YesBid         float64 `json:"yes_bid"`
			YesAsk         float64 `json:"yes_ask"`
			LastPrice      float64 `json:"last_price"`
			Volume24h      float64 `json:"volume_24h"`
			Liquidity      float64 `json:"liquidity"`
			ExpirationTime string  `json:"expiration_time"`
		} `json:"markets"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode Kalshi JSON: %w", err)
	}
	out := make([]PredictionMarket, 0, len(doc.Markets))
	for i, m := range doc.Markets {
		if i >= 60 {
			break
		}
		q := trimText(m.Title, 280)
		if q == "" {
			continue
		}
		price := m.LastPrice
		if m.YesBid > 0 && m.YesAsk > 0 {
			price = (m.YesBid + m.YesAsk) / 2.0
		}
		if price >= 1 {
			price = price / 100.0
		}
		yesProb := clamp01(price)
		ticker := trimText(m.Ticker, 64)
		id := ticker
		if id == "" {
			id = news.Hash([]byte("kalshi:" + q))[:16]
		}
		url := ""
		if ticker != "" {
			url = ValidateEventURL("https://kalshi.com/markets/" + strings.ToLower(ticker))
		}
		reg, _, lat, lon := InferLocation(q+" "+m.Subtitle, RegionNA)
		vol := m.Volume24h
		if math.IsNaN(vol) || math.IsInf(vol, 0) || vol < 0 {
			vol = 0
		}
		liq := m.Liquidity
		if math.IsNaN(liq) || math.IsInf(liq, 0) || liq < 0 {
			liq = 0
		}
		out = append(out, PredictionMarket{
			ID:        "kalshi-" + strings.ToLower(id),
			Venue:     VenueKalshi,
			Slug:      ticker,
			Question:  q,
			Category:  inferPredictionCategory(q, m.Category),
			YesProb:   yesProb,
			NoProb:    clamp01(1 - yesProb),
			Volume24h: math.Round(vol*100) / 100,
			Liquidity: math.Round(liq*100) / 100,
			URL:       url,
			EndDate:   trimText(m.ExpirationTime, 64),
			Tickers:   ExtractTickers(q + " " + m.Subtitle),
			Region:    reg,
			Lat:       lat,
			Lon:       lon,
			UpdatedAt: now,
		})
	}
	return out, nil
}

func inferPredictionCategory(question, rawCat string) string {
	qLower := strings.ToLower(question + " " + rawCat)
	switch {
	case strings.Contains(qLower, "btc") || strings.Contains(qLower, "bitcoin") || strings.Contains(qLower, "eth") || strings.Contains(qLower, "sol") || strings.Contains(qLower, "crypto"):
		return CategoryCrypto
	case strings.Contains(qLower, "fed") || strings.Contains(qLower, "cpi") || strings.Contains(qLower, "rate") || strings.Contains(qLower, "gdp") || strings.Contains(qLower, "ecb"):
		return CategoryMacro
	case strings.Contains(qLower, "oil") || strings.Contains(qLower, "opec") || strings.Contains(qLower, "energy"):
		return CategoryEnergy
	case strings.Contains(qLower, "war") || strings.Contains(qLower, "ceasefire") || strings.Contains(qLower, "election") || strings.Contains(qLower, "tariff"):
		return CategoryGeopolitics
	default:
		if norm := NormalizeCategory(rawCat); norm != "" {
			return norm
		}
		return CategoryPrediction
	}
}

// ParseRSSOrAtom delegates canonical feeds to internal/news.ParseFeed and accepts
// safe public HTTPS feeds exceeding internal/news's strict 64KiB/queryless limits while
// strictly refusing XML DTDs, entities, directives, or non-xml processing instructions.
func ParseRSSOrAtom(raw []byte, src SourceConfig, now int64) ([]IntelEvent, error) {
	if len(raw) == 0 || len(raw) > maxResponseBytes {
		return nil, errors.New("RSS payload outside bounds")
	}
	// First enforce XML structural safety before any decode (rejects DTDs/XXE/excessive depth).
	if err := checkSafeXMLTokens(raw); err != nil {
		return nil, err
	}

	if len(raw) <= 65536 {
		fetchSeed := news.Fetch{
			SourceID:      src.ID,
			URL:           src.Endpoint,
			MaxAgeSeconds: 14 * 24 * 3600,
			CreatedAt:     now,
		}
		if parsed, err := news.ParseFeed(raw, fetchSeed, now, func(t string) bool { return strings.TrimSpace(t) != "" }); err == nil && len(parsed.Items) > 0 {
			events := make([]IntelEvent, 0, len(parsed.Items))
			for _, item := range parsed.Items {
				reg, country, lat, lon := InferLocation(item.Title, src.Region)
				events = append(events, IntelEvent{
					ID:          makeEventID(src.ID, item.URL, item.Title),
					SourceID:    src.ID,
					SourceName:  src.Name,
					Category:    src.Category,
					Title:       trimText(item.Title, 280),
					URL:         item.URL,
					Region:      reg,
					CountryCode: country,
					Lat:         lat,
					Lon:         lon,
					Tickers:     ExtractTickers(item.Title),
					PublishedAt: item.PublishedAt,
					IngestedAt:  now,
					Untrusted:   true,
				})
			}
			return events, nil
		}
	}

	type rawFeedItem struct {
		title string
		link  string
		pub   string
		desc  string
	}
	var items []rawFeedItem

	var rss struct {
		XMLName  xml.Name `xml:"rss"`
		Channels []struct {
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				PubDate     string `xml:"pubDate"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if xml.Unmarshal(raw, &rss) == nil && rss.XMLName.Local == "rss" && len(rss.Channels) > 0 {
		for _, it := range rss.Channels[0].Items {
			items = append(items, rawFeedItem{title: it.Title, link: it.Link, pub: it.PubDate, desc: it.Description})
		}
	} else {
		var atom struct {
			XMLName xml.Name `xml:"feed"`
			Entries []struct {
				Title     string `xml:"title"`
				Summary   string `xml:"summary"`
				Published string `xml:"published"`
				Updated   string `xml:"updated"`
				Links     []struct {
					Href string `xml:"href,attr"`
					Rel  string `xml:"rel,attr"`
				} `xml:"link"`
			} `xml:"entry"`
		}
		if err := xml.Unmarshal(raw, &atom); err != nil || atom.XMLName.Local != "feed" {
			return nil, errors.New("unsupported or invalid RSS/Atom feed")
		}
		for _, ent := range atom.Entries {
			link := ""
			for _, l := range ent.Links {
				rel := strings.ToLower(strings.TrimSpace(l.Rel))
				if rel == "alternate" {
					link = l.Href
					break
				}
				if rel == "" && link == "" {
					link = l.Href
				}
			}
			pub := strings.TrimSpace(ent.Published)
			if pub == "" {
				pub = strings.TrimSpace(ent.Updated)
			}
			items = append(items, rawFeedItem{title: ent.Title, link: link, pub: pub, desc: ent.Summary})
		}
	}

	events := make([]IntelEvent, 0, len(items))
	for _, it := range items {
		if len(events) >= 40 {
			break
		}
		title := trimText(it.title, 280)
		safeURL := ValidateEventURL(it.link)
		if title == "" || safeURL == "" {
			continue
		}
		pubAt := parseFlexibleTimestamp(it.pub, now)
		reg, country, lat, lon := InferLocation(title+" "+it.desc, src.Region)
		events = append(events, IntelEvent{
			ID:          makeEventID(src.ID, safeURL, title),
			SourceID:    src.ID,
			SourceName:  src.Name,
			Category:    src.Category,
			Title:       title,
			Summary:     trimText(it.desc, 280),
			URL:         safeURL,
			Region:      reg,
			CountryCode: country,
			Lat:         lat,
			Lon:         lon,
			Tickers:     ExtractTickers(title + " " + it.desc),
			PublishedAt: pubAt,
			IngestedAt:  now,
			Untrusted:   true,
		})
	}
	return events, nil
}

func checkSafeXMLTokens(raw []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	depth, tokens, roots := 0, 0, 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("invalid feed XML")
		}
		tokens++
		if tokens > 32768 {
			return errors.New("feed XML token bound exceeded")
		}
		switch x := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
			if depth > 24 {
				return errors.New("feed XML nesting bound exceeded")
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return errors.New("XML directives refused")
		case xml.ProcInst:
			if x.Target != "xml" {
				return errors.New("XML processing instruction refused")
			}
		}
	}
	if roots != 1 || depth != 0 {
		return errors.New("incomplete XML feed document")
	}
	return nil
}

// ParseWorldMonitorSidecarJSON parses clean-room local sidecar JSON event feeds.
func ParseWorldMonitorSidecarJSON(raw []byte, src SourceConfig, now int64) ([]IntelEvent, error) {
	type rawSidecarItem struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		Summary     string   `json:"summary"`
		URL         string   `json:"url"`
		Category    string   `json:"category"`
		Region      string   `json:"region"`
		Lat         float64  `json:"lat"`
		Lon         float64  `json:"lon"`
		PublishedAt any      `json:"publishedAt"`
		Tickers     []string `json:"tickers"`
	}
	var items []rawSidecarItem
	var env struct {
		Events []rawSidecarItem `json:"events"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.Events != nil {
		items = env.Events
	} else if err2 := json.Unmarshal(raw, &items); err2 != nil {
		return nil, fmt.Errorf("decode sidecar JSON: %w", err2)
	}

	out := make([]IntelEvent, 0, len(items))
	for i, it := range items {
		if i >= 60 {
			break
		}
		title := trimText(it.Title, 280)
		if title == "" {
			continue
		}
		cat := NormalizeCategory(it.Category)
		if cat == "" {
			cat = src.Category
		}
		defaultReg := NormalizeRegion(it.Region)
		if defaultReg == "" {
			defaultReg = src.Region
		}
		reg, country, infLat, infLon := InferLocation(title+" "+it.Summary, defaultReg)
		lat, lon := sanitizeCoord(it.Lat, it.Lon, reg)
		if it.Lat == 0 && it.Lon == 0 {
			lat, lon = infLat, infLon
		}
		tickSet := make(map[string]bool)
		for _, tk := range ExtractTickers(title + " " + it.Summary) {
			tickSet[tk] = true
		}
		for _, rawTk := range it.Tickers {
			if norm := NormalizeTicker(rawTk); norm != "" {
				tickSet[norm] = true
			}
		}
		var tickers []string
		for tk := range tickSet {
			tickers = append(tickers, tk)
		}
		sort.Strings(tickers)
		id := trimText(it.ID, 64)
		safeURL := ValidateEventURL(it.URL)
		if id == "" {
			id = makeEventID(src.ID, safeURL, title)
		}
		pubAt := now
		if num := parseFlexibleFloat(it.PublishedAt); num > 0 {
			if num >= 1e12 {
				num /= 1000.0
			}
			pubAt = int64(num)
			if pubAt > now {
				pubAt = now
			}
		} else if s, ok := it.PublishedAt.(string); ok {
			pubAt = parseFlexibleTimestamp(s, now)
		}
		out = append(out, IntelEvent{
			ID:          id,
			SourceID:    src.ID,
			SourceName:  src.Name,
			Category:    cat,
			Title:       title,
			Summary:     trimText(it.Summary, 280),
			URL:         safeURL,
			Region:      reg,
			CountryCode: country,
			Lat:         lat,
			Lon:         lon,
			Tickers:     tickers,
			PublishedAt: pubAt,
			IngestedAt:  now,
			Untrusted:   true,
		})
	}
	return out, nil
}
