//go:build linux

package signer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateContractMatchesDurableSigner(t *testing.T) {
	dir := fixtureDir(t)
	v := openFixture(t, dir)
	if err := v.Create(fixturePassword); err != nil {
		t.Fatal(err)
	}
	c := UpdateContract()
	b, err := os.ReadFile(filepath.Join(dir, "vault.json"))
	if err != nil {
		t.Fatal(err)
	}
	var e envelope
	if err = json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	if e.Version != VaultFormat || c.State[0].Versions.Min != e.Version || c.State[0].Versions.Max != e.Version {
		t.Fatal("stale vault contract")
	}
	for i, name := range []string{"operations.log", "mock-receipts.log"} {
		b, err = os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
			var r journalRecord
			if err = strictWire(line, &r); err != nil {
				t.Fatal(err)
			}
			if r.Version != JournalFormat || c.State[i+1].Versions.Min != r.Version || c.State[i+1].Versions.Max != r.Version {
				t.Fatal("stale journal contract")
			}
		}
	}
	if v.Status().WalletSigner || v.Status().FinancialExecution {
		t.Fatal("metadata enabled real wallets")
	}
}
