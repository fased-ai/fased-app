//go:build !linux

package signer

import "github.com/fased-ai/fased-app/internal/update"

func CommitOrdinaryReaderHandoff(Config, string) (*update.SignerSnapshot, error) {
	return nil, ErrUnavailable
}
