package equities

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const maxQuoteResponseBytes = 256 * 1024

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// Client provides SSRF-safe HTTP transport for Alpaca Paper and loopback transport for IBKR.
type Client struct {
	alpacaHTTP *http.Client
	ibkrHTTP   *http.Client
}

func isPublicUnicastIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified() &&
		!cgnatPrefix.Contains(ip)
}

func dialPublicAddress(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("alpaca paper address unavailable")
	}
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	for _, ip := range ips {
		if !isPublicUnicastIP(ip) {
			return nil, errors.New("private or loopback alpaca address denied")
		}
	}
	for _, ip := range ips {
		c, dErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if dErr == nil {
			return c, nil
		}
	}
	return nil, errors.New("alpaca paper dial failed")
}

// NewClient initializes locked Paper clients for Alpaca (SSRF-filtered) and IBKR (loopback only).
func NewClient() *Client {
	return NewClientWithTransport(nil)
}

// NewClientWithTransport initializes Paper clients with an optional custom HTTP RoundTripper for Alpaca transport testing.
func NewClientWithTransport(override http.RoundTripper) *Client {
	var publicTransport http.RoundTripper = &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		Proxy:           nil,
		DialContext:     dialPublicAddress,
	}
	if override != nil {
		publicTransport = override
	}
	loopbackTransport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		Proxy:           nil,
	}
	return &Client{
		alpacaHTTP: &http.Client{
			Timeout:   8 * time.Second,
			Transport: publicTransport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		ibkrHTTP: &http.Client{
			Timeout:   8 * time.Second,
			Transport: loopbackTransport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// ParseAlpacaSnapshotQuote parses Alpaca Data v2 /v2/stocks/{symbol}/snapshot payloads.
func ParseAlpacaSnapshotQuote(symbol string, raw []byte, nowUnix int64) (Quote, error) {
	clean := strings.ToUpper(strings.TrimSpace(symbol))
	if !tickerPattern.MatchString(clean) {
		return Quote{}, errors.New("invalid equity ticker symbol")
	}
	var snap struct {
		LatestTrade *struct {
			P float64 `json:"p"`
			T string  `json:"t"`
		} `json:"latestTrade"`
		LatestQuote *struct {
			BP float64 `json:"bp"`
			AP float64 `json:"ap"`
			T  string  `json:"t"`
		} `json:"latestQuote"`
		DailyBar *struct {
			V float64 `json:"v"`
		} `json:"dailyBar"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		return Quote{}, fmt.Errorf("decode alpaca snapshot: %w", err)
	}
	var bid, ask, last float64
	if snap.LatestQuote != nil {
		if !math.IsNaN(snap.LatestQuote.BP) && !math.IsInf(snap.LatestQuote.BP, 0) && snap.LatestQuote.BP > 0 {
			bid = snap.LatestQuote.BP
		}
		if !math.IsNaN(snap.LatestQuote.AP) && !math.IsInf(snap.LatestQuote.AP, 0) && snap.LatestQuote.AP > 0 {
			ask = snap.LatestQuote.AP
		}
	}
	if snap.LatestTrade != nil && !math.IsNaN(snap.LatestTrade.P) && !math.IsInf(snap.LatestTrade.P, 0) && snap.LatestTrade.P > 0 {
		last = snap.LatestTrade.P
	}
	if last == 0 && bid > 0 && ask > 0 {
		last = (bid + ask) / 2
	}
	if last <= 0 {
		return Quote{}, errors.New("alpaca snapshot missing valid trade or quote price")
	}
	if bid == 0 {
		bid = last
	}
	if ask == 0 {
		ask = last
	}
	var vol int64
	if snap.DailyBar != nil && !math.IsNaN(snap.DailyBar.V) && !math.IsInf(snap.DailyBar.V, 0) && snap.DailyBar.V >= 0 {
		vol = int64(snap.DailyBar.V)
	}
	ts := nowUnix
	if snap.LatestTrade != nil && snap.LatestTrade.T != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(snap.LatestTrade.T)); err == nil {
			ts = parsed.UTC().Unix()
		}
	} else if snap.LatestQuote != nil && snap.LatestQuote.T != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(snap.LatestQuote.T)); err == nil {
			ts = parsed.UTC().Unix()
		}
	}
	return Quote{
		Symbol:    clean,
		Bid:       bid,
		Ask:       ask,
		Last:      last,
		Volume:    vol,
		Session:   "regular",
		Source:    "alpaca-iex",
		UpdatedAt: ts,
	}, nil
}

// FetchAlpacaSnapshotQuote fetches an SSRF-safe IEX snapshot quote from Alpaca Data API v2.
func (c *Client) FetchAlpacaSnapshotQuote(ctx context.Context, symbol, keyID, secret string, nowUnix int64) (Quote, error) {
	if c == nil || c.alpacaHTTP == nil {
		return Quote{}, errors.New("alpaca http client unavailable")
	}
	clean := strings.ToUpper(strings.TrimSpace(symbol))
	if !tickerPattern.MatchString(clean) {
		return Quote{}, errors.New("invalid equity ticker symbol")
	}
	endpoint := AlpacaDataOrigin + "/v2/stocks/" + url.PathEscape(clean) + "/snapshot?feed=iex"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Quote{}, err
	}
	req.Header.Set("APCA-API-KEY-ID", keyID)
	req.Header.Set("APCA-API-SECRET-KEY", secret)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "fased-equities/1.0")

	resp, err := c.alpacaHTTP.Do(req)
	if err != nil {
		return Quote{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return Quote{}, errors.New("alpaca snapshot redirect blocked by security policy")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxQuoteResponseBytes+1))
	if err != nil {
		return Quote{}, err
	}
	if len(body) > maxQuoteResponseBytes {
		return Quote{}, errors.New("alpaca snapshot response exceeded limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Quote{}, fmt.Errorf("alpaca data returned HTTP %d", resp.StatusCode)
	}
	return ParseAlpacaSnapshotQuote(clean, body, nowUnix)
}

func fixtureSpotForSymbol(sym string) float64 {
	switch strings.ToUpper(strings.TrimSpace(sym)) {
	case "SPY":
		return 598.40
	case "NVDA":
		return 142.85
	case "AAPL":
		return 234.50
	case "QQQ":
		return 514.20
	case "MSFT":
		return 428.10
	default:
		return 125.00
	}
}

func generateFixtureQuote(sym string, nowUnix int64) Quote {
	spot := fixtureSpotForSymbol(sym)
	return Quote{
		Symbol:    strings.ToUpper(strings.TrimSpace(sym)),
		Bid:       math.Round((spot-0.02)*100) / 100,
		Ask:       math.Round((spot+0.02)*100) / 100,
		Last:      spot,
		Volume:    1250000,
		Session:   "regular",
		Source:    "alpaca-paper-fixture",
		UpdatedAt: nowUnix,
	}
}

func generateFixtureOptionsChain(underlying string, now time.Time) (OptionsChainView, error) {
	sym := strings.ToUpper(strings.TrimSpace(underlying))
	if !tickerPattern.MatchString(sym) {
		return OptionsChainView{}, errors.New("invalid underlying symbol")
	}
	spot := fixtureSpotForSymbol(sym)
	step := 5.0
	if spot < 200 {
		step = 2.5
	}
	baseStrike := math.Round(spot/step) * step
	expDate := now.UTC().AddDate(0, 1, 0).Format("2006-01-02")
	contracts := make([]OptionContract, 0, 10)
	for i := -2; i <= 2; i++ {
		strike := math.Round((baseStrike+float64(i)*step)*100) / 100
		if strike <= 0 {
			continue
		}
		for _, right := range []string{"C", "P"} {
			occ, err := FormatOCCSymbol(sym, expDate, right, strike)
			if err != nil {
				return OptionsChainView{}, err
			}
			intrinsic := 0.0
			delta := 0.50
			if right == "C" {
				intrinsic = math.Max(0, spot-strike)
				delta = math.Max(0.10, math.Min(0.90, 0.50+((spot-strike)/(spot*0.20))))
			} else {
				intrinsic = math.Max(0, strike-spot)
				delta = -math.Max(0.10, math.Min(0.90, 0.50+((strike-spot)/(spot*0.20))))
			}
			mid := math.Round((intrinsic+math.Max(1.25, spot*0.015))*100) / 100
			contracts = append(contracts, OptionContract{
				OCCSymbol:         occ,
				Underlying:        sym,
				Expiration:        expDate,
				Right:             right,
				Strike:            strike,
				Bid:               math.Round(math.Max(0.05, mid-0.05)*100) / 100,
				Ask:               math.Round((mid+0.05)*100) / 100,
				ImpliedVolatility: math.Round((0.22+math.Abs(float64(i))*0.015)*1000) / 1000,
				Delta:             math.Round(delta*1000) / 1000,
				OpenInterest:      1200 + int64(5-absInt(i))*350,
			})
		}
	}
	return OptionsChainView{
		Underlying: sym,
		SpotPrice:  spot,
		Contracts:  contracts,
		Source:     "occ-fixture",
		Fixture:    true,
		UpdatedAt:  now.Unix(),
	}, nil
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
