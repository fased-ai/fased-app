package signer

import "testing"

func TestOrdinaryCustodyConfigContract(t *testing.T) {
	t.Parallel()
	c := Config{Version: 3, Profile: OwnerCustodyProfile, Provenance: OwnerKeyProvenance, Mode: "ordinary-custody:v1", Network: "devnet", SignerID: "00112233445566778899aabbccddeeff", OwnerUID: 1101, AppUID: 1102, SignerUID: 1103, StateDir: "/state", SocketDir: "/socket"}
	if err := c.Validate(); err != nil {
		t.Fatalf("ordinary custody contract refused: %v", err)
	}
	c.Network, c.Provenance = "", ControlledCustodyProfile
	if c.Validate() == nil {
		t.Fatal("an unbound controlled pre-wallet signer was accepted")
	}
}
