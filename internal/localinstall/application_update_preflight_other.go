//go:build !linux

package localinstall

import (
	"context"
	"errors"
)

func updateServicePreflight(context.Context, Manifest) error {
	return errors.New("application updates currently support Linux only")
}
