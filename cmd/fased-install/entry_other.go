//go:build !linux

package main

import (
	"context"
	"errors"
	"io"
)

func installedEntry(context.Context, string, string, io.Reader, io.Writer, io.Writer) (bool, error) {
	return false, errors.New("Remote entry requires a supported Ubuntu release and architecture")
}
