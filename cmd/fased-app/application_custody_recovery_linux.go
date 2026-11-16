//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/desktop"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

// An offline Local owner can request only the existing operation; the same
// protected worker used by the core validates and resumes it under its lock.
func recoverEnrolledOffline(ctx context.Context, dir string, out, _ io.Writer) error {
	owner := os.Getuid()
	if owner <= 0 || os.Geteuid() != owner {
		return errors.New("custody recovery requires the installation owner")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	root, packaged, err := desktop.PackageLocalDataRoot(abs, owner)
	if err != nil {
		return err
	}
	if packaged {
		m, err := custodyinstall.LoadPackageInstance(owner, root)
		if err != nil {
			return err
		}
		a, err := maintenance.ReadPackageAuthority(owner, m.Location, m.ID)
		if err != nil || maintenance.ValidateOwnerIdentity(a) != nil || maintenance.VerifyImage(a) != nil {
			return errors.New("original Local package maintenance authority required")
		}
		if _, err = custodyinstall.ReadEnrollmentCommit(m); err != nil {
			return err
		}
		r, err := update.ReadPackageMaintenanceRecord(owner, m.Location, m.ID)
		if err != nil {
			return err
		}
		id, err := custodyinstall.PackageRecoveryOperation(m, r)
		if err != nil {
			return err
		}
		if err = packageMaintenanceWorker(owner, m.Location, m.ID).resumeWorker(ctx, id); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "Recovery requested. Check Updates for the result.")
		return err
	}
	q, err := enrollment.EnrolledRecoveryRequest(dir, owner)
	if err != nil {
		return err
	}
	a, err := maintenance.ReadAuthority(owner)
	if err != nil || a.Profile != "local" {
		return errors.New("original Local maintenance authority required")
	}
	if err = maintenance.VerifyImage(a); err != nil {
		return err
	}
	c := custodyMaintenanceController{owner: owner}
	if err = c.resumeWorker(ctx, q.OperationID); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Recovery requested. Check Updates for the result.")
	return err
}
