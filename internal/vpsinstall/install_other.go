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

func SetupLink(context.Context, Manifest, *custodyinstall.System, io.Writer) error {
	return unsupported
}

func ApplicationAcquisitionParent(Manifest) (string, error) { return "", unsupported }

func ChangeAccess(context.Context, Manifest, string, *custodyinstall.System, func(context.Context, Manifest) error) (Manifest, error) {
	return Manifest{}, unsupported
}
