package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// This entire adapter is package-private and non-activated. Its UID arguments
// describe disposable fixtures; no installed caller or configuration uses it.
type inspectionReader struct {
	Profile     string `json:"profile"`
	Capability  string `json:"capability"`
	Descriptor  int    `json:"descriptor"`
	Store       int    `json:"store"`
	Source      int    `json:"source"`
	Predecessor int    `json:"predecessor"`
}

func inspectionReaderV1() inspectionReader {
	return inspectionReader{inspectionProfile, inspectionCapability, 1, 1, 1, 4}
}

type inspectionCatalog struct {
	Version         int              `json:"version"`
	Profile         string           `json:"profile"`
	FixtureOnly     bool             `json:"fixtureOnly"`
	Provision       string           `json:"provision"`
	Installation    string           `json:"installation"`
	ConfigSHA       string           `json:"configSha256"`
	RootSHA         string           `json:"rootSha256"`
	ManifestSHA     string           `json:"manifestSha256"`
	BeforeImages    [2]string        `json:"beforeImages"`
	CandidateImages [2]string        `json:"candidateImages"`
	BeforeUnits     string           `json:"beforeUnits"`
	GuardedUnits    string           `json:"guardedUnits"`
	FinalUnits      string           `json:"finalUnits"`
	UIDs            [3]int           `json:"uids"`
	GIDs            [3]int           `json:"gids"`
	Reader          inspectionReader `json:"reader"`
	Roles           [2]string        `json:"roles"` // Exact separately loaded descriptor hashes.
}
type inspectionRoleInput struct {
	Version      int              `json:"version"`
	Profile      string           `json:"profile"`
	FixtureOnly  bool             `json:"fixtureOnly"`
	Provision    string           `json:"provision"`
	Installation string           `json:"installation"`
	ConfigSHA    string           `json:"configSha256"`
	Role         string           `json:"role"`
	UID          int              `json:"uid"`
	GID          int              `json:"gid"`
	SeedSHA      string           `json:"seedSha256"`
	PolicySHA    string           `json:"policySha256"`
	FetchPin     string           `json:"fetchPolicySha256"`
	IndexSHA     string           `json:"indexSha256"`
	Epoch        string           `json:"epoch"`
	Reader       inspectionReader `json:"reader"`
}
type inspectionClock struct {
	Version   int    `json:"version"`
	Profile   string `json:"profile"`
	Provision string `json:"provision"`
	Role      string `json:"role"`
	Epoch     string `json:"epoch"`
	Revision  uint64 `json:"revision,string"`
	UTC       int64  `json:"utc"`
	Confident bool   `json:"confident"`
}
type inspectionInputs struct {
	catalog      inspectionCatalog
	role         inspectionRoleInput
	source       *inspectionOfflineSource
	seed, policy []byte
	directory    string
	publisher    int
	pin          string
}

func mustInspectionJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func openInspectionInputs(dir, catalogPin, role string, publisher int) (*inspectionInputs, error) {
	if !hashOK(catalogPin) || role != "owner" && role != "signer" {
		return nil, errors.New("explicit fixture catalog/role required")
	}
	r, e := inspectionRoot(dir, publisher, 0755)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	names, e := inspectionNames(r)
	expected := []string{"bundle", "catalog.json", "owner-clock.json", "owner-policy.json", "owner-seed.json", "owner.json", "signer-clock.json", "signer-policy.json", "signer-seed.json", "signer.json"}
	if e != nil || strings.Join(names, "\x00") != strings.Join(expected, "\x00") {
		return nil, errors.New("closed fixture catalog namespace required")
	}
	cb, e := inspectionRead(r, "catalog.json", publisher, 0644, MaxDocument)
	if e != nil || digest(cb) != catalogPin {
		return nil, errors.New("catalog pin mismatch")
	}
	var c inspectionCatalog
	if StrictJSON(cb, &c) != nil || c.Version != 1 || c.Profile != inspectionProfile || !c.FixtureOnly || !IDOK(c.Provision) || !IDOK(c.Installation) || c.Reader != inspectionReaderV1() {
		return nil, errors.New("unsupported fixture catalog")
	}
	for _, h := range append([]string{c.ConfigSHA, c.RootSHA, c.ManifestSHA, c.BeforeUnits, c.GuardedUnits, c.FinalUnits, c.Roles[0], c.Roles[1]}, append(c.BeforeImages[:], c.CandidateImages[:]...)...) {
		if !hashOK(h) {
			return nil, errors.New("incomplete catalog identities")
		}
	}
	for i, u := range c.UIDs {
		if u <= 0 || c.GIDs[i] <= 0 {
			return nil, errors.New("ordinary role identities required")
		}
		for j := 0; j < i; j++ {
			if u == c.UIDs[j] {
				return nil, errors.New("three distinct fixture UIDs required")
			}
		}
	}
	slot, identity := 0, 0
	if role == "signer" {
		slot, identity = 1, 2
	}
	rb, e := inspectionRead(r, role+".json", publisher, 0644, MaxDocument)
	if e != nil || digest(rb) != c.Roles[slot] {
		return nil, errors.New("role descriptor pin mismatch")
	}
	var d inspectionRoleInput
	if StrictJSON(rb, &d) != nil || d.Version != 1 || d.Profile != inspectionProfile || !d.FixtureOnly || d.Provision != c.Provision || d.Installation != c.Installation || d.ConfigSHA != c.ConfigSHA || d.Role != role || d.UID != c.UIDs[identity] || d.GID != c.GIDs[identity] || d.Reader != c.Reader || !IDOK(d.Epoch) {
		return nil, errors.New("role descriptor binding refused")
	}
	for _, h := range []string{d.SeedSHA, d.PolicySHA, d.FetchPin, d.IndexSHA} {
		if !hashOK(h) {
			return nil, errors.New("independent role pins required")
		}
	}
	seed, e := inspectionRead(r, role+"-seed.json", publisher, 0644, MaxDocument)
	if e != nil || digest(seed) != d.SeedSHA {
		return nil, errors.New("role seed mismatch")
	}
	policy, e := inspectionRead(r, role+"-policy.json", publisher, 0644, MaxDocument)
	if e != nil || digest(policy) != d.PolicySHA {
		return nil, errors.New("role policy mismatch")
	}
	var seedValue TrustFixtureSeed
	if StrictJSON(seed, &seedValue) != nil || !seedValue.FixtureBinding.valid() {
		return nil, errors.New("fixture seed required")
	}
	source, e := openInspectionSource(filepath.Join(dir, "bundle"), d.IndexSHA, seedValue.Origin, d.FetchPin, publisher)
	if e != nil {
		return nil, e
	}
	root, e := fixtureRoot(seedValue.Root, seedValue.FixtureBinding)
	if e != nil {
		return nil, e
	}
	if e = source.rootRange(root.Signed.Version); e != nil {
		return nil, e
	}
	in := &inspectionInputs{c, d, source, seed, policy, dir, publisher, catalogPin}
	_, e = in.clock()
	if e != nil {
		return nil, e
	}
	return in, nil
}
func (in *inspectionInputs) clock() (inspectionClock, error) {
	var c inspectionClock
	r, e := inspectionRoot(in.directory, in.publisher, 0755)
	if e != nil {
		return c, e
	}
	defer r.Close()
	b, e := inspectionRead(r, in.role.Role+"-clock.json", in.publisher, 0644, 4096)
	if e != nil || StrictJSON(b, &c) != nil || c.Version != 1 || c.Profile != inspectionProfile || c.Provision != in.role.Provision || c.Role != in.role.Role || c.Epoch != in.role.Epoch || c.Revision == 0 || c.UTC <= 0 || c.UTC > 4102444800 || !c.Confident {
		return c, errors.New("independent confident fixture clock required")
	}
	return c, nil
}

