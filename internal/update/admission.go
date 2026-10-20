package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const AdmissionProfile = "fased-app-release-admission-fixture:v1"
const FixtureProvenancePurpose = "fased-app-build-provenance-fixture:v1"
const inertArtifactPrefix = "FASED-INERT-ARTIFACT:v1\n"
const maxInertArtifact = 64 << 10

// FixtureBuildIdentity is synthetic policy input, not an authenticated GitHub,
// OIDC or Sigstore identity. A builder signature cannot prove source honesty.
type FixtureBuildIdentity struct {
	SourceRepository string `json:"sourceRepository"`
	SourceCommit     string `json:"sourceCommit"`
	SourceTree       string `json:"sourceTree"`
	Workflow         string `json:"workflow"`
	WorkflowRevision string `json:"workflowRevision"`
	Run              uint64 `json:"run,string"`
	Attempt          int    `json:"attempt"`
	Builder          string `json:"builder"`
	ToolchainSHA256  string `json:"toolchainSha256"`
	LocksSHA256      string `json:"locksSha256"`
}

type FixtureAdmissionPolicy struct {
	FixtureBinding
	Version    int                  `json:"version"`
	Profile    string               `json:"profile"`
	Target     Target               `json:"target"`
	Identity   FixtureBuildIdentity `json:"identity"`
	BuilderKey []byte               `json:"builderKey"`
	NotBefore  int64                `json:"notBefore"`
	NotAfter   int64                `json:"notAfter"`
}

type FixtureReleaseSubject struct {
	Role         string `json:"role"`
	Target       Target `json:"target"`
	SourceCommit string `json:"sourceCommit"`
	Artifact
}

type FixtureReleaseCompatibility struct {
	CoreProtocol    int `json:"coreProtocol"`
	SignerProtocol  int `json:"signerProtocol"`
	OwnerEntrypoint int `json:"ownerEntrypoint"`
	InstallerFormat int `json:"installerFormat"`
}

type FixtureReleaseUnit struct {
	FixtureBinding
	Version       int                         `json:"version"`
	Profile       string                      `json:"profile"`
	Sequence      uint64                      `json:"sequence,string"`
	Target        Target                      `json:"target"`
	Identity      FixtureBuildIdentity        `json:"identity"`
	Compatibility FixtureReleaseCompatibility `json:"compatibility"`
	Subjects      []FixtureReleaseSubject     `json:"subjects"`
}

type FixtureBuildProvenance struct {
	FixtureBinding
	Version    int                     `json:"version"`
	Profile    string                  `json:"profile"`
	UnitSHA256 string                  `json:"unitSha256"`
	Identity   FixtureBuildIdentity    `json:"identity"`
	BuiltAt    int64                   `json:"builtAt"`
	Subjects   []FixtureReleaseSubject `json:"subjects"`
}

type FixtureReleaseEvidence struct {
	FixtureBinding
	Version    int    `json:"version"`
	Profile    string `json:"profile"`
	Unit       []byte `json:"unit"`
	Provenance []byte `json:"provenance"`
}

type ReleaseAdmissionOptions struct {
	Policy       []byte
	PolicySHA256 string
	Evidence     []byte
	ArtifactDir  string
}

type ReleaseAdmissionReport struct {
	TrustRehearsalReport
	ReleaseUnitVerified bool   `json:"releaseUnitVerified"`
	ProvenanceVerified  bool   `json:"provenanceVerified"`
	ArtifactsVerified   bool   `json:"artifactsVerified"`
	ArtifactCount       int    `json:"artifactCount"`
	PolicySHA256        string `json:"policySha256"`
	EvidenceSHA256      string `json:"evidenceSha256"`
	UnitSHA256          string `json:"unitSha256"`
}

func fixtureCommitOK(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 20 && hex.EncodeToString(b) == s
}

func fixtureIdentityOK(i FixtureBuildIdentity) bool {
	return i.SourceRepository == "fixture/source" && i.Workflow == "fixture/build.yml" && i.Builder == "fixture/builder" && fixtureCommitOK(i.SourceCommit) && fixtureCommitOK(i.SourceTree) && fixtureCommitOK(i.WorkflowRevision) && i.Run > 0 && i.Attempt > 0 && i.Attempt <= 1000 && hashOK(i.ToolchainSHA256) && hashOK(i.LocksSHA256)
}

