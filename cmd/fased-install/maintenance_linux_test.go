//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestCandidateRecoveryKeepsTheOriginalOperationAfterExpiry(t *testing.T) {
	r := maintenance.CandidateReceipt{Version: 1, Owner: 1400, Scope: "update", PacketSHA256: strings.Repeat("a", 64), InstallerSHA256: strings.Repeat("b", 64), EngineSHA256: strings.Repeat("c", 64), ProductVersion: "0.1.12", Sequence: 12, ReleaseSHA256: strings.Repeat("d", 64), Target: update.Target{OS: "linux", Arch: "amd64"}, BaseSequence: 11, BaseReleaseSHA256: strings.Repeat("e", 64), BaseInstallation: strings.Repeat("1", 32), BaseManifestSHA: strings.Repeat("f", 64), ExpiresAt: 100}
	op := maintenance.CandidateOperation{Version: 1, Owner: r.Owner, Scope: r.Scope, PacketSHA256: r.PacketSHA256, OperationID: strings.Repeat("2", 32), RequestSHA256: strings.Repeat("3", 64)}
	if bind, err := candidateBindingRequired(r, op, nil, time.Unix(99, 0)); err != nil || !bind {
		t.Fatal("valid manual operation did not bind", err)
	}
	if _, err := candidateBindingRequired(r, op, nil, time.Unix(100, 0)); err == nil {
		t.Fatal("new operation began after admission expired")
	}
	if bind, err := candidateBindingRequired(r, op, &op, time.Unix(101, 0)); err != nil || bind {
		t.Fatal("original interrupted operation could not resume", err)
	}
	for _, change := range []func(*maintenance.CandidateOperation){
		func(v *maintenance.CandidateOperation) { v.OperationID = strings.Repeat("4", 32) },
		func(v *maintenance.CandidateOperation) { v.RequestSHA256 = strings.Repeat("5", 64) },
		func(v *maintenance.CandidateOperation) { v.PacketSHA256 = strings.Repeat("6", 64) },
	} {
		changed := op
		change(&changed)
		if _, err := candidateBindingRequired(r, changed, &op, time.Unix(101, 0)); err == nil {
			t.Fatal("recovery accepted a different operation")
		}
	}
}

func TestCustodyWorkerKeepsCompleteOwnerRelease(t *testing.T) {
	id := strings.Repeat("1", 32)
	release := core.ApplicationRelease{EngineSHA256: strings.Repeat("a", 64), Version: "0.1.12", Sequence: 12, ManifestSHA256: strings.Repeat("b", 64), Target: update.Target{OS: "linux", Arch: "amd64"}}
	policy := core.UpdatePolicy{Mode: "manual"}
	view := core.ApplicationOwnerView{View: core.ApplicationUpdateView{Policy: policy, Operation: core.ApplicationUpdateOperation{ID: id, Phase: "applying", Release: &release, PolicySHA256: update.SnapshotDigest("application-update-policy", policy)}}}
	q, got, manual, err := approvedCustodyOperation(view, id, time.Now())
	if err != nil || !manual || got == nil || *got != release || q.OperationID != id || q.Sequence != release.Sequence || q.ReleaseSHA256 != release.ManifestSHA256 {
		t.Fatalf("owner operation lost its release identity: request=%+v release=%+v err=%v", q, got, err)
	}
	// The returned identity is a copy, so later state changes cannot retarget
	// the independently verified acquisition.
	release.EngineSHA256 = strings.Repeat("c", 64)
	if got.EngineSHA256 == release.EngineSHA256 {
		t.Fatal("approved engine changed after inspection")
	}
	for name, change := range map[string]func(*core.ApplicationOwnerView){
		"different operation": func(v *core.ApplicationOwnerView) { v.View.Operation.ID = strings.Repeat("2", 32) },
		"not applying":        func(v *core.ApplicationOwnerView) { v.View.Operation.Phase = "available" },
		"no release":          func(v *core.ApplicationOwnerView) { v.View.Operation.Release = nil },
		"changed policy":      func(v *core.ApplicationOwnerView) { v.View.Policy.Notify = true },
		"automatic held":      func(v *core.ApplicationOwnerView) { v.View.Operation.Automatic = true },
	} {
		t.Run(name, func(t *testing.T) {
			changed := view
			change(&changed)
			if _, _, _, err := approvedCustodyOperation(changed, id, time.Now()); err == nil {
				t.Fatal("unapproved owner operation was accepted")
			}
		})
	}
}