// Prefix-safe immutable fixture publication. Existing bytes are preserved on
// every conflict. This is not a root publisher or installation entrypoint.
func publishInspectionFile(dir, name string, wanted []byte, uid int) error {
	if name == "" || filepath.Base(name) != name || name == "." || strings.HasSuffix(name, ".tmp") || len(wanted) == 0 || len(wanted) > MaxDocument {
		return errors.New("fixed bounded publication required")
	}
	r, e := inspectionRoot(dir, uid, 0755)
	if e != nil {
		return e
	}
	defer r.Close()
	if b, e := inspectionRead(r, name, uid, 0644, MaxDocument); e == nil {
		if !bytes.Equal(b, wanted) {
			return errors.New("immutable publication conflict")
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	tmp := "publish-" + digest(append([]byte(name+"\x00"), wanted...)) + ".tmp"
	b, e := inspectionRead(r, tmp, uid, 0644, MaxDocument)
	flags := os.O_WRONLY | os.O_APPEND
	if os.IsNotExist(e) {
		flags |= os.O_CREATE | os.O_EXCL
		b = nil
	} else if e != nil {
		return e
	}
	if len(b) > len(wanted) || !bytes.Equal(b, wanted[:len(b)]) {
		return errors.New("foreign partial publication")
	}
	f, e := r.OpenFile(tmp, flags, 0644)
	if e != nil {
		return e
	}
	if _, e = f.Write(wanted[len(b):]); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	// Atomic no-replace publication cannot replace a foreign final file.
	if e = inspectionPublish(r, tmp, name); e != nil {
		return e
	}
	d, e := r.Open(".")
	if e != nil {
		return e
	}
	defer d.Close()
	if e = d.Sync(); e != nil {
		return e
	}
	b, e = inspectionRead(r, name, uid, 0644, MaxDocument)
	if e != nil || !bytes.Equal(b, wanted) {
		return errors.New("published fixture integrity mismatch")
	}
	return nil
}

type inspectionCheckpoint struct {
	Version    int             `json:"version"`
	Profile    string          `json:"profile"`
	Config     string          `json:"config"`
	Role       string          `json:"role"`
	Serial     uint64          `json:"serial,string"`
	Clock      inspectionClock `json:"clock"`
	Pending    string          `json:"pending"`
	Historical bool            `json:"historical"`
	Trust      json.RawMessage `json:"trust,omitempty"`
	Source     string          `json:"source"`
}
type inspectionHead struct {
	Version int    `json:"version"`
	Profile string `json:"profile"`
	Config  string `json:"config"`
	Role    string `json:"role"`
	SHA     string `json:"sha256"`
}
type inspectionPublication struct{ previous, next []byte }
type inspectionStore struct {
	state, anchor *FixtureTrustStore
	in            *inspectionInputs
}

func openInspectionStore(state, anchor string, in *inspectionInputs) (*inspectionStore, error) {
	if state == anchor {
		return nil, errors.New("separate private checkpoint/anchor required")
	}
	a, e := inspectionRoot(state, os.Geteuid(), 0700)
	if e != nil {
		return nil, e
	}
	a.Close()
	a, e = inspectionRoot(anchor, os.Geteuid(), 0700)
	if e != nil {
		return nil, e
	}
	a.Close()
	s, e := OpenFixtureTrustStore(state)
	if e != nil {
		return nil, e
	}
	s.limit = maxHandoffCheckpoint
	t, e := OpenFixtureTrustStore(anchor)
	if e != nil {
		s.Close()
		return nil, e
	}
	t.limit = 4096
	return &inspectionStore{s, t, in}, nil
}
func (s *inspectionStore) close() { s.state.Close(); s.anchor.Close() }
func (s *inspectionStore) parse(b []byte) (inspectionCheckpoint, error) {
	var c inspectionCheckpoint
	if strictJSONBounded(b, &c, maxHandoffCheckpoint) != nil || c.Version != 1 || c.Profile != inspectionProfile || c.Config != s.in.pin || c.Role != s.in.role.Role || c.Serial == 0 || c.Serial > 32 || c.Source != s.in.source.digest || c.Clock.Profile != inspectionProfile || c.Clock.Version != 1 || c.Clock.Provision != s.in.role.Provision || c.Clock.Role != c.Role || c.Clock.Epoch != s.in.role.Epoch || !c.Clock.Confident || c.Clock.UTC <= 0 || c.Clock.Revision == 0 || c.Pending != "" && !hashOK(c.Pending) || c.Historical && c.Pending != "" {
		return c, errors.New("invalid private inspection checkpoint")
	}
	if len(c.Trust) > 0 {
		var t TrustFixtureSnapshot
		if StrictJSON(c.Trust, &t) != nil || t.SeedSHA256 != s.in.role.SeedSHA || t.ObservedAt > c.Clock.UTC {
			return c, errors.New("checkpoint trust binding mismatch")
		}
	}
	return c, nil
}
func (s *inspectionStore) head(f *FixtureTrustStore) (inspectionHead, error) {
	var h inspectionHead
	b, e := inspectionRead(f.root, "head.json", os.Geteuid(), 0600, 4096)
	if e != nil {
		return h, e
	}
	if StrictJSON(b, &h) != nil || h.Version != 1 || h.Profile != inspectionProfile || h.Config != s.in.pin || h.Role != s.in.role.Role || !hashOK(h.SHA) {
		return h, errors.New("foreign inspection head")
	}
	return h, nil
}
func inspectionNamespace(f *FixtureTrustStore) error {
	names, e := inspectionNames(f.root)
	if e != nil {
		return e
	}
	if len(names) >= 256 {
		return errors.New("inspection namespace exhausted")
	}
	for _, n := range names {
		valid := n == "lock" || n == "head.json" || len(n) == 69 && hashOK(n[:64]) && strings.HasSuffix(n, ".json")
		if !valid {
			for _, p := range []string{"snapshot-", "head-"} {
				if strings.HasPrefix(n, p) && strings.HasSuffix(n, ".tmp") && hashOK(strings.TrimSuffix(strings.TrimPrefix(n, p), ".tmp")) {
					valid = true
				}
			}
		}
		if !valid {
			return errors.New("foreign inspection namespace entry")
		}
		st, e := f.root.Lstat(n)
		if e != nil || !inspectionFileIdentity(st, os.Geteuid(), false, 0600) {
			return errors.New("unsafe private namespace entry")
		}
	}
	return nil
}
func (s *inspectionStore) load() ([]byte, error) {
	for _, f := range []*FixtureTrustStore{s.state, s.anchor} {
		if e := inspectionNamespace(f); e != nil {
			return nil, e
		}
	}
	h, e := s.head(s.state)
	if e != nil {
		return nil, e
	}
	a, e := s.head(s.anchor)
	if e != nil || h != a {
		return nil, errors.New("checkpoint/anchor gap; hold")
	}
	b, e := inspectionRead(s.state.root, h.SHA+".json", os.Geteuid(), 0600, maxHandoffCheckpoint)
	if e != nil || digest(b) != h.SHA {
		return nil, errors.New("missing or corrupt checkpoint")
	}
	_, e = s.parse(b)
	return b, e
}
func inspectionLock(f *FixtureTrustStore) (*os.File, error) {
	flags := os.O_RDWR
	st, e := f.root.Lstat("lock")
	if os.IsNotExist(e) {
		flags |= os.O_CREATE | os.O_EXCL
	} else if e != nil || !inspectionFileIdentity(st, os.Geteuid(), false, 0600) {
		return nil, errors.New("unsafe inspection lock")
	}
	l, e := f.root.OpenFile("lock", flags, 0600)
	if e != nil {
		return nil, e
	}
	a, e := l.Stat()
	b, be := f.root.Lstat("lock")
	if e != nil || be != nil || !os.SameFile(a, b) || !inspectionFileIdentity(a, os.Geteuid(), false, 0600) {
		l.Close()
		return nil, errors.New("inspection lock changed")
	}
	if e = lockFixtureTrust(l); e != nil {
		l.Close()
		return nil, e
	}
	return l, nil
}
func (s *inspectionStore) commit(p inspectionPublication) error {
	c, e := s.parse(p.next)
	if e != nil {
		return e
	}
	if len(p.previous) > 0 {
		old, e := s.parse(p.previous)
		if e != nil || old.Serial >= 32 || c.Serial != old.Serial+1 || c.Clock.UTC < old.Clock.UTC || c.Clock.Revision < old.Clock.Revision {
			return errors.New("checkpoint counter/clock regression")
		}
	} else if c.Serial != 1 {
		return errors.New("initial checkpoint serial required")
	}
	// Fixed state -> anchor lock order. Role trees are never shared.
	locks := []*os.File{}
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			unlockFixtureTrust(locks[i])
			locks[i].Close()
		}
	}()
	for _, f := range []*FixtureTrustStore{s.state, s.anchor} {
		if e = inspectionNamespace(f); e != nil {
			return e
		}
		l, e := inspectionLock(f)
		if e != nil {
			return e
		}
		locks = append(locks, l)
	}
	nextSHA := digest(p.next)
	previousSHA := ""
	if len(p.previous) > 0 {
		previousSHA = digest(p.previous)
	}
	h, he := s.head(s.state)
	a, ae := s.head(s.anchor)
	if he == nil && ae == nil && h == a && h.SHA == nextSHA {
		b, e := s.load()
		if e != nil || !bytes.Equal(b, p.next) {
			return errors.New("retry snapshot conflict")
		}
		return nil
	}
	if previousSHA == "" {
		if (!os.IsNotExist(he) && (he != nil || h.SHA != nextSHA)) || !os.IsNotExist(ae) {
			return errors.New("one-time empty initialization required")
		}
		for _, f := range []*FixtureTrustStore{s.state, s.anchor} {
			names, _ := inspectionNames(f.root)
			for _, n := range names {
				if n != "lock" && !(f == s.state && n == "head.json") && n != nextSHA+".json" && n != "snapshot-"+nextSHA+".tmp" && n != "head-"+nextSHA+".tmp" {
					return errors.New("nonempty namespace cannot initialize")
				}
			}
		}
	} else {
		// Only this exact opaque publication can close a head/anchor gap.
		if he != nil || ae != nil || (h.SHA != previousSHA && h.SHA != nextSHA) || a.SHA != previousSHA {
			return errors.New("stale private checkpoint CAS or gap")
		}
		b, e := inspectionRead(s.state.root, previousSHA+".json", os.Geteuid(), 0600, maxHandoffCheckpoint)
		if e != nil || !bytes.Equal(b, p.previous) {
			return errors.New("prior checkpoint conflict")
		}
	}
	name := nextSHA + ".json"
	if b, e := inspectionRead(s.state.root, name, os.Geteuid(), 0600, maxHandoffCheckpoint); e == nil {
		if !bytes.Equal(b, p.next) {
			return errors.New("immutable checkpoint conflict")
		}
	} else if os.IsNotExist(e) {
		tmp := "snapshot-" + nextSHA + ".tmp"
		if e = s.state.temporary(tmp, p.next, "snapshot"); e != nil {
			return e
		}
		if e = s.state.root.Rename(tmp, name); e != nil {
			return e
		}
		if e = s.state.sync(); e != nil {
			return e
		}
	} else {
		return e
	}
	hb := mustInspectionJSON(inspectionHead{1, inspectionProfile, s.in.pin, s.in.role.Role, nextSHA})
	for _, f := range []*FixtureTrustStore{s.state, s.anchor} {
		tmp := "head-" + nextSHA + ".tmp"
		if e = f.temporary(tmp, hb, "head"); e != nil {
			return e
		}
		if e = f.root.Rename(tmp, "head.json"); e != nil {
			return e
		}
		if e = f.sync(); e != nil {
			return e
		}
		if e = f.checkpoint("published"); e != nil {
			return e
		}
	}
	return nil
}

