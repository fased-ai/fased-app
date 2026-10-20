//go:build !linux

package main

import "errors"
import "github.com/fased-ai/fased-app/internal/signer"

func preflightOwnerFile(string) error { return errors.New("signer unsupported on this OS") }

func writeOwnerFile(string, []byte) error  { return errors.New("signer unsupported on this OS") }
func readOwnerFile(string) ([]byte, error) { return nil, errors.New("signer unsupported on this OS") }

func readPublicProposal(string) ([]byte, error) { return nil, signer.ErrUnavailable }
