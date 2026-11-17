//go:build !linux

package main

func dispatchPackageInstance([]string) error { return nil }

func defaultPackageLocalData(fallback string, _ bool) (string, error) { return fallback, nil }
