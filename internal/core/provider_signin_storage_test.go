package core

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignInPlanSchemaMigrationPreservesRecordsAndHolds(t *testing.T) {
	for _, phase := range []string{"idle", "paused"} {
		t.Run(phase, func(t *testing.T) {
			s := fixtureStore(t)
			v := agentApply(t, s, AgentCommand{Action: "session-create", Name: "Preserved context"})
			v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ID: v.Sessions[0].ID, Text: "Synthetic text", Limits: &AILimits{MaxInputTokens: 8000, MaxOutputTokens: 128, DeadlineSeconds: 5, BudgetMicrousd: 10000}})
			if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Key: "fased-test-schema-preserved-key", Passphrase: "disposable schema vault password"}); err != nil {
				t.Fatal(err)
			}
			doc, _, err := readAgents(t.Context(), s.db)
			if err != nil {
				t.Fatal(err)
			}
			q := &doc.AIRequests[0]
			q.State, q.DispatchBoot, q.HeldMicrousd = "unknown", s.bootID, aiRequestReservation(*q)
			raw, _ := json.Marshal(doc)
			if _, err = s.db.ExecContext(t.Context(), "UPDATE agent_workspace SET document=? WHERE id=1", string(raw)); err != nil {
				t.Fatal(err)
			}
			var before string
			var revision int64
			if err = s.db.QueryRowContext(t.Context(), "SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&before, &revision); err != nil {
				t.Fatal(err)
			}
			vault, err := os.ReadFile(s.aiVaultPath())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.ExecContext(t.Context(), "PRAGMA user_version=25"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.ExecContext(t.Context(), "UPDATE update_pause SET phase=? WHERE id=1", phase); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			next, openErr := OpenStore(s.dir)
			if phase == "paused" {
				if openErr == nil || !strings.Contains(openErr.Error(), "schema 25 recovery") {
					if next != nil {
						next.Close()
					}
					t.Fatal("paused migration was not refused", openErr)
				}
			} else {
				if openErr != nil {
					t.Fatal(openErr)
				}
				defer next.Close()
			}
			probe, err := sql.Open("sqlite", filepath.Join(s.dir, "core.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			var after string
			var afterRevision int64
			var schema int
			if err = probe.QueryRowContext(t.Context(), "SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&after, &afterRevision); err != nil {
				t.Fatal(err)
			}
			if err = probe.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&schema); err != nil {
				t.Fatal(err)
			}
			wantSchema := StateSchema
			if phase == "paused" {
				wantSchema = 25
			}
			keptVault, err := os.ReadFile(s.aiVaultPath())
			if err != nil || schema != wantSchema || before != after || revision != afterRevision || !bytes.Equal(vault, keptVault) {
				t.Fatal("migration changed records, revision, vault or recovery hold", schema, err)
			}
		})
	}
}
