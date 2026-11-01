//go:build linux && fased_finalizer_vm

package main

// Only go test -tags=fased_finalizer_vm links this composition. The default
// product has neither these hooks nor the fixture-only bootstrap transport.
import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	"github.com/fased-ai/fased-app/internal/releaseprovenance/fixture"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

func init() {
	applicationVMFinalizer = &applicationVMFinalizerHooks{Finish: applicationVMFinalizeInputs, Options: func(dir string, seed, ca []byte, trust applicationVMTrust) (bootstrap.Options, error) {
		return bootstrap.FinalizerVMOptions(seed, trust.SeedSHA256, ca, dir)
	}, Handler: applicationVMFinalizedHandler}
}
func applicationVMFinalizeInputs(t *testing.T, c applicationVMFinalizerComposition) {
	t.Helper()
	seed := c.Seed
	seed.Binding = bootstrap.Binding{Version: 3, Purpose: bootstrap.ProductionPurpose, Repository: "fased-ai/fased-app", Channel: "stable", Target: update.Target{OS: "linux", Arch: "amd64"}}
	seed.Origin = ""
	seed.GitHub = releaseops.GitHubPolicy{Version: 1, Repository: seed.Repository, RepositoryID: "7", MetadataBranch: "release-metadata", MetadataHost: "raw.githubusercontent.com", APIHost: "api.github.com", CDNHost: "release-assets.githubusercontent.com", CDNPathPrefix: "/github-production-release-asset/7/", APIVersion: releaseops.GitHubAPIVersion}
	manifest := applicationVMJSON(c.Manifest)
	d := bootstrap.Descriptor{Binding: seed.Binding, Sequence: 7, ProductVersion: c.Manifest.Version, Source: c.Manifest.Source, BundleSHA256: applicationVMDigest(manifest), Archive: update.Artifact{File: "fased-app-linux-amd64.tar.gz", Size: int64(len(c.Archive)), SHA256: applicationVMDigest(c.Archive)}}
	d.GitHub = releaseops.GitHubAsset{RepositoryID: "7", ReleaseID: "11", AssetID: "17", Name: d.Archive.File, Size: d.Archive.Size, SHA256: d.Archive.SHA256, CDNPath: seed.GitHub.CDNPathPrefix + "fixture-finalized"}
	d.Release = bootstrap.ProductionRelease{SourceTree: strings.Repeat("b", 40), TagObject: strings.Repeat("c", 40), PackageLockSHA256: strings.Repeat("d", 64), GoSumSHA256: strings.Repeat("e", 64), WorkflowCommit: d.Source, RunID: "23", RunAttempt: "1"}
	policy, proof, err := fixture.New(releaseprovenance.Policy{Version: 1, Repository: seed.Repository, RepositoryID: "7", OwnerID: "8", Workflow: releaseprovenance.WorkflowPath}, releaseprovenance.Expected{ArchiveName: d.Archive.File, ArchiveSHA256: d.Archive.SHA256, SourceCommit: d.Source, WorkflowCommit: d.Source, Tag: "v" + d.ProductVersion, RunID: "23", RunAttempt: "1"}, c.Now)
	if err != nil {
		t.Fatal(err)
	}
	seed.Provenance = &policy
	d.Release.Provenance = update.Artifact{File: "fased-app-linux-amd64.provenance.sigstore.json", Size: int64(len(proof)), SHA256: applicationVMDigest(proof)}
	sb := applicationVMJSON(seed)
	archive := filepath.Join(c.Dir, "repo/assets/"+d.Archive.File)
	c.Put("repo/assets/"+d.Archive.File, c.Archive, 0600)
	plan := bootstrap.ReleasePlan{Version: 1, Descriptor: d, PreviousSequence: 6, Versions: bootstrap.ReleaseVersions{Targets: 1, Snapshot: 1, Timestamp: 1}, Expires: bootstrap.ReleaseExpiries{Targets: c.Now.Add(12 * time.Hour), Snapshot: c.Now.Add(6 * time.Hour), Timestamp: c.Now.Add(2 * time.Hour)}}
	finalizer, err := bootstrap.NewReleaseFinalizer(bootstrap.ReleaseFinalizerInput{Seed: sb, SeedSHA256: applicationVMDigest(sb), ArchiveFile: archive, Provenance: proof, WorkDirectory: c.Dir, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	approvals := bootstrap.ReleaseApprovals{}
	for _, role := range []string{"targets", "snapshot", "timestamp"} {
		request, err := finalizer.Request(role, approvals)
		if err != nil {
			t.Fatal(err)
		}
		count := 1
		if role == "targets" {
			count = 2
		}
		var sigs []metadata.Signature
		for _, signer := range c.Keys[role][:count] {
			pub, err := signer.PublicKey()
			if err != nil {
				t.Fatal(err)
			}
			key, err := metadata.KeyFromPublicKey(pub)
			if err != nil {
				t.Fatal(err)
			}
			id, err := key.ID()
			if err != nil {
				t.Fatal(err)
			}
			sig, err := signer.SignMessage(bytes.NewReader(request.Payload))
			if err != nil {
				t.Fatal(err)
			}
			sigs = append(sigs, metadata.Signature{KeyID: id, Signature: sig})
		}
		switch role {
		case "targets":
			approvals.Targets = sigs
		case "snapshot":
			approvals.Snapshot = sigs
		case "timestamp":
			approvals.Timestamp = sigs
		}
	}
	out, err := finalizer.Finalize(approvals)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range out.Files {
		c.Put("repo/"+name, b, 0600)
	}
	c.Put("repo/finalization.json", applicationVMJSON(out.Report), 0600)
	c.Put("seed.json", sb, 0600)
	trust := applicationVMTrust{Finalized: true, SeedSHA256: applicationVMDigest(sb), CASHA256: applicationVMDigest(c.CA), SourceSHA256: applicationVMDigest(c.Source), TargetSHA256: applicationVMDigest(c.Target), DescriptorSHA256: out.Report.DescriptorSHA256, ProvenanceSHA256: d.Release.Provenance.SHA256, PlanSHA256: out.Report.PlanSHA256}
	c.Put("fixture.json", applicationVMJSON(trust), 0600)
	c.Put("../input-receipt.json", applicationVMJSON(map[string]any{"fixtureOnly": true, "finalizerProduced": true, "productionKeys": false, "privateSigningKeysRetained": false, "sequence": d.Sequence, "descriptorSha256": trust.DescriptorSHA256, "provenanceSha256": trust.ProvenanceSHA256, "planSha256": trust.PlanSHA256, "engineSha256": trust.TargetSHA256, "sourceSha256": trust.SourceSHA256, "targetSha256": trust.TargetSHA256}), 0600)
}
func applicationVMFinalizedHandler(trust applicationVMTrust) (http.Handler, error) {
	b, err := os.ReadFile(applicationVMInputs + "/repo/targets/production-bootstrap/" + trust.DescriptorSHA256 + ".descriptor.json")
	if err != nil {
		return nil, err
	}
	var d bootstrap.Descriptor
	if update.StrictJSON(b, &d) != nil || applicationVMDigest(b) != trust.DescriptorSHA256 || d.GitHub.RepositoryID != "7" || d.GitHub.ReleaseID != "11" || d.GitHub.AssetID != "17" {
		return nil, errors.New("exact finalized VM descriptor required")
	}
	files := http.FileServer(http.Dir(applicationVMInputs + "/repo"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			http.Error(w, "fixture GET only", 400)
			return
		}
		switch r.Host {
		case "raw.githubusercontent.com":
			if !strings.HasPrefix(r.URL.Path, "/fased-ai/fased-app/release-metadata/") {
				http.NotFound(w, r)
				return
			}
			http.StripPrefix("/fased-ai/fased-app/release-metadata", files).ServeHTTP(w, r)
		case "api.github.com":
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/repos/fased-ai/fased-app":
				json.NewEncoder(w).Encode(map[string]any{"id": 7, "full_name": "fased-ai/fased-app"})
			case "/repos/fased-ai/fased-app/releases/11":
				json.NewEncoder(w).Encode(map[string]any{"id": 11, "assets": []any{map[string]any{"id": 17, "name": d.Archive.File, "size": d.Archive.Size, "state": "uploaded"}}})
			case "/repos/fased-ai/fased-app/releases/assets/17":
				http.Redirect(w, r, "https://release-assets.githubusercontent.com"+d.GitHub.CDNPath+"?fixture=1", http.StatusFound)
			default:
				http.NotFound(w, r)
			}
		case "release-assets.githubusercontent.com":
			if r.URL.Path != d.GitHub.CDNPath {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, applicationVMInputs+"/repo/assets/"+d.Archive.File)
		default:
			http.NotFound(w, r)
		}
	}), nil
}
