package worldintel

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fased-ai/fased-app/internal/decision"
	"github.com/fased-ai/fased-app/internal/news"
)

// Scorer evaluates structured urgency/sentiment questions (backed by TypeSafe Jev or fallback).
type Scorer func(ctx context.Context, req decision.EvaluateRequest) (decision.EvaluateResponse, error)

type geoHub struct {
	keyword string
	region  string
	country string
	lat     float64
	lon     float64
}

var gazetteer = []geoHub{
	{keyword: "washington", region: RegionNA, country: "US", lat: 38.9072, lon: -77.0369},
	{keyword: "federal reserve", region: RegionNA, country: "US", lat: 38.8921, lon: -77.0459},
	{keyword: "fomc", region: RegionNA, country: "US", lat: 38.8921, lon: -77.0459},
	{keyword: "treasury", region: RegionNA, country: "US", lat: 38.8979, lon: -77.0332},
	{keyword: "new york", region: RegionNA, country: "US", lat: 40.7128, lon: -74.0060},
	{keyword: "sec ", region: RegionNA, country: "US", lat: 38.8995, lon: -77.0074},
	{keyword: "chicago", region: RegionNA, country: "US", lat: 41.8781, lon: -87.6298},
	{keyword: "san francisco", region: RegionNA, country: "US", lat: 37.7749, lon: -122.4194},
	{keyword: "texas", region: RegionNA, country: "US", lat: 29.7604, lon: -95.3698},
	{keyword: "united states", region: RegionNA, country: "US", lat: 38.9072, lon: -77.0369},
	{keyword: "canada", region: RegionNA, country: "CA", lat: 45.4215, lon: -75.6972},
	{keyword: "toronto", region: RegionNA, country: "CA", lat: 43.6532, lon: -79.3832},
	{keyword: "mexico", region: RegionLATAM, country: "MX", lat: 19.4326, lon: -99.1332},
	{keyword: "london", region: RegionEU, country: "GB", lat: 51.5074, lon: -0.1278},
	{keyword: "bank of england", region: RegionEU, country: "GB", lat: 51.5141, lon: -0.0884},
	{keyword: "frankfurt", region: RegionEU, country: "DE", lat: 50.1109, lon: 8.6821},
	{keyword: "ecb", region: RegionEU, country: "DE", lat: 50.1095, lon: 8.7024},
	{keyword: "eurozone", region: RegionEU, country: "EU", lat: 50.8503, lon: 4.3517},
	{keyword: "brussels", region: RegionEU, country: "BE", lat: 50.8503, lon: 4.3517},
	{keyword: "paris", region: RegionEU, country: "FR", lat: 48.8566, lon: 2.3522},
	{keyword: "zurich", region: RegionEU, country: "CH", lat: 47.3769, lon: 8.5417},
	{keyword: "kyiv", region: RegionEU, country: "UA", lat: 50.4501, lon: 30.5234},
	{keyword: "ukraine", region: RegionEU, country: "UA", lat: 50.4501, lon: 30.5234},
	{keyword: "moscow", region: RegionEU, country: "RU", lat: 55.7558, lon: 37.6173},
	{keyword: "russia", region: RegionEU, country: "RU", lat: 55.7558, lon: 37.6173},
	{keyword: "istanbul", region: RegionMENA, country: "TR", lat: 41.0082, lon: 28.9784},
	{keyword: "turkey", region: RegionMENA, country: "TR", lat: 39.9334, lon: 32.8597},
	{keyword: "tel aviv", region: RegionMENA, country: "IL", lat: 32.0853, lon: 34.7818},
	{keyword: "israel", region: RegionMENA, country: "IL", lat: 31.7683, lon: 35.2137},
	{keyword: "tehran", region: RegionMENA, country: "IR", lat: 35.6892, lon: 51.3890},
	{keyword: "iran", region: RegionMENA, country: "IR", lat: 35.6892, lon: 51.3890},
	{keyword: "riyadh", region: RegionMENA, country: "SA", lat: 24.7136, lon: 46.6753},
	{keyword: "saudi", region: RegionMENA, country: "SA", lat: 24.7136, lon: 46.6753},
	{keyword: "dubai", region: RegionMENA, country: "AE", lat: 25.2048, lon: 55.2708},
	{keyword: "red sea", region: RegionMENA, country: "YE", lat: 15.3694, lon: 44.1910},
	{keyword: "strait of hormuz", region: RegionMENA, country: "OM", lat: 26.5667, lon: 56.2500},
	{keyword: "beijing", region: RegionAPAC, country: "CN", lat: 39.9042, lon: 116.4074},
	{keyword: "shanghai", region: RegionAPAC, country: "CN", lat: 31.2304, lon: 121.4737},
	{keyword: "china", region: RegionAPAC, country: "CN", lat: 39.9042, lon: 116.4074},
	{keyword: "hong kong", region: RegionAPAC, country: "HK", lat: 22.3193, lon: 114.1694},
	{keyword: "taiwan", region: RegionAPAC, country: "TW", lat: 25.0330, lon: 121.5654},
	{keyword: "taipei", region: RegionAPAC, country: "TW", lat: 25.0330, lon: 121.5654},
	{keyword: "tokyo", region: RegionAPAC, country: "JP", lat: 35.6762, lon: 139.6503},
	{keyword: "bank of japan", region: RegionAPAC, country: "JP", lat: 35.6859, lon: 139.7709},
	{keyword: "japan", region: RegionAPAC, country: "JP", lat: 35.6762, lon: 139.6503},
	{keyword: "seoul", region: RegionAPAC, country: "KR", lat: 37.5665, lon: 126.9780},
	{keyword: "korea", region: RegionAPAC, country: "KR", lat: 37.5665, lon: 126.9780},
	{keyword: "singapore", region: RegionAPAC, country: "SG", lat: 1.3521, lon: 103.8198},
	{keyword: "mumbai", region: RegionAPAC, country: "IN", lat: 19.0760, lon: 72.8777},
	{keyword: "india", region: RegionAPAC, country: "IN", lat: 28.6139, lon: 77.2090},
	{keyword: "sydney", region: RegionAPAC, country: "AU", lat: -33.8688, lon: 151.2093},
	{keyword: "sao paulo", region: RegionLATAM, country: "BR", lat: -23.5505, lon: -46.6333},
	{keyword: "brazil", region: RegionLATAM, country: "BR", lat: -15.7975, lon: -47.8919},
	{keyword: "argentina", region: RegionLATAM, country: "AR", lat: -34.6037, lon: -58.3816},
	{keyword: "nigeria", region: RegionAfrica, country: "NG", lat: 6.5244, lon: 3.3792},
	{keyword: "johannesburg", region: RegionAfrica, country: "ZA", lat: -26.2041, lon: 28.0473},
	{keyword: "south africa", region: RegionAfrica, country: "ZA", lat: -26.2041, lon: 28.0473},
}

