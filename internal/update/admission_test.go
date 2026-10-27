package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

type admissionFixture struct {
	*trustFixture
	policy     FixtureAdmissionPolicy
	unit       FixtureReleaseUnit
	provenance FixtureBuildProvenance
	evidence   FixtureReleaseEvidence
	private    ed25519.PrivateKey
	dir        string
}

func newAdmissionFixture(t *testing.T) *admissionFixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &admissionFixture{trustFixture: newTrustFixture(t), dir: dir}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.private = private
	id := FixtureBuildIdentity{SourceRepository: "fixture/source", SourceCommit: strings.Repeat("1", 40), SourceTree: strings.Repeat("2", 40), Workflow: "fixture/build.yml", WorkflowRevision: strings.Repeat("3", 40), Run: 42, Attempt: 1, Builder: "fixture/builder", ToolchainSHA256: digest([]byte("fixture toolchain")), LocksSHA256: digest([]byte("fixture locks"))}
	f.policy = FixtureAdmissionPolicy{FixtureBinding: f.seed.FixtureBinding, Version: 1, Profile: AdmissionProfile, Target: Target{"linux", "amd64"}, Identity: id, BuilderKey: pub, NotBefore: f.now - 10, NotAfter: f.now + 500}
	f.unit = FixtureReleaseUnit{FixtureBinding: f.seed.FixtureBinding, Version: 1, Profile: AdmissionProfile, Sequence: 10, Target: f.policy.Target, Identity: id, Compatibility: FixtureReleaseCompatibility{1, 1, 4, 1}}
	for _, role := range []string{"core", "signer", "owner-helper", "installer"} {
		b := []byte(inertArtifactPrefix + "opaque fixture " + role)
		a := FixtureReleaseSubject{Role: role, Target: f.policy.Target, SourceCommit: id.SourceCommit, Artifact: Artifact{File: role + ".fixture", Size: int64(len(b)), SHA256: digest(b)}}
		f.unit.Subjects = append(f.unit.Subjects, a)
		if e := os.WriteFile(filepath.Join(f.dir, a.File), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	f.provenance = FixtureBuildProvenance{FixtureBinding: f.seed.FixtureBinding, Version: 1, Profile: AdmissionProfile, UnitSHA256: digest(tj(f.unit)), Identity: id, BuiltAt: f.now - 1, Subjects: append([]FixtureReleaseSubject(nil), f.unit.Subjects...)}
	f.seal()
	return f
}

func (f *admissionFixture) seal() {
	payload := tj(f.provenance)
	envelope := Envelope{Version: 1, KeyID: KeyID(f.policy.BuilderKey), Payload: payload, Signature: ed25519.Sign(f.private, append([]byte(FixtureProvenancePurpose+"\x00"), payload...))}
	f.evidence = FixtureReleaseEvidence{FixtureBinding: f.seed.FixtureBinding, Version: 1, Profile: AdmissionProfile, Unit: tj(f.unit), Provenance: tj(envelope)}
	f.anchor()
}

func (f *admissionFixture) anchor() {
	f.t.Helper()
	db := tj(TrustFixtureDescriptor{FixtureBinding: f.seed.FixtureBinding, Version: 1, Sequence: f.unit.Sequence, PreserveSHA256: f.seed.PreserveSHA256, EvidenceSHA256: digest(tj(f.evidence))})
	target, err := metadata.TargetFile().FromBytes(fixtureTarget, db, "sha256")
	if err != nil {
		f.t.Fatal(err)
	}
	f.tg.Signed.Targets[fixtureTarget] = target
	f.tg.ClearSignatures()
	for _, s := range f.keys["targets"][:2] {
		if _, e := f.tg.Sign(s); e != nil {
			f.t.Fatal(e)
		}
	}
	f.publishTargets(f.bytes(f.tg))
	f.put("targets/fixture/"+digest(db)+".descriptor.json", db)
}

func (f *admissionFixture) admissionOptions() ReleaseAdmissionOptions {
	b := tj(f.policy)
	return ReleaseAdmissionOptions{Policy: b, PolicySHA256: digest(b), Evidence: tj(f.evidence), ArtifactDir: f.dir}
}

func admissionDisabled(r ReleaseAdmissionReport) bool {
	return !r.InstallAllowed && !r.ResumeAllowed && !r.UpdaterEnabled && !r.WalletSigner && !r.FinancialExecution
}

func (f *admissionFixture) admit(previous *TrustTransition) (TrustTransition, ReleaseAdmissionReport, error) {
	return AdmitFixtureRelease(context.Background(), f.options(previous), f.admissionOptions())
}

func TestReleaseAdmissionRequiresExplicitPinnedInputs(t *testing.T) {
	_, r, err := AdmitFixtureRelease(context.Background(), TrustRehearsalOptions{}, ReleaseAdmissionOptions{})
	if err == nil || r.ReleaseUnitVerified || r.ProvenanceVerified || r.InstallAllowed || r.FinancialExecution {
		t.Fatal("missing explicit fixture inputs admitted")
	}
}

func TestReleaseAdmissionCompleteUnitAndReadOnlyRestart(t *testing.T) {
	f := newAdmissionFixture(t)
	a, r, e := f.admit(nil)
	if e != nil || !r.ReleaseUnitVerified || !r.ProvenanceVerified || !r.ArtifactsVerified || !r.MetadataVerified || r.ArtifactCount != 4 || !r.FixtureOnly || !r.ReadOnly || !admissionDisabled(r) || r.UnitSHA256 != digest(tj(f.unit)) {
		t.Fatal("complete fixture refused or gained authority", e, r)
	}
	f.now++
	_, r, e = f.admit(&a)
	if e != nil || !r.ReleaseUnitVerified || !admissionDisabled(r) || r.Sequence != 10 {
		t.Fatal("same release restart refused", e)
	}
	for _, subject := range f.unit.Subjects {
		b, e := os.ReadFile(filepath.Join(f.dir, subject.File))
		if e != nil || digest(b) != subject.SHA256 {
			t.Fatal("artifact changed", e)
		}
	}
}

func TestReleaseAdmissionRejectsUnitAndProvenanceSubstitution(t *testing.T) {
	cases := map[string]func(*admissionFixture){
		"missing-core":      func(f *admissionFixture) { f.unit.Subjects = f.unit.Subjects[1:] },
		"missing-signer":    func(f *admissionFixture) { f.unit.Subjects = append(f.unit.Subjects[:1], f.unit.Subjects[2:]...) },
		"missing-helper":    func(f *admissionFixture) { f.unit.Subjects = append(f.unit.Subjects[:2], f.unit.Subjects[3]) },
		"missing-installer": func(f *admissionFixture) { f.unit.Subjects = f.unit.Subjects[:3] },
		"extra-subject":     func(f *admissionFixture) { f.unit.Subjects = append(f.unit.Subjects, f.unit.Subjects[0]) },
		"reordered-pair": func(f *admissionFixture) {
			f.unit.Subjects[0], f.unit.Subjects[1] = f.unit.Subjects[1], f.unit.Subjects[0]
		},
		"mixed-platform":             func(f *admissionFixture) { f.unit.Subjects[1].Target = Target{"darwin", "arm64"} },
		"mixed-source":               func(f *admissionFixture) { f.unit.Subjects[1].SourceCommit = strings.Repeat("4", 40) },
		"repeated-artifact":          func(f *admissionFixture) { f.unit.Subjects[1].SHA256 = f.unit.Subjects[0].SHA256 },
		"unsafe-path":                func(f *admissionFixture) { f.unit.Subjects[0].File = "../core.fixture" },
		"archive":                    func(f *admissionFixture) { f.unit.Subjects[2].File = "helper.tar" },
		"invalid-size":               func(f *admissionFixture) { f.unit.Subjects[0].Size = maxInertArtifact + 1 },
		"invalid-digest":             func(f *admissionFixture) { f.unit.Subjects[0].SHA256 = strings.Repeat("A", 64) },
		"incompatible-helper":        func(f *admissionFixture) { f.unit.Compatibility.OwnerEntrypoint++ },
		"provenance-unit-digest":     func(f *admissionFixture) { f.provenance.UnitSHA256 = digest([]byte("other release")) },
		"unit-source":                func(f *admissionFixture) { f.unit.Identity.SourceCommit = strings.Repeat("4", 40) },
		"unit-tree":                  func(f *admissionFixture) { f.unit.Identity.SourceTree = strings.Repeat("4", 40) },
		"unit-workflow":              func(f *admissionFixture) { f.unit.Identity.Workflow = "fixture/other.yml" },
		"unit-workflow-revision":     func(f *admissionFixture) { f.unit.Identity.WorkflowRevision = strings.Repeat("4", 40) },
		"unit-run":                   func(f *admissionFixture) { f.unit.Identity.Run++ },
		"unit-attempt":               func(f *admissionFixture) { f.unit.Identity.Attempt++ },
		"unit-builder":               func(f *admissionFixture) { f.unit.Identity.Builder = "fixture/other" },
		"unit-toolchain":             func(f *admissionFixture) { f.unit.Identity.ToolchainSHA256 = digest([]byte("other toolchain")) },
		"unit-locks":                 func(f *admissionFixture) { f.unit.Identity.LocksSHA256 = digest([]byte("other locks")) },
		"unit-channel":               func(f *admissionFixture) { f.unit.Channel = "stable" },
		"unit-fixture-mark":          func(f *admissionFixture) { f.unit.FixtureOnly = false },
		"unit-profile":               func(f *admissionFixture) { f.unit.Profile = "production" },
		"unit-repository":            func(f *admissionFixture) { f.unit.Repository = NewProbe() },
		"provenance-source":          func(f *admissionFixture) { f.provenance.Identity.SourceCommit = strings.Repeat("4", 40) },
		"provenance-builder":         func(f *admissionFixture) { f.provenance.Identity.Builder = "fixture/other" },
		"provenance-subject":         func(f *admissionFixture) { f.provenance.Subjects[2].SHA256 = digest([]byte("other helper")) },
		"provenance-missing-subject": func(f *admissionFixture) { f.provenance.Subjects = f.provenance.Subjects[:3] },
		"provenance-future":          func(f *admissionFixture) { f.provenance.BuiltAt = f.now + 1 },
		"provenance-before-policy":   func(f *admissionFixture) { f.provenance.BuiltAt = f.policy.NotBefore - 1 },
		"provenance-profile":         func(f *admissionFixture) { f.provenance.Profile = "production" },
		"evidence-purpose":           func(f *admissionFixture) { f.policy.Purpose = "production" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newAdmissionFixture(t)
			mutate(f)
			f.seal()
			_, r, e := f.admit(nil)
			if e == nil || r.ReleaseUnitVerified || r.ProvenanceVerified || r.ArtifactsVerified || !admissionDisabled(r) {
				t.Fatal("substitution admitted", e, r)
			}
		})
	}
}

