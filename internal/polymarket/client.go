package polymarket

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
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

// Polymarket paginated market directories (/markets, /sampling-markets) return up to ~2.5 MiB.
const maxResponseBytes = 4 * 1024 * 1024

// Client is a bounded SSRF-safe Polymarket CLOB REST adapter.
type Client struct {
	network    string
	origin     string
	http       *http.Client
	readOnly   bool
	controlled bool
}

func isPublicUnicastIP(ip netip.Addr) bool {
	return netguard.IsPublicUnicastIP(ip)
}

func interleavePublicIPs(ips []netip.Addr) ([]netip.Addr, error) {
	if len(ips) == 0 {
		return nil, errors.New("Polymarket API address unavailable")
	}
	var v4, v6 []netip.Addr
	for _, ip := range ips {
		if !isPublicUnicastIP(ip) {
			return nil, errors.New("private or loopback Polymarket address denied")
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
		return nil, errors.New("Polymarket API address unavailable")
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
	return nil, errors.New("Polymarket API address unavailable")
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
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Polymarket redirects denied") },
	}
}

// NewReadOnlyClient creates a strict read-only Polymarket CLOB client pinned to MainnetCLOBOrigin.
func NewReadOnlyClient() *Client {
	return &Client{
		network:  "mainnet",
		origin:   MainnetCLOBOrigin,
		http:     newHardenedHTTPClient(),
		readOnly: true,
	}
}

// NewTestnetExecutionClient creates a Stage 4 Amoy testnet client that permits public
// CLOB market/book reads while failing closed on live MainnetCLOBOrigin mutations.
func NewTestnetExecutionClient() *Client {
	return &Client{
		network:    "amoy",
		origin:     MainnetCLOBOrigin,
		http:       newHardenedHTTPClient(),
		readOnly:   false,
		controlled: false,
	}
}

// NewControlledClient attaches an isolated test RoundTripper for Polymarket CLOB simulation.
func NewControlledClient(network string, rt ControlledTransport) (*Client, error) {
	if rt == nil || !rt.ControlledFixture() {
		return nil, errors.New("controlled Polymarket transport required")
	}
	netName := strings.ToLower(strings.TrimSpace(network))
	if netName != "amoy" && netName != "mainnet" {
		return nil, errors.New("Polymarket network must be amoy or mainnet")
	}
	return &Client{
		network: netName,
		origin:  MainnetCLOBOrigin,
		http: &http.Client{
			Transport:     rt,
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Polymarket redirects denied") },
		},
		controlled: true,
	}, nil
}

func (c *Client) Network() string { return c.network }
func (c *Client) Origin() string  { return c.origin }

func parseFixedDecimals(raw string, maxDecimals int) (uint64, error) {
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
		if len(fracPart) > maxDecimals {
			return 0, fmt.Errorf("decimal precision exceeds %d places", maxDecimals)
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
	paddedFrac := fracPart + strings.Repeat("0", maxDecimals-len(fracPart))
	combined := intPart + paddedFrac
	v, err := strconv.ParseUint(combined, 10, 64)
	if err != nil {
		return 0, errors.New("decimal value overflows uint64")
	}
	return v, nil
}

func randomSaltDecimal() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// Keep within 48 bits to stay JS-safe across REST/JSON bridges.
	val := binary.BigEndian.Uint64(b[:]) & ((uint64(1) << 48) - 1)
	if val == 0 {
		val = 1
	}
	return strconv.FormatUint(val, 10), nil
}

func randomOperationID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "pm-ord-" + hex.EncodeToString(b[:]), nil
}

