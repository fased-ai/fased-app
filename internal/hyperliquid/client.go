package hyperliquid

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

const maxResponseBytes = 1024 * 1024

// ControlledTransport is a Go-test-only HTTP RoundTripper for isolated Hyperliquid
// simulation. Ordinary production clients always use NewClient.
type ControlledTransport interface {
	http.RoundTripper
	ControlledFixture() bool
}

// Client is a bounded Hyperliquid Exchange & Info REST adapter.
type Client struct {
	network    string
	origin     string
	http       *http.Client
	controlled bool
}

// ValidateOrigin requires an exact match to the pinned Hyperliquid mainnet or testnet origin.
func ValidateOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("invalid Hyperliquid API origin")
	}
	canon := "https://" + u.Host
	if canon != MainnetAPIOrigin && canon != TestnetAPIOrigin {
		return "", errors.New("untrusted Hyperliquid API origin")
	}
	return canon, nil
}

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

func isPublicUnicastIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified() &&
		!cgnatPrefix.Contains(ip)
}

func interleavePublicIPs(ips []netip.Addr) ([]netip.Addr, error) {
	if len(ips) == 0 {
		return nil, errors.New("Hyperliquid API address unavailable")
	}
	var v4, v6 []netip.Addr
	for _, ip := range ips {
		if !isPublicUnicastIP(ip) {
			return nil, errors.New("private or loopback Hyperliquid address denied")
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
		return nil, errors.New("Hyperliquid API address unavailable")
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
	return nil, errors.New("Hyperliquid API address unavailable")
}

func originForNetwork(network string) (string, error) {
	switch network {
	case "mainnet":
		return MainnetAPIOrigin, nil
	case "testnet":
		return TestnetAPIOrigin, nil
	default:
		return "", errors.New("Hyperliquid network must be mainnet or testnet")
	}
}

// NewClient constructs a strict SSRF-safe read/preview client pinned to official
// Hyperliquid origins with TLS 1.3 and redirects disabled.
func NewClient(network string) (*Client, error) {
	origin, err := originForNetwork(network)
	if err != nil {
		return nil, err
	}
	t := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:       tls.VersionTLS13,
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
		network: network,
		origin:  origin,
		http: &http.Client{
			Transport:     t,
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Hyperliquid redirects denied") },
		},
		controlled: false,
	}, nil
}

// NewControlledClient attaches an isolated test transport for controlled Hyperliquid verification.
func NewControlledClient(network string, rt ControlledTransport) (*Client, error) {
	if rt == nil || !rt.ControlledFixture() {
		return nil, errors.New("controlled Hyperliquid transport required")
	}
	origin, err := originForNetwork(network)
	if err != nil {
		return nil, err
	}
	return &Client{
		network: network,
		origin:  origin,
		http: &http.Client{
			Transport:     rt,
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Hyperliquid redirects denied") },
		},
		controlled: true,
	}, nil
}

func (c *Client) Network() string { return c.network }
func (c *Client) Origin() string  { return c.origin }
func (c *Client) Controlled() bool {
	return c != nil && c.controlled
}

func (c *Client) postJSON(ctx context.Context, path string, reqBody any) ([]byte, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("Hyperliquid client unavailable")
	}
	if path != "/info" && path != "/exchange" {
		return nil, errors.New("unsupported Hyperliquid API path")
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("Hyperliquid transport unavailable")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || resp.StatusCode != http.StatusOK || len(raw) > maxResponseBytes {
		return nil, errors.New("Hyperliquid rejected or oversized response")
	}
	return raw, nil
}

// FetchMarkets calls POST /info {"type":"metaAndAssetCtxs"} and normalizes perpetual metadata.
func (c *Client) FetchMarkets(ctx context.Context) (MarketSnapshot, error) {
	raw, err := c.postJSON(ctx, "/info", map[string]any{"type": "metaAndAssetCtxs"})
	if err != nil {
		return MarketSnapshot{}, err
	}
	var tuple []json.RawMessage
	if err := json.Unmarshal(raw, &tuple); err != nil || len(tuple) != 2 {
		return MarketSnapshot{}, errors.New("malformed metaAndAssetCtxs response")
	}
	var meta struct {
		Universe []struct {
			Name         string `json:"name"`
			SzDecimals   int    `json:"szDecimals"`
			MaxLeverage  uint32 `json:"maxLeverage"`
			OnlyIsolated bool   `json:"onlyIsolated"`
		} `json:"universe"`
	}
	if err := json.Unmarshal(tuple[0], &meta); err != nil || len(meta.Universe) == 0 {
		return MarketSnapshot{}, errors.New("malformed Hyperliquid universe metadata")
	}
	var ctxs []struct {
		Funding      string `json:"funding"`
		OpenInterest string `json:"openInterest"`
		PrevDayPx    string `json:"prevDayPx"`
		DayNtlVlm    string `json:"dayNtlVlm"`
		OraclePx     string `json:"oraclePx"`
		MarkPx       string `json:"markPx"`
		MidPx        string `json:"midPx"`
	}
	if err := json.Unmarshal(tuple[1], &ctxs); err != nil || len(ctxs) != len(meta.Universe) {
		return MarketSnapshot{}, errors.New("mismatched Hyperliquid asset contexts")
	}
	markets := make([]MarketMeta, 0, len(meta.Universe))
	for i, u := range meta.Universe {
		ac := ctxs[i]
		if u.Name == "" || ac.MarkPx == "" {
			continue
		}
		markets = append(markets, MarketMeta{
			AssetIndex:   uint32(i),
			Symbol:       u.Name,
			SzDecimals:   u.SzDecimals,
			MaxLeverage:  u.MaxLeverage,
			OnlyIsolated: u.OnlyIsolated,
			MarkPrice:    ac.MarkPx,
			OraclePrice:  ac.OraclePx,
			MidPrice:     ac.MidPx,
			PrevDayPrice: ac.PrevDayPx,
			DayNotional:  ac.DayNtlVlm,
			FundingRate:  ac.Funding,
			OpenInterest: ac.OpenInterest,
		})
	}
	if len(markets) == 0 {
		return MarketSnapshot{}, errors.New("no active Hyperliquid markets returned")
	}
	return MarketSnapshot{
		Network: c.network,
		Markets: markets,
	}, nil
}

// FetchAccountState retrieves clearinghouseState and openOrders for a valid EIP-55 address.
func (c *Client) FetchAccountState(ctx context.Context, address string) (AccountState, error) {
	canon, err := signer.ValidateEthereumAddress(address)
	if err != nil {
		return AccountState{}, err
	}
	rawState, err := c.postJSON(ctx, "/info", map[string]any{
		"type": "clearinghouseState",
		"user": canon,
	})
	if err != nil {
		return AccountState{}, err
	}
	var stateWire struct {
		MarginSummary struct {
			AccountValue    string `json:"accountValue"`
			TotalMarginUsed string `json:"totalMarginUsed"`
		} `json:"marginSummary"`
		Withdrawable   string `json:"withdrawable"`
		AssetPositions []struct {
			Position struct {
				Coin          string `json:"coin"`
				Szi           string `json:"szi"`
				EntryPx       string `json:"entryPx"`
				PositionValue string `json:"positionValue"`
				UnrealizedPnl string `json:"unrealizedPnl"`
				LiquidationPx string `json:"liquidationPx"`
				Leverage      struct {
					Type  string `json:"type"`
					Value uint32 `json:"value"`
				} `json:"leverage"`
			} `json:"position"`
		} `json:"assetPositions"`
	}
	if err := json.Unmarshal(rawState, &stateWire); err != nil {
		return AccountState{}, errors.New("malformed clearinghouseState response")
	}
	rawOrders, err := c.postJSON(ctx, "/info", map[string]any{
		"type": "openOrders",
		"user": canon,
	})
	if err != nil {
		return AccountState{}, err
	}
	var ordersWire []struct {
		Coin      string `json:"coin"`
		Side      string `json:"side"`
		LimitPx   string `json:"limitPx"`
		Sz        string `json:"sz"`
		Oid       uint64 `json:"oid"`
		Timestamp int64  `json:"timestamp"`
		Cloid     string `json:"cloid"`
	}
	if err := json.Unmarshal(rawOrders, &ordersWire); err != nil {
		return AccountState{}, errors.New("malformed openOrders response")
	}
	positions := make([]Position, 0, len(stateWire.AssetPositions))
	for _, ap := range stateWire.AssetPositions {
		p := ap.Position
		if p.Coin == "" || p.Szi == "" || p.Szi == "0" {
			continue
		}
		positions = append(positions, Position{
			Symbol:           p.Coin,
			Size:             p.Szi,
			EntryPrice:       p.EntryPx,
			PositionValue:    p.PositionValue,
			UnrealizedPnL:    p.UnrealizedPnl,
			LiquidationPrice: p.LiquidationPx,
			Leverage:         p.Leverage.Value,
			MarginMode:       p.Leverage.Type,
		})
	}
	openOrders := make([]OpenOrder, 0, len(ordersWire))
	for _, o := range ordersWire {
		openOrders = append(openOrders, OpenOrder{
			Symbol:        o.Coin,
			Side:          o.Side,
			LimitPrice:    o.LimitPx,
			Size:          o.Sz,
			OrderID:       o.Oid,
			Timestamp:     o.Timestamp,
			ClientOrderID: o.Cloid,
		})
	}
	return AccountState{
		Address:      canon,
		AccountValue: stateWire.MarginSummary.AccountValue,
		TotalMargin:  stateWire.MarginSummary.TotalMarginUsed,
		Withdrawable: stateWire.Withdrawable,
		Positions:    positions,
		OpenOrders:   openOrders,
	}, nil
}

// BuildOrderWirePayload constructs the JSON body for POST /exchange limit orders.
func BuildOrderWirePayload(intent signer.HyperliquidOrderIntent, signed *signer.HyperliquidSignedAction, vaultAddress string) (map[string]any, error) {
	if signed == nil {
		return nil, errors.New("signed EIP-712 action required")
	}
	px, err := signer.NormalizeHyperliquidDecimal(intent.LimitPrice)
	if err != nil {
		return nil, err
	}
	sz, err := signer.NormalizeHyperliquidDecimal(intent.Size)
	if err != nil {
		return nil, err
	}
	orderObj := map[string]any{
		"a": intent.AssetIndex,
		"b": intent.IsBuy,
		"p": px,
		"s": sz,
		"r": intent.ReduceOnly,
		"t": map[string]any{
			"limit": map[string]any{"tif": intent.TimeInForce},
		},
	}
	if intent.ClientOrderID != "" {
		orderObj["c"] = strings.ToLower(intent.ClientOrderID)
	}
	grouping := intent.Grouping
	if grouping == "" {
		grouping = "na"
	}
	body := map[string]any{
		"action": map[string]any{
			"type":     "order",
			"orders":   []any{orderObj},
			"grouping": grouping,
		},
		"nonce": signed.Nonce,
		"signature": map[string]any{
			"r": signed.SignatureR,
			"s": signed.SignatureS,
			"v": signed.SignatureV,
		},
	}
	if vaultAddress != "" {
		canonVault, err := signer.ValidateEthereumAddress(vaultAddress)
		if err != nil {
			return nil, err
		}
		body["vaultAddress"] = canonVault
	}
	return body, nil
}

// BuildUpdateLeverageWirePayload constructs the JSON body for POST /exchange updateLeverage.
func BuildUpdateLeverageWirePayload(intent signer.HyperliquidUpdateLeverageIntent, signed *signer.HyperliquidSignedAction, vaultAddress string) (map[string]any, error) {
	if signed == nil || intent.Leverage == 0 || intent.AssetIndex > 10000 {
		return nil, errors.New("signed updateLeverage action, leverage, and valid assetIndex required")
	}
	body := map[string]any{
		"action": map[string]any{
			"type":     "updateLeverage",
			"asset":    intent.AssetIndex,
			"isCross":  intent.IsCross,
			"leverage": intent.Leverage,
		},
		"nonce": signed.Nonce,
		"signature": map[string]any{
			"r": signed.SignatureR,
			"s": signed.SignatureS,
			"v": signed.SignatureV,
		},
	}
	if vaultAddress != "" {
		canonVault, err := signer.ValidateEthereumAddress(vaultAddress)
		if err != nil {
			return nil, err
		}
		body["vaultAddress"] = canonVault
	}
	return body, nil
}

// BuildCancelWirePayload constructs the JSON body for POST /exchange cancel-by-oid.
func BuildCancelWirePayload(intent signer.HyperliquidCancelIntent, signed *signer.HyperliquidSignedAction, vaultAddress string) (map[string]any, error) {
	if signed == nil || intent.OrderID == 0 {
		return nil, errors.New("signed cancel action and orderId required")
	}
	body := map[string]any{
		"action": map[string]any{
			"type": "cancel",
			"cancels": []any{
				map[string]any{
					"a": intent.AssetIndex,
					"o": intent.OrderID,
				},
			},
		},
		"nonce": signed.Nonce,
		"signature": map[string]any{
			"r": signed.SignatureR,
			"s": signed.SignatureS,
			"v": signed.SignatureV,
		},
	}
	if vaultAddress != "" {
		canonVault, err := signer.ValidateEthereumAddress(vaultAddress)
		if err != nil {
			return nil, err
		}
		body["vaultAddress"] = canonVault
	}
	return body, nil
}

func parseExchangeStatuses(raw []byte) (ExchangeSubmissionResult, error) {
	var resp struct {
		Status   string `json:"status"`
		Response *struct {
			Type string `json:"type"`
			Data *struct {
				Statuses []struct {
					Resting *struct {
						Oid uint64 `json:"oid"`
					} `json:"resting"`
					Filled *struct {
						Oid     uint64 `json:"oid"`
						TotalSz string `json:"totalSz"`
						AvgPx   string `json:"avgPx"`
					} `json:"filled"`
					Error string `json:"error"`
				} `json:"statuses"`
			} `json:"data"`
		} `json:"response"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ExchangeSubmissionResult{State: "unknown", Message: "malformed exchange response"}, err
	}
	if resp.Status != "ok" {
		return ExchangeSubmissionResult{State: "rejected", Message: fmt.Sprintf("exchange status %q", resp.Status)}, nil
	}
	if resp.Response == nil || resp.Response.Data == nil || len(resp.Response.Data.Statuses) == 0 {
		return ExchangeSubmissionResult{State: "unknown", Message: "missing exchange status entry"}, nil
	}
	st := resp.Response.Data.Statuses[0]
	if st.Error != "" {
		return ExchangeSubmissionResult{State: "rejected", Message: st.Error}, nil
	}
	if st.Filled != nil {
		return ExchangeSubmissionResult{
			State:      "filled",
			OrderID:    st.Filled.Oid,
			FilledSize: st.Filled.TotalSz,
			AvgPrice:   st.Filled.AvgPx,
		}, nil
	}
	if st.Resting != nil {
		return ExchangeSubmissionResult{
			State:   "resting",
			OrderID: st.Resting.Oid,
		}, nil
	}
	return ExchangeSubmissionResult{State: "cancelled"}, nil
}

// SubmitSignedOrder fails closed with ErrExecutionDisabled unless this Client
// holds a ControlledFixture transport AND HyperliquidActivationEnabled is false on mainnet.
func (c *Client) SubmitSignedOrder(ctx context.Context, intent signer.HyperliquidOrderIntent, signed *signer.HyperliquidSignedAction, vaultAddress string) (ExchangeSubmissionResult, error) {
	if c == nil || !c.controlled {
		return ExchangeSubmissionResult{}, ErrExecutionDisabled
	}
	if intent.Network == "mainnet" && !signer.HyperliquidActivationEnabled {
		return ExchangeSubmissionResult{}, ErrExecutionDisabled
	}
	body, err := BuildOrderWirePayload(intent, signed, vaultAddress)
	if err != nil {
		return ExchangeSubmissionResult{}, err
	}
	raw, err := c.postJSON(ctx, "/exchange", body)
	if err != nil {
		return ExchangeSubmissionResult{State: "unknown", Message: err.Error()}, err
	}
	return parseExchangeStatuses(raw)
}

// SubmitSignedCancel fails closed unless this Client holds a ControlledFixture transport.
func (c *Client) SubmitSignedCancel(ctx context.Context, intent signer.HyperliquidCancelIntent, signed *signer.HyperliquidSignedAction, vaultAddress string) (ExchangeSubmissionResult, error) {
	if c == nil || !c.controlled {
		return ExchangeSubmissionResult{}, ErrExecutionDisabled
	}
	if intent.Network == "mainnet" && !signer.HyperliquidActivationEnabled {
		return ExchangeSubmissionResult{}, ErrExecutionDisabled
	}
	body, err := BuildCancelWirePayload(intent, signed, vaultAddress)
	if err != nil {
		return ExchangeSubmissionResult{}, err
	}
	raw, err := c.postJSON(ctx, "/exchange", body)
	if err != nil {
		return ExchangeSubmissionResult{State: "unknown", Message: err.Error()}, err
	}
	res, err := parseExchangeStatuses(raw)
	if err == nil && res.State != "rejected" && res.State != "unknown" {
		res.State = "cancelled"
	}
	return res, err
}

func parseUpdateLeverageResponse(raw []byte) (ExchangeSubmissionResult, error) {
	var resp struct {
		Status   string          `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ExchangeSubmissionResult{State: "unknown", Message: "malformed updateLeverage response"}, err
	}
	if resp.Status != "ok" {
		msg := fmt.Sprintf("updateLeverage status %q", resp.Status)
		var textResp string
		if json.Unmarshal(resp.Response, &textResp) == nil && textResp != "" {
			msg = textResp
		}
		return ExchangeSubmissionResult{State: "rejected", Message: msg}, errors.New(msg)
	}
	var errCheck struct {
		Data *struct {
			Statuses []struct {
				Error string `json:"error"`
			} `json:"statuses"`
		} `json:"data"`
	}
	if json.Unmarshal(resp.Response, &errCheck) == nil && errCheck.Data != nil {
		for _, st := range errCheck.Data.Statuses {
			if st.Error != "" {
				return ExchangeSubmissionResult{State: "rejected", Message: st.Error}, errors.New(st.Error)
			}
		}
	}
	return ExchangeSubmissionResult{State: "ok"}, nil
}

// SubmitSignedUpdateLeverage fails closed unless this Client holds a ControlledFixture transport.
func (c *Client) SubmitSignedUpdateLeverage(ctx context.Context, intent signer.HyperliquidUpdateLeverageIntent, signed *signer.HyperliquidSignedAction, vaultAddress string) (ExchangeSubmissionResult, error) {
	if c == nil || !c.controlled {
		return ExchangeSubmissionResult{}, ErrExecutionDisabled
	}
	if intent.Network == "mainnet" && !signer.HyperliquidActivationEnabled {
		return ExchangeSubmissionResult{}, ErrExecutionDisabled
	}
	body, err := BuildUpdateLeverageWirePayload(intent, signed, vaultAddress)
	if err != nil {
		return ExchangeSubmissionResult{}, err
	}
	raw, err := c.postJSON(ctx, "/exchange", body)
	if err != nil {
		return ExchangeSubmissionResult{State: "unknown", Message: err.Error()}, err
	}
	return parseUpdateLeverageResponse(raw)
}

func parseOrderStatusResponse(raw []byte, fallbackOID uint64, fallbackCloid string) (ExchangeSubmissionResult, error) {
	var resp struct {
		Status string `json:"status"`
		Order  *struct {
			Status string `json:"status"`
			Order  struct {
				Oid   uint64 `json:"oid"`
				Sz    string `json:"sz"`
				Limit string `json:"limitPx"`
				Cloid string `json:"cloid"`
			} `json:"order"`
		} `json:"order"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ExchangeSubmissionResult{State: "unknown", OrderID: fallbackOID, ClientOrderID: fallbackCloid, Message: "malformed orderStatus"}, err
	}
	if resp.Status != "order" || resp.Order == nil {
		return ExchangeSubmissionResult{State: "unknown", OrderID: fallbackOID, ClientOrderID: fallbackCloid, Message: "order not indexed"}, nil
	}
	state := "unknown"
	switch resp.Order.Status {
	case "open":
		state = "resting"
	case "filled":
		state = "filled"
	case "canceled", "rejected", "marginCanceled":
		state = "cancelled"
		if resp.Order.Status == "rejected" {
			state = "rejected"
		}
	}
	oid := resp.Order.Order.Oid
	if oid == 0 {
		oid = fallbackOID
	}
	cloid := resp.Order.Order.Cloid
	if cloid == "" {
		cloid = fallbackCloid
	}
	return ExchangeSubmissionResult{
		State:         state,
		OrderID:       oid,
		ClientOrderID: cloid,
		FilledSize:    resp.Order.Order.Sz,
		AvgPrice:      resp.Order.Order.Limit,
	}, nil
}

// ReconcileOrder queries POST /info orderStatus by numeric orderId to resolve uncertain submissions.
func (c *Client) ReconcileOrder(ctx context.Context, address string, orderID uint64) (ExchangeSubmissionResult, error) {
	canon, err := signer.ValidateEthereumAddress(address)
	if err != nil {
		return ExchangeSubmissionResult{}, err
	}
	if orderID == 0 {
		return ExchangeSubmissionResult{}, errors.New("reconcile requires non-zero orderId")
	}
	raw, err := c.postJSON(ctx, "/info", map[string]any{
		"type": "orderStatus",
		"user": canon,
		"oid":  orderID,
	})
	if err != nil {
		return ExchangeSubmissionResult{State: "unknown", OrderID: orderID, Message: err.Error()}, err
	}
	return parseOrderStatusResponse(raw, orderID, "")
}

// ReconcileOrderByCloid queries POST /info orderStatus by 16-byte hex cloid when numeric orderId is unknown.
func (c *Client) ReconcileOrderByCloid(ctx context.Context, address, cloid string) (ExchangeSubmissionResult, error) {
	canon, err := signer.ValidateEthereumAddress(address)
	if err != nil {
		return ExchangeSubmissionResult{}, err
	}
	canonCloid := strings.ToLower(strings.TrimSpace(cloid))
	if !strings.HasPrefix(canonCloid, "0x") || len(canonCloid) != 34 {
		return ExchangeSubmissionResult{}, errors.New("reconcile requires 16-byte 0x cloid")
	}
	if rawHex, err := hex.DecodeString(canonCloid[2:]); err != nil || len(rawHex) != 16 {
		return ExchangeSubmissionResult{}, errors.New("reconcile requires valid 16-byte hex cloid")
	}
	raw, err := c.postJSON(ctx, "/info", map[string]any{
		"type": "orderStatus",
		"user": canon,
		"oid":  canonCloid,
	})
	if err != nil {
		return ExchangeSubmissionResult{State: "unknown", ClientOrderID: canonCloid, Message: err.Error()}, err
	}
	return parseOrderStatusResponse(raw, 0, canonCloid)
}
