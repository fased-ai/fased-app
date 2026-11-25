package signer

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
)

const (
	// PolymarketActivationEnabled remains false until explicit owner Live Mainnet authorization.
	PolymarketActivationEnabled = false

	// KalshiActivationEnabled remains false until explicit owner Live Production authorization.
	KalshiActivationEnabled = false

	// PolymarketAmoyChainID identifies Polygon Amoy Testnet for Stage 4 rehearsal.
	PolymarketAmoyChainID uint64 = 80002

	// PolymarketMainnetChainID identifies Polygon Mainnet (locked while !PolymarketActivationEnabled).
	PolymarketMainnetChainID uint64 = 137

	// PolymarketAmoyExchangeContract is the canonical Amoy CTF Exchange verifyingContract.
	PolymarketAmoyExchangeContract = "0xdFE02Eb6733538f8Ea35D585af8DE5958AD99E40"

	// PolymarketAmoyNegRiskExchangeContract is the Amoy NegRisk CTF Exchange verifyingContract.
	PolymarketAmoyNegRiskExchangeContract = "0xC5d563A36AE78145C45a50134d48A1215220f80a"

	polymarketDomainTag = "polymarket:v1"
	kalshiDemoKeyFile   = "kalshi-demo-key.json"
)

var polymarketOrderTypeHash = Keccak256([]byte(
	"Order(uint256 salt,address maker,address signer,address taker,uint256 tokenId,uint256 makerAmount,uint256 takerAmount,uint256 expiration,uint256 nonce,uint256 feeRateBps,uint8 side,uint8 signatureType)",
))

// PolymarketStatus reports non-secret signer-owned Polymarket custody state.
type PolymarketStatus struct {
	Enabled                 bool   `json:"enabled"`
	Network                 string `json:"network"`
	ChainID                 uint64 `json:"chainId"`
	SignerAddress           string `json:"signerAddress,omitempty"`
	ExchangeContract        string `json:"exchangeContract"`
	NegRiskExchangeContract string `json:"negRiskExchangeContract"`
	MainnetLocked           bool   `json:"mainnetLocked"`
}

// PolymarketOrderIntent describes a bounded CTF Exchange V1 EIP-712 order for Stage 4 signing.
type PolymarketOrderIntent struct {
	Network         string `json:"network"`
	ChainID         uint64 `json:"chainId"`
	NegRisk         bool   `json:"negRisk,omitempty"`
	OperationID     string `json:"operationId"`
	Asset           string `json:"asset"`
	Salt            string `json:"salt"`
	Maker           string `json:"maker"`
	Signer          string `json:"signer"`
	Taker           string `json:"taker"`
	TokenID         string `json:"tokenId"`
	MakerAmount     string `json:"makerAmount"`
	TakerAmount     string `json:"takerAmount"`
	Expiration      uint64 `json:"expiration"`
	Nonce           uint64 `json:"nonce"`
	FeeRateBps      uint64 `json:"feeRateBps"`
	Side            uint8  `json:"side"` // 0 = BUY, 1 = SELL
	SignatureType   uint8  `json:"signatureType"`
	ReduceOnly      bool   `json:"reduceOnly,omitempty"`
	IntentExpiresAt int64  `json:"intentExpiresAt"`
}

// PolymarketSignedOrder returns canonical EIP-712 digests and low-S ECDSA signature.
type PolymarketSignedOrder struct {
	Network           string `json:"network"`
	ChainID           uint64 `json:"chainId"`
	VerifyingContract string `json:"verifyingContract"`
	SignerAddress     string `json:"signerAddress"`
	MakerAddress      string `json:"makerAddress"`
	TakerAddress      string `json:"takerAddress"`
	DomainSeparator   string `json:"domainSeparator"`
	StructHash        string `json:"structHash"`
	TypedDataDigest   string `json:"typedDataDigest"`
	SignatureHex      string `json:"signatureHex"`
	NotionalMicroUSD  uint64 `json:"notionalMicroUsd"`
	OperationID       string `json:"operationId"`
}

// KalshiStatus reports non-secret Kalshi Demo RSA-PSS signing readiness.
type KalshiStatus struct {
	Enabled         bool   `json:"enabled"`
	Environment     string `json:"environment"`
	Configured      bool   `json:"configured"`
	KeyID           string `json:"keyId,omitempty"`
	PublicKeyPEM    string `json:"publicKeyPem,omitempty"`
	PublicKeySHA256 string `json:"publicKeySha256,omitempty"`
	MainnetLocked   bool   `json:"mainnetLocked"`
}

