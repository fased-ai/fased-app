//go:build windows

package ownerclient

import "errors"

func protectRoot(string) error {
	return errors.New("protected wallet owner window requires a supported OS-owner adapter")
}
