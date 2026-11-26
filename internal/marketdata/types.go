package marketdata

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	VenueHyperliquid = "hyperliquid"
	VenueBinance     = "binance"
	VenueBybit       = "bybit"
	VenueOKX         = "okx"
	VenueKraken      = "kraken"
	VenueCoinbase    = "coinbase"
	VenueDeribit     = "deribit"
	VenueSolana      = "solana"
	VenueLighter     = "lighter"
	VenueAlpaca      = "alpaca"
	VenueFRED        = "fred"
	VenueSEC         = "sec"
	VenueCustom      = "custom"
)

const (
	StyleStandard       = "standard"
	StyleHeikinAshi     = "heikin_ashi"
	StyleRenko          = "renko"
	StyleRange          = "range"
	StyleKagi           = "kagi"
	StylePointFigure    = "point_figure"
	StyleThreeLineBreak = "three_line_break"
	StyleTick           = "tick"
	StyleVolume         = "volume"
)

const (
	maxCandlesLimit      = 2000
	maxTransformedBars   = 10000
	maxWatchlistItems    = 64
	maxDrawingsPerSymbol = 64
	maxDrawingsTotal     = 512
	maxCustomFeeds       = 64
	maxCSVBytes          = 512 * 1024

	DrawingKindHLine     = "hline"
	DrawingKindTrendline = "trendline"
	DrawingKindFib       = "fib"
)

