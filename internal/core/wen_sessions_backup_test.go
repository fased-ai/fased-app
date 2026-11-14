//go:build linux

package core

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestWENSessionStorageSnapshotBackup(t *testing.T) {
	s, h, _, review := sessionFixture(t, "review", "fixture-4h-unknown")
	v, e := h.CreateWENSession(t.Context(), review)
	if e != nil {
		t.Fatal(e)
	}
	id := v.Sessions[0].Record.SessionID
	v = sessionStep(t, h, v, id)
	v, _ = sessionAcceptance(t, h, v, id)
	v = sessionStep(t, h, v, id)
	proof := pausedBackup(t, s)
	if _, e = s.BackupMetadata(t.Context(), proof); e != nil {
		t.Fatal(e)
	}
	image := filepath.Join(s.dir, "lifecycle", proof.IntentID, "metadata.sqlite")
	db, e := sql.Open("sqlite", "file:"+image+"?mode=ro&immutable=1")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var live, saved string
	s.db.QueryRow("SELECT document || ':' || revision FROM agent_workspace WHERE id=1").Scan(&live)
	db.QueryRow("SELECT document || ':' || revision FROM agent_workspace WHERE id=1").Scan(&saved)
	if live != saved {
		t.Fatal("backup changed session or native holds")
	}
	d, _, e := readAgents(t.Context(), db)
	if e != nil || d.WENRehearsal == nil || len(d.WENRehearsal.Sessions) != 1 || d.WENRehearsal.Sessions[0].Operations[0].Status != "uncertain" || d.WENRehearsal.Pools[0].Budget.UnsettledLamports != "200000000" {
		t.Fatal("backup omitted protected rehearsal", e)
	}
	all, _, e := readWENLibraries(t.Context(), db)
	if e != nil || all[v.Profile].Versions[0].Raw != review.Source.Raw {
		t.Fatal("backup lost original strategy", e)
	}
}
