package releaseprovenance_test

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	"github.com/fased-ai/fased-app/internal/releaseprovenance/fixture"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"google.golang.org/protobuf/encoding/protojson"
)

func inputs() (releaseprovenance.Policy, releaseprovenance.Expected, time.Time) {
	p := releaseprovenance.Policy{Version: 1, Repository: "fased-ai/fased-app", RepositoryID: "7", OwnerID: "3", Workflow: releaseprovenance.WorkflowPath}
	e := releaseprovenance.Expected{ArchiveName: "fased-app-linux-amd64.tar.gz", ArchiveSHA256: strings.Repeat("a", 64), SourceCommit: strings.Repeat("b", 40), WorkflowCommit: strings.Repeat("b", 40), Tag: "v0.1.0", RunID: "11", RunAttempt: "2"}
	return p, e, time.Now().UTC().Truncate(time.Second)
}

func requireRefusal(t *testing.T, p releaseprovenance.Policy, raw []byte, e releaseprovenance.Expected, now time.Time) {
	t.Helper()
	d, err := releaseprovenance.Verify(p, raw, e, now)
	if err == nil || d != (releaseprovenance.Decision{}) {
		t.Fatal("unverified provenance admitted", d, err)
	}
	for _, secret := range []string{"DO-NOT-LEAK", "https://attacker.invalid", "<html>", "BEGIN CERTIFICATE"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("untrusted provenance escaped in diagnostic")
		}
	}
}

func TestProductionProvenanceCryptographicRefusals(t *testing.T) {
	p, e, now := inputs()
	for name, option := range map[string]fixture.Options{"no-sct": {OmitSCT: true}, "invalid-sct": {CorruptSCT: true}, "no-inclusion": {OmitProof: true}, "no-signed-time": {OmitPromise: true}, "future-signed-time": {IntegratedAt: now.Add(30 * time.Second)}} {
		t.Run(name, func(t *testing.T) {
			p, raw, err := fixture.NewWithOptions(p, e, now, option)
			if err != nil {
				t.Fatal(err)
			}
			requireRefusal(t, p, raw, e, now)
		})
	}
	p, raw, err := fixture.New(p, e, now)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("verified-time-after-clock", func(t *testing.T) { requireRefusal(t, p, raw, e, now.Add(-2*time.Second)) })
	for _, name := range []string{"signature", "inclusion-root", "integrated-time", "set", "payload", "certificate", "wrong-root", "duplicate-field", "field-alias", "oversize"} {
		t.Run(name, func(t *testing.T) {
			var b protobundle.Bundle
			if err := protojson.Unmarshal(raw, &b); err != nil {
				t.Fatal(err)
			}
			copyP := p
			switch name {
			case "signature":
				b.GetDsseEnvelope().Signatures[0].Sig[0] ^= 1
			case "inclusion-root":
				b.VerificationMaterial.TlogEntries[0].InclusionProof.RootHash[0] ^= 1
			case "integrated-time":
				b.VerificationMaterial.TlogEntries[0].IntegratedTime++
			case "set":
				b.VerificationMaterial.TlogEntries[0].InclusionPromise.SignedEntryTimestamp[0] ^= 1
			case "payload":
				b.GetDsseEnvelope().Payload[0] ^= 1
			case "certificate":
				b.VerificationMaterial.GetCertificate().RawBytes[0] ^= 1
			case "wrong-root":
				var err error
				copyP, _, err = fixture.New(p, e, now)
				if err != nil {
					t.Fatal(err)
				}
			}
			bad, err := protojson.Marshal(&b)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "duplicate-field":
				bad = bytes.Replace(bad, []byte(`"mediaType":`), []byte(`"mediaType":"DO-NOT-LEAK","mediaType":`), 1)
			case "field-alias":
				bad = bytes.Replace(bad, []byte(`"mediaType":`), []byte(`"media_type":`), 1)
			case "oversize":
				bad = bytes.Repeat([]byte("x"), releaseprovenance.MaxBundleBytes+1)
			}
			requireRefusal(t, copyP, bad, e, now)
		})
	}
}

