package core

import (
	"database/sql"
	"testing"
)

func TestApplicationUpdateMigrationPreservesPredecessorAndRefusesUnprotectedAuthority(t *testing.T) {
	for _, test := range []struct {
		name                            string
		schema                          int
		drop, active, paused, wantError bool
	}{
		{"predecessor", 33, true, false, false, false},
		{"older-predecessor", 32, false, false, false, false},
		{"unprotected-authority", 33, false, true, false, true},
		{"separate-branch", 34, true, false, false, true},
		{"paused-predecessor", 33, true, false, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := fixtureStore(t)
			var original string
			var revision int64
			if e := s.db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&original, &revision); e != nil {
				t.Fatal(e)
			}
			if test.drop {
				if _, e := s.db.Exec("DROP TABLE application_updates"); e != nil {
					t.Fatal(e)
				}
			}
			if test.active {
				if _, e := s.db.Exec("UPDATE application_updates SET revision=1"); e != nil {
					t.Fatal(e)
				}
			}
			if test.paused {
				if _, e := s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := s.db.Exec("PRAGMA user_version=" + map[int]string{32: "32", 33: "33", 34: "34"}[test.schema]); e != nil {
				t.Fatal(e)
			}
			dir := s.dir
			s.Close()
			next, e := OpenStore(dir)
			if (e != nil) != test.wantError {
				if next != nil {
					next.Close()
				}
				t.Fatalf("open error=%v, wanted refusal=%v", e, test.wantError)
			}
			if next != nil {
				next.Close()
			}
			db, e := sql.Open("sqlite", "file:"+dir+"/core.sqlite?mode=ro")
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var after string
			var afterRevision int64
			var schema int
			if e = db.QueryRow("SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&after, &afterRevision); e != nil {
				t.Fatal(e)
			}
			if e = db.QueryRow("PRAGMA user_version").Scan(&schema); e != nil {
				t.Fatal(e)
			}
			wantSchema := StateSchema
			if test.wantError {
				wantSchema = test.schema
			}
			if after != original || afterRevision != revision || schema != wantSchema {
				t.Fatal("migration changed originals or advanced a refused reader")
			}
		})
	}
}