// KalshiKeyProvision installs an owner-supplied RSA-2048+ private key PEM sealed under Vault custody.
type KalshiKeyProvision struct {
	Environment   string `json:"environment"`
	KeyID         string `json:"keyId"`
	PrivateKeyPEM []byte `json:"privateKeyPem"`
}

// KalshiSignIntent describes a bounded Kalshi Demo Trade API v2 request to sign via RSA-PSS SHA-256.
type KalshiSignIntent struct {
	Environment       string `json:"environment"`
	Method            string `json:"method"`
	Path              string `json:"path"`
	TimestampMs       int64  `json:"timestampMs"`
	Ticker            string `json:"ticker,omitempty"`
	Asset             string `json:"asset,omitempty"`
	OperationID       string `json:"operationId"`
	EstimatedMicroUSD uint64 `json:"estimatedMicroUsd,omitempty"`
	ReduceOnly        bool   `json:"reduceOnly,omitempty"`
	ExpiresAt         int64  `json:"expiresAt"`
}

// KalshiSignedRequest returns canonical RSA-PSS signature metadata (never private key material).
type KalshiSignedRequest struct {
	Environment       string `json:"environment"`
	KeyID             string `json:"keyId"`
	TimestampMs       int64  `json:"timestampMs"`
	Method            string `json:"method"`
	Path              string `json:"path"`
	SignatureBase64   string `json:"signatureBase64"`
	PublicKeySHA256   string `json:"publicKeySha256"`
	OperationID       string `json:"operationId"`
	EstimatedMicroUSD uint64 `json:"estimatedMicroUsd,omitempty"`
}

type kalshiSealedKey struct {
	Version         int    `json:"version"`
	Environment     string `json:"environment"`
	KeyID           string `json:"keyId"`
	PublicKeyPEM    string `json:"publicKeyPem"`
	PublicKeySHA256 string `json:"publicKeySha256"`
	Nonce           []byte `json:"nonce"`
	Ciphertext      []byte `json:"ciphertext"`
}

func parseUint256Decimal(raw string) (*big.Int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || len(trimmed) > 80 {
		return nil, errors.New("uint256 value out of bounds")
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] < '0' || trimmed[i] > '9' {
			return nil, errors.New("uint256 must be unsigned base-10 digits")
		}
	}
	v, ok := new(big.Int).SetString(trimmed, 10)
	if !ok || v.Sign() < 0 || v.BitLen() > 256 {
		return nil, errors.New("uint256 overflow or invalid format")
	}
	return v, nil
}

func encodeABIWordBigInt(v *big.Int) [32]byte {
	var word [32]byte
	if v != nil && v.Sign() > 0 {
		v.FillBytes(word[:])
	}
	return word
}

func encodeABIWordUint64(v uint64) [32]byte {
	var word [32]byte
	for i := 0; i < 8; i++ {
		word[31-i] = byte(v >> (8 * i))
	}
	return word
}

func encodeABIWordAddress(canonAddr string) ([32]byte, error) {
	canon, err := ValidateEthereumAddress(canonAddr)
	if err != nil {
		return [32]byte{}, err
	}
	raw, err := hex.DecodeString(canon[2:])
	if err != nil || len(raw) != 20 {
		return [32]byte{}, errors.New("invalid Ethereum address")
	}
	var word [32]byte
	copy(word[12:], raw)
	return word, nil
}

