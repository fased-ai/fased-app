package releaseprovenance

import (
	"crypto/sha256"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	sigverify "github.com/sigstore/sigstore-go/pkg/verify"
)

var versionTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func canonicalNumber(s string) bool {
	n, err := strconv.ParseUint(s, 10, 64)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == s
}
func canonicalHash(s string, bytes int) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == bytes && hex.EncodeToString(b) == s
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Protobuf JSON accepts snake_case aliases. This fixed format accepts only its
// canonical JSON names, while StrictJSON rejects duplicates and ambiguous bytes.
func strictWire(raw []byte) error {
	var value map[string]any
	if update.StrictJSON(raw, &value) != nil || value == nil {
		return errors.New("invalid provenance JSON")
	}
	var walk func(any) bool
	walk = func(value any) bool {
		switch x := value.(type) {
		case map[string]any:
			for name, child := range x {
				if strings.Contains(name, "_") || !walk(child) {
					return false
				}
			}
		case []any:
			for _, child := range x {
				if !walk(child) {
					return false
				}
			}
		}
		return true
	}
	if !walk(value) {
		return errors.New("noncanonical provenance JSON names")
	}
	return nil
}

func (p Policy) Validate() error {
	if p.Version != PolicyVersion || p.Repository != "fased-ai/fased-app" || !canonicalNumber(p.RepositoryID) || !canonicalNumber(p.OwnerID) || p.Workflow != WorkflowPath || len(p.TrustedRoot) == 0 || len(p.TrustedRoot) > MaxTrustedRootBytes || strictWire(p.TrustedRoot) != nil {
		return errors.New("explicit pinned production provenance policy required")
	}
	trusted, err := root.NewTrustedRootFromJSON(p.TrustedRoot)
	if err != nil || len(trusted.FulcioCertificateAuthorities()) == 0 || len(trusted.CTLogs()) == 0 || len(trusted.RekorLogs()) == 0 {
		return errors.New("offline certificate, CT and transparency roots required")
	}
	return nil
}
func (e Expected) Validate() error {
	if e.ArchiveName != "fased-app-linux-amd64.tar.gz" || !canonicalHash(e.ArchiveSHA256, 32) || !canonicalHash(e.SourceCommit, 20) || e.WorkflowCommit != e.SourceCommit || len(e.Tag) > 80 || !versionTag.MatchString(e.Tag) || !canonicalNumber(e.RunID) || !canonicalNumber(e.RunAttempt) {
		return errors.New("exact Ubuntu artifact, local workflow and immutable release identities required")
	}
	return nil
}

func expectedExtensions(p Policy, e Expected) certificate.Extensions {
	repository := "https://github.com/" + p.Repository
	workflow := repository + "/" + p.Workflow + "@refs/tags/" + e.Tag
	return certificate.Extensions{Issuer: Issuer, BuildSignerURI: workflow, BuildSignerDigest: e.WorkflowCommit, RunnerEnvironment: "github-hosted", SourceRepositoryURI: repository, SourceRepositoryDigest: e.SourceCommit, SourceRepositoryRef: "refs/tags/" + e.Tag, SourceRepositoryIdentifier: p.RepositoryID, SourceRepositoryOwnerURI: "https://github.com/fased-ai", SourceRepositoryOwnerIdentifier: p.OwnerID, BuildConfigURI: workflow, BuildConfigDigest: e.WorkflowCommit, BuildTrigger: "push", RunInvocationURI: repository + "/actions/runs/" + e.RunID + "/attempts/" + e.RunAttempt, SourceRepositoryVisibilityAtSigning: "public"}
}

