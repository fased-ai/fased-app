package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

func TestWalletSetupWatchAddressAndPreservation(t *testing.T) {
	s, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claimFixture(t, s)
	old := defaultAccounts()
	old.Accounts = []Account{publicRecord()}
	v, err := s.UpdateAccounts(t.Context(), old)
	if err != nil {
		t.Fatal(err)
	}
	address := testRef().PublicIdentity().Address
	c := AccountSetupCommand{Revision: v.Record.Revision, Action: "watch", Name: "Research", Network: "devnet", Address: address}
	before, _ := json.Marshal(v.Record)
	if _, err = s.SetupAccount(t.Context(), c); err == nil {
		t.Fatal("ordinary admission enabled")
	}
	unchanged, _ := s.Accounts(t.Context())
	after, _ := json.Marshal(unchanged.Record)
	if string(before) != string(after) {
		t.Fatal("held setup changed records")
	}
	s.rpcAdmission = true // Test-only constructor boundary; no network call.
	v, err = s.SetupAccount(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Record.Accounts) != 2 || v.Record.Accounts[0] != old.Accounts[0] {
		t.Fatal("original record changed")
	}
	a := v.Record.Accounts[1]
	if a.Version != 2 || a.Mode != "watch" || a.Network != "devnet" || a.PublicKey != testRef().PublicKey || v.Addresses[a.ID] != address || a.Signer != nil {
		t.Fatal("wrong public identity", a)
	}
	if _, err = s.SetupAccount(t.Context(), c); err != ErrConflict {
		t.Fatal("stale creation accepted", err)
	}
	c.Revision = v.Record.Revision
	if _, err = s.SetupAccount(t.Context(), c); err == nil {
		t.Fatal("duplicate creation accepted")
	}
}

func TestWalletSetupAttachRequiresCurrentOwnerReference(t *testing.T) {
	s, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claimFixture(t, s)
	ref := testRef()
	ref.Version, ref.Network = 2, "devnet"
	current := ref
	status := signer.Status{FixtureOnly: true, Locked: true, RecoveryRequired: true, Account: &current}
	s.custodyNetwork = "devnet"
	s.fixture = func(_ context.Context, req signer.Request) (signer.Response, error) {
		if req.Action != "status" {
			t.Fatalf("setup requested owner mutation %s", req.Action)
		}
		return signer.Response{Status: status}, nil
	}
	v, _ := s.Accounts(t.Context())
	if v.Setup.State != "recovery" || v.Setup.Network != ref.Network {
		t.Fatal("hold hidden", v.Setup)
	}
	c := AccountSetupCommand{Action: "attach", Name: "Wallet", Network: "devnet", Reference: &ref}
	v, err = s.SetupAccount(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Record.Accounts) != 1 || v.Record.Accounts[0].Version != 3 || *v.Record.Accounts[0].Signer != ref || v.Setup.State != "recovery" || !status.Locked {
		t.Fatal("attach changed custody")
	}
	current.Epoch = strings.Repeat("9", 32)
	c.Revision = v.Record.Revision
	if _, err = s.SetupAccount(t.Context(), c); err == nil {
		t.Fatal("stale epoch attached")
	}
	c.Reference = &current
	c.Network = "mainnet-beta"
	if _, err = s.SetupAccount(t.Context(), c); err == nil {
		t.Fatal("retargeted owner identity")
	}
	status.WalletSigner = true
	c.Network = "devnet"
	if _, err = s.SetupAccount(t.Context(), c); err == nil {
		t.Fatal("non-fixture signer admitted")
	}
}

func TestWalletSetupInvalidInputsNeverWrite(t *testing.T) {
	s, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claimFixture(t, s)
	s.rpcAdmission = true
	c := AccountSetupCommand{Action: "watch", Name: "Watch", Network: "devnet", Address: testRef().PublicIdentity().Address}
	for _, mutate := range []func(*AccountSetupCommand){
		func(c *AccountSetupCommand) { c.Address = strings.Repeat("4", 64) },
		func(c *AccountSetupCommand) { c.Address = " " + c.Address },
		func(c *AccountSetupCommand) { c.Address = "0OIl" },
		func(c *AccountSetupCommand) { c.Network = signer.FixtureNetwork },
		func(c *AccountSetupCommand) { c.Network = "testnet" },
		func(c *AccountSetupCommand) { c.Action = "create" },
		func(c *AccountSetupCommand) { c.Action = "import" },
		func(c *AccountSetupCommand) { r := testRef(); c.Reference = &r },
	} {
		bad := c
		mutate(&bad)
		if _, err = s.SetupAccount(t.Context(), bad); err == nil {
			t.Fatal("invalid input accepted")
		}
		v, _ := s.Accounts(t.Context())
		if len(v.Record.Accounts) != 0 || v.Record.Revision != 0 {
			t.Fatal("partial write")
		}
	}
}

