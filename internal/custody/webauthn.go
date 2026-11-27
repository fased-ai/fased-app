package custody

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"
)

const (
	coseAlgES256         = -7
	coseAlgEdDSA         = -8
	passkeyChallengeTTL  = 120 * time.Second
	maxPasskeyChallenges = 32
	maxOwnerPasskeys     = 16
	authDataFlagUP       = 0x01
	authDataFlagUV       = 0x04
	authDataFlagAT       = 0x40
)

// OwnerPasskey is the public metadata for a WebAuthn/FIDO2 passkey registered directly inside fased-signer.
type OwnerPasskey struct {
	CredentialID string `json:"credentialId"`
	Label        string `json:"label"`
	RPID         string `json:"rpId"`
	Alg          int    `json:"alg"`
	SignCount    uint32 `json:"signCount"`
	CreatedAt    int64  `json:"createdAt"`
	LastUsedAt   int64  `json:"lastUsedAt,omitempty"`
}

// PasskeyRegisterOptions is returned by fased-signer to begin WebAuthn navigator.credentials.create().
type PasskeyRegisterOptions struct {
	Challenge             string   `json:"challenge"`
	RPID                  string   `json:"rpId"`
	RPName                string   `json:"rpName"`
	UserID                string   `json:"userId"`
	UserName              string   `json:"userName"`
	UserDisplayName       string   `json:"userDisplayName"`
	PubKeyCredAlgs        []int    `json:"pubKeyCredAlgs"`
	ExcludeCredentialIDs  []string `json:"excludeCredentialIds,omitempty"`
	TimeoutMs             int64    `json:"timeoutMs"`
	UserVerification      string   `json:"userVerification"`
	AttestationPreference string   `json:"attestation"`
	ExpiresAt             int64    `json:"expiresAt"`
}

// PasskeyRegisterFinishRequest carries the browser's WebAuthn attestation payload through fased-app as an opaque relay.
type PasskeyRegisterFinishRequest struct {
	Challenge         string `json:"challenge"`
	Label             string `json:"label,omitempty"`
	CredentialID      string `json:"credentialId"`
	ClientDataJSON    string `json:"clientDataJSON"`    // base64url
	AttestationObject string `json:"attestationObject"` // base64url CBOR {fmt, authData, attStmt}
}

// PasskeyAssertOptions is returned by fased-signer to begin WebAuthn navigator.credentials.get()
// cryptographically bound to (action, walletId, bindingDigest).
type PasskeyAssertOptions struct {
	Challenge          string   `json:"challenge"`
	RPID               string   `json:"rpId"`
	AllowCredentialIDs []string `json:"allowCredentialIds"`
	Action             string   `json:"action"`
	WalletID           string   `json:"walletId"`
	BindingDigest      string   `json:"bindingDigest,omitempty"`
	TimeoutMs          int64    `json:"timeoutMs"`
	UserVerification   string   `json:"userVerification"`
	ExpiresAt          int64    `json:"expiresAt"`
}

// PasskeyAssertFinishRequest carries the signed WebAuthn assertion payload through fased-app as an opaque relay.
type PasskeyAssertFinishRequest struct {
	Challenge         string `json:"challenge"`
	CredentialID      string `json:"credentialId"`
	ClientDataJSON    string `json:"clientDataJSON"`    // base64url
	AuthenticatorData string `json:"authenticatorData"` // base64url
	Signature         string `json:"signature"`         // base64url
}

type passkeyChallengeEntry struct {
	kind          string // "register" | "assert"
	rpID          string
	origin        string
	label         string
	action        string
	walletID      string
	bindingDigest string
	expiresAt     time.Time
}

type storedPasskeyPubKey struct {
	Crv string `json:"crv"` // "P-256" | "Ed25519"
	X   string `json:"x"`   // base64url
	Y   string `json:"y,omitempty"`
}

type webAuthnClientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin,omitempty"`
}

