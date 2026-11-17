//go:build !linux

package main

import "errors"

func dispatchPackageInstance([]string) error { return nil }

func redirectLocalPackageWallet(string, string, string) error {
	return errors.New("Local protected Wallets requires Linux")
}

func defaultPackageLocalData(fallback string, _ bool) (string, error) { return fallback, nil }
