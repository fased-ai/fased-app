//go:build linux

package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"golang.org/x/sys/unix"
)

func PrepareLegacyLocalHandoff(ctx context.Context, o Options, predecessor, target string, paths localinstall.Paths, destination string) (LocalHandoffStage, error) {
	preflight, err := VerifyLegacyLocalHandoff(o, predecessor, target, paths)
	if err != nil {
		return LocalHandoffStage{}, err
	}
	return prepareLegacyLocalStage(ctx, preflight, legacyLocalCore, paths, destination, predecessor, target)
}

func prepareLegacyLocalStage(ctx context.Context, preflight LocalHandoffPreflight, coreSHA256 string, paths localinstall.Paths, destination string, packetDirs ...string) (LocalHandoffStage, error) {
	var stage LocalHandoffStage
	if preflight.Owner <= 0 || preflight.Owner != os.Getuid() || preflight.Owner != os.Geteuid() || preflight.ApplyAllowed {
		return stage, errors.New("pre-Apply Local owner verification required")
	}
	parent := filepath.Dir(destination)
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || !exactHandoffDirectory(parent) {
		return stage, errors.New("an absolute stage path in an existing private directory is required")
	}
	for _, source := range append([]string{paths.Root, paths.Data}, packetDirs...) {
		if filepathHasPrefix(destination, source) || filepathHasPrefix(source, destination) {
			return stage, errors.New("the adoption stage must be separate from the installation, state and packets")
		}
	}
	parentFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return stage, err
	}
	parentFile := os.NewFile(uintptr(parentFD), parent)
	defer parentFile.Close()
	var parentStat unix.Stat_t
	if err := unix.Fstat(int(parentFile.Fd()), &parentStat); err != nil || parentStat.Mode&07777 != 0700 || parentStat.Uid != uint32(preflight.Owner) {
		return stage, errors.New("the adoption stage parent must belong to the owner and have mode 0700")
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return stage, errors.New("an adoption stage already exists or is inaccessible; preserve it")
	}
	// Use the predecessor's existing installer lock without creating anything
	// under its install or state directories. The core lease also blocks service
	// start and direct owner setup writers while copying its SQLite image.
	installLockPath := filepath.Join(paths.Root, "install.lock")
	fd, err := unix.Open(installLockPath, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return stage, errors.New("the original installation lock is required")
	}
	lock := os.NewFile(uintptr(fd), installLockPath)
	defer lock.Close()
	info, err := lock.Stat()
	var st unix.Stat_t
	if err != nil || unix.Fstat(fd, &st) != nil || st.Mode != unix.S_IFREG|0600 || st.Size != 0 ||
		st.Uid != uint32(preflight.Owner) || st.Nlink != 1 || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return stage, errors.New("the original installation lock is unsafe or busy")
	}
	image, lease, err := core.ExportExistingEnrollmentImage(ctx, paths.Data)
	if err != nil {
		return stage, err
	}
	defer lease.Close()
	if image.Schema != core.StateSchema {
		return stage, errors.New("the approved predecessor's Local state schema is required")
	}
	pending, err := os.MkdirTemp(parent, ".local-handoff-pending-")
	if err != nil {
		return stage, err
	}
	defer os.RemoveAll(pending)
	state := filepath.Join(pending, "app-state")
	if err := os.Mkdir(state, 0700); err != nil {
		return stage, err
	}
	if err := core.ImportEnrollmentImage(ctx, state, image); err != nil {
		return stage, err
	}
	stage = LocalHandoffStage{LocalHandoffPreflight: preflight, Version: 1, InstanceID: image.InstanceID,
		StateSchema: image.Schema, StateSHA256: core.EnrollmentImageDigest(image)}
	if err := core.WritePrivateJSON(filepath.Join(pending, "handoff.json"), stage); err != nil {
		return LocalHandoffStage{}, err
	}
	pendingDir, err := os.Open(pending)
	if err != nil {
		return LocalHandoffStage{}, err
	}
	if err := errors.Join(pendingDir.Sync(), pendingDir.Close()); err != nil {
		return LocalHandoffStage{}, err
	}
	manifest, err := inspectLegacyLocalHandoff(paths, coreSHA256)
	currentLock, lockErr := os.Lstat(installLockPath)
	if err != nil || manifest != preflight.LegacyManifestSHA256 || lockErr != nil || !os.SameFile(info, currentLock) || core.VerifyEnrollmentImageLease(lease) != nil {
		return LocalHandoffStage{}, errors.New("predecessor changed while preparing the adoption stage")
	}
	currentParent, err := os.Lstat(parent)
	heldParent, heldErr := parentFile.Stat()
	if err != nil || heldErr != nil || !os.SameFile(currentParent, heldParent) {
		return LocalHandoffStage{}, errors.New("adoption stage parent changed")
	}
	if err := ctx.Err(); err != nil {
		return LocalHandoffStage{}, err
	}
	if err := unix.Renameat2(parentFD, filepath.Base(pending), parentFD, filepath.Base(destination), unix.RENAME_NOREPLACE); err != nil {
		return LocalHandoffStage{}, err
	}
	return stage, parentFile.Sync()
}

func filepathHasPrefix(path, prefix string) bool {
	for {
		if path == prefix {
			return true
		}
		parent := filepath.Dir(path)
		if path == parent {
			return false
		}
		path = parent
	}
}
