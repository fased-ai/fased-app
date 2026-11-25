package swarm

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// CanonicalEnvelopeBytes computes the deterministic Ed25519 signing payload for an Envelope.
func CanonicalEnvelopeBytes(env Envelope) []byte {
	return []byte(fmt.Sprintf(
		"fased-swarm-a2a-v1|%s|%s|%s|%s|%s|%.4f|%d|%s",
		env.EnvelopeID,
		env.Nonce,
		env.Kind,
		env.Symbol,
		env.Direction,
		env.Confidence,
		env.Timestamp,
		env.Thesis,
	))
}

// SignEnvelope attaches SenderPubKeyHex and SignatureHex using an Ed25519 private key.
func SignEnvelope(priv ed25519.PrivateKey, env *Envelope) error {
	if env == nil || len(priv) != ed25519.PrivateKeySize {
		return errors.New("valid Ed25519 private key and envelope required")
	}
	pub := priv.Public().(ed25519.PublicKey)
	env.SenderPubKeyHex = hex.EncodeToString(pub)
	if err := ValidateEnvelopeFields(env); err != nil {
		return err
	}
	sig := ed25519.Sign(priv, CanonicalEnvelopeBytes(*env))
	env.SignatureHex = hex.EncodeToString(sig)
	return nil
}

// VerifyEnvelope checks field invariants, clock skew (<= 5m), Ed25519 signature,
// and forces Untrusted=true on all verified payloads.
func VerifyEnvelope(env *Envelope, now time.Time) error {
	if err := ValidateEnvelopeFields(env); err != nil {
		return err
	}
	delta := now.UTC().Sub(time.Unix(env.Timestamp, 0).UTC())
	if delta < 0 {
		delta = -delta
	}
	if delta > MaxClockSkew {
		return ErrTimestampSkew
	}
	pub, err := hex.DecodeString(env.SenderPubKeyHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return ErrInvalidSignature
	}
	sig, err := hex.DecodeString(env.SignatureHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	if !ed25519.Verify(pub, CanonicalEnvelopeBytes(*env), sig) {
		return ErrInvalidSignature
	}
	env.Untrusted = true
	env.VerifiedAt = now.UTC().Unix()
	return nil
}
