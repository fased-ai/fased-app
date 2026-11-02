package bootstrap

// Offline, bounded renewal of online roles. No key, transport or scheduler.
import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"github.com/secure-systems-lab/go-securesystemslib/cjson"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

type ReleaseRefreshPlan struct {
	Version              int             `json:"version"`
	CreatedAt            time.Time       `json:"createdAt"`
	Due                  bool            `json:"due"`
	RenewSnapshot        bool            `json:"renewSnapshot"`
	Versions             ReleaseVersions `json:"versions"`
	Expires              ReleaseExpiries `json:"expires"`
	OfflineRenewalNeeded bool            `json:"offlineRenewalNeeded"`
}
type ReleaseRefreshInput struct {
	Seed              []byte
	SeedSHA256        string
	Publication       []byte
	PublicationSHA256 string
	Plan              *ReleaseRefreshPlan
	now               func() time.Time // Controlled tests only; no CLI/environment clock override.
}
type ReleaseRefreshResult struct {
	Plan                      ReleaseRefreshPlan `json:"plan"`
	Publication               ReleasePublication `json:"publication"`
	PreviousPublicationSHA256 string             `json:"previousPublicationSha256"`
	TargetsUnchanged          bool               `json:"targetsUnchanged"`
	NoOp                      bool               `json:"noOp"`
	Published                 bool               `json:"published"`
	Activation                bool               `json:"activation"`
}
type ReleaseRefresher struct {
	input                    ReleaseRefreshInput
	seed                     Seed
	prior                    ReleasePublication
	root                     *metadata.Metadata[metadata.RootType]
	targets                  *metadata.Metadata[metadata.TargetsType]
	snapshot                 *metadata.Metadata[metadata.SnapshotType]
	timestamp                *metadata.Metadata[metadata.TimestampType]
	plan                     ReleaseRefreshPlan
	hash                     string
	targetName, snapshotName string
}

