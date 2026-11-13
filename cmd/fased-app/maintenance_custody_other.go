//go:build !linux

package main

import (
	"errors"
	"io"
)

func metadataMaintenanceCommand([]string, io.Reader, io.Writer) error {
	return errors.New("protected custody maintenance requires Linux")
}
