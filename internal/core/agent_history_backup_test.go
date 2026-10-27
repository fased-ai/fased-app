//go:build linux

package core

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestAgentHistoryIncludedInProtectedMetadataBackup(t *testing.T) {
	s := fixtureStore(t)
	v, id := completedHistoryTask(t, s, "Backed up result")
	agentApply(t, s, AgentCommand{Action: "task-archive", ProfileID: v.ProfileID, ID: id})
	v = aiStart(t, s, aiPrepare(t, s))
	if e := s.advanceAgentAI(t.Context(), fixtureAITransport{}, true); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), v.ProfileID, "")
	agentApply(t, s, AgentCommand{Action: "session-archive", ProfileID: v.ProfileID, ID: v.Sessions[0].ID})
	original, e := historySnapshot(t.Context(), s.db)
	if e != nil {
		t.Fatal(e)
	}
	var document string
	s.db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&document)
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
	saved, e := historySnapshot(t.Context(), db)
	if e != nil || saved != original {
		t.Fatal("archive absent or changed in backup", e)
	}
	var copied string
	db.QueryRow("SELECT document FROM agent_workspace WHERE id=1").Scan(&copied)
	if copied != document {
		t.Fatal("active notes or journal totals changed in backup")
	}
}
