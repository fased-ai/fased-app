//go:build linux

package update

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

type inspectionFrame struct {
	Version    int             `json:"version"`
	Profile    string          `json:"profile"`
	Capability string          `json:"capability"`
	Provision  string          `json:"provision"`
	Channel    string          `json:"channel"`
	Kind       string          `json:"kind"`
	Body       json.RawMessage `json:"body"`
}

func inspectionWrite(c net.Conn, in *inspectionInputs, channel, kind string, body any, deadline time.Time) error {
	f := inspectionFrame{1, inspectionProfile, inspectionCapability, in.role.Provision, channel, kind, mustInspectionJSON(body)}
	b := mustInspectionJSON(f)
	if len(b) > MaxDocument {
		return errors.New("inspection frame bound")
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if e := c.SetWriteDeadline(earlier(deadline, time.Now().Add(5*time.Second))); e != nil {
		return e
	}
	_, e := io.Copy(c, bytes.NewReader(append(h[:], b...)))
	return e
}
func inspectionRawFrame(c net.Conn, in *inspectionInputs, channel string, deadline time.Time) (inspectionFrame, error) {
	var empty inspectionFrame
	if e := c.SetReadDeadline(deadline); e != nil {
		return empty, e
	}
	var h [4]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return empty, e
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > MaxDocument {
		return empty, errors.New("inspection frame bound")
	}
	if e := c.SetReadDeadline(earlier(deadline, time.Now().Add(5*time.Second))); e != nil {
		return empty, e
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(c, b); e != nil {
		return empty, e
	}
	var f inspectionFrame
	if StrictJSON(b, &f) != nil || f.Version != 1 || f.Profile != inspectionProfile || f.Capability != inspectionCapability || f.Provision != in.role.Provision || f.Channel != channel {
		return empty, errors.New("closed fixture inspection frame refused")
	}
	switch f.Kind {
	case "hello", "challenge", "inspect", "result", "confirm", "confirmed", "status", "status-result", "error":
	default:
		return empty, errors.New("unknown inspection frame kind")
	}
	return f, nil
}

func inspectionReadFrame(c net.Conn, in *inspectionInputs, channel, kind string, body any, deadline time.Time) error {
	f, e := inspectionRawFrame(c, in, channel, deadline)
	if e != nil {
		return e
	}
	if f.Kind != kind || StrictJSON(f.Body, body) != nil {
		return errors.New("inspection frame state refused")
	}
	return nil
}

type inspectionHello struct {
	Nonce  string          `json:"nonce"`
	Boot   string          `json:"boot"`
	Head   string          `json:"head"`
	Source string          `json:"source"`
	Core   CoreSnapshot    `json:"core"`
	Unit   string          `json:"unit"`
	Clock  inspectionClock `json:"clock"`
}
type inspectionChallenge struct {
	ID     string          `json:"id"`
	Boot   string          `json:"boot"`
	Head   string          `json:"head"`
	Source string          `json:"source"`
	Signer SignerSnapshot  `json:"signer"`
	Unit   string          `json:"unit"`
	Clock  inspectionClock `json:"clock"`
}
type inspectionConsent struct {
	Proposal   string `json:"proposal"`
	ReviewedAt int64  `json:"reviewedAt"`
	Consent    string `json:"consent"`
	Review     string `json:"review"`
}

func installedInspectionProposal(in *inspectionInputs, channel string, h inspectionHello, c inspectionChallenge) string {
	return hashTuple("fased-app-installed-inspection-fixture-proposal:v1", inspectionProfile, inspectionCapability, "1", in.catalog.Provision, in.catalog.Installation, in.pin, in.catalog.Roles[0], in.catalog.Roles[1], string(mustInspectionJSON(in.catalog.Reader)), in.catalog.CandidateImages[0], in.catalog.CandidateImages[1], channel, string(mustInspectionJSON(h)), string(mustInspectionJSON(c)))
}
func installedInspectionReview(p string, at int64, consent string) string {
	return hashTuple("fased-app-installed-inspection-fixture-review:v1", p, string(mustInspectionJSON(at)), consent)
}
func inspectionSafeSigner(s SignerSnapshot) bool {
	return s.Version == 1 && IDOK(s.BootID) && hashOK(s.AuthoritySHA256) && s.ActiveIntentID == "" && s.Pending == 0 && s.Unknown == 0 && !s.RecoveryHold
}

type InstalledInspectionServer struct {
	listener    *net.UnixListener
	role        *InstalledInspectionRole
	wg          sync.WaitGroup
	mu          sync.Mutex
	connections map[*net.UnixConn]bool
	closed      bool
	active      chan struct{}
	slots       chan struct{}
}

// snapshot is supplied only by the actual signer Vault, never by configuration,
// an app inventory file, an acknowledgement or decoded protocol data.
func NewInstalledInspectionServer(ctx context.Context, role *InstalledInspectionRole, snapshot func(string) (SignerSnapshot, error)) (*InstalledInspectionServer, error) {
	if role == nil || role.role != "signer" || snapshot == nil {
		return nil, errors.New("installed signer role required")
	}
	in := role.session.store.in
	socket := role.root + "/signer-ipc/inspection.sock"
	if st, e := os.Lstat(socket); e == nil {
		v, ok := st.Sys().(*syscall.Stat_t)
		if !ok || st.Mode()&os.ModeSocket == 0 || int(v.Uid) != os.Getuid() {
			return nil, errors.New("foreign inspection socket preserved")
		}
		if e = os.Remove(socket); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	l, e := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if e != nil {
		return nil, e
	}
	l.SetUnlinkOnClose(true)
	if e = os.Chmod(socket, 0666); e != nil {
		l.Close()
		return nil, e
	}
	s := &InstalledInspectionServer{listener: l, role: role, connections: map[*net.UnixConn]bool{}, active: make(chan struct{}, 1), slots: make(chan struct{}, 4)}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, e := l.AcceptUnix()
			if e != nil {
				return
			}
			uid, e := fixturePeerUID(c)
			if e != nil || uid != in.catalog.UIDs[0] {
				c.Close()
				continue
			}
			select {
			case s.slots <- struct{}{}:
			default:
				c.Close()
				continue
			}
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				c.Close()
				<-s.slots
				return
			}
			s.connections[c] = true
			s.wg.Add(1)
			s.mu.Unlock()
			go func() {
				defer s.wg.Done()
				defer func() { c.Close(); s.mu.Lock(); delete(s.connections, c); s.mu.Unlock(); <-s.slots }()
				s.handle(ctx, c, snapshot)
			}()
		}
	}()
	return s, nil
}
func (s *InstalledInspectionServer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.listener.Close()
	for c := range s.connections {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	s.role.Close()
}
func (s *InstalledInspectionServer) handle(ctx context.Context, c *net.UnixConn, snapshot func(string) (SignerSnapshot, error)) {
	select {
	case s.active <- struct{}{}:
		defer func() { <-s.active }()
	default:
		return
	}
	deadline := time.Now().Add(60 * time.Second)
	in := s.role.session.store.in
	first, e := inspectionRawFrame(c, in, "", deadline)
	if e != nil {
		return
	}
	if first.Kind == "status" {
		var req struct {
			Nonce string `json:"nonce"`
		}
		if StrictJSON(first.Body, &req) != nil || !IDOK(req.Nonce) {
			return
		}
		b, e := s.role.session.store.load()
		if e != nil {
			return
		}
		cp, e := s.role.session.store.parse(b)
		if e != nil {
			return
		}
		status := InstalledInspectionStatus{FixtureOnly: true, Historical: true, Profile: inspectionProfile, Head: digest(b), Held: cp.Pending != "", CatalogSHA: in.pin}
		_ = inspectionWrite(c, in, req.Nonce, "status-result", status, deadline)
		return
	}
	var hello inspectionHello
	if first.Kind != "hello" || StrictJSON(first.Body, &hello) != nil || !IDOK(hello.Nonce) || !IDOK(hello.Boot) || !hashOK(hello.Head) || !hashOK(hello.Source) {
		return
	}
	channel := hello.Nonce
	own, e := s.role.observe(ctx)
	if e != nil {
		return
	}
	sign, e := snapshot(channel)
	if e != nil || !inspectionSafeSigner(sign) {
		return
	}
	if _, e = JoinSnapshots(hello.Core, sign, Target{"linux", "amd64"}); e != nil {
		return
	}
	var seed TrustFixtureSeed
	if StrictJSON(in.seed, &seed) != nil || seed.InstalledFloor != sign.InstalledSequence || seed.ReservedFloor != sign.SequenceFloor {
		return
	}
	clock, e := in.clock()
	if e != nil {
		return
	}
	challenge := inspectionChallenge{NewProbe(), s.role.boot, s.role.head(), in.source.digest, sign, own.Admission.UnitSHA256, clock}
	if hello.Unit != challenge.Unit || inspectionWrite(c, in, channel, "challenge", challenge, deadline) != nil {
		return
	}
	var consent inspectionConsent
	if inspectionReadFrame(c, in, channel, "inspect", &consent, deadline) != nil {
		return
	}
	proposal := installedInspectionProposal(in, channel, hello, challenge)
	if consent.Proposal != proposal || consent.Consent != "INSPECT FIXTURE "+proposal || consent.ReviewedAt < hello.Clock.UTC || consent.ReviewedAt > hello.Clock.UTC+60 || consent.Review != installedInspectionReview(proposal, consent.ReviewedAt, consent.Consent) {
		return
	}
	sign2, e := snapshot(channel)
	if e != nil || SnapshotDigest("signer", sign2) != SnapshotDigest("signer", sign) {
		return
	}
	now, e := in.clock()
	if e != nil || now != clock || !time.Now().Before(deadline) {
		return
	}
	out := own
	out.Core = hello.Core
	out.Signer = sign
	out.Sources = [2]string{hello.Source, challenge.Source}
	out.Proposal = proposal
	out.Review = consent.Review
	out.Exchange = hashTuple("fased-app-installed-inspection-fixture-exchange:v1", proposal, consent.Review)
	if inspectionWrite(c, in, channel, "result", out, deadline) != nil {
		return
	}
	for retries := 0; ; retries++ {
		frame, e := inspectionRawFrame(c, in, channel, deadline)
		if e != nil {
			return
		}
		if frame.Kind == "inspect" && retries == 0 {
			var replay inspectionConsent
			if StrictJSON(frame.Body, &replay) != nil || replay != consent {
				return
			}
			if inspectionWrite(c, in, channel, "result", out, deadline) != nil {
				return
			}
			continue
		}
		var confirm struct {
			Exchange string `json:"exchange"`
		}
		if frame.Kind != "confirm" || StrictJSON(frame.Body, &confirm) != nil || confirm.Exchange != out.Exchange {
			return
		}
		break
	}
	fresh, e := s.role.observe(ctx)
	if e != nil || fresh.Admission.UnitSHA256 != out.Admission.UnitSHA256 {
		return
	}
	sign2, e = snapshot(channel)
	if e != nil || SnapshotDigest("signer", sign2) != SnapshotDigest("signer", sign) {
		return
	}
	_ = inspectionWrite(c, in, channel, "confirmed", out, deadline)
}

func InspectInstalledFixture(ctx context.Context, role *InstalledInspectionRole, probe func(context.Context, string) (CoreSnapshot, error), review func(string, InstalledInspectionStatus) error) (InstalledInspectionStatus, error) {
	var out InstalledInspectionStatus
	if role == nil || role.role != "owner" || probe == nil || review == nil {
		return out, errors.New("protected installed owner helper required")
	}
	in := role.session.store.in
	deadline := time.Now().Add(60 * time.Second)
	nonce := NewProbe()
	core, e := probe(ctx, nonce)
	if e != nil {
		return out, e
	}
	own, e := role.observe(ctx)
	if e != nil {
		return out, e
	}
	clock, e := in.clock()
	if e != nil {
		return out, e
	}
	d := net.Dialer{}
	conn, e := d.DialContext(ctx, "unix", role.root+"/signer-ipc/inspection.sock")
	if e != nil {
		return out, e
	}
	defer conn.Close()
	c := conn.(*net.UnixConn)
	uid, e := fixturePeerUID(c)
	if e != nil || uid != in.catalog.UIDs[2] {
		return out, errors.New("kernel signer UID refused")
	}
	hello := inspectionHello{nonce, role.boot, role.head(), in.source.digest, core, own.Admission.UnitSHA256, clock}
	if e = inspectionWrite(c, in, "", "hello", hello, deadline); e != nil {
		return out, e
	}
	var challenge inspectionChallenge
	if e = inspectionReadFrame(c, in, nonce, "challenge", &challenge, deadline); e != nil {
		return out, e
	}
	if !IDOK(challenge.ID) || !IDOK(challenge.Boot) || !hashOK(challenge.Head) || !hashOK(challenge.Source) || challenge.Unit != hello.Unit || !inspectionSafeSigner(challenge.Signer) {
		return out, errors.New("invalid independent signer challenge")
	}
	if _, e = JoinSnapshots(core, challenge.Signer, Target{"linux", "amd64"}); e != nil {
		return out, e
	}
	proposal := installedInspectionProposal(in, nonce, hello, challenge)
	own.Proposal = proposal
	own.Core = core
	own.Signer = challenge.Signer
	own.Sources = [2]string{hello.Source, challenge.Source}
	if e = review("INSPECT FIXTURE "+proposal, own); e != nil {
		return out, e
	}
	now, e := in.clock()
	if e != nil || now != clock || !time.Now().Before(deadline) {
		return out, errors.New("owner review clock/deadline changed")
	}
	core2, e := probe(ctx, nonce)
	if e != nil || SnapshotDigest("core", core2) != SnapshotDigest("core", core) {
		return out, errors.New("core changed during review")
	}
	consent := inspectionConsent{proposal, now.UTC, "INSPECT FIXTURE " + proposal, ""}
	consent.Review = installedInspectionReview(proposal, consent.ReviewedAt, consent.Consent)
	if e = inspectionWrite(c, in, nonce, "inspect", consent, deadline); e != nil {
		return out, e
	}
	if e = inspectionReadFrame(c, in, nonce, "result", &out, deadline); e != nil {
		return out, e
	}
	exchange := hashTuple("fased-app-installed-inspection-fixture-exchange:v1", proposal, consent.Review)
	if digest(mustInspectionJSON(out.Unit)) != own.Admission.UnitSHA256 || out.ProvenanceSHA != own.ProvenanceSHA || out.CatalogSHA != in.pin || SnapshotDigest("core", out.Core) != SnapshotDigest("core", core) || SnapshotDigest("signer", out.Signer) != SnapshotDigest("signer", challenge.Signer) || out.Profile != inspectionProfile || !out.FixtureOnly || !out.Historical || out.Exchange != exchange || out.Proposal != proposal || out.Review != consent.Review || out.Admission.UnitSHA256 != own.Admission.UnitSHA256 || out.InstallAllowed || out.ResumeAllowed || out.WalletSigner || out.FinancialExecution || out.UpdaterEnabled || (out.Admission.InstallAllowed || out.Admission.ResumeAllowed || out.Admission.UpdaterEnabled || out.Admission.WalletSigner || out.Admission.FinancialExecution) {
		return InstalledInspectionStatus{}, errors.New("inspection result binding/capability refused")
	}
	refreshed, e := role.observe(ctx)
	if e != nil || refreshed.Admission.UnitSHA256 != own.Admission.UnitSHA256 {
		return InstalledInspectionStatus{}, errors.New("owner final refresh refused")
	}
	core2, e = probe(ctx, nonce)
	if e != nil || SnapshotDigest("core", core2) != SnapshotDigest("core", core) {
		return InstalledInspectionStatus{}, errors.New("core changed before confirmation")
	}
	if e = inspectionWrite(c, in, nonce, "confirm", struct {
		Exchange string `json:"exchange"`
	}{exchange}, deadline); e != nil {
		return InstalledInspectionStatus{}, e
	}
	var final InstalledInspectionStatus
	if e = inspectionReadFrame(c, in, nonce, "confirmed", &final, deadline); e != nil || string(mustInspectionJSON(final)) != string(mustInspectionJSON(out)) {
		return InstalledInspectionStatus{}, errors.New("same-channel confirmation refused")
	}
	return out, nil
}

// A fresh authenticated channel yields historical data only, with no challenge,
// review/exchange, trust learning or reusable inspection completion.
func InstalledFixtureStatus(ctx context.Context, role *InstalledInspectionRole) (InstalledInspectionStatus, error) {
	var out InstalledInspectionStatus
	if role == nil || role.role != "owner" {
		return out, errors.New("installed owner role required")
	}
	in := role.session.store.in
	d := net.Dialer{}
	conn, e := d.DialContext(ctx, "unix", role.root+"/signer-ipc/inspection.sock")
	if e != nil {
		return out, e
	}
	defer conn.Close()
	c := conn.(*net.UnixConn)
	uid, e := fixturePeerUID(c)
	if e != nil || uid != in.catalog.UIDs[2] {
		return out, errors.New("signer UID refused")
	}
	nonce := NewProbe()
	deadline := time.Now().Add(10 * time.Second)
	if e = inspectionWrite(c, in, "", "status", struct {
		Nonce string `json:"nonce"`
	}{nonce}, deadline); e != nil {
		return out, e
	}
	if e = inspectionReadFrame(c, in, nonce, "status-result", &out, deadline); e != nil {
		return out, e
	}
	if !out.FixtureOnly || !out.Historical || out.Profile != inspectionProfile || !hashOK(out.Head) || out.Exchange != "" || out.Review != "" || out.Proposal != "" || out.InstallAllowed || out.ResumeAllowed || out.WalletSigner || out.FinancialExecution || out.UpdaterEnabled {
		return InstalledInspectionStatus{}, errors.New("historical status binding refused")
	}
	return out, nil
}
