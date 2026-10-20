package localinstall

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const shortcutName = "org.fased.app.desktop"

func desktopQuote(s string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "$", "\\$", "`", "\\`", "%", "%%").Replace(s) + "\""
}
func shortcutDefinition(p Paths) []byte {
	return []byte("[Desktop Entry]\nType=Application\nName=Fased App\nComment=Private trading automation workspace\nExec=" + desktopQuote(filepath.Join(p.Root, engineName)) + " open --prefix " + desktopQuote(p.Root) + " --data-dir " + desktopQuote(p.Data) + "\nTerminal=false\nCategories=Finance;\n")
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
	if e != nil || to != filepath.Join(m.Root, shortcutName) {
		return false, errors.New("existing desktop shortcut belongs to another installation")
	}
	return true, nil
}
func registerShortcut(m Manifest) error {
	if m.Files[shortcutName] == "" {
		return nil
	}
	ok, e := shortcutOwned(m)
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
	return os.Symlink(filepath.Join(m.Root, shortcutName), p)
}
func removeShortcut(m Manifest) error {
	if m.Files[shortcutName] == "" {
		return nil
	}
	ok, e := shortcutOwned(m)
	if e != nil || !ok {
		return e
	}
	p, e := shortcutLink()
	if e != nil {
		return e
	}
	return os.Remove(p)
}
