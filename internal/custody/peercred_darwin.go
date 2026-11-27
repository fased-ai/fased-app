//go:build darwin

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
		return fmt.Errorf("expected unix socket connection")
	}
	rawConn, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Xucred
	var ctlErr error
	if err := rawConn.Control(func(fd uintptr) {
		cred, ctlErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return err
	}
	if ctlErr != nil {
		return ctlErr
	}
	if cred == nil || int(cred.Uid) != allowedUID {
		got := -1
		if cred != nil {
			got = int(cred.Uid)
		}
		return fmt.Errorf("unix socket peer uid %d denied (allowed uid %d)", got, allowedUID)
	}
	return nil
}
