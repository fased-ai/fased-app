//go:build !linux

package main

import (
	"errors"
	"io"
	"os"
)

func inspectionFixtureCommand([]string, *os.File, io.Writer, io.Writer) error {
	return errors.New("installed inspection fixtures require Linux amd64; wallets disabled")
}