var (
	validIntervals = map[string]int64{
		"1m":  60,
		"5m":  300,
		"15m": 900,
		"1h":  3600,
		"4h":  14400,
		"1d":  86400,
	}

	validStyles = map[string]bool{
		StyleStandard:       true,
		StyleHeikinAshi:     true,
		StyleRenko:          true,
		StyleRange:          true,
		StyleKagi:           true,
		StylePointFigure:    true,
		StyleThreeLineBreak: true,
		StyleTick:           true,
		StyleVolume:         true,
	}

	validVenues = map[string]bool{
		VenueHyperliquid: true,
		VenueBinance:     true,
		VenueBybit:       true,
		VenueOKX:         true,
		VenueKraken:      true,
		VenueCoinbase:    true,
		VenueDeribit:     true,
		VenueSolana:      true,
		VenueLighter:     true,
		VenueAlpaca:      true,
		VenueFRED:        true,
		VenueSEC:         true,
		VenueCustom:      true,
	}

	symbolPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,63}$`)
	seriesIDPattern = regexp.MustCompile(`^[A-Z0-9_]{1,32}$`)
	digitsPattern   = regexp.MustCompile(`^[0-9]{1,10}$`)
	idPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// Instrument describes a tradeable asset or tracked feed on a read-only venue.
type Instrument struct {
	Venue       string  `json:"venue"`
	Symbol      string  `json:"symbol"`
	Base        string  `json:"base"`
	Quote       string  `json:"quote"`
	AssetClass  string  `json:"assetClass"`
	TickSize    float64 `json:"tickSize,omitempty"`
	LotSize     float64 `json:"lotSize,omitempty"`
	MaxLeverage float64 `json:"maxLeverage,omitempty"`
	UpdatedAt   int64   `json:"updatedAt"`
}

// Candle represents a single OHLCV or transformed price bar.
type Candle struct {
	Time       int64   `json:"time"`
	Open       float64 `json:"open"`
	High       float64 `json:"high"`
	Low        float64 `json:"low"`
	Close      float64 `json:"close"`
	Volume     float64 `json:"volume"`
	BuyVolume  float64 `json:"buyVolume,omitempty"`
	SellVolume float64 `json:"sellVolume,omitempty"`
	Trades     int64   `json:"trades,omitempty"`
}

// TransformOptions parameterizes candle transformations and tick/volume aggregation.
type TransformOptions struct {
	Style           string  `json:"style"`
	BrickSize       float64 `json:"brickSize,omitempty"`
	RangeSize       float64 `json:"rangeSize,omitempty"`
	Reversal        float64 `json:"reversal,omitempty"`
	BoxSize         float64 `json:"boxSize,omitempty"`
	LineBreakCount  int     `json:"lineBreakCount,omitempty"`
	TickCount       int     `json:"tickCount,omitempty"`
	VolumeThreshold float64 `json:"volumeThreshold,omitempty"`
}

// CandleSeries is returned by candle query endpoints and MCP tools.
type CandleSeries struct {
	Venue     string   `json:"venue"`
	Symbol    string   `json:"symbol"`
	Interval  string   `json:"interval"`
	Style     string   `json:"style"`
	Candles   []Candle `json:"candles"`
	Source    string   `json:"source"`
	Fixture   bool     `json:"fixture,omitempty"`
	UpdatedAt int64    `json:"updatedAt"`
}

// TradeTick represents an individual public market trade.
type TradeTick struct {
	ID    string  `json:"id"`
	Time  int64   `json:"time"` // Unix milliseconds.
	Price float64 `json:"price"`
	Size  float64 `json:"size"`
	Side  string  `json:"side"` // "buy" or "sell"
}

// OrderBookLevel represents an aggregated price level on one side of the book.
type OrderBookLevel struct {
	Price float64 `json:"price"`
	Size  float64 `json:"size"`
	Total float64 `json:"total"`
}

// OrderBookSnapshot captures L2 bid/ask depth and derived top-of-book metrics.
type OrderBookSnapshot struct {
	Venue    string           `json:"venue"`
	Symbol   string           `json:"symbol"`
	Time     int64            `json:"time"` // Unix milliseconds.
	Bids     []OrderBookLevel `json:"bids"`
	Asks     []OrderBookLevel `json:"asks"`
	Spread   float64          `json:"spread"`
	MidPrice float64          `json:"midPrice"`
	Source   string           `json:"source"`
	Fixture  bool             `json:"fixture,omitempty"`
}

// FootprintLevel aggregates bid/ask volume at a discrete price step within a bar.
type FootprintLevel struct {
	Price    float64 `json:"price"`
	BidVol   float64 `json:"bidVol"`
	AskVol   float64 `json:"askVol"`
	Delta    float64 `json:"delta"`
	TotalVol float64 `json:"totalVol"`
}

// FootprintBar holds orderflow footprint levels, CVD, VPOC, and 70% Value Area bounds.
type FootprintBar struct {
	Time         int64            `json:"time"`
	Open         float64          `json:"open"`
	High         float64          `json:"high"`
	Low          float64          `json:"low"`
	Close        float64          `json:"close"`
	Volume       float64          `json:"volume"`
	CandleVolume float64          `json:"candleVolume,omitempty"`
	Delta        float64          `json:"delta"`
	CVD          float64          `json:"cvd"`
	VPOC         float64          `json:"vpoc"`
	VAH          float64          `json:"vah"`
	VAL          float64          `json:"val"`
	SourceKind   string           `json:"sourceKind,omitempty"`
	TickCoverage float64          `json:"tickCoverage,omitempty"`
	Levels       []FootprintLevel `json:"levels"`
}

// FootprintSeries wraps calculated footprint bars for a venue/symbol/interval.
type FootprintSeries struct {
	Venue     string         `json:"venue"`
	Symbol    string         `json:"symbol"`
	Interval  string         `json:"interval"`
	StepSize  float64        `json:"stepSize"`
	Bars      []FootprintBar `json:"bars"`
	Source    string         `json:"source"`
	Fixture   bool           `json:"fixture,omitempty"`
	UpdatedAt int64          `json:"updatedAt"`
}

// MacroPoint represents a single macro observation from FRED.
type MacroPoint struct {
	SeriesID  string  `json:"seriesId"`
	Date      string  `json:"date"`
	Value     float64 `json:"value"`
	Source    string  `json:"source"`
	UpdatedAt int64   `json:"updatedAt"`
}

// SECFiling describes an indexed filing from SEC EDGAR submissions.
type SECFiling struct {
	CIK             string `json:"cik"`
	Company         string `json:"company"`
	Form            string `json:"form"`
	FilingDate      string `json:"filingDate"`
	AccessionNumber string `json:"accessionNumber"`
	PrimaryDocument string `json:"primaryDocument"`
	URL             string `json:"url"`
	Description     string `json:"description,omitempty"`
}

// CohortEntry captures a single public trader cohort row.
type CohortEntry struct {
	Address      string  `json:"address"`
	DisplayName  string  `json:"displayName,omitempty"`
	AccountValue float64 `json:"accountValue"`
	DayROI       float64 `json:"dayRoi"`
	WeekROI      float64 `json:"weekRoi"`
	MonthROI     float64 `json:"monthRoi"`
	AllTimeROI   float64 `json:"allTimeRoi"`
	Rank         int     `json:"rank"`
}

// CohortSnapshot summarizes top leaderboard cohorts for a window.
type CohortSnapshot struct {
	Venue     string        `json:"venue"`
	Window    string        `json:"window"`
	UpdatedAt int64         `json:"updatedAt"`
	Entries   []CohortEntry `json:"entries"`
	Source    string        `json:"source"`
	Fixture   bool          `json:"fixture,omitempty"`
}

// WatchlistItem identifies an entry on a saved user watchlist.
type WatchlistItem struct {
	Venue    string `json:"venue"`
	Symbol   string `json:"symbol"`
	Label    string `json:"label,omitempty"`
	Interval string `json:"interval,omitempty"`
	Style    string `json:"style,omitempty"`
}

// Watchlist persists a user's saved symbol collection.
type Watchlist struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Items     []WatchlistItem `json:"items"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
}

