//go:build !linux

package main

import (
	"context"
	"errors"
)

func registerPackageWalletShortcut(context.Context, string) error {
	return errors.New("Local package Wallets requires Linux")
}
