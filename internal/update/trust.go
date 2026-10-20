package update

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/trustedmetadata"
)

const RehearsalPurpose = "fased-app-tuf-rehearsal:v1"
const maxTrustMetadata = 8 << 10
const fixtureTarget = "fixture/descriptor.json"

type FixtureBinding struct {
	FixtureOnly bool   `json:"fixtureOnly"`
	Purpose     string `json:"purpose"`
	Repository  string `json:"repository"`
	Channel     string `json:"channel"`
}

func (b FixtureBinding) valid() bool {
	return b.FixtureOnly && b.Purpose == RehearsalPurpose && IDOK(b.Repository) && b.Channel == "development"
}

type TrustFixtureSeed struct {
	FixtureBinding
	Version        int    `json:"version"`
	Origin         string `json:"origin"`
	Root           []byte `json:"root"`
	CreatedAt      int64  `json:"createdAt"`
	PreserveSHA256 string `json:"preserveSha256"`
	InstalledFloor uint64 `json:"installedFloor,string"`
	ReservedFloor  uint64 `json:"reservedFloor,string"`
}

type TrustFixtureDescriptor struct {
	FixtureBinding
	Version        int    `json:"version"`
	Sequence       uint64 `json:"sequence,string"`
	PreserveSHA256 string `json:"preserveSha256"`
	EvidenceSHA256 string `json:"evidenceSha256"` // Inert fixture identity, not verified build provenance.
}

type TrustFixtureSnapshot struct {
	FixtureBinding
	Version        int    `json:"version"`
	Complete       bool   `json:"complete"`
	SeedSHA256     string `json:"seedSha256"`
	Root           []byte `json:"root"`
	Timestamp      []byte `json:"timestamp"`
	Snapshot       []byte `json:"snapshot"`
	Targets        []byte `json:"targets"`
	Descriptor     []byte `json:"descriptor"`
	ObservedAt     int64  `json:"observedAt"`
	ReleaseFloor   uint64 `json:"releaseFloor,string"`
	PreserveSHA256 string `json:"preserveSha256"`
	InstalledFloor uint64 `json:"installedFloor,string"`
	ReservedFloor  uint64 `json:"reservedFloor,string"`
}

type TrustRehearsalOptions struct {
	Development    bool
	Initial        bool
	Seed           []byte
	SeedSHA256     string
	Previous       []byte
	PreviousSHA256 string
	Now            int64 // Explicit fixture clock only; never host clock mutation.
	TLSCA          []byte
}

type TrustRehearsalReport struct {
	Version            int             `json:"version"`
	Result             string          `json:"result"`
	FixtureOnly        bool            `json:"fixtureOnly"`
	ReadOnly           bool            `json:"readOnly"`
	MetadataVerified   bool            `json:"metadataVerified"`
	DescriptorVerified bool            `json:"descriptorVerified"`
	OnlineCacheReset   bool            `json:"onlineCacheReset"`
	TrustProgressOnly  bool            `json:"trustProgressOnly"`
	RootVersion        int64           `json:"rootVersion"`
	Sequence           uint64          `json:"sequence,string"`
	SeedSHA256         string          `json:"seedSha256"`
	PreviousSHA256     string          `json:"previousSha256"`
	NextSHA256         string          `json:"nextSha256"`
	NextState          json.RawMessage `json:"nextState"`
	InstallAllowed     bool            `json:"installAllowed"`
	ResumeAllowed      bool            `json:"resumeAllowed"`
	UpdaterEnabled     bool            `json:"updaterEnabled"`
	WalletSigner       bool            `json:"walletSigner"`
	FinancialExecution bool            `json:"financialExecution"`
}

// TrustTransition cannot be constructed from JSON or an app-supplied approval.
// Its snapshot is disposable model state; it never conveys installed authority.
type TrustTransition struct {
	next, previous []byte
	seedSHA        string
	report         TrustRehearsalReport
}

func (t TrustTransition) Report() TrustRehearsalReport {
	r := t.report
	r.NextState = append(json.RawMessage(nil), t.next...)
	return r
}

