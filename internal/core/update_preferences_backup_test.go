//go:build linux

package core

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestUpdatePreferencesIncludedInPrivateMetadataBackup(t *testing.T) {
	s := fixtureStore(t)
	c := candidateFixture()
	v, e := s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Revision: 0, Action: "propose", Candidate: &c})
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
	var document string
	if e = db.QueryRow("SELECT setup FROM instance WHERE id=1").Scan(&document); e != nil {
		t.Fatal(e)
	}
	var setup Setup
	if e = json.Unmarshal([]byte(document), &setup); e != nil || setup.Updates == nil || setup.Updates.Proposals[0].Digest != v.Proposals[0].Digest {
		t.Fatal("backup lost preferences", e)
	}
	var guards int
	if e = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='preserve_update_preferences'").Scan(&guards); e != nil || guards != 1 {
		t.Fatal("backup lost preservation guard", e)
	}
}
