//go:build !linux

package vpsinstall

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"io"
)

var unsupported = errors.New("dedicated Remote installation requires Ubuntu 24.04")

func Install(context.Context, Options, *custodyinstall.System) (Manifest, error) {
	return Manifest{}, unsupported
}
func Load(int) (Manifest, error)                                     { return Manifest{}, unsupported }
func Start(context.Context, Manifest, *custodyinstall.System) error  { return unsupported }
func Stop(context.Context, Manifest, *custodyinstall.System) error   { return unsupported }
func Remove(context.Context, Manifest, *custodyinstall.System) error { return unsupported }
func Claim(context.Context, Manifest, *custodyinstall.System, bool, io.Writer) error {
	return unsupported
}
func Status(context.Context, Manifest, *custodyinstall.System) (ServiceStatus, error) {
	return ServiceStatus{}, unsupported
}
func CheckApplication(string, string, string, string, string) error { return unsupported }
