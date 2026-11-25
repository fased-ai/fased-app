package signer

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

func init() {
	hyperliquidJournalFloor = func(v *Vault) uint64 {
		if v != nil && v.ledger.allowStage4PolicyReplay() {
			return v.ledger.hlLastNonce
		}
		return 0
	}
	recordHyperliquidJournalSign = func(v *Vault, action string, nonce uint64, digest string) error {
		if v == nil || v.ledger == nil || v.ledger.main == nil || !v.ledger.allowStage4PolicyReplay() {
			return nil
		}
		return v.ledger.append(v, journalRecord{
			Kind:              "hyperliquid-sign",
			HyperliquidAction: action,
			HyperliquidNonce:  nonce,
			HyperliquidDigest: digest,
		})
	}
}

func (l *ledger) applyHyperliquidPolicy(r journalRecord) error {
	if r.HyperliquidPolicy == nil {
		return errors.New("hyperliquid policy record required")
	}
	p := clone(*r.HyperliquidPolicy)
	if err := ValidateHyperliquidPolicyForProfile(p, r.At, l.allowStage4PolicyReplay(), MainnetCappedHardMaxOrderMicroUSD); err != nil {
		return err
	}
	l.hlPolicy = &p
	return nil
}

func (l *ledger) applyHyperliquidSign(r journalRecord) error {
	if r.HyperliquidNonce == 0 || r.HyperliquidNonce <= l.hlLastNonce {
		return errors.New("hyperliquid journal nonce must be strictly increasing")
	}
	switch r.HyperliquidAction {
	case "order", "updateLeverage", "cancel":
	default:
		return errors.New("invalid hyperliquid journal action")
	}
	if strings.TrimSpace(r.HyperliquidDigest) == "" {
		return errors.New("hyperliquid journal digest required")
	}
	l.hlLastNonce = r.HyperliquidNonce
	return nil
}

func (l *ledger) applyAgentVaultPolicy(r journalRecord) error {
	if r.AgentVault == nil {
		return errors.New("agent vault policy record required")
	}
	p := clone(*r.AgentVault)
	if err := ValidateAgentVaultPolicy(p, r.At); err != nil {
		return err
	}
	if l.agentVaults == nil {
		l.agentVaults = map[string]AgentVaultPolicy{}
	}
	if existing, exists := l.agentVaults[p.VaultID]; exists && existing.Revoked {
		return errors.New("revoked agent vault cannot be re-enabled")
	}
	l.agentVaults[p.VaultID] = p
	return nil
}

func (l *ledger) applyAgentVaultRevoke(r journalRecord) error {
	if !isHex32ID(r.ID) {
		return errors.New("valid agent vault id required for revocation")
	}
	p, ok := l.agentVaults[r.ID]
	if !ok {
		return errors.New("unknown agent vault")
	}
	p.Revoked = true
	l.agentVaults[r.ID] = p
	return nil
}

func (l *ledger) applyAgentVaultSpend(r journalRecord) error {
	if r.AgentSpend == nil {
		return errors.New("agent vault spend record required")
	}
	rec := *r.AgentSpend
	if err := ValidateAgentVaultSpendRecord(rec); err != nil {
		return err
	}
	p, ok := l.agentVaults[rec.VaultID]
	if !ok {
		return errors.New("unknown agent vault")
	}
	if p.Revoked {
		return errors.New("agent vault is revoked")
	}
	if !slices.Contains(p.AllowedVenues, rec.Venue) || !slices.Contains(p.AllowedAssets, rec.Asset) {
		return errors.New("agent vault spend violates venue or asset allowlist")
	}
	if l.agentVaultOps == nil {
		l.agentVaultOps = map[string]bool{}
	}
	if l.agentVaultOps[rec.OperationID] {
		return errors.New("duplicate agent vault operation identity")
	}
	if l.agentDailySpend == nil {
		l.agentDailySpend = map[string]map[string]uint64{}
	}
	if l.agentDailyLoss == nil {
		l.agentDailyLoss = map[string]map[string]uint64{}
	}
	if l.agentDailySpend[rec.VaultID] == nil {
		l.agentDailySpend[rec.VaultID] = map[string]uint64{}
	}
	if l.agentDailyLoss[rec.VaultID] == nil {
		l.agentDailyLoss[rec.VaultID] = map[string]uint64{}
	}
	spent := l.agentDailySpend[rec.VaultID][rec.DayUTC]
	if ^uint64(0)-spent < rec.SpendMicroUSD {
		return errors.New("agent vault daily spend overflow")
	}
	loss := l.agentDailyLoss[rec.VaultID][rec.DayUTC]
	if ^uint64(0)-loss < rec.LossMicroUSD {
		return errors.New("agent vault daily loss overflow")
	}
	l.agentDailySpend[rec.VaultID][rec.DayUTC] = spent + rec.SpendMicroUSD
	l.agentDailyLoss[rec.VaultID][rec.DayUTC] = loss + rec.LossMicroUSD
	l.agentVaultOps[rec.OperationID] = true
	return nil
}

