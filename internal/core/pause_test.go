package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestPauseSchemaHasDurableWriteFence(t *testing.T) {
	s := fixtureStore(t)
	var count int
	if e := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'update_pause_%'").Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count < 2 {
		t.Fatal("durable metadata pause guards missing")
	}
	var synchronous int
	if e := s.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); e != nil || synchronous != 2 {
		t.Fatal("pause commits require FULL synchronous WAL", synchronous, e)
	}
}

func pauseCommand(t *testing.T, s *Store, sequence uint64) update.CorePauseCommand {
	t.Helper()
	p := update.NewProbe()
	snapshot, e := s.UpdateSnapshot(t.Context(), p)
	if e != nil {
		t.Fatal(e)
	}
	return update.CorePauseCommand{Action: "pause", ProbeID: p, Request: &update.CorePauseRequest{Version: 1, IntentID: update.NewProbe(), Sequence: sequence, ManifestSHA256: update.SnapshotDigest("manifest", sequence), Expected: snapshot}}
}
func aborted(c update.CorePauseCommand) *update.Intent {
	r := c.Request
	return &update.Intent{ID: r.IntentID, Sequence: r.Sequence, ManifestSHA256: r.ManifestSHA256, Core: r.Expected, State: "aborted"}
}
func TestCorePauseFencesMetadataAcrossRestartAndRecovery(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	c := pauseCommand(t, s, 1)
	a, e := s.UpdatePause(t.Context(), c)
	if e != nil || a.State != "paused" {
		t.Fatal(a, e)
	}
	setup, e := s.Setup(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateSetup(t.Context(), setup.Revision, setup.Settings); !errors.Is(e, ErrCorePaused) {
		t.Fatal("setup wrote through pause", e)
	}
	accounts, e := s.Accounts(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateAccounts(t.Context(), accounts.Record); !errors.Is(e, ErrCorePaused) {
		t.Fatal("accounts wrote through pause", e)
	}
	if _, e = s.Login(t.Context(), testPassword); e != nil {
		t.Fatal("pause locked out owner login", e)
	}
	if !s.Session(t.Context(), token) {
		t.Fatal("pause revoked owner")
	}
	// SQL-level protection includes a second independent connection.
	other, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = other.db.Exec("UPDATE instance SET password_hash=x'010203'"); e == nil {
		t.Fatal("credentials changed through another connection")
	}
	other.Close()
	dir := s.dir
	s.Close()
	if e = RecoverPassword(t.Context(), dir, testPassword+" replacement"); !errors.Is(e, ErrCorePaused) {
		t.Fatal("owner recovery bypassed pause", e)
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	b, e := s.UpdatePause(t.Context(), c)
	if e != nil || b.PauseID != a.PauseID || b.Core.BootID == a.Core.BootID || !update.SameCoreMetadata(a.Core, b.Core) {
		t.Fatal("restart lost pause", b, e)
	}
	if _, e = s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "release", ProbeID: c.ProbeID, Aborted: aborted(pauseCommand(t, s, 2))}); e == nil {
		t.Fatal("different abort released pause")
	}
	release := update.CorePauseCommand{Action: "release", ProbeID: c.ProbeID, Aborted: aborted(c)}
	for n := 0; n < 2; n++ {
		b, e = s.UpdatePause(t.Context(), release)
		if e != nil || b.State != "released" {
			t.Fatal("release not recoverable", e)
		}
	}
	if _, e = s.UpdatePause(t.Context(), c); e == nil {
		t.Fatal("released sequence replayed")
	}
	if _, e = s.UpdateSetup(t.Context(), setup.Revision, setup.Settings); e != nil {
		t.Fatal("metadata stayed held after release", e)
	}
	if !s.Session(t.Context(), token) {
		t.Fatal("identity/session lost")
	}
}
func TestCorePauseConcurrentAndStaleRequests(t *testing.T) {
	s := fixtureStore(t)
	_ = claimFixture(t, s)
	base := pauseCommand(t, s, 1)
	stale := base
	copy := *base.Request
	copy.Expected.BootID = update.NewProbe()
	stale.Request = &copy
	if _, e := s.UpdatePause(t.Context(), stale); e == nil {
		t.Fatal("stale boot accepted")
	}
	var wg sync.WaitGroup
	wins := make(chan update.CorePauseCommand, 8)
	for n := 0; n < 8; n++ {
		c := base
		r := *base.Request
		r.IntentID = update.NewProbe()
		c.Request = &r
		wg.Go(func() {
			if _, e := s.UpdatePause(t.Context(), c); e == nil {
				wins <- c
			}
		})
	}
	wg.Wait()
	close(wins)
	count := 0
	var winner update.CorePauseCommand
	for c := range wins {
		count++
		winner = c
	}
	if count != 1 {
		t.Fatal("pause winners", count)
	}
	if _, e := s.UpdatePause(t.Context(), winner); e != nil {
		t.Fatal("same pause retry failed", e)
	}
	// A metadata writer racing pause either commits before the bound read (and
	// invalidates it), or is rejected by the SQL fence; it never writes afterward.
	if _, e := s.db.Exec("UPDATE accounts SET revision=revision+1"); e == nil {
		t.Fatal("direct SQL bypass")
	}
}
func TestCorePauseRequiresPrivateAuthenticatedCSRFTransport(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(pauseCommand(t, s, 1))
	for _, tc := range []struct {
		private, auth, csrf bool
		want                int
	}{{false, true, true, 403}, {true, false, true, 401}, {true, true, false, 403}, {true, true, true, 200}} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/updates/pause", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("X-Fased-Private", "true") // Cannot forge the IPC context.
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
	}
}
func TestCorePauseCommitFailureRecovery(t *testing.T) {
	for _, phase := range []string{"pause:before-commit", "pause:durable"} {
		t.Run(phase, func(t *testing.T) {
			s := fixtureStore(t)
			_ = claimFixture(t, s)
			c := pauseCommand(t, s, 1)
			s.pauseFault = func(p string) error {
				if p == phase {
					return syscall.ENOSPC
				}
				return nil
			}
			if _, e := s.UpdatePause(t.Context(), c); e == nil {
				t.Fatal("fault not injected")
			}
			s.pauseFault = nil
			s.Close()
			s, e := OpenStore(s.dir)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			p, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: c.ProbeID})
			if e != nil {
				t.Fatal(e)
			}
			want := "idle"
			if phase == "pause:durable" {
				want = "paused"
			}
			if p.State != want {
				t.Fatal("uncertain commit recovery", p.State)
			}
			if phase == "pause:durable" {
				if _, e = s.UpdatePause(t.Context(), c); e != nil {
					t.Fatal("lost reply not recoverable", e)
				}
			}
		})
	}
}
func TestCorePauseActualProcessTermination(t *testing.T) {
	if dir := os.Getenv("FASED_PAUSE_CHILD"); dir != "" {
		s, e := OpenStore(dir)
		if e != nil {
			t.Fatal(e)
		}
		c := pauseCommand(t, s, 1)
		s.pauseFault = func(p string) error {
			if p == "pause:durable" {
				os.Exit(79)
			}
			return nil
		}
		_, e = s.UpdatePause(t.Context(), c)
		t.Fatal("child did not terminate", e)
	}
	s := fixtureStore(t)
	_ = claimFixture(t, s)
	dir := s.dir
	s.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestCorePauseActualProcessTermination$")
	child.Env = append(os.Environ(), "FASED_PAUSE_CHILD="+dir)
	if b, e := child.CombinedOutput(); e == nil || child.ProcessState.ExitCode() != 79 {
		t.Fatalf("exit proof %s %v", b, e)
	}
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
	if e != nil || p.State != "paused" {
		t.Fatal("process death dropped fence", e)
	}
	if _, e = s.UpdateSetup(t.Context(), 0, DefaultSetup()); !errors.Is(e, ErrCorePaused) {
		t.Fatal("write after death", e)
	}
}

