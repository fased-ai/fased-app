package signer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPreWalletIdentityDoesNotAdmitSignerOperations(t *testing.T) {
	c := Config{Version: 3, Mode: OrdinaryCustodyMode, Profile: OwnerCustodyProfile, Provenance: OwnerKeyProvenance, SignerID: strings.Repeat("a", 32), OwnerUID: 1000, AppUID: 1001, SignerUID: 1002, StateDir: "/var/lib/fased-test/signer-state", SocketDir: "/var/lib/fased-test/signer-ipc"}
	if err := c.Validate(); err != nil {
		t.Fatal("pre-wallet identity rejected:", err)
	}
	if _, err := Serve(context.Background(), c); !errors.Is(err, ErrUnavailable) {
		t.Fatal("pre-wallet identity started the signer", err)
	}
	for _, change := range []func(*Config){
		func(v *Config) { v.Network = "unknown" },
		func(v *Config) { v.Provenance = ControlledCustodyProfile },
		func(v *Config) { v.Version = 2 },
	} {
		bad := c
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("ambiguous pre-wallet identity accepted")
		}
	}
}
