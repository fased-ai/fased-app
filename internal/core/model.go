package core

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

const APIVersion = "1"
const Version = "0.0.0-core.1"

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
type Health struct {
	Status       string          `json:"status"`
	APIVersion   string          `json:"apiVersion"`
	Version      string          `json:"version"`
	InstanceID   string          `json:"instanceId"`
	Capabilities map[string]bool `json:"capabilities"`
}

func (s *Store) Health() Health {
	return Health{Status: "ok", APIVersion: APIVersion, Version: Version, InstanceID: s.instanceID, Capabilities: map[string]bool{"persistentSetup": true, "ownerAuthentication": true, "walletSigner": false, "financialExecution": false, "aiConnections": false, "updater": false}}
}

type Endpoint struct {
	URL        string `json:"url"`
	InstanceID string `json:"instanceId"`
	Network    string `json:"network"`
	Address    string `json:"address"`
}
type ClaimCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}
