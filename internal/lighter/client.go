package lighter

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

const maxResponseBytes = 4 * 1024 * 1024

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// Client is a bounded SSRF-safe Lighter L2 REST adapter.
type Client struct {
	network    string
	origin     string
	http       *http.Client
	readOnly   bool
	controlled bool
}

func isPublicUnicastIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified() &&
		!cgnatPrefix.Contains(ip)
}

func interleavePublicIPs(ips []netip.Addr) ([]netip.Addr, error) {
	if len(ips) == 0 {
		return nil, errors.New("Lighter API address unavailable")
	}
	var v4, v6 []netip.Addr
	for _, ip := range ips {
		if !isPublicUnicastIP(ip) {
			return nil, errors.New("private or loopback Lighter address denied")
		}
		if ip.Is4() {
			v4 = append(v4, ip)
		} else {
			v6 = append(v6, ip)
		}
	}
	out := make([]netip.Addr, 0, min(len(ips), 6))
	for i := 0; len(out) < 6 && (i < len(v4) || i < len(v6)); i++ {
		if i < len(v4) {
			out = append(out, v4[i])
		}
		if len(out) < 6 && i < len(v6) {
			out = append(out, v6[i])
		}
	}
	return out, nil
}

func dialPublicAddress(
	ctx context.Context,
	network, address string,
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	rawIPs, err := lookup(ctx, "ip", host)
	if err != nil {
		return nil, errors.New("Lighter API address unavailable")
	}
	targets, err := interleavePublicIPs(rawIPs)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range targets {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, dErr := dial(attemptCtx, network, net.JoinHostPort(ip.String(), port))
		cancel()
		if dErr == nil {
			return conn, nil
		}
		lastErr = dErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("Lighter API address unavailable")
}

func newHardenedHTTPClient() *http.Client {
	t := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:       tls.VersionTLS12,
			CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
		},
		Proxy:                 nil,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		DialContext: func(ctx context.Context, netName, address string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}
			return dialPublicAddress(ctx, netName, address, net.DefaultResolver.LookupNetIP, dialer.DialContext)
		},
	}
	return &http.Client{
		Transport:     t,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Lighter redirects denied") },
	}
}

// NewTestnetClient constructs a live Stage 4 client pinned strictly to TestnetAPIOrigin.
func NewTestnetClient() *Client {
	return &Client{
		network:    "testnet",
		origin:     TestnetAPIOrigin,
		http:       newHardenedHTTPClient(),
		readOnly:   false,
		controlled: true,
	}
}

// NewReadOnlyMainnetClient constructs a strict read-only Lighter Mainnet API client.
func NewReadOnlyMainnetClient() *Client {
	return &Client{
		network:  "mainnet",
		origin:   MainnetAPIOrigin,
		http:     newHardenedHTTPClient(),
		readOnly: true,
	}
}

// NewControlledClient attaches an isolated test RoundTripper for Lighter simulation.
func NewControlledClient(network string, rt ControlledTransport) (*Client, error) {
	if rt == nil || !rt.ControlledFixture() {
		return nil, errors.New("controlled Lighter transport required")
	}
	netName := strings.ToLower(strings.TrimSpace(network))
	origin := TestnetAPIOrigin
	if netName == "mainnet" {
		origin = MainnetAPIOrigin
	} else if netName != "testnet" {
		return nil, errors.New("Lighter network must be testnet or mainnet")
	}
	return &Client{
		network: netName,
		origin:  origin,
		http: &http.Client{
			Transport:     rt,
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Lighter redirects denied") },
		},
		controlled: true,
	}, nil
}

func (c *Client) Network() string { return c.network }
func (c *Client) Origin() string  { return c.origin }

