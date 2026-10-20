//go:build linux

package signer

import (
	"bytes"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

var errAbsent = os.ErrNotExist

type storage struct {
	dir    int
	lock   int
	failed bool
	crash  func(string) error // Set only by package tests, never an RPC option.
}

func openStorage(dir string) (*storage, error) {
	fd, err := unix.Open(dir, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	s := &storage{dir: fd, lock: -1}
	ok := false
	defer func() {
		if !ok {
			s.close()
		}
	}()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return nil, err
	}
	if int(st.Uid) != os.Geteuid() || st.Mode&0077 != 0 {
		return nil, errors.New("signer directory must be owned by this identity and private")
	}
	lock, err := unix.Openat(fd, "vault.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	s.lock = lock
	if err = privateFD(lock); err != nil {
		return nil, err
	}
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.New("signer state is already locked")
	}
	if err = s.recoverPublication(); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}

func privateFD(fd int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if int(st.Uid) != os.Geteuid() || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0077 != 0 || st.Nlink > 2 {
		return errors.New("unsafe signer file ownership, mode or type")
	}
	return nil
}

func (s *storage) readName(name string) ([]byte, error) {
	fd, err := unix.Openat(s.dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if err = privateFD(fd); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxDocument+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxDocument {
		return nil, errors.New("oversized signer state")
	}
	return b, nil
}

func (s *storage) recoverPublication() error {
	pending, err := s.readName("vault.pending")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var e envelope
	if err = decode(pending, &e); err != nil {
		return err
	}
	if err = e.validate("vault"); err != nil {
		return err
	}
	if err = s.ensureInitialHistory(e); err != nil {
		return err
	}
	current, err := s.readName("vault.json")
	if errors.Is(err, os.ErrNotExist) {
		if err = unix.Linkat(s.dir, "vault.pending", s.dir, "vault.json", 0); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if !bytes.Equal(pending, current) {
		return errors.New("conflicting interrupted vault publication; preserve both files")
	}
	if err = unix.Fsync(s.dir); err != nil {
		return err
	}
	if err = unix.Unlinkat(s.dir, "vault.pending", 0); err != nil {
		return err
	}
	return unix.Fsync(s.dir)
}
func (s *storage) read() ([]byte, error) { return s.readName("vault.json") }
func (s *storage) checkpoint(phase string) error {
	if s.crash != nil {
		return s.crash(phase)
	}
	return nil
}

func (s *storage) create(data []byte) (result error) {
	if s.failed {
		return errors.New("storage outcome uncertain; restart signer before continuing")
	}
	if _, err := s.read(); !errors.Is(err, os.ErrNotExist) {
		return errors.New("refusing to overwrite existing or unreadable vault")
	}
	fd, err := unix.Openat(s.dir, "vault.pending", unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	// After any publication attempt, an error requires restart/recovery. Never
	// generate a replacement identity or delete an uncertain encrypted original.
	defer func() {
		if result != nil {
			s.failed = true
		}
	}()
	f := os.NewFile(uintptr(fd), "vault.pending")
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = unix.Fsync(s.dir); err != nil {
		return err
	}
	if err = s.checkpoint("staged"); err != nil {
		return err
	}
	var e envelope
	if err = decode(data, &e); err != nil {
		return err
	}
	if err = s.ensureInitialHistory(e); err != nil {
		return err
	}
	if err = unix.Linkat(s.dir, "vault.pending", s.dir, "vault.json", 0); err != nil {
		return err
	}
	if err = unix.Fsync(s.dir); err != nil {
		return err
	}
	if err = s.checkpoint("published"); err != nil {
		return err
	}
	if err = unix.Unlinkat(s.dir, "vault.pending", 0); err != nil {
		return err
	}
	return unix.Fsync(s.dir)
}

func (s *storage) close() error {
	var err error
	if s.lock >= 0 {
		err = unix.Close(s.lock)
		s.lock = -1
	}
	if s.dir >= 0 {
		err = errors.Join(err, unix.Close(s.dir))
		s.dir = -1
	}
	return err
}

func (s *storage) failedState() bool { return s.failed }
func (s *storage) poison()           { s.failed = true }
