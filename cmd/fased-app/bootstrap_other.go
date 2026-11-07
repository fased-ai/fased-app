//go:build !linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
)

func bootstrapLocalInventoryCommand(context.Context, string, int, string, string, string, io.Writer) error {
	return errors.New("protected Local bootstrap inventory requires Linux")
}

func bootstrapCommand([]string, *os.File, io.Writer, io.Writer) error {
	return errors.New("Linux disposable bootstrap only")
}
func ownerBootstrapCommand([]string, *os.File, io.Writer, io.Writer) error {
	return errors.New("Linux disposable bootstrap only")
}