func trustBody(b []byte, role string, binding FixtureBinding) (map[string]json.RawMessage, error) {
	var e struct {
		Signed     json.RawMessage `json:"signed"`
		Signatures []struct {
			KeyID string `json:"keyid"`
			Sig   string `json:"sig"`
		} `json:"signatures"`
	}
	if len(b) > maxTrustMetadata || StrictJSON(b, &e) != nil || len(e.Signatures) < 1 || len(e.Signatures) > 8 {
		return nil, errors.New("invalid bounded fixture metadata envelope")
	}
	for _, s := range e.Signatures {
		v, err := hex.DecodeString(s.Sig)
		if !hashOK(s.KeyID) || err != nil || len(v) != 64 {
			return nil, errors.New("invalid fixture signature shape")
		}
	}
	var body map[string]json.RawMessage
	if StrictJSON(e.Signed, &body) != nil {
		return nil, errors.New("invalid fixture signed body")
	}
	allowed := map[string]bool{"_type": true, "spec_version": true, "version": true, "expires": true, "fasedFixture": true}
	switch role {
	case "root":
		allowed["keys"], allowed["roles"], allowed["consistent_snapshot"] = true, true, true
	case "targets":
		allowed["targets"] = true
	default:
		allowed["meta"] = true
	}
	for k := range body {
		if !allowed[k] {
			return nil, errors.New("unsupported fixture metadata field")
		}
	}
	var mark FixtureBinding
	var kind, spec string
	var version int64
	var expires time.Time
	if StrictJSON(body["fasedFixture"], &mark) != nil || mark != binding || !mark.valid() || json.Unmarshal(body["_type"], &kind) != nil || kind != role || json.Unmarshal(body["spec_version"], &spec) != nil || spec != metadata.SPECIFICATION_VERSION || json.Unmarshal(body["version"], &version) != nil || version < 1 || version > 1<<31-1 || json.Unmarshal(body["expires"], &expires) != nil || expires.Unix() <= 0 {
		return nil, errors.New("wrong fixture binding/type/spec/version/expiry")
	}
	return body, nil
}

func fixtureRoot(b []byte, binding FixtureBinding) (*metadata.Metadata[metadata.RootType], error) {
	body, err := trustBody(b, "root", binding)
	if err != nil {
		return nil, err
	}
	r, err := metadata.Root().FromBytes(b)
	if err != nil {
		return nil, err
	}
	if !r.Signed.ConsistentSnapshot || len(r.Signed.Keys) != 8 || len(r.Signed.Roles) != 4 {
		return nil, errors.New("fixed four-role consistent-snapshot fixture required")
	}
	var keys map[string]struct {
		Type   string `json:"keytype"`
		Scheme string `json:"scheme"`
		Value  struct {
			Public string `json:"public"`
		} `json:"keyval"`
	}
	var roles map[string]struct {
		KeyIDs    []string `json:"keyids"`
		Threshold int      `json:"threshold"`
	}
	if StrictJSON(body["keys"], &keys) != nil || StrictJSON(body["roles"], &roles) != nil {
		return nil, errors.New("invalid fixture key/role fields")
	}
	seen, public := map[string]bool{}, map[string]bool{}
	for name, count := range map[string]int{"root": 3, "targets": 3, "snapshot": 1, "timestamp": 1} {
		role := r.Signed.Roles[name]
		threshold := 1
		if count == 3 {
			threshold = 2
		}
		if role == nil || role.Threshold != threshold || len(role.KeyIDs) != count {
			return nil, errors.New("fixture quorum policy mismatch")
		}
		for _, id := range role.KeyIDs {
			k := r.Signed.Keys[id]
			if k == nil || k.Type != "ed25519" || k.Scheme != "ed25519" || !hashOK(k.Value.PublicKey) || seen[id] || public[k.Value.PublicKey] {
				return nil, errors.New("fixture keys must be distinct across roles")
			}
			actual, e := k.ID()
			if e != nil || actual != id {
				return nil, errors.New("noncanonical fixture key ID")
			}
			seen[id], public[k.Value.PublicKey] = true, true
		}
	}
	return r, nil
}

