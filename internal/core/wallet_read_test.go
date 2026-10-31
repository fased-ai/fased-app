package core

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	fasedapp "github.com/fased-ai/fased-app"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

type walletReadTestState struct {
	mu     sync.Mutex
	mode   string
	calls  int
	change func()
}

func (f *walletReadTestState) set(mode string) { f.mu.Lock(); defer f.mu.Unlock(); f.mode = mode }
func walletReadTLS(t *testing.T) (*walletReadTestState, walletReader) {
	t.Helper()
	f := &walletReadTestState{}
	address := onchain.Base58(bytes.Repeat([]byte{2}, 32))
	mint := onchain.Base58(bytes.Repeat([]byte{3}, 32))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid request")
			return
		}
		if r.URL.Query().Get("api-key") != disposableProviderKey || r.Host != "devnet.helius-rpc.com" || r.Header.Get("Authorization") != "" {
			t.Error("credential routing")
		}
		f.mu.Lock()
		mode := f.mode
		f.calls++
		change := f.change
		f.mu.Unlock()
		if request.Method != "getGenesisHash" {
			var owner string
			_ = json.Unmarshal(request.Params[0], &owner)
			if owner != address {
				t.Error("wallet binding")
			}
		}
		var result any
		switch request.Method {
		case "getGenesisHash":
			result = solanaGenesis("devnet")
			if mode == "network" {
				result = solanaGenesis("mainnet-beta")
			}
		case "getBalance":
			value := uint64(9007199254740993)
			if mode == "zero" {
				value = 0
			}
			result = map[string]any{"context": map[string]any{"slot": 100}, "value": value}
			if mode == "change" && change != nil {
				change()
			}
			if mode == "cancel" {
				<-r.Context().Done()
				return
			}
		case "getTokenAccountsByOwner":
			var filter struct {
				Program string `json:"programId"`
			}
			_ = json.Unmarshal(request.Params[1], &filter)
			entries := []any{}
			if mode != "zero" && filter.Program == onchain.Token {
				for _, n := range []byte{4, 5} {
					owner := address
					if mode == "owner" {
						owner = mint
					}
					amount := "9007199254740993"
					if n == 5 {
						amount = "7"
					}
					if mode == "amount" {
						amount = "1e9"
					}
					entries = append(entries, map[string]any{"pubkey": onchain.Base58(bytes.Repeat([]byte{n}, 32)), "account": map[string]any{"owner": filter.Program, "executable": false, "data": map[string]any{"parsed": map[string]any{"type": "account", "info": map[string]any{"owner": owner, "mint": mint, "state": "initialized", "tokenAmount": map[string]any{"amount": amount, "decimals": 6}}}}}})
				}
			}
			slot := 100
			if mode == "slot" {
				slot = 99
			}
			result = map[string]any{"context": map[string]any{"slot": slot}, "value": entries}
		case "getSignaturesForAddress":
			var config struct {
				Limit      int    `json:"limit"`
				Commitment string `json:"commitment"`
			}
			_ = json.Unmarshal(request.Params[1], &config)
			if config.Limit != 20 || config.Commitment != "finalized" {
				t.Error("unbounded activity")
			}
			result = []any{}
			if mode != "zero" {
				result = []any{map[string]any{"signature": onchain.Base58(bytes.Repeat([]byte{6}, 64)), "slot": 99, "err": nil, "blockTime": nil, "confirmationStatus": "finalized"}}
			}
		default:
			t.Error("unrequested method", request.Method)
		}
		if mode == "failure" {
			http.Error(w, "provider secret must not escape", 502)
			return
		}
		if mode == "oversize" {
			w.Write(bytes.Repeat([]byte{'x'}, 262145))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	t.Cleanup(server.Close)
	next := newPublicRPCTransport()
	next.enabled = true
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	next.transport.TLSClientConfig.RootCAs = roots
	next.transport.TLSClientConfig.ServerName = "example.com"
	next.transport.DialContext = func(ctx context.Context, network, destination string) (net.Conn, error) {
		if destination != "devnet.helius-rpc.com:443" {
			t.Fatal("foreign dial", destination)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(next.transport.CloseIdleConnections)
	read := func(ctx context.Context, a Account, address string, key []byte, valid func() error) (WalletSnapshot, error) {
		transport := &providerRPCTransport{origin: a.RPC.Endpoint, provider: "helius", key: key, valid: valid, next: next}
		return walletRPC(ctx, a.RPC.Endpoint, transport, valid, true, address, a.Network)
	}
	return f, read
}

func walletReadAccount(t *testing.T, s *Store) WalletReadCommand {
	t.Helper()
	claimFixture(t, s)
	s.rpcAdmission = true
	record := defaultAccounts()
	account := publicRecord()
	account.Version = 2
	account.Network = "devnet"
	account.PublicKey = strings.Repeat("02", 32)
	record.Accounts = []Account{account}
	view, err := s.UpdateAccounts(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	view, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: account.ID, Revision: view.Record.Revision, Endpoint: "https://devnet.helius-rpc.com", Network: "devnet"})
	if err != nil {
		t.Fatal(err)
	}
	command := RPCCredentialCommand{AccountID: account.ID, Revision: view.Record.Revision, Action: "save", Key: disposableProviderKey, Passphrase: "Disposable provider vault password 123"}
	saved, err := s.RPCCredential(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	return WalletReadCommand{AccountID: account.ID, Revision: command.Revision, Ref: saved.Ref, Passphrase: command.Passphrase}
}

func TestWalletReadHTTPSAndPreservation(t *testing.T) {
	dir := fixtureDir(t)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	in := walletReadAccount(t, s)
	state, read := walletReadTLS(t)
	s.walletReadFixture = read
	before, err := os.ReadFile(s.rpcVaultPath(in.AccountID))
	if err != nil {
		t.Fatal(err)
	}
	record, _ := s.accountsRecord(t.Context())
	original, _ := json.Marshal(record)
	result, err := s.ReadWallet(t.Context(), in)
	if err != nil || result.Lamports != "9007199254740993" || len(result.Tokens) != 1 || result.Tokens[0].Amount != "9007199254741000" || len(result.Activity) != 1 || !result.FixtureOnly || result.LiveRPC || result.Binding == "" || result.ExpiresAt-result.FetchedAt != 60 {
		t.Fatal(result, err)
	}
	for _, mode := range []string{"network", "owner", "amount", "slot", "failure", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			state.set(mode)
			v, err := s.ReadWallet(t.Context(), in)
			if err == nil || v.Version != 0 || strings.Contains(err.Error(), "secret") {
				t.Fatal("invalid/partial snapshot", v, err)
			}
		})
	}
	state.set("zero")
	zero, err := s.ReadWallet(t.Context(), in)
	if err != nil || zero.Lamports != "0" || len(zero.Tokens) != 0 || len(zero.Activity) != 0 {
		t.Fatal(zero, err)
	}
	after, _ := os.ReadFile(s.rpcVaultPath(in.AccountID))
	current, _ := s.accountsRecord(t.Context())
	encoded, _ := json.Marshal(current)
	if !bytes.Equal(before, after) || !bytes.Equal(original, encoded) {
		t.Fatal("read changed ciphertext/accounts")
	}
}
func TestWalletReadLocksRestartAndRevocation(t *testing.T) {
	dir := fixtureDir(t)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	in := walletReadAccount(t, s)
	state, read := walletReadTLS(t)
	s.walletReadFixture = read
	bad := in
	bad.Passphrase = "Wrong disposable vault password"
	if _, err = s.ReadWallet(t.Context(), bad); err == nil {
		t.Fatal("wrong password")
	}
	bad = in
	bad.Ref = "old"
	if _, err = s.ReadWallet(t.Context(), bad); err == nil {
		t.Fatal("wrong credential")
	}
	var authorized atomic.Bool
	authorized.Store(true)
	state.set("change")
	state.change = func() { authorized.Store(false) }
	if _, err = s.readWallet(t.Context(), in, authorized.Load); err == nil {
		t.Fatal("signed-out request returned data")
	}
	state.change = func() {
		v, _ := s.accountsRecord(t.Context())
		v.Accounts[0].Name = "Renamed"
		_, e := s.UpdateAccounts(t.Context(), v)
		if e != nil {
			t.Error(e)
		}
	}
	if _, err = s.ReadWallet(t.Context(), in); err == nil {
		t.Fatal("configuration changed during read")
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.walletReadFixture = read
	state.set("zero")
	v, _ := s.accountsRecord(t.Context())
	in.Revision = v.Revision
	if _, err = s.ReadWallet(t.Context(), in); err != nil {
		t.Fatal("restart/name-only change stranded encrypted key", err)
	}
	s.walletReadFixture = nil
	if _, err = s.ReadWallet(t.Context(), in); err == nil {
		t.Fatal("restart enabled ordinary live reads")
	}
}

func TestWalletReadCredentialRotationPauseAndCancellation(t *testing.T) {
	s := fixtureStore(t)
	defer s.Close()
	in := walletReadAccount(t, s)
	state, read := walletReadTLS(t)
	s.walletReadFixture = read
	s.walletReadMu.Lock()
	if _, err := s.ReadWallet(t.Context(), in); err == nil {
		t.Fatal("parallel credential reads unbounded")
	}
	s.walletReadMu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ReadWallet(ctx, in); err == nil {
		t.Fatal("cancelled read")
	}
	state.set("change")
	state.change = func() {
		_, err := s.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: in.AccountID, Revision: in.Revision, Ref: in.Ref, Passphrase: in.Passphrase, Action: "save", Key: disposableProviderKey + "-rotated"})
		if err != nil {
			t.Error(err)
		}
	}
	if result, err := s.ReadWallet(t.Context(), in); err == nil || result.Version != 0 {
		t.Fatal("rotated key returned snapshot", err)
	}
	if _, err := s.UpdatePause(t.Context(), pauseCommand(t, s, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadWallet(t.Context(), in); !errors.Is(err, ErrCorePaused) {
		t.Fatal("pause allowed credential use", err)
	}
}

func TestWalletReadRejectsRecoveredSignerEpoch(t *testing.T) {
	s := fixtureStore(t)
	defer s.Close()
	claimFixture(t, s)
	keydir := t.TempDir()
	if err := os.Chmod(keydir, 0700); err != nil {
		t.Fatal(err)
	}
	v, err := signer.OpenControlledOwnerCustody(keydir, "00112233445566778899aabbccddeeff", "devnet")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err = v.Create([]byte("Disposable signer password 123")); err != nil {
		t.Fatal(err)
	}
	status := v.Status()
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: status}, nil
	}
	s.custodyOwnerSupported = true
	view, err := s.Accounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	view, err = s.SetupAccount(t.Context(), AccountSetupCommand{Revision: view.Record.Revision, Action: "attach", Name: "Wallet", Network: "devnet", Reference: status.Account})
	if err != nil {
		t.Fatal(err)
	}
	id := view.Record.Accounts[0].ID
	view, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: id, Revision: view.Record.Revision, Endpoint: "https://devnet.helius-rpc.com", Network: "devnet"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: id, Revision: view.Record.Revision, Action: "save", Key: disposableProviderKey, Passphrase: "Disposable provider vault password 123"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.rpcVaultPath(id))
	s.walletReadFixture = func(ctx context.Context, a Account, address string, key []byte, valid func() error) (WalletSnapshot, error) {
		ref := *status.Account
		ref.Epoch = strings.Repeat("d", 32)
		status.Account = &ref
		return WalletSnapshot{Version: 1, Lamports: "0"}, nil
	}
	result, err := s.ReadWallet(t.Context(), WalletReadCommand{AccountID: id, Revision: view.Record.Revision, Ref: saved.Ref, Passphrase: "Disposable provider vault password 123"})
	if err == nil || result.Version != 0 {
		t.Fatal("recovered epoch returned old data", err)
	}
	after, _ := os.ReadFile(s.rpcVaultPath(id))
	if !bytes.Equal(before, after) {
		t.Fatal("read rewrote old credential")
	}
}