// BuildOrderIntent validates an operator order draft and encodes exact 6-decimal
// CTF Exchange makerAmount/takerAmount integers for EIP-712 signing.
func BuildOrderIntent(draft OrderDraft, now int64) (signer.PolymarketOrderIntent, error) {
	netName := strings.ToLower(strings.TrimSpace(draft.Network))
	if netName == "" {
		netName = "amoy"
	}
	chainID := signer.PolymarketAmoyChainID
	if netName == "mainnet" || netName == "polygon" {
		chainID = signer.PolymarketMainnetChainID
	} else if netName != "amoy" {
		return signer.PolymarketOrderIntent{}, errors.New("polymarket order network must be amoy or mainnet")
	}

	tokenID := strings.TrimSpace(draft.TokenID)
	if tokenID == "" || len(tokenID) > 80 {
		return signer.PolymarketOrderIntent{}, errors.New("invalid polymarket tokenId")
	}
	for i := 0; i < len(tokenID); i++ {
		if tokenID[i] < '0' || tokenID[i] > '9' {
			return signer.PolymarketOrderIntent{}, errors.New("polymarket tokenId must be unsigned integer string")
		}
	}

	sideStr := strings.ToUpper(strings.TrimSpace(draft.Side))
	var side uint8
	switch sideStr {
	case "BUY":
		side = 0
	case "SELL":
		side = 1
	default:
		return signer.PolymarketOrderIntent{}, errors.New("polymarket side must be BUY or SELL")
	}

	orderType := strings.ToUpper(strings.TrimSpace(draft.OrderType))
	if orderType == "" {
		orderType = "GTC"
	}
	switch orderType {
	case "GTC", "GTD", "FOK", "FAK":
	default:
		return signer.PolymarketOrderIntent{}, errors.New("unsupported polymarket orderType")
	}

	priceMicros, err := parseFixedDecimals(draft.Price, 6)
	if err != nil {
		return signer.PolymarketOrderIntent{}, fmt.Errorf("invalid price: %w", err)
	}
	if priceMicros < 1_000 || priceMicros > 999_000 {
		return signer.PolymarketOrderIntent{}, errors.New("polymarket price must be strictly between 0.001 and 0.999")
	}
	sharesMicros, err := parseFixedDecimals(draft.Shares, 6)
	if err != nil {
		return signer.PolymarketOrderIntent{}, fmt.Errorf("invalid shares: %w", err)
	}
	if sharesMicros == 0 {
		return signer.PolymarketOrderIntent{}, errors.New("polymarket shares must be positive")
	}

	usdBig := new(big.Int).Mul(new(big.Int).SetUint64(sharesMicros), new(big.Int).SetUint64(priceMicros))
	usdBig.Div(usdBig, big.NewInt(1_000_000))
	if !usdBig.IsUint64() || usdBig.Sign() <= 0 {
		return signer.PolymarketOrderIntent{}, errors.New("polymarket order notional out of bounds")
	}
	usdMicros := usdBig.Uint64()

	var makerAmt, takerAmt uint64
	if side == 0 {
		makerAmt = usdMicros
		takerAmt = sharesMicros
		if makerAmt >= takerAmt {
			return signer.PolymarketOrderIntent{}, errors.New("invalid polymarket BUY amount ratio")
		}
	} else {
		makerAmt = sharesMicros
		takerAmt = usdMicros
		if takerAmt >= makerAmt {
			return signer.PolymarketOrderIntent{}, errors.New("invalid polymarket SELL amount ratio")
		}
	}

	asset := strings.ToUpper(strings.TrimSpace(draft.Asset))
	if asset == "" {
		asset = "USDC"
	}
	salt := strings.TrimSpace(draft.Salt)
	if salt == "" {
		salt, err = randomSaltDecimal()
		if err != nil {
			return signer.PolymarketOrderIntent{}, err
		}
	}
	opID := strings.TrimSpace(draft.OperationID)
	if opID == "" {
		opID, err = randomOperationID()
		if err != nil {
			return signer.PolymarketOrderIntent{}, err
		}
	}
	exp := draft.Expiration
	if orderType == "GTD" && exp == 0 {
		exp = uint64(now + 3600)
	}

	return signer.PolymarketOrderIntent{
		Network:         netName,
		ChainID:         chainID,
		NegRisk:         draft.NegRisk,
		OperationID:     opID,
		Asset:           asset,
		Salt:            salt,
		TokenID:         tokenID,
		MakerAmount:     strconv.FormatUint(makerAmt, 10),
		TakerAmount:     strconv.FormatUint(takerAmt, 10),
		Expiration:      exp,
		Nonce:           draft.Nonce,
		FeeRateBps:      draft.FeeRateBps,
		Side:            side,
		SignatureType:   0,
		ReduceOnly:      draft.ReduceOnly,
		IntentExpiresAt: now + 300,
	}, nil
}

