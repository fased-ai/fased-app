package core

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const disposableProviderKey = "disposable-provider-input-not-issued-123456"

func providerAccount(t *testing.T, s *Store, endpoint, network string) RPCCredentialCommand {
	t.Helper()
	claimFixture(t, s)
	r := defaultAccounts()
	a := publicRecord()
	a.Version = 2
	a.Network = network
	a.Mode = "watch"
	r.Accounts = []Account{a}
	if _, e := s.UpdateAccounts(t.Context(), r); e == nil {
		t.Fatal("closed account admission")
	}
	s.rpcAdmission = true
	v, e := s.UpdateAccounts(t.Context(), r)
	if e != nil {
		t.Fatal(e)
	}
	v, e = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: a.ID, Revision: v.Record.Revision, Endpoint: endpoint, Network: network})
	if e != nil {
		t.Fatal(e)
	}
	return RPCCredentialCommand{AccountID: a.ID, Revision: v.Record.Revision, Action: "save", Key: disposableProviderKey, Passphrase: "Disposable provider vault password 123"}
}
func TestProviderRPCVaultLifecycle(t *testing.T) {
	dir := fixtureDir(t)
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	c := providerAccount(t, s, "https://mainnet.helius-rpc.com", "mainnet-beta")
	saved, e := s.RPCCredential(t.Context(), c)
	if e != nil || saved.TestOnly || !saved.Saved || saved.LiveRPC {
		t.Fatal(saved, e)
	}
	raw, e := os.ReadFile(s.rpcVaultPath(c.AccountID))
	if e != nil || bytes.Contains(raw, []byte(c.Key)) {
		t.Fatal("plaintext vault", e)
	}
	v, e := s.readRPCVault(c.AccountID)
	if e != nil || v.Version != 2 {
		t.Fatal(v, e)
	}
	key := c.Key
	c.Key = ""
	c.Ref = saved.Ref
	c.Action = "backup"
	backup, e := s.RPCCredential(t.Context(), c)
	if e != nil || backup.Backup.Version != 2 {
		t.Fatal(backup, e)
	}
	original := backup.Backup
	p := filepath.Join(dir, "provider-backup.json")
	if e = WriteRPCBackup(p, original); e != nil {
		t.Fatal(e)
	}
	if e = WriteRPCBackup(p, original); e == nil {
		t.Fatal("backup overwritten")
	}
	c.Action = "save"
	c.Key = key + "-rotated"
	rotated, e := s.RPCCredential(t.Context(), c)
	if e != nil || rotated.Ref == saved.Ref {
		t.Fatal(rotated, e)
	}
	c.Key = ""
	c.Action = "check"
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("stale ref accepted")
	}
	c.Ref = rotated.Ref
	c.Action = "remove"
	bad := c
	bad.Passphrase = "Wrong disposable password 123"
	if _, e = s.RPCCredential(t.Context(), bad); e == nil {
		t.Fatal("wrong password accepted")
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	c.Action = "check"
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("restart enabled credentials")
	}
	c.Action = "status"
	status := c
	status.Passphrase = ""
	view, e := s.RPCCredential(t.Context(), status)
	if e != nil || view.TestOnly || !view.Locked {
		t.Fatal(view, e)
	}
	c.Action = "remove"
	if _, e = s.RPCCredential(t.Context(), c); e != nil {
		t.Fatal("inactive cleanup", e)
	}
	c.Ref = ""
	c.Action = "restore"
	c.Backup = original
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("closed restore admitted")
	}
	s.rpcAdmission = true
	restored, e := s.RPCCredential(t.Context(), c)
	if e != nil || restored.Ref == saved.Ref || restored.TestOnly {
		t.Fatal(restored, e)
	}
	c.Ref = restored.Ref
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("restore overwrote vault")
	}
	c.Action = "verify-backup"
	copyBackup := *original
	copyBackup.InstanceID = "wrong"
	c.Backup = &copyBackup
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("wrong instance accepted")
	}
	copyBackup = *original
	copyBackup.Vault.Version = 1
	copyBackup.Version = 1
	c.Backup = &copyBackup
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("downgrade accepted")
	}
	c.Action = "check"
	c.Backup = nil
	if _, e = s.RPCCredential(t.Context(), c); e == nil {
		t.Fatal("ordinary transport enabled")
	}
}

