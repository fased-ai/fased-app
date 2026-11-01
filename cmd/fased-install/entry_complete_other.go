//go:build !linux

package main

import (
	"context"
	"errors"
	"io"
)

func completeInstalledEntry(string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("installed update entry requires its supported platform adapter")
}

func signInForEntryUpgrade(context.Context, string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("use the supported installed update adapter")
}
