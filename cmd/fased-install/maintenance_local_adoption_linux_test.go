//go:build linux

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
)

func TestLocalAdoptionImagePreservesAuthenticationBeforeStart(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires an ordinary fixture user")
	}
	source := filepath.Join(t.TempDir(), "original")
	stage := filepath.Join(t.TempDir(), "app-state")
	for _, dir := range []string{source, stage} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s, err := core.OpenStore(source)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.IssueClaimCode(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const password = "disposable fixture password"
	token, err := s.Claim(t.Context(), claim.Code, password)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	image, lease, err := core.ExportEnrollmentImage(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	digest := core.EnrollmentImageDigest(image)
	identity := bootstrap.LocalHandoffStage{
		LocalHandoffPreflight: bootstrap.LocalHandoffPreflight{Owner: os.Getuid(),
			LegacyPacketSHA256: maintenance.LegacyLocalPredecessorPacket, TargetPacketSHA256: maintenance.LegacyLocalRetainedPacket,
			TargetBundleSHA256: maintenance.LegacyLocalRetainedBundle},
		Version: 1, InstanceID: image.InstanceID, StateSchema: image.Schema, StateSHA256: digest,
	}
	var wire bytes.Buffer
	e := json.NewEncoder(&wire)
	if err := e.Encode(identity); err != nil {
		t.Fatal(err)
	}
	if err := e.Encode(image); err != nil {
		t.Fatal(err)
	}
	wire.WriteString("ok\n")
	r := bufio.NewReader(&wire)
	header, raw, err := readLocalAdoptionImage(r)
	if err != nil || header != identity {
		t.Fatal("private image frame substituted its identity", err)
	}
	if more, err := r.ReadString('\n'); err != nil || more != "ok\n" {
		t.Fatal("the image reader consumed the source lease acknowledgement", err)
	}
	invoke := func(action, workspace, hash string) error {
		return run([]string{"maintenance-local-adoption-image", action, stage, workspace, hash}, bytes.NewReader(raw), io.Discard, io.Discard)
	}
	if err := invoke("import", "other", digest); err == nil {
		t.Fatal("a different workspace was accepted")
	}
	if err := invoke("import", image.InstanceID, strings.Repeat("1", 64)); err == nil {
		t.Fatal("substituted image was accepted")
	}
	if entries, err := os.ReadDir(stage); err != nil || len(entries) != 0 {
		t.Fatal("a refused import wrote state", err)
	}
	if err := invoke("verify", image.InstanceID, digest); err == nil {
		t.Fatal("missing imported state admitted for Apply")
	}
	if err := invoke("import", image.InstanceID, digest); err != nil {
		t.Fatal("could not import test image", err)
	}
	if err := invoke("verify", image.InstanceID, digest); err != nil {
		t.Fatal("exact imported state refused", err)
	}
	dbPath := filepath.Join(stage, "core.sqlite")
	f, err := os.Stat(dbPath)
	if err != nil || f.Size() < 2 {
		t.Fatal("test import has no database", err)
	}
	if err := os.Truncate(dbPath, f.Size()-1); err != nil {
		t.Fatal(err)
	}
	if err := invoke("verify", image.InstanceID, digest); err == nil {
		t.Fatal("partial imported state admitted for Apply")
	}
	partial, err := os.Stat(dbPath)
	if err != nil || partial.Size() != f.Size()-1 {
		t.Fatal("read-only verification repaired the truncated state", err)
	}
	if err := invoke("import", image.InstanceID, digest); err != nil {
		t.Fatal("exact original import could not resume", err)
	}
	imported, err := core.OpenStore(stage)
	if err != nil {
		t.Fatal(err)
	}
	defer imported.Close()
	if !imported.Session(t.Context(), token) {
		t.Fatal("the original authenticated session was lost")
	}
	if _, err := imported.Login(t.Context(), password); err != nil {
		t.Fatal("the original owner password did not work", err)
	}
}

func TestLocalAdoptionRequiresAdministratorEvenForValidArguments(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires an ordinary fixture user")
	}
	args := []string{"maintenance-package-adopt-local", "--owner", strconv.Itoa(os.Getuid()), "--location", "local", "--instance", strings.Repeat("a", 32),
		"--listen", "127.0.0.1:8080", "--legacy-candidate", "/old", "--retained-candidate", "/target", "--prefix", "/old-app", "--data-dir", "/old-state"}
	err := run(args, strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "administrator Local adoption required") {
		t.Fatalf("ordinary caller reached protected adoption: %v", err)
	}
	for _, action := range []string{"maintenance-package-apply-local-adoption", "maintenance-package-recover-local-adoption"} {
		args[0] = action
		if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "administrator Local adoption required") {
			t.Fatalf("ordinary caller reached %s: %v", action, err)
		}
	}
}
