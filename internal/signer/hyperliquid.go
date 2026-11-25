package signer

import (
	"encoding/hex"
	"errors"
	"math/big"
	"slices"
	"strings"
	"sync"
)

const (
	// HyperliquidActivationEnabled remains false until explicit owner Live-Trading authorization.
	HyperliquidActivationEnabled = false

	// HyperliquidMaxHardLeverage bounds maximum leverage any policy may permit.
	HyperliquidMaxHardLeverage uint32 = 20

	// HyperliquidMaxHardNotionalMicroUSD bounds single-order notional to $100,000.00.
	HyperliquidMaxHardNotionalMicroUSD uint64 = 100_000_000_000

	hyperliquidDomainTag = "hyperliquid:v1"
)

type hyperliquidVaultState struct {
	enabled   bool
	lastNonce uint64
}

var (
	hyperliquidVaultMu           sync.Mutex
	hyperliquidVaultStates       = map[*Vault]*hyperliquidVaultState{}
	hyperliquidJournalFloor      func(*Vault) uint64
	recordHyperliquidJournalSign func(*Vault, string, uint64, string) error
)

// HyperliquidPolicy bounds controlled Hyperliquid L1 order signing inside internal/signer.
type HyperliquidPolicy struct {
	Version                   int      `json:"version"`
	Network                   string   `json:"network"`
	AllowedAssets             []string `json:"allowedAssets"`
	MaxLeverage               uint32   `json:"maxLeverage"`
	MaxNotionalMicroUSD       uint64   `json:"maxNotionalMicroUsd"`
	MaxOrderPriceBpsDeviation uint32   `json:"maxOrderPriceBpsDeviation"`
	VaultAddress              string   `json:"vaultAddress,omitempty"`
	ExpiresAt                 int64    `json:"expiresAt"`
}

// HyperliquidOrderIntent represents a single bounded limit order intent for EIP-712 signing.
type HyperliquidOrderIntent struct {
	Network            string `json:"network"`
	Asset              string `json:"asset"`
	AssetIndex         uint32 `json:"assetIndex"`
	IsBuy              bool   `json:"isBuy"`
	LimitPrice         string `json:"limitPrice"`
	Size               string `json:"size"`
	ReferenceMarkPrice string `json:"referenceMarkPrice"`
	Leverage           uint32 `json:"leverage"`
	IsCross            bool   `json:"isCross,omitempty"`
	ReduceOnly         bool   `json:"reduceOnly"`
	TimeInForce        string `json:"timeInForce"`
	Grouping           string `json:"grouping"`
	ClientOrderID      string `json:"clientOrderId,omitempty"`
	Nonce              uint64 `json:"nonce"`
	ExpiresAt          int64  `json:"expiresAt"`
}

// HyperliquidUpdateLeverageIntent represents an explicit leverage & margin-mode
// configuration action signed before order submission.
type HyperliquidUpdateLeverageIntent struct {
	Network    string `json:"network"`
	Asset      string `json:"asset"`
	AssetIndex uint32 `json:"assetIndex"`
	Leverage   uint32 `json:"leverage"`
	IsCross    bool   `json:"isCross"`
	Nonce      uint64 `json:"nonce"`
	ExpiresAt  int64  `json:"expiresAt"`
}

// HyperliquidCancelIntent represents a single cancel-by-oid intent for EIP-712 signing.
type HyperliquidCancelIntent struct {
	Network    string `json:"network"`
	Asset      string `json:"asset"`
	AssetIndex uint32 `json:"assetIndex"`
	OrderID    uint64 `json:"orderId"`
	Nonce      uint64 `json:"nonce"`
	ExpiresAt  int64  `json:"expiresAt"`
}

// HyperliquidSignedAction holds the EIP-712 signature and canonical digests returned by Vault.
type HyperliquidSignedAction struct {
	Network          string `json:"network"`
	SignerAddress    string `json:"signerAddress"`
	Nonce            uint64 `json:"nonce"`
	ConnectionID     string `json:"connectionId"`
	TypedDataDigest  string `json:"typedDataDigest"`
	SignatureHex     string `json:"signatureHex"`
	SignatureR       string `json:"r"`
	SignatureS       string `json:"s"`
	SignatureV       uint8  `json:"v"`
	NotionalMicroUSD uint64 `json:"notionalMicroUsd,omitempty"`
}