func TestReleaseAdmissionStrictEvidenceAndSignatures(t *testing.T) {
	for _, name := range []string{"evidence-unbound", "evidence-wrong-binding", "evidence-unknown-field", "unit-unknown-hook", "unit-duplicate", "unit-omitted-version", "provenance-signature", "provenance-domain", "provenance-key-id", "provenance-signed-unknown", "provenance-duplicate", "wrong-unit-sequence", "policy-stale-pin", "policy-expired", "policy-future", "policy-long-validity", "policy-replaced-key", "policy-binding", "policy-unknown", "policy-target"} {
		t.Run(name, func(t *testing.T) {
			f := newAdmissionFixture(t)
			o := f.admissionOptions()
			var env Envelope
			_ = StrictJSON(f.evidence.Provenance, &env)
			switch name {
			case "evidence-unbound":
				o.Evidence = append(o.Evidence, ' ')
			case "evidence-wrong-binding":
				f.evidence.Repository = NewProbe()
			case "evidence-unknown-field":
				o.Evidence = append([]byte(`{"hook":"execute",`), o.Evidence[1:]...)
			case "unit-unknown-hook":
				f.evidence.Unit = append([]byte(`{"hook":"execute",`), f.evidence.Unit[1:]...)
			case "unit-duplicate":
				f.evidence.Unit = append([]byte(`{"version":1,`), f.evidence.Unit[1:]...)
			case "unit-omitted-version":
				f.unit.Version = 0
				f.seal()
			case "provenance-signature":
				env.Signature[0] ^= 1
			case "provenance-domain":
				env.Signature = ed25519.Sign(f.private, append([]byte(SignatureDomain), env.Payload...))
			case "provenance-key-id":
				env.KeyID = digest([]byte("different builder"))
			case "provenance-signed-unknown":
				env.Payload = append([]byte(`{"hook":"execute",`), env.Payload[1:]...)
				env.Signature = ed25519.Sign(f.private, append([]byte(FixtureProvenancePurpose+"\x00"), env.Payload...))
			case "provenance-duplicate":
				env.Payload = append([]byte(`{"version":1,`), env.Payload[1:]...)
				env.Signature = ed25519.Sign(f.private, append([]byte(FixtureProvenancePurpose+"\x00"), env.Payload...))
			case "wrong-unit-sequence":
				f.unit.Sequence++
				f.provenance.UnitSHA256 = digest(tj(f.unit))
				f.seal()
				f.unit.Sequence-- // descriptor is 10, authenticated evidence still names 11
			case "policy-stale-pin":
				o.PolicySHA256 = digest([]byte("substitution"))
			case "policy-expired":
				f.policy.NotAfter = f.now
			case "policy-future":
				f.policy.NotBefore = f.now + 1
			case "policy-long-validity":
				f.policy.NotAfter = f.policy.NotBefore + 7*86400 + 1
			case "policy-replaced-key":
				f.policy.BuilderKey = make([]byte, ed25519.PublicKeySize)
			case "policy-binding":
				f.policy.Repository = NewProbe()
			case "policy-unknown":
				o.Policy = append([]byte(`{"unknown":true,`), o.Policy[1:]...)
				o.PolicySHA256 = digest(o.Policy)
			case "policy-target":
				f.policy.Target = Target{"darwin", "arm64"}
			}
			if strings.HasPrefix(name, "provenance-") {
				f.evidence.Provenance = tj(env)
			}
			if name != "evidence-unbound" && name != "evidence-unknown-field" && name != "policy-stale-pin" && name != "policy-unknown" {
				f.anchor()
				o = f.admissionOptions()
			}
			_, r, e := AdmitFixtureRelease(context.Background(), f.options(nil), o)
			if e == nil || r.ReleaseUnitVerified || !admissionDisabled(r) {
				t.Fatal("malformed or substituted evidence admitted", e)
			}
		})
	}
}