type custodyAcquisitionFixture struct {
	release core.ApplicationRelease
	report  bootstrap.Report
	bindErr error
	seen    []string
}

func (f *custodyAcquisitionFixture) ApplicationRelease() (*core.ApplicationRelease, error) {
	f.seen = append(f.seen, "release")
	r := f.release
	return &r, nil
}
func (f *custodyAcquisitionFixture) BindApplicationRelease(r core.ApplicationRelease) error {
	f.seen = append(f.seen, "bind")
	if f.bindErr != nil || r != f.release {
		return errors.New("verified release differs")
	}
	return nil
}
func (f *custodyAcquisitionFixture) Report() bootstrap.Report {
	f.seen = append(f.seen, "report")
	return f.report
}
func (f *custodyAcquisitionFixture) MaintenanceIdentity() (bootstrap.MaintenanceIdentity, error) {
	f.seen = append(f.seen, "identity")
	return bootstrap.MaintenanceIdentity{Version: f.release.Version}, nil
}

func TestCustodyAcquisitionRetiresFreshCandidateOnlyAfterVerifiedApproval(t *testing.T) {
	id := update.NewProbe()
	release := core.ApplicationRelease{EngineSHA256: strings.Repeat("a", 64), Version: "0.1.12", Sequence: 12, ManifestSHA256: strings.Repeat("b", 64), Target: update.Target{OS: "linux", Arch: "amd64"}}
	policy := core.UpdatePolicy{Mode: "manual"}
	view := core.ApplicationOwnerView{View: core.ApplicationUpdateView{Policy: policy, Operation: core.ApplicationUpdateOperation{ID: id, Phase: "applying", Release: &release, PolicySHA256: update.SnapshotDigest("application-update-policy", policy)}}}
	q := maintenance.CustodyUpdateRequest{OperationID: id, Sequence: release.Sequence, ReleaseSHA256: release.ManifestSHA256}
	newAcquisition := func() *custodyAcquisitionFixture {
		return &custodyAcquisitionFixture{release: release, report: bootstrap.Report{Sequence: release.Sequence, DescriptorSHA256: release.ManifestSHA256, ProvenanceVerified: true}}
	}
	for _, tc := range []struct {
		name   string
		change func(*core.ApplicationOwnerView, *custodyAcquisitionFixture)
	}{
		{"owner operation differs", func(v *core.ApplicationOwnerView, _ *custodyAcquisitionFixture) {
			v.View.Operation.ID = update.NewProbe()
		}},
		{"release binding fails", func(_ *core.ApplicationOwnerView, p *custodyAcquisitionFixture) {
			p.bindErr = errors.New("bad release")
		}},
		{"verified report differs", func(_ *core.ApplicationOwnerView, p *custodyAcquisitionFixture) {
			p.report.DescriptorSHA256 = strings.Repeat("c", 64)
		}},
		{"provenance absent", func(_ *core.ApplicationOwnerView, p *custodyAcquisitionFixture) { p.report.ProvenanceVerified = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, p, retired := view, newAcquisition(), false
			tc.change(&v, p)
			_, _, err := finishCustodyAcquisition(q, &v, time.Now(), p, func(bootstrap.Report, bootstrap.MaintenanceIdentity) error { retired = true; return nil })
			if err == nil || retired {
				t.Fatalf("unverified request retired the original candidate: %v", err)
			}
		})
	}
	// This models an interrupted archive-first cleanup. The installed helper
	// still has to prove the actual protected files during acceptance.
	p := newAcquisition()
	archive, initialJournal, attempts := "", "initial-installation", 0
	retire := func(bootstrap.Report, bootstrap.MaintenanceIdentity) error {
		attempts++
		if archive != "" && archive != "original-fresh-operation" || initialJournal != "initial-installation" {
			t.Fatal("interrupted retirement changed its original identity")
		}
		archive = "original-fresh-operation"
		if attempts == 1 {
			return errors.New("interrupted after archive")
		}
		return nil
	}
	if _, _, err := finishCustodyAcquisition(q, &view, time.Now(), p, retire); err == nil {
		t.Fatal("interrupted cleanup appeared complete")
	}
	if _, _, err := finishCustodyAcquisition(q, &view, time.Now(), newAcquisition(), retire); err != nil || archive != "original-fresh-operation" || attempts != 2 {
		t.Fatal("original cleanup could not resume", err)
	}
	bootstrapRequest := q
	bootstrapRequest.Bootstrap = true
	if _, _, err := finishCustodyAcquisition(bootstrapRequest, nil, time.Now(), newAcquisition(), func(bootstrap.Report, bootstrap.MaintenanceIdentity) error { return nil }); err != nil {
		t.Fatal("verified legacy bootstrap was refused", err)
	}
}