func earlier(values ...time.Time) time.Time {
	r := values[0]
	for _, v := range values[1:] {
		if v.Before(r) {
			r = v
		}
	}
	return r
}
func (f *ReleaseRefresher) currentTime() time.Time {
	if f.input.now != nil {
		return f.input.now().UTC().Truncate(time.Second)
	}
	return time.Now().UTC().Truncate(time.Second)
}
func NewReleaseRefresher(in ReleaseRefreshInput) (*ReleaseRefresher, error) {
	if os.Geteuid() == 0 || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return nil, errors.New("ordinary Ubuntu amd64 publisher required")
	}
	if !hashOK(in.PublicationSHA256) || len(in.Publication) == 0 || len(in.Publication) > 1<<20 || digest(in.Publication) != in.PublicationSHA256 {
		return nil, errors.New("exact bounded prior publication required")
	}
	f := &ReleaseRefresher{input: in}
	f.input.Seed = append([]byte(nil), in.Seed...)
	f.input.Publication = append([]byte(nil), in.Publication...)
	if in.Plan != nil {
		p := *in.Plan
		f.input.Plan = &p
	}
	now := f.currentTime()
	seed, err := parseSeed(in.Seed, in.SeedSHA256, now)
	if err != nil {
		return nil, err
	}
	if seed.Version != 3 || seed.SigningProfile != SingleOwnerSigningProfile {
		return nil, errors.New("explicit single-owner production seed required")
	}
	f.seed = seed
	if update.StrictJSONBounded(in.Publication, &f.prior, 1<<20) != nil {
		return nil, errors.New("strict prior publication required")
	}
	p := f.prior
	if p.Version != 1 || p.SigningProfile != seed.SigningProfile || p.Repository != seed.Repository || p.RepositoryID != seed.GitHub.RepositoryID || p.OwnerID != seed.Provenance.OwnerID || p.MetadataBranch != seed.GitHub.MetadataBranch || p.SeedSHA256 != in.SeedSHA256 || !hashOK(p.PlanSHA256) || len(p.Files) != 6 {
		return nil, errors.New("prior publication differs from pinned release authority")
	}
	f.root, err = metadata.Root().FromBytes(seed.Root)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(p.Files[fmt.Sprintf("%d.root.json", f.root.Signed.Version)], seed.Root) {
		return nil, errors.New("refresh cannot replace Root")
	}
	// Identify exact signed role files. Expired online metadata is historical
	// evidence here only; ordinary bootstrap admission is never relaxed.
	for name, b := range p.Files {
		if len(b) == 0 || len(b) > update.MaxDocument {
			return nil, errors.New("bounded prior public metadata required")
		}
		if name == "timestamp.json" {
			if metadataShape(b, "timestamp") != nil {
				return nil, errors.New("invalid prior timestamp")
			}
			f.timestamp, err = metadata.Timestamp().FromBytes(b)
			if err != nil {
				return nil, err
			}
		}
		if len(name) > 13 && name[len(name)-13:] == ".targets.json" {
			if f.targets != nil || metadataShape(b, "targets") != nil {
				return nil, errors.New("invalid prior Targets")
			}
			f.targets, err = metadata.Targets().FromBytes(b)
			if err != nil {
				return nil, err
			}
			f.targetName = name
		}
		if len(name) > 14 && name[len(name)-14:] == ".snapshot.json" {
			if f.snapshot != nil || metadataShape(b, "snapshot") != nil {
				return nil, errors.New("invalid prior snapshot")
			}
			f.snapshot, err = metadata.Snapshot().FromBytes(b)
			if err != nil {
				return nil, err
			}
			f.snapshotName = name
		}
	}
	if f.targets == nil || f.snapshot == nil || f.timestamp == nil {
		return nil, errors.New("complete prior role chain required")
	}
	if f.targetName != fmt.Sprintf("%d.targets.json", f.targets.Signed.Version) || f.snapshotName != fmt.Sprintf("%d.snapshot.json", f.snapshot.Signed.Version) {
		return nil, errors.New("exact versioned role filenames required")
	}
	if err = f.root.VerifyDelegate("targets", f.targets); err != nil {
		return nil, err
	}
	if err = f.root.VerifyDelegate("snapshot", f.snapshot); err != nil {
		return nil, err
	}
	if err = f.root.VerifyDelegate("timestamp", f.timestamp); err != nil {
		return nil, err
	}
	if len(f.snapshot.Signed.Meta) != 1 || len(f.timestamp.Signed.Meta) != 1 || !metaBound(f.snapshot.Signed.Meta["targets.json"]) || !metaBound(f.timestamp.Signed.Meta["snapshot.json"]) {
		return nil, errors.New("exact previous role links required")
	}
	sm, tm := f.snapshot.Signed.Meta["targets.json"], f.timestamp.Signed.Meta["snapshot.json"]
	if sm.Version != f.targets.Signed.Version || tm.Version != f.snapshot.Signed.Version {
		return nil, errors.New("previous role versions differ")
	}
	if err = sm.VerifyLengthHashes(p.Files[f.targetName]); err != nil {
		return nil, err
	}
	if err = tm.VerifyLengthHashes(p.Files[f.snapshotName]); err != nil {
		return nil, err
	}
	if f.snapshot.Signed.Expires.After(f.targets.Signed.Expires) || f.timestamp.Signed.Expires.After(f.snapshot.Signed.Expires) {
		return nil, errors.New("previous roles exceed offline authority")
	}
	// Authenticate the immutable descriptor/provenance against existing Targets.
	db, err := json.Marshal(p.Descriptor)
	if err != nil {
		return nil, err
	}
	names := targetNames(seed)
	if len(f.targets.Signed.Targets) != len(names) {
		return nil, errors.New("Targets inventory changed")
	}
	for _, name := range names {
		if f.targets.Signed.Targets[name] == nil {
			return nil, errors.New("Targets inventory missing")
		}
	}
	if err = f.targets.Signed.Targets[productionDescriptorTarget].VerifyLengthHashes(db); err != nil {
		return nil, err
	}
	if !bytes.Equal(p.Files[descriptorPath(seed, mustReleaseHash(db))], db) {
		return nil, errors.New("immutable descriptor file changed")
	}
	proof := p.Files["targets/provenance/"+p.Descriptor.Release.Provenance.SHA256+".sigstore.json"]
	if err = f.targets.Signed.Targets[productionProvenanceTarget].VerifyLengthHashes(proof); err != nil {
		return nil, err
	}
	if err = validateProductionDescriptor(p.Descriptor, f.targets.Signed.Targets[productionProvenanceTarget]); err != nil {
		return nil, err
	}
	archive := f.targets.Signed.Targets[productionArchiveTarget]
	if archive.Length != p.Descriptor.Archive.Size || len(archive.Hashes) != 1 || hex.EncodeToString(archive.Hashes["sha256"]) != p.Descriptor.Archive.SHA256 {
		return nil, errors.New("archive authority changed")
	}
	if p.Descriptor.Binding != seed.Binding || p.Descriptor.Sequence < seed.MinimumSequence || p.Descriptor.GitHub.Validate(seed.GitHub) != nil {
		return nil, errors.New("release identity changed")
	}
	if _, err = productionProof(seed, p.Descriptor, proof, now); err != nil {
		return nil, err
	}
	created := now
	if in.Plan != nil {
		created = in.Plan.CreatedAt
	}
	if created.Nanosecond() != 0 || created.After(now) || created.Unix() <= 0 {
		return nil, errors.New("current whole-second refresh plan required")
	}
	f.plan = f.derivePlan(created)
	if in.Plan != nil && *in.Plan != f.plan {
		return nil, errors.New("refresh plan differs from bounded derivation")
	}
	binding, _ := json.Marshal(struct {
		Seed, Previous string
		Plan           ReleaseRefreshPlan
	}{in.SeedSHA256, in.PublicationSHA256, f.plan})
	f.hash = digest(binding)
	if err = f.fresh(); err != nil {
		return nil, err
	}
	return f, nil
}
func (f *ReleaseRefresher) derivePlan(created time.Time) ReleaseRefreshPlan {
	offline := earlier(f.targets.Signed.Expires, f.root.Signed.Expires, time.Unix(f.seed.ExpiresAt, 0))
	snapshotExpiry := earlier(created.Add(7*24*time.Hour), offline)
	renewSnapshot := f.snapshot.Signed.Expires.Sub(created) <= 48*time.Hour && snapshotExpiry.After(f.snapshot.Signed.Expires)
	plan := ReleaseRefreshPlan{Version: 1, CreatedAt: created, Versions: ReleaseVersions{Targets: f.targets.Signed.Version, Snapshot: f.snapshot.Signed.Version, Timestamp: f.timestamp.Signed.Version}, Expires: ReleaseExpiries{Targets: f.targets.Signed.Expires, Snapshot: f.snapshot.Signed.Expires, Timestamp: f.timestamp.Signed.Expires}, OfflineRenewalNeeded: f.targets.Signed.Expires.Sub(created) <= 7*24*time.Hour || f.root.Signed.Expires.Sub(created) <= 30*24*time.Hour || time.Unix(f.seed.ExpiresAt, 0).Sub(created) <= 7*24*time.Hour}
	if renewSnapshot {
		plan.RenewSnapshot = true
		plan.Versions.Snapshot++
		plan.Expires.Snapshot = snapshotExpiry
	}
	timestampExpiry := earlier(created.Add(48*time.Hour), plan.Expires.Snapshot, offline)
	plan.Due = renewSnapshot || (f.timestamp.Signed.Expires.Sub(created) <= 36*time.Hour && timestampExpiry.After(f.timestamp.Signed.Expires))
	if plan.Due {
		plan.Versions.Timestamp++
		plan.Expires.Timestamp = timestampExpiry
	}
	return plan
}
func (f *ReleaseRefresher) fresh() error {
	now := f.currentTime()
	if now.Before(f.plan.CreatedAt) {
		return errors.New("refresh clock moved before frozen plan")
	}
	if _, err := parseSeed(f.input.Seed, f.input.SeedSHA256, now); err != nil {
		return err
	}
	if !f.targets.Signed.Expires.After(now) || f.targets.Signed.Expires.After(now.Add(30*24*time.Hour)) {
		return errors.New("offline Targets authorization expired/out of bounds")
	}
	for _, v := range []int64{f.plan.Versions.Targets, f.plan.Versions.Snapshot, f.plan.Versions.Timestamp} {
		if v < 1 || v > 1<<31-1 {
			return errors.New("refresh version exhausted")
		}
	}
	if f.plan.Due && (!f.plan.Expires.Timestamp.After(now) || !f.plan.Expires.Snapshot.After(now)) {
		return errors.New("frozen refresh plan expired; preserve held operation")
	}
	return nil
}
func (f *ReleaseRefresher) Plan() ReleaseRefreshPlan { return f.plan }
func (f *ReleaseRefresher) onlineSnapshot(a ReleaseApprovals) ([]byte, error) {
	if len(a.Targets) != 0 || (!f.plan.RenewSnapshot && len(a.Snapshot) != 0) {
		return nil, errors.New("refresh cannot accept offline/new unneeded role approval")
	}
	if !f.plan.RenewSnapshot {
		return append([]byte(nil), f.prior.Files[f.snapshotName]...), nil
	}
	m := metadata.Snapshot(f.plan.Expires.Snapshot)
	m.Signed.Version = f.plan.Versions.Snapshot
	m.Signed.Meta = map[string]*metadata.MetaFiles{"targets.json": releaseMeta(f.targets.Signed.Version, f.prior.Files[f.targetName])}
	check := ReleaseFinalizer{root: f.root}
	var err error
	m.Signatures, err = check.signatures("snapshot", m.Signed, a.Snapshot)
	if err != nil {
		return nil, err
	}
	return m.ToBytes(false)
}
func (f *ReleaseRefresher) Request(role string, a ReleaseApprovals) (ReleaseSigningRequest, error) {
	var empty ReleaseSigningRequest
	if err := f.fresh(); err != nil {
		return empty, err
	}
	if !f.plan.Due || len(a.Targets) > 0 {
		return empty, errors.New("refresh not due or offline role prohibited")
	}
	var body any
	switch role {
	case "snapshot":
		if !f.plan.RenewSnapshot {
			return empty, errors.New("snapshot renewal not needed")
		}
		m := metadata.Snapshot(f.plan.Expires.Snapshot)
		m.Signed.Version = f.plan.Versions.Snapshot
		m.Signed.Meta = map[string]*metadata.MetaFiles{"targets.json": releaseMeta(f.targets.Signed.Version, f.prior.Files[f.targetName])}
		body = m.Signed
	case "timestamp":
		b, err := f.onlineSnapshot(a)
		if err != nil {
			return empty, err
		}
		m := metadata.Timestamp(f.plan.Expires.Timestamp)
		m.Signed.Version = f.plan.Versions.Timestamp
		m.Signed.Meta = map[string]*metadata.MetaFiles{"snapshot.json": releaseMeta(f.plan.Versions.Snapshot, b)}
		body = m.Signed
	default:
		return empty, errors.New("only snapshot/timestamp refresh requests permitted")
	}
	payload, err := cjson.EncodeCanonical(body)
	if err != nil {
		return empty, err
	}
	grant := f.root.Signed.Roles[role]
	return ReleaseSigningRequest{Version: 1, Role: role, SeedSHA256: f.input.SeedSHA256, PlanSHA256: f.hash, Payload: payload, PayloadSHA256: digest(payload), KeyIDs: append([]string(nil), grant.KeyIDs...), Threshold: grant.Threshold}, nil
}
func (f *ReleaseRefresher) Finalize(a ReleaseApprovals) (ReleaseRefreshResult, error) {
	var empty ReleaseRefreshResult
	if err := f.fresh(); err != nil {
		return empty, err
	}
	if len(a.Targets) > 0 {
		return empty, errors.New("offline role approval prohibited")
	}
	pub := f.prior
	pub.Files = map[string][]byte{}
	for name, b := range f.prior.Files {
		pub.Files[name] = append([]byte(nil), b...)
	}
	if f.plan.Due {
		snapshot, err := f.onlineSnapshot(a)
		if err != nil {
			return empty, err
		}
		tm := metadata.Timestamp(f.plan.Expires.Timestamp)
		tm.Signed.Version = f.plan.Versions.Timestamp
		tm.Signed.Meta = map[string]*metadata.MetaFiles{"snapshot.json": releaseMeta(f.plan.Versions.Snapshot, snapshot)}
		check := ReleaseFinalizer{root: f.root}
		tm.Signatures, err = check.signatures("timestamp", tm.Signed, a.Timestamp)
		if err != nil {
			return empty, err
		}
		timestamp, err := tm.ToBytes(false)
		if err != nil {
			return empty, err
		}
		delete(pub.Files, f.snapshotName)
		pub.Files[fmt.Sprintf("%d.snapshot.json", f.plan.Versions.Snapshot)] = snapshot
		pub.Files["timestamp.json"] = timestamp
		pub.PlanSHA256 = f.hash
	} else if len(a.Snapshot) > 0 || len(a.Timestamp) > 0 {
		return empty, errors.New("no new approval permitted for not-due refresh")
	}
	chain, err := initialChain(f.seed, pub.Files["timestamp.json"], pub.Files[fmt.Sprintf("%d.snapshot.json", f.plan.Versions.Snapshot)], pub.Files[f.targetName], f.currentTime())
	if err != nil {
		return empty, err
	}
	d, err := verifyDescriptor(mustJSON(pub.Descriptor), f.seed, chain)
	if err != nil || d != f.prior.Descriptor {
		return empty, errors.New("refresh changed release authorization")
	}
	return ReleaseRefreshResult{Plan: f.plan, Publication: pub, PreviousPublicationSHA256: f.input.PublicationSHA256, TargetsUnchanged: bytes.Equal(pub.Files[f.targetName], f.prior.Files[f.targetName]), NoOp: !f.plan.Due}, nil
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
