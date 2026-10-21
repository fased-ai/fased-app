package update

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"sync"
)

const HandoffProfile = "fased-app-owner-signer-admission-fixture:v1"
const handoffMode = "inspection-only"
const handoffCapability = "fixture-release-handoff-v1"

type FixtureHandoffInventory struct {
	InstanceID      string `json:"instanceId"`
	AuthoritySHA256 string `json:"authoritySha256"`
	CanarySHA256    string `json:"canarySha256"`
	Revision        uint64 `json:"revision,string"`
	InstalledFloor  uint64 `json:"installedFloor,string"`
	ReservedFloor   uint64 `json:"reservedFloor,string"`
	ActiveIntentID  string `json:"activeIntentId"`
}

func (i FixtureHandoffInventory) valid() bool {
	return IDOK(i.InstanceID) && hashOK(i.AuthoritySHA256) && hashOK(i.CanarySHA256) && i.Revision > 0 && i.ReservedFloor >= i.InstalledFloor && (i.ActiveIntentID == "" || IDOK(i.ActiveIntentID))
}

// All callbacks and pins are harness-owned constructor inputs, never wire data.
// This models independent actors; it does not authenticate kernel UIDs or expose
// an installed owner/signer API.
type FixtureHandoffConfig struct {
	Development   bool
	Role          string
	Store         *HandoffFixtureStore
	Initial       bool
	ExpectedHead  string
	Trust         TrustRehearsalOptions
	Admission     ReleaseAdmissionOptions
	Clock         func() (int64, bool)
	Inventory     func() FixtureHandoffInventory
	Review        func(FixtureHandoffReview) bool
	checkAnchor   func(string) error // Transport-owned, never configurable over RPC.
	advanceAnchor func(string, string) error
}

type FixtureHandoffReview struct {
	RequestDigest   string
	AuthorizationID string
	UnitID          string
	Sequence        uint64
	Target          Target
	Deadline        int64
	Identity        FixtureBuildIdentity
	Subjects        []FixtureReleaseSubject
}

type FixtureHandoffActor struct {
	mu                                  sync.Mutex
	cfg                                 FixtureHandoffConfig
	configSHA, transportSHA, boot, head string
	binding                             FixtureBinding
	retry                               *handoffPublication
}

