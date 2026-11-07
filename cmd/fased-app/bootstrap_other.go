//go:build !linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
)

func bootstrapCustodyInventoryCommand(context.Context, string, int, string, string, string, io.Writer) error {
	return errors.New("protected custody bootstrap inventory requires Linux")
}

func bootstrapCommand([]string, *os.File, io.Writer, io.Writer) error {
	return errors.New("Linux disposable bootstrap only")
}
func ownerBootstrapCommand([]string, *os.File, io.Writer, io.Writer) error {
	return errors.New("Linux disposable bootstrap only")
}
