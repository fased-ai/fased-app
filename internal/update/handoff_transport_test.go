//go:build linux

package update

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFixtureTransportBindings(t *testing.T) {
	if _, e := NewFixtureOwnerClient(nil, nil); e == nil {
		t.Fatal("implicit owner accepted")
	}
	if _, e := NewFixtureSignerServer(nil); e == nil {
		t.Fatal("implicit signer accepted")
	}
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only fixture transport")
	}
	t.Run("UT-11-no-public-same-UID-fallback", func(t *testing.T) {
		h := newTransportFixture(t)
		if _, e := LoadFixtureTransportRole(h.path("owner", "config.json"), h.pins["owner"], FixtureTransportLoadOptions{Initial: true}); e == nil {
			t.Fatal("public UID bypass")
		}
	})
	t.Run("UT-02-configured-peer-denial-supporting", func(t *testing.T) {
		h := newTransportFixture(t)
		r := h.load("signer", true)
		r.cfg.OwnerUID = os.Getuid() + 10000
		s := h.serve(r)
		before := r.actor.Head()
		conn, e := net.Dial("unix", r.cfg.Socket)
		if e != nil {
			t.Fatal(e)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		b, e := io.ReadAll(conn)
		if e != nil || len(b) != 0 {
			t.Fatal("unauthorized peer received data", e)
		}
		if before != r.actor.Head() {
			t.Fatal("unauthorized peer changed checkpoint")
		}
		_ = s
	})
	t.Run("UT-03-forged-server-UID-supporting", func(t *testing.T) {
		h := newTransportFixture(t)
		h.serve(h.load("signer", true))
		if _, e := FixtureTransportProbe(context.Background(), h.cfg["owner"].Socket, os.Getuid()+10000, nil); e == nil {
			t.Fatal("forged signer peer accepted")
		}
	})
	t.Run("UT-03-existing-socket-refused", func(t *testing.T) {
		h := newTransportFixture(t)
		r := h.load("signer", true)
		h.serve(r)
		if _, e := NewFixtureSignerServer(r); e == nil {
			t.Fatal("foreign/live socket adopted")
		}
	})
}
func TestFixtureTransportFraming(t *testing.T) {
	binding := FixtureBinding{true, RehearsalPurpose, NewProbe(), "development"}
	frame := transportFrame(binding, "hello", "", fixtureHello{NewProbe(), NewProbe(), "inspect", "", ""})
	valid, _ := json.Marshal(frame)
	encode := func(b []byte) []byte {
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], uint32(len(b)))
		return append(h[:], b...)
	}
	cases := map[string][]byte{"zero": {0, 0, 0, 0}, "oversize": {0, 1, 0, 1}, "partial-header": {0, 1}, "partial-body": {0, 0, 0, 8, 123}, "duplicate": encode(append([]byte(`{"version":1,`), valid[1:]...)), "unknown": encode(append([]byte(`{"unknown":true,`), valid[1:]...)), "alias": encode(append([]byte(`{"Version":1,`), valid[1:]...)), "profile": encode([]byte(strings.Replace(string(valid), FixtureTransportProfile, "production", 1))), "trailing": encode(append(valid, []byte(` {}`)...)), "utf8": encode([]byte{0xff})}
	for name, data := range cases {
		t.Run("UT-04-"+name, func(t *testing.T) {
			a, b := net.Pipe()
			defer a.Close()
			go func() { _, _ = b.Write(data); _ = b.Close() }()
			if _, e := readFixtureFrame(a, time.Now().Add(time.Second)); e == nil {
				t.Fatal("malformed frame accepted")
			}
		})
	}
	t.Run("UT-04-whole-frame-limit", func(t *testing.T) {
		a, b := net.Pipe()
		defer a.Close()
		defer b.Close()
		frame.Body = json.RawMessage(`"` + strings.Repeat("x", MaxDocument) + `"`)
		if writeFixtureFrame(a, frame, time.Now().Add(time.Second)) == nil {
			t.Fatal("oversized envelope")
		}
	})
}
func TestFixtureTransportBounds(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux transport")
	}
	t.Run("UT-10-corrupt-anchor", func(t *testing.T) {
		h := newTransportFixture(t)
		r := h.load("owner", true)
		h.write("owner", "anchor.json", []byte(`{}`))
		if _, e := r.inspection(context.Background(), NewProbe()); e == nil {
			t.Fatal("corrupt anchor accepted")
		}
	})
	t.Run("UT-10-pending-and-caps", func(t *testing.T) {
		h := newTransportFixture(t)
		r := h.load("signer", true)
		a := r.actor
		channel := NewProbe()
		a.mu.Lock()
		_, c, e := a.load()
		if e != nil {
			t.Fatal(e)
		}
		id := NewProbe()
		c.Active = channel
		c.Pending = id
		_ = putHandoffRecord(&c, handoffRecord{id, channel, "verification-pending", json.RawMessage(`{}`)})
		e = a.publish(c, nil)
		a.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = r.inspection(context.Background(), channel); e == nil {
			t.Fatal("unknown learning cleared")
		}
		if len(h.checkpoint(r).Records) != 1 {
			t.Fatal("pending state mutated")
		}
	})
	t.Run("UT-10-private-input-file", func(t *testing.T) {
		h := newTransportFixture(t)
		if e := os.Chmod(h.path("owner", "clock.json"), 0644); e != nil {
			t.Fatal(e)
		}
		if _, e := loadFixtureTransportRole(h.path("owner", "config.json"), h.pins["owner"], FixtureTransportLoadOptions{Initial: true}, true); e == nil {
			t.Fatal("public clock accepted")
		}
	})
	t.Run("UT-10-anchor-publication-retry", func(t *testing.T) {
		h := newTransportFixture(t)
		r := h.load("owner", true)
		a := r.actor
		advance := a.cfg.advanceAnchor
		once := true
		a.cfg.advanceAnchor = func(old, next string) error {
			if once {
				once = false
				return io.ErrClosedPipe
			}
			return advance(old, next)
		}
		a.mu.Lock()
		_, c, e := a.load()
		if e != nil {
			t.Fatal(e)
		}
		e = a.publish(c, nil)
		a.mu.Unlock()
		if e == nil || a.retry == nil {
			t.Fatal("anchor write not held")
		}
		if _, e = r.inspection(context.Background(), NewProbe()); e == nil {
			t.Fatal("anchor gap adopted")
		}
		if e = a.RetryPublication(); e != nil {
			t.Fatal(e)
		}
		if _, _, e = a.load(); e != nil {
			t.Fatal(e)
		}
	})
}
func TestFixtureTransportNoExecution(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux transport")
	}
	t.Run("UT-11-no-installed-or-mutation-path", func(t *testing.T) {
		h := newTransportFixture(t)
		before := h.canaries()
		h.serve(h.load("signer", true))
		c, e := NewFixtureOwnerClient(h.load("owner", true), func(v FixtureTransportReview) bool { return v.Review.UnitID == digest(tj(h.f.unit)) })
		if e != nil {
			t.Fatal(e)
		}
		out, e := c.Inspect(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if !disabledTransportOutcome(out.Outcome) || out.Outcome.Outcome != "inspection-complete" {
			t.Fatal("execution authority", out)
		}
		after := h.canaries()
		if !bytes.Equal(before, after) {
			t.Fatal("canaries changed")
		}
		for _, r := range h.roles {
			inv := h.checkpoint(r).Inventory
			if inv.InstalledFloor != h.f.seed.InstalledFloor || inv.ReservedFloor != h.f.seed.ReservedFloor {
				t.Fatal("floor mutation")
			}
		}
	})
}
