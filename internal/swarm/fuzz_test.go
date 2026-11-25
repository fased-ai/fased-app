package swarm

import (
	"encoding/json"
	"testing"
	"time"
)

func FuzzSwarmEnvelopeAndA2A(f *testing.F) {
	now := time.Unix(1795600000, 0)
	f.Add(`{"envelopeId":"sig-123","nonce":"nonce-12345678","kind":"signal","symbol":"BTC","direction":"LONG","thesis":"Fuzz check","confidence":0.5,"timestamp":1795600000}`)
	f.Add(`{"envelopeId":"bad","confidence":5}`)

	f.Fuzz(func(t *testing.T, raw string) {
		var env Envelope
		if json.Unmarshal([]byte(raw), &env) == nil {
			_ = VerifyEnvelope(&env, now)
		}
		_, _ = ValidateCopyMode(raw)
	})
}
