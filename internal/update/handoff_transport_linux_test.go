//go:build linux

package update

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type transportFixture struct {
	t        *testing.T
	f        *admissionFixture
	root     string
	cfg      map[string]FixtureTransportConfig
	pins     map[string]string
	roles    map[string]*FixtureTransportRole
	lastStop func()
}

func newTransportFixture(t *testing.T) *transportFixture {
	t.Helper()
	root, e := os.MkdirTemp("", "fht-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(root); e != nil {
			t.Error(e)
		}
	})
	h := &transportFixture{t: t, f: newAdmissionFixture(t), root: root, cfg: map[string]FixtureTransportConfig{}, pins: map[string]string{}, roles: map[string]*FixtureTransportRole{}}
	socket := filepath.Join(root, "socket")
	if e = os.Mkdir(socket, 0711); e != nil {
		t.Fatal(e)
	}
	_ = os.Chmod(socket, 0711)
	experiment := NewProbe()
	instance := NewProbe()
	for _, role := range []string{"owner", "signer"} {
		dir := filepath.Join(root, role)
		for _, d := range []string{dir, filepath.Join(dir, "store"), filepath.Join(dir, "artifacts")} {
			if e = os.Mkdir(d, 0700); e != nil {
				t.Fatal(e)
			}
		}
		for _, subject := range h.f.unit.Subjects {
			b, e := os.ReadFile(filepath.Join(h.f.dir, subject.File))
			if e != nil {
				t.Fatal(e)
			}
			h.write(role, "artifacts/"+subject.File, b)
		}
		c := FixtureTransportConfig{FixtureBinding: h.f.seed.FixtureBinding, Version: 1, Profile: FixtureTransportProfile, Experiment: experiment, Role: role, OwnerUID: os.Getuid(), SignerUID: os.Getuid(), AppUID: os.Getuid(), PrivateDir: dir, Socket: filepath.Join(socket, "owner.sock"), SeedSHA256: digest(tj(h.f.seed)), PolicySHA256: digest(tj(h.f.policy)), CASHA256: digest(h.f.ca)}
		h.cfg[role] = c
		b := tj(c)
		h.pins[role] = digest(b)
		for name, b := range map[string][]byte{"config.json": b, "seed.json": tj(h.f.seed), "policy.json": tj(h.f.policy), "ca.pem": h.f.ca, "evidence.json": tj(h.f.evidence), "clock.json": tj(map[string]any{"now": h.f.now, "confident": true}), "financial-canary": []byte("unopened originals/counters/holds " + role), "inventory.json": tj(FixtureHandoffInventory{InstanceID: instance, AuthoritySHA256: digest([]byte("disposable epoch")), Revision: 1, InstalledFloor: h.f.seed.InstalledFloor, ReservedFloor: h.f.seed.ReservedFloor}), "anchor.json": tj(fixtureTransportAnchor{1, FixtureTransportProfile, experiment, h.pins[role], ""})} {
			h.write(role, name, b)
		}
	}
	return h
}
func (h *transportFixture) path(role, name string) string { return filepath.Join(h.root, role, name) }
func (h *transportFixture) write(role, name string, b []byte) {
	h.t.Helper()
	if e := os.WriteFile(h.path(role, name), b, 0600); e != nil {
		h.t.Fatal(e)
	}
}
func (h *transportFixture) load(role string, initial bool) *FixtureTransportRole {
	h.t.Helper()
	r, e := loadFixtureTransportRole(h.path(role, "config.json"), h.pins[role], FixtureTransportLoadOptions{Initial: initial}, true)
	if e != nil {
		h.t.Fatal(e)
	}
	h.t.Cleanup(func() { _ = r.Close() })
	h.roles[role] = r
	return r
}
func (h *transportFixture) serve(r *FixtureTransportRole) *FixtureSignerServer {
	h.t.Helper()
	s, e := NewFixtureSignerServer(r)
	if e != nil {
		h.t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if e := <-done; e != nil {
				h.t.Error(e)
			}
		})
	}
	h.lastStop = stop
	h.t.Cleanup(stop)
	return s
}
func (h *transportFixture) checkpoint(r *FixtureTransportRole) handoffCheckpoint {
	h.t.Helper()
	r.actor.mu.Lock()
	defer r.actor.mu.Unlock()
	_, c, e := r.actor.load()
	if e != nil {
		h.t.Fatal(e)
	}
	return c
}
func (h *transportFixture) canaries() []byte {
	var b []byte
	for _, role := range []string{"owner", "signer"} {
		v, e := os.ReadFile(h.path(role, "financial-canary"))
		if e != nil {
			h.t.Fatal(e)
		}
		b = append(b, v...)
	}
	return b
}

