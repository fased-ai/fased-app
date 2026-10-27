//go:build linux

package core

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func backupProof(t *testing.T, s *Store) update.Preflight {
	t.Helper()
	c := pauseCommand(t, s, 1)
	p := update.Preflight{Version: 1, IntentID: c.Request.IntentID, Sequence: 1, ManifestSHA256: c.Request.ManifestSHA256, Verified: true}
	p.Pause = update.CorePauseReceipt{Version: 1, State: "paused", PauseID: update.NewProbe(), Request: c.Request, Core: c.Request.Expected}
	p.Signer = update.SignerSnapshot{Version: 1, ProbeID: c.ProbeID, BootID: update.NewProbe(), AuthoritySHA256: c.Request.ManifestSHA256, SequenceFloor: 1, ActiveIntentID: p.IntentID, States: []update.State{{Name: "vault"}, {Name: "operations"}, {Name: "mock-receipts"}}}
	for _, name := range []string{"app", "signer"} {
		a := update.Artifact{File: name, Size: 4, SHA256: update.SnapshotDigest("artifact", name)}
		p.Files = append(p.Files, update.StageFile{Artifact: a, Bytes: 4})
	}
	p.BindingSHA256 = update.SnapshotDigest("preflight:v1", p)
	return p
}
func pausedBackup(t *testing.T, s *Store) update.Preflight {
	t.Helper()
	p := backupProof(t, s)
	r, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "pause", ProbeID: p.Pause.Core.ProbeID, Request: p.Pause.Request})
	if e != nil {
		t.Fatal(e)
	}
	p.Pause = r
	return rebindProof(p)
}
func rebindProof(p update.Preflight) update.Preflight {
	p.BindingSHA256 = ""
	p.BindingSHA256 = update.SnapshotDigest("preflight:v1", p)
	return p
}

func TestMetadataBackupConsistentPrivateAndNoRestore(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	p := pausedBackup(t, s)
	i, e := s.BackupMetadata(t.Context(), p)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := update.BackupBinding(p)
	if !update.ValidRecoveryIndex(i, b) {
		t.Fatal(i)
	}
	dir := filepath.Join(s.dir, "lifecycle", p.IntentID)
	for _, name := range []string{"request.json", "metadata.sqlite", "recovery.json", "backup.lock"} {
		st, e := os.Stat(filepath.Join(dir, name))
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal(name, st, e)
		}
	}
	db, e := sql.Open("sqlite", "file:"+filepath.Join(dir, "metadata.sqlite")+"?mode=ro&immutable=1")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var hash []byte
	if e = db.QueryRow("SELECT password_hash FROM instance").Scan(&hash); e != nil || len(hash) == 0 {
		t.Fatal("credentials absent", e)
	}
	var sessions int
	if e = db.QueryRow("SELECT count(*) FROM sessions").Scan(&sessions); e != nil || sessions < 1 {
		t.Fatal("sessions absent", e)
	}
	if !s.Session(t.Context(), token) {
		t.Fatal("backup changed owner session")
	}
	got, e := s.BackupMetadata(t.Context(), p)
	if e != nil || update.SnapshotDigest("index", got) != update.SnapshotDigest("index", i) {
		t.Fatal("retry changed index", e)
	}
	_, e = s.UpdateSetup(t.Context(), 0, DefaultSetup())
	if !errors.Is(e, ErrCorePaused) {
		t.Fatal("backup released pause", e)
	}
	// Real SQLite/WAL image, not raw live file copying or JSON reconstruction.
	raw, _ := os.ReadFile(filepath.Join(dir, "metadata.sqlite"))
	if !bytes.HasPrefix(raw, []byte("SQLite format 3\x00")) {
		t.Fatal("not SQLite")
	}
}

func TestMetadataBackupRecoverableFaultsAndRestart(t *testing.T) {
	for _, step := range []string{"write:request.json", "publish:request.json", "backup:request", "write:metadata.sqlite", "publish:metadata.sqlite", "backup:snapshot", "backup:verified", "write:recovery.json", "publish:recovery.json", "backup:durable"} {
		t.Run(step, func(t *testing.T) {
			s := fixtureStore(t)
			claimFixture(t, s)
			p := pausedBackup(t, s)
			s.backupFault = func(at string) error {
				if at == step {
					return syscall.ENOSPC
				}
				return nil
			}
			if _, e := s.BackupMetadata(t.Context(), p); !errors.Is(e, syscall.ENOSPC) {
				t.Fatal("fault not reached", e)
			}
			dir := s.dir
			s.Close()
			var e error
			s, e = OpenStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			p.Pause, e = s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
			if e != nil {
				t.Fatal(e)
			}
			p.Signer.ProbeID = p.Pause.Core.ProbeID
			p.Signer.BootID = update.NewProbe()
			p = rebindProof(p)
			if _, e = s.BackupMetadata(t.Context(), p); e != nil {
				t.Fatal("same input could not recover", e)
			}
		})
	}
}