func TestReleaseAdmissionRejectsUnsafeArtifacts(t *testing.T) {
	for _, name := range []string{"missing", "truncated", "changed", "executable", "non-inert", "extra-file", "directory", "symlink", "symlink-directory", "relative-directory"} {
		t.Run(name, func(t *testing.T) {
			f := newAdmissionFixture(t)
			path := filepath.Join(f.dir, "core.fixture")
			b, _ := os.ReadFile(path)
			var e error
			switch name {
			case "missing":
				e = os.Remove(path)
			case "truncated":
				e = os.WriteFile(path, b[:len(b)-1], 0600)
			case "changed":
				b[len(b)-1] ^= 1
				e = os.WriteFile(path, b, 0600)
			case "executable":
				if runtime.GOOS == "windows" {
					t.Skip("Unix executable mode predicate")
				}
				e = os.Chmod(path, 0700)
			case "non-inert":
				b[0] = '#'
				f.unit.Subjects[0].SHA256 = digest(b)
				f.provenance.Subjects[0] = f.unit.Subjects[0]
				f.provenance.UnitSHA256 = digest(tj(f.unit))
				f.seal()
				e = os.WriteFile(path, b, 0600)
			case "extra-file":
				e = os.WriteFile(filepath.Join(f.dir, "hook.sh"), []byte("not executed"), 0600)
			case "directory":
				if e = os.Remove(path); e == nil {
					e = os.Mkdir(path, 0700)
				}
			case "symlink":
				if runtime.GOOS == "windows" {
					t.Skip("symlink privilege unavailable")
				}
				if e = os.Remove(path); e == nil {
					e = os.Symlink(filepath.Join(f.dir, "signer.fixture"), path)
				}
			case "symlink-directory":
				if runtime.GOOS == "windows" {
					t.Skip("symlink privilege unavailable")
				}
				link := filepath.Join(t.TempDir(), "link")
				e = os.Symlink(f.dir, link)
				f.dir = link
			case "relative-directory":
				f.dir = "."
			}
			if e != nil {
				t.Fatal(e)
			}
			_, r, e := f.admit(nil)
			if e == nil || r.ReleaseUnitVerified || !admissionDisabled(r) {
				t.Fatal("unsafe artifact admitted", e)
			}
		})
	}
}

