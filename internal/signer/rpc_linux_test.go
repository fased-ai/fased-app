//go:build linux

package signer

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIdentityConfigurationRejectsSameUIDAndUntrustedSource(t *testing.T) {
	c := Config{Version: 1, Mode: FixtureNetwork, SignerID: randomID(), OwnerUID: 1001, AppUID: 1002, SignerUID: 1003, StateDir: "/one", SocketDir: "/two"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.AppUID = c.OwnerUID }, func(c *Config) { c.OwnerUID = c.SignerUID }, func(c *Config) { c.SignerUID = c.AppUID }, func(c *Config) { c.SignerUID = 0 }, func(c *Config) { c.Mode = "solana" }, func(c *Config) { c.Version = 2 }} {
		other := c
		mutate(&other)
		requireError(t, other.Validate())
	}
	path := filepath.Join(fixtureDir(t), "config.json")
	b, _ := json.Marshal(c)
	os.WriteFile(path, b, 0600)
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("owner/app-writable configuration accepted as trusted role authority")
	}
	if server, err := Serve(context.Background(), c); err == nil {
		server.Close()
		t.Fatal("unprivileged fixture promoted to installed custody")
	}
}

// This test intentionally uses one UID through the private listener constructor:
// protocol tests are SUPPORTING, never proof of separate installed principals.
func protocolFixture(t *testing.T, ownerUID, appUID int) (Config, *Vault) {
	t.Helper()
	v := openFixture(t, fixtureDir(t))
	sockets := fixtureDir(t)
	c := Config{Version: 1, Mode: FixtureNetwork, SignerID: v.signerID, OwnerUID: ownerUID, AppUID: appUID, SignerUID: os.Geteuid(), StateDir: "unused", SocketDir: sockets}
	ctx, cancel := context.WithCancel(context.Background())
	s, err := serveListeners(ctx, c, v)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); s.Close() })
	return c, v
}
func protocolConnect(t *testing.T, c Config, owner bool) (*net.UnixConn, greeting) {
	t.Helper()
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath(c, owner), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	uid, err := peerUID(conn)
	if err != nil || uid != os.Geteuid() {
		t.Fatal("actual kernel peer identity")
	}
	var hello greeting
	if err = readWire(conn, &hello); err != nil {
		t.Fatal(err)
	}
	return conn, hello
}
func protocolRequest(t *testing.T, c Config, owner bool, r Request) Response {
	t.Helper()
	conn, hello := protocolConnect(t, c, owner)
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(wireRequest{Version: 1, SignerID: c.SignerID, Challenge: hello.Challenge, Request: r}); err != nil {
		t.Fatal(err)
	}
	var result Response
	if err := readWire(conn, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestOwnerAndAppProtocolBoundaries(t *testing.T) {
	c, v := protocolFixture(t, os.Geteuid(), os.Geteuid())
	for _, action := range []string{"create", "unlock", "lock", "backup", "verify-backup", "restore", "export-fixture-seed", "sign", "submit", "grant"} {
		r := protocolRequest(t, c, false, Request{Action: action, Password: []byte("Disposable RPC password")})
		if r.Error == "" || r.Document != nil || v.Status().Account != nil {
			t.Fatalf("app authorized %s", action)
		}
	}
	r := protocolRequest(t, c, true, Request{Action: "create", Password: []byte("Disposable RPC password")})
	if r.Error != "" || r.Status.Account == nil || !r.Status.Locked {
		t.Fatal("owner fixture create")
	}
	r = protocolRequest(t, c, false, Request{Action: "status"})
	if r.Error != "" || r.Status.Account == nil || r.Status.WalletSigner || r.Status.FinancialExecution {
		t.Fatal("app public reference/capabilities")
	}
	r = protocolRequest(t, c, true, Request{Action: "sign"})
	if r.Error == "" {
		t.Fatal("arbitrary signing accepted")
	}
}

func TestNonceReplayIdentityAndMalformedRequests(t *testing.T) {
	c, _ := protocolFixture(t, os.Geteuid(), os.Geteuid())
	first, old := protocolConnect(t, c, true)
	first.Close()
	for _, variant := range []string{"replay", "signer", "version", "unknown", "oversized"} {
		t.Run(variant, func(t *testing.T) {
			conn, hello := protocolConnect(t, c, true)
			defer conn.Close()
			request := wireRequest{Version: 1, SignerID: c.SignerID, Challenge: hello.Challenge, Request: Request{Action: "status"}}
			switch variant {
			case "replay":
				request.Challenge = old.Challenge
			case "signer":
				request.SignerID = randomID()
			case "version":
				request.Version = 2
			}
			b, _ := json.Marshal(request)
			if variant == "unknown" {
				b = append(b[:len(b)-1], []byte(",\"owner\":true}")...)
			}
			if variant == "oversized" {
				b = make([]byte, 65537)
			}
			conn.Write(append(b, '\n'))
			if readWire(conn, new(Response)) == nil {
				t.Fatal("invalid/replayed request accepted")
			}
		})
	}
}

func TestKernelPeerUIDDenialBeforeHandshake(t *testing.T) {
	c, _ := protocolFixture(t, os.Geteuid()+100, os.Geteuid()+101)
	for _, owner := range []bool{true, false} {
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath(c, owner), Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		if readWire(conn, new(greeting)) == nil {
			t.Fatal("foreign UID obtained handshake")
		}
		conn.Close()
	}
}
