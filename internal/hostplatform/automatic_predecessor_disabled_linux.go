//go:build linux && (!amd64 || !fased_automatic_predecessor || custody_acceptance_fixture || fased_realhost_fixture)

package hostplatform

import "github.com/fased-ai/fased-app/internal/tailnet"

const AutomaticPredecessorBuild = false

func AutomaticPredecessorAllowed(int, string, string, string) bool { return false }
func AutomaticPredecessorDeviceAllowed(tailnet.Identity) bool      { return false }
