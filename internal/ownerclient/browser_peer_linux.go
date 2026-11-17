//go:build linux

package ownerclient

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

// The browser cannot connect to the signer's Unix socket. For the short-lived
// Local owner origin, bind each accepted loopback connection to its kernel TCP
// socket owner. Missing, ambiguous or unreadable socket state fails closed.
func browserPeerUID(conn net.Conn) (int, error) {
	f, err := os.Open("/proc/net/tcp")
	if err != nil {
		return 0, errors.New("Local owner connection cannot be verified")
	}
	defer f.Close()
	return browserPeerUIDFromTable(f, conn.RemoteAddr(), conn.LocalAddr())
}

func browserPeerUIDFromTable(table io.Reader, remote, local net.Addr) (int, error) {
	address := func(value net.Addr) (string, error) {
		a, ok := value.(*net.TCPAddr)
		if !ok || a.Port < 1 || a.Port > 65535 || !a.IP.Equal(net.IPv4(127, 0, 0, 1)) {
			return "", errors.New("exact Local owner connection required")
		}
		return fmt.Sprintf("%08X:%04X", binary.LittleEndian.Uint32(a.IP.To4()), a.Port), nil
	}
	client, err := address(remote)
	if err != nil {
		return 0, err
	}
	server, err := address(local)
	if err != nil {
		return 0, err
	}
	const limit = 4 << 20
	b, err := io.ReadAll(io.LimitReader(table, limit+1))
	if err != nil || len(b) > limit {
		return 0, errors.New("Local owner socket table is unavailable")
	}
	scan := bufio.NewScanner(bytes.NewReader(b))
	matched, uid := false, 0
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) < 8 || fields[1] != client || fields[2] != server || fields[3] != "01" {
			continue
		}
		value, err := strconv.Atoi(fields[7])
		if err != nil || value <= 0 || matched {
			return 0, errors.New("Local owner socket identity is ambiguous")
		}
		matched, uid = true, value
	}
	if scan.Err() != nil || !matched {
		return 0, errors.New("Local owner connection is unavailable")
	}
	return uid, nil
}
