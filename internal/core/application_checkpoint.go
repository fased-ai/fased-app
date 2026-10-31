package core

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
)

// ApplicationCheckpoint is app metadata only, never signer or financial restore.
// Its private SQLite image stays in the existing app-owned recovery directory.
type ApplicationCheckpoint struct {
	PreviousEngineSHA256 string                  `json:"previousEngineSha256"`
	Version              int                     `json:"version"`
	OperationID          string                  `json:"operationId"`
	ManifestSHA256       string                  `json:"manifestSha256"`
	Pause                update.CorePauseReceipt `json:"pause"`
	Snapshot             update.MetadataSnapshot `json:"snapshot"`
}

func (s *Store) applicationUpdatePause(ctx context.Context, id string) (update.CorePauseReceipt, error) {
	if s.fixture != nil || s.custodyNetwork != "" {
		return update.CorePauseReceipt{}, errors.New("installed custody requires the existing paired update controller")
	}
	if !s.aiMu.TryLock() {
		return update.CorePauseReceipt{}, errors.New("analysis is active; update deferred")
	}
	defer s.aiMu.Unlock()
	if !s.newsMu.TryLock() {
		return update.CorePauseReceipt{}, errors.New("news fetch is active; update deferred")
	}
	defer s.newsMu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return update.CorePauseReceipt{}, e
	}
	defer tx.Rollback()
	o, _, e := readApplicationUpdate(ctx, tx)
	if e != nil {
		return update.CorePauseReceipt{}, e
	}
	if o.ID != id || o.Release == nil || !oneOf(o.Phase, "applying", "recovery") {
		return update.CorePauseReceipt{}, errors.New("exact active application update required")
	}
	var raw string
	if e = tx.QueryRowContext(ctx, "SELECT document FROM accounts WHERE id=1").Scan(&raw); e != nil {
		return update.CorePauseReceipt{}, e
	}
	var accounts AccountsRecord
	if update.StrictJSON([]byte(raw), &accounts) != nil {
		return update.CorePauseReceipt{}, errors.New("invalid account inventory")
	}
	for _, a := range accounts.Accounts {
		if a.Signer != nil {
			return update.CorePauseReceipt{}, errors.New("signer-bound accounts require paired custody updates")
		}
	}
	d, _, e := readAgents(ctx, tx)
	if e != nil {
		return update.CorePauseReceipt{}, e
	}
	for _, r := range d.AIRequests {
		if r.HeldMicrousd > 0 || oneOf(r.State, "queued", "dispatching", "running", "unknown", "uncertain") {
			return update.CorePauseReceipt{}, errors.New("active or uncertain analysis must reconcile before updating")
		}
	}
	for _, t := range d.Tasks {
		if t.Swap != nil {
			return update.CorePauseReceipt{}, errors.New("swap history requires the existing paired custody update controller")
		}
	}
	phase, prior, e := readPause(ctx, tx)
	if e != nil {
		return update.CorePauseReceipt{}, e
	}
	snapshot, e := s.snapshotTx(ctx, tx, update.NewProbe())
	if e != nil {
		return update.CorePauseReceipt{}, e
	}
	request := update.CorePauseRequest{Version: 1, IntentID: id, Sequence: o.Release.Sequence, ManifestSHA256: o.Release.ManifestSHA256, Expected: snapshot}
	if phase == "paused" {
		if prior.Request.IntentID != id || prior.Request.ManifestSHA256 != o.Release.ManifestSHA256 || !update.SameCoreMetadata(snapshot, prior.Request.Expected) {
			return update.CorePauseReceipt{}, errors.New("another or changed metadata fence is active")
		}
		request = prior.Request
	}
	if e = tx.Commit(); e != nil {
		return update.CorePauseReceipt{}, e
	}
	return s.UpdatePause(ctx, update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: &request})
}
