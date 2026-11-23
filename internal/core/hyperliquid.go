package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/hyperliquid"
	"github.com/fased-ai/fased-app/internal/signer"
)

const (
	hyperliquidVersion           = "hyperliquid-perps-v1"
	hyperliquidActivationEnabled = false
	hyperliquidReviewTTLSeconds  = 120
	maxStoredHyperliquidOrders   = 64
)

// HyperliquidCommand carries bounded UI/CLI command parameters for POST /api/v1/agents.
type HyperliquidCommand struct {
	Digest  string                  `json:"digest,omitempty"`
	OrderID uint64                  `json:"orderId,omitempty"`
	Request *hyperliquid.OrderDraft `json:"request,omitempty"`
}

// HyperliquidReview represents a short-lived, single-use order review pending owner submission.
type HyperliquidReview struct {
	Revision  int64                    `json:"revision"`
	ExpiresAt int64                    `json:"expiresAt"`
	Digest    string                   `json:"digest"`
	Preview   hyperliquid.OrderPreview `json:"preview"`
}

// HyperliquidOrderRecord tracks signed/submitted/reconciled controlled Hyperliquid orders.
type HyperliquidOrderRecord struct {
	ID              string `json:"id"`
	OrderID         uint64 `json:"orderId"`
	Asset           string `json:"asset"`
	IsBuy           bool   `json:"isBuy"`
	LimitPrice      string `json:"limitPrice"`
	Size            string `json:"size"`
	Leverage        uint32 `json:"leverage"`
	ReduceOnly      bool   `json:"reduceOnly"`
	TimeInForce     string `json:"timeInForce"`
	State           string `json:"state"`
	Nonce           uint64 `json:"nonce"`
	SignerAddress   string `json:"signerAddress"`
	ConnectionID    string `json:"connectionId"`
	TypedDataDigest string `json:"typedDataDigest"`
	SignatureHex    string `json:"signatureHex"`
	FilledSize      string `json:"filledSize,omitempty"`
	AvgPrice        string `json:"avgPrice,omitempty"`
	Message         string `json:"message,omitempty"`
	UpdatedAt       int64  `json:"updatedAt"`
}

// HyperliquidView is projected under AgentView.Hyperliquid only when configured.
type HyperliquidView struct {
	Version           string                    `json:"version"`
	Available         bool                      `json:"available"`
	ActivationEnabled bool                      `json:"activationEnabled"`
	LiveExecution     bool                      `json:"liveExecution"`
	Network           string                    `json:"network"`
	SignerAddress     string                    `json:"signerAddress"`
	Policy            signer.HyperliquidPolicy  `json:"policy"`
	Markets           []hyperliquid.MarketMeta  `json:"markets"`
	Account           *hyperliquid.AccountState `json:"account,omitempty"`
	Review            *HyperliquidReview        `json:"review,omitempty"`
	Orders            []HyperliquidOrderRecord  `json:"orders"`
}

type hyperliquidReviewEntry struct {
	review HyperliquidReview
	bootID string
}

type hyperliquidRuntime struct {
	mu        sync.Mutex
	client    *hyperliquid.Client
	vault     *signer.Vault
	policy    signer.HyperliquidPolicy
	address   string
	markets   hyperliquid.MarketSnapshot
	account   hyperliquid.AccountState
	reviews   map[string]hyperliquidReviewEntry
	orders    []HyperliquidOrderRecord
	lastNonce uint64
	nextNonce uint64
}

var (
	hyperliquidRuntimeMu sync.RWMutex
	hyperliquidRuntimes  = map[*Store]*hyperliquidRuntime{}
)

func (s *Store) hyperliquidRuntime() *hyperliquidRuntime {
	if s == nil {
		return nil
	}
	hyperliquidRuntimeMu.RLock()
	defer hyperliquidRuntimeMu.RUnlock()
	return hyperliquidRuntimes[s]
}

func (s *Store) clearHyperliquidRuntime() {
	if s == nil {
		return
	}
	hyperliquidRuntimeMu.Lock()
	delete(hyperliquidRuntimes, s)
	hyperliquidRuntimeMu.Unlock()
}

