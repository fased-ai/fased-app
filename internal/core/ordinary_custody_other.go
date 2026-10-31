//go:build !linux

package core

import "context"

func ordinaryCustodySettled(context.Context, snapshotReader, string, ApplicationUpdateOperation) error {
	return ErrCorePaused
}
