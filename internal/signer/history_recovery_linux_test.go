//go:build linux

package signer

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/onchaintest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIndependentHistoryRecovery(t *testing.T) {
	dir, id := portableFixture(t)
	source, err := Open(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	backup := execute(source, Request{Action: "backup-history", Password: bytes.Clone(backupPassword)}, true)
	if backup.Error != "" || len(backup.Document) == 0 {
		t.Fatal("protected history backup unavailable:", backup.Error)
	}
	before, _ := source.FixtureState()
	original := *source.Status().Account
	source.Close()
	offline := dir + ".offline"
	if err = os.Rename(dir, offline); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(offline) })
	targetDir := fixtureDir(t)
	targetID := "99999999999999999999999999999999"
	target, err := Open(targetDir, targetID)
	if err != nil {
		t.Fatal(err)
	}
	restore := execute(target, Request{Action: "restore", Document: backup.Document, Password: bytes.Clone(backupPassword), NewPassword: bytes.Clone(fixturePassword), SnapshotSHA256: BackupFingerprint(backup.Document)}, true)
	if restore.Error != "" {
		t.Fatal(restore.Error)
	}
	ref := *target.Status().Account
	if !sameWallet(ref, original) || ref.SignerID != targetID || ref.Epoch == original.Epoch || !target.Status().RecoveryRequired {
		t.Fatal("identity/hold lost")
	}
	after, err := target.FixtureState()
	if err != nil || after.Spent != before.Spent || after.Held != before.Held || after.Policy == nil || after.Policy.Budget != before.Policy.Budget {
		t.Fatal("financial history changed", after, err)
	}
	if err = target.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = target.VerifyBackup(backup.Document, backupPassword); err != nil {
		t.Fatal(err)
	}
	if target.RecoverFixtureAuthority(ref) == nil {
		t.Fatal("historical archive cleared recovery hold")
	}
	if _, err = target.ReserveFixture(intentFor(target.ledger.grants[nextGrant(target)], 1)); err == nil {
		t.Fatal("old grant survived")
	}
	target.Close()
	target, err = Open(targetDir, targetID)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if *target.Status().Account != ref || !target.Status().Locked || !target.Status().RecoveryRequired {
		t.Fatal("restart changed recovery")
	}
	if got, err := os.ReadFile(filepath.Join(targetDir, "operations.log")); err != nil || len(got) == 0 {
		t.Fatal("missing recovered history", err)
	}
}
func nextGrant(v *Vault) string {
	for id := range v.ledger.grants {
		return id
	}
	return ""
}