// PolymarketOrderStructHash computes the EIP-712 structHash for a Polymarket CTF Exchange Order.
func PolymarketOrderStructHash(intent PolymarketOrderIntent) ([32]byte, error) {
	salt, err := parseUint256Decimal(intent.Salt)
	if err != nil {
		return [32]byte{}, fmt.Errorf("invalid polymarket order salt: %w", err)
	}
	tokenID, err := parseUint256Decimal(intent.TokenID)
	if err != nil {
		return [32]byte{}, fmt.Errorf("invalid polymarket order tokenId: %w", err)
	}
	makerAmount, err := parseUint256Decimal(intent.MakerAmount)
	if err != nil || makerAmount.Sign() <= 0 {
		return [32]byte{}, errors.New("polymarket makerAmount must be positive uint256")
	}
	takerAmount, err := parseUint256Decimal(intent.TakerAmount)
	if err != nil || takerAmount.Sign() <= 0 {
		return [32]byte{}, errors.New("polymarket takerAmount must be positive uint256")
	}
	if intent.Side > 1 {
		return [32]byte{}, errors.New("polymarket order side must be 0 (BUY) or 1 (SELL)")
	}
	if intent.SignatureType > 2 {
		return [32]byte{}, errors.New("unsupported polymarket signatureType")
	}
	makerWord, err := encodeABIWordAddress(intent.Maker)
	if err != nil {
		return [32]byte{}, err
	}
	signerWord, err := encodeABIWordAddress(intent.Signer)
	if err != nil {
		return [32]byte{}, err
	}
	takerWord, err := encodeABIWordAddress(intent.Taker)
	if err != nil {
		return [32]byte{}, err
	}

	var buf bytes.Buffer
	buf.Write(polymarketOrderTypeHash[:])
	wSalt := encodeABIWordBigInt(salt)
	buf.Write(wSalt[:])
	buf.Write(makerWord[:])
	buf.Write(signerWord[:])
	buf.Write(takerWord[:])
	wToken := encodeABIWordBigInt(tokenID)
	buf.Write(wToken[:])
	wMakerAmt := encodeABIWordBigInt(makerAmount)
	buf.Write(wMakerAmt[:])
	wTakerAmt := encodeABIWordBigInt(takerAmount)
	buf.Write(wTakerAmt[:])
	wExp := encodeABIWordUint64(intent.Expiration)
	buf.Write(wExp[:])
	wNonce := encodeABIWordUint64(intent.Nonce)
	buf.Write(wNonce[:])
	wFee := encodeABIWordUint64(intent.FeeRateBps)
	buf.Write(wFee[:])
	wSide := encodeABIWordUint64(uint64(intent.Side))
	buf.Write(wSide[:])
	wSigType := encodeABIWordUint64(uint64(intent.SignatureType))
	buf.Write(wSigType[:])
	return Keccak256(buf.Bytes()), nil
}

// PolymarketOrderDigest computes EIP-712 domainSeparator, structHash, digest, and pinned Exchange address.
func PolymarketOrderDigest(intent PolymarketOrderIntent) ([32]byte, [32]byte, [32]byte, string, error) {
	net := strings.ToLower(strings.TrimSpace(intent.Network))
	if net == "mainnet" || net == "polygon" || intent.ChainID == PolymarketMainnetChainID {
		if !PolymarketActivationEnabled {
			return [32]byte{}, [32]byte{}, [32]byte{}, "", ErrUnavailable
		}
	}
	if net != "amoy" {
		return [32]byte{}, [32]byte{}, [32]byte{}, "", errors.New("polymarket signing requires amoy testnet network")
	}
	chainID := intent.ChainID
	if chainID == 0 {
		chainID = PolymarketAmoyChainID
	}
	if chainID != PolymarketAmoyChainID {
		return [32]byte{}, [32]byte{}, [32]byte{}, "", errors.New("polymarket signing requires Polygon Amoy chainId 80002")
	}
	contract := PolymarketAmoyExchangeContract
	if intent.NegRisk {
		contract = PolymarketAmoyNegRiskExchangeContract
	}
	canonContract, err := ValidateEthereumAddress(strings.ToLower(contract))
	if err != nil {
		return [32]byte{}, [32]byte{}, [32]byte{}, "", err
	}
	domainSep, err := EIP712DomainSeparator(EIP712Domain{
		Name:              "Polymarket CTF Exchange",
		Version:           "1",
		ChainID:           big.NewInt(int64(chainID)),
		VerifyingContract: canonContract,
	})
	if err != nil {
		return [32]byte{}, [32]byte{}, [32]byte{}, "", err
	}
	structHash, err := PolymarketOrderStructHash(intent)
	if err != nil {
		return [32]byte{}, [32]byte{}, [32]byte{}, "", err
	}
	digest := EIP712Digest(domainSep, structHash)
	return domainSep, structHash, digest, canonContract, nil
}

