package update

// This fixture transport is deliberately separate from installed signer RPC.
// No value decoded here can construct a model receipt or execute a release.
import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const FixtureTransportProfile = "fased-app-owner-signer-transport-fixture:v1"
const fixtureTransportCapability = "fixture-release-transport-v1"

// All paths and pins are independently provisioned, not taken from a proposal.
type FixtureTransportConfig struct {
	FixtureBinding
	Version      int    `json:"version"`
	Profile      string `json:"profile"`
	Experiment   string `json:"experiment"`
	Role         string `json:"role"`
	OwnerUID     int    `json:"ownerUid"`
	SignerUID    int    `json:"signerUid"`
	AppUID       int    `json:"appUid"`
	PrivateDir   string `json:"privateDir"`
	Socket       string `json:"socket"`
	SeedSHA256   string `json:"seedSha256"`
	PolicySHA256 string `json:"policySha256"`
	CASHA256     string `json:"caSha256"`
}
type FixtureTransportLoadOptions struct{ Initial, Audit bool }
type fixtureTransportAnchor struct {
	Version      int    `json:"version"`
	Profile      string `json:"profile"`
	Experiment   string `json:"experiment"`
	ConfigSHA256 string `json:"configSha256"`
	Head         string `json:"head"`
}
type FixtureTransportRole struct {
	cfg       FixtureTransportConfig
	actor     *FixtureHandoffActor
	store     *HandoffFixtureStore
	configSHA string
	testOnly  bool // Private _test.go construction only; never loaded from flags/JSON.
}

func (r *FixtureTransportRole) Close() error {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.Close()
}

