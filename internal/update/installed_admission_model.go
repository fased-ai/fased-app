package update

// This file is a non-installed fixture model. Its private types have no CLI,
// RPC, service, wallet or lifecycle-controller consumer. Model identifiers do
// not allocate production wire, lifecycle or schema versions.
import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

const installedAdmissionModelProfile = "fased-app-non-installed-admission-model:v1"
const installedAdmissionModelCapability = "non-installed-model-only"

type admissionModelReader struct {
	Lifecycle  int
	Proposed   bool
	Capability string
	Readable   bool
}
type admissionModelReaders struct {
	Protocol, FinancialFormat int
	TopologySHA               string
	Owner, Signer             admissionModelReader
	Target                    Target
	Held                      bool
}

// These are decisions, not permissions to run any of the classified readers.
func admissionModelCompatibility(v admissionModelReaders) string {
	if v.Target != (Target{"linux", "amd64"}) {
		return "RC-08"
	}
	if v.Held {
		return "RC-07"
	}
	if v.Protocol != 1 || v.FinancialFormat != 1 || !hashOK(v.TopologySHA) {
		return "RC-01"
	}
	valid := func(r admissionModelReader) bool {
		if !r.Readable {
			return false
		}
		if r.Proposed {
			return r.Lifecycle == 0 && r.Capability == installedAdmissionModelCapability
		}
		return r.Lifecycle >= 1 && r.Lifecycle <= 4 && r.Capability == ""
	}
	if !valid(v.Owner) || !valid(v.Signer) {
		return "RC-01"
	}
	if !v.Owner.Proposed && v.Owner.Lifecycle < 3 || !v.Signer.Proposed && v.Signer.Lifecycle < 3 {
		return "RC-01"
	}
	if v.Owner.Proposed != v.Signer.Proposed {
		return "RC-05"
	}
	if v.Owner.Proposed && v.Signer.Proposed {
		return "RC-06"
	}
	if v.Owner.Lifecycle != v.Signer.Lifecycle {
		return "RC-05"
	}
	switch v.Owner.Lifecycle {
	case 3:
		return "RC-02"
	case 4:
		return "RC-03"
	default:
		return "RC-01"
	}
}

// The proposed bridge is a distinct decision from ordinary admission.
func admissionModelBridgeDecision(from, to admissionModelReaders) string {
	if admissionModelCompatibility(from) == "RC-03" && admissionModelCompatibility(to) == "RC-06" && from.Protocol == to.Protocol && from.FinancialFormat == to.FinancialFormat && from.TopologySHA == to.TopologySHA {
		return "RC-04"
	}
	return admissionModelCompatibility(from)
}

type admissionModelIdentity struct {
	FixtureOnly, AuthenticatedSeed             bool
	InstanceID                                 string
	OwnerUID, AppUID, SignerUID                int
	InstallSHA, ConfigSHA, OwnerSHA, SignerSHA string
	SeedSHA, PolicySHA, TransportSHA           string
	Readers                                    admissionModelReaders
}

func (v admissionModelIdentity) valid() bool {
	if !v.FixtureOnly || !v.AuthenticatedSeed || !IDOK(v.InstanceID) || v.OwnerUID <= 0 || v.AppUID <= 0 || v.SignerUID <= 0 || v.OwnerUID == v.AppUID || v.OwnerUID == v.SignerUID || v.AppUID == v.SignerUID {
		return false
	}
	for _, h := range []string{v.InstallSHA, v.ConfigSHA, v.OwnerSHA, v.SignerSHA, v.SeedSHA, v.PolicySHA, v.TransportSHA} {
		if !hashOK(h) {
			return false
		}
	}
	return admissionModelCompatibility(v.Readers) == "RC-06"
}
func (v admissionModelIdentity) digest() string {
	return SnapshotDigest(installedAdmissionModelProfile+":identity", v)
}

type admissionModelRole struct {
	actor    *FixtureHandoffActor
	identity func() admissionModelIdentity // Trusted, independent fixture input, never JSON/RPC.
	baseline admissionModelIdentity
	head     string
}
type admissionModelRecord struct {
	Profile, Binding, Phase, Exchange string
	Request, Authorization, Unit      string
	Deadline                          int64
}
type admissionModelRequest struct {
	Profile, Capability, Channel, OwnerBoot, SignerBoot             string
	Binding, OwnerInventory, SignerInventory, OwnerHead, SignerHead string
	Authorization, Unit                                             string
	Sequence                                                        uint64
	Target                                                          Target
	Deadline                                                        int64
}
type admissionModelReview struct {
	Digest   string
	Request  admissionModelRequest
	Identity FixtureBuildIdentity
	Subjects []FixtureReleaseSubject
}
type admissionModelOutcome struct {
	Profile, Exchange, Unit, Outcome                                           string
	FixtureOnly                                                                bool
	InstallAllowed, ReserveAllowed, PauseAllowed, UnlockAllowed, ResumeAllowed bool
	RealWallets, InstalledExecution, AutomaticUpdates, FinancialExecution      bool
}

