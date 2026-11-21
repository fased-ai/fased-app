//go:build linux

package privateaccess

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyRootPeerRejectsNonRootUnixSocket(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root cannot exercise non-root SO_PEERCRED rejection")
	}
	sockPath := filepath.Join(t.TempDir(), "tailscaled.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := ln.Accept()
		if e == nil {
			_ = c.Close()
		}
	}()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if verifyRootPeer(conn) {
		t.Fatal("non-root unix peer accepted as tailscaled")
	}
	<-done
}
