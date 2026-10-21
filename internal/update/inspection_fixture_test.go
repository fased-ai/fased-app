package update

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type inspectionTest struct {
	dir     string
	in      *inspectionInputs
	catalog inspectionCatalog
	roles   [2]inspectionRoleInput
	files   map[string][]byte
	now     int64
	canary  string
}

func inspectionSupported(t *testing.T) bool {
	t.Helper()
	if runtime.GOOS == "linux" {
		return true
	}
	if _, e := inspectionRoot(t.TempDir(), os.Geteuid(), 0755); e == nil {
		t.Fatal("unsupported platform admitted")
	}
	return false
}
func newInspectionTest(t *testing.T) *inspectionTest {
	t.Helper()
	f := newAdmissionFixture(t)
	blobs := map[string][]byte{}
	for _, n := range []string{"timestamp.json", formatVersion(f.sn.Signed.Version) + ".snapshot.json", formatVersion(f.tg.Signed.Version) + ".targets.json", "targets/fixture/" + digest(tj(TrustFixtureDescriptor{FixtureBinding: f.seed.FixtureBinding, Version: 1, Sequence: f.unit.Sequence, PreserveSHA256: f.seed.PreserveSHA256, EvidenceSHA256: digest(tj(f.evidence))})) + ".descriptor.json"} {
		blobs[filepath.Base(n)] = f.get(n)
	}
	blobs["evidence.json"] = tj(f.evidence)
	for _, s := range f.unit.Subjects {
		b, e := os.ReadFile(filepath.Join(f.dir, s.File))
		if e != nil {
			t.Fatal(e)
		}
		blobs[s.File] = b
	}
	// Close HTTPS now: all subsequent verification must work without a server.
	f.server.Close()
	index := tj(inspectionIndex{1, inspectionProfile, true, sortedInspectionFiles(blobs)})
	c := inspectionCatalog{Version: 1, Profile: inspectionProfile, FixtureOnly: true, Provision: NewProbe(), Installation: NewProbe(), ConfigSHA: digest([]byte("config")), RootSHA: digest([]byte("root")), ManifestSHA: digest([]byte("manifest")), BeforeImages: [2]string{digest([]byte("before core")), digest([]byte("before signer"))}, CandidateImages: [2]string{digest([]byte("candidate core")), digest([]byte("candidate signer"))}, BeforeUnits: digest([]byte("before units")), GuardedUnits: digest([]byte("guarded units")), FinalUnits: digest([]byte("final units")), UIDs: [3]int{1001, 1002, 1003}, GIDs: [3]int{1001, 1002, 1003}, Reader: inspectionReaderV1()}
	all := map[string][]byte{"bundle/index.json": index}
	for n, b := range blobs {
		all["bundle/"+n] = b
	}
	x := &inspectionTest{catalog: c, files: all, now: f.now}
	for i, role := range []string{"owner", "signer"} {
		id := 0
		if i == 1 {
			id = 2
		}
		d := inspectionRoleInput{Version: 1, Profile: inspectionProfile, FixtureOnly: true, Provision: c.Provision, Installation: c.Installation, ConfigSHA: c.ConfigSHA, Role: role, UID: c.UIDs[id], GID: c.GIDs[id], SeedSHA: digest(tj(f.seed)), PolicySHA: digest(tj(f.policy)), FetchPin: digest([]byte("bounded fixture HTTPS fetch policy")), IndexSHA: digest(index), Epoch: NewProbe(), Reader: c.Reader}
		x.roles[i] = d
		all[role+".json"] = tj(d)
		c.Roles[i] = digest(all[role+".json"])
		all[role+"-seed.json"] = tj(f.seed)
		all[role+"-policy.json"] = tj(f.policy)
		all[role+"-clock.json"] = tj(inspectionClock{1, inspectionProfile, c.Provision, role, d.Epoch, 1, f.now, true})
	}
	x.catalog = c
	all["catalog.json"] = tj(c)
	return x.clone(t)
}
func (base *inspectionTest) clone(t *testing.T) *inspectionTest {
	t.Helper()
	var dir string
	var e error
	if parent := os.Getenv("FASED_INSPECTION_EVIDENCE"); parent != "" {
		dir, e = os.MkdirTemp(parent, "fixture-")
	} else {
		dir, e = os.MkdirTemp("", "fased-inspection-disposable-")
	}
	if e != nil {
		t.Fatal(e)
	}
	x := *base
	x.dir = dir
	x.files = map[string][]byte{}
	for n, b := range base.files {
		x.files[n] = append([]byte(nil), b...)
	}
	// Financial state is deliberately outside inputs and is never passed to code.
	canary := tj(struct {
		Installed, Reserved uint64
		Vault, Journal      string
	}{3, 4, "disposable empty vault", "disposable journal"})
	x.canary = filepath.Join(dir, "financial-canary.json")
	if e = os.WriteFile(x.canary, canary, 0600); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		after, err := os.ReadFile(x.canary)
		if err != nil || string(after) != string(canary) {
			t.Error("financial canary/floors changed", err)
		}
		var actual struct {
			Installed, Reserved uint64
			Vault, Journal      string
		}
		if json.Unmarshal(after, &actual) != nil || actual.Installed != 3 || actual.Reserved != 4 {
			t.Error("financial floor readback failed")
		}
		if e = os.RemoveAll(dir); e != nil {
			t.Error(e)
		}
		_, e = os.Stat(dir)
		if !os.IsNotExist(e) {
			t.Error("owned disposable cleanup failed")
		}
		t.Logf("INSPECTION_EVIDENCE {\"before\":%q,\"after\":%q,\"installed\":%d,\"reserved\":%d,\"cleanup\":%t,\"execution\":false}", digest(canary), digest(after), actual.Installed, actual.Reserved, os.IsNotExist(e))
	})
	x.writeAll(t)
	x.in = x.open(t, "owner")
	return &x
}
func (x *inspectionTest) writeAll(t *testing.T) {
	t.Helper()
	for n, b := range x.files {
		p := filepath.Join(x.dir, "inputs", n)
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.Chmod(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, b, 0644); e != nil {
			t.Fatal(e)
		}
	}
}
func (x *inspectionTest) open(t *testing.T, role string) *inspectionInputs {
	t.Helper()
	in, e := openInspectionInputs(filepath.Join(x.dir, "inputs"), digest(x.files["catalog.json"]), role, os.Geteuid())
	if e != nil {
		t.Fatal(e)
	}
	return in
}
func (x *inspectionTest) session(t *testing.T, role string) *inspectionSession {
	t.Helper()
	dirs := []string{}
	for _, kind := range []string{"state", "anchor"} {
		p := filepath.Join(x.dir, role+"-"+kind)
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
		dirs = append(dirs, p)
	}
	s, e := openInspectionStore(dirs[0], dirs[1], x.open(t, role))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.close)
	a, e := newInspectionSession(s, true)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (x *inspectionTest) resealCatalog(t *testing.T, c inspectionCatalog) {
	t.Helper()
	x.catalog = c
	x.files["catalog.json"] = tj(c)
	x.writeAll(t)
}
func ifp(t *testing.T, id string) { t.Helper(); t.Log("INSPECTION_CASE " + id) }
func requireInspectionDisabled(t *testing.T, r inspectionResult) {
	t.Helper()
	if r.InstallAllowed || r.ResumeAllowed || r.UpdaterEnabled || r.WalletSigner || r.FinancialExecution || !admissionDisabled(r.Admission) {
		t.Fatal("execution capability enabled")
	}
}

