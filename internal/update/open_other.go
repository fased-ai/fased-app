//go:build !linux && !darwin

package update

import "os"

func acquisitionParentOwned(os.FileInfo) bool { return false }

func openReadOnly(path string) (*os.File, error) { return os.Open(path) }
