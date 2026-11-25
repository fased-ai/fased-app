//go:build !linux

package vpsnetwork

import (
	"errors"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

var errUnsupportedOS = errors.New("Remote network lockdown is only supported on Linux")

// FromPackageManifest projects a verified Format-4 Remote package instance
// into the network lockdown controller.
func FromPackageManifest(custodyinstall.Manifest) (Manifest, error) {
	return Manifest{}, errUnsupportedOS
}

// Load resolves an owner's immutable Format-4 Remote package instance.
func Load(int, string) (Manifest, error) {
	return Manifest{}, errUnsupportedOS
}
