//go:build !linux

package update

import (
	"context"
	"errors"
)

type InstalledInspectionRole struct{}

func VerifyInstalledInspectionGuard(string, string) (FixtureInspectionFence, error) {
	return FixtureInspectionFence{}, errors.New("installed inspection fixtures require Linux amd64")
}
func OpenInstalledInspectionFixture(string, string) (*InstalledInspectionRole, error) {
	return nil, errors.New("installed inspection fixtures require Linux amd64")
}
func InitializeInstalledInspectionFixture(context.Context, string, string) error {
	return errors.New("installed inspection fixtures require Linux amd64")
}
func (*InstalledInspectionRole) Close() {}
