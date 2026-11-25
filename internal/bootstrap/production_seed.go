package bootstrap

import (
	"errors"
	"runtime"
	"time"
)

// ValidateProductionSeed applies the installer's existing offline trust checks
// for publisher preparation. It performs no fetch, binding, signing or install.
func ValidateProductionSeed(raw []byte, pin string) (Seed, error) {
	return ValidateProductionSeedForArch(raw, pin, runtime.GOARCH)
}

// ValidateProductionSeedForArch validates a pinned production seed against an
// explicit Linux architecture (used when preparing arm64 releases on amd64 hosts).
func ValidateProductionSeedForArch(raw []byte, pin, arch string) (Seed, error) {
	if arch != "amd64" && arch != "arm64" {
		return Seed{}, errors.New("unsupported Linux release seed architecture")
	}
	seed, err := parseSeedForArch(raw, pin, time.Now().UTC(), arch)
	if err != nil {
		return Seed{}, err
	}
	if (seed.Version != 3 && seed.Version != 4) || seed.DevelopmentOnly {
		return Seed{}, errors.New("production first-install seed required")
	}
	return seed, nil
}