func TestProductionProvenanceCertificateBindings(t *testing.T) {
	p, e, now := inputs()
	p, raw, err := fixture.New(p, e, now)
	if err != nil {
		t.Fatal(err)
	}
	var signed protobundle.Bundle
	if err = protojson.Unmarshal(raw, &signed); err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(signed.VerificationMaterial.GetCertificate().RawBytes)
	if err != nil {
		t.Fatal(err)
	}
	ext, err := certificate.ParseExtensions(cert.Extensions)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"san", "issuer", "runner", "repository", "owner", "source", "source-digest", "ref", "signer-uri", "config-uri", "workflow-digest", "invocation", "event", "missing-modern"} {
		t.Run(name, func(t *testing.T) {
			copy := ext
			option := fixture.Options{Statement: signed.GetDsseEnvelope().Payload}
			switch name {
			case "san":
				option.Identity = "https://attacker.invalid/DO-NOT-LEAK"
			case "issuer":
				copy.Issuer = "https://attacker.invalid/DO-NOT-LEAK"
			case "runner":
				copy.RunnerEnvironment = "self-hosted"
			case "repository":
				copy.SourceRepositoryIdentifier = "8"
			case "owner":
				copy.SourceRepositoryOwnerIdentifier = "4"
			case "source":
				copy.SourceRepositoryURI = "https://github.com/attacker/repo"
			case "source-digest":
				copy.SourceRepositoryDigest = strings.Repeat("c", 40)
			case "ref":
				copy.SourceRepositoryRef = "refs/heads/main"
			case "signer-uri":
				copy.BuildSignerURI = "https://github.com/attacker/shared/.github/workflows/release.yml@refs/tags/v0.1.0"
			case "config-uri":
				copy.BuildConfigURI = "https://github.com/fased-ai/fased-app/.github/workflows/other.yml@refs/tags/v0.1.0"
			case "workflow-digest":
				copy.BuildSignerDigest = strings.Repeat("c", 40)
			case "invocation":
				copy.RunInvocationURI = "https://github.com/fased-ai/fased-app/actions/runs/11/attempts/3"
			case "event":
				copy.BuildTrigger = "pull_request"
			case "missing-modern":
				copy.SourceRepositoryOwnerIdentifier = ""
			}
			option.Extensions = &copy
			policy, bad, err := fixture.NewWithOptions(p, e, now, option)
			if err != nil {
				t.Fatal(err)
			}
			requireRefusal(t, policy, bad, e, now)
		})
	}
}

func TestProductionProvenanceStatementBindings(t *testing.T) {
	p, e, now := inputs()
	p, raw, err := fixture.New(p, e, now)
	if err != nil {
		t.Fatal(err)
	}
	var signed protobundle.Bundle
	if err = protojson.Unmarshal(raw, &signed); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"subject", "digest", "extra-subject", "extra-digest", "predicate", "build-type", "repository", "ref", "workflow", "event", "repository-id", "owner-id", "runner", "builder", "invocation", "dependency", "commit", "extra-dependency", "duplicate", "alias", "unknown"} {
		t.Run(name, func(t *testing.T) {
			var s map[string]any
			if err := json.Unmarshal(signed.GetDsseEnvelope().Payload, &s); err != nil {
				t.Fatal(err)
			}
			predicate := s["predicate"].(map[string]any)
			build := predicate["buildDefinition"].(map[string]any)
			workflow := build["externalParameters"].(map[string]any)["workflow"].(map[string]any)
			github := build["internalParameters"].(map[string]any)["github"].(map[string]any)
			run := predicate["runDetails"].(map[string]any)
			dependency := build["resolvedDependencies"].([]any)[0].(map[string]any)
			switch name {
			case "subject":
				s["subject"].([]any)[0].(map[string]any)["name"] = "other.tar.gz"
			case "digest":
				s["subject"].([]any)[0].(map[string]any)["digest"].(map[string]any)["sha256"] = strings.Repeat("c", 64)
			case "extra-subject":
				s["subject"] = append(s["subject"].([]any), s["subject"].([]any)[0])
			case "extra-digest":
				s["subject"].([]any)[0].(map[string]any)["digest"].(map[string]any)["sha512"] = "extra"
			case "predicate":
				s["predicateType"] = "https://slsa.dev/provenance/v0.2"
			case "build-type":
				build["buildType"] = "https://attacker.invalid/DO-NOT-LEAK"
			case "repository":
				workflow["repository"] = "https://github.com/attacker/repo"
			case "ref":
				workflow["ref"] = "refs/heads/main"
			case "workflow":
				workflow["path"] = ".github/workflows/shared.yml"
			case "event":
				github["event_name"] = "pull_request"
			case "repository-id":
				github["repository_id"] = "8"
			case "owner-id":
				github["repository_owner_id"] = "4"
			case "runner":
				github["runner_environment"] = "self-hosted"
			case "builder":
				run["builder"].(map[string]any)["id"] = "https://github.com/actions/runner/self-hosted"
			case "invocation":
				run["metadata"].(map[string]any)["invocationId"] = "https://github.com/fased-ai/fased-app/actions/runs/12/attempts/2"
			case "dependency":
				dependency["uri"] = "git+https://github.com/fased-ai/fased-app@refs/heads/main"
			case "commit":
				dependency["digest"].(map[string]any)["gitCommit"] = strings.Repeat("c", 40)
			case "extra-dependency":
				build["resolvedDependencies"] = append(build["resolvedDependencies"].([]any), dependency)
			case "unknown":
				build["externalParameters"].(map[string]any)["arbitrary"] = "DO-NOT-LEAK"
			}
			payload, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if name == "duplicate" {
				payload = bytes.Replace(payload, []byte(`"_type":`), []byte(`"_type":"DO-NOT-LEAK","_type":`), 1)
			}
			if name == "alias" {
				payload = bytes.Replace(payload, []byte(`"predicateType":`), []byte(`"PredicateType":`), 1)
			}
			policy, bad, err := fixture.NewWithOptions(p, e, now, fixture.Options{Statement: payload})
			if err != nil {
				t.Fatal(err)
			}
			requireRefusal(t, policy, bad, e, now)
		})
	}
}