func fixtureMetaBody(b []byte, role string, binding FixtureBinding, child string) error {
	body, e := trustBody(b, role, binding)
	if e != nil {
		return e
	}
	var entries map[string]struct {
		Version int64             `json:"version"`
		Length  int64             `json:"length"`
		Hashes  map[string]string `json:"hashes"`
	}
	if StrictJSON(body["meta"], &entries) != nil || len(entries) != 1 {
		return errors.New("single bounded metadata link required")
	}
	c, ok := entries[child+".json"]
	if !ok || c.Version < 1 || c.Version > 1<<31-1 || c.Length <= 0 || c.Length > maxTrustMetadata || len(c.Hashes) != 1 || !hashOK(c.Hashes["sha256"]) {
		return errors.New("exact metadata length/hash/version required")
	}
	return nil
}

func verifyFixtureSet(s TrustFixtureSnapshot) (*trustedmetadata.TrustedMetadata, TrustFixtureDescriptor, error) {
	var d TrustFixtureDescriptor
	if _, e := fixtureRoot(s.Root, s.FixtureBinding); e != nil {
		return nil, d, e
	}
	t, e := trustedmetadata.New(s.Root)
	if e != nil {
		return nil, d, e
	}
	t.RefTime = time.Unix(s.ObservedAt, 0).UTC()
	if !t.Root.Signed.Expires.After(t.RefTime) {
		return nil, d, errors.New("final fixture root expired")
	}
	if e = fixtureMetaBody(s.Timestamp, "timestamp", s.FixtureBinding, "snapshot"); e != nil {
		return nil, d, e
	}
	if _, e = t.UpdateTimestamp(s.Timestamp); e != nil {
		return nil, d, e
	}
	if !t.Timestamp.Signed.Expires.After(t.RefTime) {
		return nil, d, errors.New("fixture timestamp expired")
	}
	if e = fixtureMetaBody(s.Snapshot, "snapshot", s.FixtureBinding, "targets"); e != nil {
		return nil, d, e
	}
	if _, e = t.UpdateSnapshot(s.Snapshot, false); e != nil {
		return nil, d, e
	}
	if !t.Snapshot.Signed.Expires.After(t.RefTime) {
		return nil, d, errors.New("fixture snapshot expired")
	}
	body, e := trustBody(s.Targets, "targets", s.FixtureBinding)
	if e != nil {
		return nil, d, e
	}
	var targets map[string]struct {
		Length int64             `json:"length"`
		Hashes map[string]string `json:"hashes"`
	}
	if StrictJSON(body["targets"], &targets) != nil || len(targets) != 1 {
		return nil, d, errors.New("one inert fixture target required")
	}
	target, ok := targets[fixtureTarget]
	if !ok || target.Length < 1 || target.Length > 1024 || len(target.Hashes) != 1 || !hashOK(target.Hashes["sha256"]) {
		return nil, d, errors.New("invalid descriptor target")
	}
	if _, e = t.UpdateTargets(s.Targets); e != nil {
		return nil, d, e
	}
	if !t.Targets["targets"].Signed.Expires.After(t.RefTime) {
		return nil, d, errors.New("fixture targets expired")
	}
	if e = t.Targets["targets"].Signed.Targets[fixtureTarget].VerifyLengthHashes(s.Descriptor); e != nil {
		return nil, d, e
	}
	if len(s.Descriptor) > 1024 || StrictJSON(s.Descriptor, &d) != nil || d.Version != 1 || d.FixtureBinding != s.FixtureBinding || d.Sequence == 0 || d.PreserveSHA256 != s.PreserveSHA256 || !hashOK(d.EvidenceSHA256) {
		return nil, d, errors.New("invalid inert fixture descriptor")
	}
	return t, d, nil
}

func onlineRoleChanged(a, b *metadata.Metadata[metadata.RootType]) bool {
	for _, name := range []string{"timestamp", "snapshot"} {
		x, y := a.Signed.Roles[name], b.Signed.Roles[name]
		if x.Threshold != y.Threshold || len(x.KeyIDs) != len(y.KeyIDs) {
			return true
		}
		for i, k := range x.KeyIDs {
			if k != y.KeyIDs[i] {
				return true
			}
		}
	}
	return false
}