func TestHistoryRecoveryInterruption(t *testing.T) {
	dir, id := portableFixture(t)
	source, err := Open(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	doc, err := source.BackupHistory(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	source.Close()
	pin := BackupFingerprint(doc)
	for _, phase := range []string{"staged", "recovery:operations.log", "recovery:mock-receipts.log", "published"} {
		t.Run(phase, func(t *testing.T) {
			targetDir := fixtureDir(t)
			id := "88888888888888888888888888888888"
			v, err := Open(targetDir, id)
			if err != nil {
				t.Fatal(err)
			}
			v.store.crash = func(p string) error {
				if p == phase {
					return errors.New("disposable interruption")
				}
				return nil
			}
			if v.RestoreHistory(doc, backupPassword, fixturePassword, pin) == nil {
				t.Fatal("missing interruption")
			}
			pending, err := os.ReadFile(filepath.Join(targetDir, "vault.pending"))
			if err != nil {
				t.Fatal(err)
			}
			var e envelope
			if decode(pending, &e) != nil {
				t.Fatal("missing bound pending identity")
			}
			v.Close()
			v, err = Open(targetDir, id)
			if err != nil {
				t.Fatal("real startup recovery", err)
			}
			defer v.Close()
			if *v.Status().Account != e.Ref || !v.Status().RecoveryRequired || !v.Status().Locked {
				t.Fatal("restart changed pending identity/hold")
			}
			s, err := v.FixtureState()
			if err != nil || s.Spent != 5 || s.Held != 7 {
				t.Fatal("interrupted counters", s, err)
			}
			inv, err := v.UpdateInventory("12345678901234567890123456789012")
			if err != nil || inv.InstalledSequence != 1 || inv.SequenceFloor != 2 || !inv.RecoveryHold {
				t.Fatal("floors/holds", inv, err)
			}
			if v.RestoreHistory(doc, backupPassword, fixturePassword, pin) == nil {
				t.Fatal("second restore overwrote key")
			}
		})
	}
}
func TestHistoryRecoveryInvalidAndForeignInputs(t *testing.T) {
	dir, id := portableFixture(t)
	v, err := Open(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	denied := execute(v, Request{Action: "backup-history", Password: bytes.Clone(backupPassword)}, false)
	if denied.Error == "" || len(denied.Document) != 0 {
		t.Fatal("app exported owner history")
	}
	doc, err := v.BackupHistory(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	v.Close()
	for _, kind := range []string{"missing-pin", "wrong-pin", "wrong-password", "tampered", "torn-history", "header-limit", "header-account", "foreign-network"} {
		t.Run(kind, func(t *testing.T) {
			b := bytes.Clone(doc)
			pin := BackupFingerprint(b)
			password := backupPassword
			switch kind {
			case "missing-pin":
				pin = ""
			case "wrong-pin":
				pin = BackupFingerprint(nil)
			case "wrong-password":
				password = fixturePassword
			case "tampered":
				b[len(b)-1] ^= 1
				pin = BackupFingerprint(b)
			case "torn-history", "header-limit", "header-account":
				d, e := unsealPortable(b, password, pin)
				if e != nil {
					t.Fatal(e)
				}
				defer clearPortable(&d)
				switch kind {
				case "torn-history":
					d.files[1] = d.files[1][:len(d.files[1])-1]
				case "header-limit":
					d.header.History.Held = 0
				case "header-account":
					d.header.History.Account.WalletID = randomID()
				}
				b, e = sealPortable(d, password)
				if e != nil {
					t.Fatal(e)
				}
				pin = BackupFingerprint(b)
			}
			targetDir := fixtureDir(t)
			var target *Vault
			if kind == "foreign-network" {
				target, err = openVaultNetwork(targetDir, randomID(), false, "devnet")
			} else {
				target, err = Open(targetDir, randomID())
			}
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if target.RestoreHistory(b, password, fixturePassword, pin) == nil {
				t.Fatal("invalid recovery accepted")
			}
			if target.Status().Account != nil {
				t.Fatal("invalid input published a key")
			}
			if _, err = os.Stat(filepath.Join(targetDir, "vault.pending")); !os.IsNotExist(err) {
				t.Fatal("invalid input staged publication")
			}
		})
	}
}
func TestHistoryRecoveryNetworkCustody(t *testing.T) {
	for _, ordinary := range []bool{false, true} {
		t.Run(fmt.Sprint(ordinary), func(t *testing.T) {
			open := func(dir, id string) (*Vault, error) {
				if ordinary {
					return OpenControlledOwnerCustody(dir, id, "devnet")
				}
				return openVaultNetwork(dir, id, false, "devnet")
			}
			source, err := open(fixtureDir(t), randomID())
			if err != nil {
				t.Fatal(err)
			}
			seed := bytes.Repeat([]byte{46}, 32)
			if ordinary {
				err = source.ImportControlledOrdinarySeed(seed, fixturePassword)
			} else {
				err = source.importManagedSeed(seed, fixturePassword)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = source.Unlock(fixturePassword, time.Minute); err != nil {
				t.Fatal(err)
			}
			old := *source.Status().Account
			doc, err := source.BackupHistory(backupPassword)
			if err != nil {
				t.Fatal(err)
			}
			source.Close()
			targetDir := fixtureDir(t)
			id := randomID()
			target, err := open(targetDir, id)
			if err != nil {
				t.Fatal(err)
			}
			if err = target.RestoreHistory(doc, backupPassword, fixturePassword, BackupFingerprint(doc)); err != nil {
				t.Fatal(err)
			}
			ref := *target.Status().Account
			if !sameWallet(old, ref) || ref.Epoch == old.Epoch || ref.SignerID != id || !target.Status().RecoveryRequired {
				t.Fatal("network identity/hold lost")
			}
			if err = target.Unlock(fixturePassword, time.Minute); err != nil {
				t.Fatal(err)
			}
			if err = target.VerifyBackup(doc, backupPassword); err != nil {
				t.Fatal(err)
			}
			if _, err = target.Backup(backupPassword); err == nil {
				t.Fatal("key-only export dropped historical fence")
			}
			if target.RecoverFixtureAuthority(ref) == nil {
				t.Fatal("network authority reset")
			}
			target.Close()
			target, err = open(targetDir, id)
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if *target.Status().Account != ref || !target.Status().RecoveryRequired || !target.Status().Locked {
				t.Fatal("network restart identity/hold lost")
			}
		})
	}
}

func TestHistoryRecoveryManagedRecordedSwap(t *testing.T) {
	dir := fixtureDir(t)
	source := managedFixture(t, dir, "mainnet-beta")
	if err := source.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	chain := &onchaintest.Chain{}
	client := onchaintest.Client(chain)
	if err := source.ConfigureSwapObserver(client); err != nil {
		t.Fatal(err)
	}
	proposal := managedSwapQuote(t, source, chain)
	if err := source.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ApproveSwap(proposal); err != nil {
		t.Fatal(err)
	}
	signed, err := source.SignSwap(proposal.Quote.ID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := source.BackupHistory(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	source.Close()
	offline := dir + ".offline"
	if err = os.Rename(dir, offline); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(offline) })
	targetDir := fixtureDir(t)
	id := randomID()
	target, err := openVaultNetwork(targetDir, id, false, "mainnet-beta")
	if err != nil {
		t.Fatal(err)
	}
	if err = target.RestoreHistory(doc, backupPassword, fixturePassword, BackupFingerprint(doc)); err != nil {
		t.Fatal("managed recorded history recovery", err)
	}
	ref := *target.Status().Account
	retained, err := target.SwapReceipt(proposal.Quote.ID)
	if err != nil || !bytes.Equal(retained.Signed, signed.Signed) || retained.State != "signed" {
		t.Fatal("original unknown outcome lost", err)
	}
	inv, err := target.UpdateInventory(randomID())
	if err != nil || inv.Unknown != 1 || inv.Pending != 1 || !inv.RecoveryHold {
		t.Fatal("unknown exposure lost", inv, err)
	}
	if err = target.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err = target.SignSwap(proposal.Quote.ID); err == nil {
		t.Fatal("restored historical approval signed")
	}
	if err = target.ConfigureSwapObserver(client); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Send(t.Context(), proposal.Quote, signed.Signed); err != nil {
		t.Fatal(err)
	} // inert fixture chain only
	for n := 0; n < 2; n++ {
		settled, e := target.ReconcileSwap(t.Context(), proposal.Quote.ID)
		if e != nil || settled.State != "confirmed" || !bytes.Equal(settled.Signed, signed.Signed) {
			t.Fatal("read-only reconciliation", e)
		}
	}
	if !target.Status().RecoveryRequired || target.Status().SwapReady {
		t.Fatal("receipt restored spending authority")
	}
	target.Close()
	target, err = openVaultNetwork(targetDir, id, false, "mainnet-beta")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if *target.Status().Account != ref || !target.Status().RecoveryRequired || !target.Status().Locked {
		t.Fatal("recorded recovery restart")
	}
	retained, err = target.SwapReceipt(proposal.Quote.ID)
	if err != nil || retained.State != "confirmed" {
		t.Fatal("reconciled record lost", err)
	}
}

func TestHistoryRecoveryManagedCredentialStaysLocked(t *testing.T) {
	factory := func(onchain.CredentialAccess) (SwapObserver, error) {
		t.Fatal("historical credential was borrowed")
		return nil, ErrUnavailable
	}
	source, err := OpenManagedSwapCredentialsFixture(fixtureDir(t), randomID(), "mainnet-beta", factory)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	if _, err = source.ProvisionReceiptRPC("https://mainnet.helius-rpc.com", bytes.Repeat([]byte{'x'}, 32), fixturePassword); err != nil {
		t.Fatal(err)
	}
	if err = source.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	original := clone(source.ledger.receiptRPC)
	doc, err := source.BackupHistory(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	source.Close()
	target, err := OpenManagedSwapCredentialsFixture(fixtureDir(t), randomID(), "mainnet-beta", factory)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err = target.RestoreHistory(doc, backupPassword, fixturePassword, BackupFingerprint(doc)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(target.ledger.receiptRPC.Ciphertext, original.Ciphertext) || target.ledger.receiptRPC.Account != original.Account {
		t.Fatal("credential evidence changed")
	}
	if _, err = target.UnlockReceiptRPC(fixturePassword, time.Minute); err == nil {
		t.Fatal("old credential silently acquired fresh-epoch authority")
	}
	if !target.Status().RecoveryRequired || target.receiptRPCLease != nil {
		t.Fatal("credential restored authority")
	}
}

func TestHistoryRecoveryPreservesNewerRetainedJournals(t *testing.T) {
	dir := fixtureDir(t)
	source, _, grant := configuredFixture(t, dir)
	doc, err := source.BackupHistory(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	spent := intentFor(grant, 17)
	if _, err = source.ReserveFixture(spent); err != nil {
		t.Fatal(err)
	}
	if _, err = source.ExecuteFixture(spent.OperationID); err != nil {
		t.Fatal(err)
	}
	held := intentFor(grant, 13)
	if _, err = source.ReserveFixture(held); err != nil {
		t.Fatal(err)
	}
	id := source.signerID
	old := *source.Status().Account
	source.Close()
	before := map[string][]byte{}
	for _, n := range []string{"operations.log", "mock-receipts.log"} {
		before[n], err = os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Remove(filepath.Join(dir, "vault.json")); err != nil {
		t.Fatal(err)
	} // disposable simulated key loss only
	target, err := Open(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err = target.RestoreHistory(doc, backupPassword, fixturePassword, BackupFingerprint(doc)); err != nil {
		t.Fatal(err)
	}
	for n, want := range before {
		got, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("newer retained journal overwritten", n, err)
		}
	}
	state, err := target.FixtureState()
	if err != nil || state.Spent != 17 || state.Held != 13 || !state.RecoveryRequired {
		t.Fatal("newer exposure lost", state, err)
	}
	if target.Status().Account.Epoch == old.Epoch {
		t.Fatal("old epoch retained")
	}
	if err = target.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = target.VerifyBackup(doc, backupPassword); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryVerificationRefusesTornRetainedTail(t *testing.T) {
	dir, id := portableFixture(t)
	source, err := Open(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	doc, err := source.BackupHistory(backupPassword)
	if err != nil {
		t.Fatal(err)
	}
	source.Close()
	targetDir := fixtureDir(t)
	target, err := Open(targetDir, randomID())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err = target.RestoreHistory(doc, backupPassword, fixturePassword, BackupFingerprint(doc)); err != nil {
		t.Fatal(err)
	}
	if err = target.Unlock(fixturePassword, time.Minute); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(targetDir, "operations.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte("torn")); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if target.VerifyBackup(doc, backupPassword) == nil {
		t.Fatal("damaged retained history verified as complete")
	}
}
