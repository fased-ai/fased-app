package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSingleOwnerProductionRoundTrip(t *testing.T) {
	f, o, input := finalizerFixture(t, SingleOwnerSigningProfile)
	seed, err := ValidateProductionSeed(input.Seed, input.SeedSHA256)
	if err != nil || seed.SigningProfile != SingleOwnerSigningProfile {
		t.Fatal(seed, err)
	}
	finalizer, err := NewReleaseFinalizer(input)
	if err != nil {
		t.Fatal(err)
	}
	req, err := finalizer.Request("targets", ReleaseApprovals{})
	if err != nil || req.Threshold != 1 || len(req.KeyIDs) != 1 {
		t.Fatal(req, err)
	}
	approvals := approveRelease(t, f, finalizer)
	out, err := finalizer.Finalize(approvals)
	if err != nil || !out.Report.MetadataVerified || out.Report.Published || out.Report.Activation {
		t.Fatal(out.Report, err)
	}
	publicFixture := filepath.Join("..", "..", ".evidence", "single-owner-release", "publication-fixture.json")
	if err = os.MkdirAll(filepath.Dir(publicFixture), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(publicFixture, append(jsonBytes(t, out.Publication), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	f.files = map[string][]byte{}
	for name, b := range out.Files {
		f.files["/"+name] = b
	}
	o.Directory = filepath.Join(f.parent, "single-owner-finalized")
	p, err := Prepare(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	release, err := p.ApplicationRelease()
	if err != nil || release.Sequence != f.descriptor.Sequence {
		t.Fatal(release, err)
	}
	if _, err = os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatal("installer executed without authority")
	}
	// Role and payload binding remain mandatory with one approval.
	for name, change := range map[string]func(*ReleaseApprovals){
		"wrong-role": func(a *ReleaseApprovals) { a.Targets = a.Snapshot },
		"duplicate":  func(a *ReleaseApprovals) { a.Targets = append(a.Targets, a.Targets[0]) },
		"tampered-signature": func(a *ReleaseApprovals) {
			a.Targets[0].Signature = append([]byte(nil), a.Targets[0].Signature...)
			a.Targets[0].Signature[0] ^= 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := approveRelease(t, f, finalizer)
			change(&a)
			if _, e := finalizer.Finalize(a); e == nil {
				t.Fatal("invalid role approval admitted")
			}
		})
	}
	// One owner still needs each distinct role approval, with exact payloads.
	approvals.Targets = nil
	if _, err = finalizer.Finalize(approvals); err == nil {
		t.Fatal("unsigned targets admitted")
	}
}

func TestSingleOwnerPinnedProfileRefusals(t *testing.T) {
	f, _, _ := productionFixture(t, SingleOwnerSigningProfile)
	for name, mutate := range map[string]func(*Seed){
		"absent-profile":  func(s *Seed) { s.SigningProfile = "" },
		"unknown-profile": func(s *Seed) { s.SigningProfile = "one-key-anything" },
		"development-promotion": func(s *Seed) {
			s.Binding = Binding{Version: 1, Purpose: Purpose, DevelopmentOnly: true, Repository: s.Repository, Channel: "development", Target: s.Target}
			s.Provenance = nil
			s.Origin = "https://127.0.0.1:24443"
		},
		"expired":          func(s *Seed) { s.ExpiresAt = f.now.Add(-time.Second).Unix() },
		"wrong-repository": func(s *Seed) { s.Repository = "attacker/repo" },
		"bad-root-signature": func(s *Seed) {
			var root map[string]any
			_ = json.Unmarshal(s.Root, &root)
			root["signatures"] = []any{}
			s.Root = jsonBytes(t, root)
		},
	} {
		t.Run(name, func(t *testing.T) {
			seed := f.seed
			mutate(&seed)
			raw := jsonBytes(t, seed)
			if _, err := parseSeed(raw, digest(raw), f.now); err == nil {
				t.Fatal("invalid profile admitted")
			}
		})
	}
	// Original profile is neither silently relaxed nor reinterpreted.
	old, _, _ := productionFixture(t, "")
	old.seed.SigningProfile = SingleOwnerSigningProfile
	raw := jsonBytes(t, old.seed)
	if _, err := parseSeed(raw, digest(raw), old.now); err == nil {
		t.Fatal("eight-key root accepted under single-owner profile")
	}
}
