//go:build linux

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestEnrollmentImagePreservesSameInstance(t *testing.T) {
	s := fixtureStore(t)
	claimFixture(t, s)
	agentApply(t, s, AgentCommand{Action: "session-create", Name: "Enrollment context"})
	if _, err := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Key: "fased-test-enrollment-only-key", Passphrase: "disposable enrollment vault password"}); err != nil {
		t.Fatal(err)
	}
	vault, err := os.ReadFile(s.aiVaultPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc string
	var rev int64
	if err = s.db.QueryRowContext(t.Context(), "SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&doc, &rev); err != nil {
		t.Fatal(err)
	}
	id := s.instanceID
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	image, lock, err := ExportEnrollmentImage(t.Context(), s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if image.InstanceID != id || image.Schema != StateSchema || ValidateEnrollmentImage(image) != nil {
		t.Fatal("identity or current schema lost")
	}
	stage := filepath.Join(fixtureDir(t), "stage")
	if err = os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err = ImportEnrollmentImage(t.Context(), stage, image); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(stage, "ai-credentials.json")); err != nil || !bytes.Equal(got, vault) {
		t.Fatal("encrypted vault bytes changed", err)
	}
	next, err := OpenStore(stage)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	var gotDoc string
	var gotRev int64
	if err = next.db.QueryRowContext(t.Context(), "SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&gotDoc, &gotRev); err != nil || gotDoc != doc || gotRev != rev || next.instanceID != id {
		t.Fatal("app records changed", err)
	}
}

