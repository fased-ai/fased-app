//go:build linux

package tailnet

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type trustedKey struct{}

// Listen creates a dedicated HTTP proxy socket. Never use a maintenance/owner
// IPC socket as a Serve backend. Only a root-owned tailscaled connection may
// supply identity headers; the application OS user cannot spoof that proof.
func Listen(dir string) (net.Listener, error) {
	path := filepath.Join(dir, "tailscale.sock")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("private ingress path is occupied")
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}
func rootPeer(c net.Conn) bool {
	unixConn, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return false
	}
	trusted := false
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		trusted = e == nil && cred.Uid == 0
	})
	if err != nil || !trusted {
		return false
	}
	return true
}
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	if rootPeer(c) {
		return context.WithValue(ctx, trustedKey{}, true)
	}
	return ctx
}
func dialDaemon(ctx context.Context) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/tailscale/tailscaled.sock")
	if err != nil {
		return nil, err
	}
	if !rootPeer(conn) {
		conn.Close()
		return nil, errors.New("Tailscale daemon must run under the trusted OS account")
	}
	return conn, nil
}
func Trusted(r *http.Request) bool { return r.Context().Value(trustedKey{}) == true }
func Target(dir string) string     { return "unix:" + filepath.Join(dir, "tailscale.sock") }
