package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxHandoffCheckpoint = 256 << 10

// This ordinary-user fixture store has no installed lifecycle consumer. One
// checkpoint atomically contains trust, clock, pending learning and replay state.
type HandoffFixtureStore struct{ fs *FixtureTrustStore }

type handoffRecord struct {
	ID      string          `json:"id"`
	Channel string          `json:"channel"`
	Kind    string          `json:"kind"`
	Body    json.RawMessage `json:"body"`
}

type handoffCheckpoint struct {
	Version   int                     `json:"version"`
	Profile   string                  `json:"profile"`
	Mode      string                  `json:"mode"`
	Binding   FixtureBinding          `json:"binding"`
	Role      string                  `json:"role"`
	BootID    string                  `json:"bootId"`
	ConfigSHA string                  `json:"configSha256"`
	SeedSHA   string                  `json:"seedSha256"`
	Revision  uint64                  `json:"revision,string"`
	LastAt    int64                   `json:"lastAt"`
	Inventory FixtureHandoffInventory `json:"inventory"`
	Trust     json.RawMessage         `json:"trust,omitempty"`
	TrustSHA  string                  `json:"trustSha256"`
	Active    string                  `json:"active"`
	Pending   string                  `json:"pending"`
	Records   []handoffRecord         `json:"records"`
}

// Not reconstructible from an RPC/JSON approval. Trust changes additionally
// require the opaque transition produced by the existing verifier.
type handoffPublication struct {
	previous, next []byte
	proof          *TrustTransition
}

type handoffHead struct {
	Version   int    `json:"version"`
	Profile   string `json:"profile"`
	ConfigSHA string `json:"configSha256"`
	StateSHA  string `json:"stateSha256"`
}

func OpenHandoffFixtureStore(dir string) (*HandoffFixtureStore, error) {
	real, e := filepath.EvalSymlinks(dir)
	if e != nil || !filepath.IsAbs(dir) || real != filepath.Clean(dir) {
		return nil, errors.New("real absolute fixture store required")
	}
	parent, e := os.Stat(filepath.Dir(dir))
	if e != nil || parent.Mode().Perm() != 0700 || !acquisitionParentOwned(parent) {
		return nil, errors.New("private owner-controlled fixture parent required")
	}
	f, e := OpenFixtureTrustStore(dir)
	if e != nil {
		return nil, e
	}
	f.limit = maxHandoffCheckpoint
	return &HandoffFixtureStore{fs: f}, nil
}

func (s *HandoffFixtureStore) Close() error { return s.fs.Close() }

func parseHandoffCheckpoint(b []byte) (handoffCheckpoint, error) {
	var c handoffCheckpoint
	if strictJSONBounded(b, &c, maxHandoffCheckpoint) != nil || c.Version != 1 || c.Profile != HandoffProfile || c.Mode != handoffMode || !c.Binding.valid() || c.Role != "owner" && c.Role != "signer" || !IDOK(c.BootID) || !hashOK(c.ConfigSHA) || !hashOK(c.SeedSHA) || c.Revision == 0 || c.LastAt <= 0 || c.LastAt > 4102444800 || !c.Inventory.valid() || len(c.Records) > 32 || c.Active != "" && !IDOK(c.Active) || c.Pending != "" && !IDOK(c.Pending) {
		return c, errors.New("invalid combined fixture checkpoint")
	}
	if len(c.Trust) > 0 {
		var trust TrustFixtureSnapshot
		if len(c.Trust) > MaxDocument || digest(c.Trust) != c.TrustSHA || StrictJSON(c.Trust, &trust) != nil || trust.FixtureBinding != c.Binding || trust.SeedSHA256 != c.SeedSHA || trust.ObservedAt > c.LastAt || trust.InstalledFloor != c.Inventory.InstalledFloor || trust.ReservedFloor != c.Inventory.ReservedFloor {
			return c, errors.New("combined trust/fixture identity mismatch")
		}
	} else if c.TrustSHA != "" {
		return c, errors.New("missing embedded trust")
	}
	seen, pending := map[string]bool{}, 0
	for _, r := range c.Records {
		encoded, _ := json.Marshal(r)
		if !IDOK(r.ID) || !IDOK(r.Channel) || seen[r.ID] || len(encoded) > 4096 || !json.Valid(r.Body) {
			return c, errors.New("invalid bounded fixture replay record")
		}
		seen[r.ID] = true
		switch r.Kind {
		case "verification-pending":
			pending++
			if c.Pending != r.ID || c.Active != r.Channel {
				return c, errors.New("pending learning identity mismatch")
			}
		case "installed-admission-model":
			if !validInstalledAdmissionModelRecord(r.Body, r.ID, r.Channel) {
				return c, errors.New("invalid non-installed admission model record")
			}
		case "verified", "rejected", "challenge", "reviewed", "checked", "inspection-complete":
		default:
			return c, errors.New("unsupported fixture replay outcome")
		}
	}
	if c.Pending == "" && pending != 0 || c.Pending != "" && pending != 1 {
		return c, errors.New("unknown interrupted fixture verification")
	}
	return c, nil
}

