package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Open only a stable regular file. A hash check still binds the bytes actually
// read; this read-only result is never permission to install a later pathname.
func openRegular(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("regular nonsymlink file required")
	}
	f, err := openReadOnly(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("file changed while opening")
	}
	return f, nil
}

func ReadDocument(path string) ([]byte, error) {
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxDocument+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxDocument {
		return nil, errors.New("document too large")
	}
	return b, nil
}

// Inspect performs no writes and never executes artifacts. Files are local;
// interrupted downloads, directories and altered bytes cannot pass.
func Inspect(manifest, trust, inventory []byte, artifactDir string, now int64) (Plan, error) {
	t, err := ParseTrust(trust, now)
	if err != nil {
		return Plan{}, err
	}
	m, sha, err := Authenticate(manifest, t, now)
	if err != nil {
		return Plan{}, err
	}
	var inv Inventory
	if err = StrictJSON(inventory, &inv); err != nil {
		return Plan{}, err
	}
	p, err := Compatible(m, sha, inv)
	if err != nil {
		return Plan{}, err
	}
	for _, c := range m.Components {
		a := c.Artifact
		f, e := openRegular(filepath.Join(artifactDir, a.File))
		if e != nil {
			return Plan{}, fmt.Errorf("artifact %s unavailable", c.Contract.Component)
		}
		st, e := f.Stat()
		if e != nil || st.Size() != a.Size {
			_ = f.Close()
			return Plan{}, errors.New("artifact size mismatch")
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(f, a.Size+1))
		closeErr := f.Close()
		if e != nil || closeErr != nil || n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
			return Plan{}, errors.New("artifact hash or complete-read mismatch")
		}
	}
	p.ArtifactsVerified = true
	return p, nil
}
