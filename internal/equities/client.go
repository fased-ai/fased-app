package equities

import (
	"context"
	"crypto/tls"
	"errors"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

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
	publicTransport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		Proxy:           nil,
		DialContext:     dialPublicAddress,
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
