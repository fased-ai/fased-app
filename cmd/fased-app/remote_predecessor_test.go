//go:build linux

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

// Run the exact retained installed executable only against fresh disposable
// state. Never read the owner VPS database, credentials or recovery files.
func TestRemoteMaintenanceActualReleasedPredecessor(t *testing.T) {
	testRemoteMaintenancePredecessor(t, false)
}
func TestRemoteCustodyBridgeActualPredecessor(t *testing.T) {
	testRemoteMaintenancePredecessor(t, true)
}
func testRemoteMaintenancePredecessor(t *testing.T, custody bool) {
	image := os.Getenv("FASED_REMOTE_PREDECESSOR")
	if image == "" {
		t.Skip("actual retained predecessor supplied by focused acceptance")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	name := "fased"
	if custody {
		name = "app-state"
	}
	dir := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, image, "serve", "--data-dir", dir, "--listen", "127.0.0.1:0")
	var stderr readerIPCLog
	child.Stdout, child.Stderr = io.Discard, &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	var code core.ClaimCode
	for {
		if raw, err := os.ReadFile(filepath.Join(dir, "claim-code.json")); err == nil && json.Unmarshal(raw, &code) == nil && code.Code != "" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("predecessor startup", stderr.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	c, err := core.NewClient(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Claim(ctx, code.Code, "disposable-predecessor-password"); err != nil {
		t.Fatal(err)
	}
	if err = c.SaveSession(dir); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "core.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var beforeVersion int
	var beforeSalt, beforeHash []byte
	if err = db.QueryRow("PRAGMA user_version").Scan(&beforeVersion); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&beforeSalt, &beforeHash); err != nil {
		t.Fatal(err)
	}
	// Remove only this disposable CLI credential. The old owner session remains.
	if err = os.Remove(filepath.Join(dir, "cli-session.json")); err != nil {
		t.Fatal(err)
	}
	fresh, err := core.NewClient(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	m := vpsinstall.Manifest{Data: dir, AppUID: os.Getuid(), OwnerUID: os.Getuid() + 1}
	if custody {
		baseTransport := fresh.HTTP.Transport
		if baseTransport == nil {
			baseTransport = http.DefaultTransport
		}
		fresh.HTTP.Transport = bootstrapInventoryTransport(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/api/v1/health" {
				rec := httptest.NewRecorder()
				json.NewEncoder(rec).Encode(core.Health{InstanceID: fresh.Endpoint.InstanceID, Connection: &core.ConnectionInfo{Location: "remote", OwnerKind: "custody", OwnerUID: m.OwnerUID}})
				return rec.Result(), nil
			}
			return baseTransport.RoundTrip(req)
		})
		manifest := custodyinstall.Manifest{Root: filepath.Dir(dir), Owner: m.OwnerUID, Location: "remote"}
		manifest.App.UID = m.AppUID
		// OS/image binding is exercised separately by installer tests.
		if filepath.Base(dir) != "app-state" {
			t.Fatal("custody fixture requires app-state")
		}
		err = prepareCustodyReaderSession(ctx, fresh, manifest, fresh.Endpoint.InstanceID)
	} else {
		err = loadRemoteHelperSession(ctx, fresh, m)
	}
	if err != nil {
		t.Fatal("password-free predecessor session", err)
	}
	// Exercise the immutable command actually selected by the Remote helper
	// policy. Its output is the application-update view, not update-status.
	beforeSession, err := os.ReadFile(filepath.Join(dir, "cli-session.json"))
	if err != nil {
		t.Fatal(err)
	}
	statusOutput, err := exec.CommandContext(ctx, image, "updates", "status", "--data-dir", dir).Output()
	if err != nil {
		t.Fatal("actual predecessor update status", err)
	}
	var status core.ApplicationUpdateView
	if json.Unmarshal(statusOutput, &status) != nil || status.Version != 1 || status.Schema != beforeVersion || status.InstanceID != c.Endpoint.InstanceID {
		t.Fatal("actual predecessor update status did not bind the expected instance and schema")
	}
	healthOutput, err := exec.CommandContext(ctx, image, "status", "--data-dir", dir).Output()
	if err != nil {
		t.Fatal("actual predecessor health status", err)
	}
	var health core.Health
	if json.Unmarshal(healthOutput, &health) != nil || health.Status != "ok" || health.APIVersion != core.APIVersion || health.InstanceID != status.InstanceID {
		t.Fatal("actual predecessor status responses disagree")
	}
	afterSession, err := os.ReadFile(filepath.Join(dir, "cli-session.json"))
	if err != nil || !bytes.Equal(beforeSession, afterSession) {
		t.Fatal("read-only status changed the saved predecessor session")
	}
	probe := update.NewProbe()
	result, err := exec.CommandContext(ctx, image, "update-inventory", "--data-dir", dir, "--probe-id", probe).CombinedOutput()
	if err != nil {
		t.Fatal("actual predecessor rejected new session", err, string(result))
	}
	var snapshot update.CoreSnapshot
	if json.Unmarshal(result, &snapshot) != nil || snapshot.ProbeID != probe || snapshot.InstanceID != c.Endpoint.InstanceID || snapshot.State.Version != beforeVersion {
		t.Fatal("source identity/schema changed", string(result))
	}
	if custody {
		for _, action := range []string{"update-pause", "update-backup", "update-recovery"} {
			command := exec.CommandContext(ctx, image, action, "--data-dir", dir, "--request-stdin")
			command.Stdin = bytes.NewBufferString("{}\n")
			result, e := command.CombinedOutput()
			if e == nil || (!strings.Contains(string(result), "(400)") && !strings.Contains(string(result), "(409)")) {
				t.Fatalf("%s did not reach typed refusal after authentication: %v %s", action, e, result)
			}
		}
	}

	var afterVersion int
	var afterSalt, afterHash []byte
	if err = db.QueryRow("PRAGMA user_version").Scan(&afterVersion); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&afterSalt, &afterHash); err != nil {
		t.Fatal(err)
	}
	if afterVersion != beforeVersion || string(beforeSalt) != string(afterSalt) || string(beforeHash) != string(afterHash) {
		t.Fatal("predecessor migration/owner change")
	}
	var auth struct {
		Authenticated bool `json:"authenticated"`
	}
	if err = c.Do(ctx, http.MethodGet, "/api/v1/auth/state", nil, &auth); err != nil || !auth.Authenticated {
		t.Fatal("original owner session lost", err)
	}
	t.Logf("actual predecessor schema=%d; missing CLI session recovered; original owner/session preserved; no migration", beforeVersion)
}