// A typed in-process handle, not a serializable bearer authorization.
type admissionModelReceipt struct {
	model                *installedAdmissionModel
	signerHead, exchange string
}
type installedAdmissionModel struct {
	mu                    sync.Mutex
	owner, signer         admissionModelRole
	channel, stage        string
	ownerBoot, signerBoot string
	review                func(admissionModelReview) bool
	request               admissionModelRequest
	wire                  []byte
	evidence              handoffInspection
	receipt               admissionModelReceipt
	live                  bool
}

func newInstalledAdmissionModel(owner, signer admissionModelRole, review func(admissionModelReview) bool, initial bool) (*installedAdmissionModel, error) {
	if owner.actor == nil || signer.actor == nil || owner.identity == nil || signer.identity == nil || review == nil {
		return nil, errors.New("independent fixture roles and protected model review required")
	}
	// Existing constructor checks role, independent stores and verifier pins. It
	// models a channel only; it is not kernel/executable authentication.
	if e := matchingFixtureHandoffActors(owner.actor, signer.actor); e != nil {
		return nil, e
	}
	owner.baseline, signer.baseline = owner.identity(), signer.identity()
	if !owner.baseline.valid() || !signer.baseline.valid() || owner.baseline != signer.baseline {
		return nil, errors.New("unsupported model identity, trust or reader capability")
	}
	m := &installedAdmissionModel{owner: owner, signer: signer, channel: NewProbe(), stage: "created", review: review, ownerBoot: owner.actor.boot, signerBoot: signer.actor.boot, live: initial}
	for _, r := range []*admissionModelRole{&m.owner, &m.signer} {
		a := r.actor
		if r.baseline.InstanceID != a.cfg.Inventory().InstanceID || r.baseline.SeedSHA != a.cfg.Trust.SeedSHA256 || r.baseline.PolicySHA != a.cfg.Admission.PolicySHA256 || r.baseline.TransportSHA != a.transportSHA {
			return nil, errors.New("role-owned verifier differs from model pins")
		}
		a.mu.Lock()
		_, cp, e := a.load()
		a.mu.Unlock()
		if e != nil {
			return nil, e
		}
		r.head = a.Head()
		if initial {
			// A separate, empty fixture namespace is mandatory; never adopt an old
			// handoff/model/installed history as this model's initialization.
			if len(cp.Records) != 0 || len(cp.Trust) != 0 || cp.Pending != "" || cp.Active != "" {
				return nil, errors.New("explicit empty model namespace required")
			}
		} else {
			found := false
			for _, record := range cp.Records {
				if record.Kind == "installed-admission-model" {
					var v admissionModelRecord
					if StrictJSON(record.Body, &v) != nil || v.Profile != installedAdmissionModelProfile || v.Binding != r.baseline.digest() {
						return nil, errors.New("foreign model checkpoint")
					}
					found = true
				}
			}
			if !found {
				return nil, errors.New("missing model history; no reset")
			}
		}
	}
	if initial {
		for _, r := range []*admissionModelRole{&m.owner, &m.signer} {
			if e := m.record(r, "created", false); e != nil {
				return nil, e
			}
		}
	}
	return m, nil
}
func (m *installedAdmissionModel) ready(r *admissionModelRole) (handoffCheckpoint, int64, error) {
	if !m.live || m.owner.actor.boot != m.ownerBoot || m.signer.actor.boot != m.signerBoot || !r.identity().valid() || r.identity() != r.baseline {
		return handoffCheckpoint{}, 0, errors.New("model identity/reader/boot changed or historical-only handle")
	}
	a := r.actor
	a.mu.Lock()
	defer a.mu.Unlock()
	cp, now, e := a.current(m.channel)
	if e != nil {
		return cp, now, e
	}
	if a.head != r.head {
		return cp, now, errors.New("model checkpoint changed outside this exchange")
	}
	return cp, now, nil
}
func (m *installedAdmissionModel) record(r *admissionModelRole, phase string, clear bool) error {
	a := r.actor
	a.mu.Lock()
	defer a.mu.Unlock()
	cp, now, e := a.current(m.channel)
	if e != nil {
		return e
	}
	if a.head != r.head {
		return errors.New("model CAS baseline changed")
	}
	v := admissionModelRecord{Profile: installedAdmissionModelProfile, Binding: r.baseline.digest(), Phase: phase, Exchange: m.channel, Request: digest(m.wire), Authorization: m.evidence.authorization, Unit: m.evidence.unit, Deadline: m.request.Deadline}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if e = putHandoffRecord(&cp, handoffRecord{ID: m.channel, Channel: m.channel, Kind: "installed-admission-model", Body: b}); e != nil {
		return e
	}
	cp.LastAt = now
	cp.Active = m.channel
	if clear {
		cp.Active = ""
	}
	if e = a.publish(cp, nil); e != nil {
		return e
	}
	r.head = a.head
	return nil
}
func (m *installedAdmissionModel) inspect(ctx context.Context, r *admissionModelRole) (handoffInspection, error) {
	if _, _, e := m.ready(r); e != nil {
		return handoffInspection{}, e
	}
	v, e := r.actor.inspect(ctx, m.channel)
	// The verifier owns its combined pending/trust/replay publication. On failure
	// never reset or import a previous snapshot, even if the other peer failed.
	if e == nil {
		r.head = r.actor.Head()
	}
	return v, e
}
func (m *installedAdmissionModel) fail() { m.live = false }
func (m *installedAdmissionModel) prepare(ctx context.Context) (wire []byte, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer func() {
		if err != nil {
			m.fail()
		}
	}()
	if m.stage != "created" {
		return nil, errors.New("one model exchange only")
	}
	oc, on, e := m.ready(&m.owner)
	if e != nil {
		return nil, e
	}
	sc, sn, e := m.ready(&m.signer)
	if e != nil {
		return nil, e
	}
	if abs64(on-sn) > 30 {
		return nil, errors.New("independent role clock skew")
	}
	v, e := m.inspect(ctx, &m.owner)
	if e != nil {
		return nil, e
	}
	m.evidence = v
	p, e := parseAdmissionPolicy(m.signer.actor.cfg.Admission, sn)
	if e != nil {
		return nil, e
	}
	m.request = admissionModelRequest{Profile: installedAdmissionModelProfile, Capability: installedAdmissionModelCapability, Channel: m.channel, OwnerBoot: m.ownerBoot, SignerBoot: m.signerBoot, Binding: m.owner.baseline.digest(), OwnerInventory: handoffInventoryDigest(oc.Inventory), SignerInventory: handoffInventoryDigest(sc.Inventory), OwnerHead: m.owner.head, SignerHead: m.signer.head, Authorization: v.authorization, Unit: v.unit, Sequence: v.sequence, Target: v.target, Deadline: min(on+60, sn+60, v.expiry, p.NotAfter)}
	m.wire = mustHandoffJSON(m.request)
	if !m.review(admissionModelReview{Digest: hashTuple(installedAdmissionModelProfile+":review", string(m.wire)), Request: m.request, Identity: v.release.Identity, Subjects: append([]FixtureReleaseSubject(nil), v.release.Subjects...)}) {
		return nil, errors.New("protected model review denied")
	}
	if e = m.bound(); e != nil {
		return nil, e
	}
	if e = m.record(&m.owner, "reviewed", false); e != nil {
		return nil, e
	}
	m.stage = "reviewed"
	return append([]byte(nil), m.wire...), nil
}
func validInstalledAdmissionModelRecord(raw []byte, id, channel string) bool {
	var v admissionModelRecord
	if StrictJSON(raw, &v) != nil || v.Profile != installedAdmissionModelProfile || v.Exchange != id || id != channel || !hashOK(v.Binding) || !IDOK(v.Exchange) || !hashOK(v.Request) {
		return false
	}
	switch v.Phase {
	case "created":
		return v.Authorization == "" && v.Unit == "" && v.Deadline == 0
	case "reviewed", "result", "confirmed", "complete":
		return hashOK(v.Authorization) && hashOK(v.Unit) && v.Deadline > 0
	default:
		return false
	}
}

