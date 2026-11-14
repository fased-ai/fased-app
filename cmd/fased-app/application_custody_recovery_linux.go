//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/maintenance"
)

// An offline Local owner can request only the existing operation; the same
// protected worker used by the core validates and resumes it under its lock.
func recoverEnrolledOffline(ctx context.Context, dir string, out, _ io.Writer) error {
	owner := os.Getuid()
	if owner <= 0 || os.Geteuid() != owner {
		return errors.New("custody recovery requires the installation owner")
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