// Event is pushed over the in-process Bus to SSE subscribers.
type Event struct {
	Type      string `json:"type"`
	Venue     string `json:"venue,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
	Interval  string `json:"interval,omitempty"`
	Style     string `json:"style,omitempty"`
	Timestamp int64  `json:"timestamp"`
	Payload   any    `json:"payload,omitempty"`
}

// Filter restricts Bus subscriptions when fields are non-empty.
type Filter struct {
	Venue    string
	Symbol   string
	Interval string
	Style    string
	Type     string
}

// NormalizeVenue validates and canonicalizes a supported market data venue name.
func NormalizeVenue(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if !validVenues[v] {
		return "", fmt.Errorf("unsupported market venue %q", raw)
	}
	return v, nil
}

// NormalizeSymbol validates a symbol/pair/token address without path traversal or query injection.
func NormalizeSymbol(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if !symbolPattern.MatchString(s) || strings.Contains(s, "..") {
		return "", fmt.Errorf("invalid symbol %q", raw)
	}
	return s, nil
}

// NormalizeInterval validates and canonicalizes a bar interval.
func NormalizeInterval(raw string) (string, error) {
	iv := strings.ToLower(strings.TrimSpace(raw))
	if iv == "" {
		return "1h", nil
	}
	if _, ok := validIntervals[iv]; !ok {
		return "", fmt.Errorf("unsupported interval %q", raw)
	}
	return iv, nil
}

// IntervalSeconds returns the duration of a normalized interval in seconds.
func IntervalSeconds(interval string) (int64, error) {
	iv, err := NormalizeInterval(interval)
	if err != nil {
		return 0, err
	}
	return validIntervals[iv], nil
}

// NormalizeStyle validates a candle rendering style.
func NormalizeStyle(raw string) (string, error) {
	st := strings.ToLower(strings.TrimSpace(raw))
	if st == "" {
		return StyleStandard, nil
	}
	if !validStyles[st] {
		return "", fmt.Errorf("unsupported candle style %q", raw)
	}
	return st, nil
}

// NormalizeCohortWindow validates and canonicalizes a leaderboard window.
func NormalizeCohortWindow(raw string) (string, error) {
	w := strings.ToLower(strings.TrimSpace(raw))
	switch w {
	case "", "week":
		return "week", nil
	case "day":
		return "day", nil
	case "month":
		return "month", nil
	case "alltime":
		return "allTime", nil
	default:
		return "", fmt.Errorf("unsupported cohort window %q", raw)
	}
}

// NormalizeSeriesID validates a FRED macro series identifier.
func NormalizeSeriesID(raw string) (string, error) {
	id := strings.ToUpper(strings.TrimSpace(raw))
	if !seriesIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid FRED series id %q", raw)
	}
	return id, nil
}

// NormalizeCIK validates and left-pads an SEC CIK to 10 digits.
func NormalizeCIK(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if !digitsPattern.MatchString(trimmed) {
		return "", fmt.Errorf("invalid SEC CIK %q", raw)
	}
	return fmt.Sprintf("%010s", trimmed), nil
}

// ValidateWatchlist sanitizes a Watchlist before persistence.
func ValidateWatchlist(w Watchlist) (Watchlist, error) {
	id := strings.TrimSpace(w.ID)
	if !idPattern.MatchString(id) {
		return Watchlist{}, errors.New("watchlist id must match [A-Za-z0-9._-] (1-64 chars)")
	}
	name := strings.TrimSpace(w.Name)
	if name == "" || len(name) > 80 {
		return Watchlist{}, errors.New("watchlist name must be 1-80 characters")
	}
	if len(w.Items) > maxWatchlistItems {
		return Watchlist{}, fmt.Errorf("watchlist exceeds %d items", maxWatchlistItems)
	}
	out := Watchlist{
		ID:        id,
		Name:      name,
		Items:     make([]WatchlistItem, 0, len(w.Items)),
		CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt,
	}
	for _, it := range w.Items {
		v, err := NormalizeVenue(it.Venue)
		if err != nil {
			return Watchlist{}, err
		}
		sym, err := NormalizeSymbol(it.Symbol)
		if err != nil {
			return Watchlist{}, err
		}
		iv := it.Interval
		if iv != "" {
			iv, err = NormalizeInterval(iv)
			if err != nil {
				return Watchlist{}, err
			}
		} else {
			iv = "1h"
		}
		st := it.Style
		if st != "" {
			st, err = NormalizeStyle(st)
			if err != nil {
				return Watchlist{}, err
			}
		} else {
			st = StyleStandard
		}
		label := strings.TrimSpace(it.Label)
		if len(label) > 64 {
			label = label[:64]
		}
		out.Items = append(out.Items, WatchlistItem{
			Venue:    v,
			Symbol:   sym,
			Label:    label,
			Interval: iv,
			Style:    st,
		})
	}
	return out, nil
}

func isFinitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

func isFiniteNonNeg(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

// DrawingPoint represents an anchor coordinate on a chart series.
type DrawingPoint struct {
	Time  int64   `json:"time"`
	Price float64 `json:"price"`
}

// ChartDrawing persists a user-drawn horizontal level, trendline, or fib retracement.
type ChartDrawing struct {
	ID        string        `json:"id"`
	Venue     string        `json:"venue"`
	Symbol    string        `json:"symbol"`
	Interval  string        `json:"interval,omitempty"`
	Kind      string        `json:"kind"`
	P1        DrawingPoint  `json:"p1"`
	P2        *DrawingPoint `json:"p2,omitempty"`
	Label     string        `json:"label,omitempty"`
	CreatedAt int64         `json:"createdAt"`
	UpdatedAt int64         `json:"updatedAt"`
}

// ValidateChartDrawing canonicalizes and bounds-checks a ChartDrawing payload.
func ValidateChartDrawing(d ChartDrawing) (ChartDrawing, error) {
	id := strings.TrimSpace(d.ID)
	if !idPattern.MatchString(id) {
		return ChartDrawing{}, errors.New("drawing id must match [A-Za-z0-9._-] (1-64 chars)")
	}
	venue, err := NormalizeVenue(d.Venue)
	if err != nil {
		return ChartDrawing{}, err
	}
	symbol, err := NormalizeSymbol(d.Symbol)
	if err != nil {
		return ChartDrawing{}, err
	}
	interval := ""
	if rawIv := strings.TrimSpace(d.Interval); rawIv != "" {
		iv, ivErr := NormalizeInterval(rawIv)
		if ivErr != nil {
			return ChartDrawing{}, ivErr
		}
		interval = iv
	}
	kind := strings.ToLower(strings.TrimSpace(d.Kind))
	switch kind {
	case DrawingKindHLine, DrawingKindTrendline, DrawingKindFib:
	default:
		return ChartDrawing{}, fmt.Errorf("unsupported drawing kind %q", d.Kind)
	}
	if d.P1.Time < 0 || !isFinitePositive(d.P1.Price) {
		return ChartDrawing{}, errors.New("drawing p1 must have non-negative time and positive finite price")
	}
	var p2Copy *DrawingPoint
	if kind == DrawingKindTrendline || kind == DrawingKindFib {
		if d.P2 == nil || d.P2.Time < 0 || !isFinitePositive(d.P2.Price) {
			return ChartDrawing{}, fmt.Errorf("drawing kind %q requires p2 with non-negative time and positive finite price", kind)
		}
		pt := *d.P2
		p2Copy = &pt
	} else if d.P2 != nil {
		if d.P2.Time < 0 || !isFinitePositive(d.P2.Price) {
			return ChartDrawing{}, errors.New("drawing p2 must have non-negative time and positive finite price")
		}
		pt := *d.P2
		p2Copy = &pt
	}
	label := strings.TrimSpace(d.Label)
	if len(label) > 80 {
		label = label[:80]
	}
	return ChartDrawing{
		ID:        id,
		Venue:     venue,
		Symbol:    symbol,
		Interval:  interval,
		Kind:      kind,
		P1:        d.P1,
		P2:        p2Copy,
		Label:     label,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}, nil
}

// EvaluateDrawingPrice resolves the reference price of a drawing at the given unix timestamp.
// For horizontal lines it returns p1.price; for trendlines it linearly projects p1->p2;
// for fib drawings it returns the golden 61.8% retracement level matching computeFibLevels.
func EvaluateDrawingPrice(d ChartDrawing, atTime int64) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(d.Kind)) {
	case DrawingKindHLine:
		if !isFinitePositive(d.P1.Price) {
			return 0, false
		}
		return d.P1.Price, true
	case DrawingKindTrendline:
		if !isFinitePositive(d.P1.Price) {
			return 0, false
		}
		if d.P2 == nil || !isFinitePositive(d.P2.Price) {
			return d.P1.Price, true
		}
		if d.P2.Time == d.P1.Time {
			return d.P2.Price, true
		}
		slope := (d.P2.Price - d.P1.Price) / float64(d.P2.Time-d.P1.Time)
		level := d.P1.Price + slope*float64(atTime-d.P1.Time)
		if !isFinitePositive(level) {
			return 0, false
		}
		return level, true
	case DrawingKindFib:
		if !isFinitePositive(d.P1.Price) {
			return 0, false
		}
		if d.P2 == nil || !isFinitePositive(d.P2.Price) {
			return d.P1.Price, true
		}
		high := math.Max(d.P1.Price, d.P2.Price)
		low := math.Min(d.P1.Price, d.P2.Price)
		level := high - (high-low)*0.618
		if !isFinitePositive(level) {
			return 0, false
		}
		return level, true
	default:
		return 0, false
	}
}

// CustomFeedSummary describes a user-ingested custom OHLCV symbol+interval feed.
type CustomFeedSummary struct {
	Symbol    string `json:"symbol"`
	Interval  string `json:"interval"`
	BarCount  int    `json:"barCount"`
	LastTime  int64  `json:"lastTime"`
	UpdatedAt int64  `json:"updatedAt"`
}

// IngestCandlesRequest imports CSV or JSON OHLCV bars into the local "custom" venue.
type IngestCandlesRequest struct {
	Symbol   string   `json:"symbol"`
	Interval string   `json:"interval,omitempty"`
	Format   string   `json:"format,omitempty"`
	CSV      string   `json:"csv,omitempty"`
	Candles  []Candle `json:"candles,omitempty"`
}

func parseCSVTimestamp(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, errors.New("empty timestamp")
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n >= 1_000_000_000_000 {
			n /= 1000
		}
		if n <= 0 {
			return 0, errors.New("timestamp must be positive")
		}
		return n, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			if t.Unix() <= 0 {
				return 0, errors.New("timestamp out of range")
			}
			return t.UTC().Unix(), nil
		}
	}
	return 0, fmt.Errorf("invalid timestamp %q", s)
}

// ParseCSVOHLCV parses CSV OHLCV lines ("time,open,high,low,close[,volume]") into sorted, validated candles.
func ParseCSVOHLCV(raw string) ([]Candle, error) {
	if len(raw) > maxCSVBytes {
		return nil, fmt.Errorf("csv payload exceeds %d bytes", maxCSVBytes)
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	out := make([]Candle, 0, min(len(lines), maxCandlesLimit))
	sawData := false
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.Split(trimmed, ",")
		if len(parts) < 5 || len(parts) > 8 {
			return nil, fmt.Errorf("line %d: expected 5..6 comma-separated columns", idx+1)
		}
		col0 := strings.TrimSpace(strings.Trim(parts[0], `"'`))
		ts, tsErr := parseCSVTimestamp(col0)
		if tsErr != nil {
			low := strings.ToLower(col0)
			if !sawData && (strings.Contains(low, "time") || strings.Contains(low, "date") || low == "ts") {
				continue
			}
			return nil, fmt.Errorf("line %d: %w", idx+1, tsErr)
		}
		parseF := func(col int, name string) (float64, error) {
			v, err := strconv.ParseFloat(strings.TrimSpace(strings.Trim(parts[col], `"'`)), 64)
			if err != nil {
				return 0, fmt.Errorf("line %d: invalid %s", idx+1, name)
			}
			return v, nil
		}
		o, err := parseF(1, "open")
		if err != nil {
			return nil, err
		}
		h, err := parseF(2, "high")
		if err != nil {
			return nil, err
		}
		l, err := parseF(3, "low")
		if err != nil {
			return nil, err
		}
		c, err := parseF(4, "close")
		if err != nil {
			return nil, err
		}
		var vol float64
		if len(parts) >= 6 && strings.TrimSpace(parts[5]) != "" {
			vol, err = parseF(5, "volume")
			if err != nil {
				return nil, err
			}
		}
		if !isFinitePositive(o) || !isFinitePositive(h) || !isFinitePositive(l) || !isFinitePositive(c) || !isFiniteNonNeg(vol) {
			return nil, fmt.Errorf("line %d: non-positive OHLC or invalid volume", idx+1)
		}
		if l > o || l > c || h < o || h < c || l > h {
			return nil, fmt.Errorf("line %d: inconsistent high/low bounds", idx+1)
		}
		sawData = true
		out = append(out, Candle{Time: ts, Open: o, High: h, Low: l, Close: c, Volume: vol})
		if len(out) > maxCandlesLimit {
			return nil, fmt.Errorf("csv exceeds %d candle limit", maxCandlesLimit)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no valid OHLCV rows found")
	}
	return normalizeCandleSlice(out)
}