// PolymarketStatus returns non-secret Stage 4 Polymarket testnet readiness and EIP-55 signer address.
func (v *Vault) PolymarketStatus() (*PolymarketStatus, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.testnetExecEnabled || !v.allowStage4HyperliquidVaultLocked() {
		return nil, ErrUnavailable
	}
	exContract, _ := ValidateEthereumAddress(strings.ToLower(PolymarketAmoyExchangeContract))
	negContract, _ := ValidateEthereumAddress(strings.ToLower(PolymarketAmoyNegRiskExchangeContract))
	st := &PolymarketStatus{
		Enabled:                 true,
		Network:                 "amoy",
		ChainID:                 PolymarketAmoyChainID,
		ExchangeContract:        exContract,
		NegRiskExchangeContract: negContract,
		MainnetLocked:           !PolymarketActivationEnabled,
	}
	if v.unlocked() {
		priv, err := DeriveSecp256k1KeyFromSeed(v.seed, polymarketDomainTag)
		if err != nil {
			return nil, err
		}
		defer wipe(priv)
		pub, _, err := Secp256k1PublicKey(priv)
		if err != nil {
			return nil, err
		}
		addr, err := EthereumAddressFromPublicKey(pub)
		if err != nil {
			return nil, err
		}
		st.SignerAddress = addr
	}
	return st, nil
}