func parseAdmissionPolicy(o ReleaseAdmissionOptions, now int64) (FixtureAdmissionPolicy, error) {
	var p FixtureAdmissionPolicy
	if !hashOK(o.PolicySHA256) || digest(o.Policy) != o.PolicySHA256 || StrictJSON(o.Policy, &p) != nil || p.Version != 1 || p.Profile != AdmissionProfile || !p.FixtureBinding.valid() || !targetOK(p.Target) || !fixtureIdentityOK(p.Identity) || len(p.BuilderKey) != ed25519.PublicKeySize || p.NotBefore <= 0 || p.NotAfter <= p.NotBefore || p.NotAfter-p.NotBefore > 7*86400 || now < p.NotBefore || now >= p.NotAfter {
		return p, errors.New("independently pinned, current disposable admission policy required")
	}
	if len(o.Evidence) == 0 || len(o.Evidence) > MaxDocument || !filepath.IsAbs(o.ArtifactDir) {
		return p, errors.New("bounded evidence and absolute inert artifact directory required")
	}
	return p, nil
}

func verifyReleaseEvidence(o ReleaseAdmissionOptions, p FixtureAdmissionPolicy, s TrustFixtureSnapshot, now int64) (FixtureReleaseUnit, error) {
	var u FixtureReleaseUnit
	var d TrustFixtureDescriptor
	var ev FixtureReleaseEvidence
	if StrictJSON(s.Descriptor, &d) != nil || digest(o.Evidence) != d.EvidenceSHA256 || StrictJSON(o.Evidence, &ev) != nil || ev.Version != 1 || ev.Profile != AdmissionProfile || ev.FixtureBinding != p.FixtureBinding || p.FixtureBinding != s.FixtureBinding {
		return u, errors.New("release evidence does not match fixture TUF descriptor/policy")
	}
	if StrictJSON(ev.Unit, &u) != nil || u.Version != 1 || u.Profile != AdmissionProfile || u.FixtureBinding != p.FixtureBinding || u.Sequence != d.Sequence || u.Target != p.Target || u.Identity != p.Identity || u.Compatibility != (FixtureReleaseCompatibility{1, 1, 4, 1}) || len(u.Subjects) != 4 {
		return u, errors.New("invalid complete fixture release unit or build binding")
	}
	roles := []string{"core", "signer", "owner-helper", "installer"}
	seen := map[string]bool{}
	for i, a := range u.Subjects {
		if a.Role != roles[i] || a.File != roles[i]+".fixture" || a.Target != u.Target || a.SourceCommit != u.Identity.SourceCommit || a.Size < int64(len(inertArtifactPrefix)) || a.Size > maxInertArtifact || !hashOK(a.SHA256) || seen[a.SHA256] {
			return u, errors.New("missing, mixed, repeated or invalid fixture subject")
		}
		seen[a.SHA256] = true
	}
	// The independently pinned builder key must not double as a TUF role key.
	r, e := fixtureRoot(s.Root, s.FixtureBinding)
	if e != nil {
		return u, e
	}
	for _, k := range r.Signed.Keys {
		if k.Value.PublicKey == hex.EncodeToString(p.BuilderKey) {
			return u, errors.New("fixture builder key must be separate from metadata keys")
		}
	}
	var envelope Envelope
	if StrictJSON(ev.Provenance, &envelope) != nil || envelope.Version != 1 || envelope.KeyID != KeyID(p.BuilderKey) || len(envelope.Signature) != ed25519.SignatureSize || !ed25519.Verify(p.BuilderKey, append([]byte(FixtureProvenancePurpose+"\x00"), envelope.Payload...), envelope.Signature) {
		return u, errors.New("fixture builder provenance signature refused")
	}
	var b FixtureBuildProvenance
	if StrictJSON(envelope.Payload, &b) != nil || b.Version != 1 || b.Profile != AdmissionProfile || b.FixtureBinding != p.FixtureBinding || b.UnitSHA256 != digest(ev.Unit) || b.Identity != p.Identity || b.BuiltAt < p.NotBefore || b.BuiltAt > now || b.BuiltAt >= p.NotAfter || len(b.Subjects) != len(u.Subjects) {
		return u, errors.New("fixture provenance unit/source/workflow/builder/time mismatch")
	}
	for i := range u.Subjects {
		if b.Subjects[i] != u.Subjects[i] {
			return u, errors.New("fixture provenance subjects differ from release unit")
		}
	}
	return u, nil
}

