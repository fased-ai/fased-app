//go:build linux

package core

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestWENResearchAISnapshotBackup(t *testing.T) {
	s, _, _, c := readBatchFixture(t)
	before, e := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	r := preparedResearch(t, s, researchInput(t, c))
	after, e := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if e != nil || before.State.Anchor == after.State.Anchor {
		t.Fatal("research not in snapshot", e)
	}
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
	d, rev, e := readAgents(t.Context(), db)
	if e != nil || rev != r.Revision || len(d.ResearchTurns) != 1 || d.ResearchTurns[0].RootSHA256 != r.Turn.RootSHA256 || d.AIRequests[0].Context != r.Request.Context || d.AIRequests[0].Disclosure != r.Request.Disclosure || d.AIRequests[0].Digest != r.Request.Digest {
		t.Fatal("research or obligations absent from backup", e)
	}
	var live, saved string
	s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&live)
	db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&saved)
	if live != saved {
		t.Fatal("backup reconstructed or changed the workspace")
	}
}

func TestWENResearchAILegacyHolds(t *testing.T) {
	s := fixtureStore(t)
	v := aiPrepare(t, s)
	injectRevisionAgents(t, s, func(d *agentDocument) {
		d.AIRequests[0].State = "unknown"
		d.AIRequests[0].HeldMicrousd = 100
	})
	s.db.Exec("PRAGMA user_version=11")
	n, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	got, e := n.Agents(t.Context(), v.ProfileID, "")
	if e != nil || len(got.AIRequests) != 1 || got.AIRequests[0].HeldMicrousd != 100 || got.AIRequests[0].State != "unknown" || got.AIRequests[0].Digest != v.AIRequests[0].Digest || got.AIRequests[0].Context != v.AIRequests[0].Context {
		t.Fatal("legacy liability or disclosure changed", e)
	}
}

func TestWENResearchLoopSnapshotBackup(t *testing.T) {
	s, _, _, c := readBatchFixture(t)
	r := loopPrepared(t, s, c, 2, 3)
	r = loopAdvance(t, s, r, fixtureAITransport{})
	r = loopNext(t, s, r)
	before, e := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
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
		t.Fatal("backup rewrote transcript or prepared child")
	}
	d, rev, e := readAgents(t.Context(), db)
	if e != nil || rev != r.Revision || len(d.ResearchTurns[0].Models) != 2 || len(d.ResearchTurns[0].Tools) != 1 || d.ResearchTurns[0].CurrentChildID != r.Request.ID {
		t.Fatal("v2 backup missing", e)
	}
	if before.State.Version != StateSchema {
		t.Fatal("schema boundary not advertised", before.State.Version)
	}
}
