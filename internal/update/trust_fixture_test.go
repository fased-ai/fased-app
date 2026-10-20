package update

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

type trustFixture struct {
	t         *testing.T
	now       int64
	seed      TrustFixtureSeed
	root      *metadata.Metadata[metadata.RootType]
	tg        *metadata.Metadata[metadata.TargetsType]
	sn        *metadata.Metadata[metadata.SnapshotType]
	ts        *metadata.Metadata[metadata.TimestampType]
	keys      map[string][]signature.Signer
	files     map[string][]byte
	redirects map[string]string
	mu        sync.RWMutex
	server    *httptest.Server
	ca        []byte
}

func tj(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func (f *trustFixture) bytes(v interface{ ToBytes(bool) ([]byte, error) }) []byte {
	f.t.Helper()
	b, e := v.ToBytes(false)
	if e != nil {
		f.t.Fatal(e)
	}
	return b
}
func (f *trustFixture) key() (signature.Signer, *metadata.Key) {
	f.t.Helper()
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		f.t.Fatal(e)
	}
	s, e := signature.LoadSigner(priv, crypto.Hash(0))
	if e != nil {
		f.t.Fatal(e)
	}
	k, e := metadata.KeyFromPublicKey(pub)
	if e != nil {
		f.t.Fatal(e)
	}
	return s, k
}
func (f *trustFixture) rootSign(extra ...signature.Signer) []byte {
	f.root.ClearSignatures()
	for _, s := range append(append([]signature.Signer{}, f.keys["root"][:2]...), extra...) {
		if _, e := f.root.Sign(s); e != nil {
			f.t.Fatal(e)
		}
	}
	return f.bytes(f.root)
}
func (f *trustFixture) rotate(role string) []signature.Signer {
	old := f.keys[role]
	for _, id := range f.root.Signed.Roles[role].KeyIDs {
		delete(f.root.Signed.Keys, id)
	}
	f.root.Signed.Roles[role].KeyIDs = nil
	f.keys[role] = nil
	n := 1
	if role == "root" || role == "targets" {
		n = 3
	}
	for i := 0; i < n; i++ {
		s, k := f.key()
		if e := f.root.Signed.AddKey(k, role); e != nil {
			f.t.Fatal(e)
		}
		f.keys[role] = append(f.keys[role], s)
	}
	return old
}
func (f *trustFixture) put(name string, b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[name] = append([]byte(nil), b...)
}
func (f *trustFixture) get(name string) []byte {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return append([]byte(nil), f.files[name]...)
}
func tm(version int64, b []byte) *metadata.MetaFiles {
	m := metadata.MetaFile(version)
	sum := sha256.Sum256(b)
	m.Length = int64(len(b))
	m.Hashes = metadata.Hashes{"sha256": metadata.HexBytes(sum[:])}
	return m
}
func (f *trustFixture) release(version int64, seq uint64) {
	d := TrustFixtureDescriptor{FixtureBinding: f.seed.FixtureBinding, Version: 1, Sequence: seq, PreserveSHA256: f.seed.PreserveSHA256, EvidenceSHA256: digest([]byte("inert fixture evidence"))}
	db := tj(d)
	tg := metadata.Targets(time.Unix(f.now+600, 0))
	tg.Signed.Version = version
	tg.Signed.UnrecognizedFields = map[string]any{"fasedFixture": f.seed.FixtureBinding}
	target, e := metadata.TargetFile().FromBytes(fixtureTarget, db, "sha256")
	if e != nil {
		f.t.Fatal(e)
	}
	tg.Signed.Targets[fixtureTarget] = target
	for _, s := range f.keys["targets"][:2] {
		if _, e = tg.Sign(s); e != nil {
			f.t.Fatal(e)
		}
	}
	tb := f.bytes(tg)
	sn := metadata.Snapshot(time.Unix(f.now+600, 0))
	sn.Signed.Version = version
	sn.Signed.UnrecognizedFields = map[string]any{"fasedFixture": f.seed.FixtureBinding}
	sn.Signed.Meta = map[string]*metadata.MetaFiles{"targets.json": tm(version, tb)}
	if _, e = sn.Sign(f.keys["snapshot"][0]); e != nil {
		f.t.Fatal(e)
	}
	sb := f.bytes(sn)
	ts := metadata.Timestamp(time.Unix(f.now+600, 0))
	ts.Signed.Version = version
	ts.Signed.UnrecognizedFields = map[string]any{"fasedFixture": f.seed.FixtureBinding}
	ts.Signed.Meta = map[string]*metadata.MetaFiles{"snapshot.json": tm(version, sb)}
	if _, e = ts.Sign(f.keys["timestamp"][0]); e != nil {
		f.t.Fatal(e)
	}
	f.put("timestamp.json", f.bytes(ts))
	f.put(formatVersion(version)+".snapshot.json", sb)
	f.put(formatVersion(version)+".targets.json", tb)
	f.put("targets/fixture/"+digest(db)+".descriptor.json", db)
	f.tg, f.sn, f.ts = tg, sn, ts
}

