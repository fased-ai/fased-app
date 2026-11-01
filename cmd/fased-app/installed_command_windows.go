//go:build windows

package main

import (
	"errors"
	"io"
)

// Native Windows command installation stays with its native package adapter.
func installCommand(string, string, string, bool, io.Writer) error { return nil }

func removeCommand(string, string, string, bool) error { return nil }

func verifiedInstalledCommand(string, string, string, string, bool) (string, error) {
	return "", errors.New("use the native installed command")
}