func (v *Vault) allowStage4HyperliquidVaultLocked() bool {
	if v.closed || v.ledger == nil {
		return false
	}
	if v.mainnetCappedEnabled && v.mainnetCappedPolicy.Validate() == nil {
		if v.network == "devnet" {
			return false
		}
		return (v.network == "" || v.network == "mainnet" || v.network == "mainnet-beta") && (v.ledger.ref.Version == 1 || v.ledger.ref.Version == 3)
	}
	if v.network == "mainnet" || v.network == "mainnet-beta" {
		return false
	}
	if v.ledger.ref.Version == 1 && !v.ordinary && v.network == "" {
		return true
	}
	return v.testnetExecEnabled && v.ledger.ref.Version == 3 && v.network == "devnet"
}

func (v *Vault) requireControlledHyperliquidUnlockedLocked() (*hyperliquidVaultState, error) {
	if !v.allowStage4HyperliquidVaultLocked() || v.ledger.main == nil {
		return nil, ErrUnavailable
	}
	st, ok := v.hyperliquidState()
	if !ok || !st.enabled {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	return st, nil
}

func (v *Vault) agentVaultUsageLocked(dayUTC string) []AgentVaultUsage {
	if v.ledger == nil || len(v.ledger.agentVaults) == 0 {
		return nil
	}
	ids := make([]string, 0, len(v.ledger.agentVaults))
	for id := range v.ledger.agentVaults {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]AgentVaultUsage, 0, len(ids))
	for _, id := range ids {
		p := clone(v.ledger.agentVaults[id])
		var spent, loss uint64
		if m := v.ledger.agentDailySpend[id]; m != nil {
			spent = m[dayUTC]
		}
		if m := v.ledger.agentDailyLoss[id]; m != nil {
			loss = m[dayUTC]
		}
		out = append(out, AgentVaultUsage{
			Policy:             p,
			DayUTC:             dayUTC,
			DailySpentMicroUSD: spent,
			DailyLossMicroUSD:  loss,
		})
	}
	return out
}

