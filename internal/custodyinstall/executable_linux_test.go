//go:build linux

package custodyinstall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

type executableZeros struct {
	left    int64
	maxRead int
}

func (z *executableZeros) Read(p []byte) (int, error) {
	if len(p) > z.maxRead {
		z.maxRead = len(p)
	}
	if z.left == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > z.left {
		n = int(z.left)
	}
	clear(p[:n])
	z.left -= int64(n)
	return n, nil
}

func TestExecutableVerificationBoundedAllocation(t *testing.T) {
	const size = 32 * 1024 * 1024
	h := sha256.New()
	if _, err := io.Copy(h, &executableZeros{left: size}); err != nil {
		t.Fatal(err)
	}
	want := hex.EncodeToString(h.Sum(nil))
	r := &executableZeros{left: size}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := verifyExecutableContents(r, size, want); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated > 1024*1024 || r.maxRead > 32*1024 {
		t.Fatalf("binary-sized verification allocation: allocated=%d maxRead=%d", allocated, r.maxRead)
	}
	t.Logf("32MiB executable: allocated=%d maxRead=%d", allocated, r.maxRead)
}

type executableReadError struct{}

func (executableReadError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestExecutableVerificationRefusesChangedContents(t *testing.T) {
	data := []byte("installed binary")
	for _, tc := range []struct {
		name string
		r    io.Reader
		size int64
		hash string
	}{
		{"valid", bytes.NewReader(data), int64(len(data)), sum(data)},
		{"hash", bytes.NewReader(data), int64(len(data)), sum([]byte("other"))},
		{"truncated", bytes.NewReader(data[:3]), int64(len(data)), sum(data)},
		{"grew", bytes.NewReader(append(append([]byte{}, data...), 1)), int64(len(data)), sum(data)},
		{"read-error", executableReadError{}, int64(len(data)), sum(data)},
		{"oversize", executableReadError{}, maxInstalledExecutableSize + 1, sum(data)},
		{"negative", executableReadError{}, -1, sum(data)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyExecutableContents(tc.r, tc.size, tc.hash)
			if (err == nil) != (tc.name == "valid") {
				t.Fatal("content admission", err)
			}
			if tc.name == "read-error" && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
		})
	}
}

// Metadata fixtures do not claim privileged filesystem or installed acceptance.
type executableMetadata struct {
	mode os.FileMode
	size int64
	stat syscall.Stat_t
}

func (m executableMetadata) Name() string       { return "fased-app" }
func (m executableMetadata) Size() int64        { return m.size }
func (m executableMetadata) Mode() os.FileMode  { return m.mode }
func (m executableMetadata) ModTime() time.Time { return time.Unix(m.stat.Mtim.Sec, m.stat.Mtim.Nsec) }
func (m executableMetadata) IsDir() bool        { return m.mode.IsDir() }
func (m executableMetadata) Sys() any           { return &m.stat }

func TestExecutableVerificationMetadataAndReplacement(t *testing.T) {
	good := executableMetadata{mode: 0755, size: 123, stat: syscall.Stat_t{Uid: 0, Gid: 0, Dev: 1, Ino: 2}}
	if err := executableInfo(good); err != nil || !sameExecutable(good, good) {
		t.Fatal("valid root executable", err)
	}
	for _, name := range []string{"owner", "writable", "non-executable", "symlink", "directory", "oversize", "negative", "inode", "device", "size", "mtime", "ctime", "group"} {
		t.Run(name, func(t *testing.T) {
			bad := good
			switch name {
			case "owner":
				bad.stat.Uid = 1000
			case "writable":
				bad.mode = 0775
			case "non-executable":
				bad.mode = 0644
			case "symlink":
				bad.mode |= os.ModeSymlink
			case "directory":
				bad.mode |= os.ModeDir
			case "oversize":
				bad.size = maxInstalledExecutableSize + 1
			case "negative":
				bad.size = -1
			case "inode":
				bad.stat.Ino++
			case "device":
				bad.stat.Dev++
			case "size":
				bad.size++
			case "mtime":
				bad.stat.Mtim.Nsec++
			case "ctime":
				bad.stat.Ctim.Nsec++
			case "group":
				bad.stat.Gid++
			}
			if sameExecutable(good, bad) {
				t.Fatal("accepted changed executable metadata")
			}
		})
	}
	if err := executableInfo(executableMetadata{mode: 0755, size: maxInstalledExecutableSize, stat: good.stat}); err != nil {
		t.Fatal("previous 64MiB limit", err)
	}
}

func TestExecutableVerificationRefusesUntrustedPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fased-app")
	data := []byte("not a root installation")
	if err := os.WriteFile(p, data, 0755); err != nil {
		t.Fatal(err)
	}
	if err := verifyRootExecutable(p, sum(data)); err == nil {
		t.Fatal("accepted untrusted parent/owner")
	}
	link := p + "-link"
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if err := verifyRootExecutable(link, sum(data)); err == nil {
		t.Fatal("accepted untrusted symlink path")
	}
	b, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(b, data) {
		t.Fatal("refusal changed original", err)
	}
}