var tickerAliases = map[string]string{
	"BITCOIN":     "BTC",
	"BTC":         "BTC",
	"ETHEREUM":    "ETH",
	"ETH":         "ETH",
	"SOLANA":      "SOL",
	"SOL":         "SOL",
	"DOLLAR":      "DXY",
	"DXY":         "DXY",
	"USD":         "USD",
	"SPY":         "SPY",
	"NASDAQ":      "QQQ",
	"QQQ":         "QQQ",
	"GOLD":        "XAU",
	"XAU":         "XAU",
	"CRUDE":       "OIL",
	"BRENT":       "OIL",
	"WTI":         "OIL",
	"OIL":         "OIL",
	"TREASURY":    "TLT",
	"YIELDS":      "TLT",
	"TLT":         "TLT",
	"NVIDIA":      "NVDA",
	"NVDA":        "NVDA",
	"SEMICONDUCT": "NVDA",
	"WEN":         "WEN",
}

func regionFallbackCoords(region string) (float64, float64) {
	switch region {
	case RegionNA:
		return 39.8283, -98.5795
	case RegionEU:
		return 50.1109, 8.6821
	case RegionAPAC:
		return 35.6762, 139.6503
	case RegionMENA:
		return 25.2048, 55.2708
	case RegionLATAM:
		return -23.5505, -46.6333
	case RegionAfrica:
		return 6.5244, 3.3792
	default:
		return 40.7128, -74.0060
	}
}

func sanitizeCoord(lat, lon float64, fallbackRegion string) (float64, float64) {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || math.IsNaN(lon) || math.IsInf(lon, 0) || lat < -90 || lat > 90 || lon < -180 || lon > 180 || (lat == 0 && lon == 0) {
		return regionFallbackCoords(fallbackRegion)
	}
	return math.Round(lat*10000) / 10000, math.Round(lon*10000) / 10000
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return math.Round(v*1000) / 1000
}

// InferLocation maps event text onto a canonical region, country code, and coordinates.
func InferLocation(text, defaultRegion string) (region, countryCode string, lat, lon float64) {
	normDefault := NormalizeRegion(defaultRegion)
	if normDefault == "" {
		normDefault = RegionGlobal
	}
	lower := strings.ToLower(text)
	for _, hub := range gazetteer {
		if strings.Contains(lower, hub.keyword) {
			return hub.region, hub.country, hub.lat, hub.lon
		}
	}
	lat, lon = regionFallbackCoords(normDefault)
	return normDefault, "", lat, lon
}

