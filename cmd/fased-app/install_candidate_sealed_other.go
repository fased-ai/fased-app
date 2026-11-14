//go:build !linux

package main

import "github.com/fased-ai/fased-app/internal/bundle"

func sealedInstallationEngine(string, bundle.File) bool { return false }
