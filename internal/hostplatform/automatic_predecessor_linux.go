//go:build linux && amd64 && fased_automatic_predecessor && !custody_acceptance_fixture && !fased_realhost_fixture

package hostplatform

import (
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/update"
)

const AutomaticPredecessorBuild = true

// The reviewed test build must pin these values in both the app and helper.
// There is no runtime override. Empty or malformed inputs confer no authority.
var AutomaticPredecessorOwnerUID, AutomaticPredecessorMachineSHA256, AutomaticPredecessorInstance string
var AutomaticPredecessorRemoteOrigin, AutomaticPredecessorRemoteDeviceSHA256 string

func AutomaticPredecessorAllowed(owner int, location, instance, origin string) bool {
	if !update.IDOK(instance) || instance != AutomaticPredecessorInstance {
		return false
	}
	switch location {
	case "local":
		if origin != "" || AutomaticPredecessorRemoteOrigin != "" || AutomaticPredecessorRemoteDeviceSHA256 != "" {
			return false
		}
	case "remote":
		if _, _, err := tailnet.Origin(origin); err != nil || origin != AutomaticPredecessorRemoteOrigin || len(AutomaticPredecessorRemoteDeviceSHA256) != 64 {
			return false
		}
	default:
		return false
	}
	id, err := os.ReadFile("/etc/machine-id")
	return err == nil && fixtureHostMatches(owner, AutomaticPredecessorOwnerUID, AutomaticPredecessorMachineSHA256, id)
}

func AutomaticPredecessorDeviceAllowed(device tailnet.Identity) bool {
	if device.UserID <= 0 || device.NodeID == "" {
		return false
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", device.UserID, device.NodeID)))
	return fmt.Sprintf("%x", digest) == AutomaticPredecessorRemoteDeviceSHA256
}