type statement struct {
	Type    string `json:"_type"`
	Subject []struct {
		Name   string            `json:"name"`
		Digest map[string]string `json:"digest"`
	} `json:"subject"`
	PredicateType string `json:"predicateType"`
	Predicate     struct {
		BuildDefinition struct {
			BuildType          string `json:"buildType"`
			ExternalParameters struct {
				Workflow struct {
					Ref        string `json:"ref"`
					Repository string `json:"repository"`
					Path       string `json:"path"`
				} `json:"workflow"`
			} `json:"externalParameters"`
			InternalParameters struct {
				GitHub struct {
					Event      string `json:"event_name"`
					Repository string `json:"repository_id"`
					Owner      string `json:"repository_owner_id"`
					Runner     string `json:"runner_environment,omitempty"`
				} `json:"github"`
			} `json:"internalParameters"`
			ResolvedDependencies []struct {
				URI    string            `json:"uri"`
				Digest map[string]string `json:"digest"`
			} `json:"resolvedDependencies"`
		} `json:"buildDefinition"`
		RunDetails struct {
			Builder struct {
				ID      string            `json:"id"`
				Version map[string]string `json:"version,omitempty"`
			} `json:"builder"`
			Metadata struct {
				InvocationID string `json:"invocationId"`
				StartedOn    string `json:"startedOn,omitempty"`
				FinishedOn   string `json:"finishedOn,omitempty"`
			} `json:"metadata"`
		} `json:"runDetails"`
	} `json:"predicate"`
}

func validateStatement(raw []byte, p Policy, e Expected) error {
	var s statement
	if update.StrictJSON(raw, &s) != nil || s.Type != "https://in-toto.io/Statement/v1" || s.PredicateType != "https://slsa.dev/provenance/v1" || len(s.Subject) != 1 || s.Subject[0].Name != e.ArchiveName || len(s.Subject[0].Digest) != 1 || s.Subject[0].Digest["sha256"] != e.ArchiveSHA256 {
		return errors.New("exact in-toto artifact subject required")
	}
	b := s.Predicate.BuildDefinition
	w := b.ExternalParameters.Workflow
	g := b.InternalParameters.GitHub
	r := s.Predicate.RunDetails
	if b.BuildType != BuildType || w.Ref != "refs/tags/"+e.Tag || w.Repository != "https://github.com/"+p.Repository || w.Path != p.Workflow || g.Event != "push" || g.Repository != p.RepositoryID || g.Owner != p.OwnerID || g.Runner != "" && g.Runner != "github-hosted" || r.Builder.ID != BuilderID || len(r.Builder.Version) != 0 || r.Metadata.InvocationID != expectedExtensions(p, e).RunInvocationURI || len(b.ResolvedDependencies) != 1 {
		return errors.New("exact local GitHub release workflow provenance required")
	}
	dependency := b.ResolvedDependencies[0]
	if dependency.URI != "git+https://github.com/"+p.Repository+"@refs/tags/"+e.Tag || len(dependency.Digest) != 1 || dependency.Digest["gitCommit"] != e.SourceCommit {
		return errors.New("exact release source dependency required")
	}
	return nil
}

func Verify(p Policy, raw []byte, e Expected, now time.Time) (Decision, error) {
	return verifySigned(p, raw, e, now)
}

