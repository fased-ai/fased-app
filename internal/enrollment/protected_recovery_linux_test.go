//go:build linux

package enrollment

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestProtectedFenceRestoresTheExactArchivedSource(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "published"}[published], func(t *testing.T) {
			h, j, archive := interruptedFenceFixture(t, published)
			if err := restoreProtectedFence(h, j, os.Getuid(), os.Getgid()); err != nil {
				t.Fatal(err)
			}
			n, err := realNode(h.pinned(h.base))
			if err != nil || n != j.Node {
				t.Fatal("restored source identity changed", err)
			}
			if b, err := os.ReadFile(filepath.Join(h.path, h.base, "owner-data")); err != nil || string(b) != "original" {
				t.Fatal("original data changed", err)
			}
			if _, err := os.Lstat(archive); !os.IsNotExist(err) {
				t.Fatal("archive still contains a second source")
			}
		})
	}
}

func TestProtectedFenceRestoresRestrictedInterruptedArchive(t *testing.T) {
	h, j, archive := interruptedFenceFixture(t, false)
	pending := filepath.Dir(filepath.Dir(archive))
	if err := os.Chmod(pending, 0700); err != nil {
		t.Fatal(err)
	}
	if err := restoreProtectedFence(h, j, os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("restricted interrupted archive could not be restored: %v", err)
	}
	if n, err := realNode(h.pinned(h.base)); err != nil || n != j.Node {
		t.Fatal("the original source was not restored", err)
	}
}

func TestProtectedFenceRecoveryRefusesBusyOrForeignSource(t *testing.T) {
	for _, mode := range []string{"busy", "foreign-binding"} {
		t.Run(mode, func(t *testing.T) {
			h, j, archive := interruptedFenceFixture(t, false)
			if mode == "busy" {
				lock, err := core.LockInstance(filepath.Join(archive, "instance.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			} else if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(archive)), "enrollment.json"), []byte(`{}`), 0644); err != nil {
				t.Fatal(err)
			}
			if err := restoreProtectedFence(h, j, os.Getuid(), os.Getgid()); err == nil {
				t.Fatal("busy or foreign source was restored")
			}
			if _, err := os.Stat(filepath.Join(archive, "owner-data")); err != nil {
				t.Fatal("original archive changed", err)
			}
			if _, err := os.Lstat(filepath.Join(h.path, h.base)); !os.IsNotExist(err) {
				t.Fatal("source path was replaced on refusal")
			}
		})
	}
}

func TestProtectedFenceUsesPinnedDirectoriesAndCanRetry(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ name, data string }{{"instance.lock", ""}, {"owner-data", "original"}} {
		if err := os.WriteFile(filepath.Join(source, entry.name), []byte(entry.data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	n, err := realNode(source)
	if err != nil {
		t.Fatal(err)
	}
	j := journal{Version: 1, ID: strings.Repeat("a", 32), Owner: os.Getuid(), Node: n, Digest: strings.Repeat("b", 64)}
	f, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := &runtimeHost{parent: f, path: parent, base: "source"}
	lock, err := core.LockInstance(filepath.Join(source, "instance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = protectedFence(h, j, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = restoreProtectedFence(h, j, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal("interrupted published fence could not restore", err)
	}
	lock, err = core.LockInstance(filepath.Join(source, "instance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = protectedFence(h, j, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal("same source could not be fenced again", err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = finishProtectedFence(h, j, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(source, ".original", "data", "owner-data")); err != nil || string(b) != "original" {
		t.Fatal("the exact original was not preserved", err)
	}
}

func TestProtectedFenceRefusesUnclassifiedOrLinkedDestination(t *testing.T) {
	for _, mode := range []string{"foreign", "symlink", "restricted-foreign"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			source := filepath.Join(parent, "source")
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			n, err := realNode(source)
			if err != nil {
				t.Fatal(err)
			}
			j := journal{Version: 1, ID: strings.Repeat("a", 32), Owner: os.Getuid(), Node: n, Digest: strings.Repeat("b", 64)}
			pending := filepath.Join(parent, ".enrollment-"+j.ID)
			outside := t.TempDir()
			if mode == "symlink" {
				if err = os.Symlink(outside, pending); err != nil {
					t.Fatal(err)
				}
			} else {
				perm := os.FileMode(0755)
				if mode == "restricted-foreign" {
					perm = 0700
				}
				if err = os.Mkdir(pending, perm); err != nil {
					t.Fatal(err)
				}
				if err = os.Chmod(pending, perm); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(pending, "foreign"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f, err := os.Open(parent)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			h := &runtimeHost{parent: f, path: parent, base: "source"}
			if err = protectedFence(h, j, os.Getuid(), os.Getgid()); err == nil {
				t.Fatal("foreign destination was accepted")
			}
			if got, err := realNode(source); err != nil || got != n {
				t.Fatal("original source changed on refusal", err)
			}
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
				t.Fatal("linked destination was modified", err)
			}
		})
	}
}

func TestProtectedFenceRecoversEmptyRestrictedDirectory(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "instance.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	n, err := realNode(source)
	if err != nil {
		t.Fatal(err)
	}
	j := journal{Version: 1, ID: strings.Repeat("a", 32), Owner: os.Getuid(), Node: n, Digest: strings.Repeat("b", 64)}
	if err = os.Mkdir(filepath.Join(parent, ".enrollment-"+j.ID), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := &runtimeHost{parent: f, path: parent, base: "source"}
	lock, err := core.LockInstance(filepath.Join(source, "instance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = protectedFence(h, j, os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("empty interrupted directory was not resumed: %v", err)
	}
}

func interruptedFenceFixture(t *testing.T, published bool) (*runtimeHost, journal, string) {
	t.Helper()
	parent := t.TempDir()
	base := "source"
	source := filepath.Join(parent, base)
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ name, data string }{{"instance.lock", ""}, {"owner-data", "original"}} {
		if err := os.WriteFile(filepath.Join(source, entry.name), []byte(entry.data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	n, err := realNode(source)
	if err != nil {
		t.Fatal(err)
	}
	j := journal{Version: 1, ID: strings.Repeat("a", 32), Owner: os.Getuid(), Node: n, Digest: strings.Repeat("b", 64)}
	pending := filepath.Join(parent, ".enrollment-"+j.ID)
	if err = os.Mkdir(pending, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(pending, 0755); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(pending, ".original")
	if err = os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(pending, "enrollment.json"), jsonBytes(fenceFor(j)), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(pending, "enrollment.json"), 0644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(original, "data")
	if err = os.Rename(source, archive); err != nil {
		t.Fatal(err)
	}
	if published {
		if err = os.Rename(pending, source); err != nil {
			t.Fatal(err)
		}
		archive = filepath.Join(source, ".original", "data")
	}
	f, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return &runtimeHost{parent: f, path: parent, base: base}, j, archive
}
