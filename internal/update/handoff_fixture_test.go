package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

type handoffFixture struct {
	t             *testing.T
	f             *admissionFixture
	root          string
	actors        map[string]*FixtureHandoffActor
	cfg           map[string]FixtureHandoffConfig
	clocks        map[string]*atomic.Int64
	confidence    map[string]*atomic.Bool
	mu            sync.RWMutex
	inventories   map[string]FixtureHandoffInventory
	canaries      map[string]string
	reviewAllowed atomic.Bool
	reviews       atomic.Int64
}

func newHandoffFixture(t *testing.T) *handoffFixture {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("private fixture store unsupported; OS isolation is not emulated")
	}
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if evidence := os.Getenv("FASED_HANDOFF_EVIDENCE"); evidence != "" {
		parent := filepath.Join(evidence, "fixtures")
		if e = os.MkdirAll(parent, 0700); e != nil {
			t.Fatal(e)
		}
		root, e = os.MkdirTemp(parent, "handoff-")
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if e := os.RemoveAll(root); e != nil {
				t.Error(e)
			}
		})
	}
	h := &handoffFixture{t: t, f: newAdmissionFixture(t), root: root, actors: map[string]*FixtureHandoffActor{}, cfg: map[string]FixtureHandoffConfig{}, clocks: map[string]*atomic.Int64{}, confidence: map[string]*atomic.Bool{}, inventories: map[string]FixtureHandoffInventory{}, canaries: map[string]string{}}
	h.reviewAllowed.Store(true)
	instance, authority := NewProbe(), digest([]byte("disposable authority epoch"))
	for _, role := range []string{"owner", "signer"} {
		parent := filepath.Join(root, role)
		for _, dir := range []string{parent, filepath.Join(parent, "store"), filepath.Join(parent, "artifacts")} {
			if e = os.Mkdir(dir, 0700); e != nil {
				t.Fatal(e)
			}
		}
		for _, subject := range h.f.unit.Subjects {
			b, e := os.ReadFile(filepath.Join(h.f.dir, subject.File))
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(parent, "artifacts", subject.File), b, 0600); e != nil {
				t.Fatal(e)
			}
		}
		canary := filepath.Join(parent, "financial-canary")
		if e = os.WriteFile(canary, []byte("disposable unopened originals/counters/reservations/unknown-holds: "+role), 0600); e != nil {
			t.Fatal(e)
		}
		h.canaries[role] = canary
		h.inventories[role] = FixtureHandoffInventory{InstanceID: instance, AuthoritySHA256: authority, Revision: 1, InstalledFloor: h.f.seed.InstalledFloor, ReservedFloor: h.f.seed.ReservedFloor}
		clock, confidence := &atomic.Int64{}, &atomic.Bool{}
		clock.Store(h.f.now)
		confidence.Store(true)
		h.clocks[role], h.confidence[role] = clock, confidence
		store, e := OpenHandoffFixtureStore(filepath.Join(parent, "store"))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = store.Close() })
		trust := h.f.options(nil)
		trust.Initial = false
		trust.Now = 0
		admission := h.f.admissionOptions()
		admission.ArtifactDir = filepath.Join(parent, "artifacts")
		cfg := FixtureHandoffConfig{Development: true, Role: role, Store: store, Initial: true, Trust: trust, Admission: admission, Clock: func() (int64, bool) { return clock.Load(), confidence.Load() }, Inventory: func() FixtureHandoffInventory {
			h.mu.RLock()
			v := h.inventories[role]
			h.mu.RUnlock()
			b, _ := os.ReadFile(canary)
			v.CanarySHA256 = digest(b)
			return v
		}, Review: func(r FixtureHandoffReview) bool { h.reviews.Add(1); return h.reviewAllowed.Load() }}
		h.cfg[role] = cfg
		actor, e := NewFixtureHandoffActor(cfg)
		if e != nil {
			t.Fatal(e)
		}
		h.actors[role] = actor
	}
	t.Logf("disposable input fingerprints seed=%s policy=%s; keys memory-only; root=%s", digest(tj(h.f.seed)), digest(tj(h.f.policy)), root)
	return h
}

func (h *handoffFixture) session() *FixtureHandoffSession {
	h.t.Helper()
	s, e := NewFixtureHandoffSession(h.actors["owner"], h.actors["signer"])
	if e != nil {
		h.t.Fatal(e)
	}
	return s
}

func (h *handoffFixture) prepare() (*FixtureHandoffSession, []byte) {
	h.t.Helper()
	s := h.session()
	b, e := s.Prepare(context.Background())
	if e != nil {
		h.t.Fatal(e)
	}
	return s, b
}

func (h *handoffFixture) restart(role string) *FixtureHandoffActor {
	h.t.Helper()
	old := h.actors[role]
	c := h.cfg[role]
	c.Initial = false
	c.ExpectedHead = old.Head()
	store, e := OpenHandoffFixtureStore(filepath.Join(h.root, role, "store"))
	if e != nil {
		h.t.Fatal(e)
	}
	h.t.Cleanup(func() { _ = store.Close() })
	c.Store = store
	a, e := NewFixtureHandoffActor(c)
	if e != nil {
		h.t.Fatal(e)
	}
	h.actors[role] = a
	return a
}

func (h *handoffFixture) checkpoint(role string) handoffCheckpoint {
	h.t.Helper()
	a := h.actors[role]
	a.mu.Lock()
	defer a.mu.Unlock()
	_, c, e := a.load()
	if e != nil {
		h.t.Fatal(e)
	}
	return c
}

func (h *handoffFixture) mutateInventory(role string, f func(*FixtureHandoffInventory)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v := h.inventories[role]
	f(&v)
	h.inventories[role] = v
}

func fixtureOutcomeDisabled(r FixtureHandoffOutcome) bool {
	return r.Version == 1 && r.Profile == HandoffProfile && r.Mode == handoffMode && r.FixtureOnly && !r.InstallAllowed && !r.ResumeAllowed && !r.UpdaterEnabled && !r.WalletSigner && !r.FinancialExecution
}

func handoffProposal(t *testing.T, a *FixtureHandoffActor, mutate func(*handoffCheckpoint)) handoffPublication {
	t.Helper()
	old, c, e := a.load()
	if e != nil {
		t.Fatal(e)
	}
	mutate(&c)
	c.Revision++
	b, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	return handoffPublication{previous: old, next: b}
}
