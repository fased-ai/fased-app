package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

const (
	// MaxPayloadBytes limits Community Hub package payloads to 256 KiB.
	MaxPayloadBytes = 256 * 1024

	KindFasedScript   = "fasedscript"
	KindDeskBlueprint = "desk_blueprint"
	KindSkill         = "skill"
	KindWatchlistPack = "watchlist_pack"
	StatusDeferredWEN = "deferred_wen_hold"
)

var (
	ErrSignatureInvalid = errors.New("hub package Ed25519 signature verification failed")
	ErrPackageTooLarge  = errors.New("hub package payload exceeds 256 KiB limit")
	ErrPackageNotFound  = errors.New("hub package not found")

	idPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)
	versionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)
)

// SATBondMetadata is declarative metadata reserved for future WEN SAT bonding.
// WEN SAT bonding execution stays strictly held (Status == "deferred_wen_hold").
type SATBondMetadata struct {
	AmountSAT int64  `json:"amountSat"`
	Status    string `json:"status"`
}

// Package describes an Ed25519-signed community artifact verified before installation.
type Package struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Kind            string          `json:"kind"`
	Version         string          `json:"version"`
	AuthorPubKeyHex string          `json:"authorPubKeyHex"`
	Payload         string          `json:"payload"`
	PayloadSHA256   string          `json:"payloadSha256"`
	SignatureHex    string          `json:"signatureHex"`
	SATBond         SATBondMetadata `json:"satBond"`
	Verified        bool            `json:"verified"`
	Installed       bool            `json:"installed"`
	CreatedAt       int64           `json:"createdAt"`
}

// PublishRequest submits a signed community package to the local verified registry.
type PublishRequest struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Version         string `json:"version"`
	AuthorPubKeyHex string `json:"authorPubKeyHex"`
	Payload         string `json:"payload"`
	SignatureHex    string `json:"signatureHex"`
	AmountSAT       int64  `json:"amountSat,omitempty"`
}

// InstallRequest marks a verified community package as installed.
type InstallRequest struct {
	ID string `json:"id"`
}

// ListView summarizes Community Hub packages alongside the active WEN SAT hold flag.
type ListView struct {
	WENHeld     bool      `json:"wenHeld"`
	SATStatus   string    `json:"satStatus"`
	Packages    []Package `json:"packages"`
	FixtureMode bool      `json:"fixtureMode"`
}

// ValidKind reports whether a Community Hub package kind is supported.
func ValidKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case KindFasedScript, KindDeskBlueprint, KindSkill, KindWatchlistPack:
		return true
	default:
		return false
	}
}

// HashPayload computes the lowercase hex SHA-256 digest of a package payload.
func HashPayload(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// ValidatePackageFields validates manifest metadata, size limit (<= 256 KiB), and deferred WEN bond status.
func ValidatePackageFields(pkg *Package) error {
	if pkg == nil {
		return errors.New("package required")
	}
	pkg.ID = strings.ToLower(strings.TrimSpace(pkg.ID))
	if !idPattern.MatchString(pkg.ID) {
		return errors.New("invalid hub package id")
	}
	pkg.Name = strings.TrimSpace(pkg.Name)
	if len(pkg.Name) < 2 || len(pkg.Name) > 80 {
		return errors.New("hub package name must be 2..80 characters")
	}
	pkg.Kind = strings.ToLower(strings.TrimSpace(pkg.Kind))
	if !ValidKind(pkg.Kind) {
		return errors.New("unsupported hub package kind")
	}
	pkg.Version = strings.TrimSpace(pkg.Version)
	if !versionPattern.MatchString(pkg.Version) {
		return errors.New("hub package version must match semver")
	}
	if len(pkg.Payload) == 0 {
		return errors.New("hub package payload required")
	}
	if len(pkg.Payload) > MaxPayloadBytes {
		return ErrPackageTooLarge
	}
	pkg.AuthorPubKeyHex = strings.ToLower(strings.TrimSpace(pkg.AuthorPubKeyHex))
	pkg.SignatureHex = strings.ToLower(strings.TrimSpace(pkg.SignatureHex))
	pkg.PayloadSHA256 = HashPayload(pkg.Payload)
	if pkg.SATBond.AmountSAT < 0 {
		pkg.SATBond.AmountSAT = 0
	}
	// Always force declarative WEN hold status until Lean V1 Mainnet authorization.
	pkg.SATBond.Status = StatusDeferredWEN
	return nil
}
