//go:build linux

package custodyinstall

import "testing"

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