func TestProductionProvenanceSigned(t *testing.T) {
	p, e, now := inputs()
	p, raw, err := fixture.New(p, e, now)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := releaseprovenance.Verify(p, raw, e, now)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Identity != "https://github.com/fased-ai/fased-app/.github/workflows/release.yml@refs/tags/v0.1.0" || !decision.VerifiedAt.Equal(now.Add(-time.Second)) || decision.TimestampSource != "Tlog" || len(decision.BundleSHA256) != 64 || len(decision.PolicySHA256) != 64 {
		t.Fatal("unbound verified decision", decision)
	}
}

func TestProductionProvenanceStableTagAdmission(t *testing.T) {
	p, e, now := inputs()
	t.Run("signed-prerelease", func(t *testing.T) {
		candidate := e
		candidate.Tag = "v0.1.0-rc.1"
		policy, raw, err := fixture.New(p, candidate, now)
		if err != nil {
			t.Fatal(err)
		}
		if candidate.Validate() == nil {
			t.Fatal("prerelease accepted by stable production policy")
		}
		requireRefusal(t, policy, raw, candidate, now)
	})
	for _, tag := range []string{"v0.1.0+build.1", "v00.1.0"} {
		t.Run(tag, func(t *testing.T) {
			candidate := e
			candidate.Tag = tag
			if candidate.Validate() == nil {
				t.Fatal("noncanonical stable tag accepted")
			}
		})
	}
}

func TestProductionProvenanceObserverTimestamps(t *testing.T) {
	for _, name := range []string{"v1-set", "v1-tsa-no-set", "v1-set-and-tsa", "v2-tsa"} {
		t.Run(name, func(t *testing.T) {
			p, e, now := inputs()
			options := fixture.Options{}
			switch name {
			case "v1-tsa-no-set":
				options.Timestamp = true
				options.OmitPromise = true
			case "v1-set-and-tsa":
				options.Timestamp = true
			case "v2-tsa":
				options.RekorV2 = true
			}
			p, raw, err := fixture.NewWithOptions(p, e, now, options)
			if err != nil {
				t.Fatal(err)
			}
			clock := time.Now().UTC()
			decision, err := releaseprovenance.Verify(p, raw, e, clock)
			if err != nil {
				t.Fatal(err)
			}
			wantSource := "Tlog"
			if name == "v1-tsa-no-set" || name == "v2-tsa" {
				wantSource = "TimestampAuthority"
			}
			if decision.TimestampSource != wantSource || decision.VerifiedAt.IsZero() || decision.VerifiedAt.After(clock) {
				t.Fatal("wrong observer timestamp", decision)
			}
			var signed protobundle.Bundle
			if err = protojson.Unmarshal(raw, &signed); err != nil {
				t.Fatal(err)
			}
			if name == "v2-tsa" {
				entry := signed.VerificationMaterial.TlogEntries[0]
				if entry.KindVersion.Version != "0.0.2" || entry.InclusionPromise != nil || entry.IntegratedTime != 0 || signed.VerificationMaterial.TimestampVerificationData == nil {
					t.Fatal("not genuine V2 with TSA")
				}
			}
			if name == "v1-set-and-tsa" && !decision.VerifiedAt.Equal(now.Add(-time.Second)) {
				t.Fatal("deterministic earliest verified time changed")
			}
		})
	}
}

