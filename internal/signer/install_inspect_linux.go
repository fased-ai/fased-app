//go:build linux

package signer

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
	"os"
)

// InspectInstallation is a closed cold-recovery probe. It runs only as the
// configured signer UID, takes the real vault lock and never unlocks or signs.
func InspectInstallation(c Config, id string) (*update.Installation, error) {
	return inspectInstallation(c, id, false)
}

// InspectInstallationReadOnly refuses pending publication rather than repairing
// it; a privileged replacement check must never change signer recovery state.
func InspectInstallationReadOnly(c Config, id string) (*update.Installation, error) {
	return inspectInstallation(c, id, true)
}

func inspectInstallation(c Config, id string, readOnly bool) (*update.Installation, error) {
	v, e := inspectVaultMode(c, readOnly)
	if e != nil {
		return nil, e
	}
	defer v.Close()
	return v.Installation(update.InstallRequest{Action: "status", IntentID: id})
}
func inspectVault(c Config) (*Vault, error) { return inspectVaultMode(c, false) }
func inspectVaultMode(c Config, readOnly bool) (*Vault, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if os.Getuid() != c.SignerUID || os.Geteuid() != c.SignerUID {
		return nil, errors.New("offline inspection requires the configured non-root signer identity")
	}
	if e := VerifyExecutable(); e != nil {
		return nil, e
	}
	if e := protectedDirectory(c.StateDir, c.SignerUID, 0700); e != nil {
		return nil, e
	}
	if e := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); e != nil {
		return nil, e
	}
	if e := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); e != nil {
		return nil, e
	}
	network := c.Network
	if c.Version == 3 {
		if c.Provenance == ControlledCustodyProfile {
			if c.Profile == OwnerCustodyProfile {
				network = ordinaryOwnerTestNamespace + network
			} else {
				network = ordinaryNamespace + network
			}
		} else if c.Provenance == OwnerKeyProvenance && OrdinaryOwnerAdmissionEnabled() {
			network = ordinaryOwnerNamespace + network
		} else {
			return nil, ErrUnavailable
		}
	}
	v, e := openVaultNetwork(c.StateDir, c.SignerID, readOnly, network)
	if e != nil {
		return nil, e
	}
	return v, nil
}

func InspectUpdate(c Config, id string) (Response, error) {
	v, e := inspectVaultMode(c, true)
	if e != nil {
		return Response{}, e
	}
	defer v.Close()
	if (c.Version == 2 || c.Version == 3) && id == "" {
		if id != "" {
			return Response{}, ErrUnavailable
		}
		s, e := v.UpdateInventory(update.NewProbe())
		return Response{UpdateInventory: s, Status: v.Status()}, e
	}
	i, e := v.UpdateIntent(id)
	if e != nil {
		return Response{}, e
	}
	s, e := v.UpdateInventory(update.NewProbe())
	return Response{UpdateIntent: i, UpdateInventory: s, Status: v.Status()}, e
}
