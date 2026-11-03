package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalOwnerFormatPreservesExistingIdentity(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-local", true: "existing-password"}[existing], func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "state")
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if existing {
				claim, err := s.IssueClaimCode(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.Claim(t.Context(), claim.Code, "preserved-owner-password"); err != nil {
					t.Fatal(err)
				}
			}
			var salt, hash []byte
			var workspace string
			if err = s.db.QueryRow("SELECT password_salt,password_hash FROM instance").Scan(&salt, &hash); err != nil {
				t.Fatal(err)
			}
			if err = s.db.QueryRow("SELECT document FROM agent_workspace").Scan(&workspace); err != nil {
				t.Fatal(err)
			}
			if _, err = s.localOwnerSession(t.Context()); err != nil {
				t.Fatal(err)
			}
			var nextSalt, nextHash []byte
			var nextWorkspace string
			var schema int
			if err = s.db.QueryRow("SELECT password_salt,password_hash FROM instance").Scan(&nextSalt, &nextHash); err != nil {
				t.Fatal(err)
			}
			if err = s.db.QueryRow("SELECT document FROM agent_workspace").Scan(&nextWorkspace); err != nil {
				t.Fatal(err)
			}
			if err = s.db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			if nextWorkspace != workspace {
				t.Fatal("records changed")
			}
			if existing && (schema != 36 || !bytes.Equal(salt, nextSalt) || !bytes.Equal(hash, nextHash)) {
				t.Fatal("existing identity/format changed")
			}
			if !existing && (schema != 37 || !localPasswordDisabled(nextSalt)) {
				t.Fatal("new Local identity not protected")
			}
			snap, err := s.UpdateSnapshot(t.Context(), "0123456789abcdef0123456789abcdef")
			if err != nil || snap.State.Version != schema {
				t.Fatal("wrong format snapshot", err)
			}
		})
	}
}

func TestLocalOwnerActualReleasedReaderRefusesNewState(t *testing.T) {
	binary := os.Getenv("FASED_READER_INSTALLED_SOURCE")
	if binary == "" {
		t.Skip("actual retained public reader supplied by Local acceptance")
	}
	dir := filepath.Join(t.TempDir(), "state")
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.localOwnerSession(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "core.sqlite")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "serve", "--data-dir", dir, "--listen", "127.0.0.1:0").CombinedOutput()
	if err == nil || ctx.Err() != nil || !bytes.Contains(output, []byte("newer")) {
		t.Fatal("released reader did not refuse new auth format", err, string(output))
	}
	after, err := os.ReadFile(file)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("older reader changed data", err)
	}
}

func TestLocalOwnerCLIPreservesMalformedSession(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "wrong", "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"} {
		path := filepath.Join(dir, "cli-session.json")
		if err := WritePrivateJSON(path, SavedSession{InstanceID: "same-instance", Token: token}); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(path)
		client := &Client{Endpoint: Endpoint{InstanceID: "same-instance"}, localOwner: true}
		if err := client.LoadSession(t.Context(), dir); err == nil || errors.Is(err, ErrUnauthorized) {
			t.Fatal("corrupt credential became reauthorization", err)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("corrupt credential was replaced")
		}
	}
}
