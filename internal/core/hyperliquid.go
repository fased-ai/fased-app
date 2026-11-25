package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	ID            string                  `json:"id,omitempty"`
	Digest        string                  `json:"digest,omitempty"`
	OrderID       uint64                  `json:"orderId,omitempty"`
	ClientOrderID string                  `json:"clientOrderId,omitempty"`
	Request       *hyperliquid.OrderDraft `json:"request,omitempty"`
}

// HyperliquidReview represents a short-lived, single-use order review pending owner submission.
type HyperliquidReview struct {
	Revision       int64                                  `json:"revision"`
	ExpiresAt      int64                                  `json:"expiresAt"`
	Digest         string                                 `json:"digest"`
	Preview        hyperliquid.OrderPreview               `json:"preview"`
	LeverageIntent signer.HyperliquidUpdateLeverageIntent `json:"leverageIntent"`
}

// HyperliquidOrderRecord tracks signed/submitted/reconciled controlled Hyperliquid orders.
type HyperliquidOrderRecord = hyperliquid.OrderRecord

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

type hyperliquidSignerBackend interface {
	Address() (string, error)
	SignUpdateLeverage(ctx context.Context, intent signer.HyperliquidUpdateLeverageIntent, policy signer.HyperliquidPolicy) (*signer.HyperliquidSignedAction, error)
	SignOrder(ctx context.Context, intent signer.HyperliquidOrderIntent, policy signer.HyperliquidPolicy) (*signer.HyperliquidSignedAction, error)
	SignCancel(ctx context.Context, intent signer.HyperliquidCancelIntent, policy signer.HyperliquidPolicy) (*signer.HyperliquidSignedAction, error)
}

type vaultHyperliquidSigner struct {
	vault *signer.Vault
}

func (b vaultHyperliquidSigner) Address() (string, error) {
	return b.vault.HyperliquidAddress()
}

func (b vaultHyperliquidSigner) SignUpdateLeverage(_ context.Context, intent signer.HyperliquidUpdateLeverageIntent, policy signer.HyperliquidPolicy) (*signer.HyperliquidSignedAction, error) {
	return b.vault.SignHyperliquidUpdateLeverage(intent, policy)
}

func (b vaultHyperliquidSigner) SignOrder(_ context.Context, intent signer.HyperliquidOrderIntent, policy signer.HyperliquidPolicy) (*signer.HyperliquidSignedAction, error) {
	return b.vault.SignHyperliquidOrder(intent, policy)
}

func (b vaultHyperliquidSigner) SignCancel(_ context.Context, intent signer.HyperliquidCancelIntent, policy signer.HyperliquidPolicy) (*signer.HyperliquidSignedAction, error) {
	return b.vault.SignHyperliquidCancel(intent, policy)
}

type hyperliquidRuntime struct {
	mu        sync.Mutex
	client    *hyperliquid.Client
	signer    hyperliquidSignerBackend
	store     *hyperliquid.OrderStore
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
	hyperliquidRuntimeMu           sync.RWMutex
	hyperliquidRuntimes            = map[*Store]*hyperliquidRuntime{}
	stage4LiveExecutionHook        func(*Store) bool
	stage4ClearHook                func(*Store)
	stage4EnsureHyperliquidIPCHook func(context.Context, *Store) bool
)

func (s *Store) hyperliquidRuntime() *hyperliquidRuntime {
	if s == nil {
		return nil
	}
	hyperliquidRuntimeMu.RLock()
	defer hyperliquidRuntimeMu.RUnlock()
	return hyperliquidRuntimes[s]
}

func (s *Store) clearHyperliquidRuntime() error {
	if s == nil {
		return nil
	}
	if stage4ClearHook != nil {
		stage4ClearHook(s)
	}
	hyperliquidRuntimeMu.Lock()
	old := hyperliquidRuntimes[s]
	delete(hyperliquidRuntimes, s)
	hyperliquidRuntimeMu.Unlock()
	if old != nil && old.store != nil {
		return old.store.Close()
	}
	return nil
}

// ConfigureHyperliquidControlled attaches a bounded controlled Hyperliquid venue adapter
// and unlocked controlled signer vault to a Store. Ordinary startup never calls this;
// mainnet remains blocked while hyperliquidActivationEnabled == false.
func (s *Store) ConfigureHyperliquidControlled(client *hyperliquid.Client, vault *signer.Vault, policy signer.HyperliquidPolicy) error {
	if s == nil || client == nil || vault == nil || !client.Controlled() {
		return signer.ErrUnavailable
	}
	addr, err := vault.HyperliquidAddress()
	if err != nil {
		return err
	}
	return s.configureHyperliquidRuntime(client, vaultHyperliquidSigner{vault: vault}, policy, addr, 0)
}

