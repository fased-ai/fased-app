//go:build !linux

package custodyinstall

import (
	"context"
	"errors"
	"net"
)

func (s *System) NativeChannel(context.Context, Manifest) (net.Conn, error) {
	return nil, errors.New("protected native channel requires Linux")
}
func (s *System) EnsureNativeRunning(context.Context, Manifest) error {
	return errors.New("protected native channel requires Linux")
}
