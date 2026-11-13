//go:build linux

package maintenance

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func successorFixture(t *testing.T) (Successor, Authority, update.MaintenanceRecord, update.MaintenanceApproval) {
	t.Helper()
	id, installation := strings.Repeat("a", 32), strings.Repeat("b", 32)
	old, next := strings.Repeat("c", 64), strings.Repeat("d", 64)
	coreHash, signerHash := strings.Repeat("e", 64), strings.Repeat("f", 64)
	snapshot := update.CoreSnapshot{Version: 1, BootID: strings.Repeat("1", 32), ProbeID: strings.Repeat("2", 32), InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: 1, Anchor: strings.Repeat("3", 64)}}
	components := []update.Component{
		{Version: "0.1.11", Artifact: update.Artifact{File: "fased-app", Size: 10, SHA256: coreHash}, Contract: core.OrdinaryUpdateContract()},
		{Version: "0.1.11", Artifact: update.Artifact{File: "fased-signer", Size: 11, SHA256: signerHash}, Contract: signer.OrdinaryUpdateContract()},
	}
	plan := update.Manifest{Version: 1, Product: "fased-app-maintenance", Channel: "protected-installer", Sequence: 8, IssuedAt: 1, ExpiresAt: 2, Target: update.Target{OS: "linux", Arch: "amd64"}, Components: components}
	approval := update.MaintenanceApproval{Version: 1, Owner: 1400, InstallationID: installation, OperationID: id, HelperSHA256: old, Source: strings.Repeat("4", 40), ProductVersion: "0.1.11", ReleaseSHA256: strings.Repeat("5", 64), DevelopmentOnly: true, PreviousCore: strings.Repeat("6", 64), PreviousSigner: strings.Repeat("7", 64), PreviousManifest: strings.Repeat("8", 64), Core: snapshot, Plan: plan}
	if err := approval.Validate(); err != nil {
		t.Fatalf("invalid approval fixture: %v", err)
	}
	request := &update.CorePauseRequest{Version: 1, IntentID: id, Sequence: 8, ManifestSHA256: approval.Digest(), Expected: snapshot}
	pause := update.CorePauseReceipt{Version: 1, State: "paused", PauseID: strings.Repeat("9", 32), Request: request, Core: snapshot}
	files := []update.StageFile{{Artifact: components[0].Artifact, Bytes: 10}, {Artifact: components[1].Artifact, Bytes: 11}}
	backup := update.RecoveryBinding{Version: 1, Pause: pause, Files: files}
	index := update.RecoveryIndex{Version: 1, Phase: "metadata-backed-up", Binding: backup, BindingSHA256: update.RecoveryDigest(backup), Snapshot: update.MetadataSnapshot{File: "metadata.sqlite", Size: 1, SHA256: strings.Repeat("a", 64)}}
	binding := update.InstallBinding{Version: 1, Contracts: []update.Contract{components[0].Contract, components[1].Contract}, Index: index, InstallationID: installation, PreviousCore: approval.PreviousCore, PreviousSigner: approval.PreviousSigner, PreviousManifest: approval.PreviousManifest, TargetCore: coreHash, TargetSigner: signerHash}
	hold := &update.Installation{Version: 1, Binding: binding, Digest: update.InstallDigest(binding), Phase: "active"}
	if !update.ValidInstallation(*hold) {
		t.Fatal("invalid signer hold fixture")
	}
	r := update.MaintenanceRecord{Version: 1, Owner: 1400, OperationID: id, Phase: "ready", Start: "target", Approval: &approval, Staged: &update.Intent{ID: id, Sequence: 8, ManifestSHA256: approval.Digest()}, Recovery: &index, Installation: hold, Package: &update.MaintenancePackage{Sequence: 8, ReleaseSHA256: approval.ReleaseSHA256, Source: approval.Source}}
	a := Authority{Version: 1, Owner: 1400, ImageSHA256: old}
	h := Successor{Version: 1, Owner: 1400, OperationID: id, PreviousImage: old, SuccessorImage: next, ApprovalDigest: approval.Digest(), InstallationDigest: hold.Digest}
	return h, a, r, approval
}

