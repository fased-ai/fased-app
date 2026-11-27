//go:build linux

package core

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

type ipcPeerCredListener struct {
	net.Listener
	allowedUID uint32
}

func wrapIPCPeerCredListener(ln net.Listener) net.Listener {
	return &ipcPeerCredListener{
		Listener:   ln,
		allowedUID: uint32(os.Geteuid()),
	}
}

func (l *ipcPeerCredListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := verifyIPCPeerUID(conn, l.allowedUID); err != nil {
			_ = conn.Close()
			continue
		}
		return conn, nil
	}
}

func verifyIPCPeerUID(conn net.Conn, allowedUID uint32) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("expected unix ipc connection")
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var ucred *unix.Ucred
	var ctlErr error
	if err := rc.Control(func(fd uintptr) {
		ucred, ctlErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if ctlErr != nil {
		return ctlErr
	}
	if ucred == nil || (ucred.Uid != allowedUID && ucred.Uid != 0) {
		return fmt.Errorf("ipc peer uid denied")
	}
	return nil
}
