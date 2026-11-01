package bootstrap

// This is offline publisher tooling, not an app permission or release publisher.
import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/secure-systems-lab/go-securesystemslib/cjson"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

type ReleaseVersions struct {
	Targets   int64 `json:"targets"`
	Snapshot  int64 `json:"snapshot"`
	Timestamp int64 `json:"timestamp"`
}
type ReleaseExpiries struct {
	Targets   time.Time `json:"targets"`
	Snapshot  time.Time `json:"snapshot"`
	Timestamp time.Time `json:"timestamp"`
}

// Previous values are explicitly reviewed publisher floors, never inferred from
// a network response. They do not replace installed client/controller floors.
type ReleasePlan struct {
	Version          int             `json:"version"`
	Descriptor       Descriptor      `json:"descriptor"`
	PreviousSequence uint64          `json:"previousSequence,string"`
	Versions         ReleaseVersions `json:"versions"`
	PreviousVersions ReleaseVersions `json:"previousVersions"`
	Expires          ReleaseExpiries `json:"expires"`
}
type ReleaseFinalizerInput struct {
	Seed          []byte
	SeedSHA256    string
	ArchiveFile   string
	Provenance    []byte
	WorkDirectory string
	Plan          ReleasePlan
}
type ReleaseApprovals struct {
	Targets   []metadata.Signature `json:"targets"`
	Snapshot  []metadata.Signature `json:"snapshot"`
	Timestamp []metadata.Signature `json:"timestamp"`
}

// Payload is the exact TUF canonical signed body. No private key is accepted.
type ReleaseSigningRequest struct {
	Version       int      `json:"version"`
	Role          string   `json:"role"`
	SeedSHA256    string   `json:"seedSha256"`
	PlanSHA256    string   `json:"planSha256"`
	Payload       []byte   `json:"payload"`
	PayloadSHA256 string   `json:"payloadSha256"`
	KeyIDs        []string `json:"keyIds"`
	Threshold     int      `json:"threshold"`
}
type ReleaseFinalizationReport struct {
	Version          int    `json:"version"`
	SeedSHA256       string `json:"seedSha256"`
	PlanSHA256       string `json:"planSha256"`
	DescriptorSHA256 string `json:"descriptorSha256"`
	ArchiveSHA256    string `json:"archiveSha256"`
	MetadataVerified bool   `json:"metadataVerified"`
	Published        bool   `json:"published"`
	Activation       bool   `json:"activation"`
}

// ReleasePublication contains only already verified public metadata and the
// immutable release identity. A separate approved digest binds publisher input.
type ReleasePublication struct {
	Version        int               `json:"version"`
	SigningProfile string            `json:"signingProfile"`
	Repository     string            `json:"repository"`
	RepositoryID   string            `json:"repositoryId"`
	OwnerID        string            `json:"ownerId"`
	MetadataBranch string            `json:"metadataBranch"`
	SeedSHA256     string            `json:"seedSha256"`
	PlanSHA256     string            `json:"planSha256"`
	Descriptor     Descriptor        `json:"descriptor"`
	Files          map[string][]byte `json:"files"`
}

type FinalizedRelease struct {
	Files       map[string][]byte
	Publication ReleasePublication
	Report      ReleaseFinalizationReport
}
type ReleaseFinalizer struct {
	seed                         Seed
	root                         *metadata.Metadata[metadata.RootType]
	seedBytes, proof, descriptor []byte
	pin, archive, planHash       string
	plan                         ReleasePlan
}

