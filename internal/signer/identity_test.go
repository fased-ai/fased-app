//go:build linux

package signer

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPublicIdentityEncodingAndRefusal(t *testing.T) {
	ref := AccountRef{Version: 1, SignerID: strings.Repeat("1", 32), WalletID: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32), Network: FixtureNetwork}
	for _, tc := range []struct{ hex, address string }{
		{strings.Repeat("0", 64), strings.Repeat("1", 32)},
		{strings.Repeat("0", 63) + "1", strings.Repeat("1", 31) + "2"},
		{strings.Repeat("f", 64), "JEKNVnkbo3jma5nREBBJCDoXFVeKkD56V3xKrvRmWxFG"},
	} {
		ref.PublicKey = tc.hex
		p := ref.PublicIdentity()
		if p == nil || p.Address != tc.address || p.Network != FixtureNetwork || !p.FixtureOnly {
			t.Fatal("address vector", p)
		}
	}
	for _, key := range []string{"", strings.Repeat("a", 62), strings.Repeat("F", 64), strings.Repeat("z", 64)} {
		ref.PublicKey = key
		if ref.PublicIdentity() != nil {
			t.Fatal("invalid key accepted")
		}
	}
	ref.PublicKey = strings.Repeat("0", 64)
	ref.Network = "mainnet-beta"
	if ref.PublicIdentity() != nil {
		t.Fatal("live network projected")
	}
}

func TestPublicIdentitySurvivesRecoveryWithoutAuthority(t *testing.T) {
	dir := fixtureDir(t)
	v := openFixture(t, dir)
	if e := v.Create(fixturePassword); e != nil {
		t.Fatal(e)
	}
	initial := v.Status().PublicView()
	if initial.PublicIdentity == nil || !initial.Locked || initial.WalletSigner {
		t.Fatal("identity requires unlock or grants signing")
	}
	if e := v.Close(); e != nil {
		t.Fatal(e)
	}
	v = openFixture(t, dir)
	if current := v.Status().PublicView(); current.PublicIdentity == nil || *current.PublicIdentity != *initial.PublicIdentity || !current.Locked {
		t.Fatal("restart changed identity or unlocked")
	}
	if e := v.Unlock(fixturePassword, time.Minute); e != nil {
		t.Fatal(e)
	}
	backup, e := v.Backup(backupPassword)
	if e != nil {
		t.Fatal(e)
	}
	defer wipe(backup)
	restored := openFixture(t, fixtureDir(t))
	if e = restored.Restore(backup, backupPassword, fixturePassword); e != nil {
		t.Fatal(e)
	}
	next := restored.Status().PublicView()
	if next.PublicIdentity == nil || *next.PublicIdentity != *initial.PublicIdentity || next.Account.Epoch == initial.Account.Epoch || !next.Locked || !next.RecoveryRequired || next.WalletSigner || next.FinancialExecution {
		t.Fatal("recovery identity/authority drift")
	}
}

func TestPublicIdentityDoesNotChangeSignerWire(t *testing.T) {
	ref := AccountRef{Version: 1, SignerID: strings.Repeat("1", 32), WalletID: strings.Repeat("2", 32), Epoch: strings.Repeat("3", 32), Network: FixtureNetwork, PublicKey: strings.Repeat("0", 64)}
	status := Status{FixtureOnly: true, Locked: true, Account: &ref}
	wire, e := json.Marshal(Response{Status: status})
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(wire, []byte("publicIdentity")) {
		t.Fatal("new field leaked into strict IPC")
	}
	var old Response
	if e = decodeWire(wire, &old); e != nil {
		t.Fatal(e)
	}
	public, e := json.Marshal(status.PublicView())
	if e != nil || !bytes.Contains(public, []byte("publicIdentity")) {
		t.Fatal("public view missing identity", e)
	}
}