func TestReleaseAdmissionFailurePreservesTrustAndFixtureFloors(t *testing.T) {
	f := newAdmissionFixture(t)
	initial, _, e := f.admit(nil)
	if e != nil {
		t.Fatal(e)
	}
	store, _ := fixtureStore(t)
	if _, e = store.Commit(initial); e != nil {
		t.Fatal(e)
	}
	old := f.rotate("root")
	f.root.Signed.Version = 2
	f.put("2.root.json", f.rootSign(old[:2]...))
	f.release(2, 11)
	f.unit.Sequence = 11
	f.provenance.UnitSHA256 = digest(tj(f.unit))
	f.seal()
	core := filepath.Join(f.dir, "core.fixture")
	b, _ := os.ReadFile(core)
	if e = os.Remove(core); e != nil {
		t.Fatal(e)
	}
	progress, r, e := f.admit(&initial)
	if e == nil || !r.TrustProgressOnly || r.ReleaseUnitVerified || r.MetadataVerified || r.RootVersion != 2 || !admissionDisabled(r) {
		t.Fatal("lost progressive root or granted admission", e, r)
	}
	if _, e = store.Commit(progress); e != nil {
		t.Fatal("admission progress cannot persist", e)
	}
	if _, e = store.Load(initial.seedSHA, initial.report.NextSHA256); e == nil {
		t.Fatal("stale external head accepted after failed admission")
	}
	loaded, e := store.Load(progress.seedSHA, progress.report.NextSHA256)
	if e != nil || digest(loaded) != progress.report.NextSHA256 {
		t.Fatal("persisted admission progress unavailable", e)
	}
	var s TrustFixtureSnapshot
	if json.Unmarshal(progress.next, &s) != nil || s.InstalledFloor != f.seed.InstalledFloor || s.ReservedFloor != f.seed.ReservedFloor || s.PreserveSHA256 != f.seed.PreserveSHA256 || s.ReleaseFloor != 11 {
		t.Fatal("fixture floors/anchors changed")
	}
	// Revoked-root knowledge survives a missing successor on the next retry.
	f.mu.Lock()
	delete(f.files, "2.root.json")
	f.mu.Unlock()
	if e = os.WriteFile(core, b, 0600); e != nil {
		t.Fatal(e)
	}
	_, r, e = f.admit(&progress)
	if e != nil || !r.ReleaseUnitVerified || r.RootVersion != 2 {
		t.Fatal("identical release could not recover", e)
	}
	f.release(1, 10)
	_, r, e = f.admit(&progress)
	if e == nil || r.ReleaseUnitVerified || !admissionDisabled(r) {
		t.Fatal("older metadata erased admission failure progress")
	}
}