func privateFixtureRead(path string) ([]byte, error) {
	real, e := filepath.EvalSymlinks(path)
	if e != nil || !filepath.IsAbs(path) || real != filepath.Clean(path) {
		return nil, errors.New("real private fixture file required")
	}
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || !acquisitionParentOwned(st) {
		return nil, errors.New("private fixture ownership refused")
	}
	f, e := openReadOnly(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	fs, e := f.Stat()
	if e != nil || !os.SameFile(st, fs) {
		return nil, errors.New("fixture file changed")
	}
	b, e := io.ReadAll(io.LimitReader(f, MaxDocument+1))
	if e != nil || len(b) > MaxDocument {
		return nil, errors.New("fixture file bound")
	}
	return b, nil
}
func (r *FixtureTransportRole) read(name string) ([]byte, error) {
	return privateFixtureRead(filepath.Join(r.cfg.PrivateDir, name))
}
func (r *FixtureTransportRole) anchor() (a fixtureTransportAnchor, err error) {
	b, e := r.read("anchor.json")
	if e != nil || StrictJSON(b, &a) != nil || a.Version != 1 || a.Profile != FixtureTransportProfile || a.Experiment != r.cfg.Experiment || a.ConfigSHA256 != r.configSHA || a.Head != "" && !hashOK(a.Head) {
		return a, errors.New("fixture anchor mismatch")
	}
	return a, nil
}
func (r *FixtureTransportRole) advanceAnchor(old, next string) error {
	a, e := r.anchor()
	if e != nil {
		return e
	}
	if a.Head == next {
		return nil
	}
	if a.Head != old || !hashOK(next) {
		return errors.New("fixture anchor CAS conflict")
	}
	a.Head = next
	b, _ := json.Marshal(a)
	root, e := os.OpenRoot(r.cfg.PrivateDir)
	if e != nil {
		return e
	}
	defer root.Close()
	fs := &FixtureTrustStore{root: root}
	name := "anchor-" + digest(b) + ".tmp"
	if e = fs.temporary(name, b, "anchor"); e != nil {
		return e
	}
	current, e := r.anchor()
	if e != nil || current.Head != old {
		return errors.New("fixture anchor changed before publication")
	}
	if e = root.Rename(name, "anchor.json"); e != nil {
		return e
	}
	return fs.sync()
}
func LoadFixtureTransportRole(path, pin string, o FixtureTransportLoadOptions) (*FixtureTransportRole, error) {
	return loadFixtureTransportRole(path, pin, o, false)
}

// Only _test.go callers may construct same-UID peers. No flag/config hook.
func loadFixtureTransportRole(path, pin string, o FixtureTransportLoadOptions, testOnly bool) (*FixtureTransportRole, error) {
	if e := fixtureTransportSupported(); e != nil {
		return nil, e
	}
	if os.Geteuid() == 0 || os.Getuid() != os.Geteuid() || !hashOK(pin) || o.Initial && o.Audit {
		return nil, errors.New("explicit ordinary-user fixture inputs required")
	}
	b, e := privateFixtureRead(path)
	if e != nil || digest(b) != pin {
		return nil, errors.New("fixture configuration pin refused")
	}
	var c FixtureTransportConfig
	if StrictJSON(b, &c) != nil || c.Version != 1 || c.Profile != FixtureTransportProfile || !c.FixtureBinding.valid() || !IDOK(c.Experiment) || c.Role != "owner" && c.Role != "signer" || !hashOK(c.SeedSHA256) || !hashOK(c.PolicySHA256) || !hashOK(c.CASHA256) || filepath.Join(c.PrivateDir, "config.json") != path {
		return nil, errors.New("fixture configuration refused")
	}
	r := &FixtureTransportRole{cfg: c, configSHA: pin, testOnly: testOnly}
	if e = fixtureRoleIdentity(r); e != nil {
		return nil, e
	}
	var seed TrustFixtureSeed
	seedBytes, e := r.read("seed.json")
	if e != nil || digest(seedBytes) != c.SeedSHA256 || StrictJSON(seedBytes, &seed) != nil || seed.FixtureBinding != c.FixtureBinding {
		return nil, errors.New("fixture seed pin refused")
	}
	policy, e := r.read("policy.json")
	if e != nil || digest(policy) != c.PolicySHA256 {
		return nil, errors.New("fixture policy pin refused")
	}
	ca, e := r.read("ca.pem")
	if e != nil || digest(ca) != c.CASHA256 {
		return nil, errors.New("fixture CA pin refused")
	}
	evidence, e := r.read("evidence.json")
	if e != nil {
		return nil, e
	}
	anchor, e := r.anchor()
	if e != nil || o.Initial && anchor.Head != "" || !o.Initial && !hashOK(anchor.Head) {
		return nil, errors.New("explicit fresh or anchored fixture required")
	}
	store, e := OpenHandoffFixtureStore(filepath.Join(c.PrivateDir, "store"))
	if e != nil {
		return nil, e
	}
	r.store = store
	fail := func(e error) (*FixtureTransportRole, error) { _ = store.Close(); return nil, e }
	if o.Audit {
		if c.Role != "owner" {
			return fail(errors.New("owner audit only"))
		}
		return r, nil
	}
	clock := func() (int64, bool) {
		var v struct {
			Now       int64 `json:"now"`
			Confident bool  `json:"confident"`
		}
		b, e := r.read("clock.json")
		if e != nil || StrictJSON(b, &v) != nil {
			return 0, false
		}
		return v.Now, v.Confident
	}
	inventory := func() FixtureHandoffInventory {
		var v FixtureHandoffInventory
		b, e := r.read("inventory.json")
		if e != nil || StrictJSON(b, &v) != nil {
			return FixtureHandoffInventory{}
		}
		b, e = r.read("financial-canary")
		if e != nil {
			return FixtureHandoffInventory{}
		}
		v.CanarySHA256 = digest(b)
		return v
	}
	cfg := FixtureHandoffConfig{Development: true, Role: c.Role, Store: store, Initial: o.Initial, ExpectedHead: anchor.Head, Trust: TrustRehearsalOptions{Development: true, Seed: seedBytes, SeedSHA256: c.SeedSHA256, TLSCA: ca}, Admission: ReleaseAdmissionOptions{Policy: policy, PolicySHA256: c.PolicySHA256, Evidence: evidence, ArtifactDir: filepath.Join(c.PrivateDir, "artifacts")}, Clock: clock, Inventory: inventory, Review: func(FixtureHandoffReview) bool { return false }, checkAnchor: func(head string) error {
		a, e := r.anchor()
		if e != nil || a.Head != head {
			return errors.New("fixture anchor mismatch")
		}
		return nil
	}, advanceAnchor: r.advanceAnchor}
	r.actor, e = NewFixtureHandoffActor(cfg)
	if e != nil {
		return fail(e)
	}
	return r, nil
}

type fixtureTransportFrame struct {
	Version    int             `json:"version"`
	Profile    string          `json:"profile"`
	Mode       string          `json:"mode"`
	Capability string          `json:"capability"`
	Binding    FixtureBinding  `json:"binding"`
	Kind       string          `json:"kind"`
	ChannelID  string          `json:"channelId"`
	Body       json.RawMessage `json:"body"`
}

func transportFrame(binding FixtureBinding, kind, channel string, body any) fixtureTransportFrame {
	b, _ := json.Marshal(body)
	return fixtureTransportFrame{1, FixtureTransportProfile, handoffMode, fixtureTransportCapability, binding, kind, channel, b}
}
func readFixtureFrame(c net.Conn, deadline time.Time) (f fixtureTransportFrame, err error) {
	if e := c.SetReadDeadline(deadline); e != nil {
		return f, e
	}
	var header [4]byte
	if _, e := io.ReadFull(c, header[:]); e != nil {
		return f, e
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > MaxDocument {
		return f, errors.New("fixture frame length refused")
	}
	if e := c.SetReadDeadline(earlier(deadline, time.Now().Add(5*time.Second))); e != nil {
		return f, e
	}
	b := make([]byte, int(n))
	if _, e := io.ReadFull(c, b); e != nil {
		return f, e
	}
	if StrictJSON(b, &f) != nil || f.Version != 1 || f.Profile != FixtureTransportProfile || f.Mode != handoffMode || f.Capability != fixtureTransportCapability || !f.Binding.valid() || f.ChannelID != "" && !IDOK(f.ChannelID) {
		return f, errors.New("unsupported or malformed fixture frame")
	}
	return f, nil
}
func writeFixtureFrame(c net.Conn, f fixtureTransportFrame, deadline time.Time) error {
	b, e := json.Marshal(f)
	if e != nil || len(b) > MaxDocument {
		return errors.New("fixture frame bound")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(b)))
	if e = c.SetWriteDeadline(earlier(deadline, time.Now().Add(5*time.Second))); e != nil {
		return e
	}
	_, e = io.Copy(c, bytes.NewReader(append(header[:], b...)))
	return e
}
func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func wireDecode[T any](f fixtureTransportFrame, kind, channel string) (v T, e error) {
	if f.Kind != kind || f.ChannelID != channel || StrictJSON(f.Body, &v) != nil {
		return v, errors.New("fixture frame state refused")
	}
	return v, nil
}

type fixtureHello struct {
	ClientNonce    string `json:"clientNonce"`
	OwnerBootID    string `json:"ownerBootId"`
	Operation      string `json:"operation"`
	RequestID      string `json:"requestId"`
	ExchangeDigest string `json:"exchangeDigest"`
}
type fixtureInspect struct {
	Request        json.RawMessage `json:"request"`
	OwnerNotAfter  int64           `json:"ownerNotAfter"`
	ReviewDeadline int64           `json:"reviewDeadline"`
	ExchangeDigest string          `json:"exchangeDigest"`
}

func (q fixtureInspect) digest(c FixtureTransportConfig, channel, nonce string) string {
	return hashTuple("fased-app-fixture-transport-exchange:v1", FixtureTransportProfile, fixtureTransportCapability, c.Repository, c.Channel, channel, nonce, strconv.Itoa(c.OwnerUID), strconv.Itoa(c.SignerUID), digest(q.Request), strconv.FormatInt(q.OwnerNotAfter, 10), strconv.FormatInt(q.ReviewDeadline, 10))
}

type fixtureTransportResult struct {
	Outcome        FixtureHandoffOutcome `json:"outcome"`
	ExchangeDigest string                `json:"exchangeDigest"`
	OwnerBootID    string                `json:"ownerBootId"`
	ClientNonce    string                `json:"clientNonce"`
	ChannelID      string                `json:"channelId"`
}
type fixturePublished struct {
	Payload    json.RawMessage `json:"payload"`
	HeadSHA256 string          `json:"headSha256"`
}
type fixtureConfirm struct {
	ExchangeDigest string `json:"exchangeDigest"`
	ResultSHA256   string `json:"resultSha256"`
}
type fixtureConfirmed struct {
	fixtureConfirm
	RequestID    string `json:"requestId"`
	SignerBootID string `json:"signerBootId"`
	ObservedAt   int64  `json:"observedAt"`
	Deadline     int64  `json:"deadline"`
}
type FixtureTransportReview struct {
	Review         FixtureHandoffReview `json:"review"`
	ExchangeDigest string               `json:"exchangeDigest"`
}
type FixtureTransportReport struct {
	Outcome           FixtureHandoffOutcome `json:"outcome"`
	ExchangeDigest    string                `json:"exchangeDigest"`
	OwnerHead         string                `json:"ownerHead"`
	SignerHead        string                `json:"signerHead"`
	ConfirmationHead  string                `json:"confirmationHead"`
	OwnerUID          int                   `json:"ownerUid"`
	SignerUID         int                   `json:"signerUid"`
	LiveRetryVerified bool                  `json:"liveRetryVerified"`
}
type fixtureAudit struct {
	Historical     bool            `json:"historical"`
	RequestID      string          `json:"requestId"`
	ExchangeDigest string          `json:"exchangeDigest"`
	Record         json.RawMessage `json:"record"`
}
type fixturePeer struct {
	conn *net.UnixConn
	uid  int
}
type authenticatedFixtureResult struct {
	peer      *fixturePeer
	payload   fixtureTransportResult
	published fixturePublished
}

func (r *FixtureTransportRole) localRecord(channel, id, kind string, body any, clear bool) error {
	a := r.actor
	a.mu.Lock()
	defer a.mu.Unlock()
	c, now, e := a.current(channel)
	if e != nil {
		return e
	}
	b, e := json.Marshal(body)
	if e != nil {
		return e
	}
	if e = putHandoffRecord(&c, handoffRecord{ID: id, Channel: channel, Kind: kind, Body: b}); e != nil {
		return e
	}
	c.LastAt = now
	c.Active = channel
	if clear {
		c.Active = ""
	}
	return a.publish(c, nil)
}
func (r *FixtureTransportRole) cancel(channel string) {
	if r.actor == nil {
		return
	}
	a := r.actor
	a.mu.Lock()
	defer a.mu.Unlock()
	c, now, e := a.current(channel)
	if e != nil || c.Active != channel {
		return
	}
	c.Active = ""
	c.LastAt = now
	_ = a.publish(c, nil)
}
func (r *FixtureTransportRole) inspection(ctx context.Context, channel string) (handoffInspection, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return r.actor.inspect(ctx, channel)
}
func (r *FixtureTransportRole) audit(id, exchange string) (fixtureAudit, error) {
	if !IDOK(id) || !hashOK(exchange) {
		return fixtureAudit{}, errors.New("invalid audit identity")
	}
	a, e := r.anchor()
	if e != nil {
		return fixtureAudit{}, e
	}
	cfg := hashTuple(HandoffProfile+":config", r.cfg.Role, r.cfg.SeedSHA256, r.cfg.PolicySHA256, handoffTransportFromConfig(r))
	b, e := r.store.Load(cfg, a.Head)
	if e != nil {
		return fixtureAudit{}, e
	}
	c, e := parseHandoffCheckpoint(b)
	if e != nil {
		return fixtureAudit{}, e
	}
	for _, record := range c.Records {
		if record.ID == id && record.Kind == "checked" {
			var result fixtureTransportResult
			if StrictJSON(record.Body, &result) == nil && result.ExchangeDigest == exchange {
				return fixtureAudit{true, id, exchange, append(json.RawMessage(nil), record.Body...)}, nil
			}
		}
	}
	return fixtureAudit{}, errors.New("audit unavailable")
}
func handoffTransportFromConfig(r *FixtureTransportRole) string {
	b, _ := r.read("seed.json")
	var seed TrustFixtureSeed
	_ = StrictJSON(b, &seed)
	ca, _ := r.read("ca.pem")
	return handoffTransport(seed, ca)
}

// NewFixtureOwnerClient requires independently loaded owner state. Audit-only
// roles may read history but cannot inspect or obtain authenticated results.
type FixtureOwnerClient struct {
	role   *FixtureTransportRole
	review func(FixtureTransportReview) bool
}

func NewFixtureOwnerClient(r *FixtureTransportRole, review func(FixtureTransportReview) bool) (*FixtureOwnerClient, error) {
	if r == nil || r.cfg.Role != "owner" || fixtureRoleIdentity(r) != nil {
		return nil, errors.New("fixture owner role required")
	}
	return &FixtureOwnerClient{r, review}, nil
}
func (c *FixtureOwnerClient) connect(ctx context.Context) (*fixturePeer, error) {
	conn, e := fixtureDial(ctx, c.role)
	if e != nil {
		return nil, e
	}
	uid, e := fixturePeerUID(conn)
	if e != nil || uid != c.role.cfg.SignerUID {
		_ = conn.Close()
		return nil, errors.New("unauthorized signer peer")
	}
	return &fixturePeer{conn, uid}, nil
}
func (c *FixtureOwnerClient) Audit(ctx context.Context, id, exchange string) (json.RawMessage, error) {
	p, e := c.connect(ctx)
	if e != nil {
		return nil, e
	}
	defer p.conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	hello := fixtureHello{NewProbe(), NewProbe(), "status", id, exchange}
	if e = writeFixtureFrame(p.conn, transportFrame(c.role.cfg.FixtureBinding, "hello", "", hello), deadline); e != nil {
		return nil, e
	}
	f, e := readFixtureFrame(p.conn, deadline)
	if e != nil || f.Binding != c.role.cfg.FixtureBinding {
		return nil, errors.New("audit unavailable")
	}
	v, e := wireDecode[fixtureAudit](f, "audit", f.ChannelID)
	if e != nil || !v.Historical || v.RequestID != id || v.ExchangeDigest != exchange {
		return nil, errors.New("audit mismatch")
	}
	return append(json.RawMessage(nil), v.Record...), nil
}

func (c *FixtureOwnerClient) Inspect(ctx context.Context) (out FixtureTransportReport, err error) {
	r := c.role
	a := r.actor
	if a == nil || c.review == nil {
		return out, errors.New("inspection requires local owner state and review")
	}
	nonce := NewProbe()
	defer r.cancel(nonce)
	initial, e := r.inspection(ctx, nonce)
	if e != nil {
		return out, e
	}
	p, e := c.connect(ctx)
	if e != nil {
		return out, e
	}
	defer p.conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = p.conn.Close() })
	defer stop()
	deadline := time.Now().Add(60 * time.Second)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if e = writeFixtureFrame(p.conn, transportFrame(r.cfg.FixtureBinding, "hello", "", fixtureHello{nonce, a.boot, "inspect", "", ""}), deadline); e != nil {
		return out, e
	}
	f, e := readFixtureFrame(p.conn, earlier(deadline, time.Now().Add(5*time.Second)))
	if e != nil || f.Binding != r.cfg.FixtureBinding {
		return out, errors.New("fixture challenge unavailable")
	}
	ch, e := wireDecode[FixtureHandoffChallenge](f, "challenge", f.ChannelID)
	if e != nil {
		return out, e
	}
	defer r.cancel(ch.ChannelID)
	a.mu.Lock()
	state, now, e := a.current(nonce)
	head := a.head
	a.mu.Unlock()
	if e != nil || ch.Version != 1 || ch.Profile != HandoffProfile || ch.Mode != handoffMode || ch.Capability != handoffCapability || ch.FixtureBinding != a.binding || ch.ChannelID != f.ChannelID || !IDOK(ch.ID) || !IDOK(ch.ChannelID) || !IDOK(ch.SignerBootID) || !hashOK(ch.HeadSHA256) || ch.SeedSHA256 != r.cfg.SeedSHA256 || ch.PolicySHA256 != r.cfg.PolicySHA256 || ch.TransportSHA256 != a.transportSHA || ch.Inventory.InstanceID != state.Inventory.InstanceID || ch.Inventory.AuthoritySHA256 != state.Inventory.AuthoritySHA256 || !ch.Inventory.valid() || ch.ObservedAt <= 0 || ch.ObservedAt > 4102444800 || ch.Deadline <= 0 || ch.Deadline > 4102444800 || abs64(initial.at-ch.ObservedAt) > 30 || now >= ch.Deadline || ch.Deadline > ch.ObservedAt+60 || now >= initial.expiry {
		return out, errors.New("fixture challenge binding refused")
	}
	request := FixtureHandoffRequest{Challenge: ch, ID: NewProbe(), OwnerBootID: a.boot, Inventory: state.Inventory, OwnerHeadSHA256: head, AuthorizationID: initial.authorization, UnitID: initial.unit, Target: initial.target, Sequence: initial.sequence, ObservedAt: initial.at, ReviewedAt: now}
	requestBytes, _ := json.Marshal(request)
	q := fixtureInspect{Request: requestBytes, OwnerNotAfter: initial.expiry, ReviewDeadline: min(initial.expiry, ch.Deadline, now+3600)}
	q.ExchangeDigest = q.digest(r.cfg, ch.ChannelID, nonce)
	review := FixtureTransportReview{FixtureHandoffReview{request.requestDigest(), initial.authorization, initial.unit, initial.sequence, initial.target, q.ReviewDeadline, initial.release.Identity, append([]FixtureReleaseSubject(nil), initial.release.Subjects...)}, q.ExchangeDigest}
	if !c.review(review) || !time.Now().Before(deadline) {
		return out, errors.New("fixture owner review refused")
	}
	a.mu.Lock()
	state, now, e = a.current(nonce)
	if e == nil && (a.head != head || now >= q.ReviewDeadline) {
		e = errors.New("fixture review context changed")
	}
	if e == nil {
		b, _ := json.Marshal(q)
		e = putHandoffRecord(&state, handoffRecord{ID: request.ID, Channel: ch.ChannelID, Kind: "reviewed", Body: b})
		state.Active = ch.ChannelID
		state.LastAt = now
		if e == nil {
			e = a.publish(state, nil)
		}
	}
	a.mu.Unlock()
	if e != nil {
		return out, e
	}
	if e = writeFixtureFrame(p.conn, transportFrame(r.cfg.FixtureBinding, "inspect", ch.ChannelID, q), deadline); e != nil {
		return out, e
	}
	f, e = readFixtureFrame(p.conn, deadline)
	if e != nil || f.Binding != r.cfg.FixtureBinding {
		return out, errors.New("fixture inspection unavailable")
	}
	published, e := wireDecode[fixturePublished](f, "result", ch.ChannelID)
	if e != nil || !hashOK(published.HeadSHA256) {
		return out, errors.New("invalid fixture result")
	}
	var result fixtureTransportResult
	if StrictJSON(published.Payload, &result) != nil {
		return out, errors.New("malformed fixture result")
	}
	v := result.Outcome
	if !disabledTransportOutcome(v) || v.Outcome != "checked" || v.RequestDigest != request.requestDigest() || v.RequestID != request.ID || v.ChallengeID != ch.ID || v.AuthorizationID != initial.authorization || v.UnitID != initial.unit || v.SignerBootID != ch.SignerBootID || v.AuthoritySHA256 != ch.Inventory.AuthoritySHA256 || v.BaselineHead != ch.HeadSHA256 || v.InventoryRevision != ch.Inventory.Revision || v.Sequence != initial.sequence || v.Deadline > q.ReviewDeadline || v.Deadline <= 0 || v.ObservedAt <= 0 || v.ObservedAt > 4102444800 || result.ExchangeDigest != q.ExchangeDigest || result.OwnerBootID != a.boot || result.ClientNonce != nonce || result.ChannelID != ch.ChannelID {
		return out, errors.New("fixture result binding refused")
	}
	// The capability exists only in this stack frame on this authenticated socket.
	authenticated := authenticatedFixtureResult{p, result, published}
	// This development-only inspector deliberately rehearses one exact retry.
	if e = writeFixtureFrame(p.conn, transportFrame(r.cfg.FixtureBinding, "inspect", ch.ChannelID, q), deadline); e != nil {
		return out, e
	}
	retry, e := readFixtureFrame(p.conn, deadline)
	if e != nil || retry.Binding != r.cfg.FixtureBinding || retry.Kind != "result" || retry.ChannelID != ch.ChannelID || !bytes.Equal(retry.Body, f.Body) {
		return out, errors.New("fixture result retry changed")
	}

	fresh, e := r.inspection(ctx, ch.ChannelID)
	if e != nil {
		return out, e
	}
	if fresh.authorization != initial.authorization || fresh.unit != initial.unit || fresh.sequence != initial.sequence || fresh.target != initial.target || fresh.at >= min(fresh.expiry, v.Deadline) || abs64(fresh.at-v.ObservedAt) > 30 {
		return out, errors.New("fresh owner authorization differs")
	}
	confirm := fixtureConfirm{q.ExchangeDigest, digest(authenticated.published.Payload)}
	if e = writeFixtureFrame(authenticated.peer.conn, transportFrame(r.cfg.FixtureBinding, "confirm", ch.ChannelID, confirm), deadline); e != nil {
		return out, e
	}
	f, e = readFixtureFrame(p.conn, deadline)
	if e != nil || f.Binding != r.cfg.FixtureBinding {
		return out, errors.New("fixture confirmation unavailable")
	}
	cp, e := wireDecode[fixturePublished](f, "confirmed", ch.ChannelID)
	var confirmed fixtureConfirmed
	if e != nil || !hashOK(cp.HeadSHA256) || StrictJSON(cp.Payload, &confirmed) != nil || confirmed.fixtureConfirm != confirm || confirmed.RequestID != request.ID || confirmed.SignerBootID != ch.SignerBootID || confirmed.ObservedAt <= 0 || confirmed.ObservedAt > 4102444800 || confirmed.Deadline != v.Deadline {
		return out, errors.New("fixture confirmation mismatch")
	}
	if e = writeFixtureFrame(p.conn, transportFrame(r.cfg.FixtureBinding, "confirm", ch.ChannelID, confirm), deadline); e != nil {
		return out, e
	}
	retry, e = readFixtureFrame(p.conn, deadline)
	if e != nil || retry.Binding != r.cfg.FixtureBinding || retry.Kind != "confirmed" || retry.ChannelID != ch.ChannelID || !bytes.Equal(retry.Body, f.Body) {
		return out, errors.New("fixture confirmation retry changed")
	}
	a.mu.Lock()
	state, now, e = a.current(ch.ChannelID)
	if e == nil && (!time.Now().Before(deadline) || now >= min(v.Deadline, fresh.expiry) || abs64(now-confirmed.ObservedAt) > 30 || now < fresh.at) {
		e = errors.New("fixture completion expired")
	}
	reviewed := false
	for _, record := range state.Records {
		if record.ID == request.ID && record.Kind == "reviewed" {
			b, _ := json.Marshal(q)
			reviewed = bytes.Equal(b, record.Body)
		}
	}
	if e == nil && !reviewed {
		e = errors.New("protected fixture review missing")
	}
	v.Outcome = "inspection-complete"
	if e == nil {
		b, _ := json.Marshal(struct {
			Outcome            FixtureHandoffOutcome `json:"outcome"`
			ExchangeDigest     string                `json:"exchangeDigest"`
			ResultSHA256       string                `json:"resultSha256"`
			ConfirmationSHA256 string                `json:"confirmationSha256"`
		}{v, q.ExchangeDigest, confirm.ResultSHA256, digest(cp.Payload)})
		e = putHandoffRecord(&state, handoffRecord{ID: request.ID, Channel: ch.ChannelID, Kind: "inspection-complete", Body: b})
		state.Active = ""
		state.LastAt = now
		if e == nil {
			e = a.publish(state, nil)
		}
	}
	ownerHead := a.head
	a.mu.Unlock()
	if e != nil {
		return out, e
	}
	return FixtureTransportReport{v, q.ExchangeDigest, ownerHead, published.HeadSHA256, cp.HeadSHA256, os.Getuid(), p.uid, true}, nil
}
func disabledTransportOutcome(v FixtureHandoffOutcome) bool {
	return v.Version == 1 && v.Profile == HandoffProfile && v.Mode == handoffMode && v.FixtureOnly && !v.InstallAllowed && !v.ResumeAllowed && !v.UpdaterEnabled && !v.WalletSigner && !v.FinancialExecution
}