func TestCustodyNativeBindingUsesAuthenticatedReleaseFloor(t *testing.T) {
	source := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name     string
		report   bootstrap.Report
		identity bootstrap.MaintenanceIdentity
		want     string
		wantErr  bool
	}{
		{"older headless release", bootstrap.Report{Version: 4, Sequence: 1_000_012, ProvenanceVerified: true}, bootstrap.MaintenanceIdentity{Version: "0.1.11"}, "", false},
		{"development fixture", bootstrap.Report{Version: 4, Sequence: 1_000_013, DevelopmentOnly: true}, bootstrap.MaintenanceIdentity{}, "", false},
		{"new authenticated pair", bootstrap.Report{Version: 4, Sequence: 1_000_013, ProvenanceVerified: true}, bootstrap.MaintenanceIdentity{Version: "0.1.12", Source: source}, source, false},
		{"new target under legacy sequence", bootstrap.Report{Version: 3, Sequence: 7, ProvenanceVerified: true}, bootstrap.MaintenanceIdentity{Version: "0.1.12", Source: source}, source, false},
		{"later authenticated pair", bootstrap.Report{Version: 4, Sequence: 2_000_001, ProvenanceVerified: true}, bootstrap.MaintenanceIdentity{Version: "0.2.0", Source: source}, source, false},
		{"missing source", bootstrap.Report{Version: 4, Sequence: 1_000_013, ProvenanceVerified: true}, bootstrap.MaintenanceIdentity{Version: "0.1.12"}, "", true},
		{"unverified release", bootstrap.Report{Version: 4, Sequence: 1_000_013}, bootstrap.MaintenanceIdentity{Version: "0.1.12", Source: source}, "", true},
		{"invalid version", bootstrap.Report{Version: 4, ProvenanceVerified: true}, bootstrap.MaintenanceIdentity{Version: "0.1.12-rc", Source: source}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := custodyNativeBindingSource(tc.report, tc.identity)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatal("wrong native binding requirement", got, err)
			}
		})
	}
}

func TestExpiredUnboundCandidateDoesNotShadowPublicRelease(t *testing.T) {
	r := maintenance.CandidateReceipt{Scope: "update", Sequence: 12, ReleaseSHA256: "candidate", EngineSHA256: "candidate-core", ExpiresAt: 100}
	for _, tc := range []struct {
		name      string
		now       int64
		release   string
		core      string
		operation error
		selected  bool
		wantErr   bool
	}{
		{"expired unused candidate, different public release", 100, "public", "public-core", os.ErrNotExist, false, false},
		{"expired bound candidate, different release", 100, "public", "public-core", nil, true, true},
		{"active candidate, different release", 99, "public", "public-core", os.ErrNotExist, true, true},
		{"expired bound candidate, original release", 100, "candidate", "candidate-core", nil, true, false},
		{"expired candidate, unreadable operation", 100, "candidate", "candidate-core", os.ErrPermission, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected, err := candidateSelectsRelease(r, "update", 12, tc.release, tc.core, time.Unix(tc.now, 0), func() error { return tc.operation })
			if selected != tc.selected || (err != nil) != tc.wantErr {
				t.Fatalf("selected=%t err=%v; want selected=%t error=%t", selected, err, tc.selected, tc.wantErr)
			}
		})
	}
}

