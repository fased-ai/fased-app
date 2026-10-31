//go:build linux

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
	"io"
	"os"
)

// PrepareApplicationCheckpoint is a protected owner-controller operation. No
// download path, artifact bytes, key, wallet or endpoint may be supplied here.
func (s *Store) PrepareApplicationCheckpoint(ctx context.Context, id string) (ApplicationCheckpoint, error) {
	return s.prepareApplicationCheckpoint(ctx, id, "")
}

func (s *Store) prepareApplicationCheckpoint(ctx context.Context, id, previousEngine string) (ApplicationCheckpoint, error) {
	var out ApplicationCheckpoint
	if previousEngine != "" && !hexValue(previousEngine, 32) {
		return out, errors.New("exact predecessor executable required")
	}
	pause, e := s.applicationUpdatePause(ctx, id)
	if e != nil {
		return out, e
	}
	d, e := recoveryDirectory(s.dir, id)
	if e != nil {
		return out, e
	}
	defer unix.Close(d)
	lock, e := recoveryFile(d, "backup.lock", true)
	if e != nil {
		return out, e
	}
	defer lock.Close()
	if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return out, errors.New("metadata backup already active")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	c, e := s.db.Conn(ctx)
	if e != nil {
		return out, e
	}
	defer c.Close()
	if _, e = c.ExecContext(ctx, "BEGIN IMMEDIATE"); e != nil {
		return out, e
	}
	defer c.ExecContext(context.Background(), "ROLLBACK")
	phase, held, e := readPause(ctx, c)
	if e != nil {
		return out, e
	}
	current, e := s.snapshotTx(ctx, c, pause.Core.ProbeID)
	if e != nil {
		return out, e
	}
	if phase != "paused" || held.PauseID != pause.PauseID || !update.SameCoreMetadata(current, pause.Core) {
		return out, errors.New("metadata fence changed before backup")
	}
	if raw, err := recoveryRead(d, "application.json", 16384); err == nil {
		if update.StrictJSON(raw, &out) != nil || out.Version != 1 || out.OperationID != id || out.Pause.PauseID != pause.PauseID || out.ManifestSHA256 != pause.Request.ManifestSHA256 || out.Snapshot.File != "application-"+out.Snapshot.SHA256+".sqlite" || out.Snapshot.Size <= 0 || out.Snapshot.Size > update.MaxMetadataBackup || !update.SameCoreMetadata(out.Pause.Core, pause.Core) {
			return out, errors.New("conflicting application checkpoint")
		}
		if previousEngine != "" && out.PreviousEngineSHA256 != previousEngine {
			return out, errors.New("reader-bootstrap predecessor executable changed")
		}
		if e = s.verifyMetadata(ctx, d, out.Snapshot, update.Preflight{Pause: out.Pause}); e != nil {
			return out, e
		}
		return out, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	previous := previousEngine
	if previous == "" {
		previous, e = runningApplicationSHA256()
		if e != nil {
			return out, e
		}
	}
	image, e := serializeMetadataConnection(ctx, c)
	if e != nil {
		return out, e
	}
	out = ApplicationCheckpoint{PreviousEngineSHA256: previous, Version: 1, OperationID: id, ManifestSHA256: pause.Request.ManifestSHA256, Pause: pause, Snapshot: update.MetadataSnapshot{File: "application-" + metadataHash(image) + ".sqlite", Size: int64(len(image)), SHA256: metadataHash(image)}}
	if e = s.publishRecovery(d, out.Snapshot.File, image); e != nil {
		return out, e
	}
	if e = s.verifyMetadata(ctx, d, out.Snapshot, update.Preflight{Pause: pause}); e != nil {
		return out, e
	}
	raw, _ := json.Marshal(out)
	// Only a fully written/fsynced index becomes the fixed commit name. A
	// stopped writer cannot leave a partial committed application.json. Older
	// partial content-addressed files are retained, never truncated or adopted.
	name := "application-index-" + metadataHash(raw) + ".json"
	if e = s.publishRecovery(d, name, raw); e != nil {
		return out, e
	}
	if e = unix.Renameat2(d, name, d, "application.json", unix.RENAME_NOREPLACE); e != nil {
		return out, e
	}
	return out, unix.Fsync(d)
}

// CompleteApplicationCheckpoint never restores a DB or unlocks credentials. It
// releases only the app-only fence after the protected controller proves health
// or compatible code rollback; custody fences cannot take this path.
func (s *Store) CompleteApplicationCheckpoint(ctx context.Context, id string, proof ApplicationUpdateReceipt) error {
	o, _, err := readApplicationUpdate(ctx, s.db)
	if err != nil || o.Release == nil || o.ID != id || proof.ID != id || proof.ManifestSHA256 != o.Release.ManifestSHA256 || proof.Sequence != o.Release.Sequence || !oneOf(proof.Phase, "completed", "rolled-back") || !hexValue(proof.EngineSHA256, 32) {
		return errors.New("exact protected controller outcome required")
	}
	if proof.Phase == "completed" && proof.EngineSHA256 != o.Release.EngineSHA256 {
		return errors.New("target executable differs from admitted release")
	}
	checkpoint, e := s.PrepareApplicationCheckpoint(ctx, id)
	if e != nil {
		// A repeated completion is harmless only for the exact previously released
		// operation, never a different fence or an inferred missing-state success.
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return e
		}
		defer tx.Rollback()
		phase, p, err := readPause(ctx, tx)
		o, _, oe := readApplicationUpdate(ctx, tx)
		if err == nil && oe == nil && phase == "released" && p.Request.IntentID == id && o.ID == id {
			return nil
		}
		return e
	}
	if proof.Phase == "rolled-back" && proof.EngineSHA256 != checkpoint.PreviousEngineSHA256 {
		return errors.New("rollback executable differs from checkpoint")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	phase, p, e := readPause(ctx, tx)
	if e != nil {
		return e
	}
	current, e := s.snapshotTx(ctx, tx, update.NewProbe())
	if e != nil {
		return e
	}
	if phase != "paused" || p.Request.IntentID != id || p.PauseID != checkpoint.Pause.PauseID || !update.SameCoreMetadata(current, checkpoint.Pause.Core) {
		return errors.New("application recovery fence mismatch")
	}
	if _, e = tx.ExecContext(ctx, "UPDATE update_pause SET phase='released' WHERE id=1"); e != nil {
		return e
	}
	currentOperation, revision, e := readApplicationUpdate(ctx, tx)
	if e != nil {
		return e
	}
	if currentOperation.ID != id || currentOperation.Release == nil || currentOperation.Release.ManifestSHA256 != proof.ManifestSHA256 {
		return errors.New("application outcome changed before release")
	}
	currentOperation.Receipt = &proof
	currentOperation.Phase = proof.Phase
	currentOperation.FinishedAt = s.now().Unix()
	currentOperation.Failure = ""
	if e = saveApplicationUpdate(ctx, tx, currentOperation, revision); e != nil {
		return e
	}
	if s.pauseFault != nil {
		if e = s.pauseFault("application-release:before-commit"); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func runningApplicationSHA256() (string, error) {
	f, e := os.Open("/proc/self/exe")
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
