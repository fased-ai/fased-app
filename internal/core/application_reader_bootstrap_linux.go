//go:build linux

package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

type applicationReaderBootstrapIndex struct {
	Version       int                               `json:"version"`
	Request       ApplicationReaderBootstrapRequest `json:"request"`
	RequestSHA256 string                            `json:"requestSha256"`
	Original      update.MetadataSnapshot           `json:"original"`
}

// BootstrapApplicationReader runs only after the owner controller stopped the
// original engine. The actual instance flock, private original DB and exact
// predecessor fence are checked again before any mutation. Crossing an admitted predecessor to the current schema is
// forward-only: the old reader must never be restarted against the new schema.
func BootstrapApplicationReader(ctx context.Context, directory string, request ApplicationReaderBootstrapRequest) (ApplicationReaderBootstrapReceipt, error) {
	return bootstrapApplicationReader(ctx, directory, request, nil)
}

// Resume uses the same durable transaction, not a new source, lease or release.
func ResumeApplicationReaderBootstrap(ctx context.Context, directory, id string) (ApplicationReaderBootstrapReceipt, error) {
	if !update.IDOK(id) {
		return ApplicationReaderBootstrapReceipt{}, errors.New("exact existing reader-bootstrap required")
	}
	request, e := ReadApplicationReaderBootstrapRequest(directory, id)
	if e != nil {
		return ApplicationReaderBootstrapReceipt{}, e
	}
	return bootstrapApplicationReader(ctx, directory, request, nil)
}

// Stage persists the independently bound lease before the old process is
// stopped. It creates no new release or lease; its source pause must be exact.
func StageApplicationReaderBootstrap(ctx context.Context, directory string, request ApplicationReaderBootstrapRequest) error {
	if !request.valid() {
		return errors.New("exact reader-bootstrap request required")
	}
	if e := checkApplicationReaderPredecessor(ctx, directory, request.Predecessor.Core, &request.Predecessor); e != nil {
		return e
	}
	if request.Version == 2 {
		prepared, err := readReaderPreparation(directory, request.OperationID)
		if err != nil {
			return err
		}
		if readerIntentDigest(prepared.Request) != readerIntentDigest(request) {
			return errors.New("original prepared reader authority changed")
		}
	}
	d, e := recoveryDirectory(directory, request.OperationID)
	if e != nil {
		return e
	}
	defer unix.Close(d)
	s := &Store{dir: directory}
	raw, _ := json.Marshal(request)
	if b, e := recoveryRead(d, "reader-request.json", 16384); e == nil {
		if string(b) != string(raw) {
			return errors.New("reader-bootstrap request changed")
		}
		return nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	name := "reader-request-" + metadataHash(raw) + ".json"
	if e = s.publishRecovery(d, name, raw); e != nil {
		return e
	}
	if e = unix.Renameat2(d, name, d, "reader-request.json", unix.RENAME_NOREPLACE); e != nil {
		return e
	}
	return unix.Fsync(d)
}

func ReadApplicationReaderBootstrapRequest(directory, id string) (ApplicationReaderBootstrapRequest, error) {
	var request ApplicationReaderBootstrapRequest
	if !update.IDOK(id) {
		return request, errors.New("exact existing reader-bootstrap required")
	}
	d, e := recoveryDirectoryMode(directory, id, false)
	if e != nil {
		return request, e
	}
	defer unix.Close(d)
	b, e := recoveryRead(d, "reader-request.json", 16384)
	if errors.Is(e, os.ErrNotExist) {
		index, ie := readApplicationReaderBootstrapIndex(d, id)
		if errors.Is(ie, os.ErrNotExist) {
			preparation, pe := readReaderPreparation(directory, id)
			return preparation.Request, pe
		}
		return index.Request, ie
	}
	if e != nil {
		return request, e
	}
	if update.StrictJSON(b, &request) != nil || !request.valid() || request.OperationID != id {
		return request, errors.New("reader-bootstrap request is foreign or damaged")
	}
	return request, nil
}

// This read-only preflight is safe while the original engine is running. Its
// result is matched to the actual old IPC snapshot, then old UpdatePause repeats
// the same snapshot check before fencing writers. It never migrates or signs in.
func CheckApplicationReaderPredecessor(ctx context.Context, directory string, expected update.CoreSnapshot) error {
	return checkApplicationReaderPredecessor(ctx, directory, expected, nil)
}

func checkApplicationReaderPredecessor(ctx context.Context, directory string, expected update.CoreSnapshot, pause *update.CorePauseReceipt) error {
	if (expected.State.Version != 33 && expected.State.Version != 35) || !update.IDOK(expected.ProbeID) || !update.IDOK(expected.BootID) {
		return errors.New("admitted predecessor snapshot required")
	}
	d, e := openApplicationMetadataDirectory(directory)
	if e != nil {
		return e
	}
	defer d.Close()
	var sourceDirectoryStat unix.Stat_t
	if unix.Fstat(int(d.Fd()), &sourceDirectoryStat) != nil || int(sourceDirectoryStat.Uid) != os.Geteuid() || sourceDirectoryStat.Mode&0077 != 0 {
		return errors.New("private source metadata owner required")
	}
	db, e := sql.Open("sqlite", fmt.Sprintf("file:/proc/self/fd/%d/core.sqlite?mode=ro", d.Fd()))
	if e != nil {
		return e
	}
	defer db.Close()
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var schema int
	if e = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema); e != nil || schema != expected.State.Version {
		return errors.New("installed source schema changed")
	}
	if e = applicationReaderAppOnly(ctx, tx); e != nil {
		return e
	}
	if pause != nil {
		phase, p, e := readPause(ctx, tx)
		if e != nil || phase != "paused" || p.PauseID != pause.PauseID || update.SnapshotDigest("pause-request", p.Request) != update.SnapshotDigest("pause-request", pause.Request) {
			return errors.New("exact original source pause required before staging")
		}
	}
	if schema == 35 {
		if pause == nil {
			phase, p, err := readPause(ctx, tx)
			if err != nil {
				return err
			}
			if phase == "paused" {
				return ErrCorePaused
			}
			op, _, err := reader35Operation(ctx, tx)
			if err != nil {
				return err
			}
			if err = reader35Quiescent(ctx, tx, directory, op, phase, p); err != nil {
				return err
			}
		} else {
			preparation, err := readReaderPreparation(directory, pause.Request.IntentID)
			if err != nil {
				return err
			}
			final := preparation.Request
			final.Predecessor = *pause
			if err = verifyReader35Preparation(ctx, tx, directory, final); err != nil {
				return err
			}
		}
	}
	got, e := applicationReaderSourceSnapshot(ctx, tx, expected.ProbeID, expected.BootID, expected.State.Version)
	if e != nil || !update.SameCoreMetadata(got, expected) {
		return errors.New("source records differ from original IPC snapshot")
	}
	return nil
}