func TestSuccessorMissingDependencyDoesNotMeanFresh(t *testing.T) {
	h, _, _, _ := successorFixture(t)
	h.Owner = 2147483000
	_, err := readSuccessor(h.Owner, func() (io.ReadCloser, error) { return nil, os.ErrNotExist })
	if !errors.Is(err, ErrNoSuccessor) {
		t.Fatal("absent successor record was not classified as absent", err)
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "successor.json")
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = readSuccessor(h.Owner, func() (io.ReadCloser, error) { return os.Open(path) })
	if err == nil || errors.Is(err, ErrNoSuccessor) {
		t.Fatal("missing authority or dependency was classified as fresh", err)
	}
}

func TestSuccessorMissingCustodyRootIsNotAnAbsentFence(t *testing.T) {
	h, _, _, _ := successorFixture(t)
	h.Owner = 2147483000
	if _, err := os.Lstat("/var/lib/fased-app-custody-2147483000"); !errors.Is(err, os.ErrNotExist) {
		t.Skip("the reserved absent fixture path is not absent")
	}
	if fenced, err := readSuccessorFence(h); err == nil || fenced {
		t.Fatal("missing custody installation was treated as an absent fence", err)
	}
}

func TestSuccessorManifestAcceptsOnlyRecordedInstallation(t *testing.T) {
	h, _, r, approval := successorFixture(t)
	root := "/var/lib/fased-app-custody-1400"
	makeManifest := func(owner int, id, root, generation string) []byte {
		b, err := json.Marshal(map[string]any{"owner": owner, "id": id, "root": root, "coreHash": generation})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	base := makeManifest(h.Owner, approval.InstallationID, root, "base")
	target := makeManifest(h.Owner, approval.InstallationID, root, "target")
	r.Base, r.Target, r.Start = base, target, "target"
	if err := validateSuccessorManifest(h, approval, r, target); err != nil {
		t.Fatal(err)
	}
	if validateSuccessorManifest(h, approval, r, base) == nil {
		t.Fatal("base accepted after target start")
	}
	r.Start = ""
	if err := validateSuccessorManifest(h, approval, r, base); err != nil {
		t.Fatal("interrupted base refused", err)
	}
	if err := validateSuccessorManifest(h, approval, r, target); err != nil {
		t.Fatal("interrupted target refused", err)
	}
	for _, foreign := range [][]byte{
		nil,
		makeManifest(h.Owner+1, approval.InstallationID, root, "target"),
		makeManifest(h.Owner, strings.Repeat("e", 32), root, "target"),
		makeManifest(h.Owner, approval.InstallationID, root+"-other", "target"),
		makeManifest(h.Owner, approval.InstallationID, root, "foreign"),
		[]byte(`{"owner":1400,"owner":1401}`),
	} {
		if validateSuccessorManifest(h, approval, r, foreign) == nil {
			t.Fatal("foreign manifest accepted")
		}
	}
}

func TestSuccessorBindsOriginalApprovalAndSignerHold(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Successor, *Authority, *update.MaintenanceRecord, *update.MaintenanceApproval)
		want bool
	}{
		{"original", func(*Successor, *Authority, *update.MaintenanceRecord, *update.MaintenanceApproval) {}, true},
		{"stopped transition", func(_ *Successor, _ *Authority, r *update.MaintenanceRecord, _ *update.MaintenanceApproval) {
			r.Phase, r.Start = "quiescing", ""
		}, true},
		{"foreign operation", func(h *Successor, _ *Authority, _ *update.MaintenanceRecord, _ *update.MaintenanceApproval) {
			h.OperationID = strings.Repeat("1", 32)
		}, false},
		{"rotated authority", func(h *Successor, a *Authority, _ *update.MaintenanceRecord, _ *update.MaintenanceApproval) {
			a.ImageSHA256 = h.SuccessorImage
		}, false},
		{"same helper", func(h *Successor, _ *Authority, _ *update.MaintenanceRecord, _ *update.MaintenanceApproval) {
			h.SuccessorImage = h.PreviousImage
		}, false},
		{"changed release", func(_ *Successor, _ *Authority, r *update.MaintenanceRecord, _ *update.MaintenanceApproval) {
			r.Package.ReleaseSHA256 = strings.Repeat("9", 64)
		}, false},
		{"changed signer hold", func(_ *Successor, _ *Authority, r *update.MaintenanceRecord, _ *update.MaintenanceApproval) {
			r.Installation.Digest = strings.Repeat("9", 64)
		}, false},
		{"changed backup", func(_ *Successor, _ *Authority, r *update.MaintenanceRecord, _ *update.MaintenanceApproval) {
			r.Recovery.Snapshot.SHA256 = strings.Repeat("9", 64)
		}, false},
		{"unsafe start decision", func(_ *Successor, _ *Authority, r *update.MaintenanceRecord, _ *update.MaintenanceApproval) {
			r.Phase, r.Start = "replacing", "target"
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, a, r, approval := successorFixture(t)
			tc.edit(&h, &a, &r, &approval)
			if err := validateSuccessor(h, a, r, approval); (err == nil) != tc.want {
				t.Fatalf("successor validation: %v", err)
			}
		})
	}
}

