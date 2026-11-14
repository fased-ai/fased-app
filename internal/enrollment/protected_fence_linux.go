//go:build linux

package enrollment

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// protectedFence publishes only into directories held by descriptor. A Local
// owner can rename entries in the source parent; no privileged write follows a
// re-opened child pathname through that parent.
func protectedFence(h *runtimeHost, j journal, managerUID, managerGID int, authority *protectedFenceAuthority) error {
	if err := h.unchangedParent(); err != nil {
		return err
	}
	parent := int(h.parent.Fd())
	if n, err := realNode(h.pinned(h.base)); err != nil || n != j.Node {
		return errors.New("original source changed before fencing")
	}
	pending := ".enrollment-" + j.ID
	if _, err := authority.requireReservation(h, j); err != nil {
		return err
	}
	// Remote's source parent is protected. Local creates its pending directory
	// as the ordinary owner after reservation and before this helper call.
	if j.Source.Profile != "local" {
		if err := unix.Mkdirat(parent, pending, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return err
		}
	}
	fd, err := unix.Openat(parent, pending, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("foreign source fence; preserve it")
	}
	dir := os.NewFile(uintptr(fd), "pending-enrollment-fence")
	defer dir.Close()
	var pendingStat unix.Stat_t
	if unix.Fstat(fd, &pendingStat) != nil || !sameEnrollmentEntry(parent, pending, fd) {
		return errState
	}
	if err = authority.bind(h, j, fd); err != nil {
		return err
	}
	if (int(pendingStat.Uid) != managerUID || int(pendingStat.Gid) != managerGID) && (int(pendingStat.Uid) != j.Node.UID || int(pendingStat.Gid) != j.Node.GID) {
		return errors.New("fence ownership differs; preserve it")
	}
	if int(pendingStat.Uid) != managerUID || int(pendingStat.Gid) != managerGID {
		if err = unix.Fchown(fd, managerUID, managerGID); err != nil {
			return err
		}
	}
	// The inode has now been recorded in root-protected operation metadata.
	// Normalize only this held inode, independent of the caller's umask.
	if err = unix.Fchmod(fd, 0700); err != nil {
		return err
	}
	if err = errors.Join(unix.Fsync(fd), unix.Fsync(parent)); err != nil {
		return err
	}
	if !sameEnrollmentEntry(parent, pending, int(dir.Fd())) {
		return errState
	}
	if err = validateProtectedFenceEntries(int(dir.Fd()), j.ID, false); err != nil {
		return err
	}
	if err = publishEnrollmentRecordAt(int(dir.Fd()), "enrollment.json", j.ID, jsonBytes(fenceFor(j)), managerUID, managerGID); err != nil {
		return err
	}
	if err = unix.Mkdirat(int(dir.Fd()), ".original", 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	original, err := openProtectedOriginal(int(dir.Fd()), managerUID, managerGID)
	if err != nil {
		return errors.New("foreign original archive; preserve it")
	}
	defer original.Close()
	// Persist the archive directory entry before moving the only source into it.
	if err = unix.Fsync(int(dir.Fd())); err != nil {
		return err
	}
	data, err := openEnrollmentDirectory(parent, h.base, j.Node.UID, j.Node.GID, 0700)
	if err != nil {
		return err
	}
	defer data.Close()
	var st unix.Stat_t
	if unix.Fstat(int(data.Fd()), &st) != nil || uint64(st.Dev) != j.Node.Device || st.Ino != j.Node.Inode || !sameEnrollmentEntry(parent, h.base, int(data.Fd())) || !sameEnrollmentEntry(parent, pending, int(dir.Fd())) {
		return errState
	}
	if err = unix.Renameat2(parent, h.base, int(original.Fd()), "data", unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	if !sameEnrollmentEntry(int(original.Fd()), "data", int(data.Fd())) {
		return errors.New("original archive identity changed; preserve it")
	}
	if err = errors.Join(unix.Fsync(int(original.Fd())), unix.Fsync(parent)); err != nil {
		return err
	}
	if !sameEnrollmentEntry(parent, pending, int(dir.Fd())) {
		return errors.New("source fence moved during cutover; preserve the original")
	}
	// Only widen the directory after it contains this operation's binding and
	// exact archived inode. A substituted unrelated root directory is untouched.
	if err = unix.Fchmod(fd, 0755); err != nil {
		return err
	}
	if err = unix.Fsync(fd); err != nil {
		return err
	}
	if err = unix.Renameat2(parent, pending, parent, h.base, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	if !sameEnrollmentEntry(parent, h.base, int(dir.Fd())) {
		return errors.New("published source fence differs; preserve the original")
	}
	return unix.Fsync(parent)
}

// The bound pending directory is already owned by the maintenance authority.
// Normalize only its pinned original archive; no foreign mode or owner is
// adopted when retrying after an interrupted mkdir under a restrictive umask.
func openProtectedOriginal(parent, uid, gid int) (*os.File, error) {
	fd, err := unix.Openat(parent, ".original", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "enrollment-original")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || int(st.Uid) != uid || int(st.Gid) != gid || st.Mode&07777&^0700 != 0 || !sameEnrollmentEntry(parent, ".original", fd) {
		f.Close()
		return nil, errState
	}
	if err = unix.Fchmod(fd, 0700); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func validateProtectedFenceEntries(dir int, id string, allowReady bool) error {
	fd, err := unix.Dup(dir)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "enrollment-fence-entries")
	defer f.Close()
	names, err := f.Readdirnames(6)
	max := 3
	if allowReady {
		max = 5
	}
	if err != nil && !errors.Is(err, io.EOF) || len(names) > max {
		return errors.New("unclassified source fence; preserve it")
	}
	for _, name := range names {
		if name != "enrollment.json" && name != ".enrollment.json-"+id+".pending" && name != ".original" && !(allowReady && (name == "ready.json" || name == ".ready.json-"+id+".pending")) {
			return errors.New("unclassified source fence; preserve it")
		}
	}
	return nil
}

func finishProtectedFence(h *runtimeHost, j journal, managerUID, managerGID int, authority *protectedFenceAuthority) error {
	if err := h.unchangedParent(); err != nil {
		return err
	}
	parent := int(h.parent.Fd())
	dir, err := openEnrollmentDirectory(parent, h.base, managerUID, managerGID, 0755)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = authority.verify(h, j, int(dir.Fd())); err != nil {
		return err
	}
	if err = validateProtectedFenceEntries(int(dir.Fd()), j.ID, true); err != nil {
		return err
	}
	if err = verifyEnrollmentFenceRecord(int(dir.Fd()), j, managerUID, managerGID); err != nil {
		return err
	}
	original, err := openEnrollmentDirectory(int(dir.Fd()), ".original", managerUID, managerGID, 0700)
	if err != nil {
		return err
	}
	defer original.Close()
	data, err := openEnrollmentDirectory(int(original.Fd()), "data", j.Node.UID, j.Node.GID, 0700)
	if err != nil {
		return err
	}
	defer data.Close()
	var st unix.Stat_t
	if unix.Fstat(int(data.Fd()), &st) != nil || uint64(st.Dev) != j.Node.Device || st.Ino != j.Node.Inode || !sameEnrollmentEntry(parent, h.base, int(dir.Fd())) {
		return errState
	}
	binding := jsonBytes(map[string]any{"version": 1, "binding": hash(jsonBytes(fenceFor(j)))})
	if err = publishEnrollmentRecordAt(int(dir.Fd()), "ready.json", j.ID, binding, managerUID, managerGID); err != nil {
		return err
	}
	if !sameEnrollmentEntry(parent, h.base, int(dir.Fd())) {
		return errors.New("source fence moved during completion; preserve it")
	}
	return nil
}

// publishEnrollmentRecordAt permits only fixed enrollment records. It
// does not replace a foreign target or follow links, including after a crash.
func publishEnrollmentRecordAt(dir int, name, id string, data []byte, managerUID, managerGID int) error {
	if (name != "enrollment.json" && name != "ready.json" && name != "fence-reservation.json" && name != "fence-inode.json") || !hexID(id, 16) || len(data) == 0 || len(data) > 16384 {
		return errState
	}
	stage := "." + name + "-" + id + ".pending"
	read := func(name string, partial bool) ([]byte, unix.Stat_t, error) {
		var st unix.Stat_t
		fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
		if err != nil {
			return nil, st, err
		}
		f := os.NewFile(uintptr(fd), "enrollment-record")
		defer f.Close()
		if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != managerUID || int(st.Gid) != managerGID || (!partial && st.Mode&07777 != 0644) || (partial && st.Mode&07777&^0644 != 0) || st.Nlink < 1 || st.Nlink > 2 || st.Size < 0 || st.Size > 16384 {
			return nil, st, errState
		}
		b, err := io.ReadAll(io.LimitReader(f, 16385))
		return b, st, err
	}
	current, _, currentErr := read(name, false)
	if currentErr == nil && !bytes.Equal(current, data) || currentErr != nil && !errors.Is(currentErr, unix.ENOENT) {
		return errors.New("foreign enrollment record; preserve it")
	}
	staged, st, stageErr := read(stage, true)
	if stageErr == nil {
		if !bytes.HasPrefix(data, staged) || st.Nlink > 1 && currentErr != nil {
			return errors.New("foreign enrollment staging record; preserve it")
		}
		if err := unix.Unlinkat(dir, stage, 0); err != nil {
			return err
		}
	} else if !errors.Is(stageErr, unix.ENOENT) {
		return stageErr
	}
	if currentErr == nil {
		return unix.Fsync(dir)
	}
	fd, err := unix.Openat(dir, stage, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0644)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "enrollment-staging-"+strconv.Itoa(len(data)))
	if err = f.Chmod(0644); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = unix.Linkat(dir, stage, dir, name, 0); err != nil {
		return err
	}
	if err = unix.Fsync(dir); err != nil {
		return err
	}
	if err = unix.Unlinkat(dir, stage, 0); err != nil {
		return err
	}
	return unix.Fsync(dir)
}
