//go:build !linux

package enrollment

import (
	"context"
	"errors"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func Run(context.Context, Source, Options, *custodyinstall.System) (custodyinstall.Manifest, error) {
	return custodyinstall.Manifest{}, errors.New("same-instance custody enrollment requires Ubuntu")
}
func EnrolledTarget(string, int) (custodyinstall.Manifest, error) {
	return custodyinstall.Manifest{}, errors.New("same-instance custody enrollment requires Ubuntu")
}

func RunControlledOwnerCustody(context.Context, Source, Options, *custodyinstall.System) (custodyinstall.Manifest, error) {
	return custodyinstall.Manifest{}, errors.New("ordinary custody enrollment requires Linux")
}