// Server bounds include unauthenticated accepted connections. No installed caller.
type FixtureSignerServer struct {
	role     *FixtureTransportRole
	listener *net.UnixListener
	busy     atomic.Bool
	wg       sync.WaitGroup
	slots    chan struct{}
}

func NewFixtureSignerServer(r *FixtureTransportRole) (*FixtureSignerServer, error) {
	if r == nil || r.actor == nil || r.cfg.Role != "signer" || fixtureRoleIdentity(r) != nil {
		return nil, errors.New("fixture signer role required")
	}
	l, e := fixtureListen(r)
	if e != nil {
		return nil, e
	}
	return &FixtureSignerServer{role: r, listener: l, slots: make(chan struct{}, 4)}, nil
}
func (s *FixtureSignerServer) Close() error { return s.listener.Close() }
func (s *FixtureSignerServer) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	defer s.wg.Wait()
	for {
		conn, e := s.listener.AcceptUnix()
		if e != nil {
			if ctx.Err() != nil || errors.Is(e, net.ErrClosed) {
				return nil
			}
			return e
		}
		select {
		case s.slots <- struct{}{}:
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer func() { <-s.slots }()
				defer conn.Close()
				stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
				defer stop()
				s.handle(ctx, conn)
			}()
		default:
			_ = conn.Close()
		}
	}
}
func (s *FixtureSignerServer) handle(ctx context.Context, conn *net.UnixConn) {
	uid, e := fixturePeerUID(conn)
	if e != nil || uid != s.role.cfg.OwnerUID {
		return
	} // Before even the header.
	role := s.role
	a := role.actor
	f, e := readFixtureFrame(conn, time.Now().Add(5*time.Second))
	if e != nil || f.Binding != role.cfg.FixtureBinding {
		return
	}
	hello, e := wireDecode[fixtureHello](f, "hello", "")
	if e != nil || !IDOK(hello.ClientNonce) || !IDOK(hello.OwnerBootID) {
		return
	}
	channel := NewProbe()
	deadline := time.Now().Add(60 * time.Second)
	reply := func(kind string, body any) error {
		return writeFixtureFrame(conn, transportFrame(role.cfg.FixtureBinding, kind, channel, body), deadline)
	}
	if hello.Operation == "status" {
		v, e := role.audit(hello.RequestID, hello.ExchangeDigest)
		if e != nil {
			_ = reply("error", struct {
				Code string `json:"code"`
			}{"unavailable"})
		} else {
			_ = reply("audit", v)
		}
		return
	}
	if hello.Operation != "inspect" || hello.RequestID != "" || hello.ExchangeDigest != "" {
		return
	}
	if !s.busy.CompareAndSwap(false, true) {
		_ = reply("error", struct {
			Code string `json:"code"`
		}{"busy"})
		return
	}
	defer s.busy.Store(false)
	defer role.cancel(channel)
	fail := func(code string) {
		_ = reply("error", struct {
			Code string `json:"code"`
		}{code})
	}
	a.mu.Lock()
	state, now, e := a.current(channel)
	var ch FixtureHandoffChallenge
	if e == nil {
		var policy FixtureAdmissionPolicy
		policy, e = parseAdmissionPolicy(a.cfg.Admission, now)
		if e == nil {
			ch = FixtureHandoffChallenge{FixtureBinding: a.binding, Version: 1, Profile: HandoffProfile, Mode: handoffMode, Capability: handoffCapability, ID: NewProbe(), ChannelID: channel, SignerBootID: a.boot, Inventory: state.Inventory, SeedSHA256: role.cfg.SeedSHA256, PolicySHA256: role.cfg.PolicySHA256, TransportSHA256: a.transportSHA, ObservedAt: now, Deadline: min(now+60, policy.NotAfter)}
			b, _ := json.Marshal(ch)
			state.Active = channel
			state.LastAt = now
			e = putHandoffRecord(&state, handoffRecord{ID: ch.ID, Channel: channel, Kind: "challenge", Body: b})
			if e == nil {
				e = a.publish(state, nil)
			}
			ch.HeadSHA256 = a.head
		}
	}
	a.mu.Unlock()
	if e != nil {
		fail("held")
		return
	}
	if reply("challenge", ch) != nil {
		return
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var q fixtureInspect
	var request FixtureHandoffRequest
	var exact []byte
	var result, confirmation fixturePublished
	var resultValue fixtureTransportResult
	retries := 0
	for frames := 0; frames < 4; frames++ {
		f, e = readFixtureFrame(conn, deadline)
		if e != nil {
			return
		}
		if f.Binding != role.cfg.FixtureBinding || f.ChannelID != channel {
			fail("conflict")
			return
		}
		if f.Kind == "inspect" && len(result.Payload) > 0 {
			if len(confirmation.Payload) > 0 || !bytes.Equal(f.Body, exact) || retries >= 2 || !s.live(channel, resultValue.Outcome.Deadline, result.HeadSHA256) {
				fail("conflict")
				return
			}
			retries++
			if reply("result", result) != nil {
				return
			}
			continue
		}
		if f.Kind == "confirm" && len(result.Payload) > 0 {
			confirm, e := wireDecode[fixtureConfirm](f, "confirm", channel)
			expected := fixtureConfirm{q.ExchangeDigest, digest(result.Payload)}
			if e != nil || confirm != expected {
				fail("conflict")
				return
			}
			if len(confirmation.Payload) > 0 {
				if retries >= 2 || !s.live(channel, resultValue.Outcome.Deadline, confirmation.HeadSHA256) {
					fail("expired")
					return
				}
				retries++
				if reply("confirmed", confirmation) != nil {
					return
				}
				continue
			}
			if !s.live(channel, resultValue.Outcome.Deadline, result.HeadSHA256) {
				fail("expired")
				return
			}
			_, at, e := func() (handoffCheckpoint, int64, error) { a.mu.Lock(); defer a.mu.Unlock(); return a.current(channel) }()
			if e != nil {
				fail("held")
				return
			}
			payload, _ := json.Marshal(fixtureConfirmed{confirm, request.ID, a.boot, at, resultValue.Outcome.Deadline})
			if e = role.localRecord(channel, NewProbe(), "checked", json.RawMessage(payload), true); e != nil {
				fail("held")
				return
			}
			confirmation = fixturePublished{payload, a.Head()}
			if reply("confirmed", confirmation) != nil {
				return
			}
			continue
		}
		if f.Kind != "inspect" || len(result.Payload) > 0 {
			fail("malformed")
			return
		}
		q, e = wireDecode[fixtureInspect](f, "inspect", channel)
		if e != nil || StrictJSON(q.Request, &request) != nil {
			fail("malformed")
			return
		}
		canonical, _ := json.Marshal(request)
		a.mu.Lock()
		state, now, e = a.current(channel)
		head := a.head
		a.mu.Unlock()
		if e != nil || !bytes.Equal(q.Request, canonical) || request.Challenge != ch || request.OwnerBootID != hello.OwnerBootID || !IDOK(request.ID) || !hashOK(request.OwnerHeadSHA256) || !request.Inventory.valid() || request.Inventory.InstanceID != ch.Inventory.InstanceID || request.Inventory.AuthoritySHA256 != ch.Inventory.AuthoritySHA256 || !hashOK(request.AuthorizationID) || !hashOK(request.UnitID) || request.Sequence == 0 || q.ExchangeDigest != q.digest(role.cfg, channel, hello.ClientNonce) || head != ch.HeadSHA256 || state.Inventory != ch.Inventory || now >= ch.Deadline || request.ReviewedAt <= 0 || request.ReviewedAt > 4102444800 || request.ObservedAt <= 0 || request.ObservedAt > 4102444800 || q.OwnerNotAfter <= now || q.OwnerNotAfter > 4102444800 || q.ReviewDeadline != min(q.OwnerNotAfter, ch.Deadline, request.ReviewedAt+3600) || now >= q.ReviewDeadline || now-request.ReviewedAt > 3600 || request.ReviewedAt > now+30 || abs64(now-request.ObservedAt) > 30 {
			fail("conflict")
			return
		}
		inspected, e := role.inspection(ctx, channel)
		if e != nil {
			fail("held")
			return
		}
		if inspected.authorization != request.AuthorizationID || inspected.unit != request.UnitID || inspected.sequence != request.Sequence || inspected.target != request.Target {
			fail("conflict")
			return
		}
		a.mu.Lock()
		_, now, e = a.current(channel)
		a.mu.Unlock()
		limit := min(ch.Deadline, q.OwnerNotAfter, q.ReviewDeadline, inspected.expiry)
		if e != nil || now >= limit {
			fail("expired")
			return
		}
		outcome := FixtureHandoffOutcome{Version: 1, Profile: HandoffProfile, Mode: handoffMode, FixtureOnly: true, RequestDigest: request.requestDigest(), RequestID: request.ID, ChallengeID: ch.ID, AuthorizationID: inspected.authorization, UnitID: inspected.unit, SignerBootID: a.boot, AuthoritySHA256: ch.Inventory.AuthoritySHA256, BaselineHead: ch.HeadSHA256, InventoryRevision: ch.Inventory.Revision, Sequence: inspected.sequence, ObservedAt: now, Deadline: limit, Outcome: "checked"}
		resultValue = fixtureTransportResult{outcome, q.ExchangeDigest, hello.OwnerBootID, hello.ClientNonce, channel}
		payload, _ := json.Marshal(resultValue)
		if e = role.localRecord(channel, request.ID, "checked", json.RawMessage(payload), false); e != nil {
			fail("held")
			return
		}
		result = fixturePublished{payload, a.Head()}
		exact = append([]byte(nil), f.Body...)
		if reply("result", result) != nil {
			return
		}
	}
}
func (s *FixtureSignerServer) live(channel string, deadline int64, head string) bool {
	a := s.role.actor
	a.mu.Lock()
	defer a.mu.Unlock()
	_, now, e := a.current(channel)
	return e == nil && now < deadline && a.head == head
}

// Review acknowledgment is private fixture state, never an app/session input.
func FixtureFileReview(ctx context.Context, r *FixtureTransportRole, review FixtureTransportReview, emit func(FixtureTransportReview)) bool {
	if r == nil || r.cfg.Role != "owner" || !hashOK(review.ExchangeDigest) {
		return false
	}
	emit(review)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	for {
		var ack struct {
			ExchangeDigest string `json:"exchangeDigest"`
			Approved       bool   `json:"approved"`
		}
		if b, e := r.read("review-ack.json"); e == nil {
			if StrictJSON(b, &ack) != nil || ack.ExchangeDigest != review.ExchangeDigest || !ack.Approved {
				return false
			}
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-ticker.C:
		}
	}
}
func FixtureTransportProbe(ctx context.Context, socket string, expectedUID int, payload []byte) ([]byte, error) {
	if e := fixtureTransportSupported(); e != nil {
		return nil, e
	}
	if os.Geteuid() == 0 || os.Getuid() != os.Geteuid() || expectedUID <= 0 || len(payload) > MaxDocument+4 {
		return nil, errors.New("fixture probe refused")
	}
	d := net.Dialer{Timeout: time.Second}
	conn, e := d.DialContext(ctx, "unix", socket)
	if e != nil {
		return nil, e
	}
	defer conn.Close()
	u, e := fixturePeerUID(conn.(*net.UnixConn))
	if e != nil || u != expectedUID {
		return nil, errors.New("unauthorized probe peer")
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if len(payload) > 0 {
		if _, e = conn.Write(payload); e != nil {
			return nil, e
		}
	}
	b := make([]byte, MaxDocument+4)
	n, e := conn.Read(b)
	if e != nil && e != io.EOF {
		return nil, e
	}
	return b[:n], nil
}

// A probe never opens role trust, review, inventory, wallet or lifecycle state.
type FixtureProbeConfig struct {
	Version     int    `json:"version"`
	Profile     string `json:"profile"`
	FixtureOnly bool   `json:"fixtureOnly"`
	Socket      string `json:"socket"`
	SignerUID   int    `json:"signerUid"`
}

func LoadFixtureProbeConfig(path, pin string) (c FixtureProbeConfig, err error) {
	b, e := privateFixtureRead(path)
	if e != nil || !hashOK(pin) || digest(b) != pin || StrictJSON(b, &c) != nil || c.Version != 1 || c.Profile != FixtureTransportProfile || !c.FixtureOnly || c.SignerUID <= 0 || !filepath.IsAbs(c.Socket) || filepath.Base(c.Socket) != "owner.sock" {
		return c, errors.New("private pinned fixture probe required")
	}
	return c, nil
}
