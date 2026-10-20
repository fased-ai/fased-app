//go:build linux

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// The fixed app-owned directory contains credentials and sessions. No API
// accepts a destination path or returns database bytes. Root/host compromise
// and disk loss are outside this same-filesystem engineering backup.
func (s *Store) BackupMetadata(ctx context.Context, p update.Preflight) (update.RecoveryIndex, error) {
	var zero update.RecoveryIndex
	binding, e := update.BackupBinding(p)
	if e != nil {
		return zero, e
	}
	d, e := recoveryDirectory(s.dir, p.IntentID)
	if e != nil {
		return zero, e
	}
	defer unix.Close(d)
	lock, e := recoveryFile(d, "backup.lock", true)
	if e != nil {
		return zero, e
	}
	defer lock.Close()
	if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return zero, errors.New("metadata backup already in progress; retry")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)

	// Keep the actual paused SQLite owner and all legitimate writers fenced
	// through serialization, verification and publication, including other
	// Store connections. Serialize reads the connection's complete WAL view.
	c, e := s.db.Conn(ctx)
	if e != nil {
		return zero, e
	}
	defer c.Close()
	if _, e = c.ExecContext(ctx, "BEGIN IMMEDIATE"); e != nil {
		return zero, e
	}
	defer c.ExecContext(context.Background(), "ROLLBACK")
	phase, pause, e := readPause(ctx, c)
	if e != nil {
		return zero, e
	}
	current, e := s.snapshotTx(ctx, c, p.Pause.Core.ProbeID)
	if e != nil {
		return zero, e
	}
	if phase != "paused" || pause.PauseID != p.Pause.PauseID || update.SnapshotDigest("pause-request", pause.Request) != update.SnapshotDigest("pause-request", p.Pause.Request) || update.SnapshotDigest("core", current) != update.SnapshotDigest("core", p.Pause.Core) {
		return zero, errors.New("stale core boot, metadata or pause; preserve backup and retry fresh preflight")
	}
	// An existing index is never sufficient alone: always rehash and verify
	// its actual private SQLite image against the currently held metadata.
	if raw, err := recoveryRead(d, "recovery.json", 16384); err == nil {
		var index update.RecoveryIndex
		if update.StrictJSON(raw, &index) != nil || !update.ValidRecoveryIndex(index, binding) {
			return zero, errors.New("conflicting recovery index; preserving it")
		}
		if e = s.verifyMetadata(ctx, d, index.Snapshot, p); e != nil {
			return zero, e
		}
		if e = unix.Fsync(d); e != nil {
			return zero, e
		}
		return index, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return zero, err
	}

	// Persist the exact snapshot identity before writing bytes. A crash can
	// leave a matching prefix, never an acknowledged partial snapshot. No
	// foreign, conflicting or changed prefix is truncated or replaced.
	var image []byte
	serialize := func() error {
		var pages, size int64
		if err := c.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
			return err
		}
		if err := c.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); err != nil {
			return err
		}
		if pages <= 0 || size <= 0 || pages > update.MaxMetadataBackup/size {
			return errors.New("metadata snapshot exceeds bounded backup size")
		}
		return c.Raw(func(raw any) error {
			v, ok := raw.(interface{ Serialize() ([]byte, error) })
			if !ok {
				return errors.New("SQLite snapshot support unavailable")
			}
			var err error
			image, err = v.Serialize()
			return err
		})
	}
	index := update.RecoveryIndex{Version: 1, Phase: "metadata-backed-up", Binding: binding, BindingSHA256: update.RecoveryDigest(binding)}
	if raw, err := recoveryRead(d, "request.json", 16384); err == nil {
		if update.StrictJSON(raw, &index) != nil || !update.ValidRecoveryIndex(index, binding) {
			return zero, errors.New("conflicting recovery request; preserving it")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return zero, err
	} else {
		if e = serialize(); e != nil {
			return zero, e
		}
		index.Snapshot = update.MetadataSnapshot{File: "metadata.sqlite", Size: int64(len(image)), SHA256: metadataHash(image)}
		if !update.ValidRecoveryIndex(index, binding) {
			return zero, errors.New("invalid serialized metadata size")
		}
	}
	record, _ := json.Marshal(index)
	if e = s.publishRecovery(d, "request.json", record); e != nil {
		return zero, e
	}
	if e = s.backupStep("backup:request"); e != nil {
		return zero, e
	}
	if e = s.verifyMetadata(ctx, d, index.Snapshot, p); errors.Is(e, os.ErrNotExist) {
		if image == nil {
			e = serialize()
			if e != nil {
				return zero, e
			}
		}
		if int64(len(image)) != index.Snapshot.Size || metadataHash(image) != index.Snapshot.SHA256 {
			return zero, errors.New("interrupted snapshot source changed; preserving partial bytes and request")
		}
		if e = s.publishRecovery(d, "metadata.sqlite", image); e != nil {
			return zero, e
		}
		if e = s.backupStep("backup:snapshot"); e != nil {
			return zero, e
		}
		if e = s.verifyMetadata(ctx, d, index.Snapshot, p); e != nil {
			return zero, e
		}
	} else if e != nil {
		return zero, e
	}
	if e = s.backupStep("backup:verified"); e != nil {
		return zero, e
	}
	if e = s.publishRecovery(d, "recovery.json", record); e != nil {
		return zero, e
	}
	if e = s.backupStep("backup:durable"); e != nil {
		return zero, e
	}
	return index, nil
}

func metadataHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func (s *Store) backupStep(step string) error {
	if s.backupFault != nil {
		return s.backupFault(step)
	}
	return nil
}

func recoveryDirectory(base, id string) (int, error) {
	fd, e := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return -1, e
	}
	for _, name := range []string{"lifecycle", id} {
		if e = unix.Mkdirat(fd, name, 0700); e != nil && !errors.Is(e, unix.EEXIST) {
			unix.Close(fd)
			return -1, e
		}
		if e = unix.Fsync(fd); e != nil {
			unix.Close(fd)
			return -1, e
		}
		next, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
		var st unix.Stat_t
		if e = unix.Fstat(fd, &st); e != nil || int(st.Uid) != os.Geteuid() || st.Mode&0077 != 0 {
			unix.Close(fd)
			return -1, errors.New("unsafe recovery directory")
		}
	}
	return fd, nil
}

func recoveryFile(d int, name string, create bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if create {
		flags = unix.O_RDWR | unix.O_CREAT | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	}
	fd, e := unix.Openat(d, name, flags, 0600)
	if e != nil {
		return nil, e
	}
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != os.Geteuid() || st.Mode&0077 != 0 || st.Nlink != 1 {
		unix.Close(fd)
		return nil, errors.New("unsafe recovery file; preserving it")
	}
	return os.NewFile(uintptr(fd), name), nil
}
func recoveryRead(d int, name string, max int64) ([]byte, error) {
	f, e := recoveryFile(d, name, false)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > max {
		return nil, errors.New("recovery file too large")
	}
	return b, nil
}

// Atomic no-replace rename publishes exactly one complete file. Existing final
// bytes must match; matching pending prefixes resume without truncation.
func (s *Store) publishRecovery(d int, name string, data []byte) error {
	pending := name + ".pending"
	if got, e := recoveryRead(d, name, int64(len(data))); e == nil {
		if !bytes.Equal(got, data) {
			return errors.New("conflicting recovery bytes")
		}
		return unix.Fsync(d)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	f, e := recoveryFile(d, pending, true)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if st.Size() > int64(len(data)) {
		return errors.New("conflicting pending recovery size")
	}
	prefix, e := io.ReadAll(io.LimitReader(f, int64(len(data))+1))
	if e != nil {
		return e
	}
	if !bytes.Equal(prefix, data[:len(prefix)]) {
		return errors.New("conflicting pending recovery prefix; preserving it")
	}
	for offset := len(prefix); offset < len(data); {
		n := min(4096, len(data)-offset)
		w, err := f.Write(data[offset : offset+n])
		if err != nil {
			return err
		}
		if w != n {
			return io.ErrShortWrite
		}
		offset += n
		if e = s.backupStep("write:" + name); e != nil {
			return e
		}
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = unix.Renameat2(d, pending, d, name, unix.RENAME_NOREPLACE); e != nil {
		return e
	}
	if e = s.backupStep("publish:" + name); e != nil {
		return e
	}
	return unix.Fsync(d)
}

func (s *Store) verifyMetadata(ctx context.Context, d int, snap update.MetadataSnapshot, p update.Preflight) error {
	f, e := recoveryFile(d, snap.File, false)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, update.MaxMetadataBackup+1))
	if e != nil || n != snap.Size || hex.EncodeToString(h.Sum(nil)) != snap.SHA256 {
		return errors.New("metadata snapshot size or hash mismatch; preserving it")
	}
	// Immutable read-only URI references the validated held FD, never a caller
	// path. No migration, journal replay or write is performed on the backup.
	db, e := sql.Open("sqlite", fmt.Sprintf("file:/proc/self/fd/%d?mode=ro&immutable=1", f.Fd()))
	if e != nil {
		return e
	}
	defer db.Close()
	var integrity string
	if e = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); e != nil || integrity != "ok" {
		return errors.New("metadata snapshot integrity check failed")
	}
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	got, e := s.snapshotTx(ctx, tx, p.Pause.Core.ProbeID)
	if e != nil {
		return e
	}
	phase, r, e := readPause(ctx, tx)
	if e != nil {
		return e
	}
	if phase != "paused" || r.PauseID != p.Pause.PauseID || !update.SameCoreMetadata(got, p.Pause.Core) || update.SnapshotDigest("pause-request", r.Request) != update.SnapshotDigest("pause-request", p.Pause.Request) {
		return errors.New("snapshot metadata/pause mismatch")
	}
	return nil
}
