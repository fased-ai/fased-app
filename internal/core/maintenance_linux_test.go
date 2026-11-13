//go:build linux

package core

import (
	"database/sql"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMaintenanceColdStoreRecoversCommittedWALWithoutMigration(t *testing.T) {
	s := fixtureStore(t)
	p := pausedBackup(t, s)
	index, err := s.BackupMetadata(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	before, err := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if err != nil {
		t.Fatal(err)
	}
	lock, err := LockInstance(filepath.Join(dir, "instance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	s.Close()
	c := exec.Command(os.Args[0], "-test.run=^TestMaintenanceWALCrashWriter$")
	c.Env = append(os.Environ(), "FASED_MAINTENANCE_WAL_TEST="+dir)
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("WAL producer: %v %s", e, b)
	}
	if i, e := os.Stat(filepath.Join(dir, "core.sqlite-wal")); e != nil || i.Size() == 0 {
		t.Fatal("missing interrupted committed WAL", e)
	}
	if e := VerifyFixtureColdRecovery(t.Context(), dir, index); e == nil {
		t.Fatal("cold immutable verifier silently ignored a pending WAL")
	}
	reopened, held, err := openMaintenanceStore(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, e := openMaintenanceStore(t.Context(), dir); e == nil {
		t.Fatal("maintenance ignored the actual instance lock")
	}
	after, err := reopened.UpdateSnapshot(t.Context(), update.NewProbe())
	if err != nil || !update.SameCoreMetadata(before, after) {
		t.Fatal("committed metadata changed", err)
	}
	session := MetadataMaintenanceSession{s: reopened, lock: held}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	if err = VerifyFixtureColdRecovery(t.Context(), dir, index); err != nil {
		t.Fatal("original backup failed after real cold WAL recovery", err)
	}
	if err = os.Symlink(filepath.Join(dir, "core.sqlite"), filepath.Join(dir, "core.sqlite-wal")); err != nil {
		t.Fatal(err)
	}
	if _, _, e := openMaintenanceStore(t.Context(), dir); e == nil {
		t.Fatal("foreign WAL link accepted")
	}
}

func TestMaintenanceWALCrashWriter(t *testing.T) {
	dir := os.Getenv("FASED_MAINTENANCE_WAL_TEST")
	if dir == "" {
		t.Skip("interruption child only")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "core.sqlite")+"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE update_pause SET document=document||' ' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	os.Exit(0) // committed WAL survives; no driver cleanup/checkpoint.
}
