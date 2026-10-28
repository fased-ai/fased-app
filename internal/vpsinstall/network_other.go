//go:build !linux

package vpsinstall

import (
	"context"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func BeginNetwork(context.Context, Manifest, *custodyinstall.System, NetworkApproval) (NetworkStatus, error) {
	return NetworkStatus{}, unsupported
}
func ConfirmNetwork(context.Context, Manifest, *custodyinstall.System, string) (NetworkStatus, error) {
	return NetworkStatus{}, unsupported
}
func RollbackNetwork(context.Context, Manifest, *custodyinstall.System) (NetworkStatus, error) {
	return NetworkStatus{}, unsupported
}
func InspectNetwork(context.Context, Manifest, *custodyinstall.System) (NetworkStatus, error) {
	return NetworkStatus{}, unsupported
}
