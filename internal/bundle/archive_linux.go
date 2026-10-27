//go:build linux

package bundle

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const archiveMaxFiles = 16
const archiveMaxExpanded = 256 * 1024 * 1024
const tarBlockSize = 512

// ExtractVerifiedArchive unpacks an independently authenticated Linux archive.
// It authenticates no publisher itself, never executes bytes, and refuses to
// adopt any existing destination, including an interrupted previous attempt.
func ExtractVerifiedArchive(archivePath, directory string, expected File, manifestSHA256 string) (manifest Manifest, err error) {
	if !archiveDigest(expected.SHA256) || expected.Bytes <= 0 || expected.Bytes > maxFileBytes || !archiveDigest(manifestSHA256) {
		return manifest, errors.New("archive and inner manifest require bounded, canonical identities")
	}
	source, size, err := openRegular(archivePath, maxFileBytes)
	if err != nil {
		return manifest, err
	}
	defer source.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(source, maxFileBytes+1))
	if err != nil || n != size || n != expected.Bytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return manifest, errors.New("archive does not match the authenticated identity")
	}
	if _, err = source.Seek(0, io.SeekStart); err != nil {
		return manifest, err
	}
	parent, name, err := archiveParent(directory)
	if err != nil {
		return manifest, err
	}
	defer parent.Close()
	if err = parent.Mkdir(name, 0700); err != nil {
		return manifest, errors.New("archive output must not already exist")
	}
	owned, err := parent.Lstat(name)
	if err != nil {
		return manifest, err
	}
	output, err := parent.OpenRoot(name)
	if err != nil {
		// No contents exist yet. Remove only the inode we just created.
		if current, e := parent.Lstat(name); e == nil && os.SameFile(owned, current) {
			_ = parent.Remove(name)
		}
		return manifest, err
	}
	defer output.Close()
	defer func() {
		if err != nil {
			if cleanupErr := archiveCleanup(parent, output, name, owned); cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()
	if err = archiveSameOutput(parent, output, name, owned); err != nil {
		return manifest, err
	}
	// Bind the compressed bytes actually decoded as well as the preflight read.
	// A source changed after its first hash cannot produce runnable output.
	hash.Reset()
	compressed := &archiveHashReader{reader: io.TeeReader(io.LimitReader(source, maxFileBytes+1), hash)}
	buffered := bufio.NewReader(compressed)
	gz, err := gzip.NewReader(buffered)
	if err != nil {
		return manifest, err
	}
	defer gz.Close()
	gz.Multistream(false)
	entries, err := archiveExtractTar(gz, output)
	if err != nil {
		return manifest, err
	}
	// archiveExtractTar consumes exactly two end blocks, then gzip must finish
	// with a valid checksum and no further uncompressed or compressed data.
	var extra [1]byte
	if _, e := io.ReadFull(gz, extra[:]); e != io.EOF {
		return manifest, errors.New("archive has trailing tar data or an invalid gzip trailer")
	}
	if _, e := buffered.ReadByte(); e != io.EOF {
		return manifest, errors.New("archive has trailing compressed data")
	}
	if compressed.bytes != expected.Bytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return manifest, errors.New("archive changed during extraction")
	}
	if current, e := source.Stat(); e != nil || current.Size() != expected.Bytes {
		return manifest, errors.New("archive changed size during extraction")
	}
	// The held directory FD prevents a renamed path from redirecting the
	// existing verifier. Linux /proc is required; failure is closed.
	directoryFD, err := output.Open(".")
	if err != nil {
		return manifest, err
	}
	defer directoryFD.Close()
	manifest, err = VerifyPinned("/proc/self/fd/"+strconv.FormatUint(uint64(directoryFD.Fd()), 10), manifestSHA256)
	if err != nil {
		return manifest, err
	}
	if len(entries) != len(manifest.Files)+1 || !entries["bundle.json"] {
		return manifest, errors.New("archive inventory does not exactly match its manifest")
	}
	for name := range entries {
		if name != "bundle.json" {
			if _, present := manifest.Files[name]; !present {
				return manifest, errors.New("archive contains an uninventoried file")
			}
		}
	}
	if err = archiveSameOutput(parent, output, name, owned); err != nil {
		return manifest, err
	}
	engine, desktop := Names()
	executables := []string{engine}
	if manifest.Desktop {
		executables = append(executables, desktop)
	}
	for _, name := range executables {
		file, e := output.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			return manifest, e
		}
		e = file.Chmod(0700)
		if closeErr := file.Close(); e == nil {
			e = closeErr
		}
		if e != nil {
			return manifest, e
		}
	}
	if err = directoryFD.Sync(); err != nil {
		return manifest, err
	}
	err = archiveSameOutput(parent, output, name, owned)
	return manifest, err
}

func archiveDigest(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == value
}

type archiveHashReader struct {
	reader io.Reader
	bytes  int64
}

func (r *archiveHashReader) Read(b []byte) (int, error) {
	n, err := r.reader.Read(b)
	r.bytes += int64(n)
	return n, err
}

