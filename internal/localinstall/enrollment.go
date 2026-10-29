package localinstall

import (
	"context"
	"io"
)

// EnrollmentLease serializes the existing installer while another owner
// conductor changes data ownership. It does not grant that conductor new rights.
func EnrollmentLease(paths Paths) (Manifest, io.Closer, error) { return locked(paths) }
func ValidateEnrollment(m Manifest) error                      { return verify(m) }
func SuspendEnrollment(ctx context.Context, m Manifest) error {
	if e := verify(m); e != nil {
		return e
	}
	return unregister(ctx, m)
}
func RestoreEnrollment(ctx context.Context, m Manifest, running, autostart bool) error {
	if e := verify(m); e != nil {
		return e
	}
	if e := unregister(ctx, m); e != nil {
		return e
	}
	if e := register(ctx, m); e != nil {
		return e
	}
	if autostart {
		if e := enable(ctx, m); e != nil {
			return e
		}
	}
	if running {
		return start(ctx, m)
	}
	return nil
}
