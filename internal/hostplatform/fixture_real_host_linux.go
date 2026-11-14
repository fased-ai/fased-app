//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package hostplatform

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// These values are bound into the explicitly compiled disposable build. There
// is no command-line, HTTP, environment or configuration override.
var FixtureOwnerUID, FixtureMachineSHA256, FixtureSourceData, FixtureSourceRoot string

func fixtureHostMatches(owner int, ownerPin, machinePin string, machineID []byte) bool {
	if owner <= 0 || ownerPin != strconv.Itoa(owner) {
		return false
	}
	id := strings.TrimSpace(string(machineID))
	raw, err := hex.DecodeString(id)
	if err != nil || len(raw) != 16 || hex.EncodeToString(raw) != id {
		return false
	}
	pin, err := hex.DecodeString(machinePin)
	if err != nil || len(pin) != sha256.Size || hex.EncodeToString(pin) != machinePin {
		return false
	}
	sum := sha256.Sum256([]byte(id))
	return subtle.ConstantTimeCompare(sum[:], pin) == 1
}

// FixtureHostAllowed is scoped to one preselected OS owner and machine. It
// grants no wallet, update, root or network authority by itself.
func FixtureHostAllowed(owner int) bool {
	id, err := os.ReadFile("/etc/machine-id")
	return err == nil && fixtureHostMatches(owner, FixtureOwnerUID, FixtureMachineSHA256, id)
}

// FixtureSourceAllowed prevents this test build from enrolling another app's
// workspace, including an existing instance selected through the UI.
func FixtureSourceAllowed(owner int, data string) bool {
	return FixtureHostAllowed(owner) && FixtureSourceData != "/" && filepath.IsAbs(FixtureSourceData) &&
		!strings.ContainsAny(FixtureSourceData, "\x00\r\n") && filepath.Clean(FixtureSourceData) == FixtureSourceData && data == FixtureSourceData
}

// Check the installation path as well as its data before opening its lock.
func FixtureSourceLocationAllowed(owner int, root, data string) bool {
	return FixtureSourceAllowed(owner, data) && FixtureSourceRoot != "/" && filepath.IsAbs(FixtureSourceRoot) &&
		!strings.ContainsAny(FixtureSourceRoot, "\x00\r\n") && filepath.Clean(FixtureSourceRoot) == FixtureSourceRoot && root == FixtureSourceRoot
}