func (s *Store) configureHyperliquidRuntime(client *hyperliquid.Client, backend hyperliquidSignerBackend, policy signer.HyperliquidPolicy, addr string, signerLastNonce uint64) error {
	if s == nil || client == nil || backend == nil || !client.Controlled() {
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
	canonAddr, err := signer.ValidateEthereumAddress(addr)
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
	acct, err := client.FetchAccountState(initCtx, canonAddr)
	if err != nil {
		acct = hyperliquid.AccountState{
			Address:    canonAddr,
			Positions:  []hyperliquid.Position{},
			OpenOrders: []hyperliquid.OpenOrder{},
		}
	}
	var (
		orderStore     *hyperliquid.OrderStore
		savedOrders    = []HyperliquidOrderRecord{}
		savedLastNonce uint64
	)
	if s.dir != "" {
		orderStore, err = hyperliquid.OpenOrderStore(s.dir)
		if err != nil {
			return err
		}
		loaded, loadedNonce, loadErr := orderStore.LoadState(initCtx, maxStoredHyperliquidOrders)
		if loadErr != nil {
			_ = orderStore.Close()
			return loadErr
		}
		if loaded != nil {
			savedOrders = loaded
		}
		savedLastNonce = loadedNonce
	}
	lastNonce := max(savedLastNonce, signerLastNonce)
	nextNonce := uint64(s.now().UnixMilli())
	if lastNonce >= nextNonce {
		nextNonce = lastNonce + 1
	}
	rt := &hyperliquidRuntime{
		client:    client,
		signer:    backend,
		store:     orderStore,
		policy:    policy,
		address:   canonAddr,
		markets:   markets,
		account:   acct,
		reviews:   map[string]hyperliquidReviewEntry{},
		orders:    savedOrders,
		lastNonce: lastNonce,
		nextNonce: nextNonce,
	}
	hyperliquidRuntimeMu.Lock()
	prev := hyperliquidRuntimes[s]
	hyperliquidRuntimes[s] = rt
	hyperliquidRuntimeMu.Unlock()
	if prev != nil && prev.store != nil {
		_ = prev.store.Close()
	}
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
		LiveExecution:     stage4LiveExecutionHook != nil && stage4LiveExecutionHook(s) && rt.policy.Network == "testnet",
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
	if stage4EnsureHyperliquidIPCHook != nil {
		stage4EnsureHyperliquidIPCHook(ctx, s)
	}
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
		draft := *c.Hyperliquid.Request
		draft.ClientOrderID = strings.ToLower(strings.TrimSpace(draft.ClientOrderID))
		if draft.ClientOrderID == "" {
			var cloidBytes [16]byte
			if _, err := rand.Read(cloidBytes[:]); err != nil {
				return nil, "", err
			}
			draft.ClientOrderID = "0x" + hex.EncodeToString(cloidBytes[:])
		}

		rt.mu.Lock()
		for k, entry := range rt.reviews {
			if entry.review.ExpiresAt <= now {
				delete(rt.reviews, k)
			}
		}
		floor := uint64(s.now().UnixMilli())
		if rt.lastNonce >= floor {
			floor = rt.lastNonce + 1
		}
		if rt.nextNonce < floor {
			rt.nextNonce = floor
		} else {
			rt.nextNonce++
		}
		levNonce := rt.nextNonce
		orderNonce := levNonce + 1
		rt.nextNonce = orderNonce
		lastNonce := rt.lastNonce
		markets := rt.markets
		policy := rt.policy
		preview, _, err := hyperliquid.PreviewOrder(markets, policy, draft, levNonce, orderNonce, now)
		if err != nil {
			rt.mu.Unlock()
			return nil, "", err
		}
		levIntent := signer.HyperliquidUpdateLeverageIntent{
			Network:    policy.Network,
			Asset:      preview.Intent.Asset,
			AssetIndex: preview.Intent.AssetIndex,
			Leverage:   preview.Intent.Leverage,
			IsCross:    preview.Intent.IsCross,
			Nonce:      levNonce,
			ExpiresAt:  preview.Intent.ExpiresAt,
		}
		if err := signer.ValidateHyperliquidUpdateLeverageIntent(levIntent, policy, lastNonce, now); err != nil {
			rt.mu.Unlock()
			return nil, "", err
		}
		rev := HyperliquidReview{
			Revision:       revision,
			ExpiresAt:      min(preview.Intent.ExpiresAt, now+hyperliquidReviewTTLSeconds),
			Preview:        preview,
			LeverageIntent: levIntent,
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
		levIntent := entry.review.LeverageIntent
		intent := entry.review.Preview.Intent
		policy := rt.policy
		signerBackend := rt.signer
		client := rt.client
		addr := rt.address
		orderStore := rt.store
		rt.mu.Unlock()

		signLevCtx, cancelSignLev := context.WithTimeout(ctx, 5*time.Second)
		signedLev, err := signerBackend.SignUpdateLeverage(signLevCtx, levIntent, policy)
		cancelSignLev()
		if err != nil {
			return nil, "", err
		}
		rt.mu.Lock()
		if levIntent.Nonce > rt.lastNonce {
			rt.lastNonce = levIntent.Nonce
		}
		rt.mu.Unlock()

		if orderStore != nil {
			if err := orderStore.SaveLastNonce(ctx, levIntent.Nonce); err != nil {
				return nil, "", err
			}
		}

		// Apply explicit leverage & margin-mode configuration BEFORE signing/submitting the order.
		levCtx, cancelLev := context.WithTimeout(ctx, 5*time.Second)
		levRes, levErr := client.SubmitSignedUpdateLeverage(levCtx, levIntent, signedLev, policy.VaultAddress)
		cancelLev()
		if levErr != nil {
			return nil, "", fmt.Errorf("hyperliquid updateLeverage failed: %w", levErr)
		}
		if levRes.State != "ok" {
			return nil, "", fmt.Errorf("hyperliquid updateLeverage rejected: %s", levRes.Message)
		}

		verifyCtx, cancelVerify := context.WithTimeout(ctx, 5*time.Second)
		acct, acctErr := client.FetchAccountState(verifyCtx, addr)
		cancelVerify()
		if acctErr != nil {
			return nil, "", fmt.Errorf("hyperliquid leverage verification unavailable: %w", acctErr)
		}
		expectedMode := "isolated"
		if levIntent.IsCross {
			expectedMode = "cross"
		}
		for _, pos := range acct.Positions {
			if pos.Symbol == intent.Asset {
				if pos.Leverage != levIntent.Leverage || pos.MarginMode != expectedMode {
					return nil, "", fmt.Errorf("hyperliquid leverage verification mismatch for %s: got %dx %s, want %dx %s",
						intent.Asset, pos.Leverage, pos.MarginMode, levIntent.Leverage, expectedMode)
				}
				break
			}
		}

		rt.mu.Lock()
		rt.account = acct
		rt.mu.Unlock()

		signOrdCtx, cancelSignOrd := context.WithTimeout(ctx, 5*time.Second)
		signed, err := signerBackend.SignOrder(signOrdCtx, intent, policy)
		cancelSignOrd()
		if err != nil {
			return nil, "", err
		}
		rt.mu.Lock()
		if intent.Nonce > rt.lastNonce {
			rt.lastNonce = intent.Nonce
		}
		rt.mu.Unlock()

		if orderStore != nil {
			if err := orderStore.SaveLastNonce(ctx, intent.Nonce); err != nil {
				return nil, "", err
			}
		}

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
			ClientOrderID:   intent.ClientOrderID,
			Asset:           intent.Asset,
			IsBuy:           intent.IsBuy,
			IsCross:         intent.IsCross,
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
		if orderStore != nil {
			if err := orderStore.UpsertOrder(ctx, rec); err != nil {
				return nil, "", err
			}
		}
		_ = s.refreshHyperliquidState(ctx, rt)
		return s.hyperliquidProjection(revision, nil), profile, nil

	case "hyperliquid-reconcile":
		if c.Hyperliquid == nil || (c.Hyperliquid.OrderID == 0 && strings.TrimSpace(c.Hyperliquid.ClientOrderID) == "" && strings.TrimSpace(c.Hyperliquid.ID) == "") {
			return nil, "", errors.New("orderId, clientOrderId, or id required for reconciliation")
		}
		wantOID := c.Hyperliquid.OrderID
		wantCloid := strings.ToLower(strings.TrimSpace(c.Hyperliquid.ClientOrderID))
		wantID := strings.TrimSpace(c.Hyperliquid.ID)

		rt.mu.Lock()
		var targetOrd *HyperliquidOrderRecord
		for i := range rt.orders {
			o := &rt.orders[i]
			switch {
			case wantOID != 0 && o.OrderID == wantOID:
				cp := *o
				targetOrd = &cp
			case wantCloid != "" && strings.EqualFold(o.ClientOrderID, wantCloid):
				cp := *o
				targetOrd = &cp
			case wantID != "" && o.ID == wantID:
				cp := *o
				targetOrd = &cp
			}
			if targetOrd != nil {
				break
			}
		}
		client, addr, orderStore := rt.client, rt.address, rt.store
		rt.mu.Unlock()
		if targetOrd == nil {
			return nil, "", errors.New("hyperliquid order not found")
		}

		recCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var (
			res    hyperliquid.ExchangeSubmissionResult
			recErr error
		)
		if targetOrd.OrderID != 0 {
			res, recErr = client.ReconcileOrder(recCtx, addr, targetOrd.OrderID)
		}
		if (targetOrd.OrderID == 0 || recErr != nil || res.State == "unknown" || res.State == "") && targetOrd.ClientOrderID != "" {
			cloidRes, cloidErr := client.ReconcileOrderByCloid(recCtx, addr, targetOrd.ClientOrderID)
			if cloidErr == nil && (cloidRes.State != "" && (cloidRes.State != "unknown" || res.State == "")) {
				res = cloidRes
				recErr = nil
			} else if targetOrd.OrderID == 0 {
				res = cloidRes
				recErr = cloidErr
			}
		}
		cancel()

		var updatedRec *HyperliquidOrderRecord
		rt.mu.Lock()
		for i := range rt.orders {
			if rt.orders[i].ID == targetOrd.ID {
				if res.OrderID != 0 {
					rt.orders[i].OrderID = res.OrderID
				}
				if res.ClientOrderID != "" && rt.orders[i].ClientOrderID == "" {
					rt.orders[i].ClientOrderID = strings.ToLower(res.ClientOrderID)
				}
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
				cp := rt.orders[i]
				updatedRec = &cp
				break
			}
		}
		rt.mu.Unlock()
		if updatedRec == nil {
			return nil, "", errors.New("hyperliquid order not found")
		}
		if orderStore != nil {
			if err := orderStore.UpsertOrder(ctx, *updatedRec); err != nil {
				return nil, "", err
			}
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
		targetID := rt.orders[idx].ID
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
		floor := uint64(s.now().UnixMilli())
		if rt.lastNonce >= floor {
			floor = rt.lastNonce + 1
		}
		if rt.nextNonce < floor {
			rt.nextNonce = floor
		} else {
			rt.nextNonce++
		}
		nonce := rt.nextNonce
		policy := rt.policy
		signerBackend := rt.signer
		client := rt.client
		orderStore := rt.store
		cancelIntent := signer.HyperliquidCancelIntent{
			Network:    policy.Network,
			Asset:      assetSym,
			AssetIndex: assetIdx,
			OrderID:    c.Hyperliquid.OrderID,
			Nonce:      nonce,
			ExpiresAt:  min(now+hyperliquidReviewTTLSeconds, policy.ExpiresAt),
		}
		rt.mu.Unlock()

		signCanCtx, cancelSignCan := context.WithTimeout(ctx, 5*time.Second)
		signed, err := signerBackend.SignCancel(signCanCtx, cancelIntent, policy)
		cancelSignCan()
		if err != nil {
			return nil, "", err
		}
		rt.mu.Lock()
		if cancelIntent.Nonce > rt.lastNonce {
			rt.lastNonce = cancelIntent.Nonce
		}
		rt.mu.Unlock()

		if orderStore != nil {
			if err := orderStore.SaveLastNonce(ctx, cancelIntent.Nonce); err != nil {
				return nil, "", err
			}
		}

		cCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		res, cancelErr := client.SubmitSignedCancel(cCtx, cancelIntent, signed, policy.VaultAddress)
		cancel()

		var updatedCancel *HyperliquidOrderRecord
		rt.mu.Lock()
		for i := range rt.orders {
			if rt.orders[i].ID == targetID || rt.orders[i].OrderID == c.Hyperliquid.OrderID {
				if res.State != "" {
					rt.orders[i].State = res.State
				} else if cancelErr != nil {
					rt.orders[i].State = "unknown"
					rt.orders[i].Message = cancelErr.Error()
				}
				rt.orders[i].UpdatedAt = now
				cp := rt.orders[i]
				updatedCancel = &cp
				break
			}
		}
		rt.mu.Unlock()
		if orderStore != nil && updatedCancel != nil {
			if err := orderStore.UpsertOrder(ctx, *updatedCancel); err != nil {
				return nil, "", err
			}
		}
		_ = s.refreshHyperliquidState(ctx, rt)
		return s.hyperliquidProjection(revision, nil), profile, nil

	default:
		return nil, "", errors.New("unsupported hyperliquid action")
	}
}
