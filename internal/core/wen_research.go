package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/wenstrategy"
	"io"
	"net/http"
	"time"
)

type WENVersion struct {
	Digest    string `json:"digest"`
	Raw       string `json:"raw"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	Origin    string `json:"origin"`
}
type WENLibrary struct {
	Revision int64                         `json:"revision"`
	Versions []WENVersion                  `json:"versions"`
	Bundles  []wenstrategy.ImportedHistory `json:"bundles"`
}
type WENResearchCommand struct {
	Revision int64  `json:"revision"`
	Profile  string `json:"profile"`
	Action   string `json:"action"`
	Raw      string `json:"raw"`
}

func migrateWENResearch(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return e
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	_, e = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS wen_research(id INTEGER PRIMARY KEY CHECK(id=1),document TEXT NOT NULL,revision INTEGER NOT NULL);
INSERT OR IGNORE INTO wen_research VALUES(1,'{}',0);
CREATE TRIGGER IF NOT EXISTS pause_wen_research BEFORE UPDATE ON wen_research WHEN (SELECT phase FROM update_pause WHERE id=1)='paused' BEGIN SELECT RAISE(ABORT,'core metadata paused for update'); END;
PRAGMA user_version=11;`)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func readWENLibraries(ctx context.Context, r snapshotReader) (map[string]WENLibrary, int64, error) {
	var raw string
	var rev int64
	e := r.QueryRowContext(ctx, "SELECT document,revision FROM wen_research WHERE id=1").Scan(&raw, &rev)
	if e != nil {
		return nil, 0, e
	}
	if len(raw) > 2*1024*1024 {
		return nil, 0, errors.New("research capacity exceeded; preserve state")
	}
	var all map[string]WENLibrary
	e = json.Unmarshal([]byte(raw), &all)
	if e == nil && (all == nil || rev < 0) {
		e = errors.New("invalid research state; preserve and recover")
	}
	return all, rev, e
}
func libraryFor(all map[string]WENLibrary, profile string, rev int64) WENLibrary {
	v := all[profile]
	v.Revision = rev
	if v.Versions == nil {
		v.Versions = []WENVersion{}
	}
	if v.Bundles == nil {
		v.Bundles = []wenstrategy.ImportedHistory{}
	}
	return v
}
func (s *Store) WENResearch(ctx context.Context, profile string) (WENLibrary, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return WENLibrary{}, e
	}
	defer tx.Rollback()
	d, _, e := readAgents(ctx, tx)
	if e != nil {
		return WENLibrary{}, e
	}
	if agentProfile(d, profile) < 0 {
		return WENLibrary{}, errors.New("profile not found")
	}
	all, rev, e := readWENLibraries(ctx, tx)
	if e != nil {
		return WENLibrary{}, e
	}
	return libraryFor(all, profile, rev), nil
}
func (s *Store) ChangeWENResearch(ctx context.Context, c WENResearchCommand) (WENLibrary, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return WENLibrary{}, e
	}
	defer tx.Rollback()
	phase, _, e := readPause(ctx, tx)
	if e != nil {
		return WENLibrary{}, e
	}
	if phase == "paused" {
		return WENLibrary{}, ErrCorePaused
	}
	d, _, e := readAgents(ctx, tx)
	if e != nil {
		return WENLibrary{}, e
	}
	if agentProfile(d, c.Profile) < 0 {
		return WENLibrary{}, errors.New("profile not found")
	}
	all, rev, e := readWENLibraries(ctx, tx)
	if e != nil {
		return WENLibrary{}, e
	}
	if rev != c.Revision {
		return WENLibrary{}, ErrConflict
	}
	v := libraryFor(all, c.Profile, rev)
	switch c.Action {
	case "save":
		a, e := wenstrategy.Decode([]byte(c.Raw))
		if e != nil {
			return v, e
		}
		if a.Document.Execution.MandateID != nil || a.Document.Execution.Mode != "review" {
			return v, errors.New("review-only strategy without mandate required")
		}
		for _, old := range v.Versions {
			if old.Digest == a.Digest {
				return v, nil
			}
		}
		if len(v.Versions) >= 100 {
			return v, errors.New("100-version capacity reached")
		}
		v.Versions = append(v.Versions, WENVersion{a.Digest, c.Raw, a.Document.Name, s.now().UTC().Format(time.RFC3339), "owner-import"})
	case "history":
		h, e := wenstrategy.ParseHistory([]byte(c.Raw))
		if e != nil {
			return v, e
		}
		if len(v.Bundles) >= 50 {
			return v, errors.New("50-bundle capacity reached")
		}
		v.Bundles = append(v.Bundles, h)
	default:
		return v, errors.New("unsupported research operation")
	}
	v, e = saveWENLibrary(ctx, tx, all, c.Profile, v, rev)
	if e != nil {
		return v, e
	}
	if e = tx.Commit(); e != nil {
		return v, e
	}
	return v, nil
}

// Shared transactional writer; callers own the pause guard and final commit.
func saveWENLibrary(ctx context.Context, tx *sql.Tx, all map[string]WENLibrary, profile string, v WENLibrary, rev int64) (WENLibrary, error) {
	v.Revision = rev + 1
	all[profile] = v
	raw, e := json.Marshal(all)
	if e != nil || len(raw) > 2*1024*1024 {
		return v, errors.New("research capacity exceeded; export records")
	}
	result, e := tx.ExecContext(ctx, "UPDATE wen_research SET document=?,revision=? WHERE id=1 AND revision=?", string(raw), v.Revision, rev)
	if e != nil {
		return v, metadataWriteError(e)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return v, err
		}
		return v, ErrConflict
	}
	return v, nil
}
func (h *HTTPHandler) wenResearchAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method == http.MethodGet {
		if len(r.URL.Query()) != 1 || len(r.URL.Query()["profile"]) != 1 {
			writeError(w, 400, "one profile required")
			return
		}
		v, e := h.store.WENResearch(r.Context(), r.URL.Query().Get("profile"))
		if e != nil {
			writeError(w, 400, e.Error())
			return
		}
		writeJSON(w, 200, v)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, 400, "unexpected query")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	var c WENResearchCommand
	r.Body = http.MaxBytesReader(w, r.Body, 512*1024)
	defer r.Body.Close()
	body, readErr := io.ReadAll(r.Body)
	if readErr != nil || update.StrictJSON(body, &c) != nil {
		writeError(w, 400, "invalid research command")
		return
	}
	v, e := h.store.ChangeWENResearch(r.Context(), c)
	if errors.Is(e, ErrConflict) || errors.Is(e, ErrCorePaused) {
		writeError(w, 409, e.Error())
		return
	}
	if e != nil {
		writeError(w, 400, e.Error())
		return
	}
	writeJSON(w, 200, v)
}
