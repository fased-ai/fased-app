//go:build !linux

package main

import (
	"errors"
	"io"
)

func settleWalletPredecessorCommand([]string, io.Writer, io.Writer) error {
	return errors.New("protected wallet predecessor repair requires Linux")
}
