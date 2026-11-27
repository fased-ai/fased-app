package kalshi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/netguard"
	"github.com/fased-ai/fased-app/internal/signer"
)

const maxResponseBytes = 1024 * 1024

// RequestSigner signs a canonical Kalshi V2 request (timestampMs + METHOD + pathWithoutQuery)
// inside fased-signer under AgentVaultPolicy bounds.
type RequestSigner func(ctx context.Context, intent signer.KalshiSignIntent) (*signer.KalshiSignedRequest, error)

// Client is a bounded SSRF-safe Kalshi V2 Demo API REST adapter.
type Client struct {
	environment string
	origin      string
	http        *http.Client
	controlled  bool
}

func isPublicUnicastIP(ip netip.Addr) bool {
	return netguard.IsPublicUnicastIP(ip)
}

func interleavePublicIPs(ips []netip.Addr) ([]netip.Addr, error) {
	if len(ips) == 0 {
		return nil, errors.New("Kalshi API address unavailable")
	}
	var v4, v6 []netip.Addr
	for _, ip := range ips {
		if !isPublicUnicastIP(ip) {
			return nil, errors.New("private or loopback Kalshi address denied")
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
		return nil, errors.New("Kalshi API address unavailable")
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
	return nil, errors.New("Kalshi API address unavailable")
}

// NewDemoClient constructs an SSRF-safe Kalshi V2 client pinned strictly to DemoAPIOrigin.
func NewDemoClient() *Client {
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
	return &Client{
		environment: "demo",
		origin:      DemoAPIOrigin,
		http: &http.Client{
			Transport:     t,
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Kalshi redirects denied") },
		},
		controlled: true,
	}
}

// NewControlledClient attaches an isolated test RoundTripper for Kalshi simulation.
func NewControlledClient(env string, rt ControlledTransport) (*Client, error) {
	if rt == nil || !rt.ControlledFixture() {
		return nil, errors.New("controlled Kalshi transport required")
	}
	normEnv := strings.ToLower(strings.TrimSpace(env))
	origin := DemoAPIOrigin
	if normEnv == "prod" || normEnv == "production" {
		normEnv = "prod"
		origin = ProdAPIOrigin
	} else if normEnv != "demo" {
		return nil, errors.New("Kalshi environment must be demo or prod")
	}
	return &Client{
		environment: normEnv,
		origin:      origin,
		http: &http.Client{
			Transport:     rt,
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Kalshi redirects denied") },
		},
		controlled: true,
	}, nil
}

func (c *Client) Environment() string { return c.environment }
func (c *Client) Origin() string      { return c.origin }

func validTicker(s string) bool {
	if len(s) < 2 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func parseScaledDecimal(raw string, scaleDigits int) (uint64, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 24 || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
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
		if len(fracPart) > scaleDigits {
			return 0, fmt.Errorf("precision exceeds %d fractional digits", scaleDigits)
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
	combined := intPart + fracPart + strings.Repeat("0", scaleDigits-len(fracPart))
	v, err := strconv.ParseUint(combined, 10, 64)
	if err != nil {
		return 0, errors.New("decimal value overflow")
	}
	return v, nil
}

func randomHexSuffix(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// BuildOrder validates a Kalshi V2 event order draft, formats canonical fixed-point count
// and dollar price strings, and computes exact worst-case micro-USD collateral risk.
func BuildOrder(draft OrderDraft) (ValidatedOrder, error) {
	ticker := strings.ToUpper(strings.TrimSpace(draft.Ticker))
	if !validTicker(ticker) {
		return ValidatedOrder{}, errors.New("invalid Kalshi market ticker")
	}
	side := strings.ToLower(strings.TrimSpace(draft.Side))
	if side != "bid" && side != "ask" {
		return ValidatedOrder{}, errors.New("kalshi V2 order side must be bid or ask")
	}
	tif := strings.ToLower(strings.TrimSpace(draft.TimeInForce))
	if tif == "" {
		tif = "good_till_canceled"
	}
	switch tif {
	case "good_till_canceled", "immediate_or_cancel", "fill_or_kill":
	default:
		return ValidatedOrder{}, errors.New("unsupported kalshi time_in_force")
	}
	asset := strings.ToUpper(strings.TrimSpace(draft.Asset))
	if asset == "" {
		asset = "USD"
	}

	// Count supports up to 2 fractional decimals (centi-contracts: 100 = 1.00).
	countCenti, err := parseScaledDecimal(draft.Count, 2)
	if err != nil || countCenti < 100 {
		return ValidatedOrder{}, errors.New("kalshi count must be at least 1.00")
	}
	// Price supports up to 4 fractional decimals ($0.0100 = 100, $0.9900 = 9900).
	priceTenK, err := parseScaledDecimal(draft.Price, 4)
	if err != nil || priceTenK < 100 || priceTenK > 9900 {
		return ValidatedOrder{}, errors.New("kalshi dollar price must be within [0.0100, 0.9900]")
	}

	collateralTenK := priceTenK
	if side == "ask" {
		collateralTenK = 10_000 - priceTenK
	}
	// 100 centi-contracts * 10,000 ten-thousandths == 1,000,000 == $1.00 in micro-USD.
	riskBig := new(big.Int).Mul(new(big.Int).SetUint64(countCenti), new(big.Int).SetUint64(collateralTenK))
	if !riskBig.IsUint64() || riskBig.Sign() <= 0 {
		return ValidatedOrder{}, errors.New("kalshi risk notional overflow")
	}
	estMicroUSD := riskBig.Uint64()

	countFmt := fmt.Sprintf("%d.%02d", countCenti/100, countCenti%100)
	priceFmt := fmt.Sprintf("%d.%04d", priceTenK/10_000, priceTenK%10_000)

	clientOrdID := strings.TrimSpace(draft.ClientOrderID)
	if clientOrdID == "" {
		suf, err := randomHexSuffix(8)
		if err != nil {
			return ValidatedOrder{}, err
		}
		clientOrdID = "fased-k-" + suf
	}
	opID := strings.TrimSpace(draft.OperationID)
	if opID == "" {
		opID = clientOrdID
	}

	wire := struct {
		Ticker                  string `json:"ticker"`
		ClientOrderID           string `json:"client_order_id"`
		Side                    string `json:"side"`
		Count                   string `json:"count"`
		Price                   string `json:"price"`
		TimeInForce             string `json:"time_in_force"`
		SelfTradePreventionType string `json:"self_trade_prevention_type"`
	}{
		Ticker:                  ticker,
		ClientOrderID:           clientOrdID,
		Side:                    side,
		Count:                   countFmt,
		Price:                   priceFmt,
		TimeInForce:             tif,
		SelfTradePreventionType: "taker_at_cross",
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return ValidatedOrder{}, err
	}

	outDraft := draft
	outDraft.Ticker = ticker
	outDraft.Side = side
	outDraft.TimeInForce = tif
	outDraft.Asset = asset
	outDraft.ClientOrderID = clientOrdID
	outDraft.OperationID = opID
	return ValidatedOrder{
		Draft:             outDraft,
		CountFormatted:    countFmt,
		PriceFormatted:    priceFmt,
		EstimatedMicroUSD: estMicroUSD,
		PayloadJSON:       payload,
	}, nil
}

func (c *Client) canMutate() error {
	if c == nil || !c.controlled {
		return ErrExecutionDisabled
	}
	if (c.environment == "prod" || c.origin == ProdAPIOrigin) && !signer.KalshiActivationEnabled {
		return ErrExecutionDisabled
	}
	if c.environment != "demo" {
		return ErrExecutionDisabled
	}
	return nil
}

func (c *Client) doRequest(ctx context.Context, method, pathAndQuery string, signed *signer.KalshiSignedRequest, body []byte) (int, []byte, error) {
	if c == nil || c.http == nil {
		return 0, nil, errors.New("Kalshi client unavailable")
	}
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+pathAndQuery, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if signed != nil {
		req.Header.Set("KALSHI-ACCESS-KEY", signed.KeyID)
		req.Header.Set("KALSHI-ACCESS-TIMESTAMP", strconv.FormatInt(signed.TimestampMs, 10))
		req.Header.Set("KALSHI-ACCESS-SIGNATURE", signed.SignatureBase64)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, errors.New("Kalshi transport unavailable")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return resp.StatusCode, nil, errors.New("Kalshi response unreadable or oversized")
	}
	return resp.StatusCode, raw, nil
}

func parseKalshiError(raw []byte) (code, msg string) {
	var doc struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &doc)
	code = doc.Error.Code
	if code == "" {
		code = doc.Code
	}
	msg = doc.Error.Message
	if msg == "" {
		msg = doc.Message
	}
	return code, msg
}

// FetchMarkets calls public GET /trade-api/v2/markets?status=open&limit=...
func (c *Client) FetchMarkets(ctx context.Context, limit int) ([]MarketSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	status, raw, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/trade-api/v2/markets?status=open&limit=%d", limit), nil, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Kalshi /markets status %d", status)
	}
	var doc struct {
		Markets []MarketSummary `json:"markets"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errors.New("malformed Kalshi /markets response")
	}
	return doc.Markets, nil
}

// FetchOrderBook calls public GET /trade-api/v2/markets/{ticker}/orderbook.
func (c *Client) FetchOrderBook(ctx context.Context, ticker string) (*OrderBook, error) {
	t := strings.ToUpper(strings.TrimSpace(ticker))
	if !validTicker(t) {
		return nil, errors.New("invalid Kalshi ticker")
	}
	status, raw, err := c.doRequest(ctx, http.MethodGet, "/trade-api/v2/markets/"+url.PathEscape(t)+"/orderbook", nil, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Kalshi /orderbook status %d", status)
	}
	var doc struct {
		Orderbook *struct {
			Yes [][]any `json:"yes"`
			No  [][]any `json:"no"`
		} `json:"orderbook"`
		OrderbookFP *struct {
			YesDollars [][]any `json:"yes_dollars"`
			NoDollars  [][]any `json:"no_dollars"`
		} `json:"orderbook_fp"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, errors.New("malformed Kalshi /orderbook response")
	}
	out := &OrderBook{Ticker: t}
	if doc.OrderbookFP != nil {
		out.YesCents = len(doc.OrderbookFP.YesDollars)
		out.NoCents = len(doc.OrderbookFP.NoDollars)
		if len(doc.OrderbookFP.YesDollars) > 0 && len(doc.OrderbookFP.YesDollars[0]) > 0 {
			out.BestYesBid = fmt.Sprint(doc.OrderbookFP.YesDollars[0][0])
		}
		if len(doc.OrderbookFP.NoDollars) > 0 && len(doc.OrderbookFP.NoDollars[0]) > 0 {
			out.BestNoBid = fmt.Sprint(doc.OrderbookFP.NoDollars[0][0])
		}
	} else if doc.Orderbook != nil {
		out.YesCents = len(doc.Orderbook.Yes)
		out.NoCents = len(doc.Orderbook.No)
		if len(doc.Orderbook.Yes) > 0 && len(doc.Orderbook.Yes[0]) > 0 {
			out.BestYesBid = fmt.Sprint(doc.Orderbook.Yes[0][0])
		}
		if len(doc.Orderbook.No) > 0 && len(doc.Orderbook.No[0]) > 0 {
			out.BestNoBid = fmt.Sprint(doc.Orderbook.No[0][0])
		}
	}
	return out, nil
}

// FetchBalance queries GET /trade-api/v2/portfolio/balance using signer RSA-PSS authentication.
func (c *Client) FetchBalance(ctx context.Context, _ string, signFn RequestSigner) (BalanceResponse, error) {
	if err := c.canMutate(); err != nil {
		return BalanceResponse{}, err
	}
	if signFn == nil {
		return BalanceResponse{}, errors.New("kalshi request signer required")
	}
	const path = "/trade-api/v2/portfolio/balance"
	opSuffix, err := randomHexSuffix(8)
	if err != nil {
		return BalanceResponse{}, err
	}
	signed, err := signFn(ctx, signer.KalshiSignIntent{
		Environment: "demo",
		Method:      http.MethodGet,
		Path:        path,
		TimestampMs: time.Now().UnixMilli(),
		OperationID: "kalshi-bal-" + opSuffix,
		ExpiresAt:   time.Now().Unix() + 120,
	})
	if err != nil {
		return BalanceResponse{}, err
	}
	status, raw, err := c.doRequest(ctx, http.MethodGet, path, signed, nil)
	if err != nil {
		return BalanceResponse{}, err
	}
	if status >= 500 {
		return BalanceResponse{RawStatus: status}, fmt.Errorf("Kalshi /portfolio/balance status %d", status)
	}
	if status != http.StatusOK {
		code, msg := parseKalshiError(raw)
		if msg == "" {
			msg = code
		}
		return BalanceResponse{RawStatus: status, Message: msg}, nil
	}
	var doc BalanceResponse
	if err := json.Unmarshal(raw, &doc); err != nil {
		return BalanceResponse{RawStatus: status}, errors.New("malformed Kalshi balance response")
	}
	doc.RawStatus = status
	return doc, nil
}

// SubmitOrder validates an order, requests signer RSA-PSS authorization under AgentVaultPolicy,
// and posts to POST /trade-api/v2/portfolio/events/orders.
func (c *Client) SubmitOrder(ctx context.Context, draft OrderDraft, signFn RequestSigner) (OrderSubmissionResult, error) {
	if err := c.canMutate(); err != nil {
		return OrderSubmissionResult{}, err
	}
	if signFn == nil {
		return OrderSubmissionResult{}, errors.New("kalshi request signer required")
	}
	ord, err := BuildOrder(draft)
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	const path = "/trade-api/v2/portfolio/events/orders"
	signed, err := signFn(ctx, signer.KalshiSignIntent{
		Environment:       "demo",
		Method:            http.MethodPost,
		Path:              path,
		TimestampMs:       time.Now().UnixMilli(),
		Ticker:            ord.Draft.Ticker,
		Asset:             ord.Draft.Asset,
		OperationID:       ord.Draft.OperationID,
		EstimatedMicroUSD: ord.EstimatedMicroUSD,
		ReduceOnly:        ord.Draft.ReduceOnly,
		ExpiresAt:         time.Now().Unix() + 120,
	})
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	status, raw, err := c.doRequest(ctx, http.MethodPost, path, signed, ord.PayloadJSON)
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	if status >= 500 {
		return OrderSubmissionResult{StatusCode: status}, fmt.Errorf("Kalshi /portfolio/events/orders status %d", status)
	}
	if status < 200 || status >= 300 {
		code, msg := parseKalshiError(raw)
		return OrderSubmissionResult{
			Submitted:     false,
			StatusCode:    status,
			ClientOrderID: ord.Draft.ClientOrderID,
			UpstreamCode:  code,
			Message:       msg,
		}, nil
	}
	var doc struct {
		Order struct {
			OrderID       string `json:"order_id"`
			ClientOrderID string `json:"client_order_id"`
			Status        string `json:"status"`
		} `json:"order"`
	}
	_ = json.Unmarshal(raw, &doc)
	return OrderSubmissionResult{
		Submitted:     true,
		StatusCode:    status,
		OrderID:       doc.Order.OrderID,
		ClientOrderID: doc.Order.ClientOrderID,
		Status:        doc.Order.Status,
	}, nil
}

// CancelOrder signs ONLY the path without query parameters and sends
// DELETE /trade-api/v2/portfolio/events/orders/{orderID}[?market_ticker=...].
func (c *Client) CancelOrder(ctx context.Context, orderID, marketTicker string, signFn RequestSigner) (OrderSubmissionResult, error) {
	if err := c.canMutate(); err != nil {
		return OrderSubmissionResult{}, err
	}
	if signFn == nil {
		return OrderSubmissionResult{}, errors.New("kalshi request signer required")
	}
	id := strings.TrimSpace(orderID)
	if id == "" || strings.ContainsAny(id, "/?# ") {
		return OrderSubmissionResult{}, errors.New("invalid kalshi orderId")
	}
	basePath := "/trade-api/v2/portfolio/events/orders/" + id
	opSuffix, err := randomHexSuffix(8)
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	signed, err := signFn(ctx, signer.KalshiSignIntent{
		Environment: "demo",
		Method:      http.MethodDelete,
		Path:        basePath, // CRITICAL: query string stripped per Kalshi RSA-PSS spec
		TimestampMs: time.Now().UnixMilli(),
		Ticker:      strings.ToUpper(strings.TrimSpace(marketTicker)),
		OperationID: "kalshi-can-" + opSuffix,
		ExpiresAt:   time.Now().Unix() + 120,
	})
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	wirePath := basePath
	if t := strings.ToUpper(strings.TrimSpace(marketTicker)); t != "" {
		wirePath += "?market_ticker=" + url.QueryEscape(t)
	}
	status, raw, err := c.doRequest(ctx, http.MethodDelete, wirePath, signed, nil)
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	if status >= 500 {
		return OrderSubmissionResult{StatusCode: status}, fmt.Errorf("Kalshi DELETE order status %d", status)
	}
	if status < 200 || status >= 300 {
		code, msg := parseKalshiError(raw)
		return OrderSubmissionResult{
			Submitted:    false,
			StatusCode:   status,
			OrderID:      id,
			UpstreamCode: code,
			Message:      msg,
		}, nil
	}
	return OrderSubmissionResult{
		Submitted:  true,
		StatusCode: status,
		OrderID:    id,
		Status:     "cancelled",
	}, nil
}
