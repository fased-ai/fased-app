//go:build linux

package custodyinstall

import (
	"errors"
	"os"
)

// InstalledRemoteAccountsIdentity is separate from update-session authority.
// The existing owner-approved setpriv helper must be the immutable app itself.
func InstalledRemoteAccountsIdentity(owner int, image, data string) (Manifest, string, error) {
	m, err := Load(owner)
	if err != nil {
		return Manifest{}, "", err
	}
	if err = validateRemoteAccountsIdentity(m, os.Getuid(), os.Geteuid(), os.Getgid(), image, data); err != nil {
		return Manifest{}, "", err
	}
	instance, err := nativeUpdateSessionInstance(m, data)
	return m, instance, err
}

func validateRemoteAccountsIdentity(m Manifest, uid, euid, gid int, image, data string) error {
	if m.validate() != nil || m.Version != 3 || !m.Enrollment || m.Location != "remote" || uid <= 0 || uid != m.App.UID || euid != uid || gid != m.App.GID || image != m.Root+"/fased-app" || data != m.Root+"/app-state" {
		return errors.New("exact installed Remote custody app required")
	}
	return nil
}
