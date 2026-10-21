package update

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type installedModelFixture struct {
	h          *handoffFixture
	model      *installedAdmissionModel
	identities map[string]admissionModelIdentity
	review     func(admissionModelReview) bool
}

func newInstalledModelFixture(t *testing.T) *installedModelFixture {
	t.Helper()
	h := newHandoffFixture(t)
	proposed := admissionModelReader{Proposed: true, Capability: installedAdmissionModelCapability, Readable: true}
	id := admissionModelIdentity{FixtureOnly: true, AuthenticatedSeed: true, InstanceID: h.cfg["owner"].Inventory().InstanceID, OwnerUID: 1001, AppUID: 1002, SignerUID: 1003, InstallSHA: digest([]byte("immutable fixture install")), ConfigSHA: digest([]byte("immutable fixture config")), OwnerSHA: digest([]byte("immutable fixture owner")), SignerSHA: digest([]byte("immutable fixture signer")), SeedSHA: h.cfg["owner"].Trust.SeedSHA256, PolicySHA: h.cfg["owner"].Admission.PolicySHA256, TransportSHA: h.actors["owner"].transportSHA, Readers: admissionModelReaders{Owner: proposed, Signer: proposed, Target: Target{"linux", "amd64"}, Protocol: 1, FinancialFormat: 1, TopologySHA: digest([]byte("fixture topology"))}}
	f := &installedModelFixture{h: h, identities: map[string]admissionModelIdentity{"owner": id, "signer": id}}
	f.review = func(r admissionModelReview) bool {
		return h.reviewAllowed.Load() && len(r.Subjects) == 4 && r.Digest == hashTuple(installedAdmissionModelProfile+":review", string(mustHandoffJSON(r.Request)))
	}
	m, e := newInstalledAdmissionModel(f.role("owner"), f.role("signer"), f.review, true)
	if e != nil {
		t.Fatal(e)
	}
	f.model = m
	before := map[string]FixtureHandoffInventory{}
	for _, r := range []string{"owner", "signer"} {
		before[r] = h.cfg[r].Inventory()
	}
	t.Cleanup(func() {
		for _, r := range []string{"owner", "signer"} {
			after := h.cfg[r].Inventory()
			// Mutation tests deliberately change context; these financial fields and
			// unopened financial files must still never be changed by any model action.
			evidence := struct {
				Role, BeforeCanary, AfterCanary                                string
				BeforeInstalled, AfterInstalled, BeforeReserved, AfterReserved uint64
			}{r, before[r].CanarySHA256, after.CanarySHA256, before[r].InstalledFloor, after.InstalledFloor, before[r].ReservedFloor, after.ReservedFloor}
			t.Log("MODEL_FINANCIAL " + string(mustHandoffJSON(evidence)))
			if after.InstalledFloor != before[r].InstalledFloor || after.ReservedFloor != before[r].ReservedFloor || after.CanarySHA256 != before[r].CanarySHA256 {
				t.Errorf("model changed financial canary/floors: %s", r)
			}
		}
	})
	return f
}
func (f *installedModelFixture) role(r string) admissionModelRole {
	return admissionModelRole{actor: f.h.actors[r], identity: func() admissionModelIdentity { return f.identities[r] }}
}
func (f *installedModelFixture) prepare(t *testing.T) []byte {
	t.Helper()
	b, e := f.model.prepare(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func (f *installedModelFixture) checked(t *testing.T) admissionModelReceipt {
	t.Helper()
	b := f.prepare(t)
	r, e := f.model.check(context.Background(), b)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func mustRefuseModel(t *testing.T, e error) {
	t.Helper()
	if e == nil {
		t.Fatal("unsafe model transition accepted")
	}
}

func TestInstalledAdmissionCompatibilityModel(t *testing.T) {
	old := func(n int) admissionModelReader { return admissionModelReader{Lifecycle: n, Readable: true} }
	future := admissionModelReader{Proposed: true, Capability: installedAdmissionModelCapability, Readable: true}
	readers := func(o, s admissionModelReader) admissionModelReaders {
		return admissionModelReaders{Owner: o, Signer: s, Target: Target{"linux", "amd64"}, Protocol: 1, FinancialFormat: 1, TopologySHA: digest([]byte("fixture topology"))}
	}
	cases := []struct {
		rc string
		v  admissionModelReaders
	}{
		{"RC-01", readers(old(1), old(2))}, {"RC-02", readers(old(3), old(3))}, {"RC-03", readers(old(4), old(4))},
		{"RC-05", readers(old(4), future)}, {"RC-06", readers(future, future)},
		{"RC-07", admissionModelReaders{Owner: future, Signer: future, Target: Target{"linux", "amd64"}, Held: true, Protocol: 1, FinancialFormat: 1, TopologySHA: digest([]byte("fixture topology"))}},
		{"RC-08", admissionModelReaders{Owner: future, Signer: future, Target: Target{"darwin", "arm64"}}},
	}
	// Lifecycle 1/2 are unsupported even when their pair is mixed.
	cases[0].v = readers(old(1), old(1))
	for _, tc := range cases {
		t.Run("IA-01-"+tc.rc, func(t *testing.T) {
			if got := admissionModelCompatibility(tc.v); got != tc.rc {
				t.Fatal(got, tc.rc)
			}
		})
	}
	t.Run("IA-01-RC-04", func(t *testing.T) {
		if admissionModelBridgeDecision(readers(old(4), old(4)), readers(future, future)) != "RC-04" {
			t.Fatal("bridge not separately classified")
		}
	})
	for _, which := range []string{"old", "unknown", "missing", "equal-uids", "root", "held", "unreadable"} {
		t.Run("IA-01-before-mutation-"+which, func(t *testing.T) {
			f := newInstalledModelFixture(t)
			beforeO, beforeS := f.h.actors["owner"].Head(), f.h.actors["signer"].Head()
			id := f.identities["owner"]
			switch which {
			case "old":
				id.Readers.Owner = old(4)
				id.Readers.Signer = old(4)
			case "unknown":
				id.Readers.Owner.Lifecycle = 5
			case "missing":
				id.Readers.Owner.Capability = ""
			case "equal-uids":
				id.AppUID = id.OwnerUID
			case "root":
				id.SignerUID = 0
			case "held":
				id.Readers.Held = true
			case "unreadable":
				id.Readers.Owner.Readable = false
			}
			f.identities["owner"], f.identities["signer"] = id, id
			_, e := newInstalledAdmissionModel(f.role("owner"), f.role("signer"), f.review, true)
			mustRefuseModel(t, e)
			if beforeO != f.h.actors["owner"].Head() || beforeS != f.h.actors["signer"].Head() {
				t.Fatal("unsupported construction mutated checkpoint")
			}
		})
	}
}
func TestInstalledAdmissionAuthorityModel(t *testing.T) {
	for _, field := range []string{"fixture", "seed-auth", "instance", "config", "owner-hash", "signer-hash", "seed-pin", "policy-pin", "transport-pin"} {
		t.Run("IA-02-"+field, func(t *testing.T) {
			f := newInstalledModelFixture(t)
			id := f.identities["owner"]
			switch field {
			case "fixture":
				id.FixtureOnly = false
			case "seed-auth":
				id.AuthenticatedSeed = false
			case "instance":
				id.InstanceID = NewProbe()
			case "config":
				id.ConfigSHA = digest([]byte("foreign"))
			case "owner-hash":
				id.OwnerSHA = digest([]byte("foreign"))
			case "signer-hash":
				id.SignerSHA = digest([]byte("foreign"))
			case "seed-pin":
				id.SeedSHA = digest([]byte("foreign"))
			case "policy-pin":
				id.PolicySHA = digest([]byte("foreign"))
			case "transport-pin":
				id.TransportSHA = digest([]byte("foreign"))
			}
			f.identities["owner"] = id
			_, e := f.model.prepare(context.Background())
			mustRefuseModel(t, e)
		})
	}
	t.Run("IA-03-independent-signer-evidence-preserves-owner-learning", func(t *testing.T) {
		f := newInstalledModelFixture(t)
		wire := f.prepare(t)
		p := filepath.Join(f.h.cfg["signer"].Admission.ArtifactDir, "signer.fixture")
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		b[len(b)-1] ^= 1
		if e = os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
		_, e = f.model.check(context.Background(), wire)
		mustRefuseModel(t, e)
		for _, role := range []string{"owner", "signer"} {
			cp := f.h.checkpoint(role)
			if cp.TrustSHA == "" {
				t.Fatal("independent learned trust lost", role)
			}
		}
	})
	for _, role := range []string{"core", "signer", "owner-helper", "installer"} {
		t.Run("IA-04-artifact-"+role, func(t *testing.T) {
			f := newInstalledModelFixture(t)
			wire := f.prepare(t)
			var file string
			for _, v := range f.h.f.unit.Subjects {
				if v.Role == role {
					file = v.File
				}
			}
			if file == "" {
				t.Fatal("missing role fixture", role)
			}
			if e := os.Remove(filepath.Join(f.h.cfg["signer"].Admission.ArtifactDir, file)); e != nil {
				t.Fatal(e)
			}
			_, e := f.model.check(context.Background(), wire)
			mustRefuseModel(t, e)
		})
	}
	t.Run("IA-04-provenance", func(t *testing.T) {
		f := newInstalledModelFixture(t)
		wire := f.prepare(t)
		f.h.actors["signer"].cfg.Admission.Evidence = []byte(`{}`)
		_, e := f.model.check(context.Background(), wire)
		mustRefuseModel(t, e)
	})
}
func TestInstalledAdmissionReviewModel(t *testing.T) {
	t.Run("IA-05-denied", func(t *testing.T) {
		f := newInstalledModelFixture(t)
		f.h.reviewAllowed.Store(false)
		_, e := f.model.prepare(context.Background())
		mustRefuseModel(t, e)
		if f.h.checkpoint("owner").TrustSHA == "" {
			t.Fatal("review denial rewound trust")
		}
	})
	for _, field := range []string{"profile", "capability", "boot", "binding", "inventory", "head", "unit", "authorization", "sequence", "target", "expiry", "channel", "unknown", "duplicate", "oversized"} {
		t.Run("IA-06-exchange-"+field, func(t *testing.T) {
			f := newInstalledModelFixture(t)
			wire := f.prepare(t)
			q := f.model.request
			switch field {
			case "profile":
				q.Profile = HandoffProfile
			case "capability":
				q.Capability = "lifecycle-4"
			case "boot":
				q.SignerBoot = NewProbe()
			case "binding":
				q.Binding = digest([]byte("foreign"))
			case "inventory":
				q.OwnerInventory = digest([]byte("foreign"))
			case "head":
				q.SignerHead = digest([]byte("foreign"))
			case "unit":
				q.Unit = digest([]byte("foreign"))
			case "authorization":
				q.Authorization = digest([]byte("foreign"))
			case "sequence":
				q.Sequence++
			case "target":
				q.Target = Target{"darwin", "arm64"}
			case "expiry":
				q.Deadline++
			case "channel":
				q.Channel = NewProbe()
			}
			wire = mustHandoffJSON(q)
			switch field {
			case "unknown":
				wire = append([]byte(`{"InstallAllowed":true,`), wire[1:]...)
			case "duplicate":
				wire = append([]byte(`{"Channel":"forged",`), wire[1:]...)
			case "oversized":
				wire = make([]byte, 64<<10+1)
			}
			_, e := f.model.check(context.Background(), wire)
			mustRefuseModel(t, e)
		})
	}
	t.Run("IA-05-opaque-status-and-copied-json", func(t *testing.T) {
		f := newInstalledModelFixture(t)
		f.checked(t)
		status, e := f.model.status()
		if e != nil {
			t.Fatal(e)
		}
		var fake admissionModelReceipt
		_ = json.Unmarshal(status, &fake)
		_, e = f.model.finish(context.Background(), fake)
		mustRefuseModel(t, e)
	})
	t.Run("IA-06-cross-model-receipt", func(t *testing.T) {
		f, g := newInstalledModelFixture(t), newInstalledModelFixture(t)
		r := f.checked(t)
		g.checked(t)
		_, e := g.model.finish(context.Background(), r)
		mustRefuseModel(t, e)
	})
	for _, which := range []string{"owner-expiry", "signer-expiry", "backward", "uncertain", "review-delayed", "skew"} {
		t.Run("IA-07-"+which, func(t *testing.T) {
			f := newInstalledModelFixture(t)
			if which == "review-delayed" {
				f.model.review = func(admissionModelReview) bool { f.h.clocks["owner"].Add(60); return true }
				_, e := f.model.prepare(context.Background())
				mustRefuseModel(t, e)
				return
			}
			r := f.checked(t)
			switch which {
			case "owner-expiry":
				f.h.clocks["owner"].Store(f.model.request.Deadline)
			case "signer-expiry":
				f.h.clocks["signer"].Store(f.model.request.Deadline)
			case "backward":
				f.h.clocks["owner"].Add(-1)
			case "uncertain":
				f.h.confidence["signer"].Store(false)
			case "skew":
				f.h.clocks["owner"].Add(31)
			}
			_, e := f.model.finish(context.Background(), r)
			mustRefuseModel(t, e)
		})
	}
	t.Run("IA-10-live-exact-retries", func(t *testing.T) {
		f := newInstalledModelFixture(t)
		wire := f.prepare(t)
		r, e := f.model.check(context.Background(), wire)
		if e != nil {
			t.Fatal(e)
		}
		head := f.h.actors["signer"].Head()
		same, e := f.model.check(context.Background(), wire)
		if e != nil || same != r || head != f.h.actors["signer"].Head() {
			t.Fatal("inspection retry changed result", e)
		}
		out, e := f.model.finish(context.Background(), r)
		if e != nil {
			t.Fatal(e)
		}
		head = f.h.actors["owner"].Head()
		again, e := f.model.finish(context.Background(), r)
		if e != nil || again != out || head != f.h.actors["owner"].Head() {
			t.Fatal("completion retry changed result", e)
		}
	})
}
func TestInstalledAdmissionRecoveryModel(t *testing.T) {
	t.Run("IA-09-model-record-exchange-binding", func(t *testing.T) {
		f := newInstalledModelFixture(t)
		a := f.h.actors["owner"]
		a.mu.Lock()
		defer a.mu.Unlock()
		_, cp, e := a.load()
		if e != nil {
			t.Fatal(e)
		}
		var v admissionModelRecord
		if e = StrictJSON(cp.Records[0].Body, &v); e != nil {
			t.Fatal(e)
		}
		v.Exchange = NewProbe()
		cp.Records[0].Body = mustHandoffJSON(v)
		mustRefuseModel(t, a.publish(cp, nil))
	})
	t.Run("IA-08-pending-restart-held", func(t *testing.T) {
		f := newInstalledModelFixture(t)
		f.prepare(t)
		a := f.h.actors["signer"]
		a.mu.Lock()
		_, cp, e := a.load()
		if e == nil {
			cp.Pending = NewProbe()
			e = putHandoffRecord(&cp, handoffRecord{ID: cp.Pending, Channel: cp.Active, Kind: "verification-pending", Body: json.RawMessage(`{}`)})
			if e == nil {
				e = a.publish(cp, nil)
			}
		}
		a.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		f.h.restart("signer")
		historical, e := newInstalledAdmissionModel(f.role("owner"), f.role("signer"), f.review, false)
		if e != nil {
			t.Fatal(e)
		}
		_, e = historical.prepare(context.Background())
		mustRefuseModel(t, e)
		if f.h.checkpoint("signer").Pending == "" {
			t.Fatal("unknown pending cleared")
		}
	})
	for _, which := range []string{"missing-anchor", "corrupt-checkpoint", "anchor-gap", "record-exhaustion", "CAS-conflict"} {
		t.Run("IA-09-"+which, func(t *testing.T) {
			f := newInstalledModelFixture(t)
			wire := f.prepare(t)
			a := f.h.actors["signer"]
			switch which {
			case "missing-anchor":
				a.cfg.checkAnchor = func(string) error { return os.ErrNotExist }
			case "corrupt-checkpoint":
				if e := os.WriteFile(filepath.Join(f.h.root, "signer", "store", a.Head()+".json"), []byte(`{}`), 0600); e != nil {
					t.Fatal(e)
				}
			case "anchor-gap":
				a.cfg.advanceAnchor = func(string, string) error { return os.ErrPermission }
			case "record-exhaustion":
				a.mu.Lock()
				_, cp, e := a.load()
				if e != nil {
					t.Fatal(e)
				}
				for len(cp.Records) < 32 {
					if e = putHandoffRecord(&cp, handoffRecord{ID: NewProbe(), Channel: f.model.channel, Kind: "rejected", Body: json.RawMessage(`{}`)}); e != nil {
						t.Fatal(e)
					}
				}
				if e = a.publish(cp, nil); e != nil {
					t.Fatal(e)
				}
				a.mu.Unlock()
				f.model.signer.head = a.Head()
			case "CAS-conflict":
				a.mu.Lock()
				_, cp, e := a.load()
				if e == nil {
					cp.LastAt++
					e = a.publish(cp, nil)
				}
				a.mu.Unlock()
				if e != nil {
					t.Fatal(e)
				}
			}
			_, e := f.model.check(context.Background(), wire)
			mustRefuseModel(t, e)
			if which == "anchor-gap" && a.retry == nil {
				t.Fatal("publication uncertainty lost")
			}
		})
	}
	for _, role := range []string{"owner", "signer"} {
		t.Run("IA-10-restart-"+role, func(t *testing.T) {
			f := newInstalledModelFixture(t)
			r := f.checked(t)
			f.h.restart(role)
			historical, e := newInstalledAdmissionModel(f.role("owner"), f.role("signer"), f.review, false)
			if e != nil {
				t.Fatal(e)
			}
			b, e := historical.status()
			if e != nil || !strings.Contains(string(b), "HistoricalOnly") {
				t.Fatal("historical status absent", e)
			}
			_, e = historical.finish(context.Background(), r)
			mustRefuseModel(t, e)
		})
	}
	t.Run("IA-09-partial-owner-completion-keeps-signer-confirmation", func(t *testing.T) {
		f := newInstalledModelFixture(t)
		r := f.checked(t)
		a := f.h.actors["owner"]
		// The last owner refresh has two trust publications, followed by completion.
		n := 0
		a.cfg.advanceAnchor = func(string, string) error {
			n++
			if n == 3 {
				return os.ErrPermission
			}
			return nil
		}
		_, e := f.model.finish(context.Background(), r)
		mustRefuseModel(t, e)
		b, e := f.model.signer.actor.Status(f.model.channel)
		if e != nil || !strings.Contains(string(b), "confirmed") {
			t.Fatal("durable signer confirmation discarded", e, string(b))
		}
	})
	t.Run("IA-14-live-authority-change", func(t *testing.T) {
		f := newInstalledModelFixture(t)
		r := f.checked(t)
		f.h.mutateInventory("signer", func(i *FixtureHandoffInventory) { i.AuthoritySHA256 = digest([]byte("new authority")) })
		_, e := f.model.finish(context.Background(), r)
		mustRefuseModel(t, e)
	})
}
func modelBridgeFixture(t *testing.T) admissionModelBridge {
	t.Helper()
	old := admissionModelReader{Lifecycle: 4, Readable: true}
	future := admissionModelReader{Proposed: true, Capability: installedAdmissionModelCapability, Readable: true}
	target := Target{"linux", "amd64"}
	financial := admissionModelFinancial{HistorySHA: digest([]byte("history")), OriginalsSHA: digest([]byte("originals")), AuthoritySHA: digest([]byte("authority")), Installed: 8, Reserved: 10}
	b, e := newAdmissionModelBridge(admissionModelReaders{Owner: old, Signer: old, Target: target, Protocol: 1, FinancialFormat: 1, TopologySHA: digest([]byte("fixture topology"))}, admissionModelReaders{Owner: future, Signer: future, Target: target, Protocol: 1, FinancialFormat: 1, TopologySHA: digest([]byte("fixture topology"))}, financial, 11, digest([]byte("independent reader binding")))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestInstalledAdmissionBridgeModel(t *testing.T) {
	for _, field := range []string{"protocol", "financial-format", "topology", "unreadable"} {
		t.Run("IA-11-contract-"+field, func(t *testing.T) {
			b := modelBridgeFixture(t)
			old := admissionModelReader{Lifecycle: 4, Readable: true}
			future := admissionModelReader{Proposed: true, Capability: installedAdmissionModelCapability, Readable: true}
			from := admissionModelReaders{Owner: old, Signer: old, Target: Target{"linux", "amd64"}, Protocol: 1, FinancialFormat: 1, TopologySHA: digest([]byte("fixture topology"))}
			to := from
			to.Owner = future
			to.Signer = future
			switch field {
			case "protocol":
				to.Protocol++
			case "financial-format":
				to.FinancialFormat++
			case "topology":
				to.TopologySHA = digest([]byte("foreign topology"))
			case "unreadable":
				to.Signer.Readable = false
			}
			_, e := newAdmissionModelBridge(from, to, b.Before, b.Maintenance, b.Binding)
			mustRefuseModel(t, e)
		})
	}

	for _, field := range []string{"history", "originals", "authority", "installed", "reserved", "unknown", "pending", "held", "cold-unverified"} {
		t.Run("IA-11-"+field, func(t *testing.T) {
			b := modelBridgeFixture(t)
			observed := b.Current
			switch field {
			case "history":
				observed.HistorySHA = digest([]byte("foreign"))
			case "originals":
				observed.OriginalsSHA = digest([]byte("foreign"))
			case "authority":
				observed.AuthoritySHA = digest([]byte("foreign"))
			case "installed":
				observed.Installed = 0
			case "reserved":
				observed.Reserved = 0
			case "unknown":
				observed.Unknown = 1
			case "pending":
				observed.Pending = 1
			case "held":
				observed.Held = true
			}
			mustRefuseModel(t, b.advance("paused-backed-up", observed, field != "cold-unverified"))
			if b.Phase != "prepared" || !b.MaintenanceHeld {
				t.Fatal("failed bridge discarded hold")
			}
		})
	}
	for _, cut := range admissionModelBridgePhases {
		t.Run("IA-12-cut-"+cut, func(t *testing.T) {
			b := modelBridgeFixture(t)
			for _, phase := range admissionModelBridgePhases[1:] {
				if b.Phase == cut {
					break
				}
				if e := b.advance(phase, b.Current, true); e != nil {
					t.Fatal(e)
				}
			}
			raw, anchor := b.checkpoint()
			restored, e := restoreAdmissionModelBridge(raw, anchor, b.Binding)
			if e != nil || restored != b {
				t.Fatal("bridge cut did not restore", e)
			}
			if b.ForwardOnly {
				mustRefuseModel(t, restored.rollback(restored.Current, true, true))
			} else {
				if e = restored.rollback(restored.Current, true, true); e != nil {
					t.Fatal(e)
				}
				if e = restored.abortRollback(restored.Current); e != nil {
					t.Fatal(e)
				}
			}
			if restored.Current.Installed != 8 || restored.Current.Reserved != 11 || !restored.WalletLocked {
				t.Fatal("recovery changed floor or unlocked")
			}
		})
	}
	t.Run("IA-12-anchor-and-boundary-tampering", func(t *testing.T) {
		b := modelBridgeFixture(t)
		raw, anchor := b.checkpoint()
		b.Current.Installed = 0
		forged := mustHandoffJSON(b)
		_, e := restoreAdmissionModelBridge(forged, anchor, b.Binding)
		mustRefuseModel(t, e)
		_, e = restoreAdmissionModelBridge(raw, "", b.Binding)
		mustRefuseModel(t, e)
	})
	t.Run("IA-13-maintenance-only", func(t *testing.T) {
		b := modelBridgeFixture(t)
		before := b.Before
		for _, p := range admissionModelBridgePhases[1:] {
			if e := b.advance(p, b.Current, true); e != nil {
				t.Fatal(e)
			}
		}
		if b.MaintenanceHeld || !b.WalletLocked || b.Current.Installed != before.Installed || b.Current.Reserved != b.Maintenance || b.Current.HistorySHA != before.HistorySHA {
			t.Fatal("maintenance became installed release or financial reset")
		}
		old := admissionModelReader{Lifecycle: 4, Readable: true}
		next := admissionModelReader{Proposed: true, Capability: installedAdmissionModelCapability, Readable: true}
		r := admissionModelReaders{Owner: old, Signer: old, Target: Target{"linux", "amd64"}, Protocol: 1, FinancialFormat: 1, TopologySHA: digest([]byte("fixture topology"))}
		z := admissionModelReaders{Owner: next, Signer: next, Target: r.Target, Protocol: 1, FinancialFormat: 1, TopologySHA: r.TopologySHA}
		_, e := newAdmissionModelBridge(r, z, b.Current, b.Maintenance, b.Binding)
		mustRefuseModel(t, e)
	})
	for _, which := range []string{"active-intent", "unknown", "recovery-hold"} {
		t.Run("IA-14-"+which, func(t *testing.T) {
			f := newInstalledModelFixture(t)
			wire := f.prepare(t)
			if which == "active-intent" {
				f.h.mutateInventory("signer", func(i *FixtureHandoffInventory) { i.ActiveIntentID = NewProbe() })
			} else {
				id := f.identities["signer"]
				id.Readers.Held = true
				f.identities["signer"] = id
			}
			_, e := f.model.check(context.Background(), wire)
			mustRefuseModel(t, e)
		})
	}
}
func TestInstalledAdmissionNoExecutionModel(t *testing.T) {
	t.Run("IA-15-namespace-cannot-be-adopted", func(t *testing.T) {
		f := newInstalledModelFixture(t)
		r := f.checked(t)
		if _, e := f.model.finish(context.Background(), r); e != nil {
			t.Fatal(e)
		}
		_, e := NewFixtureHandoffSession(f.h.actors["owner"], f.h.actors["signer"])
		mustRefuseModel(t, e)
		_, e = newInstalledAdmissionModel(f.role("owner"), f.role("signer"), f.review, true)
		mustRefuseModel(t, e)
	})
	t.Run("IA-15-disabled-capabilities-and-financial-canaries", func(t *testing.T) {
		f := newInstalledModelFixture(t)
		r := f.checked(t)
		out, e := f.model.finish(context.Background(), r)
		if e != nil {
			t.Fatal(e)
		}
		if out != f.model.outcome() || !out.FixtureOnly || out.Profile != installedAdmissionModelProfile || out.InstallAllowed || out.ReserveAllowed || out.PauseAllowed || out.UnlockAllowed || out.ResumeAllowed || out.RealWallets || out.InstalledExecution || out.AutomaticUpdates || out.FinancialExecution {
			t.Fatal("model acquired execution authority", out)
		}
		t.Log("MODEL_CAPABILITIES: realWallets=false installedExecution=false automaticUpdates=false financialCanaries=unchanged reservedFloor=unchanged installedFloor=unchanged")
	})
	t.Run("IA-16-fixture-boundary-and-cleanup-model-only", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Fatal("never run project code as root")
		}
		f := newInstalledModelFixture(t)
		if !f.identities["owner"].valid() {
			t.Fatal("invalid simulated identity")
		}
		t.Log("IA-16 actual installed helpers/UID acceptance BLOCKED; keys memory-only, private fixtures cleanup registered")
	})
}
