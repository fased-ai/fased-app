//go:build !linux

package core

import (
	"context"
	"errors"
)

func CheckApplicationRollbackMetadata(context.Context, string, string, int) error {
	return errors.New("supported Ubuntu owner controller required")
}

func ReadApplicationOwnerOffline(context.Context, string, string) (ApplicationOwnerView, error) {
	return ApplicationOwnerView{}, errors.New("supported Ubuntu owner controller required")
}