func validateWebAuthnOriginAndRP(rpID, rawOrigin string) (string, string, error) {
	originStr := strings.TrimSpace(rawOrigin)
	u, err := url.Parse(originStr)
	if err != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("invalid WebAuthn origin")
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme != "https" {
		if !(u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")) {
			return "", "", errors.New("WebAuthn origin must be HTTPS or loopback localhost")
		}
	}
	cleanRP := strings.ToLower(strings.TrimSpace(rpID))
	if cleanRP == "" {
		cleanRP = host
	}
	if ip := net.ParseIP(cleanRP); ip != nil && cleanRP != host {
		return "", "", errors.New("WebAuthn rpId must match origin host")
	}
	if cleanRP != host && !strings.HasSuffix(host, "."+cleanRP) {
		return "", "", errors.New("WebAuthn rpId does not match origin host")
	}
	canonOrigin := u.Scheme + "://" + strings.ToLower(u.Host)
	return cleanRP, canonOrigin, nil
}

func derivePasskeyChallenge(kind, rpID, origin, action, walletID, bindingDigest string) (string, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte("fased-signer-webauthn:v1\x00"))
	for _, field := range []string{kind, rpID, origin, action, walletID, bindingDigest} {
		h.Write([]byte(field))
		h.Write([]byte{0})
	}
	h.Write(nonce)
	clear(nonce)
	sum := h.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(sum), nil
}

func (s *Service) storePasskeyChallenge(challenge string, entry passkeyChallengeEntry) error {
	now := s.now().UTC()
	s.proofMu.Lock()
	defer s.proofMu.Unlock()
	if s.passkeyChallenges == nil {
		s.passkeyChallenges = make(map[string]passkeyChallengeEntry)
	}
	for k, v := range s.passkeyChallenges {
		if !now.Before(v.expiresAt) {
			delete(s.passkeyChallenges, k)
		}
	}
	if len(s.passkeyChallenges) >= maxPasskeyChallenges {
		return errors.New("too many pending passkey challenges; retry shortly")
	}
	s.passkeyChallenges[challenge] = entry
	return nil
}

func (s *Service) consumePasskeyChallenge(challenge, wantKind string) (passkeyChallengeEntry, error) {
	clean := strings.TrimSpace(challenge)
	if clean == "" {
		return passkeyChallengeEntry{}, fmt.Errorf("%w: missing passkey challenge", ErrOwnerAuthRequired)
	}
	now := s.now().UTC()
	s.proofMu.Lock()
	defer s.proofMu.Unlock()
	entry, ok := s.passkeyChallenges[clean]
	if ok {
		delete(s.passkeyChallenges, clean)
	}
	if !ok || entry.kind != wantKind || !now.Before(entry.expiresAt) {
		return passkeyChallengeEntry{}, fmt.Errorf("%w: passkey challenge expired, already used, or invalid", ErrOwnerAuthRequired)
	}
	return entry, nil
}

// ListOwnerPasskeys returns all registered WebAuthn passkeys stored inside fased-signer.
func (s *Service) ListOwnerPasskeys(ctx context.Context) ([]OwnerPasskey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return nil, errors.New("custody store closed")
	}
	return s.listOwnerPasskeysLocked(ctx)
}

