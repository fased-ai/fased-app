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
	v, e := Open(c.StateDir, c.SignerID)
	if e != nil {
		return nil, e
	}
	defer v.Close()
	return v.Installation(update.InstallRequest{Action: "status", IntentID: id})
}
