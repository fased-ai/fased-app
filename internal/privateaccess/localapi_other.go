//go:build !linux

package privateaccess

import (
	"context"
	"errors"
	"net"
)

func dialTailscaled(context.Context) (net.Conn, error) {
	return nil, errors.New("guided Tailscale Serve configuration currently targets Linux VPS only")
}
