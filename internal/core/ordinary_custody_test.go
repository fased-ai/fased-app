//go:build linux

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOrdinaryCustodyAccountRPCRestartAndRecovery(t *testing.T) {
	ctx := context.Background()
	keydir := t.TempDir()
	if err := os.Chmod(keydir, 0700); err != nil {
		t.Fatal(err)
	}
	password := []byte("disposable ordinary custody password")
	v, err := signer.OpenControlledOwnerCustody(keydir, "00112233445566778899aabbccddeeff", "devnet")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if err = v.Create(password); err != nil {
		t.Fatal(err)
	}
	dir := fixtureDir(t)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	claimFixture(t, s)
	original, err := s.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ref := *v.Status().Account
	command := AccountSetupCommand{Revision: original.Record.Revision, Action: "attach", Name: "Local wallet", Network: "devnet", Reference: &ref}
	if _, err = s.SetupAccount(ctx, command); err == nil {
		t.Fatal("unverified reference admitted")
	}
	bridge := func(_ context.Context, r signer.Request) (signer.Response, error) {
		if r.Action != "status" {
			t.Fatal("key administration reached core")
		}
		return signer.Response{Status: v.Status()}, nil
	}
	s.fixture = bridge
	badRef := ref
	badRef.Version = 2
	command.Reference = &badRef
	if _, err = s.SetupAccount(ctx, command); err == nil {
		t.Fatal("public reference relabeled custody class")
	}
	command.Reference = &ref
	current, err := s.SetupAccount(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Record.Accounts) != 1 || current.Record.Accounts[0].Version != 4 || current.Record.Accounts[0].Mode != "managed" || current.Setup.CustodyClass != signer.OrdinaryCustodyMode || !current.Setup.AdmissionHeld || current.AccountStates[current.Record.Accounts[0].ID] != "current-custody-held" {
		t.Fatalf("unsafe public projection: %+v", current)
	}
	account := current.Record.Accounts[0]
	rpc := RPCConfigInput{Revision: current.Record.Revision, AccountID: account.ID, Network: "devnet", Endpoint: "https://devnet.helius-rpc.com"}
	if _, err = s.ConfigureAccountRPC(ctx, rpc); err == nil {
		t.Fatal("ordinary RPC dispatch enabled")
	}
	s.custodyOwnerSupported = true // Authenticated controlled installed binding; no live RPC.
	current, err = s.ConfigureAccountRPC(ctx, rpc)
	if err != nil {
		t.Fatal(err)
	}
	if !validProviderRPC(current.Record.Accounts[0]) {
		t.Fatal("ordinary account RPC identity lost")
	}
	credential, err := s.RPCCredential(ctx, RPCCredentialCommand{AccountID: account.ID, Revision: current.Record.Revision, Action: "save", Key: "ordinary-controlled-rpc-key-12345678", Passphrase: "disposable RPC password"})
	if err != nil || !credential.Saved || !credential.Locked || credential.LiveRPC {
		t.Fatal("ordinary RPC credential not safely saved", err)
	}
	if s.rpcAdmission {
		t.Fatal("configuration enabled a live transport")
	}
	if _, err = s.RPCCredential(ctx, RPCCredentialCommand{AccountID: account.ID, Revision: current.Record.Revision, Action: "check", Passphrase: "disposable RPC password"}); err == nil {
		t.Fatal("saving configuration enabled a real RPC check")
	}
	vault, err := s.readRPCVault(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := json.Marshal(vault)
	if bytes.Contains(sealed, []byte("ordinary-controlled-rpc-key-12345678")) {
		t.Fatal("RPC secret not encrypted")
	}
	recordBytes, _ := json.Marshal(current.Record)
	if bytes.Contains(recordBytes, password) || bytes.Contains(recordBytes, []byte("ciphertext")) {
		t.Fatal("secret leaked into app-owned records")
	}
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.fixture = bridge
	current, err = s.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(current.Record)
	if !bytes.Equal(recordBytes, after) {
		t.Fatal("restart changed records")
	}
	if err = v.Unlock(password, time.Minute); err != nil {
		t.Fatal(err)
	}
	backup, err := v.Backup(password)
	if err != nil {
		t.Fatal(err)
	}
	restoreDir := t.TempDir()
	if err = os.Chmod(restoreDir, 0700); err != nil {
		t.Fatal(err)
	}
	restored, err := signer.OpenControlledOwnerCustody(restoreDir, "00112233445566778899aabbccddeeff", "devnet")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err = restored.Restore(backup, password, password); err != nil {
		t.Fatal(err)
	}
	old := current.Record.Accounts[0]
	v = restored
	s.fixture = bridge
	current, err = s.Accounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	credential, err = s.RPCCredential(ctx, RPCCredentialCommand{AccountID: old.ID, Revision: current.Record.Revision, Action: "status"})
	if err != nil || !credential.Stale || !credential.Locked {
		t.Fatal("recovered epoch reused a credential", err)
	}
	if current.AccountStates[old.ID] != "stale" {
		t.Fatal("restored epoch reused old reference")
	}
	s.custodyOwnerSupported = true
	rpc.Revision = current.Record.Revision
	if _, err = s.ConfigureAccountRPC(ctx, rpc); err == nil {
		t.Fatal("stale wallet changed RPC")
	}
	nextref := *restored.Status().Account
	command.Reference = &nextref
	command.Revision = current.Record.Revision
	current, err = s.SetupAccount(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Record.Accounts) != 2 || !sameAccountIdentity(old, current.Record.Accounts[0]) || current.Record.Accounts[0].RPC.Endpoint != rpc.Endpoint || !restored.Status().RecoveryRequired || !restored.Status().Locked {
		t.Fatal("recovery overwrote original data/hold")
	}
}

func TestOrdinaryCustodySchemaMigrationAndErasureRefusal(t *testing.T) {
	for _, test := range []string{"valid", "current", "check-failed", "available", "paused", "unprotected"} {
		t.Run(test, func(t *testing.T) {
			dir := fixtureDir(t)
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.accountsRecord(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if test == "current" || test == "check-failed" || test == "available" {
				op := ApplicationUpdateOperation{ID: update.NewProbe(), Phase: test}
				if test == "available" {
					release := fixtureUpdateController().release
					op.Release = &release
				}
				raw, _ := json.Marshal(op)
				if _, err = s.db.Exec("UPDATE application_updates SET document=? WHERE id=1", string(raw)); err != nil {
					t.Fatal(err)
				}
			}
			if test == "paused" {
				if _, err = s.db.Exec("UPDATE update_pause SET phase='paused' WHERE id=1"); err != nil {
					t.Fatal(err)
				}
			}
			if test == "unprotected" {
				if _, err = s.db.Exec(`UPDATE accounts SET document='{"version":1,"revision":0,"accounts":[{"version":4,"mode":"managed"}],"proposals":[]}' WHERE id=1`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.db.Exec("PRAGMA user_version=35"); err != nil {
				t.Fatal(err)
			}
			s.Close()
			s, err = OpenStore(dir)
			if test == "paused" || test == "unprotected" {
				if err == nil {
					s.Close()
					t.Fatal("unprotected migration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			after, err := s.accountsRecord(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			if !bytes.Equal(a, b) {
				t.Fatal("migration rewrote existing accounts")
			}
			var version int
			if err = s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 36 {
				t.Fatal("wrong schema", version, err)
			}
			if UpdateContract().State[0].Versions.Min != 36 {
				t.Fatal("older reader can erase ordinary account format")
			}
		})
	}
}

// Synthetic schema35 fixtures exercise the settled proof, not historical host acceptance.
func TestOrdinaryCustodyMigrationSettledHistory(t *testing.T) {
	for _, phase := range []string{"completed", "rolled-back"} {
		for _, tamper := range []bool{false, true} {
			t.Run(phase+map[bool]string{false: "-preserved", true: "-tampered"}[tamper], func(t *testing.T) {
				s := fixtureStore(t)
				controller := fixtureUpdateController()
				if err := s.BindApplicationMaintenance(controller.Check, func(context.Context, string) error { return nil }); err != nil {
					t.Fatal(err)
				}
				updateCommandFixture(t, s, "check")
				updateTickFixture(t, s)
				updateCommandFixture(t, s, "apply")
				updateTickFixture(t, s)
				view, err := s.ApplicationUpdates(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				op := view.Operation
				lease := update.NewProbe()
				if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: op.ID, Lease: lease}); err != nil {
					t.Fatal(err)
				}
				prepared, err := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: op.ID, Lease: lease})
				if err != nil || prepared.Checkpoint == nil {
					t.Fatal(err)
				}
				engine := op.Release.EngineSHA256
				if phase == "rolled-back" {
					engine = prepared.Checkpoint.PreviousEngineSHA256
				}
				proof := ApplicationUpdateReceipt{ID: op.ID, Sequence: op.Release.Sequence, ManifestSHA256: op.Release.ManifestSHA256, EngineSHA256: engine, Phase: phase}
				if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "finish", ID: op.ID, Lease: lease, Receipt: &proof}); err != nil {
					t.Fatal(err)
				}
				var before string
				var revision int64
				if err = s.db.QueryRow("SELECT document,revision FROM application_updates WHERE id=1").Scan(&before, &revision); err != nil {
					t.Fatal(err)
				}
				if tamper {
					path := filepath.Join(s.dir, "lifecycle", op.ID, prepared.Checkpoint.Snapshot.File)
					b, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					b = append(b, 'x')
					if err = os.WriteFile(path, b, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = s.db.Exec("PRAGMA user_version=35"); err != nil {
					t.Fatal(err)
				}
				dir := s.dir
				s.Close()
				reopened, err := OpenStore(dir)
				if tamper {
					if err == nil {
						reopened.Close()
						t.Fatal("tampered settlement migrated")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				var after string
				var afterRevision int64
				if err = reopened.db.QueryRow("SELECT document,revision FROM application_updates WHERE id=1").Scan(&after, &afterRevision); err != nil {
					t.Fatal(err)
				}
				if before != after || revision != afterRevision {
					t.Fatal("settled update history rewritten")
				}
			})
		}
	}
}

func TestOrdinaryCustodyMigrationRetiredLease(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsent-preserved", true: "unknown-checkpoint-held"}[checkpoint], func(t *testing.T) {
			s := fixtureStore(t)
			controller := fixtureUpdateController()
			if err := s.BindApplicationMaintenance(controller.Check, func(context.Context, string) error { return nil }); err != nil {
				t.Fatal(err)
			}
			updateCommandFixture(t, s, "check")
			updateTickFixture(t, s)
			updateCommandFixture(t, s, "apply")
			updateTickFixture(t, s)
			view, err := s.ApplicationUpdates(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			id := view.Operation.ID
			lease := update.NewProbe()
			if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: id, Lease: lease}); err != nil {
				t.Fatal(err)
			}
			if checkpoint {
				dir := filepath.Join(s.dir, "lifecycle", id)
				if err = os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(dir, "application.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "reject", ID: id, Lease: lease}); err != nil {
				t.Fatal(err)
			}
			var before string
			var revision int64
			if err = s.db.QueryRow("SELECT document,revision FROM application_updates WHERE id=1").Scan(&before, &revision); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec("PRAGMA user_version=35"); err != nil {
				t.Fatal(err)
			}
			directory := s.dir
			s.Close()
			opened, err := OpenStore(directory)
			if checkpoint {
				if err == nil {
					opened.Close()
					t.Fatal("unknown prepared history adopted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			var after string
			var rev int64
			if err = opened.db.QueryRow("SELECT document,revision FROM application_updates WHERE id=1").Scan(&after, &rev); err != nil {
				t.Fatal(err)
			}
			op, _, err := readApplicationUpdate(t.Context(), opened.db)
			if err != nil || op.OwnerLease != lease || op.Receipt != nil || op.Phase != "check-failed" || before != after || revision != rev {
				t.Fatal("retired lease/audit history rewritten", err)
			}
		})
	}
}

func TestOrdinaryCustodyEmptyProjectionDoesNotOfferLegacySetup(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	v, err := signer.OpenControlledOrdinaryCustody(dir, "00112233445566778899aabbccddeeff", "devnet")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	s := fixtureStore(t)
	s.custodyNetwork = "devnet"
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		return signer.Response{Status: v.Status()}, nil
	}
	view, err := s.Accounts(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if view.Setup.State != "empty" || view.Setup.CustodyClass != signer.OrdinaryCustodyMode || !view.Setup.AdmissionHeld || view.Setup.OwnerSupported || len(view.Record.Accounts) != 0 {
		t.Fatal("ordinary namespace advertised legacy key setup")
	}
}
