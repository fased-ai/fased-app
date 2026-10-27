//go:build !linux

package signer

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
)

func InspectInstallation(Config, string) (*update.Installation, error) {
	return nil, errors.New("Linux disposable cold installation inspection only")
}

func InspectUpdate(c Config, id string) (Response, error) {
	return Response{}, errors.New("cold update inspection requires Linux")
}
