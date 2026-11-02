package releaseprovenance_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestGitHubTagPushOriginalAttestation(t *testing.T) {
	// These are immutable public bytes from the real tag-push producer, not a
	// statement assembled from the verifier's own constants.
	read := func(name, want string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("testdata", "github-v0.1.2", name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("original public fixture changed", name)
		}
		return raw
	}
	var p releaseprovenance.Policy
	var e releaseprovenance.Expected
	if err := json.Unmarshal(read("policy.json", "f128e064e35eac2f7df396e67397e004f62f6830fe7d8ed112cc71a5a974fc38"), &p); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(read("expected.json", "d70a2658e8581dd8023746bfee6c3a1e37e86af5caf7ff163e93a731a1920b3b"), &e); err != nil {
		t.Fatal(err)
	}
	raw := read("attestation.sigstore.json", "09370a64a0716f863c94d65f9c83e0d588dc12069a389568fa921058cff89f1c")
	// Verification uses the attested signing time; keep this observation clock
	// fixed after the original run so the regression needs no network or clock.
	now := time.Date(2026, time.November, 2, 10, 30, 0, 0, time.UTC)
	d, err := releaseprovenance.Verify(p, raw, e, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.Identity != "https://github.com/fased-ai/fased-app/.github/workflows/release.yml@refs/tags/v0.1.2" || d.TimestampSource != "Tlog" || d.VerifiedAt.IsZero() {
		t.Fatal("wrong original verified identity", d)
	}
	for _, name := range []string{"signature", "digest", "source", "tag", "run"} {
		t.Run(name, func(t *testing.T) {
			expected, bad := e, raw
			switch name {
			case "signature":
				var signed protobundle.Bundle
				if err := protojson.Unmarshal(raw, &signed); err != nil {
					t.Fatal(err)
				}
				signed.GetDsseEnvelope().Signatures[0].Sig[0] ^= 1
				var err error
				bad, err = protojson.Marshal(&signed)
				if err != nil {
					t.Fatal(err)
				}
			case "digest":
				expected.ArchiveSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			case "source":
				expected.SourceCommit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				expected.WorkflowCommit = expected.SourceCommit
			case "tag":
				expected.Tag = "v0.1.3"
			case "run":
				expected.RunAttempt = "2"
			}
			requireRefusal(t, p, bad, expected, now)
		})
	}
}