func TestWalletReadRejectsInterveningPauseRelease(t *testing.T) {
	s := fixtureStore(t)
	defer s.Close()
	in := walletReadAccount(t, s)
	state, read := walletReadTLS(t)
	s.walletReadFixture = read
	state.set("change")
	state.change = func() {
		pause := pauseCommand(t, s, 1)
		if _, err := s.UpdatePause(t.Context(), pause); err != nil {
			t.Error(err)
		}
		if _, err := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "release", ProbeID: pause.ProbeID, Aborted: aborted(pause)}); err != nil {
			t.Error(err)
		}
	}
	result, err := s.ReadWallet(t.Context(), in)
	if err == nil || result.Version != 0 {
		t.Fatal("read survived pause/release", result, err)
	}
}

// Explicit compiled test entry only, sharing the existing core/IPC and encrypted
// vault. No product CLI/env flag can install this TLS transport or admit reads.
func TestWalletReadUIFixture(t *testing.T) {
	dir := os.Getenv("FASED_WALLET_READ_UI_DIR")
	if dir == "" {
		t.Skip("explicit UI fixture only")
	}
	server, err := Start(t.Context(), dir, "127.0.0.1:0", "", fasedapp.UI())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	var in WalletReadCommand
	claimed, err := server.Store.Claimed(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		in = walletReadAccount(t, server.Store)
	} else {
		record, err := server.Store.accountsRecord(t.Context())
		if err != nil || len(record.Accounts) != 1 {
			t.Fatal("retained fixture account unavailable", err)
		}
		account := record.Accounts[0]
		vault, err := server.Store.readRPCVault(account.ID)
		if err != nil {
			t.Fatal(err)
		}
		in = WalletReadCommand{AccountID: account.ID, Revision: record.Revision, Ref: vault.Ref, Passphrase: "Disposable provider vault password 123"}
	}
	state, read := walletReadTLS(t)
	server.Store.walletReadFixture = func(ctx context.Context, a Account, address string, key []byte, valid func() error) (WalletSnapshot, error) {
		if b, e := os.ReadFile(filepath.Join(dir, "read-mode")); e == nil {
			state.set(strings.TrimSpace(string(b)))
		} else {
			state.set("")
		}
		return read(ctx, a, address, key, valid)
	}
	if err = server.ConfigureConnection("local"); err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve() }()
	ready := map[string]any{"url": server.URL, "input": in, "ownerPassword": testPassword}
	if err = WritePrivateJSON(filepath.Join(dir, "wallet-read-ready.json"), ready); err != nil {
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
