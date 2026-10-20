package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// FixtureTrustStore is an ordinary-user disposable persistence rehearsal.
// It is never consulted by owner/signer lifecycle admission. External expected
// digests are necessary: a whole-store/anchor rollback cannot be detected here.
type FixtureTrustStore struct {
	root  *os.Root
	fault func(string) error // Package tests only; no CLI/configuration hook.
}

func OpenFixtureTrustStore(dir string) (*FixtureTrustStore, error) {
	if os.Geteuid() == 0 || !filepath.IsAbs(dir) {
		return nil, errors.New("ordinary-user absolute fixture store required")
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || st.Mode().Perm() != 0700 || !acquisitionParentOwned(st) {
		return nil, errors.New("existing real private user-owned fixture directory required")
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		return nil, e
	}
	a, e := r.Stat(".")
	if e != nil || !os.SameFile(st, a) {
		_ = r.Close()
		return nil, errors.New("fixture directory changed")
	}
	return &FixtureTrustStore{root: r}, nil
}
func (s *FixtureTrustStore) Close() error { return s.root.Close() }

func (s *FixtureTrustStore) read(name string) ([]byte, error) {
	st, e := s.root.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || !acquisitionParentOwned(st) {
		return nil, errors.New("private regular fixture file required")
	}
	f, e := s.root.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	a, e := f.Stat()
	if e != nil || !os.SameFile(st, a) {
		return nil, errors.New("fixture file changed")
	}
	b, e := io.ReadAll(io.LimitReader(f, MaxDocument+1))
	if e != nil || len(b) > MaxDocument {
		return nil, errors.New("fixture state bound exceeded")
	}
	return b, nil
}

type fixtureTrustHead struct {
	Version     int    `json:"version"`
	Purpose     string `json:"purpose"`
	SeedSHA256  string `json:"seedSha256"`
	StateSHA256 string `json:"stateSha256"`
}

func (s *FixtureTrustStore) head() (fixtureTrustHead, error) {
	var h fixtureTrustHead
	b, e := s.read("head.json")
	if e != nil {
		return h, e
	}
	if StrictJSON(b, &h) != nil || h.Version != 1 || h.Purpose != RehearsalPurpose || !hashOK(h.SeedSHA256) || !hashOK(h.StateSHA256) {
		return h, errors.New("invalid disposable trust head")
	}
	return h, nil
}

// Load requires an independently retained expected head; missing/stale state is
// never reinterpreted as initial trust. The returned bytes remain fixture input.
func (s *FixtureTrustStore) Load(seedSHA, expected string) ([]byte, error) {
	if !hashOK(seedSHA) || !hashOK(expected) {
		return nil, errors.New("explicit fixture head anchor required")
	}
	h, e := s.head()
	if e != nil {
		return nil, e
	}
	if h.SeedSHA256 != seedSHA || h.StateSHA256 != expected {
		return nil, errors.New("stale or substituted fixture head")
	}
	b, e := s.read(expected + ".json")
	if e != nil || digest(b) != expected {
		return nil, errors.New("missing/corrupt immutable fixture snapshot")
	}
	return b, nil
}

func (s *FixtureTrustStore) checkpoint(name string) error {
	if s.fault != nil {
		return s.fault(name)
	}
	return nil
}
func (s *FixtureTrustStore) sync() error {
	f, e := s.root.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

// Temporary bytes have an exact transition-derived name and must be a prefix
// of this same public fixture snapshot/head. A partial write can retry without
// rewriting any published immutable snapshot or adopting foreign bytes.
func (s *FixtureTrustStore) temporary(name string, wanted []byte, phase string) error {
	b, e := s.read(name)
	flags := os.O_WRONLY | os.O_APPEND
	if os.IsNotExist(e) {
		flags |= os.O_CREATE | os.O_EXCL
		b = nil
	} else if e != nil {
		return e
	}
	if len(b) > len(wanted) || !bytes.Equal(b, wanted[:len(b)]) {
		return errors.New("foreign/corrupt fixture temporary bytes")
	}
	f, e := s.root.OpenFile(name, flags, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if len(b) == 0 {
		n := len(wanted) / 2
		if _, e = f.Write(wanted[:n]); e != nil {
			return e
		}
		if e = f.Sync(); e != nil {
			return e
		}
		if e = s.checkpoint("partial-" + phase); e != nil {
			return e
		}
		b = wanted[:n]
	}
	if _, e = f.Write(wanted[len(b):]); e != nil {
		return e
	}
	return f.Sync()
}

// Commit accepts only the opaque transition returned by RehearseTrust. Head
// publication is CAS under an OS lock; snapshots precede the atomic head. A
// failed reply after publication can retry exactly, without an authority reset.
func (s *FixtureTrustStore) Commit(t TrustTransition) (string, error) {
	if len(t.next) == 0 || !hashOK(t.seedSHA) || t.report.NextSHA256 != digest(t.next) {
		return "", errors.New("verified disposable transition required")
	}
	// Refuse foreign directory contents before creating even the lock file.
	d, e := s.root.Open(".")
	if e != nil {
		return "", e
	}
	names, e := d.Readdirnames(-1)
	_ = d.Close()
	if e != nil {
		return "", e
	}
	if len(names) > 128 {
		return "", errors.New("disposable store file budget reached")
	}
	for _, n := range names {
		ok := n == "lock" || n == "head.json" || len(n) == 69 && hashOK(n[:64]) && n[64:] == ".json"
		if !ok {
			for _, prefix := range []string{"snapshot-", "head-"} {
				if len(n) == len(prefix)+68 && n[:len(prefix)] == prefix && hashOK(n[len(prefix):len(prefix)+64]) && n[len(n)-4:] == ".tmp" {
					ok = true
				}
			}
		}
		if !ok {
			return "", errors.New("foreign file in disposable store")
		}
	}
	flags := os.O_RDWR
	info, err := s.root.Lstat("lock")
	if os.IsNotExist(err) {
		flags |= os.O_CREATE | os.O_EXCL
	} else if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("unsafe existing fixture lock")
	}
	lock, e := s.root.OpenFile("lock", flags, 0600)
	if e != nil {
		return "", e
	}
	defer lock.Close()
	st, e := s.root.Lstat("lock")
	a, ae := lock.Stat()
	if e != nil || ae != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || !os.SameFile(st, a) || !acquisitionParentOwned(st) {
		return "", errors.New("unsafe fixture lock")
	}
	if e = lockFixtureTrust(lock); e != nil {
		return "", e
	}
	defer unlockFixtureTrust(lock)
	h, e := s.head()
	if e == nil {
		if h.SeedSHA256 != t.seedSHA {
			return "", errors.New("fixture seed conflict")
		}
		if h.StateSHA256 == t.report.NextSHA256 {
			_, e = s.Load(t.seedSHA, h.StateSHA256)
			return h.StateSHA256, e
		}
		if len(t.previous) == 0 || h.StateSHA256 != digest(t.previous) {
			return "", errors.New("stale fixture CAS")
		}
		old, e := s.Load(t.seedSHA, h.StateSHA256)
		if e != nil || string(old) != string(t.previous) {
			return "", errors.New("fixture prior snapshot mismatch")
		}
	} else if !os.IsNotExist(e) || len(t.previous) != 0 {
		return "", errors.New("missing/corrupt fixture head; preserve state")
	} else {
		// Initial retry may retain only this exact orphan snapshot from an
		// interrupted first commit; never adopt a foreign or reset store.
		f, e := s.root.Open(".")
		if e != nil {
			return "", e
		}
		names, e := f.Readdirnames(-1)
		_ = f.Close()
		if e != nil {
			return "", e
		}
		for _, n := range names {
			if n != "lock" && n != t.report.NextSHA256+".json" && n != "snapshot-"+t.report.NextSHA256+".tmp" && n != "head-"+t.report.NextSHA256+".tmp" {
				return "", errors.New("nonempty fixture store cannot initialize")
			}
		}
	}
	if e = s.checkpoint("before-snapshot"); e != nil {
		return "", e
	}
	name := t.report.NextSHA256 + ".json"
	if b, e := s.read(name); e == nil {
		if string(b) != string(t.next) {
			return "", errors.New("immutable fixture snapshot conflict")
		}
	} else if os.IsNotExist(e) {
		tmp := "snapshot-" + t.report.NextSHA256 + ".tmp"
		if e = s.temporary(tmp, t.next, "snapshot"); e != nil {
			return "", e
		}
		if e = s.root.Rename(tmp, name); e != nil {
			return "", e
		}
	} else {
		return "", e
	}
	if e = s.sync(); e != nil {
		return "", e
	}
	if e = s.checkpoint("after-snapshot"); e != nil {
		return "", e
	}
	b, _ := json.Marshal(fixtureTrustHead{Version: 1, Purpose: RehearsalPurpose, SeedSHA256: t.seedSHA, StateSHA256: t.report.NextSHA256})
	tmp := "head-" + t.report.NextSHA256 + ".tmp"
	if e = s.temporary(tmp, b, "head"); e != nil {
		return "", e
	}
	if e = s.checkpoint("before-head"); e != nil {
		return "", e
	}
	if e = s.root.Rename(tmp, "head.json"); e != nil {
		return "", e
	}
	if e = s.sync(); e != nil {
		return "", e
	}
	if e = s.checkpoint("after-head"); e != nil {
		return "", e
	}
	return t.report.NextSHA256, nil
}
