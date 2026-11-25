//go:build linux

package main

import (
	"errors"
	"io"

	"github.com/fased-ai/fased-app/internal/signer"
)

func ordinaryEnrollmentSupported() bool {
	return signer.OrdinaryOwnerAdmissionEnabled()
}

func enrollmentCommand([]string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("legacy in-place custody enrollment has been retired; use the Format-4 package installer")
}
