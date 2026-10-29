package bootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	"github.com/fased-ai/fased-app/internal/releaseprovenance/fixture"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

func releaseArchive(t *testing.T, old []byte, engineering bool, version ...string) ([]byte, string) {
	t.Helper()
	g, err := gzip.NewReader(bytes.NewReader(old))
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	tr := tar.NewReader(g)
	files := map[string][]byte{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		files[h.Name] = b
	}
	const manifest = "fased-app-linux-amd64/bundle.json"
	var m bundle.Manifest
	if json.Unmarshal(files[manifest], &m) != nil {
		t.Fatal("fixture manifest")
	}
	m.Schema, m.UnsignedEngineering, m.Version, m.Source = 2, false, "1.2.3", strings.Repeat("a", 40)
	if len(version) == 1 {
		m.Version = version[0]
	}
	if engineering {
		m.Schema, m.UnsignedEngineering = 1, true
	}
	files[manifest] = jsonBytes(t, m)
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0700}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), digest(files[manifest])
}

func publishProduction(f *firstFixture, proof []byte, omit bool) {
	db := jsonBytes(f.t, f.descriptor)
	f.targets.Signed.Targets = map[string]*metadata.TargetFiles{}
	values := map[string][]byte{productionDescriptorTarget: db, productionArchiveTarget: f.archive, productionProvenanceTarget: proof}
	if omit {
		delete(values, productionProvenanceTarget)
	}
	f.targets.ClearSignatures()
	for name, b := range values {
		v, e := metadata.TargetFile().FromBytes(name, b, "sha256")
		if e != nil {
			f.t.Fatal(e)
		}
		f.targets.Signed.Targets[name] = v
	}
	for _, signer := range f.keys["targets"][:2] {
		if _, e := f.targets.Sign(signer); e != nil {
			f.t.Fatal(e)
		}
	}
	tg := f.bytes(f.targets)
	f.snapshot.ClearSignatures()
	f.snapshot.Signed.Meta = map[string]*metadata.MetaFiles{"targets.json": metaFile(1, tg)}
	if _, e := f.snapshot.Sign(f.keys["snapshot"][0]); e != nil {
		f.t.Fatal(e)
	}
	sn := f.bytes(f.snapshot)
	f.timestamp.ClearSignatures()
	f.timestamp.Signed.Meta = map[string]*metadata.MetaFiles{"snapshot.json": metaFile(1, sn)}
	if _, e := f.timestamp.Sign(f.keys["timestamp"][0]); e != nil {
		f.t.Fatal(e)
	}
	f.files = map[string][]byte{"/timestamp.json": f.bytes(f.timestamp), "/1.snapshot.json": sn, "/1.targets.json": tg,
		"/targets/production-bootstrap/" + digest(db) + ".descriptor.json": db,
		"/targets/provenance/" + digest(proof) + ".sigstore.json":          proof}
}

