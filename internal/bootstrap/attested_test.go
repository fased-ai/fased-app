package bootstrap

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	"github.com/fased-ai/fased-app/internal/releaseprovenance/fixture"
	"github.com/fased-ai/fased-app/internal/update"
)

func attestedFixture(t *testing.T, variant string) (*firstFixture, Options, map[string][]byte) {
	t.Helper()
	f := newFirstFixture(t)
	f.archive, f.descriptor.BundleSHA256 = releaseArchive(t, f.archive, false)
	policy := releaseops.GitHubPolicy{Version: 1, Repository: "fased-ai/fased-app", RepositoryID: "7", MetadataBranch: "release-metadata", MetadataHost: "raw.githubusercontent.com", APIHost: "api.github.com", CDNHost: "release-assets.githubusercontent.com", CDNPathPrefix: "/github-production-release-asset/7/", APIVersion: releaseops.GitHubAPIVersion}
	f.seed.Binding = Binding{Version: 4, Purpose: AttestedPurpose, Repository: policy.Repository, Channel: "stable", Target: update.Target{OS: "linux", Arch: "amd64"}}
	f.seed.Root = nil
	f.seed.Origin = ""
	f.seed.SigningProfile = GitHubAttestationProfile
	f.seed.GitHub = policy
	release := ProductionRelease{SourceTree: strings.Repeat("b", 40), TagObject: strings.Repeat("c", 40), PackageLockSHA256: strings.Repeat("d", 64), GoSumSHA256: strings.Repeat("e", 64), WorkflowCommit: strings.Repeat("a", 40), RunID: "23", RunAttempt: "1"}
	archive := update.Artifact{File: archiveTarget, Size: int64(len(f.archive)), SHA256: digest(f.archive)}
	installer, entry := []byte("inert disposable installer"), []byte("inert disposable entry")
	release.InstallerSHA256 = digest(installer)
	expected := releaseprovenance.Expected{InstallerSHA256: digest(installer), ArchiveName: archive.File, ArchiveSHA256: archive.SHA256, SourceCommit: release.WorkflowCommit, WorkflowCommit: release.WorkflowCommit, Tag: "v1.2.3", RunID: "23", RunAttempt: "1"}
	pub, proof, err := fixture.NewWithOptions(releaseprovenance.Policy{Version: releaseprovenance.PolicyVersion, Repository: policy.Repository, RepositoryID: "7", OwnerID: "8", Workflow: releaseprovenance.WorkflowPath}, expected, f.now, fixture.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f.seed.Provenance = &pub
	release.Provenance = update.Artifact{File: provenanceFile, Size: int64(len(proof)), SHA256: digest(proof)}
	packet := AttestedPacket{Version: 1, ProductVersion: "1.2.3", Source: release.WorkflowCommit, BundleSHA256: f.descriptor.BundleSHA256, Archive: archive, Installer: update.Artifact{File: "fased-install-linux-amd64", Size: int64(len(installer)), SHA256: digest(installer)}, Entry: update.Artifact{File: "install.sh", Size: int64(len(entry)), SHA256: digest(entry)}, Release: release}
	payloads := map[string][]byte{archiveTarget: f.archive, provenanceFile: proof, "fased-install-linux-amd64": installer, "install.sh": entry, AttestedPacketFile: jsonBytes(t, packet)}
	if variant == "bad-installer" {
		payloads["fased-install-linux-amd64"] = []byte("altered executable")
		packet.Installer.Size = int64(len(payloads["fased-install-linux-amd64"]))
		packet.Installer.SHA256 = digest(payloads["fased-install-linux-amd64"])
		packet.Release.InstallerSHA256 = packet.Installer.SHA256
		payloads[AttestedPacketFile] = jsonBytes(t, packet)
	}
	if variant == "bad-proof" {
		payloads[provenanceFile] = []byte("invalid controlled proof")
		packet.Release.Provenance.Size = int64(len(payloads[provenanceFile]))
		packet.Release.Provenance.SHA256 = digest(payloads[provenanceFile])
		payloads[AttestedPacketFile] = jsonBytes(t, packet)
	}
	names := []string{archiveTarget, provenanceFile, "fased-install-linux-amd64", "install.sh", AttestedPacketFile}
	assets := []map[string]any{}
	for i, name := range names {
		assets = append(assets, map[string]any{"id": 20 + i, "name": name, "size": len(payloads[name]), "state": "uploaded", "digest": "sha256:" + digest(payloads[name])})
	}
	server, ca := controlledGitHubServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credential sent to public release")
		}
		if r.Host == policy.CDNHost {
			i, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, policy.CDNPathPrefix))
			if err != nil || i < 0 || i >= len(names) {
				http.NotFound(w, r)
				return
			}
			b := payloads[names[i]]
			w.Header().Set("Content-Length", strconv.Itoa(len(b)))
			w.Write(b)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/fased-ai/fased-app":
			owner := 8
			if variant == "wrong-owner" {
				owner = 9
			}
			json.NewEncoder(w).Encode(map[string]any{"id": 7, "full_name": policy.Repository, "owner": map[string]int{"id": owner}})
		case "/repos/fased-ai/fased-app/releases/latest":
			json.NewEncoder(w).Encode(map[string]any{"id": 11, "draft": variant == "draft", "prerelease": false, "immutable": variant != "mutable", "tag_name": "v1.2.3", "assets": assets})
		case "/repos/fased-ai/fased-app/git/ref/tags/v1.2.3":
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"type": "tag", "sha": release.TagObject}})
		case "/repos/fased-ai/fased-app/git/tags/" + release.TagObject:
			json.NewEncoder(w).Encode(map[string]any{"tag": "v1.2.3", "object": map[string]string{"type": "commit", "sha": release.WorkflowCommit}})
		default:
			const prefix = "/repos/fased-ai/fased-app/releases/assets/"
			n, e := strconv.Atoi(strings.TrimPrefix(r.URL.Path, prefix))
			if e != nil || n < 20 || n >= 20+len(names) {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "https://"+policy.CDNHost+policy.CDNPathPrefix+strconv.Itoa(n-20)+"?token=disposable", 302)
		}
	}))
	o := f.options()
	o.Development = false
	o.TLSCA = nil
	o.githubFixture = func(g releaseops.GitHubPolicy) (*releaseops.GitHubTransport, error) {
		return releaseops.NewControlledGitHubTransport(g, ca, func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}, func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != "93.184.216.34:443" {
				t.Error("unverified dial")
			}
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		})
	}
	return f, o, payloads
}

