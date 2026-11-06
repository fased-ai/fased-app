//go:build !linux

package main

import (
	"errors"
	"io"
)

func installedProvider([]string, io.Writer) error {
	return errors.New("installed provider acceptance requires disposable Ubuntu")
}
