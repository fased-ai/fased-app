//go:build !linux

package vpsinstall

import (
	"context"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func PrepareSourceSession(context.Context, Manifest, string, *custodyinstall.System) error {
	return unsupported
}
func ValidateSourceSessionHelper(string, string, string, int) (Manifest, error) {
	return Manifest{}, unsupported
}