func enrollmentFixtureImage(t *testing.T) EnrollmentImage {
	t.Helper()
	s := fixtureStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	image, lock, err := ExportEnrollmentImage(t.Context(), s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	return image
}

func enrollmentStage(t *testing.T) string {
	t.Helper()
	stage := filepath.Join(fixtureDir(t), "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	return stage
}

func TestEnrollmentImageLockAndRefusal(t *testing.T) {
	s := fixtureStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	busy, err := LockInstance(filepath.Join(s.dir, "instance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if image, lock, err := ExportEnrollmentImage(t.Context(), s.dir); err == nil || lock != nil || len(image.Files) != 0 {
		t.Fatal("running instance exported")
	}
	if err = busy.Close(); err != nil {
		t.Fatal(err)
	}
	_, held, err := ExportEnrollmentImage(t.Context(), s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := LockInstance(filepath.Join(s.dir, "instance.lock")); err == nil {
		other.Close()
		t.Fatal("export released its source lock")
	}
	if err = held.Close(); err != nil {
		t.Fatal(err)
	}
	if err = held.Close(); err != nil {
		t.Fatal("closer was not idempotent", err)
	}
	if next, err := LockInstance(filepath.Join(s.dir, "instance.lock")); err != nil {
		t.Fatal("closer retained the lock", err)
	} else {
		next.Close()
	}
}

func TestEnrollmentImageWALCiphertextAndHolds(t *testing.T) {
	s := fixtureStore(t)
	claimFixture(t, s)
	v := agentApply(t, s, AgentCommand{Action: "session-create", Name: "Retained enrollment"})
	v = agentApply(t, s, AgentCommand{Action: "ai-prepare", ID: v.Sessions[0].ID, Text: "Synthetic uncertain request", Limits: &AILimits{MaxInputTokens: 8000, MaxOutputTokens: 128, DeadlineSeconds: 5, BudgetMicrousd: 10000}})
	doc, _, err := readAgents(t.Context(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	q := &doc.AIRequests[0]
	q.State, q.DispatchBoot, q.HeldMicrousd = "unknown", s.bootID, aiRequestReservation(*q)
	raw, _ := json.Marshal(doc)
	if _, err = s.db.ExecContext(t.Context(), "UPDATE agent_workspace SET document=?,revision=revision+1 WHERE id=1", string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Key: "fased-test-enrollment-wal-key", Passphrase: "disposable enrollment vault password"}); err != nil {
		t.Fatal(err)
	}
	rpc := rpcVault{Version: 2, Ref: strings.Repeat("r", 43), Binding: strings.Repeat("a", 64), Salt: bytes.Repeat([]byte{1}, 16), Nonce: bytes.Repeat([]byte{2}, 12), Ciphertext: bytes.Repeat([]byte{3}, 32)}
	rpcRaw, _ := json.Marshal(rpc)
	rpcPath := "rpc-credential-" + strings.Repeat("a", 32) + ".json"
	if err = os.WriteFile(filepath.Join(s.dir, rpcPath), rpcRaw, 0600); err != nil {
		t.Fatal(err)
	}
	recovery := filepath.Join("lifecycle", update.NewProbe())
	if err = os.MkdirAll(filepath.Join(s.dir, recovery), 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"request.json": []byte(`{"disposable":"retained recovery request"}`), "metadata.sqlite.pending": []byte("exact recovery prefix"), "backup.lock": {}} {
		if err = os.WriteFile(filepath.Join(s.dir, recovery, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"endpoint.json", "cli-session.json", "claim-code.json"} {
		if err = os.WriteFile(filepath.Join(s.dir, name), []byte(`{"transient":"not enrolled"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := map[string][]byte{}
	for _, name := range []string{"core.sqlite", "core.sqlite-wal", "core.sqlite-shm", "ai-credentials.json"} {
		before[name], err = os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(before["core.sqlite-wal"]) == 0 {
		t.Fatal("WAL fixture was not exercised")
	}
	// The fixture keeps SQLite open to retain a real uncheckpointed WAL. No
	// engine owns instance.lock and no writer runs during this cold export.
	image, lock, err := ExportEnrollmentImage(t.Context(), s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	for name, data := range before {
		if after, err := os.ReadFile(filepath.Join(s.dir, name)); err != nil || !bytes.Equal(after, data) {
			t.Fatal("export mutated original bytes", name, err)
		}
	}
	found := map[string][]byte{}
	for _, f := range image.Files {
		found[f.Path] = f.Data
	}
	for _, name := range []string{"endpoint.json", "cli-session.json", "claim-code.json", "instance.lock", "core.sqlite-wal", "core.sqlite-shm", "core.sock"} {
		if _, ok := found[name]; ok {
			t.Fatal("transient state copied", name)
		}
	}
	if !bytes.Equal(found["ai-credentials.json"], before["ai-credentials.json"]) || !bytes.Equal(found[rpcPath], rpcRaw) || !bytes.Equal(found[filepath.ToSlash(filepath.Join(recovery, "metadata.sqlite.pending"))], []byte("exact recovery prefix")) {
		t.Fatal("ciphertext or recovery prefix changed")
	}
	stage := enrollmentStage(t)
	if err = ImportEnrollmentImage(t.Context(), stage, image); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStore(stage)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	got, _, err := readAgents(t.Context(), next.db)
	if err != nil || len(got.AIRequests) != 1 || got.AIRequests[0].State != "unknown" || got.AIRequests[0].HeldMicrousd != q.HeldMicrousd || !aiHasUsageHold(got.AIRequests[0]) {
		t.Fatal("uncertain usage hold changed", err)
	}
	if len(got.Sessions) != len(doc.Sessions) || next.instanceID != s.instanceID {
		t.Fatal("WAL records or identity lost")
	}
}

func TestEnrollmentImageRejectsUnsafeSource(t *testing.T) {
	for _, mode := range []string{"unknown", "signer-directory", "symlink", "hardlink", "future", "older", "paused", "damaged"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			switch mode {
			case "future":
				_, _ = s.db.ExecContext(t.Context(), "PRAGMA user_version=30")
			case "older":
				_, _ = s.db.ExecContext(t.Context(), "PRAGMA user_version=28")
			case "paused":
				pausedBackup(t, s)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "unknown":
				if err := os.WriteFile(filepath.Join(s.dir, "unclassified.json"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "signer-directory":
				if err := os.Mkdir(filepath.Join(s.dir, "signer"), 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(s.dir, "core.sqlite"), filepath.Join(s.dir, "ai-credentials.json")); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(filepath.Join(s.dir, "core.sqlite"), filepath.Join(fixtureDir(t), "foreign.db")); err != nil {
					t.Fatal(err)
				}
			case "damaged":
				if err := os.WriteFile(filepath.Join(s.dir, "core.sqlite"), []byte("damaged fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(filepath.Join(s.dir, "core.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			if image, lock, err := ExportEnrollmentImage(t.Context(), s.dir); err == nil || lock != nil || len(image.Files) != 0 {
				if lock != nil {
					lock.Close()
				}
				t.Fatal("unsafe source accepted")
			}
			if after, err := os.ReadFile(filepath.Join(s.dir, "core.sqlite")); err != nil || !bytes.Equal(before, after) {
				t.Fatal("refusal changed source", err)
			}
		})
	}
}

func TestEnrollmentImageStageRetryAndConflict(t *testing.T) {
	image := enrollmentFixtureImage(t)
	stage := enrollmentStage(t)
	first := image.Files[0]
	partial := first.Data[:len(first.Data)/2]
	if err := os.WriteFile(filepath.Join(stage, first.Path), partial, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ImportEnrollmentImage(t.Context(), stage, image); err != nil {
		t.Fatal("matching interrupted prefix refused", err)
	}
	if err := ImportEnrollmentImage(t.Context(), stage, image); err != nil {
		t.Fatal("completed exact retry refused", err)
	}
	if err := os.WriteFile(filepath.Join(stage, first.Path), []byte("foreign prefix"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ImportEnrollmentImage(t.Context(), stage, image); err == nil {
		t.Fatal("foreign prefix overwritten")
	}
	if data, _ := os.ReadFile(filepath.Join(stage, first.Path)); !bytes.Equal(data, []byte("foreign prefix")) {
		t.Fatal("conflicting bytes changed")
	}
	for _, mode := range []string{"unknown", "symlink", "hardlink", "broad-mode", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			dir := enrollmentStage(t)
			ctx := t.Context()
			switch mode {
			case "unknown":
				_ = os.WriteFile(filepath.Join(dir, "unknown"), []byte("preserve"), 0600)
			case "symlink":
				if err := os.Symlink(filepath.Join(stage, first.Path), filepath.Join(dir, first.Path)); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(filepath.Join(stage, first.Path), filepath.Join(dir, first.Path)); err != nil {
					t.Fatal(err)
				}
			case "broad-mode":
				_ = os.Chmod(dir, 0755)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before, _ := os.ReadDir(dir)
			if err := ImportEnrollmentImage(ctx, dir, image); err == nil {
				t.Fatal("unsafe stage accepted")
			}
			after, _ := os.ReadDir(dir)
			if len(before) != len(after) {
				t.Fatal("refused stage mutated")
			}
		})
	}
}

func TestEnrollmentImageIdentityAndPathAdmission(t *testing.T) {
	base := enrollmentFixtureImage(t)
	for _, mode := range []string{"identity", "schema", "version", "duplicate", "transient", "traversal", "credential", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			image := base
			image.Files = append([]EnrollmentFile(nil), base.Files...)
			switch mode {
			case "identity":
				image.InstanceID = strings.Repeat("x", 43)
			case "schema":
				image.Schema++
			case "version":
				image.Version++
			case "duplicate":
				image.Files = append(image.Files, image.Files[0])
			case "transient":
				image.Files = append(image.Files, EnrollmentFile{Path: "endpoint.json", Data: []byte("transient")})
			case "traversal":
				image.Files[0].Path = "../core.sqlite"
			case "credential":
				image.Files = append(image.Files, EnrollmentFile{Path: "ai-credentials.json", Data: []byte(`{"key":"unencrypted test key"}`)})
				sort.Slice(image.Files, func(i, j int) bool { return image.Files[i].Path < image.Files[j].Path })
			case "oversize":
				image.Files = append(image.Files, EnrollmentFile{Path: "ai-credentials.json", Data: make([]byte, aiVaultMaxSerializedBytes+1)})
				sort.Slice(image.Files, func(i, j int) bool { return image.Files[i].Path < image.Files[j].Path })
			}
			if err := ValidateEnrollmentImage(image); err == nil {
				t.Fatal("invalid image accepted")
			}
			stage := enrollmentStage(t)
			if err := ImportEnrollmentImage(t.Context(), stage, image); err == nil {
				t.Fatal("invalid import accepted")
			}
			entries, _ := os.ReadDir(stage)
			if len(entries) != 0 {
				t.Fatal("invalid image mutated stage")
			}
		})
	}
	if digest := EnrollmentImageDigest(base); len(digest) != 64 || digest != EnrollmentImageDigest(base) {
		t.Fatal("image digest was not deterministic")
	}
	changed := base
	changed.InstanceID = strings.Repeat("z", 43)
	if EnrollmentImageDigest(changed) == EnrollmentImageDigest(base) {
		t.Fatal("digest did not bind instance")
	}
}

func TestEnrollmentImageRejectsUnclassifiedDatabaseState(t *testing.T) {
	s := fixtureStore(t)
	if _, err := s.db.ExecContext(t.Context(), "CREATE TABLE unclassified_fixture_state(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, lock, err := ExportEnrollmentImage(t.Context(), s.dir); err == nil {
		lock.Close()
		t.Fatal("unclassified current-version database state admitted")
	}
}