func productionFixture(t *testing.T, variant string) (*firstFixture, Options, *atomic.Int64) {
	t.Helper()
	f := newFirstFixture(t)
	version := "1.2.3"
	if variant == "prerelease" {
		version = "1.2.3-rc.1"
	}
	f.archive, f.descriptor.BundleSHA256 = releaseArchive(t, f.archive, variant == "engineering-bundle", version)
	f.seed.Binding = Binding{Version: 3, Purpose: ProductionPurpose, Repository: "fased-ai/fased-app", Channel: "stable", Target: update.Target{OS: "linux", Arch: "amd64"}}
	f.seed.Origin = ""
	policy := releaseops.GitHubPolicy{Version: 1, Repository: f.seed.Repository, RepositoryID: "7", MetadataBranch: "release-metadata", MetadataHost: "raw.githubusercontent.com", APIHost: "api.github.com", CDNHost: "release-assets.githubusercontent.com", CDNPathPrefix: "/github-production-release-asset/7/", APIVersion: releaseops.GitHubAPIVersion}
	f.seed.GitHub = policy
	f.descriptor.Binding, f.descriptor.ProductVersion, f.descriptor.Source = f.seed.Binding, version, strings.Repeat("a", 40)
	f.descriptor.Archive = update.Artifact{File: archiveTarget, Size: int64(len(f.archive)), SHA256: digest(f.archive)}
	asset := releaseops.GitHubAsset{RepositoryID: "7", ReleaseID: "11", AssetID: "17", Name: archiveTarget, SHA256: f.descriptor.Archive.SHA256, Size: f.descriptor.Archive.Size, CDNPath: policy.CDNPathPrefix + "fixture-release"}
	f.descriptor.GitHub = asset
	f.descriptor.Release = ProductionRelease{SourceTree: strings.Repeat("b", 40), TagObject: strings.Repeat("c", 40), PackageLockSHA256: strings.Repeat("d", 64), GoSumSHA256: strings.Repeat("e", 64), WorkflowCommit: f.descriptor.Source, RunID: "23", RunAttempt: "1"}
	expected := f.descriptor.provenanceExpected()
	if variant == "wrong-signed-source" {
		expected.SourceCommit, expected.WorkflowCommit = strings.Repeat("f", 40), strings.Repeat("f", 40)
	}
	fixtureOptions := fixture.Options{}
	if variant == "complete-v2" {
		fixtureOptions.RekorV2, fixtureOptions.Timestamp = true, true
	}
	p, proof, err := fixture.NewWithOptions(releaseprovenance.Policy{Version: releaseprovenance.PolicyVersion, Repository: f.seed.Repository, RepositoryID: "7", OwnerID: "8", Workflow: releaseprovenance.WorkflowPath}, expected, f.now, fixtureOptions)
	if err != nil {
		t.Fatal(err)
	}
	if variant == "complete-v2" {
		f.now = time.Now().UTC()
	}
	f.seed.Provenance = &p
	f.descriptor.Release.Provenance = update.Artifact{File: provenanceFile, Size: int64(len(proof)), SHA256: digest(proof)}
	switch variant {
	case "wrong-workflow":
		f.descriptor.Release.WorkflowCommit = strings.Repeat("f", 40)
	case "wrong-repository":
		f.seed.Provenance.RepositoryID = "9"
	case "legacy-policy":
		f.seed.Binding = Binding{Version: 2, Purpose: GitHubPurpose, DevelopmentOnly: true, Repository: f.seed.Repository, Channel: "development", Target: f.seed.Target}
		f.descriptor.Binding = f.seed.Binding
	case "bad-proof-hash":
		f.descriptor.Release.Provenance.SHA256 = strings.Repeat("0", 64)
	}
	publishProduction(f, proof, variant == "missing-provenance")
	hits := &atomic.Int64{}
	server, ca := controlledGitHubServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
			t.Error("credential crossed fixture transport")
		}
		switch r.Host {
		case policy.MetadataHost:
			rel, ok := strings.CutPrefix(r.URL.Path, "/"+policy.Repository+"/"+policy.MetadataBranch)
			if !ok {
				http.NotFound(w, r)
				return
			}
			b := f.files[rel]
			if b == nil {
				http.NotFound(w, r)
				return
			}
			w.Write(b)
		case policy.APIHost:
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/repos/fased-ai/fased-app":
				json.NewEncoder(w).Encode(map[string]any{"id": 7, "full_name": policy.Repository})
			case "/repos/fased-ai/fased-app/releases/11":
				json.NewEncoder(w).Encode(map[string]any{"id": 11, "assets": []any{map[string]any{"id": 17, "name": asset.Name, "size": asset.Size, "state": "uploaded"}}})
			case "/repos/fased-ai/fased-app/releases/assets/17":
				http.Redirect(w, r, "https://"+policy.CDNHost+asset.CDNPath+"?token=fixture-transient", http.StatusFound)
			default:
				http.NotFound(w, r)
			}
		case policy.CDNHost:
			w.Header().Set("Content-Length", strconv.Itoa(len(f.archive)))
			w.Write(f.archive)
		default:
			t.Error("unexpected fixture host")
			http.NotFound(w, r)
		}
	}))
	o := f.options()
	o.Development = false
	o.TLSCA = nil
	if variant == "legacy-policy" {
		o.Development = true
	}
	o.githubFixture = func(g releaseops.GitHubPolicy) (*releaseops.GitHubTransport, error) {
		return releaseops.NewControlledGitHubTransport(g, ca, func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}, func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != "93.184.216.34:443" {
				t.Error("unchecked fixture dial")
			}
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		})
	}
	if variant == "ordinary-disabled" {
		o.githubFixture = nil
	}
	if variant == "wrong-mode" {
		o.Development = true
	}
	return f, o, hits
}

