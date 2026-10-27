//go:build !windows

package core

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
)

func listenIPC(dir, id string) (net.Listener, string, string, error) {
	address := filepath.Join(dir, "core.sock")
	// Called only after the state directory's exclusive instance lock is held.
	if info, err := os.Lstat(address); err == nil && info.Mode()&os.ModeSocket == 0 {
		return nil, "", "", errors.New("refusing to replace a non-socket at the core socket path")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, "", "", err
	}
	if err := os.Remove(address); err != nil && !os.IsNotExist(err) {
		return nil, "", "", err
	}
	listener, err := net.Listen("unix", address)
	if err != nil {
		return nil, "", "", err
	}
	if err = os.Chmod(address, 0600); err != nil {
		listener.Close()
		return nil, "", "", err
	}
	return listener, "unix", address, nil
}
func dialIPC(ctx context.Context, network, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

func validateIPC(dir string, endpoint Endpoint) error {
	expected, err := filepath.Abs(filepath.Join(dir, "core.sock"))
	if err != nil {
		return err
	}
	actual, err := filepath.Abs(endpoint.Address)
	if err != nil {
		return err
	}
	if endpoint.Network != "unix" || actual != expected {
		return errors.New("local socket does not match this state directory")
	}
	return nil
}
