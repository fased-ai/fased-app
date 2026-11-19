//go:build linux && (!amd64 || !fased_automatic_predecessor || custody_acceptance_fixture || fased_realhost_fixture)

package hostplatform

const AutomaticPredecessorBuild = false

func AutomaticPredecessorAllowed(int, string, string) bool { return false }
