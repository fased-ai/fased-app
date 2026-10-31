//go:build !linux

package hostprep

import (
	"bufio"
	"context"
	"errors"
	"io"
)

func PrepareTailscale(context.Context, *bufio.Reader, io.Writer) error {
	return errors.New("guided Tailscale preparation supports Ubuntu 24.04; install and authorize Tailscale for this OS or choose SSH")
}