func (m *installedAdmissionModel) bound() error {
	for _, pair := range []struct {
		r   *admissionModelRole
		inv string
	}{{&m.owner, m.request.OwnerInventory}, {&m.signer, m.request.SignerInventory}} {
		cp, now, e := m.ready(pair.r)
		if e != nil {
			return e
		}
		if now >= m.request.Deadline || handoffInventoryDigest(cp.Inventory) != pair.inv {
			return errors.New("model review expired or authoritative inventory changed")
		}
	}
	ownerAt, _ := m.owner.actor.cfg.Clock()
	signerAt, _ := m.signer.actor.cfg.Clock()
	if abs64(ownerAt-signerAt) > 30 {
		return errors.New("role clock skew changed during review")
	}
	return nil
}
func (m *installedAdmissionModel) check(ctx context.Context, wire []byte) (receipt admissionModelReceipt, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer func() {
		if err != nil {
			m.fail()
		}
	}()
	var request admissionModelRequest
	if len(wire) > 64<<10 || StrictJSON(wire, &request) != nil || request != m.request || !bytesEqual(wire, m.wire) {
		return receipt, errors.New("exact model exchange required; JSON is not authority")
	}
	if m.stage != "reviewed" && m.stage != "result" {
		return receipt, errors.New("no live reviewed exchange")
	}
	if e := m.bound(); e != nil {
		return receipt, e
	}
	if m.stage == "result" {
		return m.receipt, nil
	}
	v, e := m.inspect(ctx, &m.signer)
	if e != nil {
		return receipt, e
	}
	if v.authorization != m.evidence.authorization || v.unit != m.evidence.unit || v.sequence != m.evidence.sequence || v.target != m.evidence.target || v.expiry < m.request.Deadline {
		return receipt, errors.New("independent signer admission differs")
	}
	if e = m.bound(); e != nil {
		return receipt, e
	}
	if e = m.record(&m.signer, "result", false); e != nil {
		return receipt, e
	}
	m.stage = "result"
	m.receipt = admissionModelReceipt{m, m.signer.head, digest(m.wire)}
	return m.receipt, nil
}
func (m *installedAdmissionModel) finish(ctx context.Context, r admissionModelReceipt) (out admissionModelOutcome, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer func() {
		if err != nil {
			m.fail()
		}
	}()
	if r.model != m || r.exchange != digest(m.wire) || m.stage != "result" && m.stage != "complete" || !m.live {
		return out, errors.New("live opaque model receipt required")
	}
	if e := m.bound(); e != nil {
		return out, e
	}
	if m.stage == "complete" {
		return m.outcome(), nil
	}
	if r.signerHead != m.signer.head {
		return out, errors.New("signer result superseded")
	}
	v, e := m.inspect(ctx, &m.owner)
	if e != nil {
		return out, e
	}
	if v.authorization != m.evidence.authorization || v.unit != m.evidence.unit || v.expiry < m.request.Deadline {
		return out, errors.New("fresh owner admission differs")
	}
	// Final signer confirmation follows owner refresh, on this live model handle.
	if e = m.bound(); e != nil {
		return out, e
	}
	if e = m.record(&m.signer, "confirmed", false); e != nil {
		return out, e
	}
	if e = m.record(&m.owner, "complete", true); e != nil {
		return out, e
	}
	if e = m.record(&m.signer, "complete", true); e != nil {
		return out, e
	}
	m.stage = "complete"
	return m.outcome(), nil
}
func (m *installedAdmissionModel) outcome() admissionModelOutcome {
	return admissionModelOutcome{Profile: installedAdmissionModelProfile, Exchange: digest(m.wire), Unit: m.evidence.unit, Outcome: "inspection-complete", FixtureOnly: true}
}
func (m *installedAdmissionModel) status() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	records := map[string][]admissionModelRecord{}
	for name, r := range map[string]*admissionModelRole{"owner": &m.owner, "signer": &m.signer} {
		a := r.actor
		a.mu.Lock()
		_, cp, e := a.load()
		a.mu.Unlock()
		if e != nil {
			return nil, e
		}
		for _, record := range cp.Records {
			if record.Kind == "installed-admission-model" {
				var v admissionModelRecord
				if StrictJSON(record.Body, &v) != nil || v.Binding != r.baseline.digest() {
					return nil, errors.New("foreign model status")
				}
				records[name] = append(records[name], v)
			}
		}
	}
	return json.Marshal(struct {
		Profile        string
		HistoricalOnly bool
		Records        map[string][]admissionModelRecord
	}{installedAdmissionModelProfile, true, records})
}