func TestProductionProvenanceObserverRefusals(t *testing.T) {
	for _, name := range []string{"v2-no-tsa", "v2-untrusted-time", "v2-invalid-tsa", "v2-untrusted-tsa", "v2-invalid-checkpoint", "v2-no-inclusion", "future-tsa", "future-extra-tsa"} {
		t.Run(name, func(t *testing.T) {
			p, e, now := inputs()
			options := fixture.Options{RekorV2: true}
			if name == "v2-no-tsa" || name == "v2-untrusted-time" {
				options.OmitTSA = true
			}
			if name == "v2-no-inclusion" {
				options.OmitProof = true
			}
			if name == "future-extra-tsa" {
				options = fixture.Options{Timestamp: true}
			}
			p, raw, err := fixture.NewWithOptions(p, e, now, options)
			if err != nil {
				t.Fatal(err)
			}
			var signed protobundle.Bundle
			if err = protojson.Unmarshal(raw, &signed); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "v2-untrusted-time":
				signed.VerificationMaterial.TlogEntries[0].IntegratedTime = now.Add(-time.Second).Unix()
			case "v2-invalid-tsa":
				signed.VerificationMaterial.TimestampVerificationData.Rfc3161Timestamps[0].SignedTimestamp[0] ^= 1
			case "v2-untrusted-tsa":
				var roots map[string]any
				if err = json.Unmarshal(p.TrustedRoot, &roots); err != nil {
					t.Fatal(err)
				}
				delete(roots, "timestampAuthorities")
				p.TrustedRoot, err = json.Marshal(roots)
				if err != nil {
					t.Fatal(err)
				}
			case "v2-invalid-checkpoint":
				signed.VerificationMaterial.TlogEntries[0].InclusionProof.Checkpoint.Envelope += "invalid checkpoint signature"
			}
			raw, err = protojson.Marshal(&signed)
			if err != nil {
				t.Fatal(err)
			}
			clock := time.Now().UTC()
			if name == "future-tsa" || name == "future-extra-tsa" {
				clock = now.Add(-time.Second)
			}
			requireRefusal(t, p, raw, e, clock)
		})
	}
}

func TestProductionProvenanceInputAdmission(t *testing.T) {
	p, e, now := inputs()
	p, raw, err := fixture.New(p, e, now)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*releaseprovenance.Policy){
		"version":       func(p *releaseprovenance.Policy) { p.Version = 0 },
		"repository":    func(p *releaseprovenance.Policy) { p.Repository = "attacker/fased-app" },
		"repository-id": func(p *releaseprovenance.Policy) { p.RepositoryID = "07" },
		"owner-id":      func(p *releaseprovenance.Policy) { p.OwnerID = "0" },
		"workflow":      func(p *releaseprovenance.Policy) { p.Workflow = ".github/workflows/*.yml" },
		"no-root":       func(p *releaseprovenance.Policy) { p.TrustedRoot = nil },
		"large-root": func(p *releaseprovenance.Policy) {
			p.TrustedRoot = bytes.Repeat([]byte("x"), releaseprovenance.MaxTrustedRootBytes+1)
		},
		"root-alias": func(p *releaseprovenance.Policy) {
			p.TrustedRoot = bytes.Replace(p.TrustedRoot, []byte(`"mediaType":`), []byte(`"media_type":`), 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			copy := p
			change(&copy)
			if copy.Validate() == nil {
				t.Fatal("unsafe policy valid")
			}
			requireRefusal(t, copy, raw, e, now)
		})
	}
	for name, change := range map[string]func(*releaseprovenance.Expected){
		"archive":         func(e *releaseprovenance.Expected) { e.ArchiveName = "fased-signer-linux-amd64.tar.gz" },
		"archive-hash":    func(e *releaseprovenance.Expected) { e.ArchiveSHA256 = "bad" },
		"source":          func(e *releaseprovenance.Expected) { e.SourceCommit = strings.Repeat("b", 64) },
		"reusable-commit": func(e *releaseprovenance.Expected) { e.WorkflowCommit = strings.Repeat("c", 40) },
		"tag":             func(e *releaseprovenance.Expected) { e.Tag = "latest" },
		"tag-traversal":   func(e *releaseprovenance.Expected) { e.Tag = "v0.1.0-.." },
		"mutable-ref":     func(e *releaseprovenance.Expected) { e.Tag = "refs/heads/main" },
		"attempt":         func(e *releaseprovenance.Expected) { e.RunAttempt = "02" },
		"run":             func(e *releaseprovenance.Expected) { e.RunID = "0" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := e
			change(&copy)
			if copy.Validate() == nil {
				t.Fatal("unsafe release expectation valid")
			}
			requireRefusal(t, p, raw, copy, now)
		})
	}
	requireRefusal(t, p, raw, e, time.Time{})
}