func readApplicationReaderBootstrapIndex(d int, id string) (applicationReaderBootstrapIndex, error) {
	var index applicationReaderBootstrapIndex
	b, e := recoveryRead(d, "reader-bootstrap.json", 16384)
	if e != nil {
		return index, e
	}
	if update.StrictJSON(b, &index) != nil || index.Version != index.Request.Version || !index.Request.valid() || index.Request.OperationID != id || index.RequestSHA256 != update.SnapshotDigest("application-reader-bootstrap", index.Request) || index.Original.File != fmt.Sprintf("reader-%d-%s.sqlite", index.Request.Predecessor.Core.State.Version, index.Original.SHA256) || index.Original.Size <= 0 || index.Original.Size > update.MaxMetadataBackup || !hexValue(index.Original.SHA256, 32) {
		return index, errors.New("unknown reader-bootstrap index; preserve originals")
	}
	return index, nil
}

func bootstrapApplicationReader(ctx context.Context, directory string, request ApplicationReaderBootstrapRequest, fault func(string) error) (ApplicationReaderBootstrapReceipt, error) {
	var out ApplicationReaderBootstrapReceipt
	if !request.valid() || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		return out, errors.New("ordinary metadata owner and exact authenticated reader-bootstrap required")
	}
	dir, e := openApplicationMetadataDirectory(directory)
	if e != nil {
		return out, e
	}
	defer dir.Close()
	info, e := dir.Stat()
	var directoryStat unix.Stat_t
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || unix.Fstat(int(dir.Fd()), &directoryStat) != nil || int(directoryStat.Uid) != os.Geteuid() {
		return out, errors.New("private original metadata directory required")
	}
	lock, e := unix.Openat(int(dir.Fd()), "instance.lock", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return out, errors.New("existing original instance lock required")
	}
	defer unix.Close(lock)
	var st unix.Stat_t
	if unix.Fstat(lock, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != os.Geteuid() || st.Mode&0077 != 0 || st.Nlink != 1 {
		return out, errors.New("private original instance lock required")
	}
	if e = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return out, errors.New("original engine is still running; reader-bootstrap refused")
	}
	defer unix.Flock(lock, unix.LOCK_UN)
	db, e := sql.Open("sqlite", fmt.Sprintf("file:/proc/self/fd/%d/core.sqlite?mode=rw", dir.Fd()))
	if e != nil {
		return out, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, e = db.ExecContext(ctx, "PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL;"); e != nil {
		return out, e
	}
	c, e := db.Conn(ctx)
	if e != nil {
		return out, e
	}
	defer c.Close()
	if _, e = c.ExecContext(ctx, "BEGIN IMMEDIATE"); e != nil {
		return out, e
	}
	defer c.ExecContext(context.Background(), "ROLLBACK")
	var schema int
	if e = c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema); e != nil {
		return out, e
	}
	source := request.Predecessor.Core.State.Version
	if schema != source && schema != StateSchema || StateSchema != 36 {
		return out, errors.New("unsupported installed reader; preserve original installation")
	}
	phase, held, e := readPause(ctx, c)
	if e != nil || phase != "paused" || held.PauseID != request.Predecessor.PauseID || held.Request.IntentID != request.OperationID || held.Request.Sequence != request.Release.Sequence || held.Request.ManifestSHA256 != request.Release.ManifestSHA256 {
		return out, errors.New("exact original reader-bootstrap metadata fence required")
	}
	if e = applicationReaderAppOnly(ctx, c); e != nil {
		return out, e
	}
	legacy, e := applicationReaderSourceSnapshot(ctx, c, request.Predecessor.Core.ProbeID, request.Predecessor.Core.BootID, source)
	if e != nil || !update.SameCoreMetadata(legacy, request.Predecessor.Core) {
		return out, errors.New("predecessor records changed before reader handoff")
	}
	if schema == source && update.SnapshotDigest("pause-request", held.Request) != update.SnapshotDigest("pause-request", *request.Predecessor.Request) {
		return out, errors.New("predecessor pause receipt changed")
	}
	if schema == 35 {
		if e = verifyReader35Preparation(ctx, c, directory, request); e != nil {
			return out, e
		}
	}
	if schema == 33 {
		var tables int
		if e = c.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='application_updates'").Scan(&tables); e != nil || tables != 0 {
			return out, errors.New("unprotected updater authority in predecessor; preserve state")
		}
	}
	d, e := recoveryDirectory(directory, request.OperationID)
	if e != nil {
		return out, e
	}
	defer unix.Close(d)
	backupLock, e := recoveryFile(d, "backup.lock", true)
	if e != nil {
		return out, e
	}
	defer backupLock.Close()
	if e = unix.Flock(int(backupLock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return out, errors.New("reader-bootstrap backup is already active")
	}
	defer unix.Flock(int(backupLock.Fd()), unix.LOCK_UN)
	s := &Store{db: db, dir: directory, bootID: request.Predecessor.Core.BootID, now: time.Now}
	index, e := readApplicationReaderBootstrapIndex(d, request.OperationID)
	if errors.Is(e, os.ErrNotExist) && schema == source {
		image, err := serializeMetadataConnection(ctx, c)
		if err != nil {
			return out, err
		}
		index = applicationReaderBootstrapIndex{Version: request.Version, Request: request, RequestSHA256: update.SnapshotDigest("application-reader-bootstrap", request), Original: update.MetadataSnapshot{File: fmt.Sprintf("reader-%d-%s.sqlite", source, metadataHash(image)), Size: int64(len(image)), SHA256: metadataHash(image)}}
		if e = s.publishRecovery(d, index.Original.File, image); e != nil {
			return out, e
		}
		if e = publishApplicationReaderIndex(s, d, index); e != nil {
			return out, e
		}
	} else if e != nil {
		return out, e
	}
	if index.RequestSHA256 != update.SnapshotDigest("application-reader-bootstrap", request) {
		return out, errors.New("reader-bootstrap release, lease, source or fence changed")
	}
	if e = verifyApplicationReaderOriginal(ctx, d, index, directory); e != nil {
		return out, e
	}
	if schema == source {
		o := ApplicationUpdateOperation{ID: request.OperationID, Phase: "applying", OwnerLease: request.OwnerLease, Release: &request.Release, BootID: request.Predecessor.Core.BootID}
		raw, _ := json.Marshal(o)
		if source == 33 {
			_, e = c.ExecContext(ctx, "CREATE TABLE application_updates(id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL, document TEXT NOT NULL); INSERT INTO application_updates(id,revision,document) VALUES(1,0,?); PRAGMA user_version=36;", string(raw))
		} else {
			_, e = c.ExecContext(ctx, "UPDATE application_updates SET revision=revision+1,document=? WHERE id=1; PRAGMA user_version=36;", string(raw))
		}
		if e != nil {
			return out, e
		}
		next, err := s.snapshotTx(ctx, c, request.Predecessor.Core.ProbeID)
		if err != nil {
			return out, err
		}
		held.Request.Expected = next
		raw, _ = json.Marshal(held)
		if _, e = c.ExecContext(ctx, "UPDATE update_pause SET document=? WHERE id=1 AND phase='paused'", string(raw)); e != nil {
			return out, e
		}
	} else {
		o, _, err := readApplicationUpdate(ctx, c)
		if err != nil || o.ID != request.OperationID || o.OwnerLease != request.OwnerLease || !oneOf(o.Phase, "applying", "recovery") || update.SnapshotDigest("reader-release", o.Release) != update.SnapshotDigest("reader-release", &request.Release) {
			return out, errors.New("reader-bootstrap current operation changed")
		}
		next, err := s.snapshotTx(ctx, c, request.Predecessor.Core.ProbeID)
		if err != nil || !update.SameCoreMetadata(next, held.Request.Expected) {
			return out, errors.New("reader-bootstrap migrated fence changed")
		}
	}
	if fault != nil {
		if e = fault("reader:before-commit"); e != nil {
			return out, e
		}
	}
	if _, e = c.ExecContext(ctx, "COMMIT"); e != nil {
		return out, e
	}
	if e = c.Close(); e != nil {
		return out, e
	}
	// The durable old33 image remains an inspection/recovery original, never a
	// DB restore. New code owns a normal schema35 checkpoint/fence from here.
	if fault != nil {
		if e = fault("reader:after-commit"); e != nil {
			return out, e
		}
	}
	unix.Flock(int(backupLock.Fd()), unix.LOCK_UN)
	checkpoint, e := s.prepareApplicationCheckpoint(ctx, request.OperationID, request.PreviousEngineSHA256)
	if e != nil {
		return out, e
	}
	return ApplicationReaderBootstrapReceipt{Version: 1, RequestSHA256: index.RequestSHA256, Original: index.Original, Checkpoint: checkpoint, Phase: "reader-migrated-forward-only"}, nil
}

