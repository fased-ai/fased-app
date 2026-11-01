package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type delayedUpdateChecker struct {
	*updateControllerFixture
	entered chan context.Context
	release chan struct{}
	calls   atomic.Int32
}

func (d *delayedUpdateChecker) Check(ctx context.Context) (*ApplicationRelease, error) {
	d.calls.Add(1)
	d.entered <- ctx
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-d.release:
		r := d.releaseValue()
		return &r, nil
	}
}
func (d *delayedUpdateChecker) releaseValue() ApplicationRelease {
	return d.updateControllerFixture.release
}
func TestApplicationUpdateAsyncCheckOutlivesTickAndCloses(t *testing.T) {
	s := fixtureStore(t)
	d := &delayedUpdateChecker{updateControllerFixture: fixtureUpdateController(), entered: make(chan context.Context, 1), release: make(chan struct{})}
	if e := s.BindApplicationUpdateController(d, true); e != nil {
		t.Fatal(e)
	}
	updateCommandFixture(t, s, "check")
	tick, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	if e := s.runApplicationUpdates(tick); e != nil {
		t.Fatal(e)
	}
	cancel()
	var job context.Context
	select {
	case job = <-d.entered:
	case <-time.After(time.Second):
		t.Fatal("check did not start")
	}
	if deadline, ok := job.Deadline(); !ok || time.Until(deadline) < time.Minute {
		t.Fatal("acquisition inherited five-second tick")
	}
	if e := s.runApplicationUpdates(t.Context()); e != nil {
		t.Fatal(e)
	}
	v, e := s.ApplicationUpdates(t.Context())
	if e != nil || v.Operation.Phase != "checking" || d.calls.Load() != 1 {
		t.Fatal(v, e)
	}
	close(d.release)
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for {
		v, e = s.ApplicationUpdates(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		if v.Operation.Phase == "available" {
			break
		}
		select {
		case <-timeout.C:
			t.Fatal("check did not finish", v)
		case <-time.After(time.Millisecond):
		}
	}
	if d.calls.Load() != 1 {
		t.Fatal("duplicate acquisition")
	}
}
func TestApplicationUpdateAsyncCloseCancelsAcquisition(t *testing.T) {
	s := fixtureStore(t)
	d := &delayedUpdateChecker{updateControllerFixture: fixtureUpdateController(), entered: make(chan context.Context, 1), release: make(chan struct{})}
	if e := s.BindApplicationUpdateController(d, true); e != nil {
		t.Fatal(e)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	select {
	case <-d.entered:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	done := make(chan struct{})
	go func() { s.closeApplicationChecks(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel/join update check")
	}
}
