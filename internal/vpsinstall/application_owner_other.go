//go:build !linux

package vpsinstall

import (
	"context"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func ApplicationOwnerCall(context.Context, Manifest, core.ApplicationOwnerCommand, *custodyinstall.System) (core.ApplicationOwnerView, error) {
	return core.ApplicationOwnerView{}, unsupported
}

func ValidateApplicationOwnerHelper(string, string, int) (Manifest, error) {
	return Manifest{}, unsupported
}

func LoadApplicationMaintenance(int, string) (Manifest, error) {
	return Manifest{}, unsupported
}

func ValidateApplicationReaderOwnerRequest(Manifest, string, core.ApplicationReaderBootstrapRequest) error {
	return unsupported
}

func ApplicationReaderBootstrapTemplate(Manifest, string, string) (core.ApplicationReaderBootstrapRequest, error) {
	return core.ApplicationReaderBootstrapRequest{}, unsupported
}
