//go:build !linux

package custodyinstall

import "context"

func CanRecoverEnrollmentCore(Manifest, string, string) bool { return false }
func (*System) RecoverEnrollmentCore(context.Context, Manifest, string, string, string) (Manifest, error) {
	return Manifest{}, unsupported
}
