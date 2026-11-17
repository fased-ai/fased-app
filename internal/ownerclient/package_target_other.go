//go:build !linux

package ownerclient

func localPackageOwnerTarget(int, string) (string, error) { return "", nil }
