//go:build linux && custody_acceptance_fixture && wallet_payload && !fased_realhost_fixture

package main

func fixtureRealHostAllowed(string) bool { return false }
func fixtureRealHostRequired() bool      { return false }
