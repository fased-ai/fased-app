//go:build !linux

package update

import (
	"errors"
	"os"
)

func inspectionFileIdentity(os.FileInfo, int, bool, os.FileMode) bool { return false }
func inspectionOpen(*os.Root, string) (*os.File, error) {
	return nil, errors.New("Linux disposable inspection fixtures only")
}

func inspectionPublish(*os.Root, string, string) error {
	return errors.New("Linux fixture publication only")
}
