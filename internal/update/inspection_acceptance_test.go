package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Crash disposition for BOTH pending and completed publications, including the
// independent anchor. A durable completion can reopen only as historical data.
func TestInspectionAcceptancePublicationCuts(t *testing.T) {
	if !inspectionSupported(t) {
		return
	}
	base := newInspectionTest(t)
	for _, role := range []string{"owner", "signer"} {
		for _, stage := range []string{"pending", "result"} {
			for _, side := range []string{"state", "anchor"} {
				for _, point := range []string{"partial-snapshot", "partial-head", "published"} {
					if side == "anchor" && point == "partial-snapshot" {
						continue
					}
					t.Run(role+"/"+stage+"/"+side+"/"+point, func(t *testing.T) {
						x := base.clone(t)
						a := x.session(t, role)
						if _, e := a.inspect(context.Background()); e != nil {
							t.Fatal(e)
						}
						before := append([]byte(nil), a.previous...)
						store := a.store.state
						if side == "anchor" {
							store = a.store.anchor
						}
						wanted := 1
						if stage == "result" {
							wanted = 2
						}
						calls := 0
						store.fault = func(phase string) error {
							if phase == point {
								calls++
								if calls == wanted {
									return errors.New("disposable publication cut")
								}
							}
							return nil
						}
						if _, e := a.inspect(context.Background()); e == nil || calls != wanted {
							t.Fatalf("cut not reached %d: %v", calls, e)
						}
						// A restart may see only the previous complete checkpoint, a held gap or
						// the newly durable historical checkpoint. It must not manufacture success.
						reopened, e := newInspectionSession(a.store, false)
						if e == nil {
							cp, e := a.store.parse(reopened.previous)
							if e != nil || cp.Pending != "" || !cp.Historical {
								t.Fatal("restart invented completion", e)
							}
							if string(reopened.previous) != string(before) && stage != "result" {
								t.Fatal("pending cut became completion")
							}
						}
						if _, e := newInspectionSession(a.store, true); e == nil {
							t.Fatal("nonempty state reinitialized")
						}
						store.fault = nil
						if e := a.retryPublication(); e != nil {
							t.Fatal("exact live retry", e)
						}
						if e := a.retryPublication(); e == nil {
							t.Fatal("retry reused after completion")
						}
						b, e := a.store.load()
						if e != nil {
							t.Fatal(e)
						}
						cp, e := a.store.parse(b)
						if e != nil {
							t.Fatal(e)
						}
						if (cp.Pending != "") != (stage == "pending") {
							t.Fatal("retry changed publication meaning")
						}
					})
				}
			}
		}
	}
}
func TestInspectionAcceptanceWholeStoreRollback(t *testing.T) {
	if !inspectionSupported(t) {
		return
	}
	x := newInspectionTest(t)
	a := x.session(t, "owner")
	if _, e := a.inspect(context.Background()); e != nil {
		t.Fatal(e)
	}
	old, e := os.ReadFile(filepath.Join(x.dir, "owner-state/head.json"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e := a.inspect(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(x.dir, "owner-state/head.json"), old, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := newInspectionSession(a.store, false); e == nil {
		t.Fatal("store-only rollback escaped independent anchor")
	}
}

func TestInspectionAcceptanceClockSkew(t *testing.T) {
	x := newInspectionTest(t)
	var a, b inspectionClock
	if StrictJSON(x.files["owner-clock.json"], &a) != nil || StrictJSON(x.files["signer-clock.json"], &b) != nil {
		t.Fatal("clock fixture")
	}
	for _, delta := range []int64{-31, -30, 0, 30, 31} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			c := b
			c.UTC += delta
			if got := inspectionClocksAgree(a, c); got != (delta >= -30 && delta <= 30) {
				t.Fatal("role skew", delta, got)
			}
		})
	}
	for _, field := range []string{"uncertain", "role", "provision", "profile", "revision"} {
		t.Run(field, func(t *testing.T) {
			c := b
			switch field {
			case "uncertain":
				c.Confident = false
			case "role":
				c.Role = "owner"
			case "provision":
				c.Provision = NewProbe()
			case "profile":
				c.Profile = "production"
			case "revision":
				c.Revision = 0
			}
			if inspectionClocksAgree(a, c) {
				t.Fatal("peer clock binding", field)
			}
		})
	}
}
