//go:build linux

package enrollment

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
)

func TestProtectedEnrollmentJournalRequiresOriginalLockForMutation(t *testing.T) {
	if os.Getuid() <= 0 || os.Getgid() <= 0 {
		t.Skip("ordinary owner required")
	}
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "fased-app-enrollment")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	owner := os.Getuid()
	j := journal{Version: 1, ID: strings.Repeat("a", 32), Owner: owner, Phase: "prepared", Network: "devnet", CoreHash: strings.Repeat("b", 64), SignerHash: strings.Repeat("c", 64), Source: sourceRecord{Profile: "local", Data: filepath.Join(home, "source"), Listen: "127.0.0.1:7332", ExpectedInstanceID: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), UID: owner, GID: os.Getgid()}, Node: node{Device: 1, Inode: 2, UID: owner, GID: os.Getgid(), Mode: 0700}}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "state.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := core.LockInstance(filepath.Join(dir, "controller.lock"))
	if err != nil {
		t.Fatal(err)
	}
	a := maintenance.Authority{Owner: owner, Group: os.Getgid(), Home: home}
	if got, err := openOwnerEnrollmentJournal(a, true); err != nil || got.ID != j.ID {
		t.Fatalf("held original journal rejected: %v", err)
	}
	pending := filepath.Join(dir, ".pending-123")
	if err = os.WriteFile(pending, []byte("original pending write"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, requireLock := range []bool{false, true} {
		if _, err = openOwnerEnrollmentJournal(a, requireLock); err == nil {
			t.Fatal("protected reader accepted an ambiguous pending journal")
		}
	}
	if b, err := os.ReadFile(pending); err != nil || string(b) != "original pending write" {
		t.Fatal("protected reader changed the pending record", err)
	}
	if err = os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = openOwnerEnrollmentJournal(a, true); err == nil {
		t.Fatal("mutation accepted after controller lock was released")
	}
	if _, err = openOwnerEnrollmentJournal(a, false); err != nil {
		t.Fatalf("read-only interrupted-state inspection failed: %v", err)
	}
	if err = os.Chmod(filepath.Join(dir, "state.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = openOwnerEnrollmentJournal(a, false); err == nil {
		t.Fatal("nonprivate owner journal accepted")
	}
}

func TestProtectedEnrollmentHeldJournalIsReadOnly(t *testing.T) {
	if os.Getuid() <= 0 || os.Getgid() <= 0 {
		t.Skip("ordinary owner required")
	}
	_, _, _, _, j := suspendedRecoveryFixture(t)
	j.Phase = "source-lock-lost"
	j.Digest = strings.Repeat("d", 64)
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "fased-app-enrollment")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := core.LockInstance(filepath.Join(dir, "controller.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	a := maintenance.Authority{Owner: os.Getuid(), Group: os.Getgid(), Home: home}
	got, err := openOwnerEnrollmentJournal(a, false)
	if err != nil || got.Phase != j.Phase || got.ID != j.ID {
		t.Fatal("held state not available for read-only inspection", err)
	}
	if _, err := openOwnerEnrollmentJournal(a, true); err == nil {
		t.Fatal("held state allowed a mutation")
	}
}

func TestProtectedRemoteEnrollmentUsesSavedIntentOnly(t *testing.T) {
	source := ProtectedSource{Data: "/var/lib/fased-app-vps-1000/data", Listen: "127.0.0.1:7332", PublicOrigin: "https://example.ts.net", UID: 1234, GID: 1234}
	j := journal{Phase: "suspending", Source: sourceRecord{Profile: "remote", Data: source.Data, Listen: source.Listen, PublicOrigin: source.PublicOrigin, UID: source.UID, GID: source.GID, Running: true, Autostart: false}}
	running, autostart, err := remoteEnrollmentIntent(j, source, "restore")
	if err != nil || !running || autostart {
		t.Fatalf("saved intent changed: %v", err)
	}
	source.ApplicationSequence, source.ApplicationReleaseSHA256 = 7, strings.Repeat("d", 64)
	j.Source.ApplicationSequence, j.Source.ApplicationReleaseSHA256 = 7, strings.Repeat("d", 64)
	if _, _, err = remoteEnrollmentIntent(j, source, "restore"); err != nil {
		t.Fatal("matching protected release identity rejected", err)
	}
	source.ApplicationReleaseSHA256 = strings.Repeat("e", 64)
	if _, _, err = remoteEnrollmentIntent(j, source, "restore"); err == nil {
		t.Fatal("foreign protected release identity accepted")
	}
	source.ApplicationReleaseSHA256 = j.Source.ApplicationReleaseSHA256
	for _, phase := range []string{"prepared", "importing", "fencing", "complete"} {
		j.Phase = phase
		if _, _, err = remoteEnrollmentIntent(j, source, "restore"); err == nil {
			t.Fatalf("restore accepted phase %q", phase)
		}
	}
	j.Phase = "suspended"
	source.UID++
	if _, _, err = remoteEnrollmentIntent(j, source, "suspend"); err == nil {
		t.Fatal("foreign Remote identity accepted")
	}
}