// SignPolymarketOrderIPC validates and EIP-712 signs a Polymarket Amoy CTF Exchange order
// under typed AgentVaultPolicy custody limits and journals spend against operations.log.
func (v *Vault) SignPolymarketOrderIPC(intent PolymarketOrderIntent, agentVaultID string) (*PolymarketSignedOrder, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.testnetExecEnabled || !v.allowStage4HyperliquidVaultLocked() || v.ledger.main == nil {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	net := strings.ToLower(strings.TrimSpace(intent.Network))
	if net == "mainnet" || net == "polygon" || intent.ChainID == PolymarketMainnetChainID {
		return nil, ErrUnavailable
	}
	if net != "amoy" {
		return nil, errors.New("polymarket signing requires amoy testnet network")
	}
	if intent.ChainID == 0 {
		intent.ChainID = PolymarketAmoyChainID
	}
	if intent.ChainID != PolymarketAmoyChainID {
		return nil, errors.New("polymarket signing requires Polygon Amoy chainId 80002")
	}
	if strings.TrimSpace(agentVaultID) == "" {
		return nil, errors.New("polymarket execution requires a typed agent vault")
	}
	if intent.OperationID == "" || len(intent.OperationID) > 96 || strings.TrimSpace(intent.OperationID) != intent.OperationID {
		return nil, errors.New("invalid polymarket operationId")
	}
	if intent.Asset == "" || len(intent.Asset) > 24 || strings.ToUpper(strings.TrimSpace(intent.Asset)) != intent.Asset {
		return nil, errors.New("invalid polymarket asset symbol")
	}
	if intent.Side > 1 || intent.SignatureType > 2 || intent.FeeRateBps > 1000 {
		return nil, errors.New("invalid polymarket order parameters")
	}

	nowTime := v.now()
	nowUnix := nowTime.Unix()
	if intent.IntentExpiresAt <= nowUnix {
		return nil, errors.New("polymarket sign intent expired")
	}
	if intent.Expiration != 0 && intent.Expiration <= uint64(nowUnix) {
		return nil, errors.New("polymarket order expiration is in the past")
	}

	makerU, err := strconv.ParseUint(strings.TrimSpace(intent.MakerAmount), 10, 64)
	if err != nil || makerU == 0 {
		return nil, errors.New("invalid polymarket makerAmount")
	}
	takerU, err := strconv.ParseUint(strings.TrimSpace(intent.TakerAmount), 10, 64)
	if err != nil || takerU == 0 {
		return nil, errors.New("invalid polymarket takerAmount")
	}
	// Binary CTF probability must stay strictly within (0, 1):
	// BUY (side=0): collateral paid (makerAmount) < outcome shares received (takerAmount).
	// SELL (side=1): outcome shares sold (makerAmount) > collateral received (takerAmount).
	var notionalMicroUSD uint64
	if intent.Side == 0 {
		if makerU >= takerU {
			return nil, errors.New("polymarket BUY order implied probability must be below 1.0")
		}
		notionalMicroUSD = makerU
	} else {
		if takerU >= makerU {
			return nil, errors.New("polymarket SELL order implied probability must be below 1.0")
		}
		notionalMicroUSD = takerU
	}

	priv, err := DeriveSecp256k1KeyFromSeed(v.seed, polymarketDomainTag)
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
	if intent.Signer == "" {
		intent.Signer = signerAddr
	} else {
		canonSigner, err := ValidateEthereumAddress(intent.Signer)
		if err != nil || canonSigner != signerAddr {
			return nil, errors.New("polymarket order signer does not match vault address")
		}
		intent.Signer = canonSigner
	}
	if intent.Maker == "" {
		intent.Maker = signerAddr
	} else {
		canonMaker, err := ValidateEthereumAddress(intent.Maker)
		if err != nil || canonMaker != signerAddr {
			return nil, errors.New("polymarket order maker does not match vault address")
		}
		intent.Maker = canonMaker
	}
	if intent.Taker == "" {
		intent.Taker = "0x0000000000000000000000000000000000000000"
	} else {
		canonTaker, err := ValidateEthereumAddress(intent.Taker)
		if err != nil {
			return nil, err
		}
		intent.Taker = canonTaker
	}

	dayUTC := nowTime.UTC().Format("2006-01-02")
	if err := v.validateAgentVaultForVenueLocked(agentVaultID, "polymarket", intent.Asset, 0, notionalMicroUSD, intent.ReduceOnly, nowUnix, dayUTC); err != nil {
		return nil, err
	}
	spendRec := AgentVaultSpendRecord{
		VaultID:       agentVaultID,
		OperationID:   intent.OperationID,
		Venue:         "polymarket",
		Asset:         intent.Asset,
		DayUTC:        dayUTC,
		SpendMicroUSD: notionalMicroUSD,
	}
	if err := ValidateAgentVaultSpendRecord(spendRec); err != nil {
		return nil, err
	}
	if v.ledger.agentVaultOps[spendRec.OperationID] {
		return nil, errors.New("duplicate agent vault operation identity")
	}

	domainSep, structHash, digest, contract, err := PolymarketOrderDigest(intent)
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
	if err := v.ledger.append(v, journalRecord{
		Kind:       "agent-vault-spend",
		AgentSpend: &spendRec,
	}); err != nil {
		return nil, err
	}
	return &PolymarketSignedOrder{
		Network:           "amoy",
		ChainID:           intent.ChainID,
		VerifyingContract: contract,
		SignerAddress:     signerAddr,
		MakerAddress:      intent.Maker,
		TakerAddress:      intent.Taker,
		DomainSeparator:   "0x" + hex.EncodeToString(domainSep[:]),
		StructHash:        "0x" + hex.EncodeToString(structHash[:]),
		TypedDataDigest:   "0x" + hex.EncodeToString(digest[:]),
		SignatureHex:      sig.Hex(),
		NotionalMicroUSD:  notionalMicroUSD,
		OperationID:       intent.OperationID,
	}, nil
}

func validKalshiKeyID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func kalshiSealKeyFromSeed(seed []byte, signerID string) ([]byte, error) {
	if len(seed) != 32 || signerID == "" {
		return nil, ErrLocked
	}
	mac := hmac.New(sha256.New, seed)
	_, _ = mac.Write([]byte("fased-signer-kalshi-rsa-v1:"))
	_, _ = mac.Write([]byte(signerID))
	return mac.Sum(nil), nil
}

func kalshiSealAAD(signerID, keyID string) []byte {
	return []byte("fased-signer-kalshi-demo-v1:" + signerID + ":" + keyID)
}

func (v *Vault) loadKalshiEnvelopeLocked() (*kalshiSealedKey, error) {
	if v.store == nil {
		return nil, ErrUnavailable
	}
	raw, err := v.store.readPredictionKeyFile(kalshiDemoKeyFile)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, errAbsent) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var env kalshiSealedKey
	if decode(raw, &env) != nil {
		return nil, errors.New("damaged sealed kalshi demo key")
	}
	if env.Version != 1 || env.Environment != "demo" || !validKalshiKeyID(env.KeyID) || len(env.Nonce) != 12 || len(env.Ciphertext) < 32 {
		return nil, errors.New("invalid sealed kalshi demo key format")
	}
	return &env, nil
}