type inspectionSession struct {
	store     *inspectionStore
	previous  []byte
	pending   *inspectionPublication
	token     string
	deadline  time.Time
	monotonic func() time.Time
}
type inspectionResult struct {
	FixtureOnly                                                                     bool
	Historical                                                                      bool
	Admission                                                                       ReleaseAdmissionReport
	InstallAllowed, ResumeAllowed, UpdaterEnabled, WalletSigner, FinancialExecution bool
}

func newInspectionSession(s *inspectionStore, initial bool) (*inspectionSession, error) {
	x := &inspectionSession{store: s, monotonic: time.Now}
	if initial {
		for _, f := range []*FixtureTrustStore{s.state, s.anchor} {
			n, e := inspectionNames(f.root)
			if e != nil || len(n) != 0 {
				return nil, errors.New("explicit empty namespace initialization required")
			}
		}
	} else {
		b, e := s.load()
		if e != nil {
			return nil, e
		}
		c, e := s.parse(b)
		if e != nil || c.Pending != "" {
			return nil, errors.New("interrupted verification held; no reset")
		}
		x.previous = b
	}
	return x, nil
}
func (x *inspectionSession) publish(p inspectionPublication) error {
	x.pending = &p
	if e := x.store.commit(p); e != nil {
		return e
	}
	x.previous = append([]byte(nil), p.next...)
	x.pending = nil
	return nil
}
func (x *inspectionSession) retryPublication() error {
	if x.pending == nil || !x.monotonic().Before(x.deadline) {
		return errors.New("no unexpired exact publication retry")
	}
	return x.publish(*x.pending)
}
func (x *inspectionSession) inspect(ctx context.Context) (inspectionResult, error) {
	result := inspectionResult{FixtureOnly: true, Historical: true}
	if x.pending != nil {
		return result, errors.New("publication pending; exact retry only")
	}
	in := x.store.in
	// Re-read the entire sealed catalog/source, independently for each role.
	fresh, e := openInspectionInputs(in.directory, in.pin, in.role.Role, in.publisher)
	if e != nil {
		return result, e
	}
	if fresh.source.digest != in.source.digest {
		return result, errors.New("offline source changed")
	}
	clock, e := fresh.clock()
	if e != nil {
		return result, e
	}
	c := inspectionCheckpoint{Version: 1, Profile: inspectionProfile, Config: in.pin, Role: in.role.Role, Serial: 1, Clock: clock, Source: in.source.digest}
	if len(x.previous) > 0 {
		old, e := x.store.parse(x.previous)
		if e != nil || old.Pending != "" || old.Serial > 30 || clock.UTC < old.Clock.UTC || clock.Revision < old.Clock.Revision || clock.Revision == old.Clock.Revision && clock.UTC != old.Clock.UTC {
			return result, errors.New("clock, pending or record-exhaustion hold")
		}
		c.Serial = old.Serial + 1
		c.Trust = old.Trust
	}
	x.deadline = x.monotonic().Add(60 * time.Second)
	x.token = digest([]byte(in.pin + in.role.Role + fresh.source.digest + string(mustInspectionJSON(clock))))
	c.Pending = x.token
	if e = x.publish(inspectionPublication{x.previous, mustInspectionJSON(c)}); e != nil {
		return result, e
	}
	trust := TrustRehearsalOptions{Development: true, Initial: len(c.Trust) == 0, Seed: fresh.seed, SeedSHA256: in.role.SeedSHA, Previous: c.Trust, Now: clock.UTC}
	if len(c.Trust) > 0 {
		trust.PreviousSHA256 = digest(c.Trust)
	}
	opts := ReleaseAdmissionOptions{Policy: fresh.policy, PolicySHA256: in.role.PolicySHA, Evidence: fresh.source.files["evidence.json"], ArtifactDir: in.directory}
	transition, report, verifyErr := admitFixtureRelease(ctx, trust, opts, fresh.source)
	// Never clear a pending checkpoint after uncertainty/deadline/time changes.
	finalInputs, ce := openInspectionInputs(in.directory, in.pin, in.role.Role, in.publisher)
	var finalClock inspectionClock
	if ce == nil {
		finalClock, ce = finalInputs.clock()
	}
	if ce != nil || finalClock != clock || !x.monotonic().Before(x.deadline) {
		return result, errors.New("clock/deadline changed; verification held")
	}
	if len(transition.next) > 0 {
		c.Trust = transition.next
	}
	c.Serial++
	c.Pending = ""
	c.Historical = verifyErr == nil
	if e = x.publish(inspectionPublication{x.previous, mustInspectionJSON(c)}); e != nil {
		return result, e
	}
	result = inspectionResult{FixtureOnly: true, Historical: true, Admission: report}
	return result, verifyErr
}

// Kept here to avoid any consumer mistaking map iteration for index canonicality.
func sortedInspectionFiles(m map[string][]byte) []inspectionFile {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	files := make([]inspectionFile, 0, len(m))
	for _, n := range names {
		files = append(files, inspectionFile{n, int64(len(m[n])), digest(m[n])})
	}
	return files
}

// Peer observation tolerance never extends either role's independent expiry.
func inspectionClocksAgree(owner, signer inspectionClock) bool {
	valid := func(c inspectionClock, role string) bool {
		return c.Version == 1 && c.Profile == inspectionProfile && c.Role == role && IDOK(c.Provision) && IDOK(c.Epoch) && c.Revision > 0 && c.UTC > 0 && c.UTC <= 4102444800 && c.Confident
	}
	return valid(owner, "owner") && valid(signer, "signer") && owner.Provision == signer.Provision && owner.UTC-signer.UTC <= 30 && signer.UTC-owner.UTC <= 30
}
