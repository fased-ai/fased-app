//go:build linux && (fased_realhost_fixture || fased_automatic_predecessor)

package hostplatform

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
)

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
