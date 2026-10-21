//go:build linux

package signer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

const maxJournal = 8 * 1024 * 1024

// Each append is synced before its caller may proceed. A torn or conflicting
// record is preserved and refused; startup never truncates an uncertain tail.
// This hash chain detects damaged records, not a hostile signer/root rolling
// back the entire disk. Only the signer UID may write this directory.
func (s *storage) readJournal(name string) (*journal, []journalRecord, error) {
	fd, err := unix.Openat(s.dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if err = journalFD(fd); err != nil {
		return nil, nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxJournal+1))
	if err != nil {
		return nil, nil, err
	}
	if len(b) == 0 || len(b) > maxJournal || b[len(b)-1] != '\n' {
		return nil, nil, errors.New("damaged fixture journal; preserve and reconcile offline")
	}
	j := &journal{name: name, size: len(b)}
	var records []journalRecord
	for _, line := range bytes.Split(b[:len(b)-1], []byte{'\n'}) {
		var r journalRecord
		if len(records) >= 8192 || strictWire(line, &r) != nil || r.Version != JournalFormat || r.Sequence != j.seq+1 || r.Previous != j.hash || r.Hash != recordHash(r) || r.At <= 0 {
			return nil, nil, errors.New("invalid fixture journal chain; refusing rollback or repair")
		}
		j.seq, j.hash = r.Sequence, r.Hash
		records = append(records, r)
	}
	return j, records, nil
}

func journalFD(fd int) error {
	if err := privateFD(fd); err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Nlink != 1 {
		return errors.New("fixture journal must have one private link")
	}
	return nil
}

func (s *storage) appendJournal(j *journal, r journalRecord) (result error) {
	if s.readOnly {
		return errors.New("cold inspection cannot append or repair")
	}
	if s.failed {
		return errors.New("storage outcome uncertain; restart signer")
	}
	r.Version, r.Sequence, r.Previous = JournalFormat, j.seq+1, j.hash
	r.Hash = recordHash(r)
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if len(b) > 64*1024 || j.size+len(b) > maxJournal || j.seq >= 8192 {
		return errors.New("fixture journal capacity reached; no history is pruned")
	}
	defer func() {
		if result != nil {
			s.failed = true
		}
	}()
	if err = s.checkpoint("journal:" + r.Kind + ":before"); err != nil {
		return err
	}
	flags := unix.O_WRONLY | unix.O_APPEND | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if j.seq == 0 {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	fd, err := unix.Openat(s.dir, j.name, flags, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), j.name)
	defer f.Close()
	if err = journalFD(fd); err != nil {
		return err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Size != int64(j.size) {
		return errors.New("fixture journal changed outside signer")
	}
	n, err := f.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	if err = s.checkpoint("journal:" + r.Kind + ":written"); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = unix.Fsync(s.dir); err != nil {
		return err
	}
	if err = s.checkpoint("journal:" + r.Kind + ":durable"); err != nil {
		return err
	}
	j.seq, j.hash, j.size = r.Sequence, r.Hash, j.size+len(b)
	return nil
}
