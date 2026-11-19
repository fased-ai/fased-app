//go:build linux && amd64 && fased_automatic_predecessor && !custody_acceptance_fixture && !fased_realhost_fixture

package hostplatform

import (
	"os"

	"github.com/fased-ai/fased-app/internal/update"
)

const AutomaticPredecessorBuild = true

// The reviewed test build must pin these values in both the app and helper.
// There is no runtime override. Empty or malformed inputs confer no authority.
var AutomaticPredecessorOwnerUID, AutomaticPredecessorMachineSHA256, AutomaticPredecessorInstance string

func AutomaticPredecessorAllowed(owner int, location, instance string) bool {
	if location != "local" || !update.IDOK(instance) || instance != AutomaticPredecessorInstance {
		return false
	}
	id, err := os.ReadFile("/etc/machine-id")
	return err == nil && fixtureHostMatches(owner, AutomaticPredecessorOwnerUID, AutomaticPredecessorMachineSHA256, id)
}
