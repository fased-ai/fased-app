//go:build !linux

package main

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/signer"
	"io"
)

func ownerWindow(context.Context, signer.Config, string, io.Reader, io.Writer) error {
	return errors.New("protected wallet owner input requires Linux")
}
