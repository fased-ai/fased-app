package update

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestFixtureHandoffBindings(t *testing.T) {
	if _, err := NewFixtureHandoffActor(FixtureHandoffConfig{}); err == nil {
		t.Fatal("implicit fixture authority accepted")
	}
	t.Run("HA-01-complete-independent-inspection", func(t *testing.T) {
		h := newHandoffFixture(t)
		s, b := h.prepare()
		r, e := s.Check(context.Background(), b)
		if e != nil {
			t.Fatal(e)
		}
		out, e := s.Finish(context.Background(), r)
		if e != nil || out.Outcome != "inspection-complete" || !fixtureOutcomeDisabled(out) {
			t.Fatal("fixture inspection refused or acquired authority", e, out)
		}
		if h.reviews.Load() != 1 || h.checkpoint("owner").TrustSHA == "" || h.checkpoint("signer").TrustSHA == "" {
			t.Fatal("missing independent review/verification")
		}
		status, e := h.actors["owner"].Status(s.request.ID)
		if e != nil || !strings.Contains(string(status), "inspection-complete") {
			t.Fatal("missing durable completion", e)
		}
	})
	for _, field := range []string{"unit", "authorization", "sequence", "platform", "owner-boot", "signer-boot", "epoch", "snapshot", "head", "nonce", "profile", "mode", "capability", "version"} {
		t.Run("HA-04-request-"+field, func(t *testing.T) {
			h := newHandoffFixture(t)
			s, b := h.prepare()
			var r FixtureHandoffRequest
			_ = json.Unmarshal(b, &r)
			switch field {
			case "unit":
				r.UnitID = digest([]byte("substitution"))
			case "authorization":
				r.AuthorizationID = digest([]byte("other"))
			case "sequence":
				r.Sequence++
			case "platform":
				r.Target = Target{"darwin", "arm64"}
			case "owner-boot":
				r.OwnerBootID = NewProbe()
			case "signer-boot":
				r.Challenge.SignerBootID = NewProbe()
			case "epoch":
				r.Inventory.AuthoritySHA256 = digest([]byte("other"))
			case "snapshot":
				r.Inventory.Revision++
			case "head":
				r.OwnerHeadSHA256 = digest([]byte("other"))
			case "nonce":
				r.Challenge.ID = NewProbe()
			case "profile":
				r.Challenge.Profile = "production"
			case "mode":
				r.Challenge.Mode = "install"
			case "capability":
				r.Challenge.Capability = "lifecycle-4"
			case "version":
				r.Challenge.Version++
			}
			if _, e := s.Check(context.Background(), tj(r)); e == nil {
				t.Fatal("substituted request admitted")
			}
			if _, e := s.Check(context.Background(), b); e == nil {
				t.Fatal("rejected challenge reused")
			}
		})
	}
	t.Run("HA-03-model-channel-role-refusal", func(t *testing.T) {
		h := newHandoffFixture(t)
		if _, e := NewFixtureHandoffSession(h.actors["signer"], h.actors["owner"]); e == nil {
			t.Fatal("app/incorrect role model channel")
		}
		if _, e := NewFixtureHandoffSession(nil, h.actors["signer"]); e == nil {
			t.Fatal("missing actor")
		}
	})
	t.Run("HA-04-tuple-unambiguous", func(t *testing.T) {
		if hashTuple("x", "a", "bc") == hashTuple("x", "ab", "c") || hashTuple("x", "a") == hashTuple("y", "a") {
			t.Fatal("ambiguous tuple")
		}
	})
}

