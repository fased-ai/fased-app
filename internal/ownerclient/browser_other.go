//go:build !linux

package ownerclient

import (
	"context"
	"errors"
)

func OpenLocalBrowser(context.Context, string) error {
	return errors.New("Local protected Wallets requires Linux")
}
