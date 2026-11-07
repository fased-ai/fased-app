package signer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestRecoveryBackupFileCompatibility(t *testing.T) {
	raw := append([]byte(portableMagic), []byte("encrypted history supplied to the signer")...)
	pin := BackupFingerprint(raw)
	b := OwnerBackup{1, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)), "devnet", pin, raw}
	native, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range [][]byte{raw, native} {
		doc, err := RecoveryDocument(file, pin, "devnet")
		if err != nil || !bytes.Equal(doc, raw) {
			t.Fatal("backup interchange", err)
		}
	}
	for name, file := range map[string][]byte{"raw": raw, "native": native} {
		if _, err := RecoveryDocument(file, "", "devnet"); err == nil {
			t.Fatal(name, "missing pin accepted")
		}
		if _, err := RecoveryDocument(file, BackupFingerprint([]byte("wrong")), "devnet"); err == nil {
			t.Fatal(name, "wrong pin accepted")
		}
	}
	for _, field := range []string{"document", "fingerprint", "network", "instanceId"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(native, &fields); err != nil {
			t.Fatal(err)
		}
		duplicate := append([]byte(`{"`+field+`":`), fields[field]...)
		duplicate = append(duplicate, ',')
		duplicate = append(duplicate, native[1:]...)
		if _, err := RecoveryDocument(duplicate, pin, "devnet"); err == nil {
			t.Fatal("duplicate metadata accepted", field)
		}
	}
	if _, err := RecoveryDocument(native, pin, "mainnet-beta"); err == nil {
		t.Fatal("foreign native network accepted")
	}
	b.Document = append(bytes.Clone(raw), 1)
	bad, _ := json.Marshal(b)
	if _, err := RecoveryDocument(bad, pin, "devnet"); err == nil {
		t.Fatal("changed archive accepted")
	}
	b.Document = raw
	b.InstanceID = "invalid"
	bad, _ = json.Marshal(b)
	if _, err := RecoveryDocument(bad, pin, "devnet"); err == nil {
		t.Fatal("invalid source identity accepted")
	}
	// Legacy raw key-only backups remain cryptographically verified by Restore.
	if got, err := RecoveryDocument([]byte(`{"purpose":"backup"}`), "", ""); err != nil || len(got) == 0 {
		t.Fatal("legacy raw input compatibility", err)
	}
}