// ParsePositiveDecimal parses an unsigned decimal string with at most 8 fractional
// digits into a scaled integer at 10^8 fixed precision.
func ParsePositiveDecimal(raw string) (*big.Int, error) {
	if raw == "" || len(raw) > 32 {
		return nil, errors.New("decimal value out of bounds")
	}
	dot := strings.IndexByte(raw, '.')
	intPart, fracPart := raw, ""
	if dot >= 0 {
		intPart = raw[:dot]
		fracPart = raw[dot+1:]
		if intPart == "" || fracPart == "" || strings.ContainsRune(fracPart, '.') {
			return nil, errors.New("malformed decimal value")
		}
		if len(fracPart) > 8 {
			return nil, errors.New("decimal precision exceeds 8 fractional digits")
		}
	}
	if len(intPart) > 1 && intPart[0] == '0' {
		return nil, errors.New("decimal integer cannot have leading zeros")
	}
	for i := 0; i < len(intPart); i++ {
		if intPart[i] < '0' || intPart[i] > '9' {
			return nil, errors.New("decimal contains non-digit")
		}
	}
	for i := 0; i < len(fracPart); i++ {
		if fracPart[i] < '0' || fracPart[i] > '9' {
			return nil, errors.New("decimal contains non-digit")
		}
	}
	paddedFrac := fracPart + strings.Repeat("0", 8-len(fracPart))
	combined := intPart + paddedFrac
	v, ok := new(big.Int).SetString(combined, 10)
	if !ok || v.Sign() <= 0 {
		return nil, errors.New("decimal must be strictly positive")
	}
	return v, nil
}

// NormalizeHyperliquidDecimal strips redundant trailing fractional zeros to match
// Hyperliquid's wire float_to_wire formatting.
func NormalizeHyperliquidDecimal(raw string) (string, error) {
	if _, err := ParsePositiveDecimal(raw); err != nil {
		return "", err
	}
	dot := strings.IndexByte(raw, '.')
	if dot < 0 {
		return raw, nil
	}
	intPart := raw[:dot]
	fracPart := strings.TrimRight(raw[dot+1:], "0")
	if fracPart == "" {
		return intPart, nil
	}
	return intPart + "." + fracPart, nil
}

// ComputeNotionalMicroUSD computes floor(price * size * 10^6) using exact big-integer arithmetic.
func ComputeNotionalMicroUSD(priceRaw, sizeRaw string) (uint64, error) {
	px, err := ParsePositiveDecimal(priceRaw)
	if err != nil {
		return 0, err
	}
	sz, err := ParsePositiveDecimal(sizeRaw)
	if err != nil {
		return 0, err
	}
	// px and sz are both scaled by 10^8, so product is scaled by 10^16.
	// Micro-USD is scaled by 10^6 -> divide by 10^10.
	prod := new(big.Int).Mul(px, sz)
	denom := new(big.Int).SetUint64(10_000_000_000)
	prod.Quo(prod, denom)
	if !prod.IsUint64() {
		return 0, errors.New("order notional overflows uint64 micro-USD")
	}
	n := prod.Uint64()
	if n == 0 {
		return 0, errors.New("order notional rounds below 1 micro-USD")
	}
	return n, nil
}

// PriceDeviationBps returns |limitPrice - markPrice| * 10000 / markPrice using exact integer math.
func PriceDeviationBps(limitRaw, markRaw string) (uint64, bool, error) {
	limit, err := ParsePositiveDecimal(limitRaw)
	if err != nil {
		return 0, false, err
	}
	mark, err := ParsePositiveDecimal(markRaw)
	if err != nil {
		return 0, false, err
	}
	diff := new(big.Int).Sub(limit, mark)
	if diff.Sign() < 0 {
		diff.Neg(diff)
	}
	scaled := new(big.Int).Mul(diff, big.NewInt(10000))
	q, rem := new(big.Int).QuoRem(scaled, mark, new(big.Int))
	if !q.IsUint64() {
		return 0, true, errors.New("price deviation overflows")
	}
	return q.Uint64(), rem.Sign() > 0, nil
}