func (s *HandoffFixtureStore) head() (handoffHead, error) {
	var h handoffHead
	b, e := s.fs.read("head.json")
	if e != nil {
		return h, e
	}
	if StrictJSON(b, &h) != nil || h.Version != 1 || h.Profile != HandoffProfile || !hashOK(h.ConfigSHA) || !hashOK(h.StateSHA) {
		return h, errors.New("invalid handoff fixture head")
	}
	return h, nil
}

// An independently retained expected head is mandatory. No missing-state reset.
func (s *HandoffFixtureStore) Load(configSHA, expected string) ([]byte, error) {
	h, e := s.head()
	if e != nil || !hashOK(configSHA) || !hashOK(expected) || h.ConfigSHA != configSHA || h.StateSHA != expected {
		return nil, errors.New("missing, corrupt or stale handoff anchor")
	}
	b, e := s.fs.read(expected + ".json")
	if e != nil || digest(b) != expected {
		return nil, errors.New("missing/corrupt handoff checkpoint")
	}
	c, e := parseHandoffCheckpoint(b)
	if e != nil || c.ConfigSHA != configSHA {
		return nil, errors.New("handoff checkpoint binding refused")
	}
	return b, nil
}

func (s *HandoffFixtureStore) names() ([]string, error) {
	d, e := s.fs.root.Open(".")
	if e != nil {
		return nil, e
	}
	defer d.Close()
	names, e := d.Readdirnames(257)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(names) > 256 {
		return nil, errors.New("fixture checkpoint file budget reached")
	}
	for _, n := range names {
		ok := n == "head.json" || n == "lock" || len(n) == 69 && hashOK(n[:64]) && strings.HasSuffix(n, ".json")
		for _, prefix := range []string{"snapshot-", "head-"} {
			if len(n) == len(prefix)+68 && strings.HasPrefix(n, prefix) && hashOK(n[len(prefix):len(prefix)+64]) && strings.HasSuffix(n, ".tmp") {
				ok = true
			}
		}
		if !ok {
			return nil, errors.New("foreign handoff fixture path")
		}
	}
	return names, nil
}