func (s *Service) listOwnerPasskeysLocked(ctx context.Context) ([]OwnerPasskey, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT credential_id, label, rp_id, alg, sign_count, created_at, last_used_at
FROM owner_passkeys ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OwnerPasskey{}
	for rows.Next() {
		var p OwnerPasskey
		if err := rows.Scan(&p.CredentialID, &p.Label, &p.RPID, &p.Alg, &p.SignCount, &p.CreatedAt, &p.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) countOwnerPasskeysLocked(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM owner_passkeys`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// HasOwnerPasskeys reports whether at least one FIDO2/WebAuthn passkey is enrolled in fased-signer.
func (s *Service) HasOwnerPasskeys(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return false, errors.New("custody store closed")
	}
	n, err := s.countOwnerPasskeysLocked(ctx)
	return n > 0, err
}

// DeleteOwnerPasskey removes a registered passkey by credential ID.
func (s *Service) DeleteOwnerPasskey(ctx context.Context, credentialID string) (bool, error) {
	cleanID := strings.TrimSpace(credentialID)
	if cleanID == "" {
		return false, errors.New("credentialId required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return false, errors.New("custody store closed")
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM owner_passkeys WHERE credential_id = ?`, cleanID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		_ = s.appendAuditExecLocked(ctx, s.db, "*", "signer_passkey_deleted", "owner", "Removed FIDO2 passkey "+cleanID[:min(12, len(cleanID))], "")
	}
	return n > 0, nil
}

// BeginPasskeyRegistration creates a single-use WebAuthn registration challenge inside fased-signer.
func (s *Service) BeginPasskeyRegistration(ctx context.Context, rpID, origin, label string) (PasskeyRegisterOptions, error) {
	cleanRP, canonOrigin, err := validateWebAuthnOriginAndRP(rpID, origin)
	if err != nil {
		return PasskeyRegisterOptions{}, err
	}
	cleanLabel := strings.TrimSpace(label)
	if cleanLabel == "" {
		cleanLabel = "Fased Owner Passkey"
	}
	if len(cleanLabel) > 80 {
		cleanLabel = cleanLabel[:80]
	}

	s.mu.Lock()
	if s.closed || s.db == nil {
		s.mu.Unlock()
		return PasskeyRegisterOptions{}, errors.New("custody store closed")
	}
	existing, err := s.listOwnerPasskeysLocked(ctx)
	s.mu.Unlock()
	if err != nil {
		return PasskeyRegisterOptions{}, err
	}
	if len(existing) >= maxOwnerPasskeys {
		return PasskeyRegisterOptions{}, fmt.Errorf("maximum of %d passkeys already registered", maxOwnerPasskeys)
	}
	exclude := make([]string, 0, len(existing))
	for _, p := range existing {
		exclude = append(exclude, p.CredentialID)
	}

	challenge, err := derivePasskeyChallenge("register", cleanRP, canonOrigin, "passkey-register", "*", "")
	if err != nil {
		return PasskeyRegisterOptions{}, err
	}
	exp := s.now().UTC().Add(passkeyChallengeTTL)
	if err := s.storePasskeyChallenge(challenge, passkeyChallengeEntry{
		kind:      "register",
		rpID:      cleanRP,
		origin:    canonOrigin,
		label:     cleanLabel,
		action:    "passkey-register",
		walletID:  "*",
		expiresAt: exp,
	}); err != nil {
		return PasskeyRegisterOptions{}, err
	}
	uidSum := sha256.Sum256([]byte("fased-signer-owner:" + cleanRP))
	return PasskeyRegisterOptions{
		Challenge:             challenge,
		RPID:                  cleanRP,
		RPName:                "Fased Signer",
		UserID:                base64.RawURLEncoding.EncodeToString(uidSum[:16]),
		UserName:              "fased-owner",
		UserDisplayName:       "Fased Signer Owner",
		PubKeyCredAlgs:        []int{coseAlgES256, coseAlgEdDSA},
		ExcludeCredentialIDs:  exclude,
		TimeoutMs:             passkeyChallengeTTL.Milliseconds(),
		UserVerification:      "required",
		AttestationPreference: "none",
		ExpiresAt:             exp.Unix(),
	}, nil
}

// FinishPasskeyRegistration verifies the WebAuthn attestation object directly inside fased-signer
// and persists the authenticator's public key in signer-vault/.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, req PasskeyRegisterFinishRequest) (OwnerPasskey, error) {
	chEntry, err := s.consumePasskeyChallenge(req.Challenge, "register")
	if err != nil {
		return OwnerPasskey{}, err
	}
	clientDataRaw, err := decodeB64URL(req.ClientDataJSON)
	if err != nil || len(clientDataRaw) == 0 || len(clientDataRaw) > 16*1024 {
		return OwnerPasskey{}, errors.New("invalid clientDataJSON encoding")
	}
	var cd webAuthnClientData
	if err := json.Unmarshal(clientDataRaw, &cd); err != nil {
		return OwnerPasskey{}, errors.New("malformed clientDataJSON")
	}
	if cd.Type != "webauthn.create" || cd.Challenge != strings.TrimSpace(req.Challenge) || cd.Origin != chEntry.origin || cd.CrossOrigin {
		return OwnerPasskey{}, fmt.Errorf("%w: WebAuthn registration clientData mismatch", ErrOwnerAuthRequired)
	}

	attRaw, err := decodeB64URL(req.AttestationObject)
	if err != nil || len(attRaw) == 0 || len(attRaw) > 64*1024 {
		return OwnerPasskey{}, errors.New("invalid attestationObject encoding")
	}
	authData, err := extractAuthDataFromAttestationObject(attRaw)
	if err != nil {
		return OwnerPasskey{}, fmt.Errorf("invalid attestationObject: %w", err)
	}
	parsed, err := parseAttestedAuthData(authData, chEntry.rpID)
	if err != nil {
		return OwnerPasskey{}, fmt.Errorf("%w: %v", ErrOwnerAuthRequired, err)
	}
	credIDB64 := base64.RawURLEncoding.EncodeToString(parsed.credentialID)
	if strings.TrimSpace(req.CredentialID) != "" && strings.TrimSpace(req.CredentialID) != credIDB64 {
		return OwnerPasskey{}, errors.New("credentialId does not match attested credential data")
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = chEntry.label
	}
	if len(label) > 80 {
		label = label[:80]
	}
	pubJSON, err := json.Marshal(parsed.pubKey)
	if err != nil {
		return OwnerPasskey{}, err
	}
	nowUnix := s.now().UTC().Unix()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return OwnerPasskey{}, errors.New("custody store closed")
	}
	count, err := s.countOwnerPasskeysLocked(ctx)
	if err != nil {
		return OwnerPasskey{}, err
	}
	if count >= maxOwnerPasskeys {
		return OwnerPasskey{}, fmt.Errorf("maximum of %d passkeys already registered", maxOwnerPasskeys)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO owner_passkeys (credential_id, label, rp_id, alg, pub_key_json, sign_count, created_at, last_used_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 0)`,
		credIDB64, label, chEntry.rpID, parsed.alg, string(pubJSON), parsed.signCount, nowUnix,
	)
	if err != nil {
		return OwnerPasskey{}, fmt.Errorf("register passkey: %w", err)
	}
	_ = s.appendAuditExecLocked(ctx, s.db, "*", "signer_passkey_registered", "owner", "Registered FIDO2 passkey "+label, "")
	return OwnerPasskey{
		CredentialID: credIDB64,
		Label:        label,
		RPID:         chEntry.rpID,
		Alg:          parsed.alg,
		SignCount:    parsed.signCount,
		CreatedAt:    nowUnix,
	}, nil
}

// BeginPasskeyAssertion mints a WebAuthn assertion challenge inside fased-signer cryptographically
// bound to (action, walletId, bindingDigest). For "execute", bindingDigest must be the exact
// review.TxDigest (64-char hex); for "policy", bindingDigest binds to nextPolicyHash.
func (s *Service) BeginPasskeyAssertion(ctx context.Context, rpID, origin, action, walletID, bindingDigest string) (PasskeyAssertOptions, error) {
	cleanRP, canonOrigin, err := validateWebAuthnOriginAndRP(rpID, origin)
	if err != nil {
		return PasskeyAssertOptions{}, err
	}
	act, wid := normalizeOwnerScope(action, walletID)
	if act == "" {
		return PasskeyAssertOptions{}, errors.New("owner authorization action required")
	}
	if wid == "*" && act != "restore" && act != "passkey-register" && act != "passkey-delete" {
		return PasskeyAssertOptions{}, fmt.Errorf("%w: explicit walletId required for %s authorization", ErrOwnerAuthRequired, act)
	}
	cleanBinding := strings.ToLower(strings.TrimSpace(bindingDigest))
	if act == "execute" && len(cleanBinding) != 64 {
		return PasskeyAssertOptions{}, fmt.Errorf("%w: execute passkey assertion must bind to exact 64-hex txDigest", ErrOwnerAuthRequired)
	}
	if cleanBinding != "" {
		if _, decErr := hex.DecodeString(cleanBinding); decErr != nil || len(cleanBinding) != 64 {
			return PasskeyAssertOptions{}, errors.New("bindingDigest must be a 64-character hex SHA-256 digest")
		}
	}

	s.mu.Lock()
	if s.closed || s.db == nil {
		s.mu.Unlock()
		return PasskeyAssertOptions{}, errors.New("custody store closed")
	}
	keys, err := s.listOwnerPasskeysLocked(ctx)
	s.mu.Unlock()
	if err != nil {
		return PasskeyAssertOptions{}, err
	}
	allowIDs := make([]string, 0, len(keys))
	for _, k := range keys {
		if k.RPID == cleanRP {
			allowIDs = append(allowIDs, k.CredentialID)
		}
	}
	if len(allowIDs) == 0 {
		return PasskeyAssertOptions{}, fmt.Errorf("%w: no passkey registered for rpId %s", ErrOwnerAuthRequired, cleanRP)
	}

	challenge, err := derivePasskeyChallenge("assert", cleanRP, canonOrigin, act, wid, cleanBinding)
	if err != nil {
		return PasskeyAssertOptions{}, err
	}
	exp := s.now().UTC().Add(passkeyChallengeTTL)
	if err := s.storePasskeyChallenge(challenge, passkeyChallengeEntry{
		kind:          "assert",
		rpID:          cleanRP,
		origin:        canonOrigin,
		action:        act,
		walletID:      wid,
		bindingDigest: cleanBinding,
		expiresAt:     exp,
	}); err != nil {
		return PasskeyAssertOptions{}, err
	}
	return PasskeyAssertOptions{
		Challenge:          challenge,
		RPID:               cleanRP,
		AllowCredentialIDs: allowIDs,
		Action:             act,
		WalletID:           wid,
		BindingDigest:      cleanBinding,
		TimeoutMs:          passkeyChallengeTTL.Milliseconds(),
		UserVerification:   "required",
		ExpiresAt:          exp.Unix(),
	}, nil
}

// FinishPasskeyAssertion verifies a WebAuthn assertion signature inside fased-signer and mints
// a single-use, action-, wallet-, and digest-bound owner proof without any password crossing fased-app.
func (s *Service) FinishPasskeyAssertion(ctx context.Context, req PasskeyAssertFinishRequest) (string, time.Time, string, error) {
	now := s.now().UTC()
	if err := s.checkOwnerAuthRateLocked(now); err != nil {
		return "", time.Time{}, "", err
	}
	chEntry, err := s.consumePasskeyChallenge(req.Challenge, "assert")
	if err != nil {
		s.recordOwnerAuthFailure(s.now().UTC())
		return "", time.Time{}, "", err
	}
	credID := strings.TrimSpace(req.CredentialID)
	if credID == "" {
		return "", time.Time{}, "", fmt.Errorf("%w: credentialId required", ErrOwnerAuthRequired)
	}

	clientDataRaw, err := decodeB64URL(req.ClientDataJSON)
	if err != nil || len(clientDataRaw) == 0 || len(clientDataRaw) > 16*1024 {
		return "", time.Time{}, "", errors.New("invalid clientDataJSON encoding")
	}
	var cd webAuthnClientData
	if err := json.Unmarshal(clientDataRaw, &cd); err != nil {
		return "", time.Time{}, "", errors.New("malformed clientDataJSON")
	}
	if cd.Type != "webauthn.get" || cd.Challenge != strings.TrimSpace(req.Challenge) || cd.Origin != chEntry.origin || cd.CrossOrigin {
		s.recordOwnerAuthFailure(s.now().UTC())
		return "", time.Time{}, "", fmt.Errorf("%w: WebAuthn assertion clientData mismatch", ErrOwnerAuthRequired)
	}

	authData, err := decodeB64URL(req.AuthenticatorData)
	if err != nil || len(authData) < 37 || len(authData) > 16*1024 {
		return "", time.Time{}, "", errors.New("invalid authenticatorData")
	}
	sigBytes, err := decodeB64URL(req.Signature)
	if err != nil || len(sigBytes) < 32 || len(sigBytes) > 512 {
		return "", time.Time{}, "", errors.New("invalid assertion signature encoding")
	}

	rpHash := sha256.Sum256([]byte(chEntry.rpID))
	if !bytes.Equal(authData[:32], rpHash[:]) {
		s.recordOwnerAuthFailure(s.now().UTC())
		return "", time.Time{}, "", fmt.Errorf("%w: authenticatorData rpIdHash mismatch", ErrOwnerAuthRequired)
	}
	flags := authData[32]
	if flags&authDataFlagUP == 0 || flags&authDataFlagUV == 0 {
		s.recordOwnerAuthFailure(s.now().UTC())
		return "", time.Time{}, "", fmt.Errorf("%w: passkey user presence and verification required", ErrOwnerAuthRequired)
	}
	newSignCount := binary.BigEndian.Uint32(authData[33:37])

	s.mu.Lock()
	if s.closed || s.db == nil {
		s.mu.Unlock()
		return "", time.Time{}, "", errors.New("custody store closed")
	}
	var (
		storedRPID string
		alg        int
		pubJSON    string
		prevCount  uint32
		label      string
	)
	err = s.db.QueryRowContext(ctx, `
SELECT label, rp_id, alg, pub_key_json, sign_count FROM owner_passkeys WHERE credential_id = ?`, credID).
		Scan(&label, &storedRPID, &alg, &pubJSON, &prevCount)
	if errors.Is(err, sql.ErrNoRows) {
		s.mu.Unlock()
		s.recordOwnerAuthFailure(s.now().UTC())
		return "", time.Time{}, "", fmt.Errorf("%w: unknown passkey credential", ErrOwnerAuthRequired)
	}
	if err != nil {
		s.mu.Unlock()
		return "", time.Time{}, "", err
	}
	if storedRPID != chEntry.rpID {
		s.mu.Unlock()
		s.recordOwnerAuthFailure(s.now().UTC())
		return "", time.Time{}, "", fmt.Errorf("%w: passkey rpId mismatch", ErrOwnerAuthRequired)
	}
	// Clone detection: when either stored or presented signCount is non-zero, count must strictly increase.
	if (prevCount > 0 || newSignCount > 0) && newSignCount <= prevCount {
		s.mu.Unlock()
		s.recordOwnerAuthFailure(s.now().UTC())
		return "", time.Time{}, "", fmt.Errorf("%w: passkey signCount did not advance (possible cloned authenticator)", ErrOwnerAuthRequired)
	}
	var pub storedPasskeyPubKey
	if err := json.Unmarshal([]byte(pubJSON), &pub); err != nil {
		s.mu.Unlock()
		return "", time.Time{}, "", errors.New("corrupted stored passkey public key")
	}
	clientDataHash := sha256.Sum256(clientDataRaw)
	signedPayload := make([]byte, 0, len(authData)+len(clientDataHash))
	signedPayload = append(signedPayload, authData...)
	signedPayload = append(signedPayload, clientDataHash[:]...)

	if !verifyWebAuthnSignature(alg, pub, signedPayload, sigBytes) {
		s.mu.Unlock()
		s.recordOwnerAuthFailure(s.now().UTC())
		return "", time.Time{}, "", fmt.Errorf("%w: invalid passkey signature", ErrOwnerAuthRequired)
	}

	nowUnix := s.now().UTC().Unix()
	if _, err := s.db.ExecContext(ctx, `UPDATE owner_passkeys SET sign_count = ?, last_used_at = ? WHERE credential_id = ?`, newSignCount, nowUnix, credID); err != nil {
		s.mu.Unlock()
		return "", time.Time{}, "", err
	}
	bindNote := ""
	if chEntry.bindingDigest != "" {
		bindNote = " digest=" + chEntry.bindingDigest[:min(12, len(chEntry.bindingDigest))]
	}
	_ = s.appendAuditExecLocked(ctx, s.db, chEntry.walletID, "signer_passkey_authorized", "owner", "Passkey "+label+" authorized "+chEntry.action+bindNote, chEntry.bindingDigest)
	s.mu.Unlock()

	s.resetOwnerAuthFailures()
	proof, exp, err := s.mintOwnerProof(chEntry.action, chEntry.walletID, chEntry.bindingDigest)
	if err != nil {
		return "", time.Time{}, "", err
	}
	return proof, exp, chEntry.bindingDigest, nil
}

func verifyWebAuthnSignature(alg int, pub storedPasskeyPubKey, signedData, sig []byte) bool {
	switch alg {
	case coseAlgES256:
		if pub.Crv != "P-256" {
			return false
		}
		xb, err1 := decodeB64URL(pub.X)
		yb, err2 := decodeB64URL(pub.Y)
		if err1 != nil || err2 != nil || len(xb) != 32 || len(yb) != 32 {
			return false
		}
		x := new(big.Int).SetBytes(xb)
		y := new(big.Int).SetBytes(yb)
		curve := elliptic.P256()
		if !curve.IsOnCurve(x, y) {
			return false
		}
		ecPub := &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
		digest := sha256.Sum256(signedData)
		return ecdsa.VerifyASN1(ecPub, digest[:], sig)
	case coseAlgEdDSA:
		if pub.Crv != "Ed25519" {
			return false
		}
		xb, err := decodeB64URL(pub.X)
		if err != nil || len(xb) != ed25519.PublicKeySize {
			return false
		}
		return ed25519.Verify(ed25519.PublicKey(xb), signedData, sig)
	default:
		return false
	}
}

func decodeB64URL(s string) ([]byte, error) {
	clean := strings.TrimSpace(s)
	clean = strings.TrimRight(clean, "=")
	return base64.RawURLEncoding.DecodeString(clean)
}

type parsedAttestation struct {
	credentialID []byte
	signCount    uint32
	alg          int
	pubKey       storedPasskeyPubKey
}

func parseAttestedAuthData(authData []byte, expectedRPID string) (parsedAttestation, error) {
	if len(authData) < 55 {
		return parsedAttestation{}, errors.New("authenticatorData too short for attested credential")
	}
	rpHash := sha256.Sum256([]byte(expectedRPID))
	if !bytes.Equal(authData[:32], rpHash[:]) {
		return parsedAttestation{}, errors.New("authenticatorData rpIdHash mismatch")
	}
	flags := authData[32]
	if flags&authDataFlagUP == 0 || flags&authDataFlagUV == 0 || flags&authDataFlagAT == 0 {
		return parsedAttestation{}, errors.New("authenticatorData missing UP, UV, or AT flag")
	}
	signCount := binary.BigEndian.Uint32(authData[33:37])
	// Bytes 37..53 are AAGUID (16 bytes); 53..55 is credentialIdLength (big-endian uint16).
	credLen := int(binary.BigEndian.Uint16(authData[53:55]))
	if credLen == 0 || credLen > 1024 || 55+credLen >= len(authData) {
		return parsedAttestation{}, errors.New("invalid attested credentialId length")
	}
	credID := make([]byte, credLen)
	copy(credID, authData[55:55+credLen])

	coseBytes := authData[55+credLen:]
	alg, pub, err := parseCOSEPublicKey(coseBytes)
	if err != nil {
		return parsedAttestation{}, err
	}
	return parsedAttestation{
		credentialID: credID,
		signCount:    signCount,
		alg:          alg,
		pubKey:       pub,
	}, nil
}

// --- Minimal strict CBOR decoder for WebAuthn attestationObject & COSE public keys ---

type cborReader struct {
	b   []byte
	pos int
}

func (r *cborReader) readByte() (byte, error) {
	if r.pos >= len(r.b) {
		return 0, errors.New("unexpected end of CBOR")
	}
	v := r.b[r.pos]
	r.pos++
	return v, nil
}

func (r *cborReader) readBytes(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.b) {
		return nil, errors.New("CBOR length exceeds buffer")
	}
	out := r.b[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

func (r *cborReader) readHead() (major byte, val uint64, err error) {
	b, err := r.readByte()
	if err != nil {
		return 0, 0, err
	}
	major = b >> 5
	ai := b & 0x1f
	switch {
	case ai < 24:
		return major, uint64(ai), nil
	case ai == 24:
		v, err := r.readByte()
		return major, uint64(v), err
	case ai == 25:
		bs, err := r.readBytes(2)
		if err != nil {
			return 0, 0, err
		}
		return major, uint64(binary.BigEndian.Uint16(bs)), nil
	case ai == 26:
		bs, err := r.readBytes(4)
		if err != nil {
			return 0, 0, err
		}
		return major, uint64(binary.BigEndian.Uint32(bs)), nil
	case ai == 27:
		bs, err := r.readBytes(8)
		if err != nil {
			return 0, 0, err
		}
		return major, binary.BigEndian.Uint64(bs), nil
	default:
		return 0, 0, errors.New("unsupported CBOR additional info")
	}
}

func (r *cborReader) readValue(depth int) (any, error) {
	if depth > 8 {
		return nil, errors.New("CBOR nesting limit exceeded")
	}
	major, val, err := r.readHead()
	if err != nil {
		return nil, err
	}
	switch major {
	case 0: // unsigned int
		return int64(val), nil
	case 1: // negative int (-1 - val)
		return -1 - int64(val), nil
	case 2: // byte string
		if val > 65536 {
			return nil, errors.New("CBOR byte string too large")
		}
		bs, err := r.readBytes(int(val))
		if err != nil {
			return nil, err
		}
		cp := make([]byte, len(bs))
		copy(cp, bs)
		return cp, nil
	case 3: // text string
		if val > 65536 {
			return nil, errors.New("CBOR text string too large")
		}
		bs, err := r.readBytes(int(val))
		if err != nil {
			return nil, err
		}
		return string(bs), nil
	case 4: // array
		if val > 128 {
			return nil, errors.New("CBOR array too large")
		}
		arr := make([]any, 0, val)
		for i := uint64(0); i < val; i++ {
			item, err := r.readValue(depth + 1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, item)
		}
		return arr, nil
	case 5: // map
		if val > 128 {
			return nil, errors.New("CBOR map too large")
		}
		m := make(map[any]any, val)
		for i := uint64(0); i < val; i++ {
			k, err := r.readValue(depth + 1)
			if err != nil {
				return nil, err
			}
			v, err := r.readValue(depth + 1)
			if err != nil {
				return nil, err
			}
			switch k.(type) {
			case int64, string:
				m[k] = v
			}
		}
		return m, nil
	case 7: // simple/float
		return val, nil
	default:
		return nil, errors.New("unsupported CBOR major type")
	}
}

func extractAuthDataFromAttestationObject(raw []byte) ([]byte, error) {
	rd := &cborReader{b: raw}
	v, err := rd.readValue(0)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[any]any)
	if !ok {
		return nil, errors.New("attestationObject must be CBOR map")
	}
	ad, ok := m["authData"].([]byte)
	if !ok || len(ad) == 0 {
		return nil, errors.New("attestationObject missing authData")
	}
	return ad, nil
}

func parseCOSEPublicKey(raw []byte) (int, storedPasskeyPubKey, error) {
	rd := &cborReader{b: raw}
	v, err := rd.readValue(0)
	if err != nil {
		return 0, storedPasskeyPubKey{}, fmt.Errorf("decode COSE key: %w", err)
	}
	m, ok := v.(map[any]any)
	if !ok {
		return 0, storedPasskeyPubKey{}, errors.New("COSE key must be CBOR map")
	}
	kty, _ := m[int64(1)].(int64)
	alg, _ := m[int64(3)].(int64)
	crv, _ := m[int64(-1)].(int64)
	xBytes, _ := m[int64(-2)].([]byte)

	switch {
	case kty == 2 && alg == coseAlgES256 && crv == 1:
		// EC2 P-256
		yBytes, _ := m[int64(-3)].([]byte)
		if len(xBytes) != 32 || len(yBytes) != 32 {
			return 0, storedPasskeyPubKey{}, errors.New("invalid P-256 COSE coordinates")
		}
		x := new(big.Int).SetBytes(xBytes)
		y := new(big.Int).SetBytes(yBytes)
		if !elliptic.P256().IsOnCurve(x, y) {
			return 0, storedPasskeyPubKey{}, errors.New("P-256 COSE key not on curve")
		}
		return coseAlgES256, storedPasskeyPubKey{
			Crv: "P-256",
			X:   base64.RawURLEncoding.EncodeToString(xBytes),
			Y:   base64.RawURLEncoding.EncodeToString(yBytes),
		}, nil
	case kty == 1 && alg == coseAlgEdDSA && crv == 6:
		// OKP Ed25519
		if len(xBytes) != ed25519.PublicKeySize {
			return 0, storedPasskeyPubKey{}, errors.New("invalid Ed25519 COSE key length")
		}
		return coseAlgEdDSA, storedPasskeyPubKey{
			Crv: "Ed25519",
			X:   base64.RawURLEncoding.EncodeToString(xBytes),
		}, nil
	default:
		return 0, storedPasskeyPubKey{}, fmt.Errorf("unsupported COSE passkey algorithm (kty=%d alg=%d crv=%d); use ES256 or Ed25519", kty, alg, crv)
	}
}
