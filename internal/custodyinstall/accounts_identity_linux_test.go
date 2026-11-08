//go:build linux

package custodyinstall

import "testing"

func TestRemoteAccountsIdentityExactOwnerAndInstallation(t *testing.T) {
	tx, _, _, _ := ordinaryBootstrapModel(t, true)
	m := tx.Base
	for _, bad := range []string{"", "owner-uid", "root", "gid", "local", "no-enrollment", "version", "image", "data", "foreign-root"} {
		t.Run(bad, func(t *testing.T) {
			c := m
			uid, euid, gid := m.App.UID, m.App.UID, m.App.GID
			image, data := m.Root+"/fased-app", m.Root+"/app-state"
			switch bad {
			case "owner-uid":
				uid = m.Owner
			case "root":
				uid = 0
				euid = 0
			case "gid":
				gid++
			case "local":
				c.Location = "local"
				c.PublicOrigin = ""
			case "no-enrollment":
				c.Enrollment = false
			case "version":
				c.Version = 2
			case "image":
				image = m.Root + "/copied-app"
			case "data":
				data += "-other"
			case "foreign-root":
				c.Root += "-other"
			}
			if err := validateRemoteAccountsIdentity(c, uid, euid, gid, image, data); (err == nil) != (bad == "") {
				t.Fatal("identity admission", err)
			}
		})
	}
}
