//go:build !linux

package main

import (
	"errors"
	"io"
)

func nativeAppClient([]string, io.Reader, io.Writer) error {
	return errors.New("protected native client requires Linux")
}