func TestCorePauseSchemaTwoMigrationPreservesOwnerMetadata(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	dir := s.dir
	before, e := s.Accounts(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`DROP TRIGGER update_pause_instance; DROP TRIGGER update_pause_accounts; DROP TABLE update_pause; PRAGMA user_version=2;`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	after, e := s.Accounts(t.Context())
	if e != nil || !s.Session(t.Context(), token) || update.SnapshotDigest("accounts", before.Record) != update.SnapshotDigest("accounts", after.Record) {
		t.Fatal("schema-2 owner/account data changed", e)
	}
	p, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
	if e != nil || p.State != "idle" || p.Core.State.Version != 3 {
		t.Fatal("schema-2 pause initialization", e)
	}
}

func TestCorePauseLostReleaseReplyKeepsTombstone(t *testing.T) {
	s := fixtureStore(t)
	_ = claimFixture(t, s)
	c := pauseCommand(t, s, 1)
	if _, e := s.UpdatePause(t.Context(), c); e != nil {
		t.Fatal(e)
	}
	s.pauseFault = func(p string) error {
		if p == "pause:durable" {
			return syscall.ENOSPC
		}
		return nil
	}
	release := update.CorePauseCommand{Action: "release", ProbeID: c.ProbeID, Aborted: aborted(c)}
	if _, e := s.UpdatePause(t.Context(), release); e == nil {
		t.Fatal("release reply loss not injected")
	}
	s.pauseFault = nil
	s.Close()
	s, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e := s.UpdatePause(t.Context(), release)
	if e != nil || p.State != "released" {
		t.Fatal("release retry failed", e)
	}
	if _, e = s.UpdatePause(t.Context(), c); e == nil {
		t.Fatal("lost release reply allowed replay")
	}
}

func TestCorePauseConcurrentAbortCannotLeaveLatePause(t *testing.T) {
	for n := 0; n < 8; n++ {
		t.Run(string(rune('a'+n)), func(t *testing.T) {
			s := fixtureStore(t)
			_ = claimFixture(t, s)
			c := pauseCommand(t, s, 1)
			release := update.CorePauseCommand{Action: "release", ProbeID: c.ProbeID, Aborted: aborted(c)}
			var wg sync.WaitGroup
			wg.Go(func() { _, _ = s.UpdatePause(t.Context(), c) })
			wg.Go(func() {
				if _, e := s.UpdatePause(t.Context(), release); e != nil {
					t.Error(e)
				}
			})
			wg.Wait()
			p, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: c.ProbeID})
			if e != nil || p.State != "released" {
				t.Fatal("late pause survived abort", p, e)
			}
			if _, e = s.UpdatePause(t.Context(), c); e == nil {
				t.Fatal("abort tombstone replayed")
			}
		})
	}
}
