//go:build !linux

package main

import (
	"context"
	"errors"
)

func launchOwnerEnrollment(context.Context, string, []string) error {
	return errors.New("protected enrollment requires a supported native owner client")
}
