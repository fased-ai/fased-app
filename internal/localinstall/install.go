// Package localinstall owns the ordinary-user engineering installation. It does
// not acquire releases, update binaries in place, run as root, or own app data.
package localinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/privateaccess"
)

type Paths struct{ Root, Data string }
type Options struct {
	Engine, Desktop, Listen, PublicOrigin string
	Start, Autostart                      bool
}
type Manifest struct {
	Schema       int               `json:"schema"`
	Phase        string            `json:"phase"`
	Version      string            `json:"version"`
	Root         string            `json:"root"`
	Data         string            `json:"data"`
	Files        map[string]string `json:"files"`
	Autostart    bool              `json:"autostart"`
	Listen       string            `json:"listen,omitempty"`
	PublicOrigin string            `json:"publicOrigin,omitempty"`
}
type State struct {
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	Autostart bool   `json:"autostart"`
	URL       string `json:"url,omitempty"`
}

// Validate rejects bad paths/listeners before an installer authorizes OS work.
func Validate(p Paths, o Options) error {
	p, e := normalize(p)
	if e != nil {
		return e
	}
	_, e = definition(p, o)
	return e
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	data, err := core.DefaultDataDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{Root: defaultRoot(home), Data: data}, nil
}
func normalize(p Paths) (Paths, error) {
	var err error
	p.Root, err = filepath.Abs(p.Root)
	if err != nil {
		return p, err
	}
	p.Data, err = filepath.Abs(p.Data)
	if err != nil {
		return p, err
	}
	if strings.ContainsAny(p.Root+p.Data, "\r\n\x00") || p.Root == string(filepath.Separator) || p.Root == p.Data {
		return p, errors.New("installation and data must be separate explicit directories")
	}
	for _, pair := range [][2]string{{p.Root, p.Data}, {p.Data, p.Root}} {
		rel, e := filepath.Rel(pair[0], pair[1])
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return p, errors.New("application and data directories must not contain one another")
		}
	}
	return p, nil
}
func digest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("refusing a non-regular installation file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func bytesDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func Load(paths Paths) (Manifest, error) {
	p, err := normalize(paths)
	if err != nil {
		return Manifest{}, err
	}
	data, err := core.ReadPrivateFile(filepath.Join(p.Root, "install.json"))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if json.Unmarshal(data, &m) != nil || m.Schema != 1 || m.Root != p.Root || m.Data != p.Data || (m.Phase != "preparing" && m.Phase != "ready" && m.Phase != "removing") {
		return m, errors.New("unknown installation receipt; refusing mutation")
	}
	for name := range m.Files {
		if name != engineName && name != desktopName && name != definitionName && name != shortcutName {
			return m, errors.New("unknown installation file in receipt")
		}
	}
	if m.Files[engineName] == "" || m.Files[definitionName] == "" {
		return m, errors.New("incomplete installation identity")
	}
	return m, nil
}
func verify(m Manifest) error {
	for name, want := range m.Files {
		got, err := digest(filepath.Join(m.Root, name))
		if err != nil || got != want {
			return fmt.Errorf("installed %s changed or is missing; refusing service mutation", name)
		}
	}
	return nil
}
func Install(ctx context.Context, paths Paths, options Options) (Manifest, error) {
	if err := supported(); err != nil {
		return Manifest{}, err
	}
	p, err := normalize(paths)
	if err != nil {
		return Manifest{}, err
	}
	if err = core.EnsurePrivateDirectory(p.Root); err != nil {
		return Manifest{}, err
	}
	lock, err := core.LockInstance(filepath.Join(p.Root, "install.lock"))
	if err != nil {
		return Manifest{}, errors.New("another installation operation is active")
	}
	defer lock.Close()
	if err = core.EnsurePrivateDirectory(p.Data); err != nil {
		return Manifest{}, err
	}
	if options.Listen == "" {
		options.Listen = "127.0.0.1:7331"
	}
	unit, err := definition(p, options)
	if err != nil {
		return Manifest{}, err
	}
	engineHash, err := digest(options.Engine)
	if err != nil {
		return Manifest{}, err
	}
	files := map[string]string{engineName: engineHash, definitionName: bytesDigest(unit)}
	if options.Desktop != "" {
		files[desktopName], err = digest(options.Desktop)
		if err != nil {
			return Manifest{}, err
		}
		if b := shortcutDefinition(p); len(b) > 0 {
			files[shortcutName] = bytesDigest(b)
		}
	}
	m, loadErr := Load(p)
	if loadErr == nil {
		if m.Phase == "removing" {
			return m, errors.New("unfinished uninstall; resume uninstall before installing")
		}
		if len(files) != len(m.Files) {
			return m, errors.New("installation differs; a verified update or explicit uninstall is required")
		}
		for name, hash := range files {
			if m.Files[name] != hash {
				return m, errors.New("installation differs; updates are not implemented yet")
			}
		}
	} else {
		if !errors.Is(loadErr, os.ErrNotExist) {
			return m, loadErr
		}
		entries, err := os.ReadDir(p.Root)
		if err != nil {
			return m, err
		}
		for _, entry := range entries {
			if entry.Name() != "install.lock" {
				return m, errors.New("installation directory contains unclassified files; refusing adoption")
			}
		}
		m = Manifest{Schema: 1, Phase: "preparing", Version: core.Version, Root: p.Root, Data: p.Data, Files: files, Listen: options.Listen, PublicOrigin: options.PublicOrigin}
		if err = core.WritePrivateJSON(filepath.Join(p.Root, "install.json"), m); err != nil {
			return m, err
		}
	}
	if m.Phase == "preparing" {
		for name, hash := range files {
			target := filepath.Join(p.Root, name)
			if got, err := digest(target); err == nil {
				if got != hash {
					return m, errors.New("partial installation differs; preserve it for inspection")
				}
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return m, err
			}
			if name == definitionName || name == shortcutName {
				b := unit
				if name == shortcutName {
					b = shortcutDefinition(p)
				}
				err = writeNew(target, strings.NewReader(string(b)), 0600)
			} else {
				source := options.Engine
				if name == desktopName {
					source = options.Desktop
				}
				var f *os.File
				f, err = os.Open(source)
				if err == nil {
					err = writeNew(target, f, 0700)
					f.Close()
				}
			}
			if err != nil {
				return m, err
			}
			if got, err := digest(target); err != nil || got != hash {
				return m, errors.New("copied application identity mismatch")
			}
		}
		m.Phase = "ready"
		if err = core.WritePrivateJSON(filepath.Join(p.Root, "install.json"), m); err != nil {
			return m, err
		}
	}
	if err = verify(m); err != nil {
		return m, err
	}
	if err = registerShortcut(m); err != nil {
		return m, err
	}
	if options.Start || options.Autostart {
		if err = register(ctx, m); err != nil {
			return m, err
		}
	}
	if options.Autostart {
		if err = enable(ctx, m); err != nil {
			return m, err
		}
		m.Autostart = true
		if err = core.WritePrivateJSON(filepath.Join(p.Root, "install.json"), m); err != nil {
			return m, err
		}
	}
	if options.Start {
		err = start(ctx, m)
	}
	return m, err
}
func writeNew(path string, in io.Reader, mode os.FileMode) (result error) {
	stage := filepath.Join(filepath.Dir(path), ".staging-"+filepath.Base(path))
	// A preparing receipt owns this exact staging name. A killed first install
	// may leave an incomplete copy; retry removes it without touching app data.
	if info, err := os.Lstat(stage); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("unclassified installation staging file")
		}
		if err = os.Remove(stage); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() {
		if result != nil {
			_ = os.Remove(stage)
		}
	}()
	_, copyErr := io.Copy(f, in)
	if err = errors.Join(copyErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	return os.Rename(stage, path)
}
func Start(ctx context.Context, paths Paths) error {
	m, lock, err := locked(paths)
	if err != nil {
		return err
	}
	defer lock.Close()
	if m.Phase != "ready" {
		return errors.New("installation is not complete")
	}
	if err = register(ctx, m); err != nil {
		return err
	}
	return start(ctx, m)
}
func Stop(ctx context.Context, paths Paths) error {
	m, lock, err := locked(paths)
	if err != nil {
		return err
	}
	defer lock.Close()
	return stop(ctx, m)
}
func locked(paths Paths) (Manifest, *core.InstanceLock, error) {
	p, err := normalize(paths)
	if err != nil {
		return Manifest{}, nil, err
	}
	if err = core.EnsurePrivateDirectory(p.Root); err != nil {
		return Manifest{}, nil, err
	}
	lock, err := core.LockInstance(filepath.Join(p.Root, "install.lock"))
	if err != nil {
		return Manifest{}, nil, errors.New("another installation operation is active")
	}
	m, err := Load(p)
	if err == nil {
		err = verify(m)
	}
	if err != nil {
		lock.Close()
		return m, nil, err
	}
	return m, lock, nil
}
func Status(ctx context.Context, paths Paths) (State, error) {
	m, err := Load(paths)
	if err != nil {
		return State{}, err
	}
	if err = verify(m); err != nil {
		return State{}, err
	}
	return status(ctx, m)
}
func Uninstall(ctx context.Context, paths Paths) error {
	m, err := Load(paths)
	if err != nil {
		return err
	}
	lock, err := core.LockInstance(filepath.Join(m.Root, "install.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if m.Phase != "removing" {
		if err = verify(m); err != nil {
			return err
		}
		if err = privateaccess.System().Remove(ctx, m.Root); err != nil {
			return err
		}
		if err = removeShortcut(m); err != nil {
			return err
		}
		if err = unregister(ctx, m); err != nil {
			return err
		}
		m.Phase = "removing"
		if err = core.WritePrivateJSON(filepath.Join(m.Root, "install.json"), m); err != nil {
			return err
		}
	} else {
		for name, want := range m.Files {
			got, e := digest(filepath.Join(m.Root, name))
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			if e != nil || got != want {
				return errors.New("remaining uninstall file differs; refusing removal")
			}
		}
	}
	// Only digest-verified application files are removed. The SQLite directory and
	// unclassified files are deliberately outside this deletion inventory.
	for name := range m.Files {
		if err = os.Remove(filepath.Join(m.Root, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Remove(filepath.Join(m.Root, "install.json"))
}

func EngineName() string  { return engineName }
func DesktopName() string { return desktopName }
