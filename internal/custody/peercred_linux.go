//go:build linux

package custody

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
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

func verifyPeerConnUID(conn net.Conn, allowedUID int) error {
	if allowedUID < 0 {
		return nil
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("expected UnixConn peer, got %T", conn)
	}
	rawConn, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Ucred
	var credErr error
	if ctrlErr := rawConn.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); ctrlErr != nil {
		return ctrlErr
	}
	if credErr != nil {
		return credErr
	}
	if cred == nil || int(cred.Uid) != allowedUID {
		gotUID := -1
		if cred != nil {
			gotUID = int(cred.Uid)
		}
		return fmt.Errorf("SO_PEERCRED denied peer uid %d (expected %d)", gotUID, allowedUID)
	}
	return nil
}