func publishApplicationReaderIndex(s *Store, d int, index applicationReaderBootstrapIndex) error {
	raw, _ := json.Marshal(index)
	name := "reader-bootstrap-index-" + metadataHash(raw) + ".json"
	if e := s.publishRecovery(d, name, raw); e != nil {
		return e
	}
	if e := unix.Renameat2(d, name, d, "reader-bootstrap.json", unix.RENAME_NOREPLACE); e != nil {
		return e
	}
	return unix.Fsync(d)
}

func applicationReaderAppOnly(ctx context.Context, r snapshotReader) error {
	var raw string
	if e := r.QueryRowContext(ctx, "SELECT document FROM accounts WHERE id=1").Scan(&raw); e != nil {
		return e
	}
	var accounts AccountsRecord
	if update.StrictJSON([]byte(raw), &accounts) != nil {
		return errors.New("invalid predecessor accounts")
	}
	for _, a := range accounts.Accounts {
		if a.Signer != nil {
			return errors.New("signer-bound predecessor requires paired custody bootstrap")
		}
	}
	d, _, e := readAgents(ctx, r)
	if e != nil {
		return e
	}
	for _, a := range d.AIRequests {
		if a.HeldMicrousd > 0 || oneOf(a.State, "queued", "dispatching", "running", "unknown", "uncertain") {
			return errors.New("active or uncertain predecessor analysis must reconcile")
		}
	}
	for _, task := range d.Tasks {
		if task.Swap != nil {
			return errors.New("predecessor swap history requires paired custody bootstrap")
		}
	}
	return nil
}

