//go:build !linux

package main

import (
	"context"
	"errors"
	"io"
)

func recoverEnrolledOffline(context.Context, string, io.Writer, io.Writer) error {
	return errors.New("custody recovery requires Linux")
}
