//go:build !linux

package privateaccess

import (
	"errors"
	"os"
)

func openPreflightDirectory(string) (*os.File, error) {
	return nil, errors.New("protected Remote setup requires Linux")
}

func removePreflightDirectory(*os.File, string) error {
	return errors.New("protected Remote setup requires Linux")
}
