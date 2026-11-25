package vpsnetwork

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/update"
)

const packageRemotePhase = "package-remote"

// Approval is an explicit owner review of private UI and console recovery.
// It is never inferred from an app session, installation, or saved preference.
type Approval struct {
	PrivateUI       bool `json:"privateUi"`
	ConsoleRecovery bool `json:"consoleRecovery"`
}

// Status describes the current recoverable Remote network lockdown state.
type Status struct {
	Phase     string    `json:"phase"`
	AttemptID string    `json:"attemptId,omitempty"`
	Deadline  time.Time `json:"deadline,omitzero"`
}

// Manifest projects an immutable Format-4 Remote package instance for network lockdown.
// Field order and JSON tags are preserved for digest stability with live Format-4 bindings.
type Manifest struct {
	ApplicationSequence      uint64 `json:"applicationSequence,omitempty"`
	ApplicationReleaseSHA256 string `json:"applicationReleaseSha256,omitempty"`
	Version                  int    `json:"version"`
	ID                       string `json:"id"`
	Phase                    string `json:"phase"`
	OwnerUID                 int    `json:"ownerUid"`
	OwnerGID                 int    `json:"ownerGid"`
	AppUID                   int    `json:"appUid"`
	AppGID                   int    `json:"appGid"`
	AppUser                  string `json:"appUser"`
	Root                     string `json:"root"`
	Data                     string `json:"data"`
	Core                     string `json:"core"`
	Config                   string `json:"config"`
	Unit                     string `json:"unit"`
	UnitPath                 string `json:"unitPath"`
	Listen                   string `json:"listen"`
	PublicOrigin             string `json:"publicOrigin"`
	CoreSHA256               string `json:"coreSha256"`
	ProviderFixture          bool   `json:"providerFixture,omitempty"`
}

func encoded(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func canonicalHex(s string, size int) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == size && hex.EncodeToString(b) == s
}

func manifestDigest(m Manifest) string {
	// Core binary hash changes across in-place package upgrades without altering the network boundary.
	m.CoreSHA256 = ""
	return hash(encoded(m))
}

func validateEndpoint(listen, origin string) error {
	h, p, err := net.SplitHostPort(listen)
	n, portErr := strconv.Atoi(p)
	if err != nil || h != "127.0.0.1" || portErr != nil || n < 1024 || n > 65535 || strconv.Itoa(n) != p {
		return errors.New("Remote requires 127.0.0.1 and an explicit unprivileged port")
	}
	if origin == "" {
		return nil
	}
	if _, _, err := tailnet.Origin(origin); err != nil {
		return errors.New("exact private HTTPS origin required")
	}
	return nil
}

func (m Manifest) validate() error {
	if (m.ApplicationSequence == 0) != (m.ApplicationReleaseSHA256 == "") ||
		(m.ApplicationReleaseSHA256 != "" && !canonicalHex(m.ApplicationReleaseSHA256, 32)) {
		return errors.New("invalid Remote application release identity")
	}
	if m.Version != 1 || m.Phase != packageRemotePhase || !canonicalHex(m.ID, 16) || !canonicalHex(m.CoreSHA256, 32) ||
		m.OwnerUID <= 0 || m.OwnerGID <= 0 || m.AppUID <= 0 || m.AppGID <= 0 ||
		m.AppUID == m.OwnerUID || m.AppGID == m.OwnerGID || m.AppUser == "" {
		return errors.New("invalid Remote package network identity")
	}
	root, err := update.PackageInstanceRoot(m.OwnerUID, "remote", m.ID)
	if err != nil || m.Root != root || m.Data != root+"/app-state" ||
		m.Core != root+"/fased-app" || m.Config != root+"/install.json" {
		return errors.New("invalid Remote package paths")
	}
	unit, err := update.PackageInstanceUnit(m.OwnerUID, "remote", m.ID, "app")
	if err != nil || m.Unit != unit || m.UnitPath != "/etc/systemd/system/"+unit || m.PublicOrigin == "" {
		return errors.New("invalid Remote package service binding")
	}
	return validateEndpoint(m.Listen, m.PublicOrigin)
}
