package custody

import (
	"encoding/json"
	"testing"
)

func FuzzWebAuthnCBORAndCOSE(f *testing.F) {
	f.Add([]byte{0xa1, 0x68, 'a', 'u', 't', 'h', 'D', 'a', 't', 'a', 0x44, 0x01, 0x02, 0x03, 0x04}, "localhost", "http://localhost:7331")
	f.Add([]byte{0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x58, 0x20}, "example.com", "https://example.com")
	f.Add([]byte{}, "127.0.0.1", "http://127.0.0.1:7331")

	f.Fuzz(func(t *testing.T, raw []byte, rpID, origin string) {
		if len(raw) > 16*1024 {
			return
		}
		_, _, _ = validateWebAuthnOriginAndRP(rpID, origin)
		if authData, err := extractAuthDataFromAttestationObject(raw); err == nil {
			_, _ = parseAttestedAuthData(authData, rpID)
		}
		_, _ = parseAttestedAuthData(raw, rpID)
		_, _, _ = parseCOSEPublicKey(raw)

		var pol WalletPolicy
		if json.Unmarshal(raw, &pol) == nil {
			_ = ComputePolicyHash(pol)
		}
	})
}
