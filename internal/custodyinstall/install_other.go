//go:build !linux

package custodyinstall

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
)

type System struct {
	In             io.Reader
	Out            io.Writer
	NonInteractive bool
}

var unsupported = errors.New("installer-managed custody identities are supported only by the Ubuntu fixture; wallets remain disabled")

func Install(context.Context, Options, *System) (Manifest, error)                { return Manifest{}, unsupported }
func Load(int) (Manifest, error)                                                 { return Manifest{}, unsupported }
func Verify(Manifest) error                                                      { return unsupported }
func Runtime(Manifest) error                                                     { return unsupported }
func Remove(context.Context, Manifest, *System) error                            { return unsupported }
func Control(context.Context, Manifest, string, string, *System) error           { return unsupported }
func (*System) AppHelper(context.Context, Manifest, bool, bool, io.Writer) error { return unsupported }
func (*System) UpdateStatus(context.Context, Manifest, io.Writer) error          { return unsupported }
func (*System) PrivateAccessProof(context.Context, Manifest, core.PrivateAccessProofRequest) (core.PrivateAccessProof, error) {
	return core.PrivateAccessProof{}, unsupported
}
func (*System) PreparePrivateAccessProof(context.Context, Manifest) error { return unsupported }
func (*System) UpdatePreferences(context.Context, Manifest, bool, io.Writer) error {
	return unsupported
}

func OwnerLock() (io.Closer, error)                            { return nil, unsupported }
func PinnedMaintenanceRunner(int) (string, error)              { return "", unsupported }
func ReusableMaintenanceSetupAvailable(Manifest) (bool, error) { return false, unsupported }
func VerifyReusableSetupLauncher(Manifest) error               { return unsupported }
func ReviewReusableMaintenanceSetup(Manifest, string, string) ([]byte, error) {
	return nil, unsupported
}
func PrepareReusableMaintenance(context.Context, Manifest, string, string, *System) error {
	return unsupported
}
func (*System) UseScopedMaintenance(int) error { return unsupported }

func (*System) AccountsHelper(context.Context, Manifest, bool, bool, bool, io.Writer) error {
	return unsupported
}
func (*System) AccountsReadHelper(context.Context, Manifest, io.Writer) error { return unsupported }
func (*System) AgentsHelper(context.Context, Manifest, string, bool, io.Writer) error {
	return unsupported
}

func (*System) UpdateInventory(context.Context, Manifest, string, io.Writer) error {
	return unsupported
}

func (*System) UpdatePause(context.Context, Manifest, update.CorePauseCommand, io.Writer) error {
	return unsupported
}

func (*System) UpdateBackup(context.Context, Manifest, update.Preflight, io.Writer) error {
	return unsupported
}

func (*System) UpdateRecovery(context.Context, Manifest, update.RecoveryCheck, io.Writer) error {
	return unsupported
}

func fixtureUnits(Manifest, bool) map[string][]byte { return nil }

func InstallControlledOwnerCustody(context.Context, Options, *System) (Manifest, error) {
	return Manifest{}, unsupported
}