func TestSuccessorAuthorityRotationRequiresExactCompletedHold(t *testing.T) {
	h, a, r, approval := successorFixture(t)
	a.ImageSHA256 = h.SuccessorImage
	if validateSuccessorTransition(h, a, r, approval, false, false) == nil {
		t.Fatal("rotated helper was accepted without its fence")
	}
	if validateSuccessorTransition(h, a, r, approval, true, false) == nil {
		t.Fatal("active signer hold was accepted for authority rotation")
	}
	r.Phase = "completed"
	r.Installation.Phase = "completed"
	r.Installation.InstalledSequenceCommitted = true
	if err := validateSuccessorTransition(h, a, r, approval, true, false); err != nil {
		t.Fatal("fenced, completed original operation was refused", err)
	}
	if err := validateSuccessorTransition(h, a, r, approval, false, true); err != nil {
		t.Fatal("archived original operation was refused", err)
	}
	if validateSuccessorTransition(h, a, r, approval, false, false) == nil {
		t.Fatal("unfenced, unarchived rotated authority was accepted")
	}
	approval.OperationID = strings.Repeat("e", 32)
	if validateSuccessorTransition(h, a, r, approval, true, true) == nil {
		t.Fatal("another operation's approval was accepted")
	}
	if _, err := SuccessorFence(h); err != nil {
		t.Fatal("original operation fence refused", err)
	}
	h.SuccessorImage = h.PreviousImage
	if _, err := SuccessorFence(h); err == nil {
		t.Fatal("a fence accepted identical helpers")
	}
}

func TestSuccessorHistoryRejectsAnotherOperationWhileFenced(t *testing.T) {
	h, a, original, _ := successorFixture(t)
	a.ImageSHA256 = h.SuccessorImage
	original.Phase, original.Installation.Phase = "completed", "completed"
	original.Installation.InstalledSequenceCommitted = true
	current := original
	current.OperationID = strings.Repeat("e", 32)
	if _, err := selectSuccessorRecord(h, a, current, original, true, true); err == nil {
		t.Fatal("a later operation was accepted while the original remains fenced")
	}
	if _, err := selectSuccessorRecord(h, a, current, original, false, false); err == nil {
		t.Fatal("a later operation was accepted without the original archive")
	}
	got, err := selectSuccessorRecord(h, a, current, original, true, false)
	if err != nil || got.OperationID != h.OperationID {
		t.Fatal("the immutable original receipt was not selected", err)
	}
	changed := original
	changed.Phase = "ready"
	if _, err = selectSuccessorRecord(h, a, original, changed, true, false); err == nil {
		t.Fatal("a changed archive was accepted for the current original operation")
	}
}