func TestProductionFirstInstallAdmissionAndHandoff(t *testing.T) {
	for _, variant := range []string{"complete", "complete-v2", "ordinary-disabled", "missing-provenance", "wrong-signed-source", "wrong-workflow", "wrong-repository", "legacy-policy", "bad-proof-hash", "engineering-bundle", "wrong-mode", "prerelease"} {
		t.Run(variant, func(t *testing.T) {
			f, o, hits := productionFixture(t, variant)
			p, err := Prepare(t.Context(), o)
			if variant != "complete" && variant != "complete-v2" {
				if err == nil {
					p.Close()
					t.Fatal("invalid production admission accepted")
				}
				reason := map[string]string{"ordinary-disabled": "ordinary GitHub bootstrap activation is disabled", "missing-provenance": "TUF targets", "wrong-signed-source": "release statement identity mismatch", "wrong-workflow": "production release/provenance identity", "wrong-repository": "production seed requires", "legacy-policy": "development seed cannot acquire", "bad-proof-hash": "production release/provenance identity", "engineering-bundle": "distinct headless Ubuntu release-bundle format", "wrong-mode": "bootstrap mode differs", "prerelease": "production release/provenance identity"}[variant]
				if reason == "" || !strings.Contains(err.Error(), reason) {
					t.Fatalf("%s failed at the wrong boundary: %v", variant, err)
				}
				if _, e := os.Stat(o.Directory); !os.IsNotExist(e) {
					t.Fatal("refused candidate left bootstrap output", e)
				}
				if _, e := os.Stat(f.marker); !os.IsNotExist(e) {
					t.Fatal("refused candidate executed")
				}
				if variant == "ordinary-disabled" && hits.Load() != 0 {
					t.Fatal("disabled ordinary path made requests")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			report := p.Report()
			if report.DevelopmentOnly || !report.ProvenanceVerified || report.PublicActivation || report.ProvenanceSHA256 == "" || report.TrustPolicySHA256 == "" {
				t.Fatal("incorrect production admission report", report)
			}
			if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
				t.Fatal("verification executed installer")
			}
			if err := p.Run(t.Context(), InstallOptions{Prefix: filepath.Join(f.parent, "app"), DataDirectory: filepath.Join(f.parent, "data")}, nil, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(f.marker); err != nil {
				t.Fatal("explicit installer handoff absent", err)
			}
			if err := p.Run(t.Context(), InstallOptions{}, nil, io.Discard, io.Discard); err == nil {
				t.Fatal("handoff replayed")
			}
		})
	}
}

func TestProductionFirstInstallFinalProofAndTimeRevalidation(t *testing.T) {
	for _, variant := range []string{"proof", "expiry", "backward"} {
		t.Run(variant, func(t *testing.T) {
			f, o, _ := productionFixture(t, "complete")
			p, err := Prepare(t.Context(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			switch variant {
			case "proof":
				p.metadata["provenance.sigstore.json"] = []byte("{}")
			case "expiry":
				p.options.now = func() time.Time { return f.now.Add(72 * time.Hour) }
			case "backward":
				p.options.now = func() time.Time { return f.now.Add(-time.Second) }
			}
			if err := p.Run(t.Context(), InstallOptions{}, nil, io.Discard, io.Discard); err == nil {
				t.Fatal("invalid final admission executed")
			}
			if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
				t.Fatal("invalid final state executed")
			}
		})
	}
}

func TestDevelopmentBootstrapRefusesReleaseFormat(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			var f *firstFixture
			var o Options
			if version == 1 {
				f = newFirstFixture(t)
				f.archive, f.descriptor.BundleSHA256 = releaseArchive(t, f.archive, false)
				f.descriptor.ProductVersion, f.descriptor.Source = "1.2.3", strings.Repeat("a", 40)
				f.descriptor.Archive.Size, f.descriptor.Archive.SHA256 = int64(len(f.archive)), digest(f.archive)
				f.publish()
				o = f.options()
			} else {
				f, o, _ = productionFixture(t, "complete")
				f.seed.Binding = Binding{Version: 2, Purpose: GitHubPurpose, DevelopmentOnly: true, Repository: f.seed.Repository, Channel: "development", Target: f.seed.Target}
				f.seed.Provenance = nil
				f.descriptor.Binding, f.descriptor.Release = f.seed.Binding, ProductionRelease{}
				f.targets.Signed.Targets = map[string]*metadata.TargetFiles{}
				f.publish()
				o.Development, o.Seed = true, jsonBytes(t, f.seed)
				o.SeedSHA256 = digest(o.Seed)
			}
			p, err := Prepare(t.Context(), o)
			if err == nil {
				p.Close()
				t.Fatal("development seed admitted release bundle")
			}
			if !strings.Contains(err.Error(), "development bootstrap cannot acquire") {
				t.Fatal("wrong refusal boundary", err)
			}
			if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
				t.Fatal("development seed executed release bundle")
			}
			if _, err := os.Stat(o.Directory); !os.IsNotExist(err) {
				t.Fatal("refused bundle output retained")
			}
		})
	}
}

func TestProductionBootstrapCompiledGate(t *testing.T) {
	binary := os.Getenv("FASED_TEST_INITIAL_INSTALLER")
	if binary == "" {
		t.Skip("explicit task-built ordinary installer binary not supplied")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("absolute compiled fixture binary required")
	}
	f, o, hits := productionFixture(t, "complete")
	seedFile := filepath.Join(f.parent, "disposable-seed.json")
	if err := os.WriteFile(seedFile, o.Seed, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--seed-file", seedFile, "--seed-sha256", o.SeedSHA256, "--output", o.Directory, "--install", "--start=false"}
	command := exec.CommandContext(t.Context(), binary, args...)
	out, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("ordinary GitHub bootstrap activation is disabled")) {
		t.Fatalf("compiled ordinary production gate = %v, %s", err, out)
	}
	if hits.Load() != 0 {
		t.Fatal("compiled ordinary gate made requests")
	}
	if _, err := os.Stat(o.Directory); !os.IsNotExist(err) {
		t.Fatal("disabled compiled gate created output")
	}
	if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatal("disabled compiled gate invoked installer")
	}
}
