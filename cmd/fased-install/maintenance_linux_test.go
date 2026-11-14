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
	"github.com/fased-ai/fased-app/internal/maintenance"
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
