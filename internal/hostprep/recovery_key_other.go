//go:build !linux

package hostprep

import (
	"bufio"
	"errors"
	"io"
)

func PrepareRecoveryKey(*bufio.Reader, io.Writer) error {
	return errors.New("Remote recovery setup requires Linux")
}
