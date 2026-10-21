//go:build !linux

package main

import (
	"github.com/fased-ai/fased-app/internal/signer"
	"io"
	"os"
)

func portableCommand([]string, *os.File, io.Writer, io.Writer) error { return signer.ErrUnavailable }