func applicationReaderLegacySnapshot(ctx context.Context, tx snapshotReader, probe, boot string) (update.CoreSnapshot, error) {
	return applicationReaderSourceSnapshot(ctx, tx, probe, boot, 33)
}
func applicationReaderSourceSnapshot(ctx context.Context, tx snapshotReader, probe, boot string, source int) (update.CoreSnapshot, error) {
	// Exact schema33 snapshot shape. Only the schema number and declared new
	// application-updates feature differ in35; underlying raw records stay exact.
	var v struct {
		History                         string
		Schema                          int
		Instance, Setup, Accounts       string
		SetupRevision, AccountsRevision int64
		PasswordSalt, PasswordHash      []byte
		Agents                          string
		AgentsRevision                  int64
		Research                        string
		ResearchRevision                int64
	}
	v.Schema = source
	if e := tx.QueryRowContext(ctx, "SELECT instance_id,setup,revision,password_salt,password_hash FROM instance WHERE id=1").Scan(&v.Instance, &v.Setup, &v.SetupRevision, &v.PasswordSalt, &v.PasswordHash); e != nil {
		return update.CoreSnapshot{}, e
	}
	if e := tx.QueryRowContext(ctx, "SELECT document,revision FROM accounts WHERE id=1").Scan(&v.Accounts, &v.AccountsRevision); e != nil {
		return update.CoreSnapshot{}, e
	}
	if e := tx.QueryRowContext(ctx, "SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&v.Agents, &v.AgentsRevision); e != nil {
		return update.CoreSnapshot{}, e
	}
	if e := tx.QueryRowContext(ctx, "SELECT document,revision FROM wen_research WHERE id=1").Scan(&v.Research, &v.ResearchRevision); e != nil {
		return update.CoreSnapshot{}, e
	}
	var e error
	v.History, e = historySnapshot(ctx, tx)
	if e != nil {
		return update.CoreSnapshot{}, e
	}
	features := []string{}
	for _, f := range UpdateContract().State[0].Features {
		if (source != 33 || f != "application-updates-v1") && f != "ordinary-custody-admission-v1" {
			features = append(features, f)
		}
	}
	return update.CoreSnapshot{Version: 1, ProbeID: probe, BootID: boot, InstanceID: v.Instance, State: update.State{Name: "core-db", Version: source, Features: features, Anchor: update.SnapshotDigest("core-db", v)}}, nil
}

