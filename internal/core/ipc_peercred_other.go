//go:build !linux

package core

import "net"

func wrapIPCPeerCredListener(ln net.Listener) net.Listener {
	return ln
}
