package swarm

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
)

const (
	// SwarmLiveCopyEnabled is a compile-time safety lock keeping automated live-money
	// copy trading disabled; Swarm copy trading is locked to shadow or paper mode only.
	SwarmLiveCopyEnabled = false

	MaxClockSkew        = 5 * time.Minute
	MaxThesisBytes      = 600
	CopyModeShadow      = "shadow"
	CopyModePaper       = "paper"
	DefaultBusBufferLen = 64
)

var (
	ErrSwarmDisabled    = errors.New("swarm networking is opt-in and currently disabled")
	ErrLiveCopyDisabled = errors.New("swarm live copy trading is disabled at compile time (shadow/paper only)")
	ErrReplayDetected   = errors.New("swarm envelope replay nonce already seen")
	ErrTimestampSkew    = errors.New("swarm envelope timestamp exceeds 5-minute clock skew window")
	ErrInvalidSignature = errors.New("swarm envelope Ed25519 signature verification failed")
	ErrUnknownPeer      = errors.New("swarm envelope sender is not a registered peer")

	idPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)
	symbolPattern = regexp.MustCompile(`^[A-Z0-9._:/-]{1,24}$`)
)

// SwarmConfig holds opt-in Swarm controls and shadow/paper copy boundaries.
type SwarmConfig struct {
	Enabled        bool   `json:"enabled"`
	CopyMode       string `json:"copyMode"`
	LocalPeerID    string `json:"localPeerId"`
	LocalPubKeyHex string `json:"localPubKeyHex"`
	LiveCopyLocked bool   `json:"liveCopyLocked"`
	UpdatedAt      int64  `json:"updatedAt"`
}

// ConfigPatch updates opt-in Swarm enablement or shadow/paper copy mode.
type ConfigPatch struct {
	Enabled  *bool   `json:"enabled,omitempty"`
	CopyMode *string `json:"copyMode,omitempty"`
}

// Peer describes an authenticated Ed25519 Swarm A2A peer.
type Peer struct {
	PeerID    string `json:"peerId"`
	Alias     string `json:"alias"`
	PubKeyHex string `json:"pubKeyHex"`
	Status    string `json:"status"`
	AddedAt   int64  `json:"addedAt"`
}

// PeerInput adds a verified Ed25519 peer public key to the Swarm allowlist.
type PeerInput struct {
	PeerID    string `json:"peerId"`
	Alias     string `json:"alias"`
	PubKeyHex string `json:"pubKeyHex"`
}

// Envelope is an Ed25519-signed Swarm A2A signal/message.
// Inbound payloads are strictly marked Untrusted=true so external signals never bypass risk gates.
type Envelope struct {
	EnvelopeID      string  `json:"envelopeId"`
	Nonce           string  `json:"nonce"`
	SenderPubKeyHex string  `json:"senderPubKeyHex"`
	Kind            string  `json:"kind"`
	Symbol          string  `json:"symbol"`
	Direction       string  `json:"direction"`
	Thesis          string  `json:"thesis"`
	Confidence      float64 `json:"confidence"`
	Timestamp       int64   `json:"timestamp"`
	SignatureHex    string  `json:"signatureHex"`
	Untrusted       bool    `json:"untrusted"`
	VerifiedAt      int64   `json:"verifiedAt,omitempty"`
}

// SignalInput submits a signed local signal broadcast over Swarm A2A.
type SignalInput struct {
	Symbol     string  `json:"symbol"`
	Direction  string  `json:"direction"`
	Thesis     string  `json:"thesis"`
	Confidence float64 `json:"confidence"`
}

// Event is pushed over the Swarm pub/sub bus and WebSocket stream topic "swarm".
type Event struct {
	Type      string   `json:"type"`
	Timestamp int64    `json:"ts"`
	Envelope  Envelope `json:"envelope"`
}

// StatusView summarizes Swarm opt-in state, locks, peers, and verified A2A signals.
type StatusView struct {
	Config           SwarmConfig `json:"config"`
	LiveCopyLocked   bool        `json:"liveCopyLocked"`
	CappedMode       bool        `json:"cappedMode,omitempty"`
	MaxOrderMicroUSD uint64      `json:"maxOrderMicroUsd,omitempty"`
	WENHeld          bool        `json:"wenHeld"`
	PeerCount        int         `json:"peerCount"`
	SignalCount      int         `json:"signalCount"`
	Peers            []Peer      `json:"peers"`
	RecentSignals    []Envelope  `json:"recentSignals"`
	FixtureMode      bool        `json:"fixtureMode"`
}

// ValidateCopyMode enforces shadow/paper copy lock while SwarmLiveCopyEnabled is false.
func ValidateCopyMode(mode string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		return CopyModeShadow, nil
	}
	if m == "live" && !SwarmLiveCopyEnabled {
		return "", ErrLiveCopyDisabled
	}
	if m != CopyModeShadow && m != CopyModePaper {
		return "", errors.New("swarm copyMode must be shadow or paper")
	}
	return m, nil
}

// ValidateEnvelopeFields checks envelope formatting, bounds, and forces Untrusted=true.
func ValidateEnvelopeFields(env *Envelope) error {
	if env == nil {
		return errors.New("envelope required")
	}
	env.EnvelopeID = strings.TrimSpace(env.EnvelopeID)
	if !idPattern.MatchString(env.EnvelopeID) {
		return errors.New("invalid swarm envelopeId")
	}
	env.Nonce = strings.TrimSpace(env.Nonce)
	if len(env.Nonce) < 8 || len(env.Nonce) > 96 {
		return errors.New("invalid swarm nonce length")
	}
	env.SenderPubKeyHex = strings.ToLower(strings.TrimSpace(env.SenderPubKeyHex))
	env.SignatureHex = strings.ToLower(strings.TrimSpace(env.SignatureHex))
	env.Kind = strings.ToLower(strings.TrimSpace(env.Kind))
	if env.Kind == "" {
		env.Kind = "signal"
	}
	if env.Kind != "signal" && env.Kind != "critique" && env.Kind != "heartbeat" {
		return errors.New("unsupported swarm envelope kind")
	}
	env.Symbol = strings.ToUpper(strings.TrimSpace(env.Symbol))
	if !symbolPattern.MatchString(env.Symbol) {
		return errors.New("invalid swarm symbol")
	}
	env.Direction = strings.ToUpper(strings.TrimSpace(env.Direction))
	if env.Direction != "LONG" && env.Direction != "SHORT" && env.Direction != "NEUTRAL" {
		return errors.New("swarm direction must be LONG, SHORT, or NEUTRAL")
	}
	env.Thesis = strings.TrimSpace(env.Thesis)
	if len(env.Thesis) == 0 || len(env.Thesis) > MaxThesisBytes {
		return errors.New("swarm thesis must be 1..600 characters")
	}
	if math.IsNaN(env.Confidence) || math.IsInf(env.Confidence, 0) || env.Confidence < 0 || env.Confidence > 1 {
		return errors.New("swarm confidence must be in [0, 1]")
	}
	// All A2A intelligence stays permanently quarantined as untrusted input.
	env.Untrusted = true
	return nil
}
