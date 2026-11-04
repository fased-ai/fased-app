//go:build linux

package vpsinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

// PrepareSourceSession is used by the already-verified current bootstrap BEFORE
// invoking an older reader's inventory/status. Only the existing OS owner can
// stage it; the root-owned, content-bound helper runs after dropping to AppUID.
// It grants no network, automatic-update, financial or database-migration power.
func PrepareSourceSession(ctx context.Context, m Manifest, image string, s *custodyinstall.System) error {
	if image == m.Core {
		return nil
	} // Current installed commands already refresh through protected IPC.
	if s == nil || os.Getuid() != m.OwnerUID || os.Geteuid() != os.Getuid() {
		return errors.New("existing Remote OS owner required")
	}
	info, err := os.Lstat(image)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("verified bootstrap executable required")
	}
	source, err := bundle.Inspect(image)
	if err != nil || source.Bytes <= 0 || source.Bytes > 64<<20 {
		return errors.New("bounded verified bootstrap executable required")
	}
	return prepareSourceSession(ctx, m, image, source.SHA256, liveOps(s))
}
func prepareSourceSession(ctx context.Context, m Manifest, image, digest string, o hostOps) error {
	decoded, e := hex.DecodeString(digest)
	if e != nil || len(decoded) != 32 {
		return errors.New("invalid bootstrap image identity")
	}
	return locked(ctx, o, m, func(o hostOps) error {
		if err := verify(ctx, o, m); err != nil {
			return err
		}
		if err := loaded(ctx, o, m); err != nil {
			return err
		}
		dir := m.Root + "-session-" + digest[:24]
		if err := directory(ctx, o, dir, 0, 0, 0755); err != nil {
			return err
		}
		target := filepath.Join(dir, "helper")
		if err := sessionBridgeImage(o, target, digest); errors.Is(err, os.ErrNotExist) {
			// OS install streams the image. Do not keep whole executable copies in Go
			// heaps alongside the old core on small VPS hosts.
			if _, err = o.run(ctx, nil, "/usr/bin/install", "-m", "0755", "-o", "root", "-g", "root", "--", image, target); err != nil {
				return err
			}
			if _, err = o.run(ctx, nil, "/usr/bin/sync", "-f", target); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := sessionBridgeImage(o, target, digest); err != nil {
			return err
		}
		binding := sourceSessionBinding{Version: 1, Installation: m, HelperSHA256: digest}
		record := filepath.Join(dir, "binding.json")
		if err := publish(ctx, o, record, digest[:32], encoded(binding), 0644); err != nil {
			return err
		}
		args := applicationOwnerHelperArgs(m, target)
		args[len(args)-3] = "reader-session-client"
		args = append(args, "--binding", record)
		return o.helper(ctx, args, io.Discard)
	})
}
func sessionBridgeImage(o hostOps, path, digest string) error {
	return rootFileContent(o, path, func(info hostFile, size int64, reader io.Reader) error {
		if info.Mode.Perm() != 0755 || size <= 0 || size > 64<<20 {
			return errors.New("invalid session helper image")
		}
		h := sha256.New()
		if _, err := io.Copy(h, reader); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != digest {
			return errors.New("session helper changed; preserve it")
		}
		return nil
	})
}
func ValidateSourceSessionHelper(config, image, record string, uid int) (Manifest, error) {
	var empty Manifest
	o := liveOps(nil)
	data, err := rootFile(o, record)
	if err != nil {
		return empty, err
	}
	var binding sourceSessionBinding
	if strict(data.Data, &binding) != nil || !sessionBindingMatches(binding, config, image, uid) || record != filepath.Join(filepath.Dir(image), "binding.json") {
		return empty, errors.New("fixed Remote source-session helper required")
	}
	m, err := ValidateApplicationOwnerHelper(config, binding.Installation.Core, uid)
	if err != nil {
		return empty, err
	}
	if hash(encoded(m)) != hash(encoded(binding.Installation)) {
		return empty, errors.New("Remote source identity changed")
	}
	if err = sessionBridgeImage(o, image, binding.HelperSHA256); err != nil {
		return empty, err
	}
	return m, nil
}
func sessionBindingMatches(b sourceSessionBinding, config, image string, uid int) bool {
	if b.Version != 1 || len(b.HelperSHA256) != 64 || uid <= 0 || b.Installation.AppUID != uid || b.Installation.OwnerUID <= 0 || b.Installation.OwnerUID == uid || b.Installation.Config != config {
		return false
	}
	decoded, err := hex.DecodeString(b.HelperSHA256)
	if err != nil || len(decoded) != 32 {
		return false
	}
	dir := b.Installation.Root + "-session-" + b.HelperSHA256[:24]
	return image == filepath.Join(dir, "helper")
}