func (c *Client) doRequest(ctx context.Context, method, pathAndQuery, contentType string, rawBody []byte) (int, []byte, error) {
	if c == nil || c.http == nil {
		return 0, nil, errors.New("Lighter client unavailable")
	}
	var bodyReader io.Reader
	if len(rawBody) > 0 {
		bodyReader = bytes.NewReader(rawBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+pathAndQuery, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, errors.New("Lighter transport unavailable")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return resp.StatusCode, nil, errors.New("Lighter response unreadable or oversized")
	}
	return resp.StatusCode, raw, nil
}

// FetchOrderBooks calls public GET /api/v1/orderBooks.
func (c *Client) FetchOrderBooks(ctx context.Context) ([]OrderBookMeta, error) {
	status, raw, err := c.doRequest(ctx, http.MethodGet, "/api/v1/orderBooks", "", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Lighter /orderBooks status %d", status)
	}
	var doc struct {
		Code       int             `json:"code"`
		OrderBooks []OrderBookMeta `json:"order_books"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errors.New("malformed Lighter /orderBooks response")
	}
	return doc.OrderBooks, nil
}

// FetchOrderBookOrders calls public GET /api/v1/orderBookOrders?market_id=...&limit=...
func (c *Client) FetchOrderBookOrders(ctx context.Context, marketID int64, limit int) (*OrderBookOrdersResponse, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	u := fmt.Sprintf("/api/v1/orderBookOrders?market_id=%d&limit=%d", marketID, limit)
	status, raw, err := c.doRequest(ctx, http.MethodGet, u, "", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Lighter /orderBookOrders status %d", status)
	}
	var out OrderBookOrdersResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.New("malformed Lighter /orderBookOrders response")
	}
	return &out, nil
}

// FetchCandles calls public GET /api/v1/candles for historical OHLCV bars.
func (c *Client) FetchCandles(ctx context.Context, marketID int64, resolution string, startMs, endMs int64, countBack int) ([]CandleRow, error) {
	if countBack <= 0 || countBack > 1000 {
		countBack = 200
	}
	q := url.Values{
		"market_id":       {strconv.FormatInt(marketID, 10)},
		"resolution":      {strings.TrimSpace(resolution)},
		"start_timestamp": {strconv.FormatInt(startMs, 10)},
		"end_timestamp":   {strconv.FormatInt(endMs, 10)},
		"count_back":      {strconv.Itoa(countBack)},
	}
	status, raw, err := c.doRequest(ctx, http.MethodGet, "/api/v1/candles?"+q.Encode(), "", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Lighter /candles status %d", status)
	}
	var doc struct {
		C []CandleRow `json:"c"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errors.New("malformed Lighter /candles response")
	}
	return doc.C, nil
}

// FetchRecentTrades calls public GET /api/v1/recentTrades?market_id=...&limit=...
func (c *Client) FetchRecentTrades(ctx context.Context, marketID int64, limit int) ([]TradeRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	u := fmt.Sprintf("/api/v1/recentTrades?market_id=%d&limit=%d", marketID, limit)
	status, raw, err := c.doRequest(ctx, http.MethodGet, u, "", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Lighter /recentTrades status %d", status)
	}
	var doc struct {
		Trades []TradeRow `json:"trades"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errors.New("malformed Lighter /recentTrades response")
	}
	return doc.Trades, nil
}

// FetchNextNonce queries GET /api/v1/nextNonce?account_index=...&api_key_index=...
func (c *Client) FetchNextNonce(ctx context.Context, accountIndex int64, apiKeyIndex uint8) (int64, error) {
	u := fmt.Sprintf("/api/v1/nextNonce?account_index=%d&api_key_index=%d", accountIndex, apiKeyIndex)
	status, raw, err := c.doRequest(ctx, http.MethodGet, u, "", nil)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("Lighter /nextNonce status %d", status)
	}
	var doc struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Nonce   int64  `json:"nonce"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return 0, errors.New("malformed Lighter /nextNonce response")
	}
	if doc.Code != 200 {
		return 0, fmt.Errorf("Lighter /nextNonce error %d: %s", doc.Code, doc.Message)
	}
	return doc.Nonce, nil
}

func parseScaledUint(raw string, decimals int) (uint64, error) {
	if decimals < 0 || decimals > 12 {
		return 0, errors.New("unsupported market decimal precision")
	}
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 32 || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		return 0, errors.New("decimal value must be unsigned")
	}
	dot := strings.IndexByte(s, '.')
	intPart, fracPart := s, ""
	if dot >= 0 {
		intPart = s[:dot]
		fracPart = s[dot+1:]
		if intPart == "" || fracPart == "" || strings.ContainsRune(fracPart, '.') {
			return 0, errors.New("malformed decimal value")
		}
		if len(fracPart) > decimals {
			return 0, fmt.Errorf("precision exceeds %d market decimals", decimals)
		}
	}
	for i := 0; i < len(intPart); i++ {
		if intPart[i] < '0' || intPart[i] > '9' {
			return 0, errors.New("invalid integer digit")
		}
	}
	for i := 0; i < len(fracPart); i++ {
		if fracPart[i] < '0' || fracPart[i] > '9' {
			return 0, errors.New("invalid fractional digit")
		}
	}
	combined := intPart + fracPart + strings.Repeat("0", decimals-len(fracPart))
	return strconv.ParseUint(combined, 10, 64)
}