func TestFixtureSuccessorSignerCannotCrossProductionBoundary(t *testing.T) {
	initial := &bootstrap.WalletPayload{Version: "0.1.11", Source: "source-a"}
	successor := &bootstrap.WalletPayload{Version: "0.1.12", Source: "source-b"}
	first := bootstrap.MaintenanceIdentity{Version: initial.Version, Source: initial.Source}
	next := bootstrap.MaintenanceIdentity{Version: successor.Version, Source: successor.Source}
	if got := selectEmbeddedSigner(first, false, initial, successor); got != initial {
		t.Fatal("original installer payload changed")
	}
	if got := selectEmbeddedSigner(next, false, initial, successor); got != nil {
		t.Fatal("fixture signer admitted in production")
	}
	if got := selectEmbeddedSigner(next, true, initial, successor); got != successor {
		t.Fatal("exact controlled successor unavailable")
	}
	if got := selectEmbeddedSigner(bootstrap.MaintenanceIdentity{Version: next.Version, Source: "foreign"}, true, initial, successor); got != nil {
		t.Fatal("foreign successor source accepted")
	}
}

func TestMaintenanceEnrollmentFramePreservesPrivateStream(t *testing.T) {
	head := `{"version":1,"action":"custody-enrollment-image"}` + "\n"
	body := []byte("private image\nCHECK lock\x00tail")
	input := append([]byte(maintenance.EnrollmentStreamPrefix+head), body...)
	got, stream, err := readMaintenanceFrame(t.Context(), bytes.NewReader(input))
	if err != nil || string(got) != head || stream == nil {
		t.Fatalf("stream header: %q %v", got, err)
	}
	rest, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(rest, body) {
		t.Fatal("private stream bytes changed")
	}
	candidate := append([]byte(maintenance.CandidateStreamPrefix+head), body...)
	got, stream, err = readMaintenanceFrame(t.Context(), bytes.NewReader(candidate))
	if err != nil || string(got) != head || stream == nil {
		t.Fatal("candidate stream header not recognized", err)
	}
	if rest, err = io.ReadAll(stream); err != nil || !bytes.Equal(rest, body) {
		t.Fatal("candidate stream bytes changed")
	}
	normal := []byte(`{"version":1,"action":"remote-status"}`)
	got, stream, err = readMaintenanceFrame(t.Context(), bytes.NewReader(normal))
	if err != nil || stream != nil || !bytes.Equal(got, normal) {
		t.Fatal("ordinary request framing changed")
	}
	oversized := maintenance.EnrollmentStreamPrefix + string(bytes.Repeat([]byte("x"), (32<<10)+1)) + "\n"
	if _, _, err = readMaintenanceFrame(t.Context(), bytes.NewBufferString(oversized)); err == nil {
		t.Fatal("oversized stream header accepted")
	}
}

func TestMaintenanceInputDeadlineIncludesCallerWithholdingEOF(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := readMaintenanceInput(ctx, r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unclosed caller pipe did not respect request deadline: %v", err)
	}
}

func TestEnrollmentCoreAcceptsOnlyVerifiedBeforeOrAfterMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fased-app")
	if err := os.WriteFile(path, []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	opener := func(path string, size int64, mode os.FileMode) (*os.File, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil || info.Mode().Perm() != mode || info.Size() != size {
			f.Close()
			return nil, errors.New("protected mode differs")
		}
		return f, nil
	}
	for _, mode := range []os.FileMode{0700, 0755} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		f, err := openEnrollmentCore(path, 7, opener)
		if err != nil {
			t.Fatalf("mode %o: %v", mode, err)
		}
		f.Close()
	}
	if err := os.Chmod(path, 0777); err != nil {
		t.Fatal(err)
	}
	if f, err := openEnrollmentCore(path, 7, opener); err == nil {
		f.Close()
		t.Fatal("foreign mode accepted")
	}
}

