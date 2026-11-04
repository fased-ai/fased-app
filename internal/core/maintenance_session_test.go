package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMaintenanceSessionNeverMigratesPredecessorOrOwner(t *testing.T) {
	for _, version := range []int{33, 35, 36, 37, 38} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "fased")
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			code, _ := s.IssueClaimCode(t.Context())
			old, err := s.Claim(t.Context(), code.Code, "preserved-owner-password")
			if err != nil {
				t.Fatal(err)
			}
			var salt, hash []byte
			s.db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&salt, &hash)
			if _, err = s.db.Exec("PRAGMA user_version=" + fmt.Sprint(version)); err != nil {
				t.Fatal(err)
			}
			token, err := IssueMaintenanceSession(t.Context(), dir, s.instanceID)
			if err != nil {
				t.Fatal(err)
			}
			var after int
			s.db.QueryRow("PRAGMA user_version").Scan(&after)
			var afterSalt, afterHash []byte
			s.db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&afterSalt, &afterHash)
			if after != version || string(salt) != string(afterSalt) || string(hash) != string(afterHash) || !s.Session(t.Context(), old) || !s.Session(t.Context(), token) {
				t.Fatal("maintenance changed predecessor identity/schema")
			}
			if _, err = IssueMaintenanceSession(t.Context(), dir, "wrong-instance"); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("foreign instance authorized", err)
			}
		})
	}
}
func TestMaintenanceSessionDoesNotCreateMissingDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fased")
	if _, err := IssueMaintenanceSession(t.Context(), dir, "unknown"); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "core.sqlite")); !os.IsNotExist(err) {
		t.Fatal("created database during recovery", err)
	}
}