// SetHyperliquidPolicy durably persists the owner-authorized Hyperliquid policy into
// operations.log. Signer IPC signing never trusts caller-supplied policies.
func (v *Vault) SetHyperliquidPolicy(policy HyperliquidPolicy) error {
	if v == nil {
		return ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, err := v.requireControlledHyperliquidUnlockedLocked(); err != nil {
		return err
	}
	if err := ValidateHyperliquidPolicyForProfile(policy, v.now().Unix(), v.mainnetCappedEnabled, v.mainnetCappedPolicy.MaxOrderMicroUSD); err != nil {
		return err
	}
	p := clone(policy)
	return v.ledger.append(v, journalRecord{
		Kind:              "hyperliquid-policy",
		HyperliquidPolicy: &p,
	})
}

// HyperliquidStatus returns signer-persisted Hyperliquid policy, monotonic nonce floor,
// derived EIP-55 address (when unlocked), and current-UTC-day agent vault usage.
func (v *Vault) HyperliquidStatus() (*HyperliquidStatus, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.allowStage4HyperliquidVaultLocked() {
		return nil, ErrUnavailable
	}
	st, ok := v.hyperliquidState()
	if !ok || !st.enabled {
		return nil, ErrUnavailable
	}
	hyperliquidVaultMu.Lock()
	lastNonce := st.lastNonce
	hyperliquidVaultMu.Unlock()
	if v.ledger.hlLastNonce > lastNonce {
		lastNonce = v.ledger.hlLastNonce
	}
	var addr string
	if v.unlocked() {
		priv, err := DeriveSecp256k1KeyFromSeed(v.seed, hyperliquidDomainTag)
		if err != nil {
			return nil, err
		}
		defer wipe(priv)
		pub, _, err := Secp256k1PublicKey(priv)
		if err != nil {
			return nil, err
		}
		addr, err = EthereumAddressFromPublicKey(pub)
		if err != nil {
			return nil, err
		}
	}
	var polCopy *HyperliquidPolicy
	if v.ledger.hlPolicy != nil {
		p := clone(*v.ledger.hlPolicy)
		polCopy = &p
	}
	dayUTC := v.now().UTC().Format("2006-01-02")
	return &HyperliquidStatus{
		Enabled:   true,
		Address:   addr,
		Policy:    polCopy,
		LastNonce: lastNonce,
		Vaults:    v.agentVaultUsageLocked(dayUTC),
	}, nil
}

// SetAgentVaultPolicy creates or updates a typed agent vault policy in operations.log.
func (v *Vault) SetAgentVaultPolicy(policy AgentVaultPolicy) error {
	if v == nil {
		return ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, err := v.requireControlledHyperliquidUnlockedLocked(); err != nil {
		return err
	}
	if err := ValidateAgentVaultPolicy(policy, v.now().Unix()); err != nil {
		return err
	}
	p := clone(policy)
	return v.ledger.append(v, journalRecord{
		Kind:       "agent-vault-policy",
		AgentVault: &p,
	})
}

// RevokeAgentVault permanently revokes a delegated agent vault in operations.log.
func (v *Vault) RevokeAgentVault(vaultID string) error {
	if v == nil {
		return ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, err := v.requireControlledHyperliquidUnlockedLocked(); err != nil {
		return err
	}
	return v.ledger.append(v, journalRecord{
		Kind: "agent-vault-revoke",
		ID:   vaultID,
	})
}

// RecordAgentVaultLoss durably records realized trading loss against a vault's UTC day.
func (v *Vault) RecordAgentVaultLoss(rec AgentVaultSpendRecord) error {
	if v == nil {
		return ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, err := v.requireControlledHyperliquidUnlockedLocked(); err != nil {
		return err
	}
	if rec.LossMicroUSD == 0 {
		return errors.New("agent vault realized loss must be non-zero")
	}
	if strings.TrimSpace(rec.DayUTC) == "" {
		rec.DayUTC = v.now().UTC().Format("2006-01-02")
	}
	rCopy := rec
	return v.ledger.append(v, journalRecord{
		Kind:       "agent-vault-spend",
		AgentSpend: &rCopy,
	})
}

// AgentVaults lists all typed agent vaults alongside current UTC-day usage.
func (v *Vault) AgentVaults() ([]AgentVaultUsage, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.allowStage4HyperliquidVaultLocked() {
		return nil, ErrUnavailable
	}
	st, ok := v.hyperliquidState()
	if !ok || !st.enabled {
		return nil, ErrUnavailable
	}
	dayUTC := v.now().UTC().Format("2006-01-02")
	return v.agentVaultUsageLocked(dayUTC), nil
}

func (v *Vault) validateAgentVaultForVenueLocked(agentVaultID, venue, asset string, leverage uint32, notional uint64, reduceOnly bool, nowUnix int64, dayUTC string) error {
	if agentVaultID == "" {
		return nil
	}
	if !AllowedAgentVaultVenue(venue) {
		return errors.New("unsupported agent vault venue")
	}
	p, ok := v.ledger.agentVaults[agentVaultID]
	if !ok {
		return errors.New("unknown agent vault")
	}
	if p.Revoked {
		return errors.New("agent vault is revoked")
	}
	if nowUnix >= p.ExpiresAt {
		return errors.New("agent vault expired")
	}
	if !slices.Contains(p.AllowedVenues, venue) {
		return fmt.Errorf("agent vault forbids %s venue", venue)
	}
	if !slices.Contains(p.AllowedAssets, asset) {
		return errors.New("agent vault forbids asset")
	}
	if leverage > 0 && leverage > p.MaxLeverage {
		return errors.New("agent vault leverage limit exceeded")
	}
	if v.mainnetCappedEnabled {
		if v.mainnetCappedPolicy.MaxOrderMicroUSD == 0 || p.MaxOrderMicroUSD > MainnetCappedHardMaxOrderMicroUSD || p.DailySpendMicroUSD > MainnetCappedHardMaxDailyMicroUSD || p.DailyLossMicroUSD == 0 {
			return errors.New("agent vault exceeds mainnet-capped ceiling")
		}
		if notional > 0 && notional > v.mainnetCappedPolicy.MaxOrderMicroUSD {
			return errors.New("mainnet-capped per-order spend limit exceeded")
		}
	}
	if notional > 0 && notional > p.MaxOrderMicroUSD {
		return errors.New("agent vault per-order spend limit exceeded")
	}
	if notional > 0 && !reduceOnly {
		var spent, loss uint64
		if m := v.ledger.agentDailySpend[agentVaultID]; m != nil {
			spent = m[dayUTC]
		}
		if m := v.ledger.agentDailyLoss[agentVaultID]; m != nil {
			loss = m[dayUTC]
		}
		if loss >= p.DailyLossMicroUSD {
			return errors.New("agent vault daily loss circuit breaker tripped")
		}
		dailyCap := p.DailySpendMicroUSD
		if v.mainnetCappedEnabled && v.mainnetCappedPolicy.MaxDailyMicroUSD > 0 && v.mainnetCappedPolicy.MaxDailyMicroUSD < dailyCap {
			dailyCap = v.mainnetCappedPolicy.MaxDailyMicroUSD
		}
		if ^uint64(0)-spent < notional || spent+notional > dailyCap {
			return errors.New("agent vault daily spend limit exceeded")
		}
	}
	return nil
}

// SignHyperliquidUpdateLeverageIPC signs an updateLeverage intent using signer-persisted
// policy and optional typed agent vault constraints (ignoring any caller-supplied policy).
func (v *Vault) SignHyperliquidUpdateLeverageIPC(intent HyperliquidUpdateLeverageIntent, agentVaultID string) (*HyperliquidSignedAction, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, err := v.requireControlledHyperliquidUnlockedLocked(); err != nil {
		return nil, err
	}
	if v.ledger.hlPolicy == nil {
		return nil, errors.New("hyperliquid policy not configured on signer")
	}
	nowTime := v.now()
	if err := v.validateAgentVaultForVenueLocked(agentVaultID, "hyperliquid", intent.Asset, intent.Leverage, 0, false, nowTime.Unix(), nowTime.UTC().Format("2006-01-02")); err != nil {
		return nil, err
	}
	return v.signHyperliquidUpdateLeverageLocked(intent, clone(*v.ledger.hlPolicy))
}

// SignHyperliquidOrderIPC validates and signs a Hyperliquid order against signer-persisted
// policy and optional typed agent vault spend/loss limits, journaling both nonce and spend.
func (v *Vault) SignHyperliquidOrderIPC(intent HyperliquidOrderIntent, agentVaultID string) (*HyperliquidSignedAction, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	st, err := v.requireControlledHyperliquidUnlockedLocked()
	if err != nil {
		return nil, err
	}
	if v.ledger.hlPolicy == nil {
		return nil, errors.New("hyperliquid policy not configured on signer")
	}
	policy := clone(*v.ledger.hlPolicy)

	hyperliquidVaultMu.Lock()
	lastNonce := st.lastNonce
	hyperliquidVaultMu.Unlock()
	if v.ledger.hlLastNonce > lastNonce {
		lastNonce = v.ledger.hlLastNonce
	}
	nowTime := v.now()
	notional, err := ValidateHyperliquidOrderIntentForProfile(intent, policy, lastNonce, nowTime.Unix(), v.mainnetCappedEnabled, v.mainnetCappedPolicy.MaxOrderMicroUSD)
	if err != nil {
		return nil, err
	}
	dayUTC := nowTime.UTC().Format("2006-01-02")
	if err := v.validateAgentVaultForVenueLocked(agentVaultID, "hyperliquid", intent.Asset, intent.Leverage, notional, intent.ReduceOnly, nowTime.Unix(), dayUTC); err != nil {
		return nil, err
	}

	var spendRec *AgentVaultSpendRecord
	if agentVaultID != "" {
		spendRec = &AgentVaultSpendRecord{
			VaultID:       agentVaultID,
			OperationID:   fmt.Sprintf("hl-order-%d", intent.Nonce),
			Venue:         "hyperliquid",
			Asset:         intent.Asset,
			DayUTC:        dayUTC,
			SpendMicroUSD: notional,
		}
		if err := ValidateAgentVaultSpendRecord(*spendRec); err != nil {
			return nil, err
		}
		if v.ledger.agentVaultOps[spendRec.OperationID] {
			return nil, errors.New("duplicate agent vault operation identity")
		}
	}

	signed, err := v.signHyperliquidOrderLocked(intent, policy)
	if err != nil {
		return nil, err
	}
	if spendRec != nil {
		if err := v.ledger.append(v, journalRecord{
			Kind:       "agent-vault-spend",
			AgentSpend: spendRec,
		}); err != nil {
			return nil, err
		}
	}
	return signed, nil
}

// SignHyperliquidCancelIPC signs a Hyperliquid cancel intent against signer-persisted policy
// and journals the monotonically increasing nonce into operations.log.
func (v *Vault) SignHyperliquidCancelIPC(intent HyperliquidCancelIntent) (*HyperliquidSignedAction, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, err := v.requireControlledHyperliquidUnlockedLocked(); err != nil {
		return nil, err
	}
	if v.ledger.hlPolicy == nil {
		return nil, errors.New("hyperliquid policy not configured on signer")
	}
	return v.signHyperliquidCancelLocked(intent, clone(*v.ledger.hlPolicy))
}
