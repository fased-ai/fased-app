//go:build !linux

package vpsinstall

import (
	"context"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

func ApplicationReaderInventory(context.Context, Manifest, string, *custodyinstall.System) (update.CoreSnapshot, error) {
	return update.CoreSnapshot{}, unsupported
}
func ApplicationReaderSignIn(context.Context, Manifest, *custodyinstall.System) error {
	return unsupported
}
