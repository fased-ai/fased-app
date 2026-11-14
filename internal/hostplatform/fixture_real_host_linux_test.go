//go:build linux && custody_acceptance_fixture && fased_realhost_fixture

package hostplatform

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRealHostFixtureRequiresExactOwnerAndMachine(t *testing.T) {
	id := []byte("0123456789abcdef0123456789abcdef\n")
	sum := sha256.Sum256(id[:len(id)-1])
	pin := hex.EncodeToString(sum[:])
	for _, tc := range []struct {
		name, ownerPin, machinePin string
		owner                      int
		machine                    []byte
		want                       bool
	}{
		{"matching", "1000", pin, 1000, id, true},
		{"foreign owner", "1000", pin, 1001, id, false},
		{"root", "0", pin, 0, id, false},
		{"unbound owner", "", pin, 1000, id, false},
		{"unbound machine", "1000", "", 1000, id, false},
		{"foreign machine", "1000", pin, 1000, []byte("1123456789abcdef0123456789abcdef"), false},
		{"invalid identity", "1000", pin, 1000, []byte("not-a-machine-id"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fixtureHostMatches(tc.owner, tc.ownerPin, tc.machinePin, tc.machine); got != tc.want {
				t.Fatalf("allowed %t, want %t", got, tc.want)
			}
		})
	}
}

func TestRealHostFixtureSourceIsExact(t *testing.T) {
	id, err := os.ReadFile("/etc/machine-id")
	if err != nil || os.Getuid() <= 0 {
		t.Skip("ordinary host identity unavailable")
	}
	oldOwner, oldMachine, oldData := FixtureOwnerUID, FixtureMachineSHA256, FixtureSourceData
	t.Cleanup(func() { FixtureOwnerUID, FixtureMachineSHA256, FixtureSourceData = oldOwner, oldMachine, oldData })
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(id))))
	FixtureOwnerUID, FixtureMachineSHA256 = strconv.Itoa(os.Getuid()), hex.EncodeToString(sum[:])
	FixtureSourceData = filepath.Join(t.TempDir(), "fixture-data")
	if !FixtureHostAllowed(os.Getuid()) {
		t.Skip("canonical Linux machine identity unavailable")
	}
	if !FixtureSourceAllowed(os.Getuid(), FixtureSourceData) || FixtureSourceAllowed(os.Getuid(), filepath.Join(t.TempDir(), "other")) || FixtureSourceAllowed(os.Getuid()+1, FixtureSourceData) {
		t.Fatal("source was not bound to the exact host and owner")
	}
	for _, invalid := range []string{"", "/", "relative", "/tmp/../other", "/tmp/fixture\nother"} {
		FixtureSourceData = invalid
		if FixtureSourceAllowed(os.Getuid(), invalid) {
			t.Fatal("invalid source was accepted")
		}
	}
}