func hashTuple(domain string, values ...string) string {
	h := sha256.New()
	h.Write([]byte(domain + "\x00"))
	var length [4]byte
	for _, v := range values {
		binary.BigEndian.PutUint32(length[:], uint32(len(v)))
		h.Write(length[:])
		h.Write([]byte(v))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func handoffTransport(seed TrustFixtureSeed, ca []byte) string {
	return hashTuple("fased-app-fixture-transport:v1", seed.Origin, digest(ca), "no-redirect:tls-verify:bounded-v1")
}

func NewFixtureHandoffActor(c FixtureHandoffConfig) (*FixtureHandoffActor, error) {
	if !c.Development || !c.Trust.Development || c.Role != "owner" && c.Role != "signer" || c.Store == nil || c.Clock == nil || c.Inventory == nil || c.Trust.Initial || len(c.Trust.Previous) != 0 || c.Trust.PreviousSHA256 != "" || c.Trust.Now != 0 || c.Role == "owner" && c.Review == nil {
		return nil, errors.New("explicit independent fixture actor inputs required")
	}
	now, valid := c.Clock()
	var seed TrustFixtureSeed
	if !valid || now <= 0 || now > 4102444800 || digest(c.Trust.Seed) != c.Trust.SeedSHA256 || StrictJSON(c.Trust.Seed, &seed) != nil || seed.Version != 1 || !seed.FixtureBinding.valid() || seed.CreatedAt > now || seed.CreatedAt <= 0 || !hashOK(seed.PreserveSHA256) {
		return nil, errors.New("pinned fixture seed and confident role-local time required")
	}
	u, e := acquisitionURL(seed.Origin)
	if e != nil || u.Path != "" {
		return nil, errors.New("controlled fixture origin required")
	}
	if _, e := fixtureRoot(seed.Root, seed.FixtureBinding); e != nil {
		return nil, e
	}
	policy, e := parseAdmissionPolicy(c.Admission, now)
	inv := c.Inventory()
	if e != nil || policy.FixtureBinding != seed.FixtureBinding || !inv.valid() || inv.InstalledFloor != seed.InstalledFloor || inv.ReservedFloor != seed.ReservedFloor {
		return nil, errors.New("independent policy/fixture inventory mismatch")
	}
	// Freeze public material; callbacks remain explicitly harness-owned.
	c.Trust.Seed = append([]byte(nil), c.Trust.Seed...)
	c.Trust.TLSCA = append([]byte(nil), c.Trust.TLSCA...)
	c.Admission.Policy = append([]byte(nil), c.Admission.Policy...)
	c.Admission.Evidence = append([]byte(nil), c.Admission.Evidence...)
	a := &FixtureHandoffActor{cfg: c, boot: NewProbe(), binding: seed.FixtureBinding}
	a.transportSHA = handoffTransport(seed, c.Trust.TLSCA)
	a.configSHA = hashTuple(HandoffProfile+":config", c.Role, c.Trust.SeedSHA256, c.Admission.PolicySHA256, a.transportSHA)
	if c.Initial {
		if c.ExpectedHead != "" {
			return nil, errors.New("initial mode cannot adopt a head")
		}
		state := handoffCheckpoint{Version: 1, Profile: HandoffProfile, Mode: handoffMode, Binding: a.binding, Role: c.Role, BootID: a.boot, ConfigSHA: a.configSHA, SeedSHA: c.Trust.SeedSHA256, Revision: 1, LastAt: now, Inventory: inv}
		next, _ := json.Marshal(state)
		a.head, e = c.Store.commit(handoffPublication{next: next})
	} else {
		a.head = c.ExpectedHead
		var state handoffCheckpoint
		_, state, e = a.load()
		if e == nil && (state.Inventory != inv || now < state.LastAt) {
			e = errors.New("restored fixture identity/time conflict")
		}
		if e == nil {
			state.BootID, state.LastAt = a.boot, now
			if state.Pending == "" {
				state.Active = ""
			}
			e = a.publish(state, nil)
		}
	}
	if e == nil && c.Initial && c.advanceAnchor != nil {
		e = c.advanceAnchor("", a.head)
	}
	if e != nil {
		return nil, e
	}
	return a, nil
}

func (a *FixtureHandoffActor) Head() string { a.mu.Lock(); defer a.mu.Unlock(); return a.head }

func (a *FixtureHandoffActor) load() ([]byte, handoffCheckpoint, error) {
	var c handoffCheckpoint
	if a.cfg.checkAnchor != nil {
		if e := a.cfg.checkAnchor(a.head); e != nil {
			return nil, c, e
		}
	}
	b, e := a.cfg.Store.Load(a.configSHA, a.head)
	if e != nil {
		return nil, c, e
	}
	c, e = parseHandoffCheckpoint(b)
	return b, c, e
}

func (a *FixtureHandoffActor) current(channel string) (handoffCheckpoint, int64, error) {
	_, c, e := a.load()
	if e != nil {
		return c, 0, e
	}
	now, valid := a.cfg.Clock()
	if !valid || now < c.LastAt || now <= 0 || now > 4102444800 || c.BootID != a.boot || a.cfg.Inventory() != c.Inventory || c.Inventory.ActiveIntentID != "" || c.Pending != "" || a.retry != nil || c.Active != "" && c.Active != channel {
		return c, 0, errors.New("fixture time, context, pending learning or active-session hold")
	}
	return c, now, nil
}

func putHandoffRecord(c *handoffCheckpoint, r handoffRecord) error {
	b, _ := json.Marshal(r)
	if len(b) > 4096 {
		return errors.New("fixture record budget reached")
	}
	for i, old := range c.Records {
		if old.ID == r.ID {
			c.Records[i] = r
			return nil
		}
	}
	if len(c.Records) >= 32 {
		return errors.New("fixture replay budget reached; preserve history")
	}
	c.Records = append(c.Records, r)
	return nil
}

func (a *FixtureHandoffActor) publish(c handoffCheckpoint, proof *TrustTransition) error {
	before, old, e := a.load()
	if e != nil {
		return e
	}
	if old.Revision == math.MaxUint64 {
		return errors.New("fixture revision exhausted")
	}
	c.Revision = old.Revision + 1
	next, e := json.Marshal(c)
	if e != nil {
		return e
	}
	t := handoffPublication{previous: before, next: next, proof: proof}
	head, e := a.cfg.Store.commit(t)
	if e == nil && a.cfg.advanceAnchor != nil {
		e = a.cfg.advanceAnchor(a.head, head)
	}
	if e != nil {
		a.retry = &t
		return e
	}
	a.head = head
	return nil
}

// Retry only the exact in-memory publication after an interrupted write/reply.
// It cannot clear an unknown verification marker after process restart.
func (a *FixtureHandoffActor) RetryPublication() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.retry == nil {
		return errors.New("no opaque pending publication")
	}
	head, e := a.cfg.Store.commit(*a.retry)
	if e == nil && a.cfg.advanceAnchor != nil {
		e = a.cfg.advanceAnchor(a.head, head)
	}
	if e == nil {
		a.head = head
		a.retry = nil
	}
	return e
}

type handoffInspection struct {
	authorization, unit string
	sequence            uint64
	target              Target
	at, expiry          int64
	release             FixtureReleaseUnit
}

func (a *FixtureHandoffActor) inspect(ctx context.Context, channel string) (out handoffInspection, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, now, e := a.current(channel)
	if e != nil {
		return out, e
	}
	attempt := NewProbe()
	c.Active, c.Pending, c.LastAt = channel, attempt, now
	if e = putHandoffRecord(&c, handoffRecord{ID: attempt, Channel: channel, Kind: "verification-pending", Body: json.RawMessage(`{}`)}); e != nil {
		return out, e
	}
	if e = a.publish(c, nil); e != nil {
		return out, e
	}
	o := a.cfg.Trust
	o.Now = now
	o.Initial = len(c.Trust) == 0
	o.Previous = append([]byte(nil), c.Trust...)
	o.PreviousSHA256 = c.TrustSHA
	transition, r, verifyErr := AdmitFixtureRelease(ctx, o, a.cfg.Admission)
	// From here, an unrecorded result leaves the durable marker unresolved.
	if len(transition.next) > 0 {
		if !bytesEqual(transition.previous, c.Trust) {
			return out, errors.New("verifier prior fixture state mismatch")
		}
		c.Trust = append(json.RawMessage(nil), transition.next...)
		c.TrustSHA = digest(c.Trust)
	}
	c.Pending = ""
	kind := "verified"
	if verifyErr != nil {
		kind = "rejected"
		c.Active = ""
	}
	if e = putHandoffRecord(&c, handoffRecord{ID: attempt, Channel: channel, Kind: kind, Body: json.RawMessage(`{}`)}); e != nil {
		return out, e
	}
	var proof *TrustTransition
	if len(transition.next) > 0 {
		proof = &transition
	}
	if e = a.publish(c, proof); e != nil {
		return out, errors.Join(verifyErr, e)
	}
	if verifyErr != nil {
		return out, verifyErr
	}
	var state TrustFixtureSnapshot
	_ = StrictJSON(c.Trust, &state)
	chain, d, e := verifyFixtureSet(state)
	if e != nil || !r.ReleaseUnitVerified {
		return out, errors.New("missing complete fixture admission")
	}
	p, e := parseAdmissionPolicy(a.cfg.Admission, now)
	if e != nil {
		return out, e
	}
	u, e := verifyReleaseEvidence(a.cfg.Admission, p, state, now)
	if e != nil {
		return out, e
	}
	expiry := min(chain.Root.Signed.Expires.Unix(), chain.Timestamp.Signed.Expires.Unix(), chain.Snapshot.Signed.Expires.Unix(), chain.Targets["targets"].Signed.Expires.Unix(), p.NotAfter)
	auth := hashTuple("fased-app-fixture-handoff-authorization:v1", a.cfg.Trust.SeedSHA256, a.cfg.Admission.PolicySHA256, a.transportSHA, a.binding.Repository, a.binding.Channel, u.Target.OS, u.Target.Arch, strconv.FormatUint(d.Sequence, 10), digest(state.Root), digest(state.Timestamp), digest(state.Snapshot), digest(state.Targets), digest(state.Descriptor), digest(a.cfg.Admission.Evidence), r.UnitSHA256)
	return handoffInspection{auth, r.UnitSHA256, d.Sequence, u.Target, now, expiry, u}, nil
}

func bytesEqual(a, b []byte) bool { return string(a) == string(b) }

func handoffInventoryDigest(i FixtureHandoffInventory) string {
	return hashTuple(HandoffProfile+":inventory", i.InstanceID, i.AuthoritySHA256, i.CanarySHA256, strconv.FormatUint(i.InstalledFloor, 10), strconv.FormatUint(i.ReservedFloor, 10), i.ActiveIntentID)
}

type FixtureHandoffChallenge struct {
	FixtureBinding
	Version         int                     `json:"version"`
	Profile         string                  `json:"profile"`
	Mode            string                  `json:"mode"`
	Capability      string                  `json:"capability"`
	ID              string                  `json:"id"`
	ChannelID       string                  `json:"channelId"`
	SignerBootID    string                  `json:"signerBootId"`
	Inventory       FixtureHandoffInventory `json:"inventory"`
	HeadSHA256      string                  `json:"headSha256"`
	SeedSHA256      string                  `json:"seedSha256"`
	PolicySHA256    string                  `json:"policySha256"`
	TransportSHA256 string                  `json:"transportSha256"`
	ObservedAt      int64                   `json:"observedAt"`
	Deadline        int64                   `json:"deadline"`
}

type FixtureHandoffRequest struct {
	Challenge       FixtureHandoffChallenge `json:"challenge"`
	ID              string                  `json:"id"`
	OwnerBootID     string                  `json:"ownerBootId"`
	Inventory       FixtureHandoffInventory `json:"inventory"`
	OwnerHeadSHA256 string                  `json:"ownerHeadSha256"`
	AuthorizationID string                  `json:"authorizationId"`
	UnitID          string                  `json:"unitId"`
	Target          Target                  `json:"target"`
	Sequence        uint64                  `json:"sequence,string"`
	ObservedAt      int64                   `json:"observedAt"`
	ReviewedAt      int64                   `json:"reviewedAt"`
}

func (r FixtureHandoffRequest) requestDigest() string {
	c := r.Challenge
	return hashTuple("fased-app-fixture-handoff-request:v1", c.Profile, c.Mode, c.Repository, c.Channel, c.ID, r.ID, r.OwnerBootID, c.SignerBootID, r.Inventory.InstanceID, r.Inventory.AuthoritySHA256, handoffInventoryDigest(r.Inventory), strconv.FormatUint(r.Inventory.Revision, 10), handoffInventoryDigest(c.Inventory), strconv.FormatUint(c.Inventory.Revision, 10), r.OwnerHeadSHA256, c.HeadSHA256, r.AuthorizationID, r.UnitID, r.Target.OS, r.Target.Arch, strconv.FormatUint(r.Sequence, 10), strconv.FormatInt(r.ObservedAt, 10), strconv.FormatInt(c.ObservedAt, 10), strconv.FormatInt(r.ReviewedAt, 10), strconv.FormatInt(c.Deadline, 10))
}

type FixtureHandoffOutcome struct {
	Version            int    `json:"version"`
	Profile            string `json:"profile"`
	Mode               string `json:"mode"`
	FixtureOnly        bool   `json:"fixtureOnly"`
	RequestDigest      string `json:"requestDigest"`
	RequestID          string `json:"requestId"`
	ChallengeID        string `json:"challengeId"`
	AuthorizationID    string `json:"authorizationId"`
	UnitID             string `json:"unitId"`
	SignerBootID       string `json:"signerBootId"`
	AuthoritySHA256    string `json:"authoritySha256"`
	BaselineHead       string `json:"baselineHead"`
	InventoryRevision  uint64 `json:"inventoryRevision,string"`
	Sequence           uint64 `json:"sequence,string"`
	ObservedAt         int64  `json:"observedAt"`
	Deadline           int64  `json:"deadline"`
	Outcome            string `json:"outcome"`
	InstallAllowed     bool   `json:"installAllowed"`
	ResumeAllowed      bool   `json:"resumeAllowed"`
	UpdaterEnabled     bool   `json:"updaterEnabled"`
	WalletSigner       bool   `json:"walletSigner"`
	FinancialExecution bool   `json:"financialExecution"`
}

// An in-process model capability, not a signed/OS-authenticated bearer token.
type FixtureHandoffReceipt struct {
	session *FixtureHandoffSession
	body    FixtureHandoffOutcome
	head    string
}

func (r FixtureHandoffReceipt) Report() FixtureHandoffOutcome { return r.body }

type FixtureHandoffSession struct {
	mu                        sync.Mutex
	owner, signer             *FixtureHandoffActor
	id, ownerBoot, signerBoot string
	challenge                 FixtureHandoffChallenge
	request                   FixtureHandoffRequest
	reviewDigest              string
	ownerExpiry               int64
	receipt                   *FixtureHandoffReceipt
	finished                  bool
	failed                    bool
}

// This constructor models an authenticated channel with opaque actor references.
// It makes no claim about kernel peer credentials; there is no socket/CLI route.
func matchingFixtureHandoffActors(owner, signer *FixtureHandoffActor) error {
	if owner == nil || signer == nil || owner == signer || owner.cfg.Role != "owner" || signer.cfg.Role != "signer" {
		return errors.New("independent owner/signer model actors required")
	}
	a, e := owner.cfg.Store.fs.root.Stat(".")
	b, be := signer.cfg.Store.fs.root.Stat(".")
	if e != nil || be != nil || os.SameFile(a, b) || owner.binding != signer.binding || owner.cfg.Trust.SeedSHA256 != signer.cfg.Trust.SeedSHA256 || owner.cfg.Admission.PolicySHA256 != signer.cfg.Admission.PolicySHA256 || owner.transportSHA != signer.transportSHA {
		return errors.New("independent matching pinned fixture stores required")
	}
	return nil
}
func NewFixtureHandoffSession(owner, signer *FixtureHandoffActor) (*FixtureHandoffSession, error) {
	if e := matchingFixtureHandoffActors(owner, signer); e != nil {
		return nil, e
	}
	for _, a := range []*FixtureHandoffActor{owner, signer} {
		a.mu.Lock()
		_, cp, e := a.load()
		a.mu.Unlock()
		if e != nil {
			return nil, e
		}
		for _, r := range cp.Records {
			if r.Kind == "installed-admission-model" {
				return nil, errors.New("non-installed admission model namespace cannot become a legacy handoff session")
			}
		}
	}
	return &FixtureHandoffSession{owner: owner, signer: signer, id: NewProbe(), ownerBoot: owner.boot, signerBoot: signer.boot}, nil
}

func (s *FixtureHandoffSession) live() error {
	if s.owner == nil || s.signer == nil || s.failed || s.owner.boot != s.ownerBoot || s.signer.boot != s.signerBoot {
		return errors.New("fixture channel failed or boot changed")
	}
	return nil
}

func (s *FixtureHandoffSession) issueChallenge() (FixtureHandoffChallenge, error) {
	a := s.signer
	a.mu.Lock()
	defer a.mu.Unlock()
	c, now, e := a.current(s.id)
	if e != nil {
		return FixtureHandoffChallenge{}, e
	}
	p, e := parseAdmissionPolicy(a.cfg.Admission, now)
	if e != nil {
		return FixtureHandoffChallenge{}, e
	}
	ch := FixtureHandoffChallenge{FixtureBinding: a.binding, Version: 1, Profile: HandoffProfile, Mode: handoffMode, Capability: handoffCapability, ID: NewProbe(), ChannelID: s.id, SignerBootID: a.boot, Inventory: c.Inventory, SeedSHA256: a.cfg.Trust.SeedSHA256, PolicySHA256: a.cfg.Admission.PolicySHA256, TransportSHA256: a.transportSHA, ObservedAt: now, Deadline: min(now+60, p.NotAfter)}
	b, _ := json.Marshal(ch)
	c.Active, c.LastAt = s.id, now
	if e = putHandoffRecord(&c, handoffRecord{ID: ch.ID, Channel: s.id, Kind: "challenge", Body: b}); e != nil {
		return ch, e
	}
	if e = a.publish(c, nil); e != nil {
		return ch, e
	}
	ch.HeadSHA256 = a.head // Publication wrapper is outside the stored payload.
	return ch, nil
}

func (s *FixtureHandoffSession) Prepare(ctx context.Context) (wire []byte, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.reject()
		}
	}()
	if e := s.live(); e != nil {
		return nil, e
	}
	if s.reviewDigest != "" || s.challenge.ID != "" {
		return nil, errors.New("fresh review requires a fresh session")
	}
	i, e := s.owner.inspect(ctx, s.id)
	if e != nil {
		return nil, e
	}
	ch, e := s.issueChallenge()
	if e != nil {
		return nil, e
	}
	s.challenge = ch
	a := s.owner
	a.mu.Lock()
	defer a.mu.Unlock()
	c, now, e := a.current(s.id)
	if e != nil {
		return nil, e
	}
	if c.Inventory.InstanceID != ch.Inventory.InstanceID || c.Inventory.AuthoritySHA256 != ch.Inventory.AuthoritySHA256 || abs64(i.at-ch.ObservedAt) > 30 || now >= ch.Deadline || now >= i.expiry {
		return nil, errors.New("fixture identity, skew or expiry conflict")
	}
	r := FixtureHandoffRequest{Challenge: ch, ID: NewProbe(), OwnerBootID: a.boot, Inventory: c.Inventory, OwnerHeadSHA256: a.head, AuthorizationID: i.authorization, UnitID: i.unit, Target: i.target, Sequence: i.sequence, ObservedAt: i.at, ReviewedAt: now}
	d := r.requestDigest()
	review := FixtureHandoffReview{d, i.authorization, i.unit, i.sequence, i.target, min(ch.Deadline, i.expiry, now+3600), i.release.Identity, append([]FixtureReleaseSubject(nil), i.release.Subjects...)}
	if !a.cfg.Review(review) {
		return nil, errors.New("trusted fixture review refused")
	}
	b, _ := json.Marshal(r)
	if e = putHandoffRecord(&c, handoffRecord{ID: r.ID, Channel: s.id, Kind: "reviewed", Body: b}); e != nil {
		return nil, e
	}
	c.LastAt = now
	if e = a.publish(c, nil); e != nil {
		return nil, e
	}
	s.request, s.reviewDigest, s.ownerExpiry = r, d, i.expiry
	return append([]byte(nil), b...), nil
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func (s *FixtureHandoffSession) Check(ctx context.Context, wire []byte) (out FixtureHandoffReceipt, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.reject()
		}
	}()
	var zero FixtureHandoffReceipt
	if e := s.live(); e != nil {
		return zero, e
	}
	var r FixtureHandoffRequest
	if StrictJSON(wire, &r) != nil || s.reviewDigest == "" || r != s.request || r.requestDigest() != s.reviewDigest || r.Challenge != s.challenge || r.Challenge.Profile != HandoffProfile || r.Challenge.Mode != handoffMode || r.Challenge.Capability != handoffCapability {
		return zero, errors.New("unreviewed, malformed or substituted fixture request")
	}
	if s.receipt != nil {
		if e := s.receiptLive(*s.receipt); e != nil {
			return zero, e
		}
		return *s.receipt, nil
	}
	a := s.signer
	a.mu.Lock()
	c, now, e := a.current(s.id)
	if e == nil && (a.head != r.Challenge.HeadSHA256 || c.Inventory != r.Challenge.Inventory || now >= r.Challenge.Deadline || now-r.ReviewedAt > 3600 || r.ReviewedAt > now+30 || abs64(now-r.ObservedAt) > 30) {
		e = errors.New("stale fixture challenge, review, skew or inventory")
	}
	a.mu.Unlock()
	if e != nil {
		return zero, e
	}
	i, e := a.inspect(ctx, s.id)
	if e != nil {
		return zero, e
	}
	if i.authorization != r.AuthorizationID || i.unit != r.UnitID || i.sequence != r.Sequence || i.target != r.Target {
		return zero, errors.New("independent signer authorization differs")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c, now, e = a.current(s.id)
	if e != nil {
		return zero, e
	}
	deadline := min(r.Challenge.Deadline, s.ownerExpiry, i.expiry, r.ReviewedAt+3600)
	if now >= deadline {
		return zero, errors.New("fixture authorization expired before result")
	}
	body := FixtureHandoffOutcome{Version: 1, Profile: HandoffProfile, Mode: handoffMode, FixtureOnly: true, RequestDigest: s.reviewDigest, RequestID: r.ID, ChallengeID: r.Challenge.ID, AuthorizationID: i.authorization, UnitID: i.unit, SignerBootID: a.boot, AuthoritySHA256: c.Inventory.AuthoritySHA256, BaselineHead: r.Challenge.HeadSHA256, InventoryRevision: c.Inventory.Revision, Sequence: i.sequence, ObservedAt: now, Deadline: deadline, Outcome: "checked"}
	b, _ := json.Marshal(body)
	if e = putHandoffRecord(&c, handoffRecord{ID: r.ID, Channel: s.id, Kind: "checked", Body: b}); e != nil {
		return zero, e
	}
	c.Active, c.LastAt = "", now
	if e = a.publish(c, nil); e != nil {
		return zero, e
	}
	receipt := FixtureHandoffReceipt{session: s, body: body, head: a.head}
	s.receipt = &receipt
	return receipt, nil
}