func TestWalletSetupMetadataIsReadOnlyAndNoSecret(t *testing.T) {
	s, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claimFixture(t, s)
	v, _ := s.Accounts(t.Context())
	if v.Setup.State != "not-installed" || v.Setup.NetworkWatchEnabled {
		t.Fatal("fabricated readiness")
	}
	s.custodyNetwork = "mainnet-beta"
	s.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		if r.Action != "status" {
			t.Fatal("mutation")
		}
		return signer.Response{Status: signer.Status{FixtureOnly: true, Locked: true}}, nil
	}
	v, _ = s.Accounts(t.Context())
	if v.Setup.State != "empty" || v.Setup.Network != "mainnet-beta" {
		t.Fatal("configured network missing", v.Setup)
	}
	b, _ := json.Marshal(v.Setup)
	for _, secret := range []string{"password", "seed", "stateDir", "socketDir", "ownerUid", "unlockUntil"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("private setup field", secret)
		}
	}
	if len(v.Record.Accounts) != 0 || v.Record.Revision != 0 {
		t.Fatal("inspection wrote records")
	}
}

func TestWalletSetupRecoveredEpochPreservesOriginalRecord(t *testing.T) {
	s, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claimFixture(t, s)
	ref := testRef()
	ref.Version, ref.Network = 2, "devnet"
	current := ref
	s.custodyNetwork = "devnet"
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: signer.Status{FixtureOnly: true, Locked: true, RecoveryRequired: true, Account: &current}}, nil
	}
	v, err := s.SetupAccount(t.Context(), AccountSetupCommand{Action: "attach", Name: "Original", Network: "devnet", Reference: &ref})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(v.Record.Accounts[0])
	current.Epoch = strings.Repeat("9", 32)
	c := AccountSetupCommand{Revision: v.Record.Revision, Action: "attach", Name: "Recovered", Network: "devnet", Reference: &current}
	v, err = s.SetupAccount(t.Context(), c)
	if err != nil {
		t.Fatal("fresh recovered reference refused", err)
	}
	after, _ := json.Marshal(v.Record.Accounts[0])
	if string(before) != string(after) || len(v.Record.Accounts) != 2 || v.AccountStates[v.Record.Accounts[0].ID] != "stale" || v.Setup.State != "recovery" {
		t.Fatal("recovery metadata rewrote original/hold")
	}
	c.Revision = v.Record.Revision
	if _, err = s.SetupAccount(t.Context(), c); err == nil {
		t.Fatal("duplicate current epoch")
	}
}

func TestWalletSetupHoldBeforeCommitRefusesWithoutWrite(t *testing.T) {
	s, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claimFixture(t, s)
	ref := testRef()
	ref.Version, ref.Network = 2, "devnet"
	s.custodyNetwork = "devnet"
	calls := 0
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		calls++
		return signer.Response{Status: signer.Status{FixtureOnly: true, Locked: true, Account: &ref, UpdateHeld: calls > 1}}, nil
	}
	_, err = s.SetupAccount(t.Context(), AccountSetupCommand{Action: "attach", Name: "Wallet", Network: "devnet", Reference: &ref})
	if err == nil {
		t.Fatal("signer maintenance began before commit but attach succeeded")
	}
	v, _ := s.Accounts(t.Context())
	if len(v.Record.Accounts) != 0 || v.Record.Revision != 0 {
		t.Fatal("partial held write")
	}
}

func TestWalletSetupRecoveredSignerMarksRPCCredentialStale(t *testing.T) {
	s, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claimFixture(t, s)
	s.rpcAdmission = true
	ref := testRef()
	ref.Version, ref.Network = 2, "devnet"
	current := ref
	s.custodyNetwork = "devnet"
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: signer.Status{FixtureOnly: true, Locked: true, RecoveryRequired: true, Account: &current}}, nil
	}
	v, err := s.SetupAccount(t.Context(), AccountSetupCommand{Action: "attach", Name: "Wallet", Network: "devnet", Reference: &ref})
	if err != nil {
		t.Fatal(err)
	}
	a := v.Record.Accounts[0]
	v, err = s.ConfigureAccountRPC(t.Context(), RPCConfigInput{AccountID: a.ID, Revision: v.Record.Revision, Endpoint: "https://devnet.helius-rpc.com", Network: "devnet"})
	if err != nil {
		t.Fatal(err)
	}
	c := RPCCredentialCommand{AccountID: a.ID, Revision: v.Record.Revision, Action: "save", Key: disposableProviderKey, Passphrase: "Disposable wallet RPC password 123"}
	saved, err := s.RPCCredential(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	c.Action, c.Key, c.Passphrase, c.Ref = "status", "", "", saved.Ref
	status, err := s.RPCCredential(t.Context(), c)
	if err != nil || status.Stale {
		t.Fatal("current identity stale", err)
	}
	current.Epoch = strings.Repeat("9", 32)
	status, err = s.RPCCredential(t.Context(), c)
	if err != nil || !status.Stale || !status.Locked || !status.Saved {
		t.Fatal("recovered signer left RPC credential apparently current", status, err)
	}
}
