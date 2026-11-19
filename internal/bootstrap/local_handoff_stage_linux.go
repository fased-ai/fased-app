//go:build linux

package bootstrap

import (
	"context"
	"errors"
	"io"
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

// ExportLegacyLocalHandoff reopens the actual stopped predecessor as its OS
// owner. The protected importer does not trust a previously staged receipt.
func ExportLegacyLocalHandoff(ctx context.Context, o Options, predecessor, target string, paths localinstall.Paths, control io.Reader, out io.Writer) error {
	preflight, err := VerifyLegacyLocalHandoff(o, predecessor, target, paths)
	if err != nil {
		return err
	}
	image, verify, closeSource, err := openLegacyLocalImage(ctx, preflight, legacyLocalCore, paths)
	if err != nil {
		return err
	}
	defer closeSource()
	if err := verify(); err != nil {
		return err
	}
	if err := writeLegacyLocalImage(out, preflight, image); err != nil {
		return err
	}
	var check [7]byte
	if _, err := io.ReadFull(control, check[:]); err != nil || string(check[:]) != "verify\n" {
		return errors.New("original Local adoption verification required")
	}
	if err := verify(); err != nil {
		return err
	}
	if _, err := io.WriteString(out, "ok\n"); err != nil {
		return err
	}
	// The controller closes the private control pipe after sealing the
	// journal. Hold the source leases through that publication.
	if n, err := io.ReadFull(control, check[:1]); n != 0 || !errors.Is(err, io.EOF) {
		return errors.New("unexpected Local adoption control data")
	}
	return nil
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
	image, verify, closeSource, err := openLegacyLocalImage(ctx, preflight, coreSHA256, paths)
	if err != nil {
		return stage, err
	}
	defer closeSource()
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
	stage = legacyLocalStage(preflight, image)
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
	if err := verify(); err != nil {
		return LocalHandoffStage{}, err
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

func openLegacyLocalImage(ctx context.Context, preflight LocalHandoffPreflight, coreSHA256 string, paths localinstall.Paths) (core.EnrollmentImage, func() error, func(), error) {
	var empty core.EnrollmentImage
	path := filepath.Join(paths.Root, "install.lock")
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return empty, nil, nil, errors.New("the original installation lock is required")
	}
	lock := os.NewFile(uintptr(fd), path)
	info, err := lock.Stat()
	var st unix.Stat_t
	if err != nil || unix.Fstat(fd, &st) != nil || st.Mode != unix.S_IFREG|0600 || st.Size != 0 ||
		st.Uid != uint32(preflight.Owner) || st.Nlink != 1 || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		lock.Close()
		return empty, nil, nil, errors.New("the original installation lock is unsafe or busy")
	}
	image, lease, err := core.ExportExistingEnrollmentImage(ctx, paths.Data)
	if err != nil {
		lock.Close()
		return empty, nil, nil, err
	}
	closeSource := func() { lease.Close(); lock.Close() }
	if image.Schema != core.StateSchema {
		closeSource()
		return empty, nil, nil, errors.New("the approved predecessor's Local state schema is required")
	}
	verify := func() error {
		manifest, err := inspectLegacyLocalHandoff(paths, coreSHA256)
		currentLock, lockErr := os.Lstat(path)
		if err != nil || manifest != preflight.LegacyManifestSHA256 || lockErr != nil || !os.SameFile(info, currentLock) || core.VerifyEnrollmentImageLease(lease) != nil {
			return errors.New("predecessor changed while preparing adoption")
		}
		return nil
	}
	return image, verify, closeSource, nil
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
