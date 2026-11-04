//go:build linux

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

// Run the exact retained installed executable only against fresh disposable
// state. Never read the owner VPS database, credentials or recovery files.
func TestRemoteMaintenanceActualReleasedPredecessor(t *testing.T) {
	image := os.Getenv("FASED_REMOTE_PREDECESSOR")
	if image == "" {
		t.Skip("actual retained predecessor supplied by focused acceptance")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "fased")
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
	if err = loadRemoteHelperSession(ctx, fresh, m); err != nil {
		t.Fatal("password-free predecessor session", err)
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