// ExtractTickers extracts sorted deduplicated market symbols mentioned in text.
func ExtractTickers(text string) []string {
	tokens := strings.FieldsFunc(strings.ToUpper(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool)
	for _, tok := range tokens {
		if mapped, ok := tickerAliases[tok]; ok {
			seen[mapped] = true
			continue
		}
		if strings.HasPrefix(tok, "SEMICONDUCT") {
			seen["NVDA"] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ScoreEvent annotates an IntelEvent with urgency, sentiment, and confidence using Scorer or offline fallback.
func ScoreEvent(ctx context.Context, scorer Scorer, evt IntelEvent) IntelEvent {
	evt.Untrusted = true
	stateText := strings.TrimSpace(evt.Title + " " + evt.Summary)
	if stateText == "" {
		evt.Urgency = 0.25
		evt.Sentiment = "neutral"
		evt.Confidence = 0.5
		evt.ScoreProvider = "deterministic-fallback"
		return evt
	}
	req := decision.EvaluateRequest{
		State: stateText,
		Questions: map[string]decision.QuestionSpec{
			"urgency": {
				Type:         decision.TypeNoul,
				Instructions: "Urgent breaking market or geopolitical risk event?",
			},
			"sentiment": {
				Type:    decision.TypeChoice,
				Choices: []string{"bullish", "bearish", "neutral"},
			},
		},
	}

	var resp decision.EvaluateResponse
	var err error
	if scorer != nil {
		resp, err = scorer(ctx, req)
	}
	if scorer == nil || err != nil {
		resp, err = decision.EvaluateFallback(req, time.Now())
	}
	if err == nil {
		if urg, ok := resp.Answers["urgency"]; ok && urg.Noul != nil {
			evt.Urgency = clamp01(*urg.Noul)
			evt.Confidence = clamp01(urg.Confidence)
		}
		if sent, ok := resp.Answers["sentiment"]; ok && sent.Choice != "" {
			evt.Sentiment = sent.Choice
			if sent.Confidence > evt.Confidence {
				evt.Confidence = clamp01(sent.Confidence)
			}
		}
		evt.ScoreProvider = resp.Provider
	}
	if evt.Sentiment == "" {
		evt.Sentiment = "neutral"
	}
	if evt.ScoreProvider == "" {
		evt.ScoreProvider = "deterministic-fallback"
	}
	return evt
}

// BuildClusters groups related recent events into correlated thematic/regional clusters.
func BuildClusters(events []IntelEvent) []CorrelatedCluster {
	if len(events) == 0 {
		return nil
	}
	type bucket struct {
		key      string
		category string
		region   string
		topic    string
		events   []IntelEvent
	}
	byKey := make(map[string]*bucket)
	for _, ev := range events {
		tickerTag := ""
		if len(ev.Tickers) > 0 {
			tickerTag = ev.Tickers[0]
		}
		key := ev.Region + "|" + ev.Category + "|" + tickerTag
		b, ok := byKey[key]
		if !ok {
			topic := strings.ToUpper(ev.Category)
			if ev.Region != "" && ev.Region != RegionGlobal {
				topic = strings.ToUpper(ev.Region) + " " + topic
			}
			if tickerTag != "" {
				topic += " · " + tickerTag
			}
			b = &bucket{key: key, category: ev.Category, region: ev.Region, topic: topic}
			byKey[key] = b
		}
		b.events = append(b.events, ev)
	}

	clusters := make([]CorrelatedCluster, 0, len(byKey))
	for _, b := range byKey {
		var urgSum, latSum, lonSum float64
		srcSet := make(map[string]bool)
		tickSet := make(map[string]bool)
		sentCounts := make(map[string]int)
		eventIDs := make([]string, 0, len(b.events))
		for _, ev := range b.events {
			urgSum += ev.Urgency
			latSum += ev.Lat
			lonSum += ev.Lon
			srcSet[ev.SourceName] = true
			for _, tk := range ev.Tickers {
				tickSet[tk] = true
			}
			sentCounts[ev.Sentiment]++
			eventIDs = append(eventIDs, ev.ID)
		}
		avgUrg := clamp01(urgSum / float64(len(b.events)))
		if len(b.events) < 2 && avgUrg < 0.70 {
			continue
		}
		sources := make([]string, 0, len(srcSet))
		for s := range srcSet {
			sources = append(sources, s)
		}
		sort.Strings(sources)

		tickers := make([]string, 0, len(tickSet))
		for tk := range tickSet {
			tickers = append(tickers, tk)
		}
		sort.Strings(tickers)

		bestSent := "neutral"
		bestCount := 0
		for s, c := range sentCounts {
			if c > bestCount || (c == bestCount && s < bestSent) {
				bestSent = s
				bestCount = c
			}
		}

		lat, lon := sanitizeCoord(latSum/float64(len(b.events)), lonSum/float64(len(b.events)), b.region)
		clusters = append(clusters, CorrelatedCluster{
			ID:         news.Hash([]byte("cluster:" + b.key))[:16],
			Topic:      b.topic,
			Category:   b.category,
			Region:     b.region,
			Lat:        lat,
			Lon:        lon,
			EventCount: len(b.events),
			Sources:    sources,
			AvgUrgency: avgUrg,
			Sentiment:  bestSent,
			Tickers:    tickers,
			EventIDs:   eventIDs,
		})
	}

	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].AvgUrgency == clusters[j].AvgUrgency {
			if clusters[i].EventCount == clusters[j].EventCount {
				return clusters[i].ID < clusters[j].ID
			}
			return clusters[i].EventCount > clusters[j].EventCount
		}
		return clusters[i].AvgUrgency > clusters[j].AvgUrgency
	})
	if len(clusters) > 24 {
		clusters = clusters[:24]
	}
	return clusters
}

func titleTokenSet(question string) map[string]bool {
	stop := map[string]bool{
		"will": true, "the": true, "in": true, "on": true, "at": true, "by": true,
		"to": true, "of": true, "for": true, "and": true, "or": true, "a": true, "an": true, "be": true,
	}
	words := strings.FieldsFunc(strings.ToLower(question), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make(map[string]bool, len(words))
	for _, w := range words {
		if len(w) >= 2 && !stop[w] {
			out[w] = true
		}
	}
	return out
}

func jaccardSimilarity(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union <= 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// FindPredictionArbitrage identifies overlapping Polymarket vs Kalshi markets with >= 3% spread.
func FindPredictionArbitrage(markets []PredictionMarket) []PredictionArbitrage {
	var polys, kalshis []PredictionMarket
	for _, m := range markets {
		switch m.Venue {
		case VenuePolymarket:
			polys = append(polys, m)
		case VenueKalshi:
			kalshis = append(kalshis, m)
		}
	}
	if len(polys) == 0 || len(kalshis) == 0 {
		return nil
	}

	polyTokens := make([]map[string]bool, len(polys))
	for i, m := range polys {
		polyTokens[i] = titleTokenSet(m.Question)
	}
	kalshiTokens := make([]map[string]bool, len(kalshis))
	for j, m := range kalshis {
		kalshiTokens[j] = titleTokenSet(m.Question)
	}

	var out []PredictionArbitrage
	usedKalshi := make(map[string]bool)
	for i, p := range polys {
		bestJ := -1
		bestSim := 0.0
		for j := range kalshis {
			if usedKalshi[kalshis[j].ID] {
				continue
			}
			sim := jaccardSimilarity(polyTokens[i], kalshiTokens[j])
			if sim >= 0.40 && sim > bestSim {
				bestSim = sim
				bestJ = j
			}
		}
		if bestJ < 0 {
			continue
		}
		k := kalshis[bestJ]
		spreadPct := math.Round(math.Abs(p.YesProb-k.YesProb)*10000) / 100
		if spreadPct < 3.0 {
			continue
		}
		usedKalshi[k.ID] = true

		tickSet := make(map[string]bool)
		for _, t := range p.Tickers {
			tickSet[t] = true
		}
		for _, t := range k.Tickers {
			tickSet[t] = true
		}
		var tickers []string
		for t := range tickSet {
			tickers = append(tickers, t)
		}
		sort.Strings(tickers)

		out = append(out, PredictionArbitrage{
			ID:             news.Hash([]byte("arb:" + p.ID + ":" + k.ID))[:16],
			Topic:          p.Question,
			PolymarketID:   p.ID,
			KalshiID:       k.ID,
			PolymarketProb: clamp01(p.YesProb),
			KalshiProb:     clamp01(k.YesProb),
			SpreadPct:      spreadPct,
			Similarity:     math.Round(bestSim*1000) / 1000,
			PolymarketURL:  p.URL,
			KalshiURL:      k.URL,
			Tickers:        tickers,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SpreadPct == out[j].SpreadPct {
			return out[i].ID < out[j].ID
		}
		return out[i].SpreadPct > out[j].SpreadPct
	})
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

func parseFlexibleFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0
		}
		return x
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0
		}
		return parsed
	default:
		return 0
	}
}