func (v *Vault) sealAndWriteKalshiKeyLocked(keyID string, priv *rsa.PrivateKey) (*KalshiStatus, error) {
	if v.store == nil {
		return nil, ErrUnavailable
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	defer wipe(pkcs8)

	spki, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: spki}))
	pubSum := sha256.Sum256(spki)
	pubSHA := hex.EncodeToString(pubSum[:])

	aesKey, err := kalshiSealKeyFromSeed(v.seed, v.signerID)
	if err != nil {
		return nil, err
	}
	defer clear(aesKey)

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	env := kalshiSealedKey{
		Version:         1,
		Environment:     "demo",
		KeyID:           keyID,
		PublicKeyPEM:    pubPEM,
		PublicKeySHA256: pubSHA,
		Nonce:           nonce,
		Ciphertext:      gcm.Seal(nil, nonce, pkcs8, kalshiSealAAD(v.signerID, keyID)),
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	if err := v.store.writePredictionKeyFile(kalshiDemoKeyFile, raw); err != nil {
		return nil, err
	}
	return &KalshiStatus{
		Enabled:         true,
		Environment:     "demo",
		Configured:      true,
		KeyID:           keyID,
		PublicKeyPEM:    pubPEM,
		PublicKeySHA256: pubSHA,
		MainnetLocked:   !KalshiActivationEnabled,
	}, nil
}

func (v *Vault) openKalshiPrivateKeyLocked() (*rsa.PrivateKey, string, string, error) {
	env, err := v.loadKalshiEnvelopeLocked()
	if err != nil {
		return nil, "", "", err
	}
	if env == nil {
		return nil, "", "", errors.New("kalshi demo RSA key is not configured on signer")
	}
	aesKey, err := kalshiSealKeyFromSeed(v.seed, v.signerID)
	if err != nil {
		return nil, "", "", err
	}
	defer clear(aesKey)

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, "", "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", "", err
	}
	pkcs8, err := gcm.Open(nil, env.Nonce, env.Ciphertext, kalshiSealAAD(v.signerID, env.KeyID))
	if err != nil {
		return nil, "", "", ErrCredential
	}
	defer wipe(pkcs8)

	parsed, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		return nil, "", "", err
	}
	rsaPriv, ok := parsed.(*rsa.PrivateKey)
	if !ok || rsaPriv.N.BitLen() < 2048 {
		return nil, "", "", errors.New("sealed kalshi key is not RSA-2048+")
	}
	return rsaPriv, env.KeyID, env.PublicKeySHA256, nil
}

// KalshiStatus returns non-secret Kalshi Demo RSA-PSS signing metadata without revealing key material.
func (v *Vault) KalshiStatus() (*KalshiStatus, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.testnetExecEnabled || !v.allowStage4HyperliquidVaultLocked() {
		return nil, ErrUnavailable
	}
	env, err := v.loadKalshiEnvelopeLocked()
	if err != nil {
		return nil, err
	}
	st := &KalshiStatus{
		Enabled:       true,
		Environment:   "demo",
		MainnetLocked: !KalshiActivationEnabled,
	}
	if env != nil {
		st.Configured = true
		st.KeyID = env.KeyID
		st.PublicKeyPEM = env.PublicKeyPEM
		st.PublicKeySHA256 = env.PublicKeySHA256
	}
	return st, nil
}

