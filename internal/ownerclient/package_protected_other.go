//go:build !linux

package ownerclient

func protectedNativePackage(string) (Package, bool, error) { return Package{}, false, nil }