func (f *trustFixture) publishTargets(tb []byte) {
	f.sn.Signed.Meta = map[string]*metadata.MetaFiles{"targets.json": tm(f.tg.Signed.Version, tb)}
	f.sn.ClearSignatures()
	if _, e := f.sn.Sign(f.keys["snapshot"][0]); e != nil {
		f.t.Fatal(e)
	}
	sb := f.bytes(f.sn)
	f.ts.Signed.Meta = map[string]*metadata.MetaFiles{"snapshot.json": tm(f.sn.Signed.Version, sb)}
	f.ts.ClearSignatures()
	if _, e := f.ts.Sign(f.keys["timestamp"][0]); e != nil {
		f.t.Fatal(e)
	}
	f.put("timestamp.json", f.bytes(f.ts))
	f.put(formatVersion(f.sn.Signed.Version)+".snapshot.json", sb)
	f.put(formatVersion(f.tg.Signed.Version)+".targets.json", tb)
}
func newTrustFixture(t *testing.T) *trustFixture {
	t.Helper()
	f := &trustFixture{t: t, now: 1800000000, keys: map[string][]signature.Signer{}, files: map[string][]byte{}, redirects: map[string]string{}}
	f.seed = TrustFixtureSeed{FixtureBinding: FixtureBinding{FixtureOnly: true, Purpose: RehearsalPurpose, Repository: NewProbe(), Channel: "development"}, Version: 1, CreatedAt: f.now - 20, PreserveSHA256: digest([]byte("unopened originals/counters/holds")), InstalledFloor: 3, ReservedFloor: 4}
	f.root = metadata.Root(time.Unix(f.now+86400, 0))
	f.root.Signed.UnrecognizedFields = map[string]any{"fasedFixture": f.seed.FixtureBinding}
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		f.rotate(role)
		if role == "root" || role == "targets" {
			f.root.Signed.Roles[role].Threshold = 2
		}
	}
	f.seed.Root = f.rootSign()
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Error("unexpected query")
		}
		if len(r.URL.Path) < 5 || r.URL.Path[:5] != "/tuf/" {
			http.NotFound(w, r)
			return
		}
		f.mu.RLock()
		redirect := f.redirects[r.URL.Path[5:]]
		f.mu.RUnlock()
		if redirect != "" {
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}
		b := f.get(r.URL.Path[5:])
		if b == nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(f.server.Close)
	f.seed.Origin = f.server.URL
	f.ca = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw})
	f.release(1, 10)
	return f
}
func formatVersion(v int64) string { return strconv.FormatInt(v, 10) }
func (f *trustFixture) options(previous *TrustTransition) TrustRehearsalOptions {
	b := tj(f.seed)
	o := TrustRehearsalOptions{Development: true, Initial: previous == nil, Seed: b, SeedSHA256: digest(b), Now: f.now, TLSCA: f.ca}
	if previous != nil {
		o.Previous = append([]byte(nil), previous.next...)
		o.PreviousSHA256 = digest(o.Previous)
	}
	return o
}
func (f *trustFixture) run(previous *TrustTransition) TrustTransition {
	f.t.Helper()
	r, e := RehearseTrust(context.Background(), f.options(previous))
	if e != nil {
		f.t.Fatal(e)
	}
	return r
}
