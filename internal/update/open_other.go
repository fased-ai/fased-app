//go:build !linux && !darwin

package update

import "os"

func openReadOnly(path string) (*os.File, error) { return os.Open(path) }
