//go:build linux

package vpsinstall

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

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"golang.org/x/sys/unix"
)

// openSealedSourceSession accepts only this process's sealed upgrade image.
// The open descriptor stays alive while the fixed OS copy reads its proc path.
func openSealedSourceSession(image string) (*os.File, string, string, error) {
	executable, err := os.Executable()
	if err != nil || image != executable || strings.TrimSuffix(image, " (deleted)") != "/memfd:fased-verified-upgrade" {
		return nil, "", "", errors.New("sealed running upgrade executable required")
	}
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		return nil, "", "", err
	}
	keep := false
	defer func() {
		if !keep {
			f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<20 {
		return nil, "", "", errors.New("bounded sealed executable required")
	}
	required := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	seals, err := unix.FcntlInt(f.Fd(), unix.F_GET_SEALS, 0)
	if err != nil || seals&required != required {
		return nil, "", "", errors.New("running upgrade executable is not sealed")
	}
	h := sha256.New()
	n, err := io.CopyBuffer(h, io.LimitReader(f, info.Size()+1), make([]byte, 32*1024))
	if err != nil || n != info.Size() {
		return nil, "", "", errors.New("sealed executable could not be read completely")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, "", "", err
	}
	keep = true
	return f, fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), f.Fd()), hex.EncodeToString(h.Sum(nil)), nil
}

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
	if strings.HasPrefix(image, "/memfd:") {
		file, path, digest, err := openSealedSourceSession(image)
		if err != nil {
			return err
		}
		defer file.Close()
		return prepareSourceSession(ctx, m, path, digest, liveOps(s))
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
		// Supported installed readers can renew their own AppUID session. This
		// exact command is already in the Remote maintenance policy, so no
		// additional privileged source copy or executable grant is necessary.
		if o.helper != nil {
			args := applicationOwnerHelperArgs(m, m.Core)
			args = append(args[:len(args)-3], "updates", "status", "--data-dir", m.Data)
			var output applicationOwnerResponse
			helperErr := o.helper(ctx, args, &output)
			if err := ctx.Err(); err != nil {
				return err
			}
			// `updates status` is the application updater view. The separate
			// `update-status` command returns core.UpdateStatus but is not the
			// command admitted by the installed maintenance policy.
			var status core.ApplicationUpdateView
			if helperErr == nil && json.Unmarshal(output.Bytes(), &status) == nil && status.Version == 1 && status.InstanceID != "" && status.Schema > 0 {
				// The installed `status` entry validates its own endpoint against
				// core health. Bind the update view to that same live instance;
				// the installer manifest ID is a different identity.
				args = applicationOwnerHelperArgs(m, m.Core)
				args = append(args[:len(args)-3], "status", "--data-dir", m.Data)
				var healthOutput applicationOwnerResponse
				healthErr := o.helper(ctx, args, &healthOutput)
				if err := ctx.Err(); err != nil {
					return err
				}
				var health core.Health
				if healthErr != nil || json.Unmarshal(healthOutput.Bytes(), &health) != nil || health.Status != "ok" || health.APIVersion != core.APIVersion || health.InstanceID != status.InstanceID || health.Connection == nil || health.Connection.Location != "remote" || health.Connection.OwnerUID != m.OwnerUID || health.Connection.OwnerKind != "" {
					return errors.New("installed update session does not match the live Remote instance; preserve it")
				}
				return nil
			}
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
	return validateSourceSessionHelper(config, image, record, uid, liveOps(nil))
}

func validateSourceSessionHelper(config, image, record string, uid int, o hostOps) (Manifest, error) {
	var empty Manifest
	data, err := rootFile(o, record)
	if err != nil {
		return empty, err
	}
	var binding sourceSessionBinding
	if strict(data.Data, &binding) != nil || !sessionBindingMatches(binding, config, image, uid) || record != filepath.Join(filepath.Dir(image), "binding.json") {
		return empty, errors.New("fixed Remote source-session helper required")
	}
	m, err := validateApplicationHelperIdentity(config, image, uid, o)
	if err != nil {
		return empty, err
	}
	if hash(encoded(m)) != hash(encoded(binding.Installation)) {
		return empty, errors.New("Remote source identity changed")
	}
	if err = verifyFiles(o, m); err != nil {
		return empty, err
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
