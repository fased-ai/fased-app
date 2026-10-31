//go:build linux

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	fasedapp "github.com/fased-ai/fased-app"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/walletfixture"
)

func TestOrdinaryWalletReadJoin(t *testing.T) {
	for _, location := range []string{"local", "remote"} {
		t.Run(location, func(t *testing.T) {
			root := fixtureDir(t)
			keydir := filepath.Join(root, "keys")
			if err := os.Mkdir(keydir, 0700); err != nil {
				t.Fatal(err)
			}
			password := []byte("Disposable installed wallet password 123")
			rpcPassword := "Disposable independent RPC vault password 123"
			vault, err := signer.OpenControlledOwnerCustody(keydir, "00112233445566778899aabbccddeeff", "devnet")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { vault.Close() }()
			if location == "remote" {
				err = vault.ImportControlledOrdinarySeed(bytes.Repeat([]byte{9}, 32), password)
			} else {
				err = vault.Create(password)
			}
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "app")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			transport, closeTransport := walletfixture.New()
			defer closeTransport()
			var server *Server
			start := func() {
				var err error
				server, err = Start(t.Context(), dir, "127.0.0.1:0", "", fasedapp.UI())
				if err != nil {
					t.Fatal(err)
				}
				server.Store.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
					if r.Action != "status" {
						t.Fatal("wallet read requested owner action", r.Action)
					}
					return signer.Response{Status: vault.Status()}, nil
				}
				server.Store.custodyOwnerSupported = true
				server.Store.custodyNetwork = "devnet"
				if err = server.ConfigureConnection(location, os.Getuid()); err != nil {
					t.Fatal(err)
				}
				if err = server.ConfigureControlledWalletReads(transport); err != nil {
					t.Fatal(err)
				}
			}
			start()
			defer func() { server.Close(context.Background()) }()
			claimFixture(t, server.Store)
			attach := func() WalletReadCommand {
				s := server.Store
				view, err := s.Accounts(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				view, err = s.SetupAccount(t.Context(), AccountSetupCommand{Revision: view.Record.Revision, Action: "attach", Name: "Installed " + location, Network: "devnet", Reference: vault.Status().Account})
				if err != nil {
					t.Fatal(err)
				}
				account := view.Record.Accounts[len(view.Record.Accounts)-1]
				view, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{Revision: view.Record.Revision, AccountID: account.ID, Endpoint: walletfixture.Endpoint, Network: "devnet"})
				if err != nil {
					t.Fatal(err)
				}
				c, err := s.RPCCredential(t.Context(), RPCCredentialCommand{Revision: view.Record.Revision, AccountID: account.ID, Action: "save", Key: disposableProviderKey, Passphrase: rpcPassword})
				if err != nil {
					t.Fatal(err)
				}
				return WalletReadCommand{Revision: view.Record.Revision, AccountID: account.ID, Ref: c.Ref, Passphrase: rpcPassword}
			}
			input := attach()
			s := server.Store
			before, err := os.ReadFile(s.rpcVaultPath(input.AccountID))
			if err != nil {
				t.Fatal(err)
			}
			record, _ := s.accountsRecord(t.Context())
			recordBefore, _ := json.Marshal(record)
			read := func(in WalletReadCommand) WalletSnapshot {
				snapshot, err := server.Store.ReadWallet(t.Context(), in)
				if err != nil || snapshot.Lamports != walletfixture.Lamports || snapshot.LiveRPC || !snapshot.FixtureOnly {
					t.Fatal(snapshot, err)
				}
				in.Selection = &WalletSelection{WalletActivity: snapshot.Activity[0], FetchedAt: snapshot.FetchedAt, ExpiresAt: snapshot.ExpiresAt, Binding: snapshot.Binding}
				detail, err := server.Store.ReadWallet(t.Context(), in)
				if err != nil || detail.Transaction == nil || detail.Transaction.FeeLamports != "5000" || detail.Transaction.Payer != snapshot.Address || detail.Transaction.NativeChange != "-5000" {
					t.Fatal(detail, err)
				}
				return detail
			}
			read(input)
			server.Close(context.Background())
			start()
			read(input)
			after, _ := os.ReadFile(server.Store.rpcVaultPath(input.AccountID))
			if !bytes.Equal(before, after) {
				t.Fatal("restart/read rewrote RPC vault")
			}
			current, _ := server.Store.accountsRecord(t.Context())
			raw, _ := json.Marshal(current)
			if !bytes.Equal(raw, recordBefore) {
				t.Fatal("read rewrote accounts")
			}
			if !vault.Status().Locked {
				t.Fatal("read unlocked signing key")
			}
			if err := vault.Unlock(password, time.Minute); err != nil {
				t.Fatal(err)
			}
			backup, err := vault.Backup(password)
			if err != nil {
				t.Fatal(err)
			}
			vault.Close()
			newdir := filepath.Join(root, "restored")
			if err = os.Mkdir(newdir, 0700); err != nil {
				t.Fatal(err)
			}
			vault, err = signer.OpenControlledOwnerCustody(newdir, "00112233445566778899aabbccddeeff", "devnet")
			if err != nil {
				t.Fatal(err)
			}
			if err = vault.Restore(backup, password, password); err != nil {
				t.Fatal(err)
			}
			if got, err := server.Store.ReadWallet(t.Context(), input); err == nil || got.Version != 0 {
				t.Fatal("recovery returned stale wallet data")
			}
			after, _ = os.ReadFile(server.Store.rpcVaultPath(input.AccountID))
			if !bytes.Equal(before, after) {
				t.Fatal("recovery rewrote old ciphertext")
			}
			fresh := attach()
			if fresh.AccountID == input.AccountID {
				t.Fatal("recovered reference reused old account")
			}
			read(fresh)
			if server.Store.rpcAdmission || !vault.Status().Locked || !vault.Status().RecoveryRequired || !vault.Status().AdmissionHeld {
				t.Fatal("read/reconnect released custody hold")
			}
		})
	}
}

