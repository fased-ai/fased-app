//go:build linux

package bootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/bundle"
)

// composeNativeBundle joins two components already authenticated by the
// installer with the separately authenticated headless archive. The public
// archive and its older-reader format are never modified.
func (p *Prepared) composeNativeBundle(desktop, signer *WalletPayload) (_ string, _ string, cleanup func() error, resultErr error) {
	if desktop == nil || signer == nil {
		return "", "", nil, errors.New("matching native client and signer are required together")
	}
	for _, payload := range []*WalletPayload{desktop, signer} {
		if payload.Version != p.descriptor.ProductVersion || payload.Source != p.descriptor.Source {
			return "", "", nil, errors.New("native payload differs from the authenticated release")
		}
		if p.report.DevelopmentOnly {
			if len(payload.Data) == 0 || len(payload.Data) > MaxWalletPayload || !hashOK(payload.SHA256) || digest(payload.Data) != payload.SHA256 {
				return "", "", nil, errors.New("matching development native payload required")
			}
		} else if err := WriteWalletPayload(io.Discard, payload); err != nil {
			return "", "", nil, err
		}
	}
	if p.seed.Version >= 3 && (!p.report.ProvenanceVerified || verifyRunningInstaller(p.descriptor.Release.InstallerSHA256) != nil) {
		return "", "", nil, errors.New("native payload requires the authenticated running installer")
	}
	manifest, err := bundle.VerifyPinned(p.report.BundleDirectory, p.report.BundleSHA256)
	if err != nil || manifest.Desktop || manifest.Files["fased-signer"].SHA256 != "" || manifest.Version != p.descriptor.ProductVersion || manifest.Source != p.descriptor.Source {
		return "", "", nil, errors.New("the original headless release is required")
	}
	const name = "native-bundle"
	if err = p.root.Mkdir(name, 0700); err != nil {
		return "", "", nil, errors.New("native staging already exists; preserve it")
	}
	if err = p.root.Chmod(name, 0700); err != nil {
		return "", "", nil, err
	}
	native, err := p.root.OpenRoot(name)
	if err != nil {
		return "", "", nil, err
	}
	created := map[string]os.FileInfo{}
	directory, err := native.Stat(".")
	if err != nil {
		native.Close()
		return "", "", nil, err
	}
	cleanup = func() error {
		defer native.Close()
		current, err := p.root.Lstat(name)
		if err != nil || !os.SameFile(directory, current) {
			return errors.New("native staging changed; preserve it")
		}
		dir, err := native.Open(".")
		if err != nil {
			return err
		}
		entries, readErr := dir.ReadDir(len(created) + 1)
		err = errors.Join(readErr, dir.Close())
		if err != nil && !errors.Is(err, io.EOF) || len(entries) != len(created) {
			return errors.New("native staging inventory changed; preserve it")
		}
		for _, entry := range entries {
			if _, ok := created[entry.Name()]; !ok {
				return errors.New("native staging contains an unexpected entry; preserve it")
			}
		}
		for file, original := range created {
			current, err := native.Lstat(file)
			if err != nil || original == nil || !os.SameFile(original, current) {
				return errors.New("native staging file changed; preserve it")
			}
		}
		for file := range created {
			if err := native.Remove(file); err != nil {
				return err
			}
		}
		return p.root.Remove(name)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, cleanup())
			cleanup = nil
		}
	}()
	source, err := p.root.OpenRoot("bundle")
	if err != nil {
		return "", "", cleanup, err
	}
	defer source.Close()
	for file, expected := range manifest.Files {
		in, err := source.Open(file)
		if err != nil {
			return "", "", cleanup, err
		}
		stat, statErr := in.Stat()
		entry, entryErr := source.Lstat(file)
		if statErr != nil || entryErr != nil || !stat.Mode().IsRegular() || !os.SameFile(stat, entry) || stat.Size() != expected.Bytes {
			in.Close()
			return "", "", cleanup, errors.New("original bundle file changed")
		}
		mode := os.FileMode(0600)
		engine, _ := bundle.Names()
		if file == engine {
			mode = 0700
		}
		err = copyNativeBundleFile(native, created, file, in, expected, mode)
		err = errors.Join(err, in.Close())
		if err != nil {
			return "", "", cleanup, err
		}
	}
	_, desktopName := bundle.Names()
	for _, item := range []struct {
		name    string
		payload *WalletPayload
	}{{desktopName, desktop}, {"fased-signer", signer}} {
		expected := bundle.File{SHA256: item.payload.SHA256, Bytes: int64(len(item.payload.Data))}
		if err = copyNativeBundleFile(native, created, item.name, bytes.NewReader(item.payload.Data), expected, 0700); err != nil {
			return "", "", cleanup, err
		}
		manifest.Files[item.name] = expected
	}
	manifest.Desktop = true
	if p.report.DevelopmentOnly {
		manifest.Schema, manifest.UnsignedEngineering = 1, true
	} else {
		manifest.Schema, manifest.UnsignedEngineering = 3, false
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return "", "", cleanup, err
	}
	body = append(body, '\n')
	manifestHash := sha256.Sum256(body)
	want := bundle.File{SHA256: hex.EncodeToString(manifestHash[:]), Bytes: int64(len(body))}
	if err = copyNativeBundleFile(native, created, "bundle.json", bytes.NewReader(body), want, 0600); err != nil {
		return "", "", cleanup, err
	}
	path := filepath.Join(p.options.Directory, name)
	if _, err = bundle.VerifyPinned(path, want.SHA256); err != nil {
		return "", "", cleanup, err
	}
	return path, want.SHA256, cleanup, nil
}

func copyNativeBundleFile(root *os.Root, created map[string]os.FileInfo, name string, in io.Reader, expected bundle.File, mode os.FileMode) error {
	if expected.Bytes <= 0 || expected.Bytes > 128<<20 {
		return errors.New("bounded native bundle input required")
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	created[name], err = f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	h := sha256.New()
	n, copyErr := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(in, expected.Bytes+1), make([]byte, 32<<10))
	err = errors.Join(f.Chmod(mode), copyErr, f.Sync(), f.Close())
	if err != nil || n != expected.Bytes || hex.EncodeToString(h.Sum(nil)) != expected.SHA256 {
		return errors.Join(err, errors.New("native bundle input changed during copy"))
	}
	return nil
}
