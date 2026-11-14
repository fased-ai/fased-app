//go:build linux

package core

import (
	"context"
	"database/sql"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// openMaintenanceStore opens only an existing database under the original app
// UID and instance lock. It runs no migration, credential setup or task worker.
// SQLite may recover/checkpoint committed WAL; financial state is not restored.
func openMaintenanceStore(ctx context.Context, dir string) (*Store, io.Closer, error) {
	if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, nil, errors.New("ordinary fixed app state required")
	}
	for _, path := range []string{dir, filepath.Join(dir, "core.sqlite"), filepath.Join(dir, "instance.lock")} {
		i, err := os.Lstat(path)
		if err != nil {
			return nil, nil, err
		}
		if i.Mode()&os.ModeSymlink != 0 || path == dir && !i.IsDir() || path != dir && !i.Mode().IsRegular() {
			return nil, nil, errors.New("real existing maintenance state required")
		}
		if err = protectPath(path, i); err != nil {
			return nil, nil, err
		}
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		path := filepath.Join(dir, "core.sqlite") + suffix
		i, e := os.Lstat(path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, nil, e
		}
		if !i.Mode().IsRegular() || protectPath(path, i) != nil || suffix == "-journal" && i.Size() != 0 {
			return nil, nil, errors.New("foreign or pending rollback journal; preserve it")
		}
	}
	lock, err := LockInstance(filepath.Join(dir, "instance.lock"))
	if err != nil {
		return nil, nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.Join(dir, "core.sqlite"), RawQuery: "mode=rw&_pragma=busy_timeout(1000)&_pragma=synchronous(FULL)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		lock.Close()
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, dir: dir, bootID: update.NewProbe(), now: time.Now}
	snapshot, readErr := s.UpdateSnapshot(ctx, update.NewProbe())
	err = readErr
	if err != nil {
		db.Close()
		lock.Close()
		return nil, nil, err
	}
	s.instanceID = snapshot.InstanceID
	return s, lock, nil
}

type MetadataMaintenanceSession struct {
	s    *Store
	lock io.Closer
	a    update.MaintenanceApproval
}

func OpenMetadataMaintenance(ctx context.Context, dir string, owner int, id string) (*MetadataMaintenanceSession, error) {
	record, err := update.ReadMaintenanceRecord(owner)
	if err != nil {
		return nil, err
	}
	if record.OperationID != id || record.Start != "" || record.Package == nil {
		return nil, errors.New("cold metadata requires the protected stopped-service transaction")
	}
	a, err := update.ReadMaintenanceApproval(owner, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	approved := err == nil
	if !approved {
		a = update.MaintenanceApproval{Owner: owner, OperationID: id}
	}
	s, lock, err := openMaintenanceStore(ctx, dir)
	if err != nil {
		return nil, err
	}
	actual, err := s.UpdateSnapshot(ctx, update.NewProbe())
	if err != nil || approved && !update.SameCoreMetadata(actual, a.Core) {
		s.db.Close()
		lock.Close()
		return nil, errors.New("existing app metadata differs from maintenance approval")
	}
	return &MetadataMaintenanceSession{s: s, lock: lock, a: a}, nil
}

func (s *MetadataMaintenanceSession) Close() error {
	_, err := s.s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return errors.Join(err, s.s.db.Close(), s.lock.Close())
}

func InspectMaintenanceMetadata(ctx context.Context, dir, probe string) (update.CoreSnapshot, error) {
	s, lock, err := openMaintenanceStore(ctx, dir)
	if err != nil {
		return update.CoreSnapshot{}, err
	}
	defer lock.Close()
	defer s.db.Close()
	return s.UpdateSnapshot(ctx, probe)
}

func (s *MetadataMaintenanceSession) Request(ctx context.Context, q update.MaintenanceCommand) (update.MetadataMaintenanceReply, error) {
	var r update.MetadataMaintenanceReply
	if q.OperationID != s.a.OperationID || !update.IDOK(q.ProbeID) {
		return r, errors.New("one bound maintenance operation and probe required")
	}
	switch q.Action {
	case "inventory":
		v, err := s.s.UpdateSnapshot(ctx, q.ProbeID)
		r.Snapshot = &v
		return r, err
	case "initialize":
		record, err := update.ReadMaintenanceRecord(s.a.Owner)
		if err != nil {
			return r, err
		}
		if !record.Bootstrap || record.Start != "" || record.OperationID != q.OperationID || record.Package == nil {
			return r, errors.New("initial migration requires the recorded OS-owner request")
		}
		view, err := s.s.ApplicationUpdates(ctx)
		if err != nil {
			return r, err
		}
		if view.Operation.ID == q.OperationID {
			return r, nil
		}
		if oneOf(view.Operation.Phase, "queued", "applying", "recovery") {
			return r, errors.New("another reviewed application update is pending")
		}
		p := record.Package
		var engine string
		for _, c := range p.Components {
			if c.Contract.Component == "core" {
				engine = c.Artifact.SHA256
			}
		}
		op := ApplicationUpdateOperation{ID: q.OperationID, Phase: "applying", Release: &ApplicationRelease{EngineSHA256: engine, Version: p.Version, Sequence: p.Sequence, ManifestSHA256: p.ReleaseSHA256, Target: update.Target{OS: "linux", Arch: "amd64"}}, PolicySHA256: update.SnapshotDigest("application-update-policy", view.Policy), BootID: s.s.bootID}
		return r, s.s.publishApplicationUpdate(ctx, op, view.Revision)
	case "pause":
		approval, err := update.ReadMaintenanceApproval(s.a.Owner, q.OperationID)
		if err != nil {
			return r, err
		}
		s.a = approval
		record, err := update.ReadMaintenanceRecord(s.a.Owner)
		if err != nil {
			return r, err
		}
		if record.OperationID != s.a.OperationID || record.Start != "" || record.Staged == nil || record.Staged.State != "staged" || record.Staged.ID != s.a.OperationID || record.Staged.ManifestSHA256 != s.a.Digest() || record.Staged.StageSHA256 != update.StageDigest(*record.Staged) || !update.SameCoreMetadata(record.Staged.Core, s.a.Core) {
			return r, errors.New("actual staged signer receipt and cold fence required")
		}
		want := update.PauseFor(*record.Staged)
		v, err := s.s.updatePause(ctx, update.CorePauseCommand{Action: "pause", ProbeID: q.ProbeID, Request: &want}, true)
		r.Pause = &v
		return r, err
	case "status":
		v, err := s.s.UpdatePause(ctx, update.CorePauseCommand{Action: "status", ProbeID: q.ProbeID})
		r.Pause = &v
		return r, err
	case "backup":
		if q.Preflight == nil {
			return r, errors.New("actual signer preflight required")
		}
		v, err := s.s.BackupMetadata(ctx, *q.Preflight)
		r.Recovery = &v
		return r, err
	case "verify":
		if q.Preflight == nil || q.Binding == nil {
			return r, errors.New("original bound backup required")
		}
		v, err := s.s.VerifyRecovery(ctx, update.RecoveryCheck{Preflight: *q.Preflight, Index: q.Binding.Index})
		r.Recovery = &v
		return r, err
	default:
		return r, errors.New("unsupported metadata maintenance action")
	}
}
