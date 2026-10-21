package core

import (
	"context"
	"database/sql"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"net/url"
	"os"
	"path/filepath"
)

// InspectFixtureMetadata never invokes OpenStore, migrations, WAL setup or repair.
// The controller must have positively stopped the app before this cold probe.
func inspectFixtureMetadata(ctx context.Context, dir, probe string, pause *FixtureColdMetadata) (update.CoreSnapshot, error) {
	var out update.CoreSnapshot
	p := filepath.Join(dir, "core.sqlite")
	if !filepath.IsAbs(dir) || !update.IDOK(probe) {
		return out, errors.New("fixed cold metadata probe required")
	}
	real, e := filepath.EvalSymlinks(p)
	if e != nil || real != p {
		return out, errors.New("real cold database required")
	}
	st, e := os.Lstat(p)
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return out, errors.New("private existing database required")
	}
	if w, e := os.Stat(p + "-wal"); e == nil && w.Size() > 0 {
		return out, errors.New("cold database has uncheckpointed WAL; hold without repair")
	} else if e != nil && !os.IsNotExist(e) {
		return out, e
	}
	db, e := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro&immutable=1&_pragma=query_only(1)"}).String())
	if e != nil {
		return out, e
	}
	defer db.Close()
	s := &Store{db: db, dir: dir, bootID: update.NewProbe()}
	c, e := s.UpdateSnapshot(ctx, probe)
	if e != nil {
		return out, e
	}
	if pause != nil {
		phase, p, e := readPause(ctx, db)
		if e != nil {
			return out, e
		}
		pause.Core = c
		pause.Phase = phase
		pause.PauseID = p.PauseID
		if phase != "idle" {
			pause.Request = &p.Request
		}
	}
	return c, nil
}

type FixtureColdMetadata struct {
	Core    update.CoreSnapshot      `json:"core"`
	Phase   string                   `json:"phase"`
	PauseID string                   `json:"pauseId"`
	Request *update.CorePauseRequest `json:"request"`
}

func InspectFixtureMetadata(ctx context.Context, dir, probe string) (update.CoreSnapshot, error) {
	return inspectFixtureMetadata(ctx, dir, probe, nil)
}
func InspectFixtureColdMetadata(ctx context.Context, dir, probe string) (FixtureColdMetadata, error) {
	var out FixtureColdMetadata
	_, e := inspectFixtureMetadata(ctx, dir, probe, &out)
	return out, e
}
