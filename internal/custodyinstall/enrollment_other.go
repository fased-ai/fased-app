//go:build !linux

package custodyinstall

import (
	"context"
	"io"
)

func ReadEnrollmentCommit(Manifest) (EnrollmentCommit, error) { return EnrollmentCommit{}, unsupported }
func ActivateEnrollment(context.Context, Manifest, string, string, bool, bool, *System) error {
	return unsupported
}
func (*System) EnrollmentExport(context.Context, Manifest, int, int, string, string) (*EnrollmentProcess, error) {
	return nil, unsupported
}
func (*System) EnrollmentImage(context.Context, Manifest, string, string, string, string, io.Reader, io.Writer) error {
	return unsupported
}
func (*System) EnrollmentSignerEmpty(context.Context, Manifest) error          { return unsupported }
func (*System) EnrollmentRecord(context.Context, string, string, []byte) error { return unsupported }