func TestFixtureHandoffIndependentInputs(t *testing.T) {
	for _, which := range []string{"seed-pin", "policy-pin", "policy-source", "time-confidence", "missing-review", "implicit-development"} {
		t.Run("HA-02-"+which, func(t *testing.T) {
			h := newHandoffFixture(t)
			c := h.cfg["owner"]
			c.Initial = false
			c.ExpectedHead = h.actors["owner"].Head()
			switch which {
			case "seed-pin":
				c.Trust.SeedSHA256 = digest([]byte("other"))
			case "policy-pin":
				c.Admission.PolicySHA256 = digest([]byte("other"))
			case "policy-source":
				c.Admission.Policy = append([]byte(nil), c.Admission.Policy...)
				c.Admission.Policy[0] ^= 1
			case "time-confidence":
				c.Clock = func() (int64, bool) { return h.f.now, false }
			case "missing-review":
				c.Review = nil
			case "implicit-development":
				c.Development = false
			}
			if _, e := NewFixtureHandoffActor(c); e == nil {
				t.Fatal("untrusted constructor input adopted")
			}
		})
	}
	t.Run("HA-02-shared-store", func(t *testing.T) {
		h := newHandoffFixture(t)
		h.actors["signer"].cfg.Store = h.actors["owner"].cfg.Store
		if _, e := NewFixtureHandoffSession(h.actors["owner"], h.actors["signer"]); e == nil {
			t.Fatal("shared store accepted")
		}
	})
	t.Run("HA-02-app-report", func(t *testing.T) {
		h := newHandoffFixture(t)
		s, _ := h.prepare()
		if _, e := s.Check(context.Background(), []byte(`{"verified":true,"installAllowed":true}`)); e == nil {
			t.Fatal("app report became approval")
		}
	})
	t.Run("HA-02-trusted-review-refuses", func(t *testing.T) {
		h := newHandoffFixture(t)
		h.reviewAllowed.Store(false)
		if _, e := h.session().Prepare(context.Background()); e == nil {
			t.Fatal("review bypass")
		}
	})
	t.Run("HA-04-signer-owned-artifact", func(t *testing.T) {
		h := newHandoffFixture(t)
		s, b := h.prepare()
		path := filepath.Join(h.cfg["signer"].Admission.ArtifactDir, "signer.fixture")
		v, _ := os.ReadFile(path)
		v[len(v)-1] ^= 1
		if e := os.WriteFile(path, v, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Check(context.Background(), b); e == nil {
			t.Fatal("signer trusted owner's artifact decision")
		}
		c := h.checkpoint("signer")
		if c.TrustSHA == "" || c.Pending != "" {
			t.Fatal("lost learned trust on artifact failure")
		}
	})
	t.Run("HA-03-private-parent", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Unix permissions")
		}
		root := t.TempDir()
		if e := os.Chmod(root, 0755); e != nil {
			t.Fatal(e)
		}
		d := filepath.Join(root, "store")
		_ = os.Mkdir(d, 0700)
		if _, e := OpenHandoffFixtureStore(d); e == nil {
			t.Fatal("nonprivate parent accepted")
		}
	})
}