func TestFixtureTransportIndependentPeers(t *testing.T) {
	if child := os.Getenv("FASED_TRANSPORT_TEST_CHILD"); child != "" {
		transportTestChild(t, child)
		return
	}
	t.Run("UT-01-separately-compiled-owner-and-signer", func(t *testing.T) {
		h := newTransportFixture(t)
		exe, e := os.Executable()
		if e != nil {
			t.Fatal(e)
		}
		start := func(role string) (*exec.Cmd, *bufio.Scanner, *bytes.Buffer) {
			cmd := exec.Command(exe, "-test.run=^TestFixtureTransportIndependentPeers$", "-test.timeout=35s")
			cmd.Env = append(os.Environ(), "FASED_TRANSPORT_TEST_CHILD="+role, "FASED_TRANSPORT_CONFIG="+h.path(role, "config.json"), "FASED_TRANSPORT_PIN="+h.pins[role])
			out, e := cmd.StdoutPipe()
			if e != nil {
				t.Fatal(e)
			}
			stderr := &bytes.Buffer{}
			cmd.Stderr = stderr
			if e = cmd.Start(); e != nil {
				t.Fatal(e)
			}
			return cmd, bufio.NewScanner(out), stderr
		}
		signer, scan, stderr := start("signer")
		t.Cleanup(func() { _ = signer.Process.Signal(syscall.SIGTERM); _ = signer.Wait() })
		if !scan.Scan() {
			t.Fatal("no signer ready", stderr.String())
		}
		var ready map[string]any
		if json.Unmarshal(scan.Bytes(), &ready) != nil || ready["event"] != "ready" {
			t.Fatal("invalid ready", scan.Text())
		}
		owner, scan, ownerErr := start("owner")
		events := 0
		var result FixtureTransportReport
		for scan.Scan() {
			var event struct {
				Event  string                 `json:"event"`
				Review FixtureTransportReview `json:"review"`
				Report FixtureTransportReport `json:"report"`
			}
			if json.Unmarshal(scan.Bytes(), &event) != nil {
				continue
			}
			switch event.Event {
			case "review":
				if event.Review.Review.UnitID != digest(tj(h.f.unit)) || len(event.Review.Review.Subjects) != 4 {
					t.Fatal("incorrect child review")
				}
				h.write("owner", "review-ack.json", tj(map[string]any{"exchangeDigest": event.Review.ExchangeDigest, "approved": true}))
				events++
			case "complete":
				result = event.Report
				events++
			}
		}
		if e = owner.Wait(); e != nil {
			t.Fatal(e, ownerErr.String())
		}
		if events != 2 || !disabledTransportOutcome(result.Outcome) || result.Outcome.Outcome != "inspection-complete" || owner.Process.Pid == signer.Process.Pid || owner.Process.Pid == os.Getpid() {
			t.Fatal("compiled boundary incomplete", events, result)
		}
		t.Logf("SUPPORTING local UIDs owner=%d signer=%d; separate PIDs %d/%d; result=%s", result.OwnerUID, result.SignerUID, owner.Process.Pid, signer.Process.Pid, result.ExchangeDigest)
	})
	for _, which := range []string{"signer-bytes", "review-denied", "owner-clock-back", "signer-time-uncertain", "equal-expiry", "clock-skew"} {
		t.Run("UT-06-UT-07-"+which, func(t *testing.T) {
			h := newTransportFixture(t)
			signer := h.load("signer", true)
			owner := h.load("owner", true)
			h.serve(signer)
			review := func(FixtureTransportReview) bool { return true }
			switch which {
			case "signer-bytes":
				h.write("signer", "artifacts/signer.fixture", []byte(inertArtifactPrefix+"changed"))
			case "review-denied":
				review = func(FixtureTransportReview) bool { return false }
			case "owner-clock-back":
				h.write("owner", "clock.json", tj(map[string]any{"now": h.f.now - 1, "confident": true}))
			case "signer-time-uncertain":
				h.write("signer", "clock.json", tj(map[string]any{"now": h.f.now, "confident": false}))
			case "equal-expiry":
				h.write("owner", "clock.json", tj(map[string]any{"now": h.f.policy.NotAfter, "confident": true}))
			case "clock-skew":
				h.write("signer", "clock.json", tj(map[string]any{"now": h.f.now + 31, "confident": true}))
			}
			c, e := NewFixtureOwnerClient(owner, review)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.Inspect(context.Background()); e == nil {
				t.Fatal("invalid independent inputs accepted")
			}
			if which == "signer-bytes" && h.checkpoint(signer).TrustSHA == "" {
				t.Fatal("signer dropped learned trust")
			}
		})
	}
}
func transportTestChild(t *testing.T, role string) {
	if role == "export" {
		transportExport(t)
		return
	}
	r, e := loadFixtureTransportRole(os.Getenv("FASED_TRANSPORT_CONFIG"), os.Getenv("FASED_TRANSPORT_PIN"), FixtureTransportLoadOptions{Initial: true}, true)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	encode := func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
	if role == "signer" {
		s, e := NewFixtureSignerServer(r)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		encode(map[string]any{"event": "ready", "pid": os.Getpid(), "uid": os.Getuid()})
		if e = s.Serve(ctx); e != nil {
			t.Fatal(e)
		}
		return
	}
	if role != "owner" {
		t.Fatal("unknown child")
	}
	c, e := NewFixtureOwnerClient(r, func(v FixtureTransportReview) bool {
		return FixtureFileReview(ctx, r, v, func(v FixtureTransportReview) { encode(map[string]any{"event": "review", "review": v}) })
	})
	if e != nil {
		t.Fatal(e)
	}
	out, e := c.Inspect(ctx)
	if e != nil {
		t.Fatal(e)
	}
	encode(map[string]any{"event": "complete", "report": out})
}
func transportExport(t *testing.T) {
	dir := os.Getenv("FASED_TRANSPORT_EXPORT")
	if dir == "" || os.Geteuid() == 0 {
		t.Fatal("explicit ordinary-user export fixture required")
	}
	f := newAdmissionFixture(t)
	var timestamps atomic.Int64
	handler := f.server.Config.Handler
	f.server.Close()
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tuf/timestamp.json" {
			count := timestamps.Add(1)
			_ = os.WriteFile(filepath.Join(dir, "timestamp-count.json"), tj(count), 0600)
			var control struct {
				PauseTimestamp int64 `json:"pauseTimestamp"`
			}
			b, _ := os.ReadFile(filepath.Join(dir, "control.json"))
			_ = json.Unmarshal(b, &control)
			if control.PauseTimestamp == count {
				_ = os.WriteFile(filepath.Join(dir, "gate-hit.json"), tj(map[string]any{"count": count}), 0600)
				for {
					if _, e := os.Stat(filepath.Join(dir, "release")); e == nil {
						break
					}
					select {
					case <-r.Context().Done():
						return
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(f.server.Close)
	f.seed.Origin = f.server.URL
	f.ca = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw})
	artifacts := map[string][]byte{}
	for _, subject := range f.unit.Subjects {
		b, e := os.ReadFile(filepath.Join(f.dir, subject.File))
		if e != nil {
			t.Fatal(e)
		}
		artifacts[subject.File] = b
	}
	bundle := map[string]any{"seed": json.RawMessage(tj(f.seed)), "policy": json.RawMessage(tj(f.policy)), "evidence": json.RawMessage(tj(f.evidence)), "ca": f.ca, "artifacts": artifacts, "now": f.now, "unitSha256": digest(tj(f.unit)), "identity": f.unit.Identity}
	if e := os.WriteFile(filepath.Join(dir, "bundle.json"), tj(bundle), 0600); e != nil {
		t.Fatal(e)
	}
	fmt.Println("fixture public inputs ready; signing keys memory-only")
	until := time.NewTimer(180 * time.Second)
	defer until.Stop()
	for {
		if _, e := os.Stat(filepath.Join(dir, "done")); e == nil {
			return
		}
		select {
		case <-until.C:
			t.Fatal("export fixture control timed out")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

type transportExchange struct {
	conn           *net.UnixConn
	channel, nonce string
	request        FixtureHandoffRequest
	q              fixtureInspect
	owner          *FixtureTransportRole
	signer         *FixtureTransportRole
	server         *FixtureSignerServer
}

func (h *transportFixture) exchange() *transportExchange {
	t := h.t
	t.Helper()
	signer := h.load("signer", true)
	owner := h.load("owner", true)
	s := h.serve(signer)
	nonce := NewProbe()
	i, e := owner.inspection(context.Background(), nonce)
	if e != nil {
		t.Fatal(e)
	}
	conn, e := fixtureDial(context.Background(), owner)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = conn.Close(); owner.cancel(nonce) })
	deadline := time.Now().Add(10 * time.Second)
	hello := fixtureHello{nonce, owner.actor.boot, "inspect", "", ""}
	if e = writeFixtureFrame(conn, transportFrame(owner.cfg.FixtureBinding, "hello", "", hello), deadline); e != nil {
		t.Fatal(e)
	}
	frame, e := readFixtureFrame(conn, deadline)
	if e != nil {
		t.Fatal(e)
	}
	ch, e := wireDecode[FixtureHandoffChallenge](frame, "challenge", frame.ChannelID)
	if e != nil {
		t.Fatal(e)
	}
	a := owner.actor
	a.mu.Lock()
	state, now, e := a.current(nonce)
	head := a.head
	a.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	request := FixtureHandoffRequest{ch, NewProbe(), a.boot, state.Inventory, head, i.authorization, i.unit, i.target, i.sequence, i.at, now}
	b := tj(request)
	q := fixtureInspect{Request: b, OwnerNotAfter: i.expiry, ReviewDeadline: min(i.expiry, ch.Deadline, now+3600)}
	q.ExchangeDigest = q.digest(owner.cfg, ch.ChannelID, nonce)
	return &transportExchange{conn, ch.ChannelID, nonce, request, q, owner, signer, s}
}
func (x *transportExchange) send(t *testing.T, kind string, body any) fixtureTransportFrame {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	if e := writeFixtureFrame(x.conn, transportFrame(x.owner.cfg.FixtureBinding, kind, x.channel, body), deadline); e != nil {
		t.Fatal(e)
	}
	f, e := readFixtureFrame(x.conn, deadline)
	if e != nil {
		t.Fatal(e)
	}
	return f
}
func TestFixtureTransportReplayRecovery(t *testing.T) {
	t.Run("UT-05-live-exact-retry-and-confirmation", func(t *testing.T) {
		h := newTransportFixture(t)
		x := h.exchange()
		f := x.send(t, "inspect", x.q)
		result, e := wireDecode[fixturePublished](f, "result", x.channel)
		if e != nil {
			t.Fatal(e, string(f.Body))
		}
		head := x.signer.actor.Head()
		again := x.send(t, "inspect", x.q)
		if !bytes.Equal(f.Body, again.Body) || head != x.signer.actor.Head() {
			t.Fatal("retry re-admitted or changed result")
		}
		confirm := fixtureConfirm{x.q.ExchangeDigest, digest(result.Payload)}
		f = x.send(t, "confirm", confirm)
		if f.Kind != "confirmed" {
			t.Fatal("confirmation", string(f.Body))
		}
		head = x.signer.actor.Head()
		again = x.send(t, "confirm", confirm)
		if !bytes.Equal(f.Body, again.Body) || head != x.signer.actor.Head() {
			t.Fatal("confirmation retry changed result")
		}
		owner, e := NewFixtureOwnerClient(x.owner, nil)
		if e != nil {
			t.Fatal(e)
		}
		audit, e := owner.Audit(context.Background(), x.request.ID, x.q.ExchangeDigest)
		if e != nil || !bytes.Contains(audit, []byte(`"checked"`)) {
			t.Fatal("historical audit", e)
		}
	})
	for _, field := range []string{"exchange", "channel", "expiry", "review", "owner-boot", "unit", "challenge"} {
		t.Run("UT-05-substituted-"+field, func(t *testing.T) {
			h := newTransportFixture(t)
			x := h.exchange()
			q := x.q
			request := x.request
			switch field {
			case "exchange":
				q.ExchangeDigest = digest([]byte("other"))
			case "channel":
				x.channel = NewProbe()
			case "expiry":
				q.OwnerNotAfter++
			case "review":
				q.ReviewDeadline--
			case "owner-boot":
				request.OwnerBootID = NewProbe()
				q.Request = tj(request)
				q.ExchangeDigest = q.digest(x.owner.cfg, x.channel, x.nonce)
			case "unit":
				request.UnitID = digest([]byte("other"))
				q.Request = tj(request)
				q.ExchangeDigest = q.digest(x.owner.cfg, x.channel, x.nonce)
			case "challenge":
				request.Challenge.ChannelID = NewProbe()
				q.Request = tj(request)
				q.ExchangeDigest = q.digest(x.owner.cfg, x.channel, x.nonce)
			}
			f := x.send(t, "inspect", q)
			if f.Kind != "error" {
				t.Fatal("substitution accepted", f.Kind)
			}
		})
	}
	t.Run("UT-08-restart-fences-result-and-audit-is-historical", func(t *testing.T) {
		h := newTransportFixture(t)
		x := h.exchange()
		f := x.send(t, "inspect", x.q)
		if f.Kind != "result" {
			t.Fatal("missing result", string(f.Body))
		}
		oldBoot := x.signer.actor.boot
		h.lastStop()
		if e := os.Remove(x.signer.cfg.Socket); e != nil {
			t.Fatal(e)
		}
		newRole := h.load("signer", false)
		h.serve(newRole)
		if newRole.actor.boot == oldBoot {
			t.Fatal("boot not changed")
		}
		c, e := NewFixtureOwnerClient(x.owner, nil)
		if e != nil {
			t.Fatal(e)
		}
		b, e := c.Audit(context.Background(), x.request.ID, x.q.ExchangeDigest)
		if e != nil {
			t.Fatal(e)
		}
		var result fixtureTransportResult
		if StrictJSON(b, &result) != nil || result.Outcome.SignerBootID != oldBoot {
			t.Fatal("historical record changed")
		}
		_ = x.conn.SetDeadline(time.Now().Add(time.Second))
		if _, e = x.conn.Read(make([]byte, 1)); e == nil {
			t.Fatal("old live context survived")
		}
	})
	t.Run("UT-09-pending-restart-preserves-unknown-learning", func(t *testing.T) {
		h := newTransportFixture(t)
		r := h.load("signer", true)
		a := r.actor
		a.mu.Lock()
		_, c, e := a.load()
		if e != nil {
			t.Fatal(e)
		}
		id, channel := NewProbe(), NewProbe()
		c.Active = channel
		c.Pending = id
		_ = putHandoffRecord(&c, handoffRecord{id, channel, "verification-pending", json.RawMessage(`{}`)})
		e = a.publish(c, nil)
		a.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		next := h.load("signer", false)
		if h.checkpoint(next).Pending != id {
			t.Fatal("pending learning erased")
		}
		if _, e = next.inspection(context.Background(), NewProbe()); e == nil {
			t.Fatal("unknown learning resumed")
		}
	})
	t.Run("UT-10-one-active-inspection", func(t *testing.T) {
		h := newTransportFixture(t)
		x := h.exchange()
		conn, e := fixtureDial(context.Background(), x.owner)
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		head := x.signer.actor.Head()
		hello := fixtureHello{NewProbe(), x.owner.actor.boot, "inspect", "", ""}
		deadline := time.Now().Add(time.Second)
		_ = writeFixtureFrame(conn, transportFrame(x.owner.cfg.FixtureBinding, "hello", "", hello), deadline)
		f, e := readFixtureFrame(conn, deadline)
		if e != nil || f.Kind != "error" || !bytes.Contains(f.Body, []byte(`busy`)) {
			t.Fatal("concurrent challenge", e, string(f.Body))
		}
		if head != x.signer.actor.Head() {
			t.Fatal("busy connection wrote state")
		}
	})
}