func verifyApplicationReaderOriginal(ctx context.Context, d int, index applicationReaderBootstrapIndex, directory string) error {
	b, e := recoveryRead(d, index.Original.File, update.MaxMetadataBackup)
	if e != nil || int64(len(b)) != index.Original.Size || metadataHash(b) != index.Original.SHA256 {
		return errors.New("original reader-bootstrap snapshot changed")
	}
	db, e := sql.Open("sqlite", fmt.Sprintf("file:/proc/self/fd/%d/%s?mode=ro&immutable=1", d, index.Original.File))
	if e != nil {
		return e
	}
	defer db.Close()
	var schema int
	var integrity string
	if e = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema); e != nil || schema != index.Request.Predecessor.Core.State.Version {
		return errors.New("original reader snapshot schema changed")
	}
	if e = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); e != nil || integrity != "ok" {
		return errors.New("original reader snapshot is not intact")
	}
	p := index.Request.Predecessor.Core
	got, e := applicationReaderSourceSnapshot(ctx, db, p.ProbeID, p.BootID, p.State.Version)
	if e != nil || !update.SameCoreMetadata(got, p) {
		return errors.New("original reader snapshot differs from predecessor")
	}
	if p.State.Version == 35 {
		return verifyReader35Preparation(ctx, db, directory, index.Request)
	}
	return nil
}
