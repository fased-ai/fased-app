//go:build linux

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/walletfixture"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

func TestRemoteCustodyAccountsPrivateNoSession(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id := tailnet.Identity{UserID: 1, NodeID: "original-device", Login: "owner"}
	code, err := s.IssueRemoteClaim(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.remoteSession(t.Context(), id, code.Code, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Logout(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	h.instanceLocation, h.instanceOwnerUID, h.instanceOwnerKind = "remote", 1000, "custody"
	s.custodyOwnerSupported, s.custodyNetwork = true, "devnet"
	var beforeSalt, beforeHash []byte
	if err = s.db.QueryRow("SELECT password_salt,password_hash FROM instance").Scan(&beforeSalt, &beforeHash); err != nil {
		t.Fatal(err)
	}
	call := func(private bool, method, path, instance string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:7331"+path, bytes.NewBufferString(`{}`))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Fased-Custody-Accounts", instance)
		w := httptest.NewRecorder()
		if private {
			privateIPCHandler(h).ServeHTTP(w, r)
		} else {
			h.ServeHTTP(w, r)
		}
		return w
	}
	if w := call(true, "GET", "/api/v1/accounts", s.instanceID); w.Code != 200 {
		t.Fatalf("actual no-session account entry: %d %s", w.Code, w.Body)
	}
	for _, route := range []string{"/api/v1/agents", "/api/v1/auth/state", "/api/v1/auth/native-local", "/api/v1/updates/application/owner", "/api/v1/accounts/venue-credential"} {
		if w := call(true, "POST", route, s.instanceID); w.Code != 403 {
			t.Fatalf("scope escaped %s: %d", route, w.Code)
		}
	}
	for _, row := range []struct {
		private  bool
		instance string
	}{{false, s.instanceID}, {true, "foreign"}, {true, ""}} {
		if w := call(row.private, "GET", "/api/v1/accounts", row.instance); w.Code == 200 {
			t.Fatal("unverified IPC/instance admitted")
		}
	}
	// Existing body validation and update fencing still run after authentication.
	if w := call(true, "POST", "/api/v1/accounts/read", s.instanceID); w.Code == 401 || w.Code == 403 || w.Code == 200 {
		t.Fatalf("read bypassed validation or auth failed: %d %s", w.Code, w.Body)
	}
	if w := call(true, "PUT", "/api/v1/accounts", s.instanceID); w.Code == 200 || w.Code == 401 || w.Code == 403 {
		t.Fatalf("record validation bypassed: %d", w.Code)
	}
	if _, err = s.ApplicationUpdates(t.Context()); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ApplicationUpdateOperation{Phase: "recovery"})
	if _, err = s.db.Exec("UPDATE application_updates SET document=? WHERE id=1", string(raw)); err != nil {
		t.Fatal(err)
	}
	if w := call(true, "PUT", "/api/v1/accounts/rpc", s.instanceID); w.Code != 409 {
		t.Fatalf("recovery fence bypassed: %d %s", w.Code, w.Body)
	}
	h.instanceOwnerKind = ""
	if w := call(true, "GET", "/api/v1/accounts", s.instanceID); w.Code == 200 {
		t.Fatal("foreign VPS acquired custody access")
	}
	h.instanceOwnerKind = "custody"
	h.instanceLocation = "local"
	if w := call(true, "GET", "/api/v1/accounts", s.instanceID); w.Code == 200 {
		t.Fatal("Local behavior changed")
	}
	h.instanceLocation = "remote"
	// No bearer token exists to copy, expire or replay. Removing owner enrollment
	// revokes this admission on the next request, including an existing connection.
	if _, err = s.db.Exec("DELETE FROM remote_owner"); err != nil {
		t.Fatal(err)
	}
	if w := call(true, "GET", "/api/v1/accounts", s.instanceID); w.Code == 200 {
		t.Fatal("missing owner metadata admitted")
	}
	var salt, hash []byte
	if err = s.db.QueryRow("SELECT password_salt,password_hash FROM instance").Scan(&salt, &hash); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(salt, beforeSalt) || !bytes.Equal(hash, beforeHash) {
		t.Fatal("auth rewritten")
	}
	var sessions int
	if err = s.db.QueryRow("SELECT count(*) FROM sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("created a general session", err, sessions)
	}
}

func TestRemoteCustodyAccountsClientAcrossRestart(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "state")
	keydir := filepath.Join(root, "keys")
	if err := os.Mkdir(keydir, 0700); err != nil {
		t.Fatal(err)
	}
	vault, err := signer.OpenControlledOwnerCustody(keydir, "00112233445566778899aabbccddeeff", "devnet")
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	if err = vault.ImportControlledOrdinarySeed(bytes.Repeat([]byte{9}, 32), []byte("Disposable signer fixture password")); err != nil {
		t.Fatal(err)
	}
	var input WalletReadCommand
	var originalRecord AccountsRecord
	for boot := 0; boot < 2; boot++ {
		server, err := Start(t.Context(), dir, "127.0.0.1:0", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		closed := false
		t.Cleanup(func() {
			if !closed {
				server.Close(context.Background())
			}
		})
		s := server.Store
		if boot == 0 {
			id := tailnet.Identity{UserID: 1, NodeID: "original-device", Login: "owner"}
			code, e := s.IssueRemoteClaim(t.Context(), id)
			if e != nil {
				t.Fatal(e)
			}
			token, e := s.remoteSession(t.Context(), id, code.Code, "")
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Logout(t.Context(), token); e != nil {
				t.Fatal(e)
			}
		}
		h := server.HTTP.Handler.(*HTTPHandler)
		h.instanceLocation, h.instanceOwnerUID, h.instanceOwnerKind = "remote", 1000, "custody"
		s.custodyOwnerSupported, s.custodyNetwork = true, "devnet"
		s.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
			if r.Action != "status" {
				t.Fatal("unexpected signer operation", r.Action)
			}
			return signer.Response{Status: vault.Status()}, nil
		}
		transport, stop := walletfixture.New()
		defer stop()
		if err = server.ConfigureControlledWalletReads(transport); err != nil {
			t.Fatal(err)
		}
		if boot == 0 {
			v, e := s.Accounts(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			v, e = s.SetupAccount(t.Context(), AccountSetupCommand{Revision: v.Record.Revision, Action: "attach", Name: "Recovered wallet", Network: "devnet", Reference: vault.Status().Account})
			if e != nil {
				t.Fatal(e)
			}
			account := v.Record.Accounts[0]
			v, e = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{Revision: v.Record.Revision, AccountID: account.ID, Endpoint: walletfixture.Endpoint, Network: "devnet"})
			if e != nil {
				t.Fatal(e)
			}
			credential, e := s.RPCCredential(t.Context(), RPCCredentialCommand{Revision: v.Record.Revision, AccountID: account.ID, Action: "save", Key: disposableProviderKey, Passphrase: "Disposable independent RPC password"})
			if e != nil {
				t.Fatal(e)
			}
			input = WalletReadCommand{Revision: v.Record.Revision, AccountID: account.ID, Ref: credential.Ref, Passphrase: "Disposable independent RPC password"}
			originalRecord = v.Record
		}
		serving := make(chan error, 1)
		go func() { serving <- server.Serve() }()
		c, err := NewClient(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.LoadSession(t.Context(), dir); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("test must begin without a session", err)
		}
		if err = c.CustodyAccounts(t.Context()); err != nil {
			t.Fatal("real private IPC account client", err)
		}
		var view AccountsView
		if err = c.Do(t.Context(), "GET", "/api/v1/accounts", nil, &view); err != nil || !reflect.DeepEqual(view.Record, originalRecord) {
			t.Fatal("actual account handler", err)
		}
		var snapshot WalletSnapshot
		if err = c.Do(t.Context(), "POST", "/api/v1/accounts/read", input, &snapshot); err != nil || snapshot.Lamports != walletfixture.Lamports || !snapshot.FixtureOnly || snapshot.LiveRPC {
			t.Fatal("positive controlled private RPC read", err, snapshot)
		}
		wrong := input
		wrong.Passphrase = "Wrong disposable RPC password"
		if err = c.Do(t.Context(), "POST", "/api/v1/accounts/read", wrong, &snapshot); err == nil {
			t.Fatal("bad vault password admitted")
		}
		if !vault.Status().Locked || !vault.Status().RecoveryRequired {
			t.Fatal("read unlocked signer or cleared recovery hold")
		}
		if err = c.Do(t.Context(), "GET", "/api/v1/agents", nil, nil); err == nil {
			t.Fatal("client leaked scope")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err = c.Do(ctx, "GET", "/api/v1/accounts", nil, nil); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation lost", err)
		}
		c.Close()
		if _, err = os.Stat(filepath.Join(dir, "cli-session.json")); !os.IsNotExist(err) {
			t.Fatal("persisted bearer credential", err)
		}
		if err = server.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		closed = true
		if err = <-serving; err != nil {
			t.Fatal(err)
		}
	}
}
