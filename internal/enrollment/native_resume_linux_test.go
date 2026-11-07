//go:build linux

package enrollment

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/signer"
)

func TestNativeEnrollmentStageOnlyResumeMatchesExactOwnerJournal(t *testing.T) {
	uid := os.Getuid()
	instance := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	coreHash, signerHash := strings.Repeat("c", 64), strings.Repeat("d", 64)
	j := journal{NativeOwnerAccess: true, Version: 1, ID: strings.Repeat("b", 32), Owner: uid, Phase: "prepared", Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.ControlledCustodyProfile, CoreHash: coreHash, SignerHash: signerHash, Network: "devnet", Source: sourceRecord{Profile: "local", Data: "/home/owner/state", Listen: "127.0.0.1:7331", ExpectedInstanceID: instance, UID: uid, GID: os.Getgid()}, Node: node{Inode: 1, UID: uid, GID: os.Getgid(), Mode: 0700}}
	m := custodyinstall.Manifest{NativeOwnerAccess: true, Enrollment: true, Version: 3, ID: strings.Repeat("e", 32), Owner: uid, Location: "local", Network: "devnet", Listen: j.Source.Listen, CoreHash: coreHash, SignerHash: signerHash, Mode: j.Mode, Profile: j.Profile, Provenance: j.Provenance}
	if !nativeResumeMatches(j, m, instance, "local", "devnet", coreHash, signerHash, true, true) {
		t.Fatal("owned StageOnly publication cannot resume")
	}
	for _, change := range []func(*journal, *custodyinstall.Manifest){func(j *journal, m *custodyinstall.Manifest) { j.Source.ExpectedInstanceID = "foreign" }, func(j *journal, m *custodyinstall.Manifest) { m.SignerHash = strings.Repeat("f", 64) }, func(j *journal, m *custodyinstall.Manifest) { m.NativeOwnerAccess = false }} {
		a, b := j, m
		change(&a, &b)
		if nativeResumeMatches(a, b, instance, "local", "devnet", coreHash, signerHash, true, true) {
			t.Fatal("conflicting target adopted")
		}
	}
	if nativeResumeMatches(j, m, instance, "local", "devnet", coreHash, signerHash, true, false) {
		t.Fatal("target without child owner intent adopted")
	}
}

func TestNativeConsentIsImmutableAfterControllerInterruption(t *testing.T) {
	h, s, o, op := fixture(t, fixtureImage(t), "local")
	o.Mode, o.Profile, o.Provenance = signer.OrdinaryCustodyMode, signer.OwnerCustodyProfile, signer.ControlledCustodyProfile
	o.NativeOwnerAccess = true
	h.m.Version, h.m.Mode, h.m.Profile, h.m.Provenance = 3, o.Mode, o.Profile, o.Provenance
	h.m.NativeOwnerAccess = true
	oldSave := op.save
	op.save = func(j journal) error {
		if err := oldSave(j); err != nil {
			return err
		}
		if j.Phase == "prepared" {
			return errors.New("disposable interruption")
		}
		return nil
	}
	if _, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err == nil || h.j == nil {
		t.Fatal("no retained native consent")
	}
	called := false
	oldPrepare := op.prepare
	op.prepare = func(ctx context.Context, j journal, o Options) (custodyinstall.Manifest, error) {
		called = true
		return oldPrepare(ctx, j, o)
	}
	changed := o
	changed.NativeOwnerAccess = false
	if _, err := run(t.Context(), s, changed, h.m.CoreHash, h.m.SignerHash, op); err == nil || called {
		t.Fatal("changed native permission resumed")
	}
	op.save = oldSave
	if _, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err != nil {
		t.Fatal("same native consent did not resume", err)
	}
}

func TestNativeCompletedEnrollmentRestartsWithoutReimport(t *testing.T) {
	for _, profile := range []string{"local", "remote"} {
		t.Run(profile, func(t *testing.T) {
			h, s, o, op := fixture(t, fixtureImage(t), profile)
			if _, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err != nil {
				t.Fatal(err)
			}
			saved := *h.j
			h.activated, h.ready = false, false
			op.emptySigner = func(context.Context, custodyinstall.Manifest) error {
				t.Fatal("existing key probed as empty")
				return nil
			}
			op.importImage = func(context.Context, journal, core.EnrollmentImage) error {
				t.Fatal("completed source reimported")
				return nil
			}
			op.restart = func(_ context.Context, j journal) error {
				if j != saved || !h.fenced || !h.published {
					t.Fatal("committed identity changed")
				}
				h.activated = true
				h.starts++
				return nil
			}
			o.StartExisting = true
			if _, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err != nil {
				t.Fatal(err)
			}
			if !h.ready || !h.activated || h.starts != 2 || *h.j != saved {
				t.Fatal("same completed identity did not become ready")
			}
			h.activated = false
			op.restart = func(context.Context, journal) error { return errors.New("OS start refused") }
			if _, err := run(t.Context(), s, o, h.m.CoreHash, h.m.SignerHash, op); err == nil || *h.j != saved {
				t.Fatal("failed OS start lost retained state")
			}
		})
	}
}