func TestInspectionFixtureDescriptors(t *testing.T) {
	if !inspectionSupported(t) {
		return
	}
	base := newInspectionTest(t)
	for _, name := range []string{"profile", "fixture-only", "reader", "capability", "identity", "uids", "digest", "unknown-field", "role-pin"} {
		t.Run(name, func(t *testing.T) {
			x := base.clone(t)
			c := x.catalog
			switch name {
			case "profile":
				c.Profile = "production"
			case "fixture-only":
				c.FixtureOnly = false
			case "reader":
				c.Reader.Predecessor = 3
			case "capability":
				c.Reader.Capability = "legacy"
			case "identity":
				c.Provision = ""
			case "uids":
				c.UIDs[2] = c.UIDs[0]
			case "digest":
				c.RootSHA = ""
			case "role-pin":
				c.Roles[0] = digest([]byte("wrong"))
			}
			x.resealCatalog(t, c)
			if name == "unknown-field" {
				x.files["catalog.json"] = append([]byte(`{"execute":true,`), x.files["catalog.json"][1:]...)
				x.writeAll(t)
			}
			if _, e := openInspectionInputs(filepath.Join(x.dir, "inputs"), digest(x.files["catalog.json"]), "owner", os.Geteuid()); e == nil {
				t.Fatal("invalid catalog admitted")
			}
		})
	}
	if _, e := openInspectionInputs(base.in.directory, base.in.pin, "app", os.Geteuid()); e == nil {
		t.Fatal("app role admitted")
	}
	ifp(t, "IFP-01")
}
func TestInspectionFixtureOfflineSource(t *testing.T) {
	if !inspectionSupported(t) {
		return
	}
	base := newInspectionTest(t)
	for _, role := range []string{"owner", "signer"} {
		t.Run("offline-"+role, func(t *testing.T) {
			x := base.clone(t)
			a := x.session(t, role)
			r, e := a.inspect(context.Background())
			if e != nil || !r.Admission.ProvenanceVerified || !r.Admission.MetadataVerified || r.Admission.ArtifactCount != 4 || !r.Historical {
				t.Fatal("offline round trip", e)
			}
			requireInspectionDisabled(t, r)
			loaded, e := a.store.load()
			if e != nil || len(loaded) == 0 {
				t.Fatal(e)
			}
			b, e := newInspectionSession(a.store, false)
			if e != nil {
				t.Fatal(e)
			}
			r, e = b.inspect(context.Background())
			if e != nil {
				t.Fatal("anchored restart", e)
			}
			requireInspectionDisabled(t, r)
		})
	}
	for _, name := range []string{"missing", "hash", "source-index", "symlink", "hardlink", "mode", "extra", "executable", "path", "size", "roots", "total", "provenance", "continuity", "root-gap", "extra-snapshot"} {
		t.Run(name, func(t *testing.T) {
			x := base.clone(t)
			bundle := filepath.Join(x.dir, "inputs/bundle")
			p := filepath.Join(bundle, "core.fixture")
			switch name {
			case "missing":
				os.Remove(p)
			case "hash":
				os.WriteFile(p, []byte("wrong"), 0644)
			case "source-index":
				os.WriteFile(filepath.Join(bundle, "index.json"), []byte(`{}`), 0644)
			case "symlink":
				os.Remove(p)
				os.Symlink(x.canary, p)
			case "hardlink":
				os.Remove(p)
				os.Link(x.canary, p)
			case "mode":
				os.Chmod(p, 0666)
			case "extra":
				os.WriteFile(filepath.Join(bundle, "extra"), []byte("foreign"), 0644)
			case "executable":
				os.Chmod(p, 0755)
			case "path", "size", "roots", "total":
				var idx inspectionIndex
				StrictJSON(x.files["bundle/index.json"], &idx)
				switch name {
				case "path":
					idx.Files[0].Name = "../escape"
				case "size":
					idx.Files[0].Size = inspectionMaxBytes + 1
				case "roots":
					for i := 1; i <= 33; i++ {
						idx.Files = append(idx.Files, inspectionFile{formatVersion(int64(i)) + ".root.json", 1, digest([]byte("x"))})
					}
				case "total":
					idx.Files = append(idx.Files, idx.Files...)
				}
				sort.Slice(idx.Files, func(i, j int) bool { return idx.Files[i].Name < idx.Files[j].Name })
				b := tj(idx)
				os.WriteFile(filepath.Join(bundle, "index.json"), b, 0644)
				x.in.role.IndexSHA = digest(b)
				_, e := openInspectionSource(bundle, digest(b), "https://fixture.invalid", x.in.role.FetchPin, os.Geteuid())
				if e == nil {
					t.Fatal("bad bounded index admitted")
				}
				return
			case "root-gap", "extra-snapshot":
				var seed TrustFixtureSeed
				StrictJSON(x.files["owner-seed.json"], &seed)
				if name == "root-gap" {
					x.files["bundle/3.root.json"] = seed.Root
				} else {
					x.files["bundle/2.snapshot.json"] = x.files["bundle/1.snapshot.json"]
				}
				x.reindex(t)
			case "provenance", "continuity": // Re-pin the index so cryptography, not a file hash, rejects it.
				n := "evidence.json"
				if name == "continuity" {
					n = "timestamp.json"
				}
				b := append([]byte(nil), x.files["bundle/"+n]...)
				b[len(b)/2] ^= 1
				x.files["bundle/"+n] = b
				x.reindex(t)
			}
			in, e := openInspectionInputs(filepath.Join(x.dir, "inputs"), digest(x.files["catalog.json"]), "owner", os.Geteuid())
			if e == nil {
				x.in = in
				a := x.session(t, "owner")
				r, e := a.inspect(context.Background())
				requireInspectionDisabled(t, r)
				if e == nil {
					t.Fatal("bad offline input admitted")
				}
			}
		})
	}
	ifp(t, "IFP-02")
	ifp(t, "IFP-03")
}
func (x *inspectionTest) reindex(t *testing.T) {
	t.Helper()
	m := map[string][]byte{}
	for n, b := range x.files {
		if strings.HasPrefix(n, "bundle/") && n != "bundle/index.json" {
			m[strings.TrimPrefix(n, "bundle/")] = b
		}
	}
	idx := tj(inspectionIndex{1, inspectionProfile, true, sortedInspectionFiles(m)})
	x.files["bundle/index.json"] = idx
	c := x.catalog
	for i, role := range []string{"owner", "signer"} {
		d := x.roles[i]
		d.IndexSHA = digest(idx)
		x.roles[i] = d
		x.files[role+".json"] = tj(d)
		c.Roles[i] = digest(tj(d))
	}
	x.resealCatalog(t, c)
}
func TestInspectionFixtureRoleInputs(t *testing.T) {
	if !inspectionSupported(t) {
		return
	}
	base := newInspectionTest(t)
	for _, name := range []string{"seed-pin", "policy-pin", "role", "clock-role", "epoch", "uncertain", "backward", "revision", "deadline", "clock-change", "catalog-change", "bundle-change"} {
		t.Run(name, func(t *testing.T) {
			x := base.clone(t)
			a := x.session(t, "owner")
			if name == "backward" || name == "revision" {
				if _, e := a.inspect(context.Background()); e != nil {
					t.Fatal(e)
				}
			}
			switch name {
			case "seed-pin":
				os.WriteFile(filepath.Join(x.in.directory, "owner-seed.json"), x.files["signer-policy.json"], 0644)
			case "policy-pin":
				os.WriteFile(filepath.Join(x.in.directory, "owner-policy.json"), x.files["owner-seed.json"], 0644)
			case "role":
				os.WriteFile(filepath.Join(x.in.directory, "owner.json"), x.files["signer.json"], 0644)
			case "deadline", "clock-change", "catalog-change", "bundle-change":
				calls := 0
				start := time.Now()
				a.monotonic = func() time.Time {
					calls++
					if calls > 1 && name == "deadline" || calls == 1 && name != "deadline" {
						if name == "deadline" {
							return start.Add(time.Minute)
						}
						if name == "catalog-change" {
							os.WriteFile(filepath.Join(x.in.directory, "catalog.json"), append(x.files["catalog.json"], '\n'), 0644)
							return start
						}
						if name == "bundle-change" {
							os.WriteFile(filepath.Join(x.in.directory, "bundle/core.fixture"), []byte("changed after source read"), 0644)
							return start
						}
						var c inspectionClock
						StrictJSON(x.files["owner-clock.json"], &c)
						c.Revision++
						os.WriteFile(filepath.Join(x.in.directory, "owner-clock.json"), tj(c), 0644)
					}
					return start
				}
			default:
				var c inspectionClock
				StrictJSON(x.files["owner-clock.json"], &c)
				switch name {
				case "clock-role":
					c.Role = "signer"
				case "epoch":
					c.Epoch = NewProbe()
				case "uncertain":
					c.Confident = false
				case "backward":
					c.UTC--
					c.Revision++
				case "revision":
					c.UTC++
				}
				os.WriteFile(filepath.Join(x.in.directory, "owner-clock.json"), tj(c), 0644)
			}
			r, e := a.inspect(context.Background())
			requireInspectionDisabled(t, r)
			if e == nil {
				t.Fatal("independent role/clock violation admitted")
			}
		})
	}
	ifp(t, "IFP-04")
}
func TestInspectionFixturePublication(t *testing.T) {
	if !inspectionSupported(t) {
		return
	}
	x := newInspectionTest(t)
	dir := filepath.Join(x.dir, "publish")
	os.Mkdir(dir, 0755)
	wanted := []byte("sealed public fixture")
	t.Run("prefix-retry", func(t *testing.T) {
		tmp := "publish-" + digest(append([]byte("test.json\x00"), wanted...)) + ".tmp"
		os.WriteFile(filepath.Join(dir, tmp), wanted[:5], 0644)
		if e := publishInspectionFile(dir, "test.json", wanted, os.Geteuid()); e != nil {
			t.Fatal(e)
		}
		if e := publishInspectionFile(dir, "test.json", wanted, os.Geteuid()); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("foreign-preserved", func(t *testing.T) {
		if e := publishInspectionFile(dir, "test.json", []byte("other"), os.Geteuid()); e == nil {
			t.Fatal("overwrite")
		}
		b, _ := os.ReadFile(filepath.Join(dir, "test.json"))
		if string(b) != string(wanted) {
			t.Fatal("foreign final changed")
		}
		tmp := "publish-" + digest(append([]byte("other.json\x00"), wanted...)) + ".tmp"
		foreign := []byte("wrong prefix")
		os.WriteFile(filepath.Join(dir, tmp), foreign, 0644)
		if e := publishInspectionFile(dir, "other.json", wanted, os.Geteuid()); e == nil {
			t.Fatal("foreign prefix admitted")
		}
		b, _ = os.ReadFile(filepath.Join(dir, tmp))
		if string(b) != string(foreign) {
			t.Fatal("foreign prefix changed")
		}
	})
	for _, n := range []string{"../escape", "a/b", "."} {
		if e := publishInspectionFile(dir, n, wanted, os.Geteuid()); e == nil {
			t.Fatal("arbitrary path admitted")
		}
	}
	ifp(t, "IFP-02")
}
func TestInspectionFixtureRecovery(t *testing.T) {
	if !inspectionSupported(t) {
		return
	}
	base := newInspectionTest(t)
	for _, name := range []string{"gap-retry", "expired-retry", "restart-pending", "anchor-missing", "corrupt", "foreign", "hardlink", "stale-CAS", "exhaustion", "one-time"} {
		t.Run(name, func(t *testing.T) {
			x := base.clone(t)
			a := x.session(t, "owner")
			switch name {
			case "gap-retry", "expired-retry", "restart-pending":
				a.store.state.fault = func(phase string) error {
					if phase == "published" {
						return errors.New("disposable crash cut")
					}
					return nil
				}
				if _, e := a.inspect(context.Background()); e == nil {
					t.Fatal("fault not reached")
				}
				if _, e := newInspectionSession(a.store, false); e == nil {
					t.Fatal("gapped restart admitted")
				}
				a.store.state.fault = nil
				if name == "expired-retry" {
					a.monotonic = func() time.Time { return a.deadline }
					if e := a.retryPublication(); e == nil {
						t.Fatal("expired retry")
					}
					return
				}
				if e := a.retryPublication(); e != nil {
					t.Fatal("exact gap retry", e)
				}
				if _, e := newInspectionSession(a.store, false); e == nil {
					t.Fatal("pending restart reset")
				}
				if _, e := newInspectionSession(a.store, true); e == nil {
					t.Fatal("pending initialization reset")
				}
				return
			}
			if _, e := a.inspect(context.Background()); e != nil {
				t.Fatal(e)
			}
			switch name {
			case "anchor-missing":
				a.store.anchor.root.Remove("head.json")
			case "corrupt":
				a.store.state.root.WriteFile(digest(a.previous)+".json", []byte(`{}`), 0600)
			case "foreign":
				a.store.state.root.WriteFile("foreign.json", []byte("preserve"), 0600)
			case "hardlink":
				a.store.state.root.Remove("head.json")
				os.Link(x.canary, filepath.Join(x.dir, "owner-state/head.json"))
			case "stale-CAS":
				old := append([]byte(nil), a.previous...)
				if _, e := a.inspect(context.Background()); e != nil {
					t.Fatal(e)
				}
				c, _ := a.store.parse(old)
				c.Serial++
				if e := a.store.commit(inspectionPublication{old, tj(c)}); e == nil {
					t.Fatal("stale CAS admitted")
				}
				return
			case "exhaustion":
				for i := 0; i < 15; i++ {
					if _, e := a.inspect(context.Background()); e != nil {
						t.Fatal(e)
					}
				}
				if _, e := a.inspect(context.Background()); e == nil {
					t.Fatal("record exhaustion bypass")
				}
				return
			case "one-time":
				if _, e := newInspectionSession(a.store, true); e == nil {
					t.Fatal("initialized twice")
				}
				return
			}
			if _, e := newInspectionSession(a.store, false); e == nil {
				t.Fatal("unknown recovery state adopted")
			}
		})
	}
	ifp(t, "IFP-05")
	ifp(t, "IFP-06")
}
func TestInspectionFixtureNoExecution(t *testing.T) {
	if !inspectionSupported(t) {
		return
	}
	x := newInspectionTest(t)
	for _, role := range []string{"owner", "signer"} {
		a := x.session(t, role)
		r, e := a.inspect(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		requireInspectionDisabled(t, r)
		if !r.FixtureOnly || !r.Historical {
			t.Fatal("non-historical authority")
		}
		encoded := tj(r)
		var copied inspectionResult
		if StrictJSON(encoded, &copied) != nil {
			t.Fatal("status copy")
		}
		requireInspectionDisabled(t, copied)
		if _, e := newInspectionSession(a.store, true); e == nil {
			t.Fatal("JSON status reinitialized state")
		}
	}
	for _, p := range []string{"../../cmd/fased-app/main.go", "../../cmd/fased-signer/main.go"} {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(b), "openInspection") || strings.Contains(string(b), "inspectionSession") {
			t.Fatal("installed dispatch introduced")
		}
	}
	ifp(t, "IFP-07")
	ifp(t, "IFP-08")
}
