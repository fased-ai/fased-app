//go:build !windows

package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivateJSONPublishesReadableOwnerStateWithRestrictiveUmask(t *testing.T) {
	if os.Getenv("FASED_TEST_RESTRICTIVE_UMASK") == "1" {
		unix.Umask(0227)
		path := os.Getenv("FASED_TEST_PRIVATE_JSON_PATH")
		if err := WritePrivateJSON(path, map[string]string{"state": "prepared"}); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private state must remain owner-readable: %v %v", info, err)
		}
		return
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPrivateJSONPublishesReadableOwnerStateWithRestrictiveUmask$", "-test.count=1")
	cmd.Env = append(os.Environ(), "FASED_TEST_RESTRICTIVE_UMASK=1", "FASED_TEST_PRIVATE_JSON_PATH="+filepath.Join(dir, "state.json"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated umask regression: %v\n%s", err, out)
	}
}
