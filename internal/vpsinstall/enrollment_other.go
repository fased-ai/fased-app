//go:build !linux

package vpsinstall

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"io"
)

func EnrollmentLease(context.Context, Manifest, *custodyinstall.System) (io.Closer, error) {
	return nil, errors.New("enrollment requires Ubuntu")
}
func ValidateEnrollment(context.Context, Manifest, *custodyinstall.System) error {
	return errors.New("enrollment requires Ubuntu")
}
func EnrollmentAutostart(Manifest) (bool, error) {
	return false, errors.New("enrollment requires Ubuntu")
}
func EnrollmentStatus(context.Context, Manifest, *custodyinstall.System) (ServiceStatus, error) {
	return ServiceStatus{}, errors.New("enrollment requires Ubuntu")
}
func SuspendEnrollment(context.Context, Manifest, *custodyinstall.System) error {
	return errors.New("enrollment requires Ubuntu")
}
func RestoreEnrollment(context.Context, Manifest, bool, bool, *custodyinstall.System) error {
	return errors.New("enrollment requires Ubuntu")
}