// ConfigureHyperliquidControlled attaches a bounded controlled Hyperliquid venue adapter
// and unlocked controlled signer vault to a Store. Ordinary startup never calls this;
// mainnet remains blocked while hyperliquidActivationEnabled == false.
func (s *Store) ConfigureHyperliquidControlled(client *hyperliquid.Client, vault *signer.Vault, policy signer.HyperliquidPolicy) error {
	if s == nil || client == nil || vault == nil || !client.Controlled() {
		return signer.ErrUnavailable
	}
	if hyperliquidActivationEnabled || signer.HyperliquidActivationEnabled {
		return signer.ErrUnavailable
	}
	if policy.Network == "mainnet" || policy.Network != client.Network() {
		return signer.ErrUnavailable
	}
	now := s.now().Unix()
	if err := signer.ValidateHyperliquidPolicy(policy, now); err != nil {
		return err
	}
	addr, err := vault.HyperliquidAddress()
	if err != nil {
		return err
	}
	initCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	markets, err := client.FetchMarkets(initCtx)
	if err != nil {
		return err
	}
	markets.FetchedAt = now
	acct, err := client.FetchAccountState(initCtx, addr)
	if err != nil {
		acct = hyperliquid.AccountState{
			Address:    addr,
			Positions:  []hyperliquid.Position{},
			OpenOrders: []hyperliquid.OpenOrder{},
		}
	}
	rt := &hyperliquidRuntime{
		client:    client,
		vault:     vault,
		policy:    policy,
		address:   addr,
		markets:   markets,
		account:   acct,
		reviews:   map[string]hyperliquidReviewEntry{},
		orders:    []HyperliquidOrderRecord{},
		nextNonce: uint64(s.now().UnixMilli()),
	}
	hyperliquidRuntimeMu.Lock()
	hyperliquidRuntimes[s] = rt
	hyperliquidRuntimeMu.Unlock()
	return nil
}

// ConfigureHyperliquidControlled exposes the controlled constructor on Server without
// opening HTTP, CLI, or environment activation paths.
func (server *Server) ConfigureHyperliquidControlled(client *hyperliquid.Client, vault *signer.Vault, policy signer.HyperliquidPolicy) error {
	if server == nil || server.Store == nil {
		return signer.ErrUnavailable
	}
	return server.Store.ConfigureHyperliquidControlled(client, vault, policy)
}

