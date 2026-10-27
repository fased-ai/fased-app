package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/fased-ai/fased-app/internal/update"
)

const pauseMigration = `CREATE TABLE update_pause(id INTEGER PRIMARY KEY CHECK(id=1),phase TEXT NOT NULL CHECK(phase IN ('idle','paused','released')),document TEXT NOT NULL);
INSERT INTO update_pause VALUES(1,'idle','');
CREATE TRIGGER update_pause_instance BEFORE UPDATE OF setup,revision,password_salt,password_hash ON instance
WHEN (SELECT phase FROM update_pause WHERE id=1)='paused' BEGIN SELECT RAISE(ABORT,'core metadata paused for update'); END;
CREATE TRIGGER update_pause_accounts BEFORE UPDATE ON accounts
WHEN (SELECT phase FROM update_pause WHERE id=1)='paused' BEGIN SELECT RAISE(ABORT,'core metadata paused for update'); END;
PRAGMA user_version=3;`

var ErrCorePaused = errors.New("core metadata paused for update")

func metadataWriteError(e error) error {
	if e != nil && strings.Contains(e.Error(), ErrCorePaused.Error()) {
		return ErrCorePaused
	}
	return e
}

type pauseRecord struct {
	PauseID string                  `json:"pauseId"`
	Request update.CorePauseRequest `json:"request"`
}

func readPause(ctx context.Context, tx snapshotReader) (string, pauseRecord, error) {
	var phase, document string
	var r pauseRecord
	e := tx.QueryRowContext(ctx, "SELECT phase,document FROM update_pause WHERE id=1").Scan(&phase, &document)
	if e != nil {
		return "", r, e
	}
	if phase == "idle" && document == "" {
		return phase, r, nil
	}
	if (phase != "paused" && phase != "released") || update.StrictJSON([]byte(document), &r) != nil || !update.IDOK(r.PauseID) || !update.ValidPauseRequest(r.Request) {
		return "", r, errors.New("damaged core pause; preserve state and recover explicitly")
	}
	return phase, r, nil
}

// The fence and its snapshot share a SQLite transaction. FULL synchronous WAL
// commit precedes acknowledgement. The same SQL triggers protect other Store
// connections, credential recovery and restart; no in-memory lease can expire.
func (s *Store) UpdatePause(ctx context.Context, c update.CorePauseCommand) (update.CorePauseReceipt, error) {
	if !update.IDOK(c.ProbeID) {
		return update.CorePauseReceipt{}, errors.New("one pause challenge required")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return update.CorePauseReceipt{}, e
	}
	defer tx.Rollback()
	phase, r, e := readPause(ctx, tx)
	if e != nil {
		return update.CorePauseReceipt{}, e
	}
	current, e := s.snapshotTx(ctx, tx, c.ProbeID)
	if e != nil {
		return update.CorePauseReceipt{}, e
	}
	switch c.Action {
	case "status":
	case "pause":
		if c.Request == nil || !update.ValidPauseRequest(*c.Request) {
			return update.CorePauseReceipt{}, errors.New("bound pause request required")
		}
		want := *c.Request
		if phase == "paused" {
			if update.SnapshotDigest("pause-request", want) != update.SnapshotDigest("pause-request", r.Request) || !update.SameCoreMetadata(current, r.Request.Expected) {
				return update.CorePauseReceipt{}, errors.New("another or stale core pause owns the slot")
			}
		} else {
			if phase == "released" && want.Sequence <= r.Request.Sequence {
				return update.CorePauseReceipt{}, errors.New("released pause sequence cannot replay")
			}
			if current.BootID != want.Expected.BootID || !update.SameCoreMetadata(current, want.Expected) {
				return update.CorePauseReceipt{}, errors.New("core metadata or boot changed before pause")
			}
			r = pauseRecord{PauseID: update.NewProbe(), Request: want}
			phase = "paused"
			b, _ := json.Marshal(r)
			if _, e = tx.ExecContext(ctx, "UPDATE update_pause SET phase='paused',document=? WHERE id=1", string(b)); e != nil {
				return update.CorePauseReceipt{}, e
			}
		}
	case "complete":
		if c.Completed == nil || !update.ValidInstallation(*c.Completed) || c.Completed.Phase != "completed" || phase == "idle" {
			return update.CorePauseReceipt{}, errors.New("exact completed signer receipt and existing fence required")
		}
		q := c.Completed.Binding.Index.Binding.Pause
		if q.Request == nil || q.PauseID != r.PauseID || update.SnapshotDigest("pause-request", *q.Request) != update.SnapshotDigest("pause-request", r.Request) {
			return update.CorePauseReceipt{}, errors.New("completion cannot release a different core pause")
		}
		if phase == "paused" && !update.SameCoreMetadata(current, r.Request.Expected) {
			return update.CorePauseReceipt{}, errors.New("completion metadata changed while paused")
		}
		if _, e = tx.ExecContext(ctx, "UPDATE update_pause SET phase='released' WHERE id=1"); e != nil {
			return update.CorePauseReceipt{}, e
		}
		phase = "released"
	case "release":
		// Metadata release is never signer authorization. The protected owner
		// conductor obtains this exact aborted receipt from the signer first.
		if c.Aborted == nil || c.Aborted.State != "aborted" || !update.ValidPauseRequest(update.PauseFor(*c.Aborted)) {
			return update.CorePauseReceipt{}, errors.New("exact aborted intent receipt required")
		}
		if phase == "idle" {
			r = pauseRecord{PauseID: update.NewProbe(), Request: update.PauseFor(*c.Aborted)}
			b, _ := json.Marshal(r)
			if _, e = tx.ExecContext(ctx, "UPDATE update_pause SET phase='released',document=? WHERE id=1", string(b)); e != nil {
				return update.CorePauseReceipt{}, e
			}
			phase = "released"
		} else {
			if update.SnapshotDigest("pause-request", update.PauseFor(*c.Aborted)) != update.SnapshotDigest("pause-request", r.Request) {
				return update.CorePauseReceipt{}, errors.New("abort cannot release a different core pause")
			}
			if _, e = tx.ExecContext(ctx, "UPDATE update_pause SET phase='released' WHERE id=1"); e != nil {
				return update.CorePauseReceipt{}, e
			}
			phase = "released"
		}
	default:
		return update.CorePauseReceipt{}, errors.New("unsupported core pause operation")
	}
	if s.pauseFault != nil {
		if e = s.pauseFault("pause:before-commit"); e != nil {
			return update.CorePauseReceipt{}, e
		}
	}
	if e = tx.Commit(); e != nil {
		return update.CorePauseReceipt{}, e
	}
	if s.pauseFault != nil {
		if e = s.pauseFault("pause:durable"); e != nil {
			return update.CorePauseReceipt{}, e
		}
	}
	out := update.CorePauseReceipt{Version: 1, State: phase, PauseID: r.PauseID, Core: current}
	if phase != "idle" {
		out.Request = &r.Request
	}
	return out, nil
}
