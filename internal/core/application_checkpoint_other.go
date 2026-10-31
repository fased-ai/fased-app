//go:build !linux

package core

import (
	"context"
	"errors"
)

func (*Store) PrepareApplicationCheckpoint(context.Context, string) (ApplicationCheckpoint, error) {
	return ApplicationCheckpoint{}, errors.New("application update checkpoints require the supported Ubuntu controller")
}
func (*Store) CompleteApplicationCheckpoint(context.Context, string, ApplicationUpdateReceipt) error {
	return errors.New("application update checkpoints require the supported Ubuntu controller")
}

func runningApplicationSHA256() (string, error) {
	return "", errors.New("supported Ubuntu owner controller required")
}
