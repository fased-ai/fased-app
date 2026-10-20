//go:build !linux && !darwin

package update

import (
	"errors"
	"os"
)

func lockFixtureTrust(*os.File) error {
	return errors.New("fixture store isolation unavailable on this OS")
}
func unlockFixtureTrust(*os.File) {}
