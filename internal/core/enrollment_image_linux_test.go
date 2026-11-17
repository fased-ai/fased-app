//go:build linux

package core

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestEnrollmentImagePreservesOSAndDeviceOwner(t *testing.T) {
	for _, profile := range []string{"local", "remote", "remote-password"} {
		t.Run(profile, func(t *testing.T) {
			s := fixtureStore(t)
			var token string
			var err error
			wantSchema := LocalOwnerStateSchema
			remote := profile != "local"
			if remote {
				id := tailnet.Identity{UserID: 7, NodeID: "enrollment-device", Login: "owner@example.test"}
				var code, existing string
				if profile == "remote-password" {
					existing = claimFixture(t, s)
				} else {
					claim, e := s.IssueRemoteClaim(t.Context(), id)
					if e != nil {
						t.Fatal(e)
					}
					code = claim.Code
				}
				token, err = s.remoteSession(t.Context(), id, code, existing)
				wantSchema = RemoteOwnerStateSchema
			} else {
				token, err = s.localOwnerSession(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			var salt, hash []byte
			if err = s.db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&salt, &hash); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			image, lease, err := ExportEnrollmentImage(t.Context(), s.dir)
			if err != nil {
				t.Fatal("ordinary installed owner cannot enroll", err)
			}
			defer lease.Close()
			if image.Schema != wantSchema {
				t.Fatal("owner schema rewritten", image.Schema)
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
			var gotSalt, gotHash []byte
			if err = next.db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&gotSalt, &gotHash); err != nil || !bytes.Equal(salt, gotSalt) || !bytes.Equal(hash, gotHash) || next.instanceID != s.instanceID || !next.Session(t.Context(), token) {
				t.Fatal("owner identity/authentication changed", err)
			}
			if remote {
				owner, err := next.remoteOwner(t.Context())
				if err != nil || owner.UserID != 7 || len(owner.Devices) != 1 || owner.Devices[0].NodeID != "enrollment-device" {
					t.Fatal("device ownership changed", err)
				}
			}
			image.Schema = StateSchema
			if ValidateEnrollmentImage(image) == nil {
				t.Fatal("mismatched declared owner schema accepted")
			}
		})
	}
}

func TestEnrollmentImageClassifiesEmptyUpdateChannel(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprint(populated), func(t *testing.T) {
			s := fixtureStore(t)
			id := s.instanceID
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			channel := filepath.Join(s.dir, "update-channel")
			if err := os.Mkdir(channel, 0700); err != nil {
				t.Fatal(err)
			}
			if populated {
				if err := os.WriteFile(filepath.Join(channel, "unclassified.json"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			image, lease, err := ExportEnrollmentImage(t.Context(), s.dir)
			if populated {
				if err == nil || lease != nil || len(image.Files) != 0 {
					t.Fatal("unclassified update state was silently omitted")
				}
				got, readErr := os.ReadFile(filepath.Join(channel, "unclassified.json"))
				if readErr != nil || string(got) != "preserve" {
					t.Fatal("original update state changed", readErr)
				}
				return
			}
			if err != nil {
				t.Fatal("installed updater's empty working directory rejected", err)
			}
			defer lease.Close()
			stage := enrollmentStage(t)
			if err = ImportEnrollmentImage(t.Context(), stage, image); err != nil {
				t.Fatal(err)
			}
			next, err := OpenStore(stage)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Close()
			if next.instanceID != id {
				t.Fatal("instance identity changed")
			}
		})
	}
}

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

func TestEnrollmentImageReacquisitionDetectsSourceChanges(t *testing.T) {
	s := fixtureStore(t)
	claimFixture(t, s)
	dir := s.dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	first, lock, err := ExportEnrollmentImage(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	second, lock, err := ExportEnrollmentImage(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if EnrollmentImageDigest(first) != EnrollmentImageDigest(second) {
		t.Fatal("unchanged source could not be safely reacquired")
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	agentApply(t, s, AgentCommand{Action: "session-create", Name: "changed while unlocked"})
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	changed, lock, err := ExportEnrollmentImage(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if EnrollmentImageDigest(first) == EnrollmentImageDigest(changed) {
		t.Fatal("source change was not reflected in the reacquired image")
	}
}

func TestEnrollmentImageApplicationUpdateQuiescentRowPreserved(t *testing.T) {
	for _, raw := range []string{`{"phase":"idle", "id":""}`, `{"id":"ed4e62f092e9658589ca0fcf8f538beb","phase":"current","checkedAt":1793995915,"finishedAt":1793995915}`} {
		t.Run(raw, func(t *testing.T) {
			s := fixtureStore(t)
			prefs, e := s.UpdatePreferences(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			policy := prefs.Policy
			policy.Notify = false
			policy.Mode = "manual"
			if _, e = s.ChangeUpdatePreferences(t.Context(), UpdatePreferencesRequest{Action: "policy", Revision: prefs.Revision, Policy: &policy}); e != nil {
				t.Fatal(e)
			}
			if _, e = s.db.Exec("UPDATE application_updates SET revision=9,document=? WHERE id=1", raw); e != nil {
				t.Fatal(e)
			}
			var setup string
			if e = s.db.QueryRow("SELECT setup FROM instance WHERE id=1").Scan(&setup); e != nil {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			image, lease, e := ExportEnrollmentImage(t.Context(), s.dir)
			if e != nil {
				t.Fatal(e)
			}
			defer lease.Close()
			stage := enrollmentStage(t)
			if e = ImportEnrollmentImage(t.Context(), stage, image); e != nil {
				t.Fatal(e)
			}
			next, e := OpenStore(stage)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			var got, gotSetup string
			var revision int64
			if e = next.db.QueryRow("SELECT document,revision FROM application_updates WHERE id=1").Scan(&got, &revision); e != nil || got != raw || revision != 9 {
				t.Fatal("idle row rewritten", got, revision, e)
			}
			if e = next.db.QueryRow("SELECT setup FROM instance WHERE id=1").Scan(&gotSetup); e != nil || gotSetup != setup {
				t.Fatal("update preferences changed", e)
			}
		})
	}
}

func TestEnrollmentImageApplicationUpdateSettledHistory(t *testing.T) {
	for _, phase := range []string{"completed", "rolled-back"} {
		t.Run(phase, func(t *testing.T) {
			s := fixtureStore(t)
			controller := fixtureUpdateController()
			if e := s.BindApplicationMaintenance(controller.Check, func(context.Context, string) error { return nil }); e != nil {
				t.Fatal(e)
			}
			updateCommandFixture(t, s, "check")
			updateTickFixture(t, s)
			updateCommandFixture(t, s, "apply")
			updateTickFixture(t, s)
			view, e := s.ApplicationUpdates(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			op := view.Operation
			leaseID := update.NewProbe()
			if _, e = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: op.ID, Lease: leaseID}); e != nil {
				t.Fatal(e)
			}
			prepared, e := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: op.ID, Lease: leaseID})
			if e != nil || prepared.Checkpoint == nil {
				t.Fatal(e)
			}
			engine := op.Release.EngineSHA256
			if phase == "rolled-back" {
				engine = prepared.Checkpoint.PreviousEngineSHA256
			}
			proof := ApplicationUpdateReceipt{ID: op.ID, Sequence: op.Release.Sequence, ManifestSHA256: op.Release.ManifestSHA256, EngineSHA256: engine, Phase: phase}
			if _, e = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "finish", ID: op.ID, Lease: leaseID, Receipt: &proof}); e != nil {
				t.Fatal(e)
			}
			// Current work may change after a settled update. Its sealed original
			// backup must be preserved independently, never compared to or restored
			// over this newer workspace during enrollment.
			agentApply(t, s, AgentCommand{Action: "session-create", Name: "After settled update"})
			var raw string
			var revision int64
			if e = s.db.QueryRow("SELECT document,revision FROM application_updates WHERE id=1").Scan(&raw, &revision); e != nil {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			image, lease, e := ExportEnrollmentImage(t.Context(), s.dir)
			if e != nil {
				t.Fatal("settled update prevented enrollment", e)
			}
			defer lease.Close()
			stage := enrollmentStage(t)
			if e = ImportEnrollmentImage(t.Context(), stage, image); e != nil {
				t.Fatal(e)
			}
			next, e := OpenStore(stage)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			var got string
			var gotRevision int64
			if e = next.db.QueryRow("SELECT document,revision FROM application_updates WHERE id=1").Scan(&got, &gotRevision); e != nil || raw != got || revision != gotRevision {
				t.Fatal("settled receipt/floor rewritten", e)
			}
			for _, file := range image.Files {
				if strings.HasPrefix(file.Path, "lifecycle/") {
					b, e := os.ReadFile(filepath.Join(stage, file.Path))
					if e != nil || !bytes.Equal(b, file.Data) {
						t.Fatal("update originals altered", file.Path, e)
					}
				}
			}
			for _, tamper := range []string{"checkpoint", "backup", "missing-backup"} {
				bad := image
				bad.Files = append([]EnrollmentFile{}, image.Files...)
				for i, f := range bad.Files {
					if tamper == "checkpoint" && f.Path == "lifecycle/"+op.ID+"/application.json" {
						bad.Files[i].Data = []byte(`{"version":1}`)
						break
					}
					if strings.HasSuffix(f.Path, prepared.Checkpoint.Snapshot.File) && tamper != "checkpoint" {
						if tamper == "missing-backup" {
							bad.Files = append(bad.Files[:i], bad.Files[i+1:]...)
						} else {
							bad.Files[i].Data = []byte("changed original")
						}
						break
					}
				}
				if e = ValidateEnrollmentImage(bad); e == nil {
					t.Fatal("tampered terminal history accepted", tamper)
				}
			}
		})
	}
}

func TestEnrollmentImageApplicationUpdateAuthorityRefused(t *testing.T) {
	for _, mode := range []string{"queued", "applying", "recovery", "checking", "available", "current", "current-lease", "current-unfinished", "unknown", "idle-lease", "unknown-field", "negative-revision", "extra-row", "schema34"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			o := ApplicationUpdateOperation{ID: update.NewProbe(), Phase: mode, Release: &ApplicationRelease{Version: "0.0.0-fixture", Sequence: 7, ManifestSHA256: strings.Repeat("a", 64), EngineSHA256: strings.Repeat("b", 64), Target: update.Target{OS: "linux", Arch: runtime.GOARCH}}}
			if mode == "idle-lease" {
				o = ApplicationUpdateOperation{Phase: "idle", OwnerLease: update.NewProbe()}
			}
			if mode == "current-lease" || mode == "current-unfinished" {
				o = ApplicationUpdateOperation{ID: update.NewProbe(), Phase: "current", CheckedAt: 1793995915}
				if mode == "current-lease" {
					o.FinishedAt, o.OwnerLease = o.CheckedAt, update.NewProbe()
				}
			}
			raw, _ := json.Marshal(o)
			if mode == "unknown-field" {
				raw = []byte(`{"phase":"idle","id":"","unclassified":true}`)
			}
			if mode == "negative-revision" || mode == "extra-row" || mode == "schema34" {
				raw = []byte(`{"phase":"idle","id":""}`)
			}
			if _, e := s.db.Exec("UPDATE application_updates SET document=? WHERE id=1", string(raw)); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "negative-revision":
				if _, e := s.db.Exec("UPDATE application_updates SET revision=-1"); e != nil {
					t.Fatal(e)
				}
			case "extra-row":
				if _, e := s.db.Exec(`ALTER TABLE application_updates RENAME TO old_updates; CREATE TABLE application_updates(id INTEGER,revision INTEGER,document TEXT); INSERT INTO application_updates SELECT * FROM old_updates; INSERT INTO application_updates VALUES(2,0,'{"phase":"idle","id":""}'); DROP TABLE old_updates`); e != nil {
					t.Fatal(e)
				}
			case "schema34":
				if _, e := s.db.Exec("PRAGMA user_version=34"); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			before, e := os.ReadFile(filepath.Join(s.dir, "core.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			if image, lease, e := ExportEnrollmentImage(t.Context(), s.dir); e == nil || lease != nil || len(image.Files) != 0 {
				if lease != nil {
					lease.Close()
				}
				t.Fatal("unsettled or unknown update authority enrolled", mode, e)
			}
			after, e := os.ReadFile(filepath.Join(s.dir, "core.sqlite"))
			if e != nil || !bytes.Equal(before, after) {
				t.Fatal("refusal changed original database", e)
			}
		})
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
				_, _ = s.db.ExecContext(t.Context(), fmt.Sprintf("PRAGMA user_version=%d", StateSchema+1))
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

func TestEnrollmentImageRetainsOnlyReadyOwnerHostPreparation(t *testing.T) {
	s := fixtureStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	receipt := []byte(fmt.Sprintf(`{"schema":1,"stage":".fased-app-bwrap-%d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.pending","phase":"ready"}`, os.Getuid()))
	if err := os.WriteFile(filepath.Join(s.dir, "host-preparation.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "host-preparation.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	image, lease, err := ExportEnrollmentImage(t.Context(), s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	for _, file := range image.Files {
		if strings.HasPrefix(file.Path, "host-preparation") {
			t.Fatal("owner host receipt transferred to app UID")
		}
	}
	after, err := os.ReadFile(filepath.Join(s.dir, "host-preparation.json"))
	if err != nil || !bytes.Equal(after, receipt) {
		t.Fatal("owner host receipt changed", err)
	}
}

func TestEnrollmentImageRejectsUnfinishedOrBusyHostPreparation(t *testing.T) {
	for _, mode := range []string{"staging", "published", "foreign-owner", "unknown-schema", "extra-field", "receipt-only", "lock-only", "busy"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			receipt := map[string]any{"schema": 1, "stage": fmt.Sprintf(".fased-app-bwrap-%d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.pending", os.Getuid()), "phase": "ready"}
			switch mode {
			case "staging", "published":
				receipt["phase"] = mode
			case "foreign-owner":
				receipt["stage"] = ".fased-app-bwrap-0-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.pending"
			case "unknown-schema":
				receipt["schema"] = 2
			case "extra-field":
				receipt["grant"] = true
			}
			data, _ := json.Marshal(receipt)
			receiptPath := filepath.Join(s.dir, "host-preparation.json")
			lockPath := filepath.Join(s.dir, "host-preparation.lock")
			if mode != "lock-only" {
				if err := os.WriteFile(receiptPath, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "receipt-only" {
				if err := os.WriteFile(lockPath, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "busy" {
				guard, err := LockInstance(lockPath)
				if err != nil {
					t.Fatal(err)
				}
				defer guard.Close()
			}
			image, lease, err := ExportEnrollmentImage(t.Context(), s.dir)
			if err == nil || lease != nil || len(image.Files) != 0 {
				if lease != nil {
					lease.Close()
				}
				t.Fatal("unfinished/foreign preparation accepted", mode)
			}
			if mode != "lock-only" {
				after, e := os.ReadFile(receiptPath)
				if e != nil || !bytes.Equal(after, data) {
					t.Fatal("original receipt changed", e)
				}
			}
		})
	}
}

func TestEnrollmentImageKeepsHostPreparationLeaseUntilFence(t *testing.T) {
	s := fixtureStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	data := []byte(fmt.Sprintf(`{"schema":1,"stage":".fased-app-bwrap-%d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.pending","phase":"ready"}`, os.Getuid()))
	if err := os.WriteFile(filepath.Join(s.dir, "host-preparation.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, "host-preparation.lock")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, lease, err := ExportEnrollmentImage(t.Context(), s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if guard, e := LockInstance(path); e == nil {
		guard.Close()
		lease.Close()
		t.Fatal("host preparation raced source image lease")
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	guard, err := LockInstance(path)
	if err != nil {
		t.Fatal("host preparation lease leaked", err)
	}
	guard.Close()
}

func TestEnrollmentImageRejectsChangedHostPreparationLock(t *testing.T) {
	for _, mode := range []string{"created-after-lock", "replaced-lock"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.dir, "host-preparation.lock")
			if mode == "replaced-lock" {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			lease, err := enrollmentSource(s.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			if mode == "replaced-lock" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			data := []byte(fmt.Sprintf(`{"schema":1,"stage":".fased-app-bwrap-%d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.pending","phase":"ready"}`, os.Getuid()))
			if err := os.WriteFile(filepath.Join(s.dir, "host-preparation.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			entries, err := enrollmentScan(t.Context(), int(lease.root.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			if err := enrollmentHostPreparation(lease, entries); err == nil {
				t.Fatal("unheld host preparation lock admitted")
			}
		})
	}
}

func TestEnrollmentImagePreservesLargeEncryptedVault(t *testing.T) {
	s := fixtureStore(t)
	credential := providerSignInCredential{Version: 1, Issuer: signInIssuer, Subject: "disposable-enrollment-user", ClientID: "oaiapp_disposable_enrollment", AccessToken: strings.Repeat("a", 4096), RefreshToken: strings.Repeat("r", 4096), IDToken: strings.Repeat("i", 4000), Scope: signInScope, ExpiresAt: s.now().Add(time.Hour).Unix()}
	raw, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parseProviderSignInCredential(raw); !ok {
		t.Fatal("invalid disposable credential fixture")
	}
	salt := bytes.Repeat([]byte{0x42}, 16)
	key := passwordHash(testVaultPassword, salt)
	block, err := aes.NewCipher(key)
	clear(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	col := aiVaultCollection{Version: 3}
	for i := 0; i < 8; i++ {
		ref, err := randomToken()
		if err != nil {
			t.Fatal(err)
		}
		env := aiVaultEnvelope{Version: 5, Provider: "openai", Mode: "openai-signin", TestOnly: true, Ref: ref, Name: "Disposable enrollment account", Salt: append([]byte(nil), salt...), Nonce: make([]byte, 12)}
		env.Nonce[0] = byte(i)
		env.Ciphertext = gcm.Seal(nil, env.Nonce, raw, s.aiVaultAAD(env))
		col.Accounts = append(col.Accounts, env)
	}
	col.Default = col.Accounts[0].Ref
	if err = s.writeAIVaultCollection(col); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(s.aiVaultPath())
	if err != nil || len(original) <= update.MaxDocument {
		t.Fatal("large vault fixture missing", len(original), err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	image, lease, err := ExportEnrollmentImage(t.Context(), s.dir)
	if err != nil {
		t.Fatal("valid large vault rejected", err)
	}
	defer lease.Close()
	stage := enrollmentStage(t)
	if err = ImportEnrollmentImage(t.Context(), stage, image); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(stage, "ai-credentials.json"))
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("vault bytes changed", err)
	}
	restored, err := OpenStore(stage)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	recovered, err := restored.readAIVaultCollection()
	if err != nil || len(recovered.Accounts) != 8 {
		t.Fatal("account collection changed", err)
	}
	for _, env := range recovered.Accounts {
		plain, err := restored.openAIVault(env, testVaultPassword)
		if err != nil || !bytes.Equal(plain, raw) {
			t.Fatal("encrypted account failed after enrollment", err)
		}
		clear(plain)
	}
	if enrollmentCredential("ai-credentials.json", bytes.Repeat([]byte(" "), aiVaultMaxSerializedBytes+1)) == nil {
		t.Fatal("vault size bound ignored")
	}
}

func TestEnrollmentImageRejectsCorruptDeviceOwner(t *testing.T) {
	for _, mode := range []string{"owner-json", "foreign-device", "duplicate-device", "invite", "orphan-session", "foreign-session", "marker", "owner-view", "session-view"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureStore(t)
			id := tailnet.Identity{UserID: 7, NodeID: "owner-device", Login: "owner@example.test"}
			code, err := s.IssueRemoteClaim(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			token, err := s.remoteSession(t.Context(), id, code.Code, "")
			if err != nil {
				t.Fatal(err)
			}
			owner, err := s.remoteOwner(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "owner-json":
				_, err = s.db.Exec("UPDATE remote_owner SET document='{}'")
			case "foreign-device", "duplicate-device", "invite":
				if mode == "foreign-device" {
					owner.Devices[0].UserID = 99
				}
				if mode == "duplicate-device" {
					owner.Devices = append(owner.Devices, owner.Devices[0])
				}
				if mode == "invite" {
					owner.InviteHash = "not-a-token"
					owner.InviteExpires = 1
				}
				raw, _ := json.Marshal(owner)
				_, err = s.db.Exec("UPDATE remote_owner SET document=?", string(raw))
			case "foreign-session":
				_, err = s.db.Exec("UPDATE remote_sessions SET node_id='unapproved-device'")
			case "marker":
				_, err = s.db.Exec("UPDATE instance SET password_hash=?", []byte("invalid"))
			case "owner-view":
				raw, _ := json.Marshal(owner)
				_, err = s.db.Exec("DROP TABLE remote_owner; CREATE VIEW remote_owner AS SELECT 1 AS id,'" + strings.ReplaceAll(string(raw), "'", "''") + "' AS document")
			case "session-view":
				_, err = s.db.Exec("DROP TABLE remote_sessions; CREATE VIEW remote_sessions AS SELECT '" + tokenHash(token) + "' AS token_hash,'owner-device' AS node_id")
			case "orphan-session":
				c, e := s.db.Conn(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				_, err = c.ExecContext(t.Context(), "PRAGMA foreign_keys=OFF")
				if err == nil {
					_, err = c.ExecContext(t.Context(), "DELETE FROM sessions WHERE token_hash=?", tokenHash(token))
				}
				c.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			if _, lease, err := ExportEnrollmentImage(t.Context(), s.dir); err == nil {
				lease.Close()
				t.Fatal("corrupt device owner accepted", mode)
			}
		})
	}
}
