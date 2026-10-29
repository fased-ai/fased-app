//go:build linux

package enrollment

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestEnrollmentPinnedParentRefusesSubstitution(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(fd), parent)
	defer f.Close()
	h := runtimeHost{parent: f, path: parent, base: "source"}
	if err = h.unchangedParent(); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(parent, parent+"-original"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err = h.unchangedParent(); err == nil {
		t.Fatal("substituted parent accepted")
	}
	// No System is supplied: the guard must fail before any privileged call.
	if err = h.fence(context.Background(), journal{}); err == nil {
		t.Fatal("substituted parent reached OS mutation")
	}
}

func TestEnrollmentBoundedHelperOutput(t *testing.T) {
	w := limitedOutput{remaining: 4}
	if _, err := w.Write([]byte("four")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("extra")); err == nil || w.String() != "four" {
		t.Fatal("helper response bound failed")
	}
}
