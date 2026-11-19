//go:build !linux

package bootstrap

import (
	"context"
	"errors"

	"github.com/fased-ai/fased-app/internal/localinstall"
)

func PrepareLegacyLocalHandoff(context.Context, Options, string, string, localinstall.Paths, string) (LocalHandoffStage, error) {
	return LocalHandoffStage{}, errors.New("Local handoff preparation requires Linux")
}
