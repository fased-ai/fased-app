//go:build !windows

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func launcherBody(corePath, root, data string, remote bool) []byte {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	header := "#!/bin/sh\n# Fased App installed command v1\nset -eu\ncore=" + q(corePath) + "\nroot=" + q(root) + "\ndata=" + q(data) + "\naction=${1:-open}\nif [ $# -gt 0 ]; then shift; fi\n"
	if remote {
		return []byte(header + `case "$action" in
 open|setup) exec "$core" vps access "$@" ;;
 status|start|stop|login) exec "$core" vps "$action" "$@" ;;
 update) if [ "$#" -eq 0 ]; then set -- wizard; fi; exec "$core" vps updates "$@" ;;
 uninstall) exec "$core" vps remove "$@" ;;
 --help|-h) printf 'Fased: open, status, start, stop, setup, login, update, uninstall\n' ;;
 *) printf 'Unknown command. Use fased --help.\n' >&2; exit 1 ;;
esac
`)
	}
	return []byte(header + `case "$action" in
 open|setup) exec "$core" open --prefix "$root" --data-dir "$data" "$@" ;;
 start|stop) exec "$core" service "$action" --prefix "$root" --data-dir "$data" "$@" ;;
 status|login|logout) exec "$core" "$action" --data-dir "$data" "$@" ;;
 update) exec "$core" update "$@" --data-dir "$data" ;;
 uninstall) exec "$core" uninstall --prefix "$root" --data-dir "$data" "$@" ;;
 --help|-h) printf 'Fased: open, status, start, stop, setup, login, update, uninstall\n' ;;
 *) printf 'Unknown command. Use fased --help.\n' >&2; exit 1 ;;
esac
`)
}
func installCommandAt(home, corePath, root, data string, remote bool) (string, error) {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return "", errors.New("installed command requires an ordinary owner")
	}
	directory := home
	for _, part := range []string{".local", "bin"} {
		directory = filepath.Join(directory, part)
		if e := os.Mkdir(directory, 0755); e != nil && !errors.Is(e, os.ErrExist) {
			return "", e
		}
		i, e := os.Lstat(directory)
		if e != nil {
			return "", e
		}
		owner, ok := i.Sys().(*syscall.Stat_t)
		if !ok || int(owner.Uid) != os.Getuid() || !i.IsDir() || i.Mode().Perm()&0022 != 0 {
			return "", errors.New("command directory must be owner-controlled without symlinks")
		}
	}
	body := launcherBody(corePath, root, data, remote)
	for _, name := range []string{"fased", "fased-app"} {
		path := filepath.Join(directory, name)
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if e == nil {
			_, we := f.Write(body)
			se := f.Sync()
			ce := f.Close()
			if we != nil {
				return "", we
			}
			if se != nil {
				return "", se
			}
			return path, ce
		}
		if !errors.Is(e, os.ErrExist) {
			return "", e
		}
		i, se := os.Lstat(path)
		if se != nil {
			return "", se
		}
		owner, ok := i.Sys().(*syscall.Stat_t)
		if ok && int(owner.Uid) == os.Getuid() && i.Mode().IsRegular() && i.Mode().Perm() == 0700 {
			prior, re := os.ReadFile(path)
			if re == nil && bytes.Equal(prior, body) {
				return path, nil
			}
		}
	}
	return "", errors.New("existing commands preserved; choose an unused command path before installing")
}
func installCommand(corePath, root, data string, remote bool, out io.Writer) error {
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	path, e := installCommandAt(home, corePath, root, data, remote)
	if e != nil {
		return e
	}
	fmt.Fprintln(out, "Command:", path, "— open, status, update")
	return nil
}

func removeCommand(corePath, root, data string, remote bool) error {
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	body := launcherBody(corePath, root, data, remote)
	for _, name := range []string{"fased", "fased-app"} {
		path := filepath.Join(home, ".local", "bin", name)
		i, e := os.Lstat(path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		owner, ok := i.Sys().(*syscall.Stat_t)
		if !ok || int(owner.Uid) != os.Getuid() || !i.Mode().IsRegular() {
			continue
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if bytes.Equal(b, body) {
			if e = os.Remove(path); e != nil {
				return e
			}
		}
	}
	return nil
}

// Select only the exact launcher produced for this installation. A protected
// foreign command with a familiar filename is never adopted.
func verifiedInstalledCommand(home, corePath, root, data string, remote bool) (string, error) {
	want := launcherBody(corePath, root, data, remote)
	for _, name := range []string{"fased", "fased-app"} {
		path := filepath.Join(home, ".local/bin", name)
		info, e := os.Lstat(path)
		if e != nil {
			continue
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(owner.Uid) != os.Getuid() || !info.Mode().IsRegular() || info.Mode().Perm() != 0700 {
			continue
		}
		b, e := os.ReadFile(path)
		if e == nil && bytes.Equal(b, want) {
			return path, nil
		}
	}
	return "", errors.New("matching installed command is missing; preserve state and repair")
}