func TestAttestedBootstrapAndCandidate(t *testing.T) {
	f, o, payloads := attestedFixture(t, "")
	p, err := Prepare(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r := p.Report()
	if !r.ProvenanceVerified || !r.BundleVerified || r.Version != 4 || r.Sequence != 1000002000004 || r.InstallerInvoked {
		t.Fatal(r)
	}
	release, err := p.ApplicationRelease()
	if err != nil || release.Sequence != 1000002000004 {
		t.Fatal(release, err)
	}
	dir := filepath.Join(f.parent, "candidate")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, raw := range payloads {
		if err = os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	verified, err := VerifyAttestedCandidate(o, dir)
	if err != nil || !verified.ProvenanceVerified || !verified.BundleVerified || verified.MetadataVerified || verified.InstallerInvoked {
		t.Fatal(verified, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "first-install-seed.json"), o.Seed, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "go", "test", "./cmd/fased-install", "-run", "^TestAttestedCandidateCLI$", "-count=1")
	command.Dir = filepath.Join("..", "..")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly -buildvcs=false", "GOPROXY=off", "GOSUMDB=off", "FASED_ATTESTED_CANDIDATE_FIXTURE=" + dir}
	if output, e := command.CombinedOutput(); e != nil {
		t.Fatalf("actual candidate CLI: %v %s", e, output)
	}
	if err = os.WriteFile(filepath.Join(dir, archiveTarget), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyAttestedCandidate(o, dir); err == nil {
		t.Fatal("changed candidate accepted")
	}
}
func TestAttestedRefusals(t *testing.T) {
	for _, variant := range []string{"wrong-owner", "mutable", "draft", "bad-proof", "bad-installer"} {
		t.Run(variant, func(t *testing.T) {
			_, o, _ := attestedFixture(t, variant)
			if _, err := Prepare(t.Context(), o); err == nil {
				t.Fatal("untrusted release accepted")
			}
		})
	}
}
func TestAttestedSeedCannotReinterpretSigningRoots(t *testing.T) {
	f, o, _ := attestedFixture(t, "")
	f.seed.Root = []byte("old signing root")
	o.Seed = jsonBytes(t, f.seed)
	o.SeedSHA256 = digest(o.Seed)
	if _, err := parseSeed(o.Seed, o.SeedSHA256, time.Now()); err == nil {
		t.Fatal("mixed trust accepted")
	}
}
