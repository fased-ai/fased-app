package core

import (
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"

	"os"
	"testing"
)

func researchRaw(t *testing.T) string {
	t.Helper()
	b, e := os.ReadFile("testdata/wen-research-strategy.json")
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestWENResearchPersistenceIsolationRevisionPause(t *testing.T) {
	s := fixtureStore(t)
	claimFixture(t, s)
	ctx := t.Context()
	a, e := s.UpdateSnapshot(ctx, update.NewProbe())
	if e != nil {
		t.Fatal(e)
	}
	raw := researchRaw(t)
	v, e := s.ChangeWENResearch(ctx, WENResearchCommand{Profile: "wen-operator", Action: "save", Raw: raw})
	if e != nil {
		t.Fatal(e)
	}
	if v.Revision != 1 || len(v.Versions) != 1 || v.Versions[0].Raw != raw {
		t.Fatal(v)
	}
	b, e := s.UpdateSnapshot(ctx, update.NewProbe())
	if e != nil || a.State.Anchor == b.State.Anchor {
		t.Fatal("snapshot missed research", e)
	}
	if _, e = s.ChangeWENResearch(ctx, WENResearchCommand{Profile: "wen-operator", Action: "save", Raw: raw}); !errors.Is(e, ErrConflict) {
		t.Fatal("revision", e)
	}
	other, e := s.WENResearch(ctx, "news-researcher")
	if e != nil || len(other.Versions) != 0 {
		t.Fatal("profile leak", e)
	}
	dup, e := s.ChangeWENResearch(ctx, WENResearchCommand{Revision: 1, Profile: "wen-operator", Action: "save", Raw: raw})
	if e != nil || dup.Revision != 1 {
		t.Fatal("duplicate", e)
	}
	c := pauseCommand(t, s, 1)
	if _, e = s.UpdatePause(ctx, c); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeWENResearch(ctx, WENResearchCommand{Revision: 1, Profile: "wen-operator", Action: "save", Raw: raw}); !errors.Is(e, ErrCorePaused) {
		t.Fatal("pause", e)
	}
	if _, e = s.UpdatePause(ctx, update.CorePauseCommand{Action: "release", ProbeID: c.ProbeID, Aborted: aborted(c)}); e != nil {
		t.Fatal(e)
	}
	dir := s.dir
	s.Close()
	n, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	got, e := n.WENResearch(ctx, "wen-operator")
	if e != nil || got.Versions[0].Digest != v.Versions[0].Digest || got.Versions[0].Raw != raw {
		t.Fatal("restart", e)
	}
}
func TestWENResearchHTTPAuthority(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(WENResearchCommand{Profile: "wen-operator", Action: "save", Raw: researchRaw(t)})
	for _, tc := range []struct {
		method, path, body, auth, csrf string
		status                         int
	}{{"GET", "?profile=wen-operator", "", "", "", 401}, {"GET", "?profile=wen-operator", "", token, "", 200}, {"POST", "", string(raw), token, "", 403}, {"POST", "?bad=x", string(raw), token, csrfToken(token), 400}, {"POST", "", string(raw), token, csrfToken(token), 200}, {"POST", "", string(raw), token, csrfToken(token), 409}, {"DELETE", "", "", token, csrfToken(token), 405}} {
		w := apiRequest(h, tc.method, "/api/v1/wen/research"+tc.path, tc.body, tc.auth, "http://127.0.0.1:7331", tc.csrf)
		if w.Code != tc.status {
			t.Fatalf("%s got %d want %d: %s", tc.method, w.Code, tc.status, w.Body.String())
		}
	}
}
func TestWENResearchSchema10MigrationAndNewerRefusal(t *testing.T) {
	s := fixtureStore(t)
	dir := s.dir
	s.db.Exec("DROP TABLE wen_research; PRAGMA user_version=10;")
	s.Close()
	n, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	var schema int
	n.db.QueryRow("PRAGMA user_version").Scan(&schema)
	if schema != StateSchema {
		t.Fatal(schema)
	}
	n.db.Exec("PRAGMA user_version=13")
	n.Close()
	if old, e := OpenStore(dir); e == nil {
		old.Close()
		t.Fatal("newer reader boundary")
	}
}

func TestWENResearchHistoryImportAndRefusals(t *testing.T) {
	s := fixtureStore(t)
	ctx := t.Context()
	raw, e := os.ReadFile("../wenstrategy/testdata/history-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var vectors []struct {
		Name, Raw string
		Valid     bool
	}
	if e = json.Unmarshal(raw, &vectors); e != nil {
		t.Fatal(e)
	}
	rev := int64(0)
	for _, v := range vectors {
		got, e := s.ChangeWENResearch(ctx, WENResearchCommand{Revision: rev, Profile: "wen-operator", Action: "history", Raw: v.Raw})
		if v.Valid {
			if e != nil {
				t.Fatal(e)
			}
			rev = got.Revision
		} else if e == nil {
			t.Fatal("invalid history accepted", v.Name)
		}
	}
	got, e := s.WENResearch(ctx, "wen-operator")
	if e != nil || len(got.Bundles) != 1 || got.Revision != rev {
		t.Fatal("invalid import mutated history", e)
	}
	dir := s.dir
	s.Close()
	n, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer n.Close()
	got, e = n.WENResearch(ctx, "wen-operator")
	if e != nil || len(got.Bundles) != 1 || got.Bundles[0].Rows[0].Committed != "100" {
		t.Fatal("history restart", e)
	}
}
