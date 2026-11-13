//go:build linux

package enrollment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// recoverPreparedOwnerJournal runs while the existing enrollment and source
// leases are held. It publishes only the original fully written first record;
// later or ambiguous interrupted transitions remain held for recovery.
func recoverPreparedOwnerJournal(ctx context.Context, h *runtimeHost, s Source, o Options, coreHash, signerHash string, verifyAuthority func() error) error {
	dirPath := filepath.Dir(h.state)
	dirFD, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	dir := os.NewFile(uintptr(dirFD), "enrollment-journal")
	defer dir.Close()
	info, err := dir.Stat()
	st, ok := infoSys(info)
	if err != nil || !ok || int(st.Uid) != os.Getuid() || !info.IsDir() || st.Mode&07777 != 0700 {
		return errState
	}
	currentDir, err := os.Lstat(dirPath)
	if err != nil || !os.SameFile(info, currentDir) || currentDir.Mode()&os.ModeSymlink != 0 {
		return errState
	}
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	var pending []string
	locked, published := false, false
	for _, entry := range entries {
		locked = locked || entry.Name() == "controller.lock"
		published = published || entry.Name() == "state.json"
		if strings.HasPrefix(entry.Name(), ".pending-") {
			if len(pending) == 8 {
				return errState
			}
			pending = append(pending, entry.Name())
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if !locked || verifyAuthority == nil {
		return errState
	}
	lockFD, err := unix.Openat(dirFD, "controller.lock", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return errState
	}
	lock := os.NewFile(uintptr(lockFD), "enrollment-controller-lock")
	defer lock.Close()
	var lockStat unix.Stat_t
	if unix.Fstat(lockFD, &lockStat) != nil || lockStat.Mode&unix.S_IFMT != unix.S_IFREG || lockStat.Mode&07777 != 0600 || int(lockStat.Uid) != os.Getuid() || lockStat.Nlink != 1 {
		return errState
	}
	if err = unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB); err == nil {
		_ = unix.Flock(lockFD, unix.LOCK_UN)
		return errState
	} else if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
		return err
	}
	if published {
		if _, err = readOwnerJournal(h.state); err != nil {
			return err
		}
		return dir.Sync()
	}
	if len(pending) != 1 {
		return errState
	}
	for _, entry := range entries {
		if entry.Name() != "controller.lock" && entry.Name() != pending[0] {
			return errState
		}
	}
	fd, err := unix.Openat(dirFD, pending[0], unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return errState
	}
	f := os.NewFile(uintptr(fd), "prepared-enrollment")
	defer f.Close()
	before, err := f.Stat()
	fileStat, ok := infoSys(before)
	if err != nil || !ok || !before.Mode().IsRegular() || (fileStat.Mode&07777 != 0600 && fileStat.Mode&07777 != 0400) || int(fileStat.Uid) != os.Getuid() || fileStat.Nlink != 1 || before.Size() <= 0 || before.Size() > 16384 {
		return errState
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	var j journal
	if err != nil || len(raw) > 16384 || update.StrictJSON(raw, &j) != nil || !j.valid(os.Getuid()) || j.Phase != "prepared" || j.Digest != "" || j.CoreRecovery != nil || j.Target != (custodyinstall.Manifest{}) ||
		s.Profile != "local" || !o.NativeOwnerAccess || o.Mode != signer.OrdinaryCustodyMode || !o.Disposable || !o.PrepareHost || o.RecoverSuspended ||
		j.NativeOwnerAccess != o.NativeOwnerAccess || j.Mode != o.Mode || j.Profile != o.Profile || j.Provenance != o.Provenance || j.Network != o.Network || j.CoreHash != coreHash || j.SignerHash != signerHash ||
		!sourceMatches(s, j.Source) || j.Source.ApplicationSequence != s.ApplicationSequence || j.Source.ApplicationReleaseSHA256 != s.ApplicationReleaseSHA256 || s.Validate == nil {
		return errState
	}
	if err = h.unchangedParent(); err != nil {
		return err
	}
	if n, e := realNode(s.Data); e != nil || n != j.Node {
		return errState
	}
	if err = s.Validate(ctx); err != nil {
		return err
	}
	if err = verifyAuthority(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	currentDir, err = os.Lstat(dirPath)
	if err != nil || !os.SameFile(info, currentDir) || currentDir.Mode()&os.ModeSymlink != 0 {
		return errState
	}
	current, err := os.Lstat(filepath.Join(dirPath, pending[0]))
	if err != nil || !os.SameFile(before, current) || current.Mode()&os.ModeSymlink != 0 {
		return errState
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	latest, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || !bytes.Equal(raw, latest) {
		return errState
	}
	// Publish a verified copy from an unnamed inode: the original pending file
	// is neither renamed nor removed, even if another owner process changes it.
	tmpFD, err := unix.Openat(dirFD, ".", unix.O_TMPFILE|unix.O_WRONLY|unix.O_CLOEXEC, 0600)
	if err != nil {
		return errState
	}
	tmp := os.NewFile(uintptr(tmpFD), "prepared-enrollment-publication")
	defer tmp.Close()
	if err = tmp.Chmod(0600); err != nil {
		return err
	}
	if n, e := tmp.Write(raw); e != nil || n != len(raw) {
		return errors.Join(e, io.ErrShortWrite)
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = unix.Linkat(unix.AT_FDCWD, fmt.Sprintf("/proc/self/fd/%d", tmpFD), dirFD, "state.json", unix.AT_SYMLINK_FOLLOW); err != nil {
		return errState
	}
	if err = dir.Sync(); err != nil {
		return err
	}
	_, err = readOwnerJournal(h.state)
	return err
}

func verifyFreshPreparedAuthority(ctx context.Context, s Source) error {
	a, err := maintenance.ReadAuthority(os.Getuid())
	if err != nil || a.Profile != "local" || a.Owner != os.Getuid() || a.Group != os.Getgid() || s.Profile != "local" {
		return errState
	}
	config, err := os.UserConfigDir()
	if err != nil || config != filepath.Join(a.Home, ".config") || maintenance.ValidateOwnerIdentity(a) != nil || maintenance.VerifyImage(a) != nil {
		return errState
	}
	entries, err := os.ReadDir(maintenance.OwnerRoot(a.Owner))
	if err != nil || len(entries) != 1 || entries[0].Name() != "authority.json" {
		return errState
	}
	if _, err = os.Lstat(custodyinstall.Root(a.Owner)); !errors.Is(err, os.ErrNotExist) {
		return errState
	}
	client, err := core.NewClient(ctx, s.Data)
	if err != nil {
		return err
	}
	defer client.Close()
	if client.Endpoint.InstanceID != s.ExpectedInstanceID || client.Endpoint.URL != "http://"+s.Listen {
		return errState
	}
	return nil
}

func infoSys(info os.FileInfo) (*syscall.Stat_t, bool) {
	if info == nil {
		return nil, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return st, ok
}