func (s *FixtureHandoffSession) receiptLive(r FixtureHandoffReceipt) error {
	if r.session != s || r.body.RequestDigest != s.reviewDigest || r.body.SignerBootID != s.signer.boot {
		return errors.New("public/foreign fixture receipt cannot authorize completion")
	}
	a := s.signer
	a.mu.Lock()
	defer a.mu.Unlock()
	c, now, e := a.current("")
	if e != nil {
		return e
	}
	if now >= r.body.Deadline || c.Inventory.Revision != r.body.InventoryRevision || c.Inventory.AuthoritySHA256 != r.body.AuthoritySHA256 {
		return errors.New("expired/stale fixture receipt")
	}
	b, _ := json.Marshal(r.body)
	for _, v := range c.Records {
		if v.ID == r.body.RequestID && v.Kind == "checked" && bytesEqual(v.Body, b) {
			return nil
		}
	}
	return errors.New("missing durable fixture receipt")
}

func (s *FixtureHandoffSession) Finish(ctx context.Context, r FixtureHandoffReceipt) (out FixtureHandoffOutcome, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if err != nil {
			s.reject()
		}
	}()
	var zero FixtureHandoffOutcome
	if e := s.live(); e != nil {
		return zero, e
	}
	if s.finished {
		return zero, errors.New("fixture inspection already completed; status only")
	}
	if e := s.receiptLive(r); e != nil {
		return zero, e
	}
	i, e := s.owner.inspect(ctx, s.id)
	if e != nil {
		return zero, e
	}
	if i.authorization != r.body.AuthorizationID || i.unit != r.body.UnitID {
		return zero, errors.New("fresh owner authorization changed; review again")
	}
	a := s.owner
	a.mu.Lock()
	defer a.mu.Unlock()
	c, now, e := a.current(s.id)
	if e != nil {
		return zero, e
	}
	if now >= min(r.body.Deadline, i.expiry) || abs64(now-r.body.ObservedAt) > 30 {
		return zero, errors.New("final fixture expiry/skew conflict")
	}
	reviewed := false
	requestBytes, _ := json.Marshal(s.request)
	for _, v := range c.Records {
		if v.ID == s.request.ID && v.Kind == "reviewed" && bytesEqual(v.Body, requestBytes) {
			reviewed = true
		}
	}
	if !reviewed {
		return zero, errors.New("missing protected fixture review")
	}
	body := r.body
	body.Outcome = "inspection-complete"
	body.ObservedAt = now
	body.BaselineHead = s.request.OwnerHeadSHA256
	b, _ := json.Marshal(struct {
		Result        FixtureHandoffOutcome `json:"result"`
		ReceiptSHA256 string                `json:"receiptSha256"`
		SignerHead    string                `json:"signerHead"`
	}{body, digest(mustHandoffJSON(r.body)), r.head})
	if e = putHandoffRecord(&c, handoffRecord{ID: s.request.ID, Channel: s.id, Kind: "inspection-complete", Body: b}); e != nil {
		return zero, e
	}
	c.Active, c.LastAt = "", now
	if e = a.publish(c, nil); e != nil {
		return zero, e
	}
	s.finished = true
	return body, nil
}

func mustHandoffJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}

func (s *FixtureHandoffSession) reject() {
	s.failed = true
	for _, a := range []*FixtureHandoffActor{s.owner, s.signer} {
		if a == nil {
			continue
		}
		a.mu.Lock()
		c, now, e := a.current(s.id)
		if e == nil && c.Active == s.id {
			c.Active, c.LastAt = "", now
			// A terminal marker prevents a rejected challenge from becoming a
			// fresh request. Unknown learning/publication failures remain held.
			b := json.RawMessage(`{"outcome":"rejected"}`)
			if putHandoffRecord(&c, handoffRecord{ID: NewProbe(), Channel: s.id, Kind: "rejected", Body: b}) == nil {
				_ = a.publish(c, nil)
			}
		}
		a.mu.Unlock()
	}
}

// Status is audit data only. No API can turn this JSON into an opaque receipt.
func (a *FixtureHandoffActor) Status(requestID string) (json.RawMessage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !IDOK(requestID) {
		return nil, errors.New("fixture request ID required")
	}
	_, c, e := a.load()
	if e != nil {
		return nil, e
	}
	for _, r := range c.Records {
		if r.ID == requestID {
			return append(json.RawMessage(nil), r.Body...), nil
		}
	}
	return nil, fmt.Errorf("fixture status not found")
}
