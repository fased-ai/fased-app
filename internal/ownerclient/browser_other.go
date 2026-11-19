//go:build !linux

package ownerclient

import (
	"context"
	"errors"
)

func OpenLocalBrowser(context.Context, string) error {
	return errors.New("Local protected Wallets requires Linux")
}

func OpenRemoteBrowser(context.Context, string, string, string) error {
	return errors.New("protected Remote Wallets requires the Linux owner client")
}
