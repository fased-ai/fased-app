package releaseops

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func jb(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func mb(v interface{ ToBytes(bool) ([]byte, error) }) []byte {
	b, e := v.ToBytes(false)
	if e != nil {
		panic(e)
	}
	return b
}
func meta(b []byte) *metadata.MetaFiles {
	m := metadata.MetaFile(1)
	h := sha256.Sum256(b)
	m.Length = int64(len(b))
	m.Hashes = metadata.Hashes{"sha256": metadata.HexBytes(h[:])}
	return m
}

// This generator follows the existing trust/admission fixtures, using only
// memory-resident disposable keys. The real verifier accepts no test shortcut.
func signedFixture(t *testing.T, origin string, ca []byte, badSignature bool) (release, map[string][]byte, update.TrustRehearsalOptions, update.ReleaseAdmissionOptions) {
	t.Helper()
	now := int64(1800000000)
	binding := update.FixtureBinding{FixtureOnly: true, Purpose: update.RehearsalPurpose, Repository: strings.Repeat("a", 32), Channel: "development"}
	seed := update.TrustFixtureSeed{FixtureBinding: binding, Version: 1, Origin: origin, CreatedAt: now - 20, PreserveSHA256: sha([]byte("unopened originals/counters/holds")), InstalledFloor: 3, ReservedFloor: 4}
	root := metadata.Root(time.Unix(now+86400, 0))
	root.Signed.UnrecognizedFields = map[string]any{"fasedFixture": binding}
	keys := map[string][]signature.Signer{}
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		count := 1
		if role == "root" || role == "targets" {
			count = 3
			root.Signed.Roles[role].Threshold = 2
		}
		for i := 0; i < count; i++ {
			pub, key, e := ed25519.GenerateKey(rand.Reader)
			if e != nil {
				t.Fatal(e)
			}
			signer, e := signature.LoadSigner(key, crypto.Hash(0))
			if e != nil {
				t.Fatal(e)
			}
			k, e := metadata.KeyFromPublicKey(pub)
			if e != nil {
				t.Fatal(e)
			}
			if e = root.Signed.AddKey(k, role); e != nil {
				t.Fatal(e)
			}
			keys[role] = append(keys[role], signer)
		}
	}
	for _, key := range keys["root"][:2] {
		if _, e := root.Sign(key); e != nil {
			t.Fatal(e)
		}
	}
	seed.Root = mb(root)
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	id := update.FixtureBuildIdentity{SourceRepository: "fixture/source", SourceCommit: strings.Repeat("1", 40), SourceTree: strings.Repeat("2", 40), Workflow: "fixture/build.yml", WorkflowRevision: strings.Repeat("3", 40), Run: 42, Attempt: 1, Builder: "fixture/builder", ToolchainSHA256: sha([]byte("toolchain")), LocksSHA256: sha([]byte("locks"))}
	policy := update.FixtureAdmissionPolicy{FixtureBinding: binding, Version: 1, Profile: update.AdmissionProfile, Target: update.Target{OS: "linux", Arch: "amd64"}, Identity: id, BuilderKey: pub, NotBefore: now - 10, NotAfter: now + 500}
	unit := update.FixtureReleaseUnit{FixtureBinding: binding, Version: 1, Profile: update.AdmissionProfile, Sequence: 10, Target: policy.Target, Identity: id, Compatibility: update.FixtureReleaseCompatibility{CoreProtocol: 1, SignerProtocol: 1, OwnerEntrypoint: 4, InstallerFormat: 1}}
	files := map[string][]byte{}
	r := release{FixtureOnly: true, Purpose: purpose, RepositoryID: "7", ReleaseID: "9", Sequence: 10, PreserveSHA256: seed.PreserveSHA256, InstalledFloor: 3, ReservedFloor: 4}
	for i, role := range []string{"core", "signer", "owner-helper", "installer"} {
		b := []byte(inertPrefix + role)
		name := role + ".fixture"
		a := update.Artifact{File: name, Size: int64(len(b)), SHA256: sha(b)}
		unit.Subjects = append(unit.Subjects, update.FixtureReleaseSubject{Role: role, Target: policy.Target, SourceCommit: id.SourceCommit, Artifact: a})
		files[name] = b
		r.Objects = append(r.Objects, object{name, strconv.Itoa(i + 1), int64(len(b)), sha(b)})
	}
	r.UnitSHA256 = sha(jb(unit))
	prov := update.FixtureBuildProvenance{FixtureBinding: binding, Version: 1, Profile: update.AdmissionProfile, UnitSHA256: r.UnitSHA256, Identity: id, BuiltAt: now - 1, Subjects: unit.Subjects}
	payload := jb(prov)
	env := update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: payload, Signature: ed25519.Sign(key, append([]byte(update.FixtureProvenancePurpose+"\x00"), payload...))}
	if badSignature {
		env.Signature[0] ^= 1
	}
	evidence := jb(update.FixtureReleaseEvidence{FixtureBinding: binding, Version: 1, Profile: update.AdmissionProfile, Unit: jb(unit), Provenance: jb(env)})
	descriptor := jb(update.TrustFixtureDescriptor{FixtureBinding: binding, Version: 1, Sequence: 10, PreserveSHA256: seed.PreserveSHA256, EvidenceSHA256: sha(evidence)})
	targets := metadata.Targets(time.Unix(now+600, 0))
	targets.Signed.UnrecognizedFields = map[string]any{"fasedFixture": binding}
	tf, e := metadata.TargetFile().FromBytes("fixture/descriptor.json", descriptor, "sha256")
	if e != nil {
		t.Fatal(e)
	}
	targets.Signed.Targets["fixture/descriptor.json"] = tf
	for _, key := range keys["targets"][:2] {
		if _, e := targets.Sign(key); e != nil {
			t.Fatal(e)
		}
	}
	tb := mb(targets)
	snapshot := metadata.Snapshot(time.Unix(now+600, 0))
	snapshot.Signed.UnrecognizedFields = map[string]any{"fasedFixture": binding}
	snapshot.Signed.Meta = map[string]*metadata.MetaFiles{"targets.json": meta(tb)}
	if _, e := snapshot.Sign(keys["snapshot"][0]); e != nil {
		t.Fatal(e)
	}
	sb := mb(snapshot)
	timestamp := metadata.Timestamp(time.Unix(now+600, 0))
	timestamp.Signed.UnrecognizedFields = map[string]any{"fasedFixture": binding}
	timestamp.Signed.Meta = map[string]*metadata.MetaFiles{"snapshot.json": meta(sb)}
	if _, e := timestamp.Sign(keys["timestamp"][0]); e != nil {
		t.Fatal(e)
	}
	files["evidence.json"], files["root.json"], files["targets.json"], files["snapshot.json"], files["timestamp.json"], files["descriptor.json"] = evidence, seed.Root, tb, sb, mb(timestamp), descriptor
	for _, name := range []string{"evidence.json", "root.json", "targets.json", "snapshot.json", "timestamp.json", "descriptor.json"} {
		b := files[name]
		r.Objects = append(r.Objects, object{name, "", int64(len(b)), sha(b)})
	}
	trust := update.TrustRehearsalOptions{Development: true, Initial: true, Seed: jb(seed), SeedSHA256: sha(jb(seed)), Now: now, TLSCA: ca}
	admission := update.ReleaseAdmissionOptions{Policy: jb(policy), PolicySHA256: sha(jb(policy)), Evidence: evidence}
	return r, files, trust, admission
}
func TestReleaseReadbackSignedJourney(t *testing.T) {
	for _, name := range []string{"success-retry", "failed-readback-retry", "bad-provenance", "expired-policy", "unit-substitution", "preservation-substitution", "publisher-outage"} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			files := map[string][]byte{}
			outage, bad := false, false
			handler := http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if outage {
					w.WriteHeader(503)
					return
				}
				var filename string
				switch {
				case q.Host == apiHost:
					asset := strings.TrimPrefix(q.URL.Path, "/repositories/7/releases/assets/")
					if !number(asset) {
						http.NotFound(w, q)
						return
					}
					w.Header().Set("Location", "https://"+assetHost+"/asset/"+asset+"?one_time_token=secret")
					w.WriteHeader(302)
					return
				case q.Host == assetHost:
					n, e := strconv.Atoi(strings.TrimPrefix(q.URL.Path, "/asset/"))
					if e != nil || n < 1 || n > 4 {
						http.NotFound(w, q)
						return
					}
					filename = []string{"core.fixture", "signer.fixture", "owner-helper.fixture", "installer.fixture"}[n-1]
				case strings.HasPrefix(q.URL.Path, "/metadata/"):
					filename = strings.TrimPrefix(q.URL.Path, "/metadata/")
				case strings.HasPrefix(q.URL.Path, "/tuf/"):
					p := strings.TrimPrefix(q.URL.Path, "/tuf/")
					switch p {
					case "timestamp.json":
						filename = p
					case "1.targets.json":
						filename = "targets.json"
					case "1.snapshot.json":
						filename = "snapshot.json"
					default:
						if p == "targets/fixture/"+sha(files["descriptor.json"])+".descriptor.json" {
							filename = "descriptor.json"
						}
					}
				}
				b := append([]byte(nil), files[filename]...)
				if b == nil {
					http.NotFound(w, q)
					return
				}
				if bad && filename == "signer.fixture" {
					b[len(b)-1] ^= 1
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(b)))
				_, _ = w.Write(b)
			})
			var s *httptest.Server
			var ca []byte
			s, ca = fixtureServer(t, handler)
			r, f, trust, admission := signedFixture(t, s.URL, ca, name == "bad-provenance")
			mu.Lock()
			files = f
			mu.Unlock()
			wire := fixtureWire(t, s, ca)
			l, dir := testLedger(t)
			scratch := t.TempDir()
			// These siblings stand in for unopened financial stores; neither code path
			// knows their locations. Real installed preservation is prior VM evidence.
			sentinel := filepath.Join(scratch, "financial-sentinel")
			original := []byte("installed=3 reserved=4 originals unchanged unknown held wallet locked")
			if e := os.WriteFile(sentinel, original, 0600); e != nil {
				t.Fatal(e)
			}
			switch name {
			case "failed-readback-retry":
				mu.Lock()
				bad = true
				mu.Unlock()

			case "expired-policy":
				trust.Now += 501
			case "unit-substitution":
				r.UnitSHA256 = sha([]byte("different unit"))
			case "preservation-substitution":
				r.PreserveSHA256 = sha([]byte("different preservation"))
			case "publisher-outage":
				mu.Lock()
				outage = true
				mu.Unlock()
			}
			e := rehearse(context.Background(), l, r, wire, scratch, trust, admission)
			if name == "success-retry" {
				if e != nil {
					t.Fatal(e)
				}
				if e = rehearse(context.Background(), l, r, wire, scratch, trust, admission); e != nil {
					t.Fatal("exact retry", e)
				}
			} else {
				if e == nil {
					t.Fatal("unsafe promotion", name)
				}
				if name == "bad-provenance" && !strings.Contains(e.Error(), "provenance signature") {
					t.Fatal("wrong negative predicate", e)
				}
				if _, e = os.Stat(filepath.Join(dir, "10.readback.json")); !os.IsNotExist(e) {
					t.Fatal("failed promotion marked complete")
				}
				if _, e = os.Stat(filepath.Join(dir, "10.reserved.json")); e != nil {
					t.Fatal("failed sequence lost", e)
				}
				different := r
				different.Objects = append([]object(nil), r.Objects...)
				different.Objects[0].SHA256 = sha([]byte("different bytes"))
				if e = l.transaction(different, false); e == nil {
					t.Fatal("failed sequence reused")
				}
				if name == "failed-readback-retry" {
					mu.Lock()
					bad = false
					mu.Unlock()
					if e = rehearse(context.Background(), l, r, wire, scratch, trust, admission); e != nil {
						t.Fatal("exact recovered readback", e)
					}
				}
			}
			got, e := os.ReadFile(sentinel)
			if e != nil || string(got) != string(original) {
				t.Fatal("financial sentinel changed")
			}
			entries, e := os.ReadDir(scratch)
			if e != nil || len(entries) != 1 {
				t.Fatal("task scratch not cleaned", e)
			}
			status := Status()
			if status.Result != "BLOCKED" || status.ProductionAdmission || status.RealWallets || status.AutomaticUpdates || status.PublicActivation || len(status.Blockers) != 8 {
				t.Fatal("fixture promoted production")
			}
		})
	}
}
func TestReleaseReadinessStaticBoundary(t *testing.T) {
	r := Status()
	r.Blockers[0] = "forged approval"
	if Status().Blockers[0] == r.Blockers[0] || Status().ProductionAdmission {
		t.Fatal("mutable authority")
	}
}
