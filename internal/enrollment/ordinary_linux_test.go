//go:build linux

package enrollment

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/signer"
)

func TestOrdinaryEnrollmentContextRecovery(t *testing.T) {
	image := fixtureImage(t)
	for _, profile := range []string{"local", "remote"} {
		t.Run(profile, func(t *testing.T) {
			h, s, o, op := fixture(t, image, profile)
			ordinaryContext(h, &o)
			m, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op)
			if err != nil || m != h.m || h.starts != 1 || !h.ready {
				t.Fatal(err)
			}
			before, _ := json.Marshal(h.j)
			if _, err = run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err != nil || h.starts != 1 {
				t.Fatal("retry changed owner topology", err)
			}
			o.Provenance = signer.OwnerKeyProvenance
			if _, err = run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err == nil {
				t.Fatal("fixture context changed into owner input")
			}
			after, _ := json.Marshal(h.j)
			if string(before) != string(after) {
				t.Fatal("mismatch rewrote the retained journal")
			}
		})
	}
}

func TestProtectedEnrollmentHandsSourceLeaseToTheHelper(t *testing.T) {
	image := fixtureImage(t)
	for _, releaseFails := range []bool{false, true} {
		h, source, options, op := fixture(t, image, "local")
		ordinaryContext(h, &options)
		export, fence := op.export, op.fence
		op.export = func(ctx context.Context, m custodyinstall.Manifest, s sourceRecord) (*imageLease, error) {
			lease, err := export(ctx, m, s)
			if err == nil {
				lease.release = func() error {
					if releaseFails {
						return errors.New("disposable lease failure")
					}
					h.held = false
					return nil
				}
			}
			return lease, err
		}
		called := false
		op.protectedFence = func(ctx context.Context, j journal) error {
			called = true
			if h.held {
				t.Fatal("owner retained the source lease during the protected reacquisition")
			}
			h.held = true // The protected helper owns its own source lease.
			err := fence(ctx, j)
			h.held = false
			return err
		}
		_, err := run(t.Context(), source, options, h.m.CoreHash, h.m.SignerHash, op)
		if releaseFails {
			if err == nil || called || h.fenced {
				t.Fatal("fence ran after the source lease could not be handed off")
			}
		} else if err != nil || !called || !h.ready || h.held {
			t.Fatalf("protected lease handoff did not finish: %v", err)
		}
	}
}

func ordinaryContext(h *fixtureHost, o *Options) {
	h.m.Version = 3
	h.m.Mode = signer.OrdinaryCustodyMode
	h.m.Profile = signer.OwnerCustodyProfile
	h.m.Provenance = signer.ControlledCustodyProfile
	o.Mode, o.Profile, o.Provenance = h.m.Mode, h.m.Profile, h.m.Provenance
}

func TestOrdinaryEnrollmentInterruptionRecovery(t *testing.T) {
	image := fixtureImage(t)
	for _, profile := range []string{"local", "remote"} {
		h, s, o, op := fixture(t, image, profile)
		ordinaryContext(h, &o)
		if _, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err != nil {
			t.Fatal(err)
		}
		count := len(h.steps)
		for _, before := range []bool{true, false} {
			for fail := 1; fail <= count; fail++ {
				t.Run(profile+"/"+strconv.Itoa(fail)+"-"+strconv.FormatBool(before), func(t *testing.T) {
					h, s, o, op := fixture(t, image, profile)
					ordinaryContext(h, &o)
					h.fail, h.before = fail, before
					_, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op)
					if err == nil || !h.tripped || h.held {
						t.Fatal("interruption failed or leaked source lock", err)
					}
					s.Running, s.Autostart = h.running, h.autostart
					m, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op)
					if err != nil || m != h.m || !h.ready || !h.fenced || !h.archived || h.starts != 1 || h.running || h.autostart || h.held || h.j.Phase != "complete" {
						t.Fatal("ordinary context or source recovery lost", err)
					}
					if h.j.Mode != signer.OrdinaryCustodyMode || h.j.Profile != signer.OwnerCustodyProfile || h.j.Provenance != signer.ControlledCustodyProfile {
						t.Fatal("ordinary enrollment context changed")
					}
				})
			}
		}
	}
}