func TestMetadataBackupRejectsStaleConflictingAndUnsafeState(t *testing.T) {
	for _, kind := range []string{"boot", "floor", "artifact", "released", "corrupt", "symlink", "hardlink", "directory", "partial"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureStore(t)
			p := pausedBackup(t, s)
			if kind == "boot" {
				p.Pause.Core.BootID = update.NewProbe()
				p = rebindProof(p)
			}
			if kind == "floor" {
				p.Signer.SequenceFloor++
				p = rebindProof(p)
			}
			if kind == "artifact" {
				p.Files[0].Bytes--
				p = rebindProof(p)
			}
			if kind == "released" {
				_, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "release", ProbeID: p.Pause.Core.ProbeID, Aborted: &update.Intent{State: "aborted", ID: p.IntentID, Sequence: p.Sequence, ManifestSHA256: p.ManifestSHA256, Core: p.Pause.Request.Expected}})
				if e != nil {
					t.Fatal(e)
				}
			}
			dir := filepath.Join(s.dir, "lifecycle", p.IntentID)
			if kind == "corrupt" {
				if _, e := s.BackupMetadata(t.Context(), p); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(filepath.Join(dir, "metadata.sqlite"), []byte("foreign"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "symlink" || kind == "hardlink" || kind == "directory" || kind == "partial" {
				if e := os.MkdirAll(dir, 0700); e != nil {
					t.Fatal(e)
				}
				target := filepath.Join(dir, "metadata.sqlite.pending")
				switch kind {
				case "symlink":
					os.Symlink(filepath.Join(s.dir, "core.sqlite"), target)
				case "hardlink":
					os.Link(filepath.Join(s.dir, "core.sqlite"), target)
				case "directory":
					os.Mkdir(target, 0700)
				case "partial":
					os.WriteFile(target, []byte("conflict"), 0600)
				}
			}
			if _, e := s.BackupMetadata(t.Context(), p); e == nil {
				t.Fatal("unsafe/stale backup accepted")
			}
		})
	}
}

