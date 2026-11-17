package localinstall

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPreservesForeignDesktopShortcut(t *testing.T) {
	for _, kind := range []string{"symlink", "file"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			p, engine := installFixture(t)
			link := filepath.Join(home, ".local", "share", "applications", shortcutName)
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				if err := os.Symlink("/old/fased/shortcut", link); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(link, []byte("existing shortcut"), 0600); err != nil {
				t.Fatal(err)
			}
			m, err := Install(t.Context(), p, Options{Engine: engine, Desktop: engine})
			if err != nil {
				t.Fatal("an unrelated shortcut blocked installation", err)
			}
			if err = removeShortcut(m); err != nil {
				t.Fatal("an unrelated shortcut blocked removal", err)
			}
			if kind == "symlink" {
				if target, err := os.Readlink(link); err != nil || target != "/old/fased/shortcut" {
					t.Fatal("foreign shortcut changed", target, err)
				}
			} else if data, err := os.ReadFile(link); err != nil || string(data) != "existing shortcut" {
				t.Fatal("foreign shortcut changed", err)
			}
		})
	}
}

func TestInstallRegistersAndRemovesOwnDesktopShortcut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p, engine := installFixture(t)
	m, err := Install(t.Context(), p, Options{Engine: engine, Desktop: engine})
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".local", "share", "applications", shortcutName)
	if target, err := os.Readlink(link); err != nil || target != filepath.Join(p.Root, shortcutName) {
		t.Fatal("own shortcut missing", target, err)
	}
	if _, err = Install(t.Context(), p, Options{Engine: engine, Desktop: engine}); err != nil {
		t.Fatal("same installation could not resume", err)
	}
	if err = removeShortcut(m); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("own shortcut was not removed", err)
	}
}
