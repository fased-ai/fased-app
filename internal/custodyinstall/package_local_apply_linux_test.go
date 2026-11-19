//go:build linux

package custodyinstall

import (
	"errors"
	"strings"
	"testing"
)

func TestLegacyLocalApplyAdmitsOnlyOneOperation(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		recover, recorded, complete, held bool
		allowed                           bool
	}{
		{name: "first Apply", held: true, allowed: true},
		{name: "missing fence"},
		{name: "no authored Apply", recover: true, held: true},
		{name: "lost reply before release", recover: true, recorded: true, held: true, allowed: true},
		{name: "duplicate Apply before release", recorded: true, held: true},
		{name: "interruption after release", recover: true, recorded: true, allowed: true},
		{name: "duplicate Apply after release", recorded: true},
		{name: "completed readback", recover: true, recorded: true, complete: true, allowed: true},
		{name: "duplicate completed Apply", recorded: true, complete: true},
		{name: "unexpected fence after completion", recover: true, recorded: true, complete: true, held: true},
		{name: "completion without Apply", recover: true, complete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkLegacyLocalApplyAdmission(tc.recover, tc.recorded, tc.complete, tc.held)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v, error=%v", tc.allowed, err)
			}
		})
	}
}

func TestLegacyLocalApplyReconcilesOnlyVerifiedStaleUnits(t *testing.T) {
	m := Manifest{Layout: PackageLayout, Owner: 1000, Location: "local", ID: strings.Repeat("a", 32), Root: "/var/lib/example", App: Identity{UID: 1201, GID: 1201}}
	image := m.Root + "/fased-app"
	for _, tc := range []struct {
		name, freshness        string
		recover, reload, allow bool
		before, after          string
		reloadFails            bool
	}{
		{name: "interrupted enable", freshness: "yes", recover: true, reload: true, allow: true},
		{name: "already refreshed", freshness: "no", recover: true, allow: true},
		{name: "ordinary Apply or completed readback stays strict", freshness: "yes"},
		{name: "missing freshness", recover: true},
		{name: "foreign override in stale units", freshness: "yes", recover: true, before: "/etc/systemd/system/foreign.conf"},
		{name: "override exposed by reload", freshness: "yes", recover: true, reload: true, after: "/etc/systemd/system/foreign.conf"},
		{name: "reload failure stops recovery", freshness: "yes", recover: true, reload: true, reloadFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string]string{
				"FragmentPath": "/etc/systemd/system/" + instanceUnit(m, "app"),
				"User":         "1201", "Group": "1201", "NoNewPrivileges": "yes", "ProtectSystem": "strict",
				"ExecStart": "{ path=" + image + " ; }", "NeedDaemonReload": tc.freshness,
				"LimitCORE": "0", "Requires": packageRecoveryUnit(m), "After": packageRecoveryUnit(m),
				"DropInPaths": tc.before,
			}
			verify := func(stale *bool) error {
				return inspectLoadedUnitFields(m, "app", image, fields, nil, nil, stale)
			}
			reloads := 0
			err := reconcileLegacyLocalApplyUnits(tc.recover, verify, func() error {
				reloads++
				if tc.reloadFails {
					return errors.New("systemd reload failed")
				}
				fields["NeedDaemonReload"] = "no"
				fields["DropInPaths"] = tc.after
				return nil
			})
			if (err == nil) != tc.allow || reloads > 1 || (reloads == 1) != tc.reload {
				t.Fatalf("reloads=%d, error=%v", reloads, err)
			}
		})
	}
}