// A checkpoint is independently hash-anchored fixture state. Cached role bytes
// supply rollback floors, never new release authority; they may use old keys
// after a verified root transition. Only a complete freshly verified chain can
// produce MetadataVerified/DescriptorVerified.
func fixtureRoleCompare(old, next []byte, role string, binding FixtureBinding) error {
	if len(old) == 0 {
		return nil
	}
	a, e := trustBody(old, role, binding)
	if e != nil {
		return e
	}
	b, e := trustBody(next, role, binding)
	if e != nil {
		return e
	}
	var av, bv int64
	_ = json.Unmarshal(a["version"], &av)
	_ = json.Unmarshal(b["version"], &bv)
	if bv < av || bv == av && digest(old) != digest(next) {
		return errors.New("metadata rollback or same-version identity conflict")
	}
	if role == "timestamp" || role == "snapshot" {
		var am, bm map[string]json.RawMessage
		_ = json.Unmarshal(a["meta"], &am)
		_ = json.Unmarshal(b["meta"], &bm)
		name := "snapshot.json"
		if role == "snapshot" {
			name = "targets.json"
		}
		var x, y struct {
			Version int64 `json:"version"`
		}
		_ = json.Unmarshal(am[name], &x)
		_ = json.Unmarshal(bm[name], &y)
		if y.Version < x.Version || y.Version == x.Version && digest(am[name]) != digest(bm[name]) {
			return errors.New("metadata child pointer rollback/conflict")
		}
	}
	return nil
}

func trustProposal(s TrustFixtureSnapshot, previous []byte, seedSHA string, reset bool) (TrustTransition, error) {
	b, e := json.Marshal(s)
	if e != nil || len(b) > MaxDocument {
		return TrustTransition{}, errors.New("fixture snapshot bound exceeded")
	}
	root, e := fixtureRoot(s.Root, s.FixtureBinding)
	if e != nil {
		return TrustTransition{}, e
	}
	result := "verified-disposable-tuf-rehearsal"
	if !s.Complete {
		result = "blocked-disposable-tuf-progress"
	}
	prev := ""
	if len(previous) > 0 {
		prev = digest(previous)
	}
	return TrustTransition{next: b, previous: append([]byte(nil), previous...), seedSHA: seedSHA, report: TrustRehearsalReport{Version: 1, Result: result, FixtureOnly: true, ReadOnly: true, MetadataVerified: s.Complete, DescriptorVerified: s.Complete, TrustProgressOnly: !s.Complete, OnlineCacheReset: reset, RootVersion: root.Signed.Version, Sequence: s.ReleaseFloor, SeedSHA256: seedSHA, PreviousSHA256: prev, NextSHA256: digest(b)}}, nil
}

