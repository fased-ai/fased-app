package signer

import "testing"

func TestOrdinaryCustodyConfigContract(t *testing.T) {
	c := Config{Version: 3, Mode: "ordinary-custody:v1", Network: "devnet", SignerID: "00112233445566778899aabbccddeeff", OwnerUID: 1101, AppUID: 1102, SignerUID: 1103, StateDir: "/state", SocketDir: "/socket"}
	if err := c.Validate(); err != nil {
		t.Fatalf("ordinary custody contract refused: %v", err)
	}
}