// Kept as a separate implementation so no SDK error (which may quote signed
// untrusted fields) can escape the public admission boundary.
func verifySigned(p Policy, raw []byte, e Expected, now time.Time) (Decision, error) {
	if p.Validate() != nil || e.Validate() != nil || now.IsZero() || len(raw) == 0 || len(raw) > MaxBundleBytes || strictWire(raw) != nil {
		return Decision{}, errors.New("invalid production provenance input")
	}
	var signed bundle.Bundle
	if signed.UnmarshalJSON(raw) != nil || signed.MediaType != "application/vnd.dev.sigstore.bundle.v0.3+json" || signed.GetDsseEnvelope() == nil || len(signed.GetDsseEnvelope().Signatures) != 1 || signed.GetDsseEnvelope().PayloadType != "application/vnd.in-toto+json" || !signed.HasInclusionProof() {
		return Decision{}, errors.New("complete certificate-backed DSSE bundle v0.3 required")
	}
	content, err := signed.VerificationContent()
	if err != nil || content.Certificate() == nil {
		return Decision{}, errors.New("certificate provenance required")
	}
	cert := content.Certificate()
	if len(cert.URIs) != 1 || len(cert.DNSNames) != 0 || len(cert.EmailAddresses) != 0 || len(cert.IPAddresses) != 0 {
		return Decision{}, errors.New("one exact workflow URI identity required")
	}
	modern := map[string]bool{}
	for _, extension := range cert.Extensions {
		modern[extension.Id.String()] = true
	}
	for _, oid := range []asn1.ObjectIdentifier{certificate.OIDIssuerV2, certificate.OIDBuildSignerURI, certificate.OIDBuildSignerDigest, certificate.OIDRunnerEnvironment, certificate.OIDSourceRepositoryURI, certificate.OIDSourceRepositoryDigest, certificate.OIDSourceRepositoryRef, certificate.OIDSourceRepositoryIdentifier, certificate.OIDSourceRepositoryOwnerURI, certificate.OIDSourceRepositoryOwnerIdentifier, certificate.OIDBuildConfigURI, certificate.OIDBuildConfigDigest, certificate.OIDBuildTrigger, certificate.OIDRunInvocationURI, certificate.OIDSourceRepositoryVisibilityAtSigning} {
		if !modern[oid.String()] {
			return Decision{}, errors.New("modern verified workflow certificate fields required")
		}
	}
	entries, err := signed.TlogEntries()
	if err != nil || len(entries) != 1 {
		return Decision{}, errors.New("one verified transparency entry required")
	}
	if validateStatement(signed.GetDsseEnvelope().Payload, p, e) != nil {
		return Decision{}, errors.New("release statement identity mismatch")
	}
	trusted, err := root.NewTrustedRootFromJSON(p.TrustedRoot)
	if err != nil {
		return Decision{}, errors.New("invalid offline provenance roots")
	}
	v, err := sigverify.NewVerifier(trusted, sigverify.WithTransparencyLog(1), sigverify.WithObserverTimestamps(1), sigverify.WithSignedCertificateTimestamps(1))
	if err != nil {
		return Decision{}, errors.New("provenance verifier unavailable")
	}
	ext := expectedExtensions(p, e)
	san, _ := sigverify.NewSANMatcher(ext.BuildSignerURI, "")
	issuer, _ := sigverify.NewIssuerMatcher(Issuer, "")
	ext.Issuer = "" // IssuerMatcher owns this exact constraint in the SDK.
	identity, err := sigverify.NewCertificateIdentity(san, issuer, ext)
	if err != nil {
		return Decision{}, errors.New("invalid exact provenance identity")
	}
	hash, _ := hex.DecodeString(e.ArchiveSHA256)
	result, err := v.Verify(&signed, sigverify.NewPolicy(sigverify.WithArtifactDigest("sha256", hash), sigverify.WithCertificateIdentity(identity)))
	if err != nil || result == nil || result.Signature == nil || result.Signature.Certificate == nil || result.VerifiedIdentity == nil {
		return Decision{}, errors.New("provenance signature, identity or transparency verification failed")
	}
	var verified time.Time
	source, uri := "", ""
	for _, stamp := range result.VerifiedTimestamps {
		if (stamp.Type != "Tlog" && stamp.Type != "TimestampAuthority") || stamp.Timestamp.IsZero() || stamp.Timestamp.After(now) {
			return Decision{}, errors.New("invalid verified provenance timestamp")
		}
		if verified.IsZero() || stamp.Timestamp.Before(verified) || stamp.Timestamp.Equal(verified) && (stamp.Type < source || stamp.Type == source && stamp.URI < uri) {
			verified, source, uri = stamp.Timestamp, stamp.Type, stamp.URI
		}
	}
	if verified.IsZero() {
		return Decision{}, errors.New("verified observer timestamp required")
	}
	policyBytes, _ := json.Marshal(p)
	return Decision{BundleSHA256: digest(raw), PolicySHA256: digest(policyBytes), Identity: ext.BuildSignerURI, VerifiedAt: verified, TimestampSource: source}, nil
}