func archiveParent(directory string) (*os.Root, string, error) {
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, "", err
	}
	name := filepath.Base(abs)
	parentName := filepath.Dir(abs)
	if name == "." || name == string(filepath.Separator) || parentName == abs {
		return nil, "", errors.New("archive output must be a new child directory")
	}
	real, err := filepath.EvalSymlinks(parentName)
	if err != nil || real != parentName {
		return nil, "", errors.New("archive parent must be a real private directory")
	}
	info, err := os.Lstat(parentName)
	if err != nil || !archivePrivateDirectory(info) {
		return nil, "", errors.New("archive parent must be current-user owned and mode 0700")
	}
	parent, err := os.OpenRoot(parentName)
	if err != nil {
		return nil, "", err
	}
	opened, err := parent.Stat(".")
	if err != nil || !os.SameFile(info, opened) || !archivePrivateDirectory(opened) {
		parent.Close()
		return nil, "", errors.New("archive parent changed while opening")
	}
	return parent, name, nil
}

func archivePrivateDirectory(info os.FileInfo) bool {
	if info == nil || !info.IsDir() || info.Mode() != os.ModeDir|0700 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

func archiveSameOutput(parent, output *os.Root, name string, owned os.FileInfo) error {
	current, err := parent.Lstat(name)
	opened, openedErr := output.Stat(".")
	if err != nil || openedErr != nil || !os.SameFile(owned, current) || !os.SameFile(owned, opened) || !archivePrivateDirectory(current) || !archivePrivateDirectory(opened) {
		return errors.New("archive output identity or privacy changed")
	}
	return nil
}

func archiveCleanup(parent, output *os.Root, name string, owned os.FileInfo) error {
	if err := archiveSameOutput(parent, output, name, owned); err != nil {
		return errors.New("archive output changed; refused cleanup of a foreign directory")
	}
	entries, err := fs.ReadDir(output.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// The extractor creates no subdirectories. Never recurse into a foreign
		// subtree introduced after creation, even during failure cleanup.
		if entry.IsDir() {
			return errors.New("archive output contains a foreign directory; cleanup refused")
		}
		if err := output.Remove(entry.Name()); err != nil {
			return err
		}
	}
	if err := archiveSameOutput(parent, output, name, owned); err != nil {
		return err
	}
	return parent.Remove(name)
}

func archiveExtractTar(reader io.Reader, output *os.Root) (map[string]bool, error) {
	prefix := "fased-app-linux-" + runtime.GOARCH
	entries := make(map[string]bool)
	var total int64
	rootHeader := false
	for {
		var block [tarBlockSize]byte
		if _, err := io.ReadFull(reader, block[:]); err != nil {
			return nil, fmt.Errorf("truncated tar header: %w", err)
		}
		if bytes.Equal(block[:], make([]byte, tarBlockSize)) {
			if _, err := io.ReadFull(reader, block[:]); err != nil || !bytes.Equal(block[:], make([]byte, tarBlockSize)) {
				return nil, errors.New("archive must end with two zero tar blocks")
			}
			return entries, nil
		}
		// Parsing one physical header at a time prevents archive/tar from
		// silently accepting PAX or GNU extension records as logical files.
		kind := block[156]
		if kind != tar.TypeReg && kind != tar.TypeRegA && kind != tar.TypeDir {
			return nil, errors.New("archive contains a forbidden member type")
		}
		header, err := tar.NewReader(bytes.NewReader(block[:])).Next()
		if err != nil || header.Format != tar.FormatUSTAR || header.Linkname != "" || len(header.PAXRecords) != 0 {
			return nil, errors.New("archive contains an invalid or extended tar header")
		}
		if kind == tar.TypeDir {
			if rootHeader || len(entries) != 0 || header.Size != 0 || header.Name != prefix && header.Name != prefix+"/" {
				return nil, errors.New("only one initial bundle-root directory header is allowed")
			}
			rootHeader = true
			continue
		}
		name, ok := strings.CutPrefix(header.Name, prefix+"/")
		if !ok || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || entries[name] {
			return nil, errors.New("archive member path is non-flat, duplicated or outside the bundle")
		}
		if len(entries) >= archiveMaxFiles || header.Size < 0 || header.Size > maxFileBytes || header.Size > archiveMaxExpanded-total {
			return nil, errors.New("archive member count or expanded size exceeds its bound")
		}
		if name == "bundle.json" && header.Size > maxManifestBytes {
			return nil, errors.New("archive manifest exceeds its bound")
		}
		total += header.Size
		entries[name] = true
		file, err := output.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return nil, err
		}
		err = file.Chmod(0600)
		if err == nil {
			_, err = io.CopyN(file, reader, header.Size)
		}
		if err == nil {
			err = file.Sync()
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, fmt.Errorf("truncated or unwritable archive member: %w", err)
		}
		padding := (tarBlockSize - header.Size%tarBlockSize) % tarBlockSize
		if _, err := io.ReadFull(reader, block[:padding]); err != nil || !bytes.Equal(block[:padding], make([]byte, padding)) {
			return nil, errors.New("archive member has invalid or truncated padding")
		}
	}
}
