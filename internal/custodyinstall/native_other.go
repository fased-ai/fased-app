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

func (*System) PrepareNativeUpdateSession(context.Context, Manifest, string) error {
	return errors.New("protected Local maintenance requires Linux")
}
func NativeUpdateSessionIdentity(int, string, string) (Manifest, string, error) {
	return Manifest{}, "", errors.New("protected Local maintenance requires Linux")
}

func InstalledNativeUpdateSessionIdentity(int, string, string) (Manifest, string, error) {
	return Manifest{}, "", errors.New("protected Local maintenance requires Linux")
}

func InstalledRemoteAccountsIdentity(int, string, string) (Manifest, string, error) {
	return Manifest{}, "", errors.New("protected Remote custody accounts require Linux")
}
