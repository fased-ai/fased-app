//go:build linux

package core

import (
	"database/sql"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestNewsWatchProtectedMetadataBackupKeepsCursorAndResults(t *testing.T) {
	s := newsTestStore(t)
	v := newsAdd(t, s, "trading-analyst")
	v = agentApply(t, s, watchReview(t, s, v, 3))
	agentApply(t, s, AgentCommand{Action: "task-run", ProfileID: v.ProfileID, ID: v.Tasks[0].ID})
	finishWatch(t, s, watchBody(s.now().Unix()), new(atomic.Int32))
	var original string
	if err := s.db.QueryRow("SELECT document || ':' || revision FROM agent_workspace WHERE id=1").Scan(&original); err != nil {
		t.Fatal(err)
	}
	proof := pausedBackup(t, s)
	if _, err := s.BackupMetadata(t.Context(), proof); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(s.dir, "lifecycle", proof.IntentID, "metadata.sqlite")+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var saved string
	if err = db.QueryRow("SELECT document || ':' || revision FROM agent_workspace WHERE id=1").Scan(&saved); err != nil || saved != original {
		t.Fatal("watch/cursor/results changed in backup", err)
	}
	d, _, err := readAgents(t.Context(), db)
	if err != nil || len(d.Tasks[0].Watch.Seen) != 1 || len(d.Runs[0].Watch.NewIDs) != 1 {
		t.Fatal("backup lost typed policy or progress", err)
	}
}
