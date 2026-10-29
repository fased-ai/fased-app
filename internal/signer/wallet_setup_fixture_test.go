//go:build linux

package signer

// Test-binary owner driver only: actual vault, no HTTP route, public export.
// This ordinary-user fixture is not three-UID or owner-host acceptance.
import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWalletOwnerFixtureDriver(t *testing.T) {
	if os.Getenv("FASED_WALLET_OWNER_FIXTURE") != "1" {
		t.Skip("explicit disposable driver only")
	}
	root, err := filepath.Abs(os.Getenv("FASED_WALLET_FIXTURE_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	base, _ := filepath.Abs("../../.evidence/b5-fixtures")
	if !strings.HasPrefix(root, base+string(os.PathSeparator)) {
		t.Fatal("unregistered fixture root")
	}
	if b, e := os.ReadFile(filepath.Join(root, "disposable.json")); e != nil || string(b) != "unfunded-reconstructible-wallet-fixture\n" {
		t.Fatal("missing declaration")
	}
	name := os.Getenv("FASED_WALLET_OWNER_DIR")
	if name != "owner" && name != "recovered" {
		t.Fatal("invalid owner fixture")
	}
	dir := filepath.Join(root, name)
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	v, err := openVaultNetwork(dir, "11111111111111111111111111111111", false, "devnet")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	defer v.Lock()
	unlock := []byte("disposable-wallet-owner-unlock")
	backupPassword := []byte("disposable-wallet-owner-backup")
	defer clear(unlock)
	defer clear(backupPassword)
	action := os.Getenv("FASED_WALLET_OWNER_ACTION")
	switch action {
	case "create":
		err = v.Create(unlock)
	case "import":
		seed := bytes.Repeat([]byte{0x42}, 32)
		defer clear(seed)
		// IPC owns fresh request buffers and wipes them after dispatch. Give the
		// dispatcher the same independent buffers rather than erasing the owner's
		// next backup/unlock input in this direct-call test driver.
		response := execute(v, Request{Action: "import-managed-seed", Document: bytes.Clone(seed), Password: bytes.Clone(unlock)}, true)
		if response.Error != "" {
			err = errors.New(response.Error)
		}
	case "recover":
		blob, e := os.ReadFile(filepath.Join(root, "owner", "backup.json"))
		if e != nil {
			t.Fatal(e)
		}
		defer clear(blob)
		err = v.Restore(blob, backupPassword, unlock)
	case "status":
	default:
		t.Fatal("invalid fixture action")
	}
	if err != nil {
		t.Fatal(err)
	}
	if action != "status" {
		if err = v.Unlock(unlock, 60*1000000000); err != nil {
			t.Fatal(err)
		}
		blob, e := v.Backup(backupPassword)
		if e != nil {
			t.Fatal(e)
		}
		defer clear(blob)
		if err = v.VerifyBackup(blob, backupPassword); err != nil {
			t.Fatal(err)
		}
		f, e := os.OpenFile(filepath.Join(dir, "backup.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			t.Fatal(e)
		}
		_, err = f.Write(blob)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
	v.Lock()
	status := v.Status()
	if status.Account == nil || !status.Locked || !status.RecoveryRequired || status.WalletSigner || status.FinancialExecution {
		t.Fatal("unsafe custody fixture")
	}
	b, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(root, "public-status.next")
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(tmp, filepath.Join(root, "public-status.json")); err != nil {
		t.Fatal(err)
	}
}
