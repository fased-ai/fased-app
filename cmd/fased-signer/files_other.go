//go:build !linux

package main

import "errors"

func writeOwnerFile(string, []byte) error  { return errors.New("signer unsupported on this OS") }
func readOwnerFile(string) ([]byte, error) { return nil, errors.New("signer unsupported on this OS") }
