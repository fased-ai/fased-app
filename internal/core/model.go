package core

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

const APIVersion = "1"

var Version = "0.0.0-core.1" // Set by the reviewed release build; not a runtime option.

func DefaultDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "fased-app"), nil
}

var ErrConflict = errors.New("settings changed in another client; reload and review before retrying")
var ErrUnauthorized = errors.New("authentication failed")
var ErrClaimed = errors.New("this instance is already claimed")
var ErrRateLimited = errors.New("too many attempts; try again later")

type Setup struct {
	Step       int                `json:"step"`
	Complete   bool               `json:"complete"`
	Host       string             `json:"host"`
	WalletMode string             `json:"walletMode"`
	Provider   string             `json:"provider"`
	Updates    *UpdatePreferences `json:"updates,omitempty"`
}

func DefaultSetup() Setup { return Setup{Host: "local", WalletMode: "skip", Provider: "none"} }
func (s Setup) Validate() error {
	if s.Step < 0 || s.Step > 3 {
		return errors.New("invalid setup step")
	}
	if s.Host != "local" && s.Host != "vps" {
		return errors.New("invalid deployment preference")
	}
	if !oneOf(s.WalletMode, "skip", "watch", "external", "automation") {
		return errors.New("invalid wallet preference")
	}
	if !oneOf(s.Provider, "none", "openai", "anthropic", "xai") {
		return errors.New("invalid AI preference")
	}
	return nil
}
func oneOf(value string, choices ...string) bool {
	for _, c := range choices {
		if value == c {
			return true
		}
	}
	return false
}

type SetupRecord struct {
	Revision int64 `json:"revision"`
	Settings Setup `json:"settings"`
}
type ConnectionInfo struct {
	RemoteReady     bool   `json:"remoteReady,omitempty"`
	Location        string `json:"location"`
	Transport       string `json:"transport"`
	RemoteTransport string `json:"remoteTransport,omitempty"`
	OwnerUID        int    `json:"ownerUid,omitempty"` // Protected owner terminal handoff; never authorization.
	OwnerKind       string `json:"ownerKind,omitempty"`
}

type Health struct {
	Connection   *ConnectionInfo `json:"connection,omitempty"`
	Status       string          `json:"status"`
	APIVersion   string          `json:"apiVersion"`
	Version      string          `json:"version"`
	InstanceID   string          `json:"instanceId"`
	Capabilities map[string]bool `json:"capabilities"`
}

func (s *Store) aiConnectionsCapability() bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	return providerActivationEnabled && s.provider.apiCredentials
}

var stage4CapabilitiesHook func(*Store) (walletSigner, financialExecution, updater bool)

func (s *Store) Health() Health {
	walletSigner, financialExecution, updater := false, false, false
	if stage4CapabilitiesHook != nil {
		walletSigner, financialExecution, updater = stage4CapabilitiesHook(s)
	}
	return Health{Status: "ok", APIVersion: APIVersion, Version: Version, InstanceID: s.instanceID, Capabilities: map[string]bool{"persistentSetup": true, "ownerAuthentication": true, "walletSigner": walletSigner, "financialExecution": financialExecution, "aiConnections": s.aiConnectionsCapability(), "updater": updater}}
}

type Endpoint struct {
	URL             string `json:"url"`
	InstanceID      string `json:"instanceId"`
	Network         string `json:"network"`
	Address         string `json:"address"`
	PublicOrigin    string `json:"publicOrigin,omitempty"`
	RemoteTransport string `json:"remoteTransport,omitempty"`
}
type ClaimCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}