// inspectInertArtifacts uses contained opens and inspects only exact bytes. Its
// result is never a pathname authorization for subsequent installation/execution.
func inspectInertArtifacts(dir string, subjects []FixtureReleaseSubject) error {
	clean := filepath.Clean(dir)
	real, e := filepath.EvalSymlinks(clean)
	if e != nil || real != clean || !filepath.IsAbs(dir) {
		return errors.New("real absolute nonsymlink fixture directory required")
	}
	r, e := os.OpenRoot(clean)
	if e != nil {
		return e
	}
	defer r.Close()
	df, e := r.Open(".")
	if e != nil {
		return e
	}
	entries, e := df.ReadDir(5)
	ce := df.Close()
	if e != nil && e != io.EOF || ce != nil || len(entries) != 4 {
		return errors.New("exact four-file inert fixture directory required")
	}
	names := map[string]bool{}
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	for _, a := range subjects {
		if !names[a.File] {
			return errors.New("missing fixture artifact")
		}
		before, e := r.Lstat(a.File)
		if e != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0111 != 0 || before.Size() != a.Size {
			return errors.New("complete regular nonexecutable fixture artifact required")
		}
		f, e := openInertArtifact(r, a.File)
		if e != nil {
			return e
		}
		after, se := f.Stat()
		if se != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Mode().Perm()&0111 != 0 || after.Size() != a.Size {
			_ = f.Close()
			return errors.New("fixture artifact changed during open")
		}
		b, re := io.ReadAll(io.LimitReader(f, a.Size+1))
		ce := f.Close()
		if re != nil || ce != nil || int64(len(b)) != a.Size || !bytes.HasPrefix(b, []byte(inertArtifactPrefix)) || digest(b) != a.SHA256 {
			return errors.New("fixture artifact prefix/complete size/hash mismatch")
		}
	}
	return nil
}

// AdmitFixtureRelease adds no installed authority. Failed artifact/provenance
// admission preserves observed trust progress through the existing opaque CAS
// transition; observed descriptor floors are not installed/reserved release floors.
func AdmitFixtureRelease(ctx context.Context, trust TrustRehearsalOptions, o ReleaseAdmissionOptions) (transition TrustTransition, report ReleaseAdmissionReport, err error) {
	if !trust.Development {
		return transition, report, errors.New("explicit disposable development mode required")
	}
	p, err := parseAdmissionPolicy(o, trust.Now)
	if err != nil {
		return transition, report, err
	}
	transition, err = RehearseTrust(ctx, trust)
	report.TrustRehearsalReport = transition.Report()
	report.PolicySHA256, report.EvidenceSHA256 = o.PolicySHA256, digest(o.Evidence)
	if err != nil {
		return transition, report, err
	}
	var s TrustFixtureSnapshot
	if err = StrictJSON(transition.next, &s); err != nil {
		return transition, report, err
	}
	defer func() {
		if err != nil {
			s.Complete = false
			var pe error
			transition, pe = trustProposal(s, trust.Previous, trust.SeedSHA256, report.OnlineCacheReset)
			err = errors.Join(err, pe)
			report.TrustRehearsalReport = transition.Report()
		}
	}()
	u, err := verifyReleaseEvidence(o, p, s, trust.Now)
	if err != nil {
		return transition, report, err
	}
	if err = inspectInertArtifacts(o.ArtifactDir, u.Subjects); err != nil {
		return transition, report, err
	}
	var ev FixtureReleaseEvidence
	_ = StrictJSON(o.Evidence, &ev)
	report.Result = "verified-disposable-release-admission"
	report.UnitSHA256 = digest(ev.Unit)
	report.ReleaseUnitVerified, report.ProvenanceVerified, report.ArtifactsVerified, report.ArtifactCount = true, true, true, len(u.Subjects)
	return transition, report, nil
}
