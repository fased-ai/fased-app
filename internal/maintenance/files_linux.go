//go:build linux

package maintenance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
)

func ProtectedDigest(path string, mode os.FileMode, max int64) (string, int64, error) {
	f, err := openProtected(path, max, mode)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(h, io.LimitReader(f, max+1), make([]byte, 32<<10))
	if err != nil || n > max {
		return "", n, errors.New("bounded protected content required")
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ReplaceProtected atomically replaces only a previously classified root file.
// This in-process primitive is never exposed as a caller-selected path request.
func ReplaceProtected(path string, data []byte, mode os.FileMode, previous string) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("protected replacement required")
	}
	h := sha256.Sum256(data)
	wanted := hex.EncodeToString(h[:])
	actual, _, err := ProtectedDigest(path, mode, 128<<20)
	if errors.Is(err, os.ErrNotExist) && previous == "" {
		return PublishProtected(path, data, mode)
	}
	if err != nil {
		return err
	}
	if actual == wanted {
		return syncDirectory(filepath.Dir(path))
	}
	if actual != previous {
		return errors.New("protected replacement target changed; preserving it")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".fased-maintenance-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	current, _, err := ProtectedDigest(path, mode, 128<<20)
	if err != nil || current != previous {
		return errors.New("protected target changed before replacement")
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func publishCopiedExclusive(temporary, target string) error {
	if err := unix.Renameat2(unix.AT_FDCWD, temporary, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE); err != nil {
		return errors.New("protected copy publication conflicted; preserving it")
	}
	return syncDirectory(filepath.Dir(target))
}

// RemoveProtected retires only a fixed helper-owned fence after the durable
// completed journal exists. It is not an IPC-selected deletion operation.
func RemoveProtected(path, hash string, mode os.FileMode) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("protected cleanup required")
	}
	actual, _, err := ProtectedDigest(path, mode, 8192)
	if errors.Is(err, os.ErrNotExist) {
		return syncDirectory(filepath.Dir(path))
	}
	if err != nil {
		return err
	}
	if actual != hash {
		return errors.New("foreign maintenance fence; preserve it")
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func CopyProtected(source, target string, mode os.FileMode, hash string, size int64, previous string) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || size <= 0 || size > 128<<20 {
		return errors.New("bounded protected copy required")
	}
	in, err := openProtected(source, size, mode)
	if err != nil {
		return err
	}
	defer in.Close()
	actual, _, err := ProtectedDigest(target, mode, 128<<20)
	if err == nil && actual == hash {
		return syncDirectory(filepath.Dir(target))
	}
	if err == nil && actual != previous {
		return errors.New("copy target is unclassified; preserving it")
	}
	if err != nil && (!errors.Is(err, os.ErrNotExist) || previous != "") {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".fased-maintenance-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(in, size+1), make([]byte, 32<<10))
	if err != nil || n != size || hex.EncodeToString(h.Sum(nil)) != hash {
		f.Close()
		return errors.New("verified copy source changed")
	}
	if err = f.Chmod(mode); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if previous == "" {
		// The directory is root-owned, but another authorized setup may race.
		return publishCopiedExclusive(f.Name(), target)
	}
	actual, _, err = ProtectedDigest(target, mode, 128<<20)
	if err != nil || actual != previous {
		return errors.New("protected target changed before copy publication")
	}
	if err = os.Rename(f.Name(), target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}

// CopyEnrollmentAsset copies only the fixed pair owned by this enrollment
// operation. Its one deterministic staging file can be resumed after a crash.
func CopyEnrollmentAsset(owner int, id, name, hash string, size int64) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || owner <= 0 || !update.IDOK(id) || (name != "fased-app" && name != "fased-signer") || size <= 0 || size > 64<<20 {
		return errors.New("fixed bounded enrollment copy required")
	}
	base := filepath.Join(OwnerRoot(owner), "fased-app", "install", "custody-"+id)
	source := filepath.Join(base, "bundle", name)
	if name == "fased-signer" {
		source = filepath.Join(base, "enrollment-signer", name)
	}
	targetDir := update.MaintenanceDirectory(owner, id)
	target := filepath.Join(targetDir, name)
	in, err := openProtected(source, size, 0755)
	if err != nil {
		return err
	}
	defer in.Close()
	if actual, n, e := ProtectedDigest(target, 0755, size); e == nil {
		if actual != hash || n != size {
			return errors.New("protected enrollment target differs; preserve it")
		}
		return syncDirectory(targetDir)
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	dir, err := os.Open(targetDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return resumeEnrollmentCopy(in, dir, name, hash, size, 0, 0)
}

func resumeEnrollmentCopy(in, dir *os.File, name, expected string, size int64, uid, gid int) error {
	if (name != "fased-app" && name != "fased-signer") || size <= 0 || size > 64<<20 {
		return errors.New("fixed enrollment copy required")
	}
	stage := "." + name + ".enrollment.pending"
	return resumeProtectedCopy(in, dir, name, stage, expected, size, 0755, uid, gid)
}

// CopyProtectedBounded keeps one content-bound staging file for a verified
// helper or signer copy. It is an internal helper, not an IPC path selector.
func CopyProtectedBounded(source, target string, mode os.FileMode, expected string, size int64) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || (mode != 0555 && mode != 0755) || size <= 0 || size > 128<<20 {
		return errors.New("bounded protected copy required")
	}
	h, err := hex.DecodeString(expected)
	if err != nil || len(h) != sha256.Size || hex.EncodeToString(h) != expected {
		return errors.New("verified protected copy hash required")
	}
	name := filepath.Base(target)
	if name != "fased-install" && name != "fased-signer" {
		return errors.New("fixed protected copy target required")
	}
	in, err := openProtected(source, size, mode)
	if err != nil {
		return err
	}
	defer in.Close()
	if actual, n, e := ProtectedDigest(target, mode, size); e == nil {
		if actual != expected || n != size {
			return errors.New("protected copy target differs; preserve it")
		}
		return syncDirectory(filepath.Dir(target))
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	dir, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer dir.Close()
	return resumeProtectedCopy(in, dir, name, "."+name+"-"+expected+".pending", expected, size, mode, 0, 0)
}

// PublishProtectedSigner bounds retention of the signer's embedded public
// executable bytes to one classified staging file for the content hash.
func PublishProtectedSigner(target string, data []byte) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || filepath.Base(target) != "fased-signer" || len(data) == 0 || len(data) > 64<<20 {
		return errors.New("bounded protected signer required")
	}
	sum := sha256.Sum256(data)
	expected := hex.EncodeToString(sum[:])
	if actual, n, err := ProtectedDigest(target, 0755, int64(len(data))); err == nil {
		if actual != expected || n != int64(len(data)) {
			return errors.New("protected signer differs; preserve it")
		}
		return syncDirectory(filepath.Dir(target))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer dir.Close()
	stage := ".fased-signer-" + expected + ".pending"
	return resumeProtectedCopy(bytes.NewReader(data), dir, "fased-signer", stage, expected, int64(len(data)), 0755, 0, 0)
}

// PublishEnrollmentRecord bounds fixed operation metadata to one resumable
// staging record. These names are not caller-selected filesystem paths.
func PublishEnrollmentRecord(owner int, id, name string, data []byte) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || owner <= 0 || !update.IDOK(id) || len(data) == 0 || len(data) > 256<<10 ||
		(name != "enrollment-acquisition-reservation.json" && name != "enrollment-acquisition.json" && name != "enrollment-package.json") {
		return errors.New("fixed enrollment record required")
	}
	sum := sha256.Sum256(data)
	expected := hex.EncodeToString(sum[:])
	dirPath := update.MaintenanceDirectory(owner, id)
	target := filepath.Join(dirPath, name)
	if actual, n, err := ProtectedDigest(target, 0644, int64(len(data))); err == nil {
		if actual != expected || n != int64(len(data)) {
			return errors.New("protected enrollment record differs; preserve it")
		}
		return syncDirectory(dirPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := os.Open(dirPath)
	if err != nil {
		return err
	}
	defer dir.Close()
	return resumeProtectedCopy(bytes.NewReader(data), dir, name, "."+name+".pending", expected, int64(len(data)), 0644, 0, 0)
}

// PublishCustodyEnrollmentIntent creates the existing private installer intent
// with one resumable stage; it never replaces an existing operation.
func PublishCustodyEnrollmentIntent(owner int, data []byte) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || owner <= 0 || len(data) == 0 || len(data) > 16384 {
		return errors.New("fixed protected installer intent required")
	}
	sum := sha256.Sum256(data)
	expected := hex.EncodeToString(sum[:])
	dirPath := filepath.Join(OwnerRoot(owner), "custody-installer")
	target := filepath.Join(dirPath, "intent.json")
	if actual, n, err := ProtectedDigest(target, 0600, int64(len(data))); err == nil {
		if actual != expected || n != int64(len(data)) {
			return errors.New("protected installer intent differs; preserve it")
		}
		return syncDirectory(dirPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := os.Open(dirPath)
	if err != nil {
		return err
	}
	defer dir.Close()
	return resumeProtectedCopy(bytes.NewReader(data), dir, "intent.json", ".intent.json.pending", expected, int64(len(data)), 0600, 0, 0)
}

func resumeProtectedCopy(in io.ReadSeeker, dir *os.File, name, stage, expected string, size int64, mode os.FileMode, uid, gid int) error {
	fd, err := unix.Openat(int(dir.Fd()), stage, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Openat(int(dir.Fd()), stage, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	}
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "enrollment-copy")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != uid || int(st.Gid) != gid || st.Nlink != 1 || st.Size < 0 || st.Size > size || (st.Mode&07777 != uint32(mode) && st.Mode&07777&^0600 != 0) {
		return errors.New("foreign enrollment copy; preserve it")
	}
	if _, err = in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	left := st.Size
	original := make([]byte, 32<<10)
	pending := make([]byte, len(original))
	for left > 0 {
		n := int64(len(original))
		if left < n {
			n = left
		}
		if _, err = io.ReadFull(in, original[:n]); err != nil {
			return err
		}
		if _, err = io.ReadFull(f, pending[:n]); err != nil {
			return err
		}
		if !bytes.Equal(original[:n], pending[:n]) {
			return errors.New("enrollment copy prefix differs; preserve it")
		}
		_, _ = h.Write(original[:n])
		left -= n
	}
	n, err := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(in, size-st.Size+1), original)
	if err != nil || n != size-st.Size || hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("verified enrollment copy differs; preserve it")
	}
	if err = f.Chmod(mode); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = unix.Renameat2(int(dir.Fd()), stage, int(dir.Fd()), name, unix.RENAME_NOREPLACE); err != nil {
		return errors.New("enrollment copy publication conflicted; preserve it")
	}
	return dir.Sync()
}
