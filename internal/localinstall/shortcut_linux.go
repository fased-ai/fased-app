package localinstall

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const shortcutName = "org.fased.app.desktop"

var errForeignShortcut = errors.New("existing desktop shortcut belongs to another installation")

func desktopQuote(s string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "$", "\\$", "`", "\\`", "%", "%%").Replace(s) + "\""
}
func shortcutDefinition(p Paths) []byte {
	return []byte("[Desktop Entry]\nType=Application\nName=Fased App\nComment=Private trading automation workspace\nExec=" + desktopQuote(filepath.Join(p.Root, engineName)) + " open --prefix " + desktopQuote(p.Root) + " --data-dir " + desktopQuote(p.Data) + " %u\nTerminal=false\nCategories=Finance;\nMimeType=x-scheme-handler/fased;\n")
}
func shortcutLink() (string, error) {
	h, e := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "applications", shortcutName), e
}
func shortcutOwned(m Manifest) (bool, error) {
	p, e := shortcutLink()
	if e != nil {
		return false, e
	}
	to, e := os.Readlink(p)
	if os.IsNotExist(e) {
		return false, nil
	}
	if errors.Is(e, syscall.EINVAL) {
		return false, errForeignShortcut
	}
	if e != nil {
		return false, e
	}
	if to != filepath.Join(m.Root, shortcutName) {
		return false, errForeignShortcut
	}
	return true, nil
}
func registerShortcut(m Manifest) error {
	if m.Files[shortcutName] == "" {
		return nil
	}
	ok, e := shortcutOwned(m)
	if errors.Is(e, errForeignShortcut) {
		return nil // A shortcut is optional; leave another installation's entry alone.
	}
	if e != nil || ok {
		return e
	}
	p, e := shortcutLink()
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	e = os.Symlink(filepath.Join(m.Root, shortcutName), p)
	if errors.Is(e, os.ErrExist) {
		_, e = shortcutOwned(m)
		if errors.Is(e, errForeignShortcut) {
			return nil
		}
	}
	return e
}
func removeShortcut(m Manifest) error {
	if m.Files[shortcutName] == "" {
		return nil
	}
	ok, e := shortcutOwned(m)
	if errors.Is(e, errForeignShortcut) {
		return nil
	}
	if e != nil || !ok {
		return e
	}
	p, e := shortcutLink()
	if e != nil {
		return e
	}
	return os.Remove(p)
}
