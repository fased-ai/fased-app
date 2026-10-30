//go:build linux

package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStrategyEnrollmentImagePreservesVersionAndPaperLedger(t *testing.T) {
	s, p := strategyFixture(t)
	strategy := saveStrategyFixture(t, s, p)
	task := strategyTaskFixture(t, s, strategy, 2)
	strategyApply(t, s, AgentCommand{Action: "task-run", ID: task.ID})
	strategyApply(t, s, AgentCommand{Action: "task-pause", ID: task.ID})
	before, rev, e := readAgents(t.Context(), s.db)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(before)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	image, lease, e := ExportEnrollmentImage(t.Context(), s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Close()
	stage := filepath.Join(t.TempDir(), "stage")
	if e = os.Mkdir(stage, 0700); e != nil {
		t.Fatal(e)
	}
	if e = ImportEnrollmentImage(t.Context(), stage, image); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(stage)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	after, got, e := readAgents(t.Context(), restored.db)
	data, _ := json.Marshal(after)
	if e != nil || got != rev || string(data) != string(raw) || restored.instanceID != s.instanceID {
		t.Fatal("metadata image changed strategy records", e)
	}
	if after.Tasks[0].State != "paused" || after.Strategies[0].Versions[0].SpentMicros != 12_500_000 {
		t.Fatal("paper allocation/approval changed")
	}
}