// BuildCreateOrderIntent converts human-readable price & base size into exact fixed-point
// Lighter L2 integers using OrderBookMeta decimal configuration.
func BuildCreateOrderIntent(draft OrderDraft, meta OrderBookMeta, now int64) (signer.LighterOrderIntent, error) {
	asset := strings.ToUpper(strings.TrimSpace(draft.Symbol))
	if asset == "" {
		asset = strings.ToUpper(strings.TrimSpace(meta.Symbol))
	}
	if asset == "" {
		return signer.LighterOrderIntent{}, errors.New("market symbol required")
	}
	if draft.AccountIndex < 0 {
		return signer.LighterOrderIntent{}, errors.New("accountIndex must be non-negative")
	}
	baseU, err := parseScaledUint(draft.Size, meta.SupportedSizeDecimals)
	if err != nil || baseU == 0 || baseU > (1<<48)-1 {
		return signer.LighterOrderIntent{}, errors.New("lighter base size out of range or precision mismatch")
	}
	priceU, err := parseScaledUint(draft.Price, meta.SupportedPriceDecimals)
	if err != nil || priceU == 0 || priceU > math.MaxUint32 {
		return signer.LighterOrderIntent{}, errors.New("lighter price out of range or precision mismatch")
	}

	var isAsk uint8
	switch strings.ToUpper(strings.TrimSpace(draft.Side)) {
	case "BUY", "BID":
		isAsk = 0
	case "SELL", "ASK":
		isAsk = 1
	default:
		return signer.LighterOrderIntent{}, errors.New("lighter order side must be BUY or SELL")
	}

	var orderType uint8
	switch strings.ToUpper(strings.TrimSpace(draft.OrderType)) {
	case "", "LIMIT":
		orderType = 0
	case "MARKET":
		orderType = 1
	default:
		return signer.LighterOrderIntent{}, errors.New("unsupported lighter orderType")
	}

	var tif uint8
	tifStr := strings.ToUpper(strings.TrimSpace(draft.TimeInForce))
	if tifStr == "" {
		if orderType == 1 {
			tifStr = "IOC"
		} else {
			tifStr = "GTT"
		}
	}
	switch tifStr {
	case "IOC":
		tif = 0
	case "GTT":
		tif = 1
	case "POST_ONLY":
		tif = 2
	default:
		return signer.LighterOrderIntent{}, errors.New("unsupported lighter timeInForce")
	}

	// Exact notional microUSD = baseU * priceU * 1_000_000 / 10^(sizeDecimals + priceDecimals)
	denom := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(meta.SupportedSizeDecimals+meta.SupportedPriceDecimals)), nil)
	num := new(big.Int).Mul(new(big.Int).SetUint64(baseU), new(big.Int).SetUint64(priceU))
	num.Mul(num, big.NewInt(1_000_000))
	microBig := new(big.Int).Div(num, denom)
	if !microBig.IsUint64() || microBig.Sign() <= 0 {
		return signer.LighterOrderIntent{}, errors.New("lighter notional micro-USD out of bounds")
	}

	clientOrderIdx := draft.ClientOrderIndex
	if clientOrderIdx == 0 {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			return signer.LighterOrderIntent{}, err
		}
		var padded [8]byte
		copy(padded[2:], b[:])
		clientOrderIdx = int64(binary.BigEndian.Uint64(padded[:])&((1<<47)-1)) + 1
	}

	opID := strings.TrimSpace(draft.OperationID)
	if opID == "" {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return signer.LighterOrderIntent{}, err
		}
		opID = "lighter-ord-" + hex.EncodeToString(b[:])
	}

	var red uint8
	if draft.ReduceOnly {
		red = 1
	}
	orderExp := (now + 3600) * 1000
	if orderType == 1 || (orderType == 0 && tif == 0) {
		orderExp = 0
	}

	return signer.LighterOrderIntent{
		Network:           "testnet",
		ChainID:           signer.LighterTestnetChainID,
		OperationID:       opID,
		Asset:             asset,
		EstimatedMicroUSD: microBig.Uint64(),
		Leverage:          draft.Leverage,
		AccountIndex:      draft.AccountIndex,
		ApiKeyIndex:       draft.APIKeyIndex,
		MarketIndex:       meta.MarketID,
		ClientOrderIndex:  clientOrderIdx,
		BaseAmount:        int64(baseU),
		Price:             uint32(priceU),
		SizeDecimals:      uint8(meta.SupportedSizeDecimals),
		PriceDecimals:     uint8(meta.SupportedPriceDecimals),
		IsAsk:             isAsk,
		OrderType:         orderType,
		TimeInForce:       tif,
		ReduceOnly:        red,
		TriggerPrice:      0,
		OrderExpiry:       orderExp,
		Nonce:             draft.Nonce,
		ExpiredAt:         (now + 300) * 1000,
		IntentExpiresAt:   now + 300,
	}, nil
}