// Bridge state is a simulation over copied financial values only. No method
// accepts a vault, controller, filesystem root, executable or service handle.
type admissionModelFinancial struct {
	HistorySHA, OriginalsSHA, AuthoritySHA string
	Installed, Reserved                    uint64
	Pending, Unknown                       uint64
	Held                                   bool
}

func (v admissionModelFinancial) valid() bool {
	return hashOK(v.HistorySHA) && hashOK(v.OriginalsSHA) && hashOK(v.AuthoritySHA) && v.Reserved >= v.Installed && v.Pending == 0 && v.Unknown == 0 && !v.Held
}

type admissionModelBridge struct {
	Profile, Binding, Phase                    string
	Before, Current                            admissionModelFinancial
	Maintenance                                uint64
	ForwardOnly, MaintenanceHeld, WalletLocked bool
}

var admissionModelBridgePhases = []string{"prepared", "paused-backed-up", "core-replaced", "pair-replaced", "records-converged", "forward-only", "maintenance-aborted"}

func newAdmissionModelBridge(from, to admissionModelReaders, financial admissionModelFinancial, maintenance uint64, binding string) (admissionModelBridge, error) {
	if admissionModelBridgeDecision(from, to) != "RC-04" || !financial.valid() || maintenance <= financial.Reserved || !hashOK(binding) {
		return admissionModelBridge{}, errors.New("finite compatible fixture bridge and known financial history required")
	}
	current := financial
	current.Reserved = maintenance
	return admissionModelBridge{installedAdmissionModelProfile, binding, "prepared", financial, current, maintenance, false, true, true}, nil
}
func (b *admissionModelBridge) advance(phase string, observed admissionModelFinancial, newReaderColdVerified bool) error {
	if !newReaderColdVerified || observed != b.Current || !observed.valid() {
		return errors.New("new-reader cold financial validation required; hold")
	}
	next := ""
	for i, p := range admissionModelBridgePhases {
		if p == b.Phase && i+1 < len(admissionModelBridgePhases) {
			next = admissionModelBridgePhases[i+1]
		}
	}
	if phase != next || next == "" {
		return errors.New("unsupported bridge phase; no inferred migration")
	}
	b.Phase = phase
	if phase == "forward-only" {
		b.ForwardOnly = true
	}
	if phase == "maintenance-aborted" {
		b.MaintenanceHeld = false
	}
	return nil
}
func (b *admissionModelBridge) rollback(observed admissionModelFinancial, newReaderColdVerified, predecessorReadable bool) error {
	if b.ForwardOnly || !b.MaintenanceHeld || !newReaderColdVerified || !predecessorReadable || observed != b.Current || !observed.valid() {
		return errors.New("old-reader rollback forbidden; preserve history and hold")
	}
	b.Phase = "rolled-back-held"
	return nil
}
func (b *admissionModelBridge) abortRollback(observed admissionModelFinancial) error {
	if b.Phase != "rolled-back-held" || !b.MaintenanceHeld || observed != b.Current {
		return errors.New("only exact known maintenance abort")
	}
	b.Phase = "rolled-back"
	b.MaintenanceHeld = false
	return nil
}
func (b admissionModelBridge) checkpoint() ([]byte, string) {
	raw := mustHandoffJSON(b)
	return raw, digest(raw)
}
func restoreAdmissionModelBridge(raw []byte, anchor, binding string) (admissionModelBridge, error) {
	var b admissionModelBridge
	if len(raw) > 4096 || !hashOK(anchor) || digest(raw) != anchor || StrictJSON(raw, &b) != nil || b.Profile != installedAdmissionModelProfile || b.Binding != binding || !hashOK(binding) || !b.Before.valid() || !b.Current.valid() || b.Maintenance <= b.Before.Reserved || !b.WalletLocked {
		return b, errors.New("missing/foreign bridge anchor or history")
	}
	expected := b.Before
	expected.Reserved = b.Maintenance
	if b.Current != expected {
		return b, errors.New("bridge financial state rewound")
	}
	phase := -1
	for i, p := range admissionModelBridgePhases {
		if b.Phase == p {
			phase = i
		}
	}
	if phase < 0 && b.Phase != "rolled-back-held" && b.Phase != "rolled-back" {
		return b, errors.New("unknown bridge phase")
	}
	if b.ForwardOnly != (phase >= 5) || b.MaintenanceHeld != (phase != 6 && b.Phase != "rolled-back") {
		return b, errors.New("inconsistent bridge recovery boundary")
	}
	return b, nil
}
