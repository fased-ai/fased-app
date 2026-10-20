//go:build linux

package signer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// All traversal starts at the already locked signer directory FD. Neither
// owner-supplied file names nor symlinks select a destination.
func (s *storage) stageFile(id string, index int, write bool) (*os.File, error) {
	if !update.IDOK(id) || index < 0 || index > 1 {
		return nil, errors.New("invalid staging identity")
	}
	name := "update-" + id
	if write {
		if e := unix.Mkdirat(s.dir, name, 0700); e != nil && !errors.Is(e, unix.EEXIST) {
			return nil, e
		}
		if e := unix.Fsync(s.dir); e != nil {
			return nil, e
		}
	}
	d, e := unix.Openat(s.dir, name, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	defer unix.Close(d)
	var st unix.Stat_t
	if e = unix.Fstat(d, &st); e != nil {
		return nil, e
	}
	if int(st.Uid) != os.Geteuid() || st.Mode&0077 != 0 {
		return nil, errors.New("unsafe staging directory ownership or permissions")
	}
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if write {
		flags = unix.O_RDWR | unix.O_CREAT | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	}
	fd, e := unix.Openat(d, fmt.Sprintf("%d.artifact", index), flags, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), "private staged artifact")
	if e = journalFD(fd); e == nil {
		e = unix.Fstat(fd, &st)
	}
	if e == nil && st.Mode&0111 != 0 {
		e = errors.New("staged data must not be executable")
	}
	if e == nil && write {
		e = unix.Fsync(d)
	}
	if e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}

func (s *storage) stageWrite(id string, index int, offset int64, data []byte) (result error) {
	f, e := s.stageFile(id, index, true)
	if e != nil {
		s.poison()
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if offset > st.Size() {
		return errors.New("chunk gap; query durable prefix before retry")
	}
	overlap := min(int64(len(data)), st.Size()-offset)
	old := make([]byte, int(overlap))
	if _, e = f.ReadAt(old, offset); e != nil && overlap > 0 {
		return e
	}
	if !bytes.Equal(old, data[:overlap]) {
		return errors.New("chunk conflicts with preserved bytes; abort this intent")
	}
	defer func() {
		if result != nil {
			s.poison()
		}
	}()
	if e = s.checkpoint("stage:before-write"); e != nil {
		return e
	}
	if overlap < int64(len(data)) {
		n, e := f.WriteAt(data[overlap:], offset+overlap)
		if e != nil {
			return e
		}
		if n != len(data)-int(overlap) {
			return io.ErrShortWrite
		}
	}
	if e = s.checkpoint("stage:written"); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	return s.checkpoint("stage:durable")
}

func (s *storage) stageFiles(i update.Intent, verify bool) ([]update.StageFile, error) {
	files := []update.StageFile{}
	for n, a := range i.Artifacts {
		var size int64
		f, e := s.stageFile(i.ID, n, false)
		if errors.Is(e, os.ErrNotExist) && !verify {
			files = append(files, update.StageFile{Artifact: a})
			continue
		}
		if e != nil {
			return nil, e
		}
		st, e := f.Stat()
		if e == nil {
			size = st.Size()
			if size > a.Size {
				e = errors.New("oversized preserved staged artifact")
			}
		}
		if e == nil && verify {
			h := sha256.New()
			var count int64
			count, e = io.Copy(h, io.LimitReader(f, a.Size+1))
			if e == nil && (count != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256) {
				e = errors.New("staged artifact incomplete or hash mismatch; preserve and abort")
			}
			if e == nil {
				e = f.Sync()
			}
		}
		closeErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
		files = append(files, update.StageFile{Artifact: a, Bytes: size})
	}
	return files, nil
}
