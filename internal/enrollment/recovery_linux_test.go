//go:build linux

package enrollment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/signer"
)

func TestSuspendedRemoteRecoveryContinuesSameController(t *testing.T) {
	h, s, o, op := fixture(t, fixtureImage(t), "remote")
	o.Mode, o.Profile, o.Provenance = signer.OrdinaryCustodyMode, signer.OwnerCustodyProfile, signer.ControlledCustodyProfile
	h.m.Version, h.m.Mode, h.m.Profile, h.m.Provenance = 3, o.Mode, o.Profile, o.Provenance
	export := op.export
	op.export = func(context.Context, custodyinstall.Manifest, sourceRecord) (*imageLease, error) {
		return nil, errors.New("retained source inventory refusal")
	}
	if _, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err == nil || h.j == nil || h.j.Phase != "suspended" || h.j.Digest != "" {
		t.Fatal("missing real suspended baseline", err)
	}
	before := *h.j
	history := false
	op.archiveRecovery = func(j journal) error {
		if j != before {
			t.Fatal("original journal changed before archive")
		}
		history = true
		return nil
	}
	next := strings.Repeat("e", 64)
	op.recoverCore = func(_ context.Context, j journal, _ Options, hash string) (custodyinstall.Manifest, error) {
		if !history || hash != next || j.ID != before.ID || h.running || h.activated || h.staged || h.fenced || h.signerForeign {
			t.Fatal("unguarded core recovery")
		}
		h.m.CoreHash = hash
		return h.m, nil
	}
	op.export = export
	o.RecoverSuspended = true
	if _, err := run(t.Context(), s, o, next, before.SignerHash, op); err != nil {
		t.Fatal("guarded recovery could not continue", err)
	}
	if !history || h.j.ID != before.ID || h.j.Source != before.Source || h.j.Node != before.Node || h.j.CoreHash != next || h.j.SignerHash != before.SignerHash || h.j.Phase != "complete" || h.starts != 1 {
		t.Fatal("recovery changed logical identity, signer or execution count")
	}
}

func suspendedRecoveryFixture(t *testing.T) (*fixtureHost, Source, Options, operations, journal) {
	t.Helper()
	h, s, o, op := fixture(t, fixtureImage(t), "remote")
	o.Mode, o.Profile, o.Provenance = signer.OrdinaryCustodyMode, signer.OwnerCustodyProfile, signer.ControlledCustodyProfile
	h.m.Version, h.m.Mode, h.m.Profile, h.m.Provenance = 3, o.Mode, o.Profile, o.Provenance
	export := op.export
	op.export = func(context.Context, custodyinstall.Manifest, sourceRecord) (*imageLease, error) {
		return nil, errors.New("inventory refusal")
	}
	if _, e := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); e == nil || h.j == nil || h.j.Phase != "suspended" {
		t.Fatal("no suspended baseline", e)
	}
	op.export = export
	o.RecoverSuspended = true
	before := *h.j
	op.archiveRecovery = func(j journal) error {
		if j != before {
			return errState
		}
		return h.step("archive-core-recovery", nil)
	}
	op.recoverCore = func(_ context.Context, j journal, _ Options, next string) (custodyinstall.Manifest, error) {
		if h.signerForeign || h.staged || h.published || h.fenced || h.activated {
			return h.m, errState
		}
		e := h.step("recover-core", func() { h.m.CoreHash = next })
		return h.m, e
	}
	return h, s, o, op, before
}

func TestSuspendedRemoteRecoveryEveryControllerInterruption(t *testing.T) {
	h, s, o, op, before := suspendedRecoveryFixture(t)
	h.steps = nil
	next := strings.Repeat("e", 64)
	if _, e := run(t.Context(), s, o, next, before.SignerHash, op); e != nil {
		t.Fatal(e)
	}
	count := len(h.steps)
	for at := 1; at <= count; at++ {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprint(at, "-", after), func(t *testing.T) {
				h, s, o, op, before := suspendedRecoveryFixture(t)
				h.steps = nil
				h.fail = at
				h.before = !after
				if _, e := run(t.Context(), s, o, next, before.SignerHash, op); e == nil || !h.tripped {
					t.Fatal("interruption not exercised", e)
				}
				h.fail = 0
				for i := 0; i < 2; i++ {
					if _, e := run(t.Context(), s, o, next, before.SignerHash, op); e != nil {
						t.Fatal("same command did not reconcile", at, after, e)
					}
				}
				if h.j.ID != before.ID || h.j.Source != before.Source || h.j.Node != before.Node || h.j.SignerHash != before.SignerHash || h.starts != 1 || !h.ready {
					t.Fatal("identity or exactly-once completion changed")
				}
			})
		}
	}
}

