//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/maintenance"
)

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