// GenerateKalshiDemoKey generates an RSA-2048 keypair inside the signer vault and seals it at 0600.
func (v *Vault) GenerateKalshiDemoKey(keyID string) (*KalshiStatus, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.testnetExecEnabled || !v.allowStage4HyperliquidVaultLocked() || v.ledger.main == nil {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	id := strings.TrimSpace(keyID)
	if id == "" {
		id = "demo-" + randomID()
	}
	if !validKalshiKeyID(id) {
		return nil, errors.New("invalid kalshi demo keyId")
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	defer func() {
		if priv.D != nil {
			priv.D.SetInt64(0)
		}
		for _, p := range priv.Primes {
			if p != nil {
				p.SetInt64(0)
			}
		}
	}()
	return v.sealAndWriteKalshiKeyLocked(id, priv)
}

// ProvisionKalshiDemoKey imports an owner-provided RSA-2048+ PEM key into sealed demo signer storage.
func (v *Vault) ProvisionKalshiDemoKey(req KalshiKeyProvision) (*KalshiStatus, error) {
	defer wipe(req.PrivateKeyPEM)
	if v == nil {
		return nil, ErrUnavailable
	}
	env := strings.ToLower(strings.TrimSpace(req.Environment))
	if env == "prod" || env == "production" || env == "mainnet" {
		return nil, ErrUnavailable
	}
	if env != "demo" {
		return nil, errors.New("kalshi key provisioning requires demo environment")
	}
	id := strings.TrimSpace(req.KeyID)
	if !validKalshiKeyID(id) {
		return nil, errors.New("invalid kalshi demo keyId")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.testnetExecEnabled || !v.allowStage4HyperliquidVaultLocked() || v.ledger.main == nil {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	block, _ := pem.Decode(req.PrivateKeyPEM)
	if block == nil {
		return nil, errors.New("invalid kalshi RSA private key PEM")
	}
	defer wipe(block.Bytes)

	var rsaPriv *rsa.PrivateKey
	switch block.Type {
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("invalid PKCS8 kalshi private key")
		}
		var ok bool
		rsaPriv, ok = parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("kalshi private key must be RSA")
		}
	case "RSA PRIVATE KEY":
		parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("invalid PKCS1 kalshi RSA private key")
		}
		rsaPriv = parsed
	default:
		return nil, errors.New("unsupported PEM block type for kalshi RSA key")
	}
	if rsaPriv.N.BitLen() < 2048 {
		return nil, errors.New("kalshi RSA key must be at least 2048 bits")
	}
	if err := rsaPriv.Validate(); err != nil {
		return nil, errors.New("invalid kalshi RSA key parameters")
	}
	defer func() {
		if rsaPriv.D != nil {
			rsaPriv.D.SetInt64(0)
		}
		for _, p := range rsaPriv.Primes {
			if p != nil {
				p.SetInt64(0)
			}
		}
	}()
	return v.sealAndWriteKalshiKeyLocked(id, rsaPriv)
}

func validateKalshiPathSegment(seg string) bool {
	if len(seg) == 0 || len(seg) > 96 {
		return false
	}
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return seg != "." && seg != ".."
}

func validateKalshiSignPath(method, path string) (isOrder bool, isCancel bool, err error) {
	if strings.ContainsAny(path, "?# \t\r\n") || strings.Contains(path, "..") {
		return false, false, errors.New("kalshi signed path must not contain query strings or whitespace")
	}
	switch {
	case method == "POST" && path == "/trade-api/v2/portfolio/events/orders":
		return true, false, nil
	case method == "DELETE" && strings.HasPrefix(path, "/trade-api/v2/portfolio/events/orders/"):
		orderID := strings.TrimPrefix(path, "/trade-api/v2/portfolio/events/orders/")
		if !validateKalshiPathSegment(orderID) {
			return false, false, errors.New("invalid kalshi orderId segment in cancel path")
		}
		return false, true, nil
	case method == "GET" && (path == "/trade-api/v2/portfolio/balance" || path == "/trade-api/v2/portfolio/orders"):
		return false, false, nil
	default:
		return false, false, errors.New("unsupported kalshi method or path")
	}
}

// SignKalshiRequestIPC validates a Kalshi Demo API v2 request against typed AgentVaultPolicy
// constraints, signs `timestampMs + METHOD + path` with RSA-PSS SHA-256, and journals spend.
func (v *Vault) SignKalshiRequestIPC(intent KalshiSignIntent, agentVaultID string) (*KalshiSignedRequest, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	env := strings.ToLower(strings.TrimSpace(intent.Environment))
	if env == "prod" || env == "production" || env == "mainnet" {
		return nil, ErrUnavailable
	}
	if env != "demo" {
		return nil, errors.New("kalshi request signing requires demo environment")
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.testnetExecEnabled || !v.allowStage4HyperliquidVaultLocked() || v.ledger.main == nil {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	if strings.TrimSpace(agentVaultID) == "" {
		return nil, errors.New("kalshi execution requires a typed agent vault")
	}
	if intent.OperationID == "" || len(intent.OperationID) > 96 || strings.TrimSpace(intent.OperationID) != intent.OperationID {
		return nil, errors.New("invalid kalshi operationId")
	}

	method := strings.ToUpper(strings.TrimSpace(intent.Method))
	path := strings.TrimSpace(intent.Path)
	isOrder, _, err := validateKalshiSignPath(method, path)
	if err != nil {
		return nil, err
	}

	nowTime := v.now()
	nowUnix := nowTime.Unix()
	if intent.ExpiresAt <= nowUnix {
		return nil, errors.New("kalshi sign intent expired")
	}
	nowMs := nowTime.UnixMilli()
	if intent.TimestampMs <= 0 || intent.TimestampMs < nowMs-5*60_000 || intent.TimestampMs > nowMs+5*60_000 {
		return nil, errors.New("kalshi timestampMs outside allowed window")
	}

	dayUTC := nowTime.UTC().Format("2006-01-02")
	asset := strings.ToUpper(strings.TrimSpace(intent.Asset))
	var spendRec *AgentVaultSpendRecord
	if isOrder {
		if strings.TrimSpace(intent.Ticker) == "" || len(intent.Ticker) > 64 {
			return nil, errors.New("kalshi order requires valid market ticker")
		}
		if asset == "" || len(asset) > 24 || asset != intent.Asset {
			return nil, errors.New("invalid kalshi order asset symbol")
		}
		if intent.EstimatedMicroUSD == 0 {
			return nil, errors.New("kalshi order requires non-zero estimated micro-USD risk")
		}
		if err := v.validateAgentVaultForVenueLocked(agentVaultID, "kalshi", asset, 0, intent.EstimatedMicroUSD, intent.ReduceOnly, nowUnix, dayUTC); err != nil {
			return nil, err
		}
		spendRec = &AgentVaultSpendRecord{
			VaultID:       agentVaultID,
			OperationID:   intent.OperationID,
			Venue:         "kalshi",
			Asset:         asset,
			DayUTC:        dayUTC,
			SpendMicroUSD: intent.EstimatedMicroUSD,
		}
		if err := ValidateAgentVaultSpendRecord(*spendRec); err != nil {
			return nil, err
		}
		if v.ledger.agentVaultOps[spendRec.OperationID] {
			return nil, errors.New("duplicate agent vault operation identity")
		}
	} else {
		if intent.EstimatedMicroUSD != 0 {
			return nil, errors.New("kalshi query or cancel must have zero estimated spend")
		}
		// Still confirm vault exists, is not revoked/expired, and allows kalshi venue.
		p, ok := v.ledger.agentVaults[agentVaultID]
		if !ok {
			return nil, errors.New("unknown agent vault")
		}
		if p.Revoked || nowUnix >= p.ExpiresAt {
			return nil, errors.New("agent vault revoked or expired")
		}
		allowedVenue := false
		for _, vn := range p.AllowedVenues {
			if vn == "kalshi" {
				allowedVenue = true
				break
			}
		}
		if !allowedVenue {
			return nil, errors.New("agent vault forbids kalshi venue")
		}
	}

	priv, keyID, pubSHA, err := v.openKalshiPrivateKeyLocked()
	if err != nil {
		return nil, err
	}
	defer func() {
		if priv.D != nil {
			priv.D.SetInt64(0)
		}
		for _, p := range priv.Primes {
			if p != nil {
				p.SetInt64(0)
			}
		}
	}()

	preSign := strconv.FormatInt(intent.TimestampMs, 10) + method + path
	digest := sha256.Sum256([]byte(preSign))
	opts := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}
	sig, err := rsa.SignPSS(rand.Reader, priv, crypto.SHA256, digest[:], opts)
	if err != nil {
		return nil, err
	}
	if err := rsa.VerifyPSS(&priv.PublicKey, crypto.SHA256, digest[:], sig, opts); err != nil {
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
	return &KalshiSignedRequest{
		Environment:       "demo",
		KeyID:             keyID,
		TimestampMs:       intent.TimestampMs,
		Method:            method,
		Path:              path,
		SignatureBase64:   base64.StdEncoding.EncodeToString(sig),
		PublicKeySHA256:   pubSHA,
		OperationID:       intent.OperationID,
		EstimatedMicroUSD: intent.EstimatedMicroUSD,
	}, nil
}

// VerifyKalshiPSSSignature verifies a Kalshi V2 RSA-PSS SHA-256 request signature against a SPKI PEM public key.
func VerifyKalshiPSSSignature(pubKeyPEM string, timestampMs int64, method, path, sigBase64 string) error {
	block, _ := pem.Decode([]byte(pubKeyPEM))
	if block == nil {
		return errors.New("invalid public key PEM")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return err
	}
	rsaPub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		return errors.New("public key is not RSA")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigBase64))
	if err != nil {
		return err
	}
	preSign := strconv.FormatInt(timestampMs, 10) + strings.ToUpper(strings.TrimSpace(method)) + strings.TrimSpace(path)
	digest := sha256.Sum256([]byte(preSign))
	return rsa.VerifyPSS(rsaPub, crypto.SHA256, digest[:], sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
}
