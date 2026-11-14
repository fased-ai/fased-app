//go:build linux && custody_acceptance_fixture && wallet_payload && fased_realhost_fixture

package main

import (
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/update"
)

func fixtureRealHostAllowed(parent string) bool {
	const prefix = update.MaintenanceRoot + "/owners/"
	if !strings.HasPrefix(parent, prefix) {
		return false
	}
	owner, err := strconv.Atoi(strings.TrimPrefix(parent, prefix))
	return err == nil && parent == update.MaintenanceOwnerRoot(owner) && hostplatform.FixtureSourceAllowed(owner, hostplatform.FixtureSourceData)
}

func fixtureRealHostRequired() bool { return true }

func init() { compiledFixtureSourceAllowed = hostplatform.FixtureSourceAllowed }
