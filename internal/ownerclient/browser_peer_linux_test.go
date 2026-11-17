//go:build linux

package ownerclient

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
)

func TestBrowserPeerUIDRequiresExactLoopbackConnection(t *testing.T) {
	server := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000}
	client := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40001}
	line := "  0: 0100007F:9C41 0100007F:9C40 01 00000000:00000000 00:00000000 00000000 1000 0 1\n"
	uid, err := browserPeerUIDFromTable(strings.NewReader(line), client, server)
	if err != nil || uid != 1000 {
		t.Fatal("owner connection was not identified", uid, err)
	}
	for _, table := range []string{
		"",
		strings.Replace(line, " 01 ", " 06 ", 1),
		strings.Replace(line, "9C41", "9C42", 1),
		strings.Replace(line, " 1000 ", " invalid ", 1),
		line + line,
	} {
		if _, err = browserPeerUIDFromTable(strings.NewReader(table), client, server); err == nil {
			t.Fatal("missing, malformed or ambiguous connection was accepted")
		}
	}
	foreign := &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: client.Port}
	if _, err = browserPeerUIDFromTable(strings.NewReader(line), foreign, server); err == nil {
		t.Fatal("a non-loopback peer was accepted")
	}
}

func TestBrowserPeerUIDMatchesRealLocalSocket(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	client, err := net.Dial("tcp4", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	uid, err := browserPeerUID(server)
	if err != nil || uid != os.Getuid() {
		t.Fatal(fmt.Sprintf("real local connection has wrong owner: %d", uid), err)
	}
}