func TestSuspendedRemoteRecoveryRefusesUnsafeState(t *testing.T) {
	for _, mode := range []string{"no-consent", "local", "digest", "importing", "committed", "key", "foreign-source", "foreign-owner", "signer", "no-journal", "same-core"} {
		t.Run(mode, func(t *testing.T) {
			h, s, o, op, b := suspendedRecoveryFixture(t)
			next := strings.Repeat("e", 64)
			sig := b.SignerHash
			switch mode {
			case "no-consent":
				o.RecoverSuspended = false
			case "local":
				s.Profile = "local"
			case "digest":
				h.j.Digest = strings.Repeat("f", 64)
			case "importing":
				h.j.Phase = "importing"
			case "committed":
				h.j.Phase = "committed"
			case "key":
				h.signerForeign = true
			case "foreign-source":
				h.wrongSource = true
			case "foreign-owner":
				h.j.Owner++
			case "signer":
				sig = strings.Repeat("d", 64)
			case "no-journal":
				h.j = nil
			case "same-core":
				next = b.CoreHash
			}
			mutated := false
			op.archiveRecovery = func(journal) error { mutated = true; return nil }
			op.recoverCore = func(context.Context, journal, Options, string) (custodyinstall.Manifest, error) {
				mutated = true
				return h.m, nil
			}
			if _, e := run(t.Context(), s, o, next, sig, op); e == nil || mutated {
				t.Fatal("unsafe recovery mutated target", mode, e)
			}
		})
	}
}

func TestNativeRecoveryReadOnlyEligibility(t *testing.T) {
	h, _, _, _, b := suspendedRecoveryFixture(t)
	next := strings.Repeat("e", 64)
	if !nativeRecoveryMatches(*h.j, b.Source.ExpectedInstanceID, "remote", b.Network, next, b.SignerHash, false) {
		t.Fatal("exact suspended target not offered recovery")
	}
	retrying := b
	retrying.CoreRecovery = &coreRecovery{Version: 1, Previous: b.Target, NextCoreHash: next}
	retrying.CoreHash, retrying.Target.CoreHash = next, next
	if !nativeRecoveryMatches(retrying, b.Source.ExpectedInstanceID, "remote", b.Network, next, b.SignerHash, false) {
		t.Fatal("recorded pre-import retry lost explicit recovery handoff")
	}
	for _, change := range []func(*journal){func(j *journal) { j.Phase = "importing" }, func(j *journal) { j.Digest = strings.Repeat("f", 64) }, func(j *journal) { j.Source.ExpectedInstanceID = "foreign" }, func(j *journal) { j.SignerHash = strings.Repeat("f", 64) }, func(j *journal) { j.Source.Profile = "local" }, func(j *journal) { j.NativeOwnerAccess = true }} {
		j := b
		change(&j)
		if nativeRecoveryMatches(j, b.Source.ExpectedInstanceID, "remote", b.Network, next, b.SignerHash, false) {
			t.Fatal("unsafe recovery advertised")
		}
	}
}

func TestSuspendedRemoteRecoveryExportRetryRestoresAndResuspends(t *testing.T) {
	h, s, o, op, b := suspendedRecoveryFixture(t)
	export := op.export
	op.export = func(context.Context, custodyinstall.Manifest, sourceRecord) (*imageLease, error) {
		return nil, errors.New("source temporarily unavailable")
	}
	next := strings.Repeat("e", 64)
	if _, e := run(t.Context(), s, o, next, b.SignerHash, op); e == nil || !h.running || h.j.Digest != "" || h.j.Phase != "suspended" || h.j.CoreHash != next {
		t.Fatal("failed pre-import recovery did not restore original source", e)
	}
	op.export = func(ctx context.Context, m custodyinstall.Manifest, record sourceRecord) (*imageLease, error) {
		if h.running {
			t.Fatal("retry exported a running restored source")
		}
		return export(ctx, m, record)
	}
	if _, e := run(t.Context(), s, o, next, b.SignerHash, op); e != nil || h.starts != 1 || h.j.Phase != "complete" {
		t.Fatal("same-command cold retry failed", e)
	}
}
