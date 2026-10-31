//go:build linux

package core

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

func applicationReaderFixture(t *testing.T) (string, ApplicationReaderBootstrapRequest, string) {
	t.Helper()
	s := fixtureStore(t)
	var original string
	if e := s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&original); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec("DROP TABLE application_updates; PRAGMA user_version=33;"); e != nil {
		t.Fatal(e)
	}
	legacy, e := applicationReaderLegacySnapshot(t.Context(), s.db, update.NewProbe(), s.bootID)
	if e != nil {
		t.Fatal(e)
	}
	release := fixtureUpdateController().release
	id := update.NewProbe()
	pause := update.CorePauseRequest{Version: 1, IntentID: id, Sequence: release.Sequence, ManifestSHA256: release.ManifestSHA256, Expected: legacy}
	held := pauseRecord{PauseID: update.NewProbe(), Request: pause}
	raw, _ := json.Marshal(held)
	if _, e = s.db.Exec("UPDATE update_pause SET phase='paused',document=? WHERE id=1", string(raw)); e != nil {
		t.Fatal(e)
	}
	r := ApplicationReaderBootstrapRequest{Version: 1, OperationID: id, OwnerLease: update.NewProbe(), PreviousEngineSHA256: strings.Repeat("b", 64), Release: release, Predecessor: update.CorePauseReceipt{Version: 1, State: "paused", PauseID: held.PauseID, Request: &pause, Core: legacy}}
	dir := s.dir
	s.Close()
	lock, e := LockInstance(filepath.Join(dir, "instance.lock"))
	if e != nil {
		t.Fatal(e)
	}
	lock.Close()
	if e = os.WriteFile(filepath.Join(dir, "retained-credential"), []byte("disposable ciphertext only"), 0600); e != nil {
		t.Fatal(e)
	}
	return dir, r, original
}

func readerSchema(t *testing.T, dir string) int {
	t.Helper()
	db, e := sql.Open("sqlite", "file:"+filepath.Join(dir, "core.sqlite")+"?mode=ro")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var schema int
	if e = db.QueryRow("PRAGMA user_version").Scan(&schema); e != nil {
		t.Fatal(e)
	}
	return schema
}

func TestApplicationReaderBootstrapPreservesRawRecordsAndCompletes(t *testing.T) {
	dir, r, original := applicationReaderFixture(t)
	got, e := BootstrapApplicationReader(t.Context(), dir, r)
	if e != nil || got.Phase != "reader-migrated-forward-only" || got.Checkpoint.PreviousEngineSHA256 != r.PreviousEngineSHA256 || got.Checkpoint.Pause.Core.State.Version != StateSchema || readerSchema(t, dir) != StateSchema {
		t.Fatal(got, e)
	}
	again, e := ResumeApplicationReaderBootstrap(t.Context(), dir, r.OperationID)
	if e != nil || again.RequestSHA256 != got.RequestSHA256 || again.Original != got.Original || again.Checkpoint.Snapshot != got.Checkpoint.Snapshot {
		t.Fatal("same transaction did not resume", again, e)
	}
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var raw string
	if e = s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&raw); e != nil || raw != original {
		t.Fatal("raw agent records changed", e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "retained-credential"))
	if e != nil || string(b) != "disposable ciphertext only" {
		t.Fatal("ciphertext changed", e)
	}
	proof := ApplicationUpdateReceipt{ID: r.OperationID, Sequence: r.Release.Sequence, ManifestSHA256: r.Release.ManifestSHA256, EngineSHA256: r.Release.EngineSHA256, Phase: "completed"}
	if _, e = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "finish", ID: r.OperationID, Lease: r.OwnerLease, Receipt: &proof}); e != nil {
		t.Fatal("normal owner completion failed", e)
	}
	view, e := s.ApplicationUpdates(t.Context())
	if e != nil || view.Operation.Phase != "completed" || view.ActivationEnabled {
		t.Fatal(view, e)
	}
	if e = CheckApplicationRollbackMetadata(t.Context(), dir, r.OperationID, 33); e == nil {
		t.Fatal("old33 reader allowed to roll back migrated metadata")
	}
}

func TestApplicationReaderBootstrapInterruptionKeepsOriginalAndResumes(t *testing.T) {
	for _, point := range []string{"reader:before-commit", "reader:after-commit"} {
		t.Run(point, func(t *testing.T) {
			dir, r, _ := applicationReaderFixture(t)
			_, e := bootstrapApplicationReader(t.Context(), dir, r, func(p string) error {
				if p == point {
					return errors.New("disposable interrupted handoff")
				}
				return nil
			})
			if e == nil {
				t.Fatal("fault not reached")
			}
			want := 33
			if point == "reader:after-commit" {
				want = StateSchema
			}
			if got := readerSchema(t, dir); got != want {
				t.Fatal("unexpected cutover", got)
			}
			if _, e = ResumeApplicationReaderBootstrap(t.Context(), dir, r.OperationID); e != nil {
				t.Fatal("exact interrupted transaction cannot resume", e)
			}
			if readerSchema(t, dir) != StateSchema {
				t.Fatal("resume did not finish")
			}
		})
	}
}