// RehearseTrust is read-only. A later failure returns an error and, when new
// trusted metadata was learned, an opaque progress checkpoint. Ignoring that
// progress can lose revocations/rollback floors; it is never candidate approval.
func RehearseTrust(ctx context.Context, o TrustRehearsalOptions) (out TrustTransition, err error) {
	if !o.Development || !hashOK(o.SeedSHA256) || digest(o.Seed) != o.SeedSHA256 || o.Now <= 0 || o.Now > 4102444800 {
		return out, errors.New("explicit pinned fixture seed and injected clock required")
	}
	var seed TrustFixtureSeed
	if StrictJSON(o.Seed, &seed) != nil || seed.Version != 1 || !seed.FixtureBinding.valid() || !hashOK(seed.PreserveSHA256) || seed.CreatedAt <= 0 || seed.CreatedAt > o.Now || seed.ReservedFloor < seed.InstalledFloor {
		return out, errors.New("invalid disposable trust seed")
	}
	u, e := acquisitionURL(seed.Origin)
	if e != nil || u.Path != "" {
		return out, errors.New("exact fixture HTTPS origin required")
	}
	if _, e = fixtureRoot(seed.Root, seed.FixtureBinding); e != nil {
		return out, e
	}
	if _, e = trustedmetadata.New(seed.Root); e != nil {
		return out, e
	}
	s := TrustFixtureSnapshot{FixtureBinding: seed.FixtureBinding, Version: 1, SeedSHA256: o.SeedSHA256, Root: seed.Root, ObservedAt: o.Now, ReleaseFloor: seed.ReservedFloor, PreserveSHA256: seed.PreserveSHA256, InstalledFloor: seed.InstalledFloor, ReservedFloor: seed.ReservedFloor}
	if o.Initial {
		if len(o.Previous) != 0 || o.PreviousSHA256 != "" {
			return out, errors.New("initial mode cannot adopt prior state")
		}
	} else {
		var old TrustFixtureSnapshot
		if !hashOK(o.PreviousSHA256) || digest(o.Previous) != o.PreviousSHA256 || StrictJSON(o.Previous, &old) != nil || old.Version != 1 || old.FixtureBinding != seed.FixtureBinding || old.SeedSHA256 != o.SeedSHA256 || old.ObservedAt < seed.CreatedAt || old.ObservedAt > o.Now || old.PreserveSHA256 != seed.PreserveSHA256 || old.InstalledFloor != seed.InstalledFloor || old.ReservedFloor != seed.ReservedFloor || old.ReleaseFloor < seed.ReservedFloor {
			return out, errors.New("missing, corrupt, mismatched or backward fixture state")
		}
		if _, e = fixtureRoot(old.Root, seed.FixtureBinding); e != nil {
			return out, e
		}
		if len(old.Descriptor) > 0 {
			var d TrustFixtureDescriptor
			if len(old.Descriptor) > 1024 || StrictJSON(old.Descriptor, &d) != nil || d.Version != 1 || d.FixtureBinding != seed.FixtureBinding || d.Sequence != old.ReleaseFloor || d.PreserveSHA256 != seed.PreserveSHA256 || !hashOK(d.EvidenceSHA256) {
				return out, errors.New("invalid checkpoint release identity")
			}
		} else if old.ReleaseFloor != seed.ReservedFloor {
			return out, errors.New("missing checkpoint release identity")
		}
		for _, pair := range []struct {
			bytes []byte
			role  string
		}{{old.Timestamp, "timestamp"}, {old.Snapshot, "snapshot"}, {old.Targets, "targets"}} {
			if len(pair.bytes) > 0 {
				if _, e = trustBody(pair.bytes, pair.role, seed.FixtureBinding); e != nil {
					return out, e
				}
			}
		}
		if old.Complete {
			_, d, e := verifyFixtureSet(old)
			if e != nil || d.Sequence != old.ReleaseFloor || old.ReleaseFloor <= seed.ReservedFloor {
				return out, errors.New("invalid completed fixture checkpoint")
			}
		}
		s = old
		s.ObservedAt = o.Now
		s.Complete = false
	}
	changed, reset := false, false
	defer func() {
		if err != nil && changed {
			var pe error
			out, pe = trustProposal(s, o.Previous, o.SeedSHA256, reset)
			err = errors.Join(err, pe)
		}
	}()
	client, transport, e := acquisitionClient(o.TLSCA)
	if e != nil {
		return out, e
	}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	get := func(name string, optional bool, limit int64) ([]byte, error) {
		r, e := http.NewRequestWithContext(ctx, http.MethodGet, seed.Origin+"/tuf/"+name, nil)
		if e != nil {
			return nil, e
		}
		r.Header.Set("Accept-Encoding", "identity")
		resp, e := client.Do(r)
		if e != nil {
			return nil, errors.New("controlled HTTPS metadata transport refused")
		}
		defer resp.Body.Close()
		if optional && resp.StatusCode == 404 {
			return nil, nil
		}
		if resp.StatusCode != 200 || resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("Content-Range") != "" || resp.ContentLength > limit {
			return nil, errors.New("complete bounded fixture response required")
		}
		b, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if e != nil || len(b) == 0 || int64(len(b)) > limit {
			return nil, errors.New("incomplete/oversized fixture response")
		}
		return b, nil
	}
	chain, e := trustedmetadata.New(s.Root)
	if e != nil {
		return out, e
	}
	chain.RefTime = time.Unix(o.Now, 0).UTC()
	for steps := 0; ; steps++ {
		if steps == 32 {
			return out, errors.New("root catch-up budget reached")
		}
		b, e := get(strconv.FormatInt(chain.Root.Signed.Version+1, 10)+".root.json", true, maxTrustMetadata)
		if e != nil {
			return out, e
		}
		if b == nil {
			break
		}
		next, e := fixtureRoot(b, seed.FixtureBinding)
		if e != nil {
			return out, e
		}
		online := onlineRoleChanged(chain.Root, next)
		if _, e = chain.UpdateRoot(b); e != nil {
			return out, e
		}
		s.Root = b
		changed = true
		if online {
			reset = true
			s.Timestamp = nil
			s.Snapshot = nil
			s.Targets = nil
		}
	}
	if !chain.Root.Signed.Expires.After(chain.RefTime) {
		return out, errors.New("final fixture root expired")
	}
	b, e := get("timestamp.json", false, maxTrustMetadata)
	if e != nil {
		return out, e
	}
	if e = fixtureMetaBody(b, "timestamp", seed.FixtureBinding, "snapshot"); e != nil {
		return out, e
	}
	if _, e = chain.UpdateTimestamp(b); e != nil {
		return out, e
	}
	if !chain.Timestamp.Signed.Expires.After(chain.RefTime) {
		return out, errors.New("fixture timestamp expired")
	}
	if e = fixtureRoleCompare(s.Timestamp, b, "timestamp", seed.FixtureBinding); e != nil {
		return out, e
	}
	if digest(s.Timestamp) != digest(b) {
		changed = true
	}
	s.Timestamp = b
	b, e = get(fmt.Sprintf("%d.snapshot.json", chain.Timestamp.Signed.Meta["snapshot.json"].Version), false, maxTrustMetadata)
	if e != nil {
		return out, e
	}
	if e = fixtureMetaBody(b, "snapshot", seed.FixtureBinding, "targets"); e != nil {
		return out, e
	}
	if _, e = chain.UpdateSnapshot(b, false); e != nil {
		return out, e
	}
	if !chain.Snapshot.Signed.Expires.After(chain.RefTime) {
		return out, errors.New("fixture snapshot expired")
	}
	if e = fixtureRoleCompare(s.Snapshot, b, "snapshot", seed.FixtureBinding); e != nil {
		return out, e
	}
	if digest(s.Snapshot) != digest(b) {
		changed = true
	}
	s.Snapshot = b
	b, e = get(fmt.Sprintf("%d.targets.json", chain.Snapshot.Signed.Meta["targets.json"].Version), false, maxTrustMetadata)
	if e != nil {
		return out, e
	}
	if _, e = trustBody(b, "targets", seed.FixtureBinding); e != nil {
		return out, e
	}
	if _, e = chain.UpdateTargets(b); e != nil {
		return out, e
	}
	if !chain.Targets["targets"].Signed.Expires.After(chain.RefTime) {
		return out, errors.New("fixture targets expired")
	}
	if e = fixtureRoleCompare(s.Targets, b, "targets", seed.FixtureBinding); e != nil {
		return out, e
	}
	if digest(s.Targets) != digest(b) {
		changed = true
	}
	s.Targets = b
	target := chain.Targets["targets"].Signed.Targets[fixtureTarget]
	if target == nil || target.Length < 1 || target.Length > 1024 || len(target.Hashes) != 1 || len(target.Hashes["sha256"]) != 32 {
		return out, errors.New("missing bounded descriptor")
	}
	descriptor, e := get("targets/fixture/"+hex.EncodeToString(target.Hashes["sha256"])+".descriptor.json", false, 1024)
	if e != nil {
		return out, e
	}
	candidate := s
	candidate.Descriptor = descriptor
	_, d, e := verifyFixtureSet(candidate)
	if e != nil {
		return out, e
	}
	if d.Sequence <= seed.ReservedFloor || d.Sequence < s.ReleaseFloor || d.Sequence == s.ReleaseFloor && digest(descriptor) != digest(s.Descriptor) {
		return out, errors.New("fixture release floor or same-sequence identity conflict")
	}
	s.Descriptor = descriptor
	s.ReleaseFloor = d.Sequence
	s.Complete = true
	return trustProposal(s, o.Previous, o.SeedSHA256, reset)
}