func TestFixtureHandoffClockAndReplay(t *testing.T) {
	for _, which := range []string{"skew", "backward", "uncertain", "deadline"} {
		t.Run("HA-05-"+which, func(t *testing.T) {
			h := newHandoffFixture(t)
			s, b := h.prepare()
			switch which {
			case "skew":
				h.clocks["signer"].Add(31)
			case "backward":
				h.clocks["signer"].Add(-1)
			case "uncertain":
				h.confidence["signer"].Store(false)
			case "deadline":
				h.clocks["signer"].Add(60)
			}
			if _, e := s.Check(context.Background(), b); e == nil {
				t.Fatal("invalid role-local clock accepted")
			}
		})
	}
	t.Run("HA-07-restart-invalidates-old-boot", func(t *testing.T) {
		h := newHandoffFixture(t)
		s, b := h.prepare()
		h.restart("signer")
		if _, e := s.Check(context.Background(), b); e == nil {
			t.Fatal("old boot challenge survived")
		}
		h.restart("owner")
		fresh, wire := h.prepare()
		if _, e := fresh.Check(context.Background(), wire); e != nil {
			t.Fatal("fresh reviewed session after restart refused", e)
		}
	})
	t.Run("HA-14-dropped-response-exact-retry", func(t *testing.T) {
		h := newHandoffFixture(t)
		s, b := h.prepare()
		r, e := s.Check(context.Background(), b)
		if e != nil {
			t.Fatal(e)
		}
		head := h.actors["signer"].Head()
		retry, e := s.Check(context.Background(), b)
		if e != nil || retry.head != r.head || retry.body != r.body || h.actors["signer"].Head() != head {
			t.Fatal("retry was not exact/idempotent", e)
		}
		if _, e = s.Finish(context.Background(), retry); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("HA-14-expired-status-is-not-authority", func(t *testing.T) {
		h := newHandoffFixture(t)
		s, b := h.prepare()
		r, e := s.Check(context.Background(), b)
		if e != nil {
			t.Fatal(e)
		}
		h.clocks["signer"].Add(60)
		if _, e = h.actors["signer"].Status(s.request.ID); e != nil {
			t.Fatal("audit lost", e)
		}
		if _, e = s.Finish(context.Background(), r); e == nil {
			t.Fatal("expired audit completed inspection")
		}
	})
	t.Run("HA-07-public-receipt-not-bearer", func(t *testing.T) {
		h := newHandoffFixture(t)
		s, b := h.prepare()
		r, e := s.Check(context.Background(), b)
		if e != nil {
			t.Fatal(e)
		}
		var forged FixtureHandoffReceipt
		_ = json.Unmarshal(tj(r.Report()), &forged)
		if _, e = s.Finish(context.Background(), forged); e == nil {
			t.Fatal("public JSON became receipt capability")
		}
	})
}

func TestFixtureHandoffTrustProgress(t *testing.T) {
	t.Run("HA-06-and-HA-13-root-progress-forward-convergence", func(t *testing.T) {
		h := newHandoffFixture(t)
		s, b := h.prepare()
		h.f.rotate("targets")
		h.f.root.Signed.Version = 2
		h.f.put("2.root.json", h.f.rootSign())
		h.f.release(2, 10)
		h.f.anchor()
		if _, e := s.Check(context.Background(), b); e == nil {
			t.Fatal("different fresh authorization accepted")
		}
		cp := h.checkpoint("signer")
		var state TrustFixtureSnapshot
		_ = json.Unmarshal(cp.Trust, &state)
		root, e := fixtureRoot(state.Root, h.f.seed.FixtureBinding)
		if e != nil || root.Signed.Version != 2 {
			t.Fatal("lost independently learned root", e)
		}
		// Owner catches up independently while signer already knows root 2.
		fresh, wire := h.prepare()
		r, e := fresh.Check(context.Background(), wire)
		if e != nil {
			t.Fatal("forward convergence refused", e)
		}
		if _, e = fresh.Finish(context.Background(), r); e != nil {
			t.Fatal(e)
		}
		h.f.mu.Lock()
		delete(h.f.files, "2.root.json")
		h.f.mu.Unlock()
		h.f.release(1, 10)
		h.f.anchor()
		if _, e = h.session().Prepare(context.Background()); e == nil {
			t.Fatal("older metadata erased progress")
		}
	})
	t.Run("HA-10-owner-final-refresh", func(t *testing.T) {
		h := newHandoffFixture(t)
		s, b := h.prepare()
		r, e := s.Check(context.Background(), b)
		if e != nil {
			t.Fatal(e)
		}
		h.f.release(2, 10)
		h.f.anchor()
		if _, e = s.Finish(context.Background(), r); e == nil {
			t.Fatal("old review survived changed authorization")
		}
		if h.checkpoint("owner").TrustSHA == "" {
			t.Fatal("lost final refresh")
		}
	})
	t.Run("HA-09-reserved-floor", func(t *testing.T) {
		h := newHandoffFixture(t)
		h.f.unit.Sequence = 4
		h.f.provenance.UnitSHA256 = digest(tj(h.f.unit))
		h.f.release(2, 4)
		h.f.seal()
		if _, e := h.session().Prepare(context.Background()); e == nil {
			t.Fatal("reserved floor bypassed")
		}
	})
	t.Run("HA-09-identical-observed-unit-retry", func(t *testing.T) {
		h := newHandoffFixture(t)
		for i := 0; i < 2; i++ {
			s, b := h.prepare()
			r, e := s.Check(context.Background(), b)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.Finish(context.Background(), r); e != nil {
				t.Fatal(e)
			}
		}
		for _, role := range []string{"owner", "signer"} {
			cp := h.checkpoint(role)
			if cp.Inventory.ReservedFloor != 4 || cp.Inventory.InstalledFloor != 3 {
				t.Fatal("inspection reserved a financial sequence")
			}
		}
	})
}

func TestFixtureHandoffCASAndRecovery(t *testing.T) {
	t.Run("HA-11-file-budget-reserves-publication-space", func(t *testing.T) {
		h := newHandoffFixture(t)
		a := h.actors["owner"]
		dir := filepath.Join(h.root, "owner", "store")
		for i := 0; i < 252; i++ {
			b := tj(map[string]int{"disposableOrphan": i})
			if e := os.WriteFile(filepath.Join(dir, digest(b)+".json"), b, 0600); e != nil {
				t.Fatal(e)
			}
		}
		before, e := os.ReadDir(dir)
		if e != nil || len(before) != 255 {
			t.Fatal("incorrect budget fixture", e, len(before))
		}
		tx := handoffProposal(t, a, func(c *handoffCheckpoint) { c.LastAt++ })
		if _, e = a.cfg.Store.commit(tx); e == nil {
			t.Fatal("publication exceeded transient file budget")
		}
		after, _ := os.ReadDir(dir)
		if len(after) != len(before) {
			t.Fatal("budget rejection created partial output")
		}
	})
	t.Run("HA-11-complete-record-size-limit", func(t *testing.T) {
		h := newHandoffFixture(t)
		a := h.actors["owner"]
		tx := handoffProposal(t, a, func(c *handoffCheckpoint) {
			c.Records = []handoffRecord{{ID: NewProbe(), Channel: NewProbe(), Kind: "rejected", Body: json.RawMessage(`"` + strings.Repeat("x", 4094) + `"`)}}
		})
		if _, e := a.cfg.Store.commit(tx); e == nil {
			t.Fatal("complete replay record exceeded 4 KiB")
		}
	})
	t.Run("HA-12-known-verifier-result-publication-retry", func(t *testing.T) {
		h := newHandoffFixture(t)
		a := h.actors["owner"]
		calls := 0
		a.cfg.Store.fs.fault = func(at string) error {
			if at == "before-head" {
				calls++
				if calls == 2 {
					return errors.New("interrupt verified-result publication")
				}
			}
			return nil
		}
		if _, e := a.inspect(context.Background(), NewProbe()); e == nil {
			t.Fatal("result publication fault absent")
		}
		if a.retry == nil {
			t.Fatal("opaque result lost")
		}
		a.cfg.Store.fs.fault = nil
		if e := a.RetryPublication(); e != nil {
			t.Fatal("known exact result could not publish", e)
		}
		cp := h.checkpoint("owner")
		if cp.Pending != "" || cp.TrustSHA == "" {
			t.Fatal("trust/replay result split or discarded")
		}
		h.restart("owner")
		s, b := h.prepare()
		if _, e := s.Check(context.Background(), b); e != nil {
			t.Fatal("fresh review after durable recovery refused", e)
		}
	})
	for _, phase := range []string{"before-snapshot", "partial-snapshot", "after-snapshot", "partial-head", "before-head", "after-head"} {
		t.Run("HA-12-atomic-interruption-"+phase, func(t *testing.T) {
			h := newHandoffFixture(t)
			a := h.actors["owner"]
			transition, _, e := h.f.admit(nil)
			if e != nil {
				t.Fatal(e)
			}
			tx := handoffProposal(t, a, func(c *handoffCheckpoint) {
				c.Trust = transition.next
				c.TrustSHA = digest(transition.next)
				c.Records = []handoffRecord{{ID: NewProbe(), Channel: NewProbe(), Kind: "checked", Body: json.RawMessage(`{}`)}}
			})
			tx.proof = &transition
			old := a.Head()
			a.cfg.Store.fs.fault = func(at string) error {
				if at == phase {
					return errors.New("disposable power-loss fault")
				}
				return nil
			}
			if _, e = a.cfg.Store.commit(tx); e == nil {
				t.Fatal("fault not reached")
			}
			head, _ := a.cfg.Store.head()
			loaded, e := a.cfg.Store.Load(a.configSHA, head.StateSHA)
			if e != nil {
				t.Fatal(e)
			}
			cp, e := parseHandoffCheckpoint(loaded)
			if e != nil {
				t.Fatal(e)
			}
			if (len(cp.Trust) > 0) != (len(cp.Records) > 0) {
				t.Fatal("split trust/replay publication")
			}
			a.cfg.Store.fs.fault = nil
			got, e := a.cfg.Store.commit(tx)
			if e != nil || got != digest(tx.next) {
				t.Fatal("exact interrupted publication refused", e)
			}
			if _, e = a.cfg.Store.Load(a.configSHA, old); e == nil {
				t.Fatal("stale external anchor accepted")
			}
		})
	}
	t.Run("HA-11-concurrent-CAS", func(t *testing.T) {
		h := newHandoffFixture(t)
		a := h.actors["owner"]
		other, e := OpenHandoffFixtureStore(filepath.Join(h.root, "owner", "store"))
		if e != nil {
			t.Fatal(e)
		}
		defer other.Close()
		tx1 := handoffProposal(t, a, func(c *handoffCheckpoint) { c.LastAt++ })
		tx2 := handoffProposal(t, a, func(c *handoffCheckpoint) { c.LastAt += 2 })
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		start := make(chan struct{})
		for i, store := range []*HandoffFixtureStore{a.cfg.Store, other} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				tx := tx1
				if i == 1 {
					tx = tx2
				}
				_, e := store.commit(tx)
				errs <- e
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		success := 0
		for e := range errs {
			if e == nil {
				success++
			}
		}
		if success != 1 {
			t.Fatal("CAS did not exclude one writer", success)
		}
	})
	t.Run("HA-12-unknown-learning-remains-held-after-restart", func(t *testing.T) {
		h := newHandoffFixture(t)
		a := h.actors["owner"]
		_, cp, e := a.load()
		if e != nil {
			t.Fatal(e)
		}
		id, channel := NewProbe(), NewProbe()
		cp.Pending, cp.Active = id, channel
		_ = putHandoffRecord(&cp, handoffRecord{ID: id, Channel: channel, Kind: "verification-pending", Body: json.RawMessage(`{}`)})
		if e = a.publish(cp, nil); e != nil {
			t.Fatal(e)
		}
		h.restart("owner")
		if _, e = h.session().Prepare(context.Background()); e == nil {
			t.Fatal("unknown interrupted learning silently retried")
		}
		if h.checkpoint("owner").Pending != id {
			t.Fatal("pending hold cleared")
		}
	})
	for _, which := range []string{"corrupt-head", "missing-head", "corrupt-checkpoint", "foreign", "symlink", "partial-foreign", "oversize", "opaque-proof", "record-budget"} {
		t.Run("HA-11-"+which, func(t *testing.T) {
			h := newHandoffFixture(t)
			a := h.actors["owner"]
			dir := filepath.Join(h.root, "owner", "store")
			tx := handoffProposal(t, a, func(c *handoffCheckpoint) { c.LastAt++ })
			switch which {
			case "corrupt-head":
				_ = os.WriteFile(filepath.Join(dir, "head.json"), []byte(`{}`), 0600)
			case "missing-head":
				_ = os.Remove(filepath.Join(dir, "head.json"))
			case "corrupt-checkpoint":
				_ = os.WriteFile(filepath.Join(dir, a.Head()+".json"), []byte(`{}`), 0600)
			case "foreign":
				_ = os.WriteFile(filepath.Join(dir, "do-not-adopt"), []byte("fixture foreign"), 0600)
			case "symlink":
				if runtime.GOOS == "windows" {
					t.Skip("Unix symlink")
				}
				_ = os.Remove(filepath.Join(dir, "head.json"))
				_ = os.Symlink(filepath.Join(dir, a.Head()+".json"), filepath.Join(dir, "head.json"))
			case "partial-foreign":
				_ = os.WriteFile(filepath.Join(dir, "snapshot-"+digest(tx.next)+".tmp"), []byte("wrong prefix"), 0600)
			case "oversize":
				tx.next = make([]byte, maxHandoffCheckpoint+1)
			case "opaque-proof":
				var cp handoffCheckpoint
				_ = strictJSONBounded(tx.next, &cp, maxHandoffCheckpoint)
				transition, _, e := h.f.admit(nil)
				if e != nil {
					t.Fatal(e)
				}
				cp.Trust = transition.next
				cp.TrustSHA = digest(cp.Trust)
				tx.next = tj(cp)
			case "record-budget":
				var cp handoffCheckpoint
				_ = strictJSONBounded(tx.next, &cp, maxHandoffCheckpoint)
				for i := 0; i < 33; i++ {
					cp.Records = append(cp.Records, handoffRecord{ID: NewProbe(), Channel: NewProbe(), Kind: "rejected", Body: json.RawMessage(`{}`)})
				}
				tx.next = tj(cp)
			}
			if _, e := a.cfg.Store.commit(tx); e == nil {
				t.Fatal("unsafe checkpoint accepted")
			}
		})
	}
}

func TestFixtureHandoffNoExecution(t *testing.T) {
	for _, which := range []string{"unknown-field", "duplicate-field", "oversized", "real-artifact", "executable"} {
		t.Run("HA-15-"+which, func(t *testing.T) {
			h := newHandoffFixture(t)
			s, b := h.prepare()
			switch which {
			case "unknown-field":
				b = append([]byte(`{"installAllowed":true,`), b[1:]...)
			case "duplicate-field":
				b = append([]byte(`{"id":"`+s.request.ID+`",`), b[1:]...)
			case "oversized":
				b = make([]byte, MaxDocument+1)
			case "real-artifact":
				p := filepath.Join(h.cfg["signer"].Admission.ArtifactDir, "core.fixture")
				v, _ := os.ReadFile(p)
				v[0] = '#'
				_ = os.WriteFile(p, v, 0600)
			case "executable":
				if runtime.GOOS == "windows" {
					t.Skip("Unix execute mode")
				}
				_ = os.Chmod(filepath.Join(h.cfg["signer"].Admission.ArtifactDir, "installer.fixture"), 0700)
			}
			if _, e := s.Check(context.Background(), b); e == nil {
				t.Fatal("execution-capable input accepted")
			}
		})
	}
	t.Run("HA-08-and-HA-15-canaries-and-floors", func(t *testing.T) {
		h := newHandoffFixture(t)
		before := map[string]string{}
		for r, p := range h.canaries {
			b, _ := os.ReadFile(p)
			before[r] = digest(b)
		}
		s, b := h.prepare()
		receipt, e := s.Check(context.Background(), b)
		if e != nil {
			t.Fatal(e)
		}
		out, e := s.Finish(context.Background(), receipt)
		if e != nil || !fixtureOutcomeDisabled(out) {
			t.Fatal(e)
		}
		for r, p := range h.canaries {
			b, _ := os.ReadFile(p)
			cp := h.checkpoint(r)
			if digest(b) != before[r] || cp.Inventory.InstalledFloor != 3 || cp.Inventory.ReservedFloor != 4 {
				t.Fatal("financial canary or floor mutated")
			}
		}
	})
	for _, field := range []string{"epoch", "revision", "held-intent", "canary"} {
		t.Run("HA-08-context-"+field, func(t *testing.T) {
			h := newHandoffFixture(t)
			s, b := h.prepare()
			h.mutateInventory("signer", func(i *FixtureHandoffInventory) {
				switch field {
				case "epoch":
					i.AuthoritySHA256 = digest([]byte("new epoch"))
				case "revision":
					i.Revision++
				case "held-intent":
					i.ActiveIntentID = NewProbe()
				}
			})
			if field == "canary" {
				_ = os.WriteFile(h.canaries["signer"], []byte("changed role-owned canary"), 0600)
			}
			if _, e := s.Check(context.Background(), b); e == nil {
				t.Fatal("stale fixture snapshot accepted")
			}
		})
	}
}
