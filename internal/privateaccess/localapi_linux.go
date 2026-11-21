//go:build linux

package privateaccess

import (
	"context"
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

func verifyRootPeer(c net.Conn) bool {
	unixConn, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return false
	}
	trusted := false
	if err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		trusted = e == nil && cred.Uid == 0
	}); err != nil {
		return false
	}
	return trusted
}

func dialTailscaled(ctx context.Context) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/tailscale/tailscaled.sock")
	if err != nil {
		return nil, err
	}
	if !verifyRootPeer(conn) {
		_ = conn.Close()
		return nil, errors.New("Tailscale daemon socket is not root-owned")
	}
	return conn, nil
}