func TestReleaseAdmissionKeepsUpstreamTrustProgress(t *testing.T) {
	f := newAdmissionFixture(t)
	first, _, e := f.admit(nil)
	if e != nil {
		t.Fatal(e)
	}
	f.release(3, 11)
	f.put("3.snapshot.json", []byte("incomplete"))
	next, r, e := f.admit(&first)
	if e == nil || !r.TrustProgressOnly || len(next.next) == 0 || r.ReleaseUnitVerified || !admissionDisabled(r) {
		t.Fatal("admission wrapper discarded upstream progressive failure", e)
	}
	f.release(1, 10)
	if _, r, e = f.admit(&next); e == nil || r.ReleaseUnitVerified {
		t.Fatal("new timestamp floor erased by release admission")
	}
}

func TestReleaseAdmissionBuilderCannotReuseMetadataKey(t *testing.T) {
	f := newAdmissionFixture(t)
	for _, key := range f.root.Signed.Keys {
		b, e := hex.DecodeString(key.Value.PublicKey)
		if e != nil {
			t.Fatal(e)
		}
		f.policy.BuilderKey = b
		break
	}
	f.seal()
	_, r, e := f.admit(nil)
	if e == nil || !strings.Contains(e.Error(), "separate from metadata keys") || r.ReleaseUnitVerified || !admissionDisabled(r) {
		t.Fatal("metadata key reused as builder authority", e)
	}
}
