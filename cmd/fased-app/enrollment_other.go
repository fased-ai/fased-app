//go:build !linux

package main

import (
	"errors"
	"io"
)

func ordinaryEnrollmentSupported() bool { return false }

func enrollmentCommand([]string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("protected enrollment requires Ubuntu")
}
