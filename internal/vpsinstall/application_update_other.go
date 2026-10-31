//go:build !linux

package vpsinstall

import (
	"context"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func encodedApplicationManifest(m Manifest) []byte { b, _ := json.Marshal(m); return b }
func ApplicationUpdateState(int) (ApplicationUpdateStatus, error) {
	return ApplicationUpdateStatus{}, unsupported
}
func ReplaceApplication(context.Context, Manifest, ApplicationUpdate, ApplicationUpdateCallbacks, *custodyinstall.System) (Manifest, error) {
	return Manifest{}, unsupported
}
func RollbackApplication(context.Context, Manifest, ApplicationUpdate, ApplicationUpdateCallbacks, *custodyinstall.System) (Manifest, error) {
	return Manifest{}, unsupported
}
