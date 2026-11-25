package hub

import (
	"encoding/json"
	"testing"
)

func FuzzHubManifestAndSignature(f *testing.F) {
	f.Add(`{"id":"hub-test-1","name":"Test","kind":"fasedscript","version":"1.0.0","payload":"const x = sma(close, 5); signal(\"long\", close > x);","authorPubKeyHex":"00","signatureHex":"00"}`)
	f.Add(`{"id":"bad","name":"","kind":"unknown","version":"bad"}`)

	f.Fuzz(func(t *testing.T, raw string) {
		var pkg Package
		if json.Unmarshal([]byte(raw), &pkg) == nil {
			_ = VerifyPackage(&pkg)
		}
	})
}
