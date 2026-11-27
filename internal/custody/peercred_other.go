//go:build !linux && !darwin

package custody

import (
	"errors"
	"net"
)

type peerCredListener struct {
	net.Listener
	allowedUID int
}

func newPeerCredListener(inner net.Listener, allowedUID int) net.Listener {
	return &peerCredListener{Listener: inner, allowedUID: allowedUID}
}

func (l *peerCredListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := verifyPeerConnUID(conn, l.allowedUID); err != nil {
			_ = conn.Close()
			continue
		}
		return conn, nil
	}
}

func verifyPeerConnUID(_ net.Conn, allowedUID int) error {
	if allowedUID >= 0 {
		return errors.New("OS unix socket peer UID verification unsupported on this platform; failing closed")
	}
	return nil
}