func TestMetadataBackupConcurrentAndChangedSource(t *testing.T) {
	s := fixtureStore(t)
	claimFixture(t, s)
	p := pausedBackup(t, s)
	var wg sync.WaitGroup
	var mu sync.Mutex
	count := 0
	for n := 0; n < 4; n++ {
		wg.Go(func() {
			if _, e := s.BackupMetadata(t.Context(), p); e == nil {
				mu.Lock()
				count++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if count == 0 {
		t.Fatal("no backup won")
	}
	i, e := s.BackupMetadata(t.Context(), p)
	if e != nil {
		t.Fatal(e)
	}
	other := p
	other.Signer.States = append([]update.State(nil), p.Signer.States...)
	other.Signer.States[0].Anchor = update.SnapshotDigest("changed", 1)
	other = rebindProof(other)
	if _, e = s.BackupMetadata(t.Context(), other); e == nil {
		t.Fatal("changed authority replaced index")
	}
	if got, e := s.BackupMetadata(t.Context(), p); e != nil || got.Snapshot != i.Snapshot {
		t.Fatal("conflict destroyed original", e)
	}
}

func TestMetadataBackupActualChildDeath(t *testing.T) {
	if dir := os.Getenv("FASED_BACKUP_CHILD"); dir != "" {
		s, e := OpenStore(dir)
		if e != nil {
			os.Exit(80)
		}
		p := pausedBackup(t, s)
		s.backupFault = func(step string) error {
			if step == os.Getenv("FASED_BACKUP_STEP") {
				os.Exit(79)
			}
			return nil
		}
		s.BackupMetadata(t.Context(), p)
		os.Exit(81)
	}
	for _, step := range []string{"write:metadata.sqlite", "publish:metadata.sqlite", "publish:recovery.json"} {
		t.Run(step, func(t *testing.T) {
			s := fixtureStore(t)
			dir := s.dir
			s.Close()
			cmd := exec.Command(os.Args[0], "-test.run=^TestMetadataBackupActualChildDeath$")
			cmd.Env = append(os.Environ(), "FASED_BACKUP_CHILD="+dir, "FASED_BACKUP_STEP="+step)
			if e := cmd.Run(); e == nil || cmd.ProcessState.ExitCode() != 79 {
				t.Fatal("child did not die at boundary", e)
			}
			var e error
			s, e = OpenStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			r, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
			if e != nil {
				t.Fatal(e)
			}
			p := backupProof(t, s)
			p.IntentID = r.Request.IntentID
			p.Sequence = r.Request.Sequence
			p.ManifestSHA256 = r.Request.ManifestSHA256
			p.Pause = r
			p.Signer.ProbeID = r.Core.ProbeID
			p.Signer.ActiveIntentID = p.IntentID
			p.Signer.AuthoritySHA256 = p.ManifestSHA256
			p = rebindProof(p)
			if _, e = s.BackupMetadata(t.Context(), p); e != nil {
				t.Fatal("child recovery", e)
			}
		})
	}
}

func TestMetadataBackupPrivateTransportAndChangedEphemeralSource(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	p := pausedBackup(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(p)
	for _, tc := range []struct {
		private, auth, csrf bool
		want                int
	}{{false, true, true, 403}, {true, false, true, 401}, {true, true, false, 403}, {true, true, true, 200}} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/updates/backup", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("X-Fased-Private", "true")
		if tc.auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if tc.csrf {
			r.Header.Set("X-CSRF-Token", csrfToken(token))
		}
		var handler http.Handler = h
		if tc.private {
			handler = privateIPCHandler(h)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if bytes.Contains(w.Body.Bytes(), []byte("password_hash")) || bytes.Contains(w.Body.Bytes(), []byte(testPassword)) {
			t.Fatal("credential bytes exported")
		}
	}
	// A partial captured image is not silently replaced if a new login changed
	// ephemeral source bytes. Completed snapshots remain reusable after login.
	if _, e = s.Login(t.Context(), testPassword); e != nil {
		t.Fatal(e)
	}
	if _, e = s.BackupMetadata(t.Context(), p); e != nil {
		t.Fatal("completed image should survive ephemeral writes", e)
	}
	other := fixtureStore(t)
	claimFixture(t, other)
	q := pausedBackup(t, other)
	other.backupFault = func(step string) error {
		if step == "write:metadata.sqlite" {
			return syscall.ENOSPC
		}
		return nil
	}
	if _, e = other.BackupMetadata(t.Context(), q); !errors.Is(e, syscall.ENOSPC) {
		t.Fatal(e)
	}
	other.backupFault = nil
	if _, e = other.Login(t.Context(), testPassword); e != nil {
		t.Fatal(e)
	}
	if _, e = other.BackupMetadata(t.Context(), q); e == nil {
		t.Fatal("changed interrupted source replaced retained identity")
	}
}

func TestMetadataBackupRequiresPausedOwner(t *testing.T) {
	s := fixtureStore(t)
	if _, e := s.BackupMetadata(t.Context(), backupProof(t, s)); e == nil {
		t.Fatal("unpaused backup accepted")
	}
}

func TestRecoveryRevalidatesOriginalImageAcrossInstallBookkeeping(t *testing.T) {
	s := fixtureStore(t)
	claimFixture(t, s)
	p := pausedBackup(t, s)
	index, e := s.BackupMetadata(t.Context(), p)
	if e != nil {
		t.Fatal(e)
	}
	p.Signer.States = append([]update.State(nil), p.Signer.States...)
	for n := range p.Signer.States {
		if p.Signer.States[n].Name == "operations" {
			p.Signer.States[n].Anchor = update.SnapshotDigest("installation-journal", 1)
		}
	}
	p = rebindProof(p)
	got, e := s.VerifyRecovery(t.Context(), update.RecoveryCheck{Preflight: p, Index: index})
	if e != nil || got.Snapshot != index.Snapshot {
		t.Fatal("metadata image lost after installation bookkeeping", e)
	}
	bad := p
	bad.Signer.Spent++
	bad = rebindProof(bad)
	if _, e = s.VerifyRecovery(t.Context(), update.RecoveryCheck{Preflight: bad, Index: index}); e == nil {
		t.Fatal("changed financial totals accepted")
	}
	bad = p
	bad.Pause.Core.BootID = update.NewProbe()
	bad = rebindProof(bad)
	if _, e = s.VerifyRecovery(t.Context(), update.RecoveryCheck{Preflight: bad, Index: index}); e == nil {
		t.Fatal("stale core accepted")
	}
}