func TestBoundCandidateRefusesRetargetingBeforeFallback(t *testing.T) {
	r := maintenance.CandidateReceipt{Version: 1, Owner: 1400, Scope: "update", PacketSHA256: strings.Repeat("a", 64), InstallerSHA256: strings.Repeat("b", 64), EngineSHA256: strings.Repeat("c", 64), ProductVersion: "0.1.12", Sequence: 12, ReleaseSHA256: strings.Repeat("d", 64), Target: update.Target{OS: "linux", Arch: "amd64"}, BaseSequence: 11, BaseReleaseSHA256: strings.Repeat("e", 64), BaseInstallation: strings.Repeat("1", 32), BaseManifestSHA: strings.Repeat("f", 64), ExpiresAt: 100}
	op := maintenance.CandidateOperation{Version: 1, Owner: r.Owner, Scope: r.Scope, PacketSHA256: r.PacketSHA256, OperationID: strings.Repeat("2", 32), RequestSHA256: strings.Repeat("3", 64)}
	if err := checkCandidateOriginal(&r, &op, r.Scope, op.OperationID, op.RequestSHA256, r.Sequence, r.ReleaseSHA256, r.EngineSHA256); err != nil {
		t.Fatal("the exact original request must remain recoverable", err)
	}
	if selected, err := candidateSelectsRelease(r, "update", r.Sequence+1, r.ReleaseSHA256, r.EngineSHA256, time.Unix(101, 0), func() error { return nil }); err != nil || selected {
		t.Fatal("regression setup must reach the old public-fallback decision", selected, err)
	}
	for _, tc := range []struct {
		name, scope, request, release, core string
		sequence                            uint64
		receipt                             *maintenance.CandidateReceipt
	}{
		{"missing receipt", "update", op.RequestSHA256, r.ReleaseSHA256, r.EngineSHA256, r.Sequence, nil},
		{"changed scope", "fresh", op.RequestSHA256, r.ReleaseSHA256, r.EngineSHA256, r.Sequence, &r},
		{"changed sequence", "update", op.RequestSHA256, r.ReleaseSHA256, r.EngineSHA256, r.Sequence + 1, &r},
		{"changed request", "update", strings.Repeat("4", 64), r.ReleaseSHA256, r.EngineSHA256, r.Sequence, &r},
		{"changed release", "update", op.RequestSHA256, strings.Repeat("5", 64), r.EngineSHA256, r.Sequence, &r},
		{"changed core", "update", op.RequestSHA256, r.ReleaseSHA256, strings.Repeat("6", 64), r.Sequence, &r},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkCandidateOriginal(tc.receipt, &op, tc.scope, op.OperationID, tc.request, tc.sequence, tc.release, tc.core); err == nil {
				t.Fatal("the original operation was allowed to select another release")
			}
		})
	}
	if err := checkCandidateOriginal(&r, &op, "update", strings.Repeat("7", 32), op.RequestSHA256, 13, strings.Repeat("8", 64), ""); err != nil {
		t.Fatal("another operation's existing admission path was changed", err)
	}
}

