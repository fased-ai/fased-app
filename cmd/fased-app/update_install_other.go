//go:build !linux

package main

import (
	"errors"
	"io"
	"os"
)

func delegateInstallation([]string, *os.File, io.Writer, io.Writer) error {
	return errors.New("Linux disposable installation controller only")
}
func ownerInstallation(string, string, string, string, bool, *os.File, io.Writer, io.Writer) error {
	return errors.New("Linux disposable installation controller only")
}
