//go:build linux

package maintenance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestEnrollmentCopyResumesOnlyMatchingBoundedPrefix(t *testing.T) {
	payload := []byte("verified disposable executable")
	sum := sha256.Sum256(payload)
	for _, scenario := range []string{"new", "partial", "complete", "foreign"} {
		t.Run(scenario, func(t *testing.T) {
			parent := t.TempDir()
			source := filepath.Join(parent, "source")
			targetDir := filepath.Join(parent, "operation")
			if err := os.Mkdir(targetDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, payload, 0600); err != nil {
				t.Fatal(err)
			}
			pending := filepath.Join(targetDir, ".fased-app.enrollment.pending")
			if scenario != "new" {
				data := payload[:8]
				mode := os.FileMode(0600)
				if scenario == "complete" {
					data, mode = payload, 0755
				}
				if scenario == "foreign" {
					data = []byte("foreign")
				}
				if err := os.WriteFile(pending, data, mode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(pending, mode); err != nil {
					t.Fatal(err)
				}
			}
			in, err := os.Open(source)
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			dir, err := os.Open(targetDir)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			err = resumeEnrollmentCopy(in, dir, "fased-app", hex.EncodeToString(sum[:]), int64(len(payload)), os.Getuid(), os.Getgid())
			if scenario == "foreign" {
				if err == nil {
					t.Fatal("foreign prefix accepted")
				}
				if got, e := os.ReadFile(pending); e != nil || string(got) != "foreign" {
					t.Fatal("foreign partial changed", e)
				}
				if _, e := os.Lstat(filepath.Join(targetDir, "fased-app")); !os.IsNotExist(e) {
					t.Fatal("target was published", e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(targetDir, "fased-app"))
			if err != nil || string(got) != string(payload) {
				t.Fatal("copy changed", err)
			}
			if _, err = os.Lstat(pending); !os.IsNotExist(err) {
				t.Fatal("stage was not retired", err)
			}
		})
	}
}

func TestProtectedSignerAndHelperStagesResumeVerifiedPrefix(t *testing.T) {
	payload := []byte("authenticated public installer bytes")
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	for _, tc := range []struct {
		name string
		mode os.FileMode
	}{{"fased-install", 0555}, {"fased-signer", 0755}} {
		t.Run(tc.name, func(t *testing.T) {
			dirPath := t.TempDir()
			stage := "." + tc.name + "-" + digest + ".pending"
			if err := os.WriteFile(filepath.Join(dirPath, stage), payload[:10], 0600); err != nil {
				t.Fatal(err)
			}
			dir, err := os.Open(dirPath)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			if err = resumeProtectedCopy(bytes.NewReader(payload), dir, tc.name, stage, digest, int64(len(payload)), tc.mode, os.Getuid(), os.Getgid()); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(filepath.Join(dirPath, tc.name))
			if err != nil || info.Mode().Perm() != tc.mode {
				t.Fatal("wrong published mode", err)
			}
			if got, err := os.ReadFile(filepath.Join(dirPath, tc.name)); err != nil || !bytes.Equal(got, payload) {
				t.Fatal("published bytes changed", err)
			}
		})
	}
}
