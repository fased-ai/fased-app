//go:build !linux

package main

import (
	"errors"
	"io"
)

func runHostMigrateUnits(string, string, io.Writer, io.Writer) error {
	return errors.New("host unit migration is only supported on Linux")
}