// BuildL2AuthHeaders builds Polymarket CLOB L2 HMAC-SHA256 authentication headers.
func BuildL2AuthHeaders(creds L2Credentials, timestampSec int64, method, requestPath, body string) (map[string]string, error) {
	canonAddr, err := signer.ValidateEthereumAddress(strings.TrimSpace(creds.Address))
	if err != nil {
		return nil, fmt.Errorf("invalid L2 credentials address: %w", err)
	}
	if strings.TrimSpace(creds.APIKey) == "" || strings.TrimSpace(creds.Secret) == "" || strings.TrimSpace(creds.Passphrase) == "" {
		return nil, errors.New("incomplete Polymarket L2 credentials")
	}
	if timestampSec <= 0 {
		return nil, errors.New("positive timestamp required for Polymarket L2 signature")
	}

	normalizedSecret := strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimSpace(creds.Secret))
	if rem := len(normalizedSecret) % 4; rem != 0 {
		normalizedSecret += strings.Repeat("=", 4-rem)
	}
	secretBytes, err := base64.StdEncoding.DecodeString(normalizedSecret)
	if err != nil || len(secretBytes) == 0 {
		return nil, errors.New("invalid base64 Polymarket L2 secret")
	}
	defer clear(secretBytes)

	tsStr := strconv.FormatInt(timestampSec, 10)
	msg := tsStr + strings.ToUpper(strings.TrimSpace(method)) + requestPath + body
	mac := hmac.New(sha256.New, secretBytes)
	_, _ = mac.Write([]byte(msg))
	sigBytes := mac.Sum(nil)
	defer clear(sigBytes)

	return map[string]string{
		"POLY_ADDRESS":    canonAddr,
		"POLY_API_KEY":    strings.TrimSpace(creds.APIKey),
		"POLY_PASSPHRASE": strings.TrimSpace(creds.Passphrase),
		"POLY_TIMESTAMP":  tsStr,
		"POLY_SIGNATURE":  base64.URLEncoding.EncodeToString(sigBytes),
	}, nil
}

func (c *Client) doRequest(ctx context.Context, method, pathAndQuery string, headers map[string]string, rawBody []byte) (int, []byte, error) {
	if c == nil || c.http == nil {
		return 0, nil, errors.New("Polymarket client unavailable")
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
	if len(rawBody) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, errors.New("Polymarket transport unavailable")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return resp.StatusCode, nil, errors.New("Polymarket response unreadable or oversized")
	}
	return resp.StatusCode, raw, nil
}

func (c *Client) fetchMarketList(ctx context.Context, pathAndQuery string, limit int) ([]MarketSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	status, raw, err := c.doRequest(ctx, http.MethodGet, pathAndQuery, nil, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Polymarket %s status %d", pathAndQuery, status)
	}
	var items []MarketSummary
	var wrapped struct {
		Data []MarketSummary `json:"data"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Data != nil {
		items = wrapped.Data
	} else if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("malformed Polymarket %s response: %w", pathAndQuery, err)
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// FetchMarkets calls public GET /markets and returns bounded market metadata.
func (c *Client) FetchMarkets(ctx context.Context, limit int) ([]MarketSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return c.fetchMarketList(ctx, fmt.Sprintf("/markets?limit=%d", limit), limit)
}

// FetchActiveMarkets calls public GET /sampling-markets for live order-book-enabled markets.
func (c *Client) FetchActiveMarkets(ctx context.Context, limit int) ([]MarketSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return c.fetchMarketList(ctx, "/sampling-markets", limit)
}

// FetchOrderBook calls public GET /book?token_id=... for a conditional token.
func (c *Client) FetchOrderBook(ctx context.Context, tokenID string) (*OrderBook, error) {
	tid := strings.TrimSpace(tokenID)
	if tid == "" {
		return nil, errors.New("polymarket token_id required")
	}
	status, raw, err := c.doRequest(ctx, http.MethodGet, "/book?token_id="+url.QueryEscape(tid), nil, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("Polymarket /book status %d", status)
	}
	var book OrderBook
	if err := json.Unmarshal(raw, &book); err != nil {
		return nil, errors.New("malformed Polymarket /book response")
	}
	return &book, nil
}

// FetchTickSize calls public GET /tick-size?token_id=...
func (c *Client) FetchTickSize(ctx context.Context, tokenID string) (string, error) {
	tid := strings.TrimSpace(tokenID)
	if tid == "" {
		return "", errors.New("polymarket token_id required")
	}
	status, raw, err := c.doRequest(ctx, http.MethodGet, "/tick-size?token_id="+url.QueryEscape(tid), nil, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("Polymarket /tick-size status %d", status)
	}
	var doc struct {
		MinimumTickSize any `json:"minimum_tick_size"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", errors.New("malformed Polymarket /tick-size response")
	}
	return fmt.Sprint(doc.MinimumTickSize), nil
}

func (c *Client) canSubmit(intent signer.PolymarketOrderIntent) error {
	if c == nil || c.readOnly || !c.controlled {
		return ErrExecutionDisabled
	}
	if intent.Network == "mainnet" || intent.Network == "polygon" || intent.ChainID == signer.PolymarketMainnetChainID || c.network == "mainnet" {
		if !signer.PolymarketActivationEnabled {
			return ErrExecutionDisabled
		}
	}
	if intent.Network != "amoy" || intent.ChainID != signer.PolymarketAmoyChainID {
		return ErrExecutionDisabled
	}
	return nil
}

