//go:build !linux

package main

func defaultPackageLocalData(fallback string, _ bool) (string, error) { return fallback, nil }
