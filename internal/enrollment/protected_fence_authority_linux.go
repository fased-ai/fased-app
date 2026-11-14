//go:build linux

package enrollment

import (
	"bytes"
	"errors"
	"io"
	"os"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// The two records live in the existing protected enrollment operation. The
// reservation precedes directory creation; the inode binding precedes any
// privileged ownership or mode change.
type protectedFenceAuthority struct {
	dir      *os.File
	uid, gid int
}

type protectedFenceBinding struct {
	Version      int    `json:"version"`
	Operation    string `json:"operation"`
	Fence        string `json:"fence"`
	ParentDevice uint64 `json:"parentDevice"`
	ParentInode  uint64 `json:"parentInode"`
	Device       uint64 `json:"device,omitempty"`
	Inode        uint64 `json:"inode,omitempty"`
}

func openProtectedFenceAuthority(owner int, j journal, m custodyinstall.Manifest) (*protectedFenceAuthority, error) {
	dir := update.MaintenanceDirectory(owner, j.ID)
	// Besides checking the file, OpenProtected checks every parent. This path
	// comes from the verified installation and original operation, not IPC.
	f, err := update.OpenProtected(dir+"/fased-app", 64<<20, 0755)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	d := os.NewFile(uintptr(fd), "protected-enrollment-operation")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Gid != 0 || st.Mode&07777 != 0755 || m.ID != j.ID || m.Owner != owner {
		d.Close()
		return nil, errState
	}
	return &protectedFenceAuthority{dir: d, uid: 0, gid: 0}, nil
}

func (a *protectedFenceAuthority) binding(h *runtimeHost, j journal) (protectedFenceBinding, error) {
	var st unix.Stat_t
	if a == nil || a.dir == nil || h.unchangedParent() != nil || unix.Fstat(int(h.parent.Fd()), &st) != nil {
		return protectedFenceBinding{}, errState
	}
	return protectedFenceBinding{Version: 1, Operation: j.ID, Fence: hash(jsonBytes(fenceFor(j))), ParentDevice: uint64(st.Dev), ParentInode: st.Ino}, nil
}

func (a *protectedFenceAuthority) read(name string) ([]byte, error) {
	fd, err := unix.Openat(int(a.dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "protected-enrollment-binding")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != a.uid || int(st.Gid) != a.gid || st.Mode&07777 != 0644 || st.Nlink < 1 || st.Nlink > 2 || st.Size <= 0 || st.Size > 16384 {
		return nil, errState
	}
	return io.ReadAll(io.LimitReader(f, 16385))
}

func (a *protectedFenceAuthority) reserve(h *runtimeHost, j journal) error {
	b, err := a.binding(h, j)
	if err != nil {
		return err
	}
	want := jsonBytes(b)
	old, err := a.read("fence-reservation.json")
	if err == nil {
		if !bytes.Equal(old, want) {
			return errors.New("protected fence reservation differs; preserve it")
		}
		return publishEnrollmentRecordAt(int(a.dir.Fd()), "fence-reservation.json", j.ID, want, a.uid, a.gid)
	}
	if !errors.Is(err, unix.ENOENT) {
		return err
	}
	// A directory that predates this operation's reservation is foreign, even
	// if it happens to be empty and owned by root.
	var st unix.Stat_t
	err = unix.Fstatat(int(h.parent.Fd()), ".enrollment-"+j.ID, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		return errors.New("unreserved pending directory; preserve it")
	}
	if !errors.Is(err, unix.ENOENT) {
		return err
	}
	return publishEnrollmentRecordAt(int(a.dir.Fd()), "fence-reservation.json", j.ID, want, a.uid, a.gid)
}

func (a *protectedFenceAuthority) requireReservation(h *runtimeHost, j journal) (protectedFenceBinding, error) {
	b, err := a.binding(h, j)
	if err != nil {
		return b, err
	}
	raw, err := a.read("fence-reservation.json")
	if err != nil || !bytes.Equal(raw, jsonBytes(b)) {
		return b, errors.New("protected fence reservation unavailable; preserve it")
	}
	return b, nil
}

func (a *protectedFenceAuthority) bind(h *runtimeHost, j journal, fd int) error {
	b, err := a.requireReservation(h, j)
	if err != nil {
		return err
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errState
	}
	b.Device, b.Inode = uint64(st.Dev), st.Ino
	want := jsonBytes(b)
	old, err := a.read("fence-inode.json")
	if err == nil {
		if !bytes.Equal(old, want) {
			return errors.New("protected fence inode differs; preserve it")
		}
		return publishEnrollmentRecordAt(int(a.dir.Fd()), "fence-inode.json", j.ID, want, a.uid, a.gid)
	}
	if !errors.Is(err, unix.ENOENT) {
		return err
	}
	if j.Source.Profile == "local" {
		if int(st.Uid) != j.Node.UID || int(st.Gid) != j.Node.GID || st.Mode&077 != 0 {
			return errors.New("unbound Local fence; preserve it")
		}
	} else if int(st.Uid) != a.uid || int(st.Gid) != a.gid {
		return errors.New("unbound Remote fence; preserve it")
	}
	// No preexisting data can be moved under privileged ownership.
	dup, err := unix.Dup(fd)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(dup), "pending-enrollment-entries")
	names, readErr := f.Readdirnames(1)
	closeErr := f.Close()
	if len(names) != 0 || readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil {
		return errors.New("unbound pending directory is not empty; preserve it")
	}
	return publishEnrollmentRecordAt(int(a.dir.Fd()), "fence-inode.json", j.ID, want, a.uid, a.gid)
}

func (a *protectedFenceAuthority) verify(h *runtimeHost, j journal, fd int) error {
	b, err := a.requireReservation(h, j)
	if err != nil {
		return err
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errState
	}
	b.Device, b.Inode = uint64(st.Dev), st.Ino
	raw, err := a.read("fence-inode.json")
	if err != nil || !bytes.Equal(raw, jsonBytes(b)) {
		return errors.New("protected fence inode unavailable; preserve it")
	}
	return nil
}

func (a *protectedFenceAuthority) verifyAt(h *runtimeHost, j journal, name string) error {
	dir, err := openEnrollmentDirectory(int(h.parent.Fd()), name, a.uid, a.gid, 0755)
	if err != nil {
		return err
	}
	defer dir.Close()
	if !sameEnrollmentEntry(int(h.parent.Fd()), name, int(dir.Fd())) {
		return errState
	}
	return a.verify(h, j, int(dir.Fd()))
}