func TestApplicationReaderBootstrapRefusesWrongReaderAuthorityAndActiveEngine(t *testing.T) {
	for _, kind := range []string{"schema34", "missing-lock", "active-lock", "source-sha", "lease", "fence", "records", "unprotected-table", "missing-resume"} {
		t.Run(kind, func(t *testing.T) {
			dir, r, _ := applicationReaderFixture(t)
			var lock *InstanceLock
			switch kind {
			case "schema34", "unprotected-table":
				db, e := sql.Open("sqlite", filepath.Join(dir, "core.sqlite"))
				if e != nil {
					t.Fatal(e)
				}
				q := "PRAGMA user_version=34"
				if kind == "unprotected-table" {
					q = "CREATE TABLE application_updates(id INTEGER PRIMARY KEY, document TEXT)"
				}
				if _, e = db.Exec(q); e != nil {
					t.Fatal(e)
				}
				db.Close()
			case "missing-lock":
				os.Remove(filepath.Join(dir, "instance.lock"))
			case "active-lock":
				var e error
				lock, e = LockInstance(filepath.Join(dir, "instance.lock"))
				if e != nil {
					t.Fatal(e)
				}
				defer lock.Close()
			case "source-sha":
				r.PreviousEngineSHA256 = "invalid"
			case "lease":
				r.OwnerLease = "invalid"
			case "fence":
				r.Predecessor.PauseID = update.NewProbe()
			case "records":
				r.Predecessor.Core.State.Anchor = strings.Repeat("f", 64)
				r.Predecessor.Request.Expected = r.Predecessor.Core
			case "missing-resume":
				if _, e := ResumeApplicationReaderBootstrap(t.Context(), dir, r.OperationID); e == nil {
					t.Fatal("missing transaction resumed")
				}
				return
			}
			if _, e := BootstrapApplicationReader(t.Context(), dir, r); e == nil {
				t.Fatal("unsafe reader handoff admitted")
			}
			want := 33
			if kind == "schema34" {
				want = 34
			}
			if got := readerSchema(t, dir); got != want {
				t.Fatal("refusal changed schema", got)
			}
		})
	}
}

func TestApplicationReaderBootstrapBindsRetrySourceAndBackup(t *testing.T) {
	for _, kind := range []string{"source", "lease", "release", "backup"} {
		t.Run(kind, func(t *testing.T) {
			dir, r, _ := applicationReaderFixture(t)
			_, e := bootstrapApplicationReader(t.Context(), dir, r, func(p string) error {
				if p == "reader:after-commit" {
					return errors.New("cut")
				}
				return nil
			})
			if e == nil {
				t.Fatal("fault absent")
			}
			switch kind {
			case "source":
				r.PreviousEngineSHA256 = strings.Repeat("c", 64)
			case "lease":
				r.OwnerLease = update.NewProbe()
			case "release":
				r.Release.EngineSHA256 = strings.Repeat("d", 64)
			case "backup":
				d, e := recoveryDirectoryMode(dir, r.OperationID, false)
				if e != nil {
					t.Fatal(e)
				}
				index, e := readApplicationReaderBootstrapIndex(d, r.OperationID)
				if e != nil {
					t.Fatal(e)
				}
				// Resolve through the already-validated recovery directory, not a guessed root.
				path := fmtReaderRecoveryPath(d, index.Original.File)
				if e = os.WriteFile(path, []byte("altered disposable snapshot"), 0600); e != nil {
					t.Fatal(e)
				}
				unix.Close(d)
			}
			if _, e = BootstrapApplicationReader(t.Context(), dir, r); e == nil {
				t.Fatal("changed retry admitted")
			}
		})
	}
}

func TestApplicationReaderBootstrapStagesLeaseBeforeStopping(t *testing.T) {
	dir, r, _ := applicationReaderFixture(t)
	lock, e := LockInstance(filepath.Join(dir, "instance.lock"))
	if e != nil {
		t.Fatal(e)
	}
	if e = StageApplicationReaderBootstrap(t.Context(), dir, r); e != nil {
		t.Fatal("could not persist the existing paused-source lease", e)
	}
	got, e := ReadApplicationReaderBootstrapRequest(dir, r.OperationID)
	if e != nil || got.OwnerLease != r.OwnerLease || got.PreviousEngineSHA256 != r.PreviousEngineSHA256 {
		t.Fatal(got, e)
	}
	changed := r
	changed.OwnerLease = update.NewProbe()
	if e = StageApplicationReaderBootstrap(t.Context(), dir, changed); e == nil {
		t.Fatal("staged lease changed")
	}
	if _, e = ResumeApplicationReaderBootstrap(t.Context(), dir, r.OperationID); e == nil {
		t.Fatal("resumed while original engine still owned its instance lock")
	}
	lock.Close()
	if _, e = ResumeApplicationReaderBootstrap(t.Context(), dir, r.OperationID); e != nil {
		t.Fatal("staged original could not resume", e)
	}
}

func fmtReaderRecoveryPath(d int, name string) string {
	return fmt.Sprintf("/proc/self/fd/%d/%s", d, name)
}