func TestEarlierCandidateMustSettleBeforeAnotherUpdate(t *testing.T) {
	hash := func(s string) string { return strings.Repeat(s, 64) }
	id := strings.Repeat("1", 32)
	receipt := maintenance.CandidateReceipt{Version: 1, Owner: 1400, Scope: "update", PacketSHA256: hash("a"), InstallerSHA256: hash("b"), EngineSHA256: hash("c"), ProductVersion: "0.1.12", Sequence: 12, ReleaseSHA256: hash("d"), Target: update.Target{OS: "linux", Arch: "amd64"}, BaseSequence: 11, BaseReleaseSHA256: hash("e"), BaseInstallation: strings.Repeat("2", 32), BaseManifestSHA: hash("f"), ExpiresAt: 100}
	bound := maintenance.CandidateOperation{Version: 1, Owner: receipt.Owner, Scope: receipt.Scope, PacketSHA256: receipt.PacketSHA256, OperationID: id, RequestSHA256: hash("3")}
	app := update.Artifact{File: "fased-app", Size: 1, SHA256: receipt.EngineSHA256}
	key := update.Artifact{File: "fased-signer", Size: 1, SHA256: hash("4")}
	appContract, signerContract := core.OrdinaryUpdateContract(), signer.OrdinaryUpdateContract()
	pack := &update.MaintenancePackage{Version: receipt.ProductVersion, Source: strings.Repeat("5", 40), Sequence: receipt.Sequence, ReleaseSHA256: receipt.ReleaseSHA256, DevelopmentOnly: true, Components: []update.Component{{Version: receipt.ProductVersion, Artifact: app, Contract: appContract}, {Version: receipt.ProductVersion, Artifact: key, Contract: signerContract}}}
	snapshot := update.CoreSnapshot{Version: 1, ProbeID: strings.Repeat("6", 32), BootID: strings.Repeat("7", 32), InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: 1, Anchor: hash("8")}}
	approval := &update.MaintenanceApproval{Version: 1, Owner: receipt.Owner, InstallationID: strings.Repeat("b", 32), OperationID: id, HelperSHA256: hash("9"), Source: pack.Source, ProductVersion: pack.Version, ReleaseSHA256: pack.ReleaseSHA256, DevelopmentOnly: true, PreviousCore: hash("b"), PreviousSigner: hash("c"), PreviousManifest: hash("d"), Core: snapshot, Plan: update.Manifest{Version: 1, Product: "fased-app-maintenance", Channel: "protected-installer", Sequence: receipt.Sequence, IssuedAt: 1, ExpiresAt: 2, Target: receipt.Target, Components: pack.Components}}
	pause := &update.CorePauseRequest{Version: 1, IntentID: id, Sequence: receipt.Sequence, ManifestSHA256: approval.Digest(), Expected: snapshot}
	recovery := update.RecoveryBinding{Version: 1, Pause: update.CorePauseReceipt{Version: 1, State: "paused", PauseID: strings.Repeat("9", 32), Request: pause}, Files: []update.StageFile{{Artifact: app, Bytes: app.Size}, {Artifact: key, Bytes: key.Size}}}
	index := update.RecoveryIndex{Version: 1, Phase: "metadata-backed-up", Binding: recovery, BindingSHA256: update.RecoveryDigest(recovery), Snapshot: update.MetadataSnapshot{File: "metadata.sqlite", Size: 1, SHA256: hash("a")}}
	binding := update.InstallBinding{Version: 1, Contracts: []update.Contract{appContract, signerContract}, Index: index, InstallationID: approval.InstallationID, PreviousCore: approval.PreviousCore, PreviousSigner: approval.PreviousSigner, PreviousManifest: approval.PreviousManifest, TargetCore: app.SHA256, TargetSigner: key.SHA256}
	installation := &update.Installation{Version: 1, Binding: binding, Digest: update.InstallDigest(binding), Phase: "completed", InstalledSequenceCommitted: true}
	complete := update.MaintenanceRecord{Version: 1, Owner: receipt.Owner, OperationID: id, Phase: "completed", Start: "target", Package: pack, Approval: approval, Installation: installation}
	previous := complete
	previous.OperationID, previous.Phase, previous.Start = strings.Repeat("0", 32), "idle", "base"
	read := func(r update.MaintenanceRecord, err error) func() (update.MaintenanceRecord, error) {
		return func() (update.MaintenanceRecord, error) { return r, err }
	}
	for _, tc := range []struct {
		name              string
		current, archived update.MaintenanceRecord
		archiveErr        error
		want              bool
	}{
		{"binding written before journal", previous, update.MaintenanceRecord{}, os.ErrNotExist, false},
		{"original still prepared", func() update.MaintenanceRecord { r := complete; r.Phase = "prepared"; return r }(), update.MaintenanceRecord{}, nil, false},
		{"same original completed", complete, update.MaintenanceRecord{}, nil, true},
		{"completed original archived", previous, complete, nil, true},
		{"foreign archive", previous, func() update.MaintenanceRecord { r := complete; r.Owner++; return r }(), nil, false},
		{"refused original is checked by live staging", func() update.MaintenanceRecord { r := complete; r.Phase, r.Start = "refused", "base"; return r }(), update.MaintenanceRecord{}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyEarlierCandidateWith(receipt.Owner, bound, receipt, read(tc.current, nil), read(tc.archived, tc.archiveErr))
			if (err == nil) != tc.want {
				t.Fatalf("admission = %v, want allowed %t", err, tc.want)
			}
		})
	}
}