func (s *HandoffFixtureStore) commit(t handoffPublication) (string, error) {
	c, e := parseHandoffCheckpoint(t.next)
	if e != nil {
		return "", e
	}
	if len(t.previous) > 0 {
		old, e := parseHandoffCheckpoint(t.previous)
		if e != nil || old.Revision == ^uint64(0) || c.Revision != old.Revision+1 || c.ConfigSHA != old.ConfigSHA || c.Role != old.Role || c.Binding != old.Binding || c.SeedSHA != old.SeedSHA || c.Inventory != old.Inventory || c.LastAt < old.LastAt {
			return "", errors.New("checkpoint identity/revision/time conflict")
		}
		if !bytes.Equal(old.Trust, c.Trust) && (t.proof == nil || !bytes.Equal(t.proof.previous, old.Trust) || !bytes.Equal(t.proof.next, c.Trust) || t.proof.seedSHA != c.SeedSHA || t.proof.report.NextSHA256 != c.TrustSHA) {
			return "", errors.New("opaque trust transition required")
		}
	} else if c.Revision != 1 || len(c.Trust) != 0 || len(c.Records) != 0 || c.Pending != "" || c.Active != "" {
		return "", errors.New("explicit empty fixture bootstrap required")
	}
	if _, e = s.names(); e != nil {
		return "", e
	}
	flags := os.O_RDWR
	if _, e = s.fs.root.Lstat("lock"); os.IsNotExist(e) {
		flags |= os.O_CREATE | os.O_EXCL
	} else if e != nil {
		return "", e
	}
	f, e := s.fs.root.OpenFile("lock", flags, 0600)
	if e != nil {
		return "", e
	}
	defer f.Close()
	a, e := f.Stat()
	b, be := s.fs.root.Lstat("lock")
	if e != nil || be != nil || !a.Mode().IsRegular() || a.Mode().Perm() != 0600 || !acquisitionParentOwned(a) || !os.SameFile(a, b) {
		return "", errors.New("unsafe handoff fixture lock")
	}
	if e = lockFixtureTrust(f); e != nil {
		return "", e
	}
	defer unlockFixtureTrust(f)
	names, e := s.names()
	if e != nil {
		return "", e
	}
	nextSHA := digest(t.next)
	h, e := s.head()
	if e == nil {
		if h.ConfigSHA != c.ConfigSHA {
			return "", errors.New("fixture configuration conflict")
		}
		if h.StateSHA == nextSHA {
			_, e = s.Load(c.ConfigSHA, nextSHA)
			return nextSHA, e
		}
		old, le := s.Load(c.ConfigSHA, digest(t.previous))
		if le != nil || !bytes.Equal(old, t.previous) {
			return "", errors.New("stale handoff CAS")
		}
	} else if !os.IsNotExist(e) || len(t.previous) > 0 {
		return "", errors.New("preserve missing/corrupt handoff head")
	} else {
		for _, n := range names {
			if n != "lock" && n != nextSHA+".json" && n != "snapshot-"+nextSHA+".tmp" && n != "head-"+nextSHA+".tmp" {
				return "", errors.New("nonempty fixture bootstrap refused")
			}
		}
	}
	// Reserve transient snapshot/head names before writing. Otherwise an
	// interrupted publication at the cap could make its own retry unreachable.
	present := map[string]bool{}
	for _, name := range names {
		present[name] = true
	}
	additional := 0
	if !present[nextSHA+".json"] && !present["snapshot-"+nextSHA+".tmp"] {
		additional++
	}
	if !present["head-"+nextSHA+".tmp"] {
		additional++
	}
	if len(names)+additional > 256 {
		return "", errors.New("fixture file budget must reserve publication/retry space")
	}
	if e = s.fs.checkpoint("before-snapshot"); e != nil {
		return "", e
	}
	if b, e := s.fs.read(nextSHA + ".json"); e == nil {
		if !bytes.Equal(b, t.next) {
			return "", errors.New("immutable checkpoint conflict")
		}
	} else if os.IsNotExist(e) {
		tmp := "snapshot-" + nextSHA + ".tmp"
		if e = s.fs.temporary(tmp, t.next, "snapshot"); e != nil {
			return "", e
		}
		if e = s.fs.root.Rename(tmp, nextSHA+".json"); e != nil {
			return "", e
		}
	} else {
		return "", e
	}
	if e = s.fs.sync(); e != nil {
		return "", e
	}
	if e = s.fs.checkpoint("after-snapshot"); e != nil {
		return "", e
	}
	head, _ := json.Marshal(handoffHead{Version: 1, Profile: HandoffProfile, ConfigSHA: c.ConfigSHA, StateSHA: nextSHA})
	tmp := "head-" + nextSHA + ".tmp"
	if e = s.fs.temporary(tmp, head, "head"); e != nil {
		return "", e
	}
	if e = s.fs.checkpoint("before-head"); e != nil {
		return "", e
	}
	if e = s.fs.root.Rename(tmp, "head.json"); e != nil {
		return "", e
	}
	if e = s.fs.sync(); e != nil {
		return "", e
	}
	if e = s.fs.checkpoint("after-head"); e != nil {
		return "", e
	}
	return nextSHA, nil
}