func TestProviderRPCControlledHTTPS(t *testing.T) {
	for _, tc := range []struct{ origin, network, path string }{
		{"https://mainnet.helius-rpc.com", "mainnet-beta", "/"},
		{"https://devnet.helius-rpc.com", "devnet", "/"},
		{"https://solana-mainnet.g.alchemy.com", "mainnet-beta", "/v2/" + disposableProviderKey},
		{"https://solana-devnet.g.alchemy.com", "devnet", "/v2/" + disposableProviderKey},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			s := fixtureStore(t)
			c := providerAccount(t, s, tc.origin, tc.network)
			v, e := s.RPCCredential(t.Context(), c)
			if e != nil {
				t.Fatal(e)
			}
			var calls atomic.Int32
			var reject atomic.Bool
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if reject.Load() {
					w.Header().Set("Location", "https://wrong.example/"+disposableProviderKey)
					w.WriteHeader(302)
					return
				}
				var request struct {
					ID     int
					Method string
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				if r.Host != strings.TrimPrefix(tc.origin, "https://") || r.URL.Path != tc.path || r.Method != "POST" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("wrong authenticated request")
				}
				if strings.Contains(tc.origin, "helius") {
					if r.URL.Query().Get("api-key") != disposableProviderKey || len(r.URL.Query()) != 1 {
						t.Error("wrong query")
					}
				} else if r.URL.RawQuery != "" {
					t.Error("unexpected query")
				}
				result := solanaGenesis(tc.network)
				if request.Method == "getHealth" {
					result = "ok"
				} else if request.Method != "getGenesisHash" {
					t.Error("unexpected method")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			}))
			defer server.Close()
			rt := newPublicRPCTransport()
			rt.enabled = true
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			rt.transport.TLSClientConfig.RootCAs = roots
			rt.transport.TLSClientConfig.ServerName = "example.com"
			rt.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != strings.TrimPrefix(tc.origin, "https://")+":443" {
					t.Error("wrong destination")
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			c.Ref = v.Ref
			c.Key = ""
			c.Action = "check"
			check := func(ctx context.Context, in RPCCheckRequest, key []byte, valid func() error) (RPCCheckResult, error) {
				return checkProviderRPC(ctx, in, key, valid, rt)
			}
			result, e := s.rpcCredential(t.Context(), c, check)
			if e != nil || calls.Load() != 2 || result.Check == nil || result.Check.Endpoint != tc.origin {
				t.Fatal(result, e, calls.Load())
			}
			b, _ := json.Marshal(result)
			if bytes.Contains(b, []byte(disposableProviderKey)) {
				t.Fatal("secret result")
			}
			for _, entry := range mustReadDir(t, s.dir) {
				if !entry.IsDir() {
					b, _ := os.ReadFile(filepath.Join(s.dir, entry.Name()))
					if bytes.Contains(b, []byte(disposableProviderKey)) {
						t.Fatal("secret persisted", entry.Name())
					}
				}
			}
			reject.Store(true)
			if _, e = s.rpcCredential(t.Context(), c, check); e == nil || strings.Contains(e.Error(), disposableProviderKey) || calls.Load() != 3 {
				t.Fatal("redirect or secret escaped", e)
			}
			c.Ref = "stale"
			if _, e = s.rpcCredential(t.Context(), c, check); e == nil || calls.Load() != 3 {
				t.Fatal("stale credential dispatched")
			}
		})
	}
}
func mustReadDir(t *testing.T, path string) []os.DirEntry {
	t.Helper()
	x, e := os.ReadDir(path)
	if e != nil {
		t.Fatal(e)
	}
	return x
}

func TestProviderRPCMigrationPreservesAndRefuses(t *testing.T) {
	for _, kind := range []string{"normal", "paused", "unprotected"} {
		t.Run(kind, func(t *testing.T) {
			dir := fixtureDir(t)
			s, e := OpenStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			c := rpcSecretSetup(t, s)
			if _, e = s.RPCCredential(t.Context(), c); e != nil {
				t.Fatal(e)
			}
			vault, _ := os.ReadFile(s.rpcVaultPath(c.AccountID))
			var before string
			s.db.QueryRow("SELECT document FROM accounts WHERE id=1").Scan(&before)
			if kind == "paused" {
				if _, e = s.UpdatePause(t.Context(), pauseCommand(t, s, 1)); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "unprotected" {
				s.db.Exec("UPDATE accounts SET document=replace(document,'\"version\":1','\"version\":2') WHERE id=1")
			}
			if _, e = s.db.Exec("PRAGMA user_version=20"); e != nil {
				t.Fatal(e)
			}
			s.Close()
			n, e := OpenStore(dir)
			if kind != "normal" {
				if e == nil {
					n.Close()
					t.Fatal("unsafe migration")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			defer n.Close()
			var after string
			var schema int
			n.db.QueryRow("SELECT document FROM accounts WHERE id=1").Scan(&after)
			n.db.QueryRow("PRAGMA user_version").Scan(&schema)
			next, _ := os.ReadFile(n.rpcVaultPath(c.AccountID))
			if before != after || !bytes.Equal(vault, next) || schema != StateSchema {
				t.Fatal("migration changed prior state")
			}
		})
	}
}
