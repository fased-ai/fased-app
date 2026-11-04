//go:build !linux || !fased_candidate_acceptance

package main

import (
	"errors"
	"io"
)

func candidateAcceptanceCommand(_ []string, _, _ io.Writer) error {
	return errors.New("candidate acceptance is not included in ordinary Fased builds")
}
