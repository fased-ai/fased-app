//go:build !linux && !darwin

package releaseops

import (
	"errors"
	"os"
)

const noFollow = 0

func owned(os.FileInfo) bool { return false }
func lock(*os.File) error    { return errors.New("fixture release ledger unsupported on this OS") }
func unlock(*os.File)        {}
