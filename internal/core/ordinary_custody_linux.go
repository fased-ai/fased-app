//go:build linux

package core

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
	"os"
)

func ordinaryCustodySettled(ctx context.Context, c snapshotReader, directory string, o ApplicationUpdateOperation) error {
	phase, pause, err := readPause(ctx, c)
	if err != nil {
		return err
	}
	return ordinaryCustodySettledWithPause(ctx, c, directory, o, phase, pause)
}

func ordinaryCustodySettledWithPause(ctx context.Context, c snapshotReader, directory string, o ApplicationUpdateOperation, phase string, pause pauseRecord) error {
	if !update.IDOK(o.ID) {
		return ErrCorePaused
	}
	if o.Phase == "check-failed" {
		if !update.IDOK(o.OwnerLease) || o.Release == nil || o.Receipt != nil || o.Failure != "Release is no longer eligible. Check for updates again." {
			return ErrCorePaused
		}
		if phase == "paused" {
			return ErrCorePaused
		}
		fd, err := recoveryDirectoryMode(directory, o.ID, false)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		_, err = recoveryRead(fd, "application.json", 16384)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return ErrCorePaused
	}
	var instance string
	if err := c.QueryRowContext(ctx, "SELECT instance_id FROM instance WHERE id=1").Scan(&instance); err != nil {
		return err
	}
	fd, err := recoveryDirectoryMode(directory, o.ID, false)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	raw, err := recoveryRead(fd, "application.json", 16384)
	if err != nil {
		return err
	}
	var checkpoint ApplicationCheckpoint
	if update.StrictJSON(raw, &checkpoint) != nil || !hexValue(checkpoint.Snapshot.SHA256, 32) || checkpoint.Snapshot.File != "application-"+checkpoint.Snapshot.SHA256+".sqlite" || checkpoint.Snapshot.Size <= 0 || checkpoint.Snapshot.Size > MaxEnrollmentFileBytes {
		return errors.New("preserve the original application checkpoint")
	}
	backup, err := recoveryRead(fd, checkpoint.Snapshot.File, MaxEnrollmentFileBytes)
	if err != nil {
		return err
	}
	prefix := "lifecycle/" + o.ID + "/"
	return enrollmentApplicationUpdates(ctx, c, instance, phase, pause, map[string][]byte{prefix + "application.json": raw, prefix + checkpoint.Snapshot.File: backup})
}
