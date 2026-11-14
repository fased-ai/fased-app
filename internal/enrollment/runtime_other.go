//go:build !linux

package enrollment

import (
	"context"
	"errors"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
)

func Run(context.Context, Source, Options, *custodyinstall.System) (custodyinstall.Manifest, error) {
	return custodyinstall.Manifest{}, errors.New("same-instance custody enrollment requires Ubuntu")
}
func EnrolledTarget(string, int) (custodyinstall.Manifest, error) {
	return custodyinstall.Manifest{}, errors.New("same-instance custody enrollment requires Ubuntu")
}
func EnrolledRecoveryRequest(string, int) (maintenance.CustodyUpdateRequest, error) {
	return maintenance.CustodyUpdateRequest{}, errors.New("custody recovery requires Linux")
}

func RunControlledOwnerCustody(context.Context, Source, Options, *custodyinstall.System) (custodyinstall.Manifest, error) {
	return custodyinstall.Manifest{}, errors.New("ordinary custody enrollment requires Linux")
}
