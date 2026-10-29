//go:build linux

package enrollment

import (
	"context"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
	"strings"
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

func TestEnrollmentFrameUsesImageBound(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	store, err := core.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.Agents(t.Context(), "trading-analyst", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		view, err = store.ChangeAgents(t.Context(), core.AgentCommand{Action: "memory-put", ProfileID: "trading-analyst", Revision: view.Revision, Text: strings.Repeat("synthetic bounded memory ", 100), Source: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	image, lease, err := core.ExportEnrollmentImage(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	raw, err := json.Marshal(image)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= update.MaxDocument {
		t.Fatal("fixture must exceed the signed-document bound")
	}
	actual, err := decodeEnrollmentFrame(append(raw, '\n'))
	if err != nil {
		t.Fatal("valid full source image rejected", len(raw), err)
	}
	if core.EnrollmentImageDigest(actual) != core.EnrollmentImageDigest(image) {
		t.Fatal("source image changed")
	}
}
