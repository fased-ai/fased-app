// Package fixture creates disposable offline Sigstore evidence for tests only.
// No caller may persist its private keys or import it into an ordinary runtime.
package fixture

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/url"
	"time"

	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	ct "github.com/google/certificate-transparency-go"
	cttls "github.com/google/certificate-transparency-go/tls"
	ctx509 "github.com/google/certificate-transparency-go/x509"
	"github.com/google/certificate-transparency-go/x509util"
	"github.com/secure-systems-lab/go-securesystemslib/dsse"
	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	common "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	protodsse "github.com/sigstore/protobuf-specs/gen/pb-go/dsse"
	protorekor "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
	"google.golang.org/protobuf/encoding/protojson"
)

type Options struct {
	Identity                        string
	Extensions                      *certificate.Extensions
	Statement                       []byte
	IntegratedAt                    time.Time
	OmitSCT, OmitProof, OmitPromise bool
	CorruptSCT                      bool
	Timestamp, RekorV2, OmitTSA     bool
}

func New(p releaseprovenance.Policy, e releaseprovenance.Expected, now time.Time) (releaseprovenance.Policy, []byte, error) {
	return NewWithOptions(p, e, now, Options{})
}

// NewWithOptions signs deliberately wrong inputs for real cryptographic refusal
// cases. Its output has no production authority; all keys exist only in memory.
func NewWithOptions(p releaseprovenance.Policy, e releaseprovenance.Expected, now time.Time, options Options) (releaseprovenance.Policy, []byte, error) {
	caCert, caKey, err := ca.GenerateRootCa()
	if err != nil {
		return p, nil, err
	}
	intermediate, intermediateKey, err := ca.GenerateFulcioIntermediate(caCert, caKey)
	if err != nil {
		return p, nil, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return p, nil, err
	}
	ctKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return p, nil, err
	}
	repository := "https://github.com/" + p.Repository
	workflow := repository + "/" + p.Workflow + "@refs/tags/" + e.Tag
	identity := options.Identity
	if identity == "" {
		identity = workflow
	}
	identityURL, err := url.Parse(identity)
	if err != nil {
		return p, nil, err
	}
	ext := certificate.Extensions{Issuer: releaseprovenance.Issuer, BuildSignerURI: workflow, BuildSignerDigest: e.WorkflowCommit, RunnerEnvironment: "github-hosted", SourceRepositoryURI: repository, SourceRepositoryDigest: e.SourceCommit, SourceRepositoryRef: "refs/tags/" + e.Tag, SourceRepositoryIdentifier: p.RepositoryID, SourceRepositoryOwnerURI: "https://github.com/fased-ai", SourceRepositoryOwnerIdentifier: p.OwnerID, BuildConfigURI: workflow, BuildConfigDigest: e.WorkflowCommit, BuildTrigger: "push", RunInvocationURI: repository + "/actions/runs/" + e.RunID + "/attempts/" + e.RunAttempt, SourceRepositoryVisibilityAtSigning: "public"}
	if options.Extensions != nil {
		ext = *options.Extensions
	}
	var extensions []pkix.Extension
	for _, field := range []struct {
		oid   asn1.ObjectIdentifier
		value string
	}{
		{certificate.OIDIssuerV2, ext.Issuer}, {certificate.OIDBuildSignerURI, ext.BuildSignerURI}, {certificate.OIDBuildSignerDigest, ext.BuildSignerDigest}, {certificate.OIDRunnerEnvironment, ext.RunnerEnvironment}, {certificate.OIDSourceRepositoryURI, ext.SourceRepositoryURI}, {certificate.OIDSourceRepositoryDigest, ext.SourceRepositoryDigest}, {certificate.OIDSourceRepositoryRef, ext.SourceRepositoryRef}, {certificate.OIDSourceRepositoryIdentifier, ext.SourceRepositoryIdentifier}, {certificate.OIDSourceRepositoryOwnerURI, ext.SourceRepositoryOwnerURI}, {certificate.OIDSourceRepositoryOwnerIdentifier, ext.SourceRepositoryOwnerIdentifier}, {certificate.OIDBuildConfigURI, ext.BuildConfigURI}, {certificate.OIDBuildConfigDigest, ext.BuildConfigDigest}, {certificate.OIDBuildTrigger, ext.BuildTrigger}, {certificate.OIDRunInvocationURI, ext.RunInvocationURI}, {certificate.OIDSourceRepositoryVisibilityAtSigning, ext.SourceRepositoryVisibilityAtSigning},
	} {
		if field.value == "" {
			continue
		}
		value, err := asn1.MarshalWithParams(field.value, "utf8")
		if err != nil {
			return p, nil, err
		}
		extensions = append(extensions, pkix.Extension{Id: field.oid, Value: value})
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(10 * time.Minute), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}, URIs: []*url.URL{identityURL}, ExtraExtensions: extensions}
	precertRaw, err := x509.CreateCertificate(rand.Reader, template, intermediate, &leafKey.PublicKey, intermediateKey)
	if err != nil {
		return p, nil, err
	}
	precert, err := x509.ParseCertificate(precertRaw)
	if err != nil {
		return p, nil, err
	}
	ctPublic, err := x509.MarshalPKIXPublicKey(&ctKey.PublicKey)
	if err != nil {
		return p, nil, err
	}
	ctID := sha256.Sum256(ctPublic)
	if !options.OmitSCT {
		sct := ct.SignedCertificateTimestamp{SCTVersion: ct.V1, LogID: ct.LogID{KeyID: ctID}, Timestamp: uint64(now.Add(-time.Second).UnixMilli())}
		entry := ct.LogEntry{Leaf: ct.MerkleTreeLeaf{Version: ct.V1, LeafType: ct.TimestampedEntryLeafType, TimestampedEntry: &ct.TimestampedEntry{Timestamp: sct.Timestamp, EntryType: ct.PrecertLogEntryType, PrecertEntry: &ct.PreCert{IssuerKeyHash: sha256.Sum256(intermediate.RawSubjectPublicKeyInfo), TBSCertificate: precert.RawTBSCertificate}}}}
		input, err := ct.SerializeSCTSignatureInput(sct, entry)
		if err != nil {
			return p, nil, err
		}
		hash := sha256.Sum256(input)
		sig, err := ecdsa.SignASN1(rand.Reader, ctKey, hash[:])
		if err != nil {
			return p, nil, err
		}
		sct.Signature = ct.DigitallySigned{Algorithm: cttls.SignatureAndHashAlgorithm{Hash: cttls.SHA256, Signature: cttls.ECDSA}, Signature: sig}
		if options.CorruptSCT {
			sct.Signature.Signature[len(sct.Signature.Signature)-1] ^= 1
		}
		list, err := x509util.MarshalSCTsIntoSCTList([]*ct.SignedCertificateTimestamp{&sct})
		if err != nil {
			return p, nil, err
		}
		serialized, err := cttls.Marshal(*list)
		if err != nil {
			return p, nil, err
		}
		value, err := asn1.Marshal(serialized)
		if err != nil {
			return p, nil, err
		}
		template.ExtraExtensions = append(template.ExtraExtensions, pkix.Extension{Id: asn1.ObjectIdentifier(ctx509.OIDExtensionCTSCT), Value: value})
	}
	leafRaw, err := x509.CreateCertificate(rand.Reader, template, intermediate, &leafKey.PublicKey, intermediateKey)
	if err != nil {
		return p, nil, err
	}
	leaf, err := x509.ParseCertificate(leafRaw)
	if err != nil {
		return p, nil, err
	}
	statement := options.Statement
	if len(statement) == 0 {
		statement, err = json.Marshal(map[string]any{"_type": "https://in-toto.io/Statement/v1", "subject": []any{map[string]any{"name": e.ArchiveName, "digest": map[string]string{"sha256": e.ArchiveSHA256}}}, "predicateType": "https://slsa.dev/provenance/v1", "predicate": map[string]any{"buildDefinition": map[string]any{"buildType": releaseprovenance.BuildType, "externalParameters": map[string]any{"workflow": map[string]string{"ref": "refs/tags/" + e.Tag, "repository": repository, "path": p.Workflow}}, "internalParameters": map[string]any{"github": map[string]string{"event_name": "push", "repository_id": p.RepositoryID, "repository_owner_id": p.OwnerID}}, "resolvedDependencies": []any{map[string]any{"uri": "git+" + repository + "@refs/tags/" + e.Tag, "digest": map[string]string{"gitCommit": e.SourceCommit}}}}, "runDetails": map[string]any{"builder": map[string]string{"id": releaseprovenance.BuilderID}, "metadata": map[string]string{"invocationId": ext.RunInvocationURI}}}})
		if err != nil {
			return p, nil, err
		}
	}
	const payloadType = "application/vnd.in-toto+json"
	hash := sha256.Sum256(dsse.PAE(payloadType, statement))
	sig, err := ecdsa.SignASN1(rand.Reader, leafKey, hash[:])
	if err != nil {
		return p, nil, err
	}
	envelope := &dsse.Envelope{PayloadType: payloadType, Payload: base64.StdEncoding.EncodeToString(statement), Signatures: []dsse.Signature{{Sig: base64.StdEncoding.EncodeToString(sig)}}}
	virtual, err := ca.NewVirtualSigstore()
	if err != nil {
		return p, nil, err
	}
	integrated := options.IntegratedAt
	if integrated.IsZero() {
		integrated = now.Add(-time.Second)
	}
	logEntry, err := virtual.GenerateTlogEntry(leaf, envelope, sig, integrated.Unix(), !options.OmitProof)
	if err != nil {
		return p, nil, err
	}
	entryProto := logEntry.TransparencyLogEntry()
	entryProto.KindVersion = &protorekor.KindVersion{Kind: "dsse", Version: "0.0.1"}
	if !options.OmitPromise {
		set, err := virtual.RekorSignPayload(tlog.RekorPayload{Body: base64.StdEncoding.EncodeToString(entryProto.CanonicalizedBody), IntegratedTime: entryProto.IntegratedTime, LogIndex: entryProto.LogIndex, LogID: hex.EncodeToString(entryProto.LogId.KeyId)})
		if err != nil {
			return p, nil, err
		}
		entryProto.InclusionPromise = &protorekor.InclusionPromise{SignedEntryTimestamp: set}
	}
	rekorLogs := virtual.RekorLogs()
	for id, log := range rekorLogs {
		log.ID, err = hex.DecodeString(id)
		if err != nil {
			return p, nil, err
		}
	}
	if options.RekorV2 {
		entryProto, rekorLogs, err = rekorV2(leaf, statement, sig, now)
		if err != nil {
			return p, nil, err
		}
		if options.OmitProof {
			entryProto.InclusionProof = nil
		}
	}
	ctLogs := map[string]*root.TransparencyLog{hex.EncodeToString(ctID[:]): {BaseURL: "https://ct.fixture.invalid", ID: ctID[:], ValidityPeriodStart: now.Add(-time.Hour), ValidityPeriodEnd: now.Add(time.Hour), HashFunc: crypto.SHA256, SignatureHashFunc: crypto.SHA256, PublicKey: &ctKey.PublicKey}}
	var authorities []root.TimestampingAuthority
	var timestampData *protobundle.TimestampVerificationData
	if (options.Timestamp || options.RekorV2) && !options.OmitTSA {
		response, err := virtual.TimestampResponse(sig)
		if err != nil {
			return p, nil, err
		}
		authorities = virtual.TimestampingAuthorities()
		timestampData = &protobundle.TimestampVerificationData{Rfc3161Timestamps: []*common.RFC3161SignedTimestamp{{SignedTimestamp: response}}}
	}
	trusted, err := root.NewTrustedRoot(root.TrustedRootMediaType01, []root.CertificateAuthority{&root.FulcioCertificateAuthority{Root: caCert, Intermediates: []*x509.Certificate{intermediate}, ValidityPeriodStart: now.Add(-time.Minute), ValidityPeriodEnd: now.Add(time.Hour), URI: "https://fulcio.fixture.invalid"}}, ctLogs, authorities, rekorLogs)
	if err != nil {
		return p, nil, err
	}
	p.TrustedRoot, err = trusted.MarshalJSON()
	if err != nil {
		return p, nil, err
	}
	b := &protobundle.Bundle{MediaType: "application/vnd.dev.sigstore.bundle.v0.3+json", VerificationMaterial: &protobundle.VerificationMaterial{Content: &protobundle.VerificationMaterial_Certificate{Certificate: &common.X509Certificate{RawBytes: leaf.Raw}}, TlogEntries: []*protorekor.TransparencyLogEntry{entryProto}}, Content: &protobundle.Bundle_DsseEnvelope{DsseEnvelope: &protodsse.Envelope{PayloadType: payloadType, Payload: statement, Signatures: []*protodsse.Signature{{Sig: sig}}}}}
	b.VerificationMaterial.TimestampVerificationData = timestampData
	raw, err := protojson.Marshal(b)
	return p, raw, err
}
