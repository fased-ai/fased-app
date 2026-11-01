package bootstrap

import (
	"errors"
	"time"
)

// ValidateProductionSeed applies the installer's existing offline trust checks
// for publisher preparation. It performs no fetch, binding, signing or install.
func ValidateProductionSeed(raw []byte, pin string) (Seed, error) {
	seed, err := parseSeed(raw, pin, time.Now().UTC())
	if err != nil {
		return Seed{}, err
	}
	if seed.Version != 3 || seed.DevelopmentOnly {
		return Seed{}, errors.New("production first-install seed required")
	}
	return seed, nil
}