func NewReleaseFinalizer(in ReleaseFinalizerInput) (*ReleaseFinalizer, error) {
	if os.Geteuid() == 0 || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return nil, errors.New("ordinary Ubuntu amd64 publisher required")
	}
	if len(in.Seed) == 0 || len(in.Seed) > update.MaxDocument || len(in.Provenance) == 0 || len(in.Provenance) > releaseprovenance.MaxBundleBytes {
		return nil, errors.New("bounded public seed and original provenance required")
	}
	seed, err := ValidateProductionSeed(in.Seed, in.SeedSHA256)
	if err != nil {
		return nil, err
	}
	root, err := metadata.Root().FromBytes(seed.Root)
	if err != nil {
		return nil, err
	}
	db, err := json.Marshal(in.Plan.Descriptor)
	if err != nil {
		return nil, err
	}
	binding, err := json.Marshal(struct {
		Seed string      `json:"seedSha256"`
		Plan ReleasePlan `json:"plan"`
	}{in.SeedSHA256, in.Plan})
	if err != nil {
		return nil, err
	}
	f := &ReleaseFinalizer{seed: seed, root: root, seedBytes: append([]byte(nil), in.Seed...), proof: append([]byte(nil), in.Provenance...), descriptor: db, pin: in.SeedSHA256, archive: in.ArchiveFile, plan: in.Plan, planHash: digest(binding)}
	if err = f.fresh(); err != nil {
		return nil, err
	}
	// Reuse the exact archive validator used by installation. Extract only into
	// an exclusive owned scratch directory and never execute its contents.
	if _, err = privateDirectory(in.WorkDirectory); err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp(in.WorkDirectory, ".release-finalizer-")
	if err != nil {
		return nil, err
	}
	owned, err := os.Lstat(scratch)
	if err != nil {
		return nil, err
	}
	defer func() {
		if current, e := os.Lstat(scratch); e == nil && os.SameFile(owned, current) {
			_ = os.RemoveAll(scratch)
		}
	}()
	d := f.plan.Descriptor
	m, err := bundle.ExtractVerifiedArchive(f.archive, filepath.Join(scratch, "bundle"), bundle.File{Bytes: d.Archive.Size, SHA256: d.Archive.SHA256}, d.BundleSHA256)
	if err != nil {
		return nil, err
	}
	if err = verifyBundleProfile(seed, m); err != nil {
		return nil, err
	}
	if m.Version != d.ProductVersion || m.Source != d.Source {
		return nil, errors.New("candidate bundle source/version differs from release plan")
	}
	return f, nil
}
func (f *ReleaseFinalizer) fresh() error {
	now := time.Now().UTC()
	if _, err := parseSeed(f.seedBytes, f.pin, now); err != nil {
		return err
	}
	p, d := f.plan, f.plan.Descriptor
	if p.Version != 1 || d.Binding != f.seed.Binding || d.Sequence < f.seed.MinimumSequence || d.Sequence <= p.PreviousSequence || !hashOK(d.BundleSHA256) || d.Archive.File != archiveTarget || d.Archive.Size <= 0 || d.Archive.Size > maxArchiveBytes || d.GitHub.Validate(f.seed.GitHub) != nil || d.GitHub.Name != d.Archive.File || d.GitHub.Size != d.Archive.Size || d.GitHub.SHA256 != d.Archive.SHA256 {
		return errors.New("exact release plan, advancing sequence and pinned asset required")
	}
	for _, v := range [][2]int64{{p.Versions.Targets, p.PreviousVersions.Targets}, {p.Versions.Snapshot, p.PreviousVersions.Snapshot}, {p.Versions.Timestamp, p.PreviousVersions.Timestamp}} {
		if v[1] < 0 || v[1] >= 1<<31-1 || v[0] != v[1]+1 {
			return errors.New("metadata versions must advance their reviewed floors exactly once")
		}
	}
	for _, v := range []struct {
		expiry time.Time
		limit  time.Duration
	}{{p.Expires.Targets, 30 * 24 * time.Hour}, {p.Expires.Snapshot, 7 * 24 * time.Hour}, {p.Expires.Timestamp, 48 * time.Hour}} {
		if !v.expiry.After(now) || v.expiry.After(now.Add(v.limit)) || v.expiry.Unix() > f.seed.ExpiresAt || v.expiry.Nanosecond() != 0 {
			return errors.New("current bounded whole-second metadata expiry required")
		}
	}
	if p.Expires.Timestamp.After(p.Expires.Snapshot) || p.Expires.Snapshot.After(p.Expires.Targets) {
		return errors.New("child metadata cannot outlive its authorization")
	}
	proof, err := metadata.TargetFile().FromBytes(productionProvenanceTarget, f.proof, "sha256")
	if err != nil {
		return err
	}
	if err = validateProductionDescriptor(d, proof); err != nil {
		return err
	}
	if _, err = productionProof(f.seed, d, f.proof, now); err != nil {
		return err
	}
	stat, err := os.Lstat(f.archive)
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() || stat.Size() != d.Archive.Size {
		return errors.New("candidate archive is not the exact regular file")
	}
	actual, err := bundle.Inspect(f.archive)
	if err != nil {
		return err
	}
	if actual.Bytes != d.Archive.Size || actual.SHA256 != d.Archive.SHA256 {
		return errors.New("candidate archive changed")
	}
	return nil
}
func (f *ReleaseFinalizer) targets() (*metadata.Metadata[metadata.TargetsType], error) {
	m := metadata.Targets(f.plan.Expires.Targets.UTC())
	m.Signed.Version = f.plan.Versions.Targets
	for name, b := range map[string][]byte{productionDescriptorTarget: f.descriptor, productionProvenanceTarget: f.proof} {
		t, err := metadata.TargetFile().FromBytes(name, b, "sha256")
		if err != nil {
			return nil, err
		}
		m.Signed.Targets[name] = t
	}
	d := f.plan.Descriptor
	sum, _ := hex.DecodeString(d.Archive.SHA256)
	m.Signed.Targets[productionArchiveTarget] = &metadata.TargetFiles{Length: d.Archive.Size, Hashes: metadata.Hashes{"sha256": sum}}
	return m, nil
}
func releaseMeta(version int64, b []byte) *metadata.MetaFiles {
	sum, _ := hex.DecodeString(digest(b))
	return &metadata.MetaFiles{Version: version, Length: int64(len(b)), Hashes: metadata.Hashes{"sha256": sum}}
}
func (f *ReleaseFinalizer) signatures(role string, body any, sigs []metadata.Signature) ([]metadata.Signature, error) {
	grant := f.root.Signed.Roles[role]
	if grant == nil || len(sigs) < grant.Threshold || len(sigs) > len(grant.KeyIDs) {
		return nil, errors.New("independent role threshold required")
	}
	payload, err := cjson.EncodeCanonical(body)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, id := range grant.KeyIDs {
		allowed[id] = true
	}
	seen := map[string]bool{}
	out := append([]metadata.Signature(nil), sigs...)
	for _, s := range out {
		key := f.root.Signed.Keys[s.KeyID]
		if !allowed[s.KeyID] || seen[s.KeyID] || key == nil {
			return nil, errors.New("wrong, duplicate or unknown signing role")
		}
		pub, err := hex.DecodeString(key.Value.PublicKey)
		if err != nil || len(s.Signature) != ed25519.SignatureSize || !ed25519.Verify(pub, payload, s.Signature) {
			return nil, errors.New("signature differs from exact canonical role payload")
		}
		seen[s.KeyID] = true
	}
	sort.Slice(out, func(i, j int) bool { return out[i].KeyID < out[j].KeyID })
	return out, nil
}
func (f *ReleaseFinalizer) approvedTargets(a ReleaseApprovals) ([]byte, error) {
	m, err := f.targets()
	if err != nil {
		return nil, err
	}
	m.Signatures, err = f.signatures("targets", m.Signed, a.Targets)
	if err != nil {
		return nil, err
	}
	if err = f.root.VerifyDelegate("targets", m); err != nil {
		return nil, err
	}
	return m.ToBytes(false)
}
func (f *ReleaseFinalizer) snapshot(a ReleaseApprovals) (*metadata.Metadata[metadata.SnapshotType], error) {
	b, err := f.approvedTargets(a)
	if err != nil {
		return nil, err
	}
	m := metadata.Snapshot(f.plan.Expires.Snapshot.UTC())
	m.Signed.Version = f.plan.Versions.Snapshot
	m.Signed.Meta = map[string]*metadata.MetaFiles{"targets.json": releaseMeta(f.plan.Versions.Targets, b)}
	return m, nil
}
func (f *ReleaseFinalizer) approvedSnapshot(a ReleaseApprovals) ([]byte, error) {
	m, err := f.snapshot(a)
	if err != nil {
		return nil, err
	}
	m.Signatures, err = f.signatures("snapshot", m.Signed, a.Snapshot)
	if err != nil {
		return nil, err
	}
	if err = f.root.VerifyDelegate("snapshot", m); err != nil {
		return nil, err
	}
	return m.ToBytes(false)
}
func (f *ReleaseFinalizer) timestamp(a ReleaseApprovals) (*metadata.Metadata[metadata.TimestampType], error) {
	b, err := f.approvedSnapshot(a)
	if err != nil {
		return nil, err
	}
	m := metadata.Timestamp(f.plan.Expires.Timestamp.UTC())
	m.Signed.Version = f.plan.Versions.Timestamp
	m.Signed.Meta = map[string]*metadata.MetaFiles{"snapshot.json": releaseMeta(f.plan.Versions.Snapshot, b)}
	return m, nil
}
func (f *ReleaseFinalizer) Request(role string, a ReleaseApprovals) (ReleaseSigningRequest, error) {
	var empty ReleaseSigningRequest
	if err := f.fresh(); err != nil {
		return empty, err
	}
	var body any
	switch role {
	case "targets":
		m, e := f.targets()
		if e != nil {
			return empty, e
		}
		body = m.Signed
	case "snapshot":
		m, e := f.snapshot(a)
		if e != nil {
			return empty, e
		}
		body = m.Signed
	case "timestamp":
		m, e := f.timestamp(a)
		if e != nil {
			return empty, e
		}
		body = m.Signed
	default:
		return empty, errors.New("only targets, snapshot or timestamp signing requests supported")
	}
	payload, err := cjson.EncodeCanonical(body)
	if err != nil {
		return empty, err
	}
	grant := f.root.Signed.Roles[role]
	ids := append([]string(nil), grant.KeyIDs...)
	sort.Strings(ids)
	return ReleaseSigningRequest{Version: 1, Role: role, SeedSHA256: f.pin, PlanSHA256: f.planHash, Payload: payload, PayloadSHA256: digest(payload), KeyIDs: ids, Threshold: grant.Threshold}, nil
}
func (f *ReleaseFinalizer) Finalize(a ReleaseApprovals) (FinalizedRelease, error) {
	var empty FinalizedRelease
	if err := f.fresh(); err != nil {
		return empty, err
	}
	targets, err := f.approvedTargets(a)
	if err != nil {
		return empty, err
	}
	snapshot, err := f.approvedSnapshot(a)
	if err != nil {
		return empty, err
	}
	tm, err := f.timestamp(a)
	if err != nil {
		return empty, err
	}
	tm.Signatures, err = f.signatures("timestamp", tm.Signed, a.Timestamp)
	if err != nil {
		return empty, err
	}
	timestamp, err := tm.ToBytes(false)
	if err != nil {
		return empty, err
	}
	chain, err := initialChain(f.seed, timestamp, snapshot, targets, time.Now().UTC())
	if err != nil {
		return empty, err
	}
	d, err := verifyDescriptor(f.descriptor, f.seed, chain)
	if err != nil || d != f.plan.Descriptor {
		return empty, errors.New("final descriptor failed production bootstrap admission")
	}
	if _, err = productionProof(f.seed, d, f.proof, time.Now().UTC()); err != nil {
		return empty, err
	}
	files := map[string][]byte{fmt.Sprintf("%d.root.json", f.root.Signed.Version): append([]byte(nil), f.seed.Root...), "timestamp.json": timestamp, fmt.Sprintf("%d.snapshot.json", f.plan.Versions.Snapshot): snapshot, fmt.Sprintf("%d.targets.json", f.plan.Versions.Targets): targets, descriptorPath(f.seed, mustReleaseHash(f.descriptor)): append([]byte(nil), f.descriptor...), "targets/provenance/" + digest(f.proof) + ".sigstore.json": append([]byte(nil), f.proof...)}
	publication := ReleasePublication{Version: 1, SigningProfile: f.seed.SigningProfile, Repository: f.seed.Repository, RepositoryID: f.seed.GitHub.RepositoryID, OwnerID: f.seed.Provenance.OwnerID, MetadataBranch: f.seed.GitHub.MetadataBranch, SeedSHA256: f.pin, PlanSHA256: f.planHash, Descriptor: d, Files: files}
	return FinalizedRelease{Files: files, Publication: publication, Report: ReleaseFinalizationReport{Version: 1, SeedSHA256: f.pin, PlanSHA256: f.planHash, DescriptorSHA256: digest(f.descriptor), ArchiveSHA256: d.Archive.SHA256, MetadataVerified: true}}, nil
}
func mustReleaseHash(b []byte) []byte { sum, _ := hex.DecodeString(digest(b)); return sum }
