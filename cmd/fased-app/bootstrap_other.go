//go:build !linux

package main

import (
	"errors"
	"io"
	"os"
)

func bootstrapCommand([]string, *os.File, io.Writer, io.Writer) error {
	return errors.New("Linux disposable bootstrap only")
}
func ownerBootstrapCommand([]string, *os.File, io.Writer, io.Writer) error {
	return errors.New("Linux disposable bootstrap only")
}
