//go:build !linux

package main

import (
	"context"
	"errors"
	"io"

	"github.com/fased-ai/fased-app/internal/bootstrap"
)

func stageAttestedCandidate(context.Context, *bootstrap.Prepared, string, string, io.Writer) error {
	return errors.New("protected candidate staging requires Linux")
}

func candidateUpdateBaseline() (string, error) {
	return "", errors.New("protected candidate staging requires Linux")
}
