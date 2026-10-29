//go:build linux

package core

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestTaskAnalysisProtectedBackupKeepsPoliciesResultsAndUsageHolds(t *testing.T) {
	s, v := analysisWatchFixture(t, "optional")
	v = fetchAnalysisWatch(t, s, v, watchBody(s.now().Unix()), true)
	rewriteReminderFixture(t, s, func(d *agentDocument) {
		d.AIRequests[0].State = "unknown"
		d.AIRequests[0].Failure = "Controlled unknown usage"
	})
	// Complete the deterministic branch, retaining the original uncertain usage.
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	d, revision, err := readAgents(t.Context(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if err = settleTaskAI(&d, d.Tasks[0].ID, s.now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err = saveAgents(t.Context(), tx, d, revision); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	original := retainedRevisionState(t, s)
	proof := pausedBackup(t, s)
	if _, err = s.BackupMetadata(t.Context(), proof); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(s.dir, "lifecycle", proof.IntentID, "metadata.sqlite")+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var copied string
	if err = db.QueryRow("SELECT document || ':' || revision FROM agent_workspace WHERE id=1").Scan(&copied); err != nil || copied != original[0] {
		t.Fatal("backup changed active policy/result", err)
	}
	saved, _, err := readAgents(t.Context(), db)
	if err != nil || saved.Tasks[0].AI.Analysis.Failure != "optional" || saved.Runs[0].Analysis.State != "skipped" || saved.AIRequests[0].HeldMicrousd == 0 || len(saved.Tasks[0].Watch.Seen) != 1 {
		t.Fatal("backup lost hold/cursor/analysis policy", err)
	}
}