func (c *Client) canSubmit(signed *signer.LighterSignedTx) error {
	if c == nil || c.readOnly || !c.controlled {
		return ErrExecutionDisabled
	}
	if signed == nil || c.network == "mainnet" || signed.Network == "mainnet" || signed.ChainID == signer.LighterMainnetChainID {
		if !signer.LighterActivationEnabled {
			return ErrExecutionDisabled
		}
	}
	if c.network != "testnet" || signed.Network != "testnet" || signed.ChainID != signer.LighterTestnetChainID {
		return ErrExecutionDisabled
	}
	return nil
}

// SubmitSignedTx posts form-urlencoded (tx_type, tx_info, price_protection=true) to /api/v1/sendTx.
// Mainnet execution remains blocked with ErrExecutionDisabled while locked.
func (c *Client) SubmitSignedTx(ctx context.Context, signed *signer.LighterSignedTx) (SubmissionResult, error) {
	if err := c.canSubmit(signed); err != nil {
		return SubmissionResult{}, err
	}
	form := url.Values{
		"tx_type":          {strconv.Itoa(int(signed.TxType))},
		"tx_info":          {signed.TxInfoJSON},
		"price_protection": {"true"},
	}
	status, raw, err := c.doRequest(ctx, http.MethodPost, "/api/v1/sendTx", "application/x-www-form-urlencoded", []byte(form.Encode()))
	if err != nil {
		return SubmissionResult{}, err
	}
	if status >= 500 {
		return SubmissionResult{StatusCode: status}, fmt.Errorf("Lighter /sendTx upstream error status %d", status)
	}
	var doc struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		TxHash  string `json:"tx_hash"`
	}
	_ = json.Unmarshal(raw, &doc)
	return SubmissionResult{
		Submitted:  status >= 200 && status < 300 && doc.Code == 200,
		StatusCode: status,
		Code:       doc.Code,
		Message:    doc.Message,
		TxHash:     doc.TxHash,
	}, nil
}