func hyperliquidDigest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Store) hyperliquidProjection(revision int64, review *HyperliquidReview) *HyperliquidView {
	rt := s.hyperliquidRuntime()
	if rt == nil {
		return nil
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	markets := slices.Clone(rt.markets.Markets)
	if markets == nil {
		markets = []hyperliquid.MarketMeta{}
	}
	orders := slices.Clone(rt.orders)
	if orders == nil {
		orders = []HyperliquidOrderRecord{}
	}
	acctCopy := rt.account
	acctCopy.Positions = slices.Clone(rt.account.Positions)
	if acctCopy.Positions == nil {
		acctCopy.Positions = []hyperliquid.Position{}
	}
	acctCopy.OpenOrders = slices.Clone(rt.account.OpenOrders)
	if acctCopy.OpenOrders == nil {
		acctCopy.OpenOrders = []hyperliquid.OpenOrder{}
	}
	policyCopy := rt.policy
	policyCopy.AllowedAssets = slices.Clone(rt.policy.AllowedAssets)
	return &HyperliquidView{
		Version:           hyperliquidVersion,
		Available:         true,
		ActivationEnabled: hyperliquidActivationEnabled,
		LiveExecution:     false,
		Network:           rt.policy.Network,
		SignerAddress:     rt.address,
		Policy:            policyCopy,
		Markets:           markets,
		Account:           &acctCopy,
		Review:            review,
		Orders:            orders,
	}
}

func (rt *hyperliquidRuntime) recordOrder(rec HyperliquidOrderRecord) {
	if len(rt.orders) >= maxStoredHyperliquidOrders {
		rt.orders = slices.Clone(rt.orders[len(rt.orders)-maxStoredHyperliquidOrders+1:])
	}
	rt.orders = append(rt.orders, rec)
}

func (s *Store) refreshHyperliquidState(ctx context.Context, rt *hyperliquidRuntime) error {
	rt.mu.Lock()
	client, addr := rt.client, rt.address
	rt.mu.Unlock()

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	markets, err := client.FetchMarkets(callCtx)
	if err != nil {
		return err
	}
	markets.FetchedAt = s.now().Unix()
	acct, acctErr := client.FetchAccountState(callCtx, addr)

	rt.mu.Lock()
	rt.markets = markets
	if acctErr == nil {
		rt.account = acct
	}
	rt.mu.Unlock()
	return acctErr
}

func (s *Store) changeHyperliquid(ctx context.Context, c AgentCommand) (*HyperliquidView, string, error) {
	rt := s.hyperliquidRuntime()
	if rt == nil {
		return nil, "", signer.ErrUnavailable
	}
	d, revision, err := readAgents(ctx, s.db)
	if err != nil {
		return nil, "", err
	}
	if c.Revision != revision {
		return nil, "", ErrConflict
	}
	profile := c.ProfileID
	if profile == "" && len(d.Profiles) > 0 {
		profile = d.Profiles[0].ID
	}
	now := s.now().Unix()

	switch c.Action {
	case "hyperliquid-refresh":
		if err := s.refreshHyperliquidState(ctx, rt); err != nil {
			return nil, "", err
		}
		return s.hyperliquidProjection(revision, nil), profile, nil

	case "hyperliquid-review":
		if c.Hyperliquid == nil || c.Hyperliquid.Request == nil {
			return nil, "", errors.New("hyperliquid order draft required")
		}
		rt.mu.Lock()
		for k, entry := range rt.reviews {
			if entry.review.ExpiresAt <= now {
				delete(rt.reviews, k)
			}
		}
		if rt.nextNonce < uint64(s.now().UnixMilli()) {
			rt.nextNonce = uint64(s.now().UnixMilli())
		} else {
			rt.nextNonce++
		}
		nextNonce := rt.nextNonce
		lastNonce := rt.lastNonce
		markets := rt.markets
		policy := rt.policy
		preview, _, err := hyperliquid.PreviewOrder(markets, policy, *c.Hyperliquid.Request, lastNonce, nextNonce, now)
		if err != nil {
			rt.mu.Unlock()
			return nil, "", err
		}
		rev := HyperliquidReview{
			Revision:  revision,
			ExpiresAt: min(preview.Intent.ExpiresAt, now+hyperliquidReviewTTLSeconds),
			Preview:   preview,
		}
		rev.Digest = hyperliquidDigest(struct {
			Review HyperliquidReview
			BootID string
		}{rev, s.bootID})
		rt.reviews[rev.Digest] = hyperliquidReviewEntry{review: rev, bootID: s.bootID}
		rt.mu.Unlock()
		return s.hyperliquidProjection(revision, &rev), profile, nil

	case "hyperliquid-submit":
		if c.Hyperliquid == nil || c.Hyperliquid.Digest == "" {
			return nil, "", errors.New("reviewed order digest required")
		}
		rt.mu.Lock()
		entry, ok := rt.reviews[c.Hyperliquid.Digest]
		if ok {
			// Consume review atomically to prevent duplicate submission/replay.
			delete(rt.reviews, c.Hyperliquid.Digest)
		}
		if !ok || entry.bootID != s.bootID || entry.review.Revision != revision || entry.review.ExpiresAt <= now {
			rt.mu.Unlock()
			return nil, "", errors.New("hyperliquid order review missing, expired, or stale")
		}
		intent := entry.review.Preview.Intent
		policy := rt.policy
		vault := rt.vault
		client := rt.client
		signed, err := vault.SignHyperliquidOrder(intent, policy)
		if err != nil {
			rt.mu.Unlock()
			return nil, "", err
		}
		if intent.Nonce > rt.lastNonce {
			rt.lastNonce = intent.Nonce
		}
		rt.mu.Unlock()

		submitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, subErr := client.SubmitSignedOrder(submitCtx, intent, signed, policy.VaultAddress)
		cancel()

		state := res.State
		if state == "" {
			state = "unknown"
		}
		msg := res.Message
		if subErr != nil && msg == "" {
			msg = subErr.Error()
		}
		rec := HyperliquidOrderRecord{
			ID:              fmt.Sprintf("hl-%d", signed.Nonce),
			OrderID:         res.OrderID,
			Asset:           intent.Asset,
			IsBuy:           intent.IsBuy,
			LimitPrice:      intent.LimitPrice,
			Size:            intent.Size,
			Leverage:        intent.Leverage,
			ReduceOnly:      intent.ReduceOnly,
			TimeInForce:     intent.TimeInForce,
			State:           state,
			Nonce:           signed.Nonce,
			SignerAddress:   signed.SignerAddress,
			ConnectionID:    signed.ConnectionID,
			TypedDataDigest: signed.TypedDataDigest,
			SignatureHex:    signed.SignatureHex,
			FilledSize:      res.FilledSize,
			AvgPrice:        res.AvgPrice,
			Message:         msg,
			UpdatedAt:       now,
		}
		rt.mu.Lock()
		rt.recordOrder(rec)
		rt.mu.Unlock()
		_ = s.refreshHyperliquidState(ctx, rt)
		return s.hyperliquidProjection(revision, nil), profile, nil

	case "hyperliquid-reconcile":
		if c.Hyperliquid == nil || c.Hyperliquid.OrderID == 0 {
			return nil, "", errors.New("orderId required for reconciliation")
		}
		rt.mu.Lock()
		client, addr := rt.client, rt.address
		rt.mu.Unlock()

		recCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, recErr := client.ReconcileOrder(recCtx, addr, c.Hyperliquid.OrderID)
		cancel()

		rt.mu.Lock()
		found := false
		for i := range rt.orders {
			if rt.orders[i].OrderID == c.Hyperliquid.OrderID {
				found = true
				if res.State != "" {
					rt.orders[i].State = res.State
				}
				if res.FilledSize != "" {
					rt.orders[i].FilledSize = res.FilledSize
				}
				if res.AvgPrice != "" {
					rt.orders[i].AvgPrice = res.AvgPrice
				}
				if res.Message != "" {
					rt.orders[i].Message = res.Message
				}
				rt.orders[i].UpdatedAt = now
			}
		}
		rt.mu.Unlock()
		if !found {
			return nil, "", errors.New("hyperliquid order not found")
		}
		if recErr != nil && res.State == "" {
			return nil, "", recErr
		}
		_ = s.refreshHyperliquidState(ctx, rt)
		return s.hyperliquidProjection(revision, nil), profile, nil

	case "hyperliquid-cancel":
		if c.Hyperliquid == nil || c.Hyperliquid.OrderID == 0 {
			return nil, "", errors.New("orderId required for cancel")
		}
		rt.mu.Lock()
		idx := -1
		for i := range rt.orders {
			if rt.orders[i].OrderID == c.Hyperliquid.OrderID {
				idx = i
				break
			}
		}
		if idx < 0 {
			rt.mu.Unlock()
			return nil, "", errors.New("hyperliquid order not found")
		}
		assetSym := rt.orders[idx].Asset
		var assetIdx uint32
		foundAsset := false
		for _, m := range rt.markets.Markets {
			if m.Symbol == assetSym {
				assetIdx = m.AssetIndex
				foundAsset = true
				break
			}
		}
		if !foundAsset {
			rt.mu.Unlock()
			return nil, "", errors.New("asset metadata unavailable for cancel")
		}
		if rt.nextNonce < uint64(s.now().UnixMilli()) {
			rt.nextNonce = uint64(s.now().UnixMilli())
		} else {
			rt.nextNonce++
		}
		nonce := rt.nextNonce
		policy := rt.policy
		vault := rt.vault
		client := rt.client
		cancelIntent := signer.HyperliquidCancelIntent{
			Network:    policy.Network,
			Asset:      assetSym,
			AssetIndex: assetIdx,
			OrderID:    c.Hyperliquid.OrderID,
			Nonce:      nonce,
			ExpiresAt:  min(now+hyperliquidReviewTTLSeconds, policy.ExpiresAt),
		}
		signed, err := vault.SignHyperliquidCancel(cancelIntent, policy)
		if err != nil {
			rt.mu.Unlock()
			return nil, "", err
		}
		if cancelIntent.Nonce > rt.lastNonce {
			rt.lastNonce = cancelIntent.Nonce
		}
		rt.mu.Unlock()

		cCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, cancelErr := client.SubmitSignedCancel(cCtx, cancelIntent, signed, policy.VaultAddress)
		cancel()

		rt.mu.Lock()
		for i := range rt.orders {
			if rt.orders[i].OrderID == c.Hyperliquid.OrderID {
				if res.State != "" {
					rt.orders[i].State = res.State
				} else if cancelErr != nil {
					rt.orders[i].State = "unknown"
					rt.orders[i].Message = cancelErr.Error()
				}
				rt.orders[i].UpdatedAt = now
			}
		}
		rt.mu.Unlock()
		_ = s.refreshHyperliquidState(ctx, rt)
		return s.hyperliquidProjection(revision, nil), profile, nil

	default:
		return nil, "", errors.New("unsupported hyperliquid action")
	}
}