func normalizeCandleSlice(in []Candle) ([]Candle, error) {
	if len(in) == 0 {
		return nil, errors.New("at least 1 candle is required")
	}
	if len(in) > maxCandlesLimit {
		return nil, fmt.Errorf("candles exceed %d limit", maxCandlesLimit)
	}
	cleaned := make([]Candle, 0, len(in))
	for i, c := range in {
		ts := c.Time
		if ts >= 1_000_000_000_000 {
			ts /= 1000
		}
		if ts <= 0 || !isFinitePositive(c.Open) || !isFinitePositive(c.High) || !isFinitePositive(c.Low) || !isFinitePositive(c.Close) || !isFiniteNonNeg(c.Volume) {
			return nil, fmt.Errorf("candle %d: invalid timestamp or OHLCV values", i)
		}
		if c.Low > c.Open || c.Low > c.Close || c.High < c.Open || c.High < c.Close || c.Low > c.High {
			return nil, fmt.Errorf("candle %d: inconsistent high/low bounds", i)
		}
		c.Time = ts
		cleaned = append(cleaned, c)
	}
	sort.Slice(cleaned, func(i, j int) bool { return cleaned[i].Time < cleaned[j].Time })
	deduped := make([]Candle, 0, len(cleaned))
	for _, c := range cleaned {
		if len(deduped) > 0 && deduped[len(deduped)-1].Time == c.Time {
			deduped[len(deduped)-1] = c
		} else {
			deduped = append(deduped, c)
		}
	}
	return deduped, nil
}