func validHyperliquidAssetSymbol(sym string) bool {
	if len(sym) == 0 || len(sym) > 16 {
		return false
	}
	for i := 0; i < len(sym); i++ {
		c := sym[i]
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func validHyperliquidCloid(cloid string) bool {
	if cloid == "" {
		return true
	}
	if !strings.HasPrefix(cloid, "0x") || len(cloid) != 34 {
		return false
	}
	_, err := hex.DecodeString(cloid[2:])
	return err == nil
}

// ValidateHyperliquidPolicy enforces hard signer bounds on asset universe, leverage,
// notional USD, price collar, optional vault address, and expiry.
func ValidateHyperliquidPolicy(p HyperliquidPolicy, now int64) error {
	if p.Version != 1 {
		return errors.New("unsupported Hyperliquid policy version")
	}
	if p.Network != "testnet" && p.Network != "mainnet" {
		return errors.New("Hyperliquid policy network must be testnet or mainnet")
	}
	if p.Network == "mainnet" && !HyperliquidActivationEnabled {
		return ErrUnavailable
	}
	if len(p.AllowedAssets) == 0 || len(p.AllowedAssets) > 32 {
		return errors.New("Hyperliquid policy requires 1..32 allowlisted assets")
	}
	seen := map[string]bool{}
	for _, a := range p.AllowedAssets {
		if !validHyperliquidAssetSymbol(a) || seen[a] {
			return errors.New("invalid or duplicate asset in Hyperliquid policy")
		}
		seen[a] = true
	}
	if p.MaxLeverage == 0 || p.MaxLeverage > HyperliquidMaxHardLeverage {
		return errors.New("Hyperliquid leverage exceeds signer cap")
	}
	if p.MaxNotionalMicroUSD < 1_000_000 || p.MaxNotionalMicroUSD > HyperliquidMaxHardNotionalMicroUSD {
		return errors.New("Hyperliquid notional cap out of bounds")
	}
	if p.MaxOrderPriceBpsDeviation == 0 || p.MaxOrderPriceBpsDeviation > 5000 {
		return errors.New("Hyperliquid price collar must be within 1..5000 bps")
	}
	if p.VaultAddress != "" {
		if _, err := ValidateEthereumAddress(p.VaultAddress); err != nil {
			return err
		}
	}
	if p.ExpiresAt <= now {
		return errors.New("Hyperliquid policy expired")
	}
	return nil
}

// ValidateHyperliquidOrderIntent validates an order intent against policy bounds,
// fat-finger price collar, monotonic nonce, TIF, cloid, and expiry.
func ValidateHyperliquidOrderIntent(intent HyperliquidOrderIntent, policy HyperliquidPolicy, lastNonce uint64, now int64) (uint64, error) {
	if err := ValidateHyperliquidPolicy(policy, now); err != nil {
		return 0, err
	}
	if intent.Network != policy.Network {
		return 0, errors.New("Hyperliquid order network mismatch")
	}
	if !slices.Contains(policy.AllowedAssets, intent.Asset) {
		return 0, errors.New("Hyperliquid asset not allowlisted")
	}
	if intent.AssetIndex > 10000 {
		return 0, errors.New("Hyperliquid asset index out of range")
	}
	if intent.Leverage == 0 || intent.Leverage > policy.MaxLeverage {
		return 0, errors.New("Hyperliquid leverage exceeds policy")
	}
	if intent.TimeInForce != "Gtc" && intent.TimeInForce != "Ioc" && intent.TimeInForce != "Alo" {
		return 0, errors.New("invalid Hyperliquid timeInForce")
	}
	if intent.Grouping != "" && intent.Grouping != "na" {
		return 0, errors.New("unsupported Hyperliquid order grouping")
	}
	if !validHyperliquidCloid(intent.ClientOrderID) {
		return 0, errors.New("invalid Hyperliquid clientOrderId")
	}
	if intent.Nonce == 0 || intent.Nonce <= lastNonce {
		return 0, errors.New("Hyperliquid order nonce must be strictly increasing")
	}
	if intent.ExpiresAt <= now || intent.ExpiresAt > policy.ExpiresAt {
		return 0, errors.New("Hyperliquid order intent expired")
	}
	notional, err := ComputeNotionalMicroUSD(intent.LimitPrice, intent.Size)
	if err != nil {
		return 0, err
	}
	if notional > policy.MaxNotionalMicroUSD {
		return 0, errors.New("Hyperliquid order notional exceeds policy cap")
	}
	bps, hasRem, err := PriceDeviationBps(intent.LimitPrice, intent.ReferenceMarkPrice)
	if err != nil {
		return 0, err
	}
	limitBps := uint64(policy.MaxOrderPriceBpsDeviation)
	if bps > limitBps || (bps == limitBps && hasRem) {
		return 0, errors.New("Hyperliquid limit price deviates beyond fat-finger collar")
	}
	return notional, nil
}

// ValidateHyperliquidUpdateLeverageIntent validates leverage/margin-mode configuration against policy and nonce.
func ValidateHyperliquidUpdateLeverageIntent(intent HyperliquidUpdateLeverageIntent, policy HyperliquidPolicy, lastNonce uint64, now int64) error {
	if err := ValidateHyperliquidPolicy(policy, now); err != nil {
		return err
	}
	if intent.Network != policy.Network {
		return errors.New("Hyperliquid updateLeverage network mismatch")
	}
	if !slices.Contains(policy.AllowedAssets, intent.Asset) {
		return errors.New("Hyperliquid updateLeverage asset not allowlisted")
	}
	if intent.AssetIndex > 10000 {
		return errors.New("Hyperliquid asset index out of range")
	}
	if intent.Leverage == 0 || intent.Leverage > policy.MaxLeverage {
		return errors.New("Hyperliquid leverage exceeds policy")
	}
	if intent.Nonce == 0 || intent.Nonce <= lastNonce {
		return errors.New("Hyperliquid updateLeverage nonce must be strictly increasing")
	}
	if intent.ExpiresAt <= now || intent.ExpiresAt > policy.ExpiresAt {
		return errors.New("Hyperliquid updateLeverage intent expired")
	}
	return nil
}

// ValidateHyperliquidCancelIntent validates a cancel-by-oid intent against policy and nonce.
func ValidateHyperliquidCancelIntent(intent HyperliquidCancelIntent, policy HyperliquidPolicy, lastNonce uint64, now int64) error {
	if err := ValidateHyperliquidPolicy(policy, now); err != nil {
		return err
	}
	if intent.Network != policy.Network {
		return errors.New("Hyperliquid cancel network mismatch")
	}
	if !slices.Contains(policy.AllowedAssets, intent.Asset) {
		return errors.New("Hyperliquid cancel asset not allowlisted")
	}
	if intent.AssetIndex > 10000 || intent.OrderID == 0 {
		return errors.New("Hyperliquid cancel requires valid asset index and orderId")
	}
	if intent.Nonce == 0 || intent.Nonce <= lastNonce {
		return errors.New("Hyperliquid cancel nonce must be strictly increasing")
	}
	if intent.ExpiresAt <= now || intent.ExpiresAt > policy.ExpiresAt {
		return errors.New("Hyperliquid cancel intent expired")
	}
	return nil
}

func (v *Vault) clearHyperliquidState() {
	hyperliquidVaultMu.Lock()
	delete(hyperliquidVaultStates, v)
	hyperliquidVaultMu.Unlock()
}

func (v *Vault) hyperliquidState() (*hyperliquidVaultState, bool) {
	hyperliquidVaultMu.Lock()
	defer hyperliquidVaultMu.Unlock()
	st, ok := hyperliquidVaultStates[v]
	return st, ok
}

// ConfigureHyperliquidControlled enables or clears controlled-fixture Hyperliquid
// EIP-712 signing on an in-process Vault. Ordinary startup leaves this OFF.
func (v *Vault) ConfigureHyperliquidControlled(enabled bool) error {
	if v == nil {
		return ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed && enabled {
		return ErrUnavailable
	}
	v.setHyperliquidControlledLocked(enabled)
	return nil
}

func (v *Vault) setHyperliquidControlledLocked(enabled bool) {
	hyperliquidVaultMu.Lock()
	defer hyperliquidVaultMu.Unlock()
	if !enabled {
		delete(hyperliquidVaultStates, v)
		return
	}
	if existing, ok := hyperliquidVaultStates[v]; ok {
		existing.enabled = true
		return
	}
	hyperliquidVaultStates[v] = &hyperliquidVaultState{enabled: true}
}

// HyperliquidAddress derives and returns the domain-separated EIP-55 Ethereum
// address for this Vault while unlocked, leaving no key material behind.
func (v *Vault) HyperliquidAddress() (string, error) {
	if v == nil {
		return "", ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return "", ErrUnavailable
	}
	st, ok := v.hyperliquidState()
	if !ok || !st.enabled {
		return "", ErrUnavailable
	}
	if !v.unlocked() {
		return "", ErrLocked
	}
	priv, err := DeriveSecp256k1KeyFromSeed(v.seed, hyperliquidDomainTag)
	if err != nil {
		return "", err
	}
	defer wipe(priv)
	pub, _, err := Secp256k1PublicKey(priv)
	if err != nil {
		return "", err
	}
	return EthereumAddressFromPublicKey(pub)
}

func (v *Vault) signHyperliquidActionLocked(network, vaultAddress string, actionMsgpack []byte, nonce, notional uint64) (*HyperliquidSignedAction, error) {
	if !v.unlocked() {
		return nil, ErrLocked
	}
	if network == "mainnet" && !HyperliquidActivationEnabled {
		return nil, ErrUnavailable
	}
	connectionID, err := HyperliquidActionConnectionID(actionMsgpack, nonce, vaultAddress)
	if err != nil {
		return nil, err
	}
	digest, err := HyperliquidAgentDigest(network, connectionID)
	if err != nil {
		return nil, err
	}
	priv, err := DeriveSecp256k1KeyFromSeed(v.seed, hyperliquidDomainTag)
	if err != nil {
		return nil, err
	}
	defer wipe(priv)
	pub, _, err := Secp256k1PublicKey(priv)
	if err != nil {
		return nil, err
	}
	signerAddr, err := EthereumAddressFromPublicKey(pub)
	if err != nil {
		return nil, err
	}
	sig, err := SignSecp256k1Digest(priv, digest)
	if err != nil {
		return nil, err
	}
	if err := VerifySecp256k1Signature(signerAddr, digest, sig); err != nil {
		return nil, err
	}
	return &HyperliquidSignedAction{
		Network:          network,
		SignerAddress:    signerAddr,
		Nonce:            nonce,
		ConnectionID:     "0x" + hex.EncodeToString(connectionID[:]),
		TypedDataDigest:  "0x" + hex.EncodeToString(digest[:]),
		SignatureHex:     sig.Hex(),
		SignatureR:       sig.RHex(),
		SignatureS:       sig.SHex(),
		SignatureV:       sig.V,
		NotionalMicroUSD: notional,
	}, nil
}

// SignHyperliquidOrder validates the policy & order intent, enforces monotonic
// nonce progression, and signs the Hyperliquid Exchange Agent EIP-712 digest.
func (v *Vault) SignHyperliquidOrder(intent HyperliquidOrderIntent, policy HyperliquidPolicy) (*HyperliquidSignedAction, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.signHyperliquidOrderLocked(intent, policy)
}

func (v *Vault) signHyperliquidOrderLocked(intent HyperliquidOrderIntent, policy HyperliquidPolicy) (*HyperliquidSignedAction, error) {
	if v.closed {
		return nil, ErrUnavailable
	}
	st, ok := v.hyperliquidState()
	if !ok || !st.enabled {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	hyperliquidVaultMu.Lock()
	lastNonce := st.lastNonce
	hyperliquidVaultMu.Unlock()
	if hyperliquidJournalFloor != nil {
		if floor := hyperliquidJournalFloor(v); floor > lastNonce {
			lastNonce = floor
		}
	}

	notional, err := ValidateHyperliquidOrderIntent(intent, policy, lastNonce, v.now().Unix())
	if err != nil {
		return nil, err
	}
	actionMsgpack, err := EncodeHyperliquidOrderAction(intent)
	if err != nil {
		return nil, err
	}
	signed, err := v.signHyperliquidActionLocked(intent.Network, policy.VaultAddress, actionMsgpack, intent.Nonce, notional)
	if err != nil {
		return nil, err
	}
	if recordHyperliquidJournalSign != nil {
		if jErr := recordHyperliquidJournalSign(v, "order", intent.Nonce, signed.TypedDataDigest); jErr != nil {
			return nil, jErr
		}
	}
	hyperliquidVaultMu.Lock()
	if cur, exists := hyperliquidVaultStates[v]; exists && cur == st && intent.Nonce > cur.lastNonce {
		cur.lastNonce = intent.Nonce
	}
	hyperliquidVaultMu.Unlock()
	return signed, nil
}

// SignHyperliquidUpdateLeverage validates leverage bounds against policy, advances nonce,
// and signs the Hyperliquid Exchange Agent EIP-712 digest for an updateLeverage action.
func (v *Vault) SignHyperliquidUpdateLeverage(intent HyperliquidUpdateLeverageIntent, policy HyperliquidPolicy) (*HyperliquidSignedAction, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.signHyperliquidUpdateLeverageLocked(intent, policy)
}

func (v *Vault) signHyperliquidUpdateLeverageLocked(intent HyperliquidUpdateLeverageIntent, policy HyperliquidPolicy) (*HyperliquidSignedAction, error) {
	if v.closed {
		return nil, ErrUnavailable
	}
	st, ok := v.hyperliquidState()
	if !ok || !st.enabled {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	hyperliquidVaultMu.Lock()
	lastNonce := st.lastNonce
	hyperliquidVaultMu.Unlock()
	if hyperliquidJournalFloor != nil {
		if floor := hyperliquidJournalFloor(v); floor > lastNonce {
			lastNonce = floor
		}
	}

	if err := ValidateHyperliquidUpdateLeverageIntent(intent, policy, lastNonce, v.now().Unix()); err != nil {
		return nil, err
	}
	actionMsgpack, err := EncodeHyperliquidUpdateLeverageAction(intent)
	if err != nil {
		return nil, err
	}
	signed, err := v.signHyperliquidActionLocked(intent.Network, policy.VaultAddress, actionMsgpack, intent.Nonce, 0)
	if err != nil {
		return nil, err
	}
	if recordHyperliquidJournalSign != nil {
		if jErr := recordHyperliquidJournalSign(v, "updateLeverage", intent.Nonce, signed.TypedDataDigest); jErr != nil {
			return nil, jErr
		}
	}
	hyperliquidVaultMu.Lock()
	if cur, exists := hyperliquidVaultStates[v]; exists && cur == st && intent.Nonce > cur.lastNonce {
		cur.lastNonce = intent.Nonce
	}
	hyperliquidVaultMu.Unlock()
	return signed, nil
}

// SignHyperliquidCancel validates the policy & cancel intent, advances nonce,
// and signs the Hyperliquid Exchange Agent EIP-712 digest.
func (v *Vault) SignHyperliquidCancel(intent HyperliquidCancelIntent, policy HyperliquidPolicy) (*HyperliquidSignedAction, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.signHyperliquidCancelLocked(intent, policy)
}

func (v *Vault) signHyperliquidCancelLocked(intent HyperliquidCancelIntent, policy HyperliquidPolicy) (*HyperliquidSignedAction, error) {
	if v.closed {
		return nil, ErrUnavailable
	}
	st, ok := v.hyperliquidState()
	if !ok || !st.enabled {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	hyperliquidVaultMu.Lock()
	lastNonce := st.lastNonce
	hyperliquidVaultMu.Unlock()
	if hyperliquidJournalFloor != nil {
		if floor := hyperliquidJournalFloor(v); floor > lastNonce {
			lastNonce = floor
		}
	}

	if err := ValidateHyperliquidCancelIntent(intent, policy, lastNonce, v.now().Unix()); err != nil {
		return nil, err
	}
	actionMsgpack, err := EncodeHyperliquidCancelAction(intent)
	if err != nil {
		return nil, err
	}
	signed, err := v.signHyperliquidActionLocked(intent.Network, policy.VaultAddress, actionMsgpack, intent.Nonce, 0)
	if err != nil {
		return nil, err
	}
	if recordHyperliquidJournalSign != nil {
		if jErr := recordHyperliquidJournalSign(v, "cancel", intent.Nonce, signed.TypedDataDigest); jErr != nil {
			return nil, jErr
		}
	}
	hyperliquidVaultMu.Lock()
	if cur, exists := hyperliquidVaultStates[v]; exists && cur == st && intent.Nonce > cur.lastNonce {
		cur.lastNonce = intent.Nonce
	}
	hyperliquidVaultMu.Unlock()
	return signed, nil
}