// This test-binary entry serves the same core/UI to an unprivileged browser and
// CLI. It is supporting evidence only; installed UID isolation is checked by the
// existing disposable Ubuntu enrollment harness.
func TestOrdinaryWalletReadUIFixture(t *testing.T) {
	root := os.Getenv("FASED_ORDINARY_READ_UI_DIR")
	if root == "" {
		t.Skip("explicit disposable browser fixture only")
	}
	if err := privateDir(root); err != nil {
		t.Fatal(err)
	}
	location := os.Getenv("FASED_ORDINARY_READ_LOCATION")
	if location != "local" && location != "remote" {
		t.Fatal("fixture location")
	}
	password := []byte("Disposable signer password 123")
	rpcPassword := "Disposable RPC vault password 123"
	keydir := filepath.Join(root, "keys")
	if err := os.MkdirAll(keydir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "recover")); err == nil {
		keydir = filepath.Join(root, "restored")
		if err = os.MkdirAll(keydir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	vault, err := signer.OpenControlledOwnerCustody(keydir, "00112233445566778899aabbccddeeff", "devnet")
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	if vault.Status().Account == nil {
		if keydir == filepath.Join(root, "restored") {
			backup, err := os.ReadFile(filepath.Join(root, "backup.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err = vault.Restore(backup, password, password); err != nil {
				t.Fatal(err)
			}
		} else {
			if location == "remote" {
				err = vault.ImportControlledOrdinarySeed(bytes.Repeat([]byte{9}, 32), password)
			} else {
				err = vault.Create(password)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = vault.Unlock(password, time.Minute); err != nil {
				t.Fatal(err)
			}
			backup, err := vault.Backup(password)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, "backup.json"), backup, 0600); err != nil {
				t.Fatal(err)
			}
			vault.Lock()
		}
	}
	dir := filepath.Join(root, "app")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	server, err := Start(t.Context(), dir, "127.0.0.1:0", "", fasedapp.UI())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	server.Store.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		if r.Action != "status" {
			return signer.Response{}, signer.ErrUnavailable
		}
		return signer.Response{Status: vault.Status()}, nil
	}
	server.Store.custodyOwnerSupported = true
	server.Store.custodyNetwork = "devnet"
	if err = server.ConfigureConnection(location, os.Getuid()); err != nil {
		t.Fatal(err)
	}
	transport, closeTransport := walletfixture.New()
	defer closeTransport()
	if err = server.ConfigureControlledWalletReads(transport); err != nil {
		t.Fatal(err)
	}
	claimed, err := server.Store.Claimed(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		claimFixture(t, server.Store)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve() }()
	ready := map[string]any{"url": server.URL, "dataDir": dir, "ownerPassword": testPassword, "vaultPassword": rpcPassword, "reference": vault.Status().Account, "status": vault.Status()}
	pending := filepath.Join(root, "read-ready.pending")
	if err = WritePrivateJSON(pending, ready); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(pending, filepath.Join(root, "read-ready.json")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-t.Context().Done():
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(90 * time.Second):
	}
}

func TestControlledWalletReadConstructorBoundary(t *testing.T) {
	s, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	server := &Server{Store: s}
	transport := &http.Transport{}
	if err := server.ConfigureControlledWalletReads(transport); err == nil {
		t.Fatal("standalone constructor admitted installed wallet reads")
	}
	if s.rpcAdmission || s.walletReadFixture != nil {
		t.Fatal("refused constructor changed admission")
	}
	if err := server.ConfigureControlledWalletReads(nil); err == nil {
		t.Fatal("nil transport")
	}
	s.custodyOwnerSupported = true
	s.custodyNetwork = "devnet"
	if err := server.ConfigureControlledWalletReads(transport); err != nil {
		t.Fatal(err)
	}
	if s.rpcAdmission {
		t.Fatal("controlled constructor enabled live RPC")
	}
	if err := server.ConfigureControlledWalletReads(transport); err == nil {
		t.Fatal("transport replaced after startup")
	}
}