// SubmitSignedOrder posts a signer-authorized Amoy EIP-712 order with L2 HMAC headers.
// Fails closed with ErrExecutionDisabled on live MainnetCLOBOrigin while locked.
func (c *Client) SubmitSignedOrder(ctx context.Context, intent signer.PolymarketOrderIntent, signed *signer.PolymarketSignedOrder, orderType string, creds L2Credentials) (OrderSubmissionResult, error) {
	if err := c.canSubmit(intent); err != nil {
		return OrderSubmissionResult{}, err
	}
	if signed == nil || signed.SignatureHex == "" {
		return OrderSubmissionResult{}, errors.New("signed Polymarket order required")
	}
	ot := strings.ToUpper(strings.TrimSpace(orderType))
	if ot == "" {
		ot = "GTC"
	}
	saltNum, err := strconv.ParseUint(intent.Salt, 10, 64)
	if err != nil {
		return OrderSubmissionResult{}, fmt.Errorf("order salt for wire payload: %w", err)
	}
	sideLabel := "BUY"
	if intent.Side == 1 {
		sideLabel = "SELL"
	}
	wire := map[string]any{
		"order": map[string]any{
			"salt":          saltNum,
			"maker":         signed.MakerAddress,
			"signer":        signed.SignerAddress,
			"taker":         signed.TakerAddress,
			"tokenId":       intent.TokenID,
			"makerAmount":   intent.MakerAmount,
			"takerAmount":   intent.TakerAmount,
			"expiration":    strconv.FormatUint(intent.Expiration, 10),
			"nonce":         strconv.FormatUint(intent.Nonce, 10),
			"feeRateBps":    strconv.FormatUint(intent.FeeRateBps, 10),
			"side":          sideLabel,
			"signatureType": intent.SignatureType,
			"signature":     signed.SignatureHex,
		},
		"owner":     strings.TrimSpace(creds.APIKey),
		"orderType": ot,
	}
	bodyBytes, err := json.Marshal(wire)
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	headers, err := BuildL2AuthHeaders(creds, time.Now().Unix(), http.MethodPost, "/order", string(bodyBytes))
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	status, raw, err := c.doRequest(ctx, http.MethodPost, "/order", headers, bodyBytes)
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	if status >= 500 {
		return OrderSubmissionResult{StatusCode: status}, fmt.Errorf("Polymarket /order upstream error status %d", status)
	}
	var parsed struct {
		Success     bool     `json:"success"`
		ErrorMsg    string   `json:"errorMsg"`
		Error       string   `json:"error"`
		OrderID     string   `json:"orderID"`
		Status      string   `json:"status"`
		OrderHashes []string `json:"transactionsHashes"`
	}
	_ = json.Unmarshal(raw, &parsed)
	msg := parsed.ErrorMsg
	if msg == "" {
		msg = parsed.Error
	}
	return OrderSubmissionResult{
		Submitted:   status >= 200 && status < 300 && parsed.Success,
		StatusCode:  status,
		OrderID:     parsed.OrderID,
		Status:      parsed.Status,
		Message:     msg,
		OrderHashes: parsed.OrderHashes,
	}, nil
}

// CancelOrder cancels a single Polymarket order on a controlled testnet transport.
func (c *Client) CancelOrder(ctx context.Context, orderID string, creds L2Credentials) (OrderSubmissionResult, error) {
	if c == nil || c.readOnly || !c.controlled || (c.network == "mainnet" && !signer.PolymarketActivationEnabled) {
		return OrderSubmissionResult{}, ErrExecutionDisabled
	}
	id := strings.TrimSpace(orderID)
	if id == "" {
		return OrderSubmissionResult{}, errors.New("orderId required")
	}
	bodyBytes, err := json.Marshal(map[string]string{"orderID": id})
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	headers, err := BuildL2AuthHeaders(creds, time.Now().Unix(), http.MethodDelete, "/order", string(bodyBytes))
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	status, raw, err := c.doRequest(ctx, http.MethodDelete, "/order", headers, bodyBytes)
	if err != nil {
		return OrderSubmissionResult{}, err
	}
	if status >= 500 {
		return OrderSubmissionResult{StatusCode: status}, fmt.Errorf("Polymarket DELETE /order status %d", status)
	}
	var parsed struct {
		Canceled    []string `json:"canceled"`
		NotCanceled any      `json:"not_canceled"`
		Error       string   `json:"error"`
	}
	_ = json.Unmarshal(raw, &parsed)
	return OrderSubmissionResult{
		Submitted:  status >= 200 && status < 300 && len(parsed.Canceled) > 0,
		StatusCode: status,
		OrderID:    id,
		Message:    parsed.Error,
	}, nil
}
