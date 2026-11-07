package bootstrap

import (
	"encoding/base64"
)

// ValidWalletEnrollment validates public intent, not owner or spending authority.
func ValidWalletEnrollment(instance, network string) bool {
	b, err := base64.RawURLEncoding.DecodeString(instance)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == instance && (network == "devnet" || network == "mainnet-beta")
}

// WalletPayload is compiled into the authenticated installer itself. It is not
// accepted from CLI/configuration. Ordinary archives retain their old inventory.
type WalletPayload struct {
	Data                    []byte
	SHA256, Version, Source string
}
