package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	"github.com/fased-ai/fased-app/internal/releaseprovenance/fixture"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestAttestedInspectOlderPublicReleaseWithoutExecutingIt(t *testing.T) {
	previous := core.Version
	core.Version = "1.2.4"
	t.Cleanup(func() { core.Version = previous })
	_, o, _ := attestedFixture(t, "staged")
	d, identity, err := Inspect(t.Context(), o)
	if err != nil || d.ProductVersion != "1.2.3" || identity == "" {
		t.Fatalf("authenticated older-public metadata must remain inspectable: %v %v", d, err)
	}
	if _, err := os.Stat(o.Directory); !os.IsNotExist(err) {
		t.Fatal("metadata inspection created executable output", err)
	}
	if p, err := Prepare(t.Context(), o); err == nil {
		p.Close()
		t.Fatal("metadata inspection authorized an older executable")
	}
	if _, err := os.Stat(o.Directory); !os.IsNotExist(err) {
		t.Fatal("refused older executable created output", err)
	}
}

func TestAttestedPairedBundleCandidateKeepsLegacyInstallerClosed(t *testing.T) {
	f, o, payloads := attestedFixture(t, "paired-staged")
	dir := filepath.Join(f.parent, "paired-candidate")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, b := range payloads {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := VerifyAttestedCandidate(o, dir)
	if err != nil || !r.ProvenanceVerified || !r.LinuxBundleVerified || r.LinuxPackageDigestsVerified {
		t.Fatal("verified paired bundle was not admitted", r, err)
	}
	o.githubFixture = nil
	o.Directory = filepath.Join(f.parent, "paired-held")
	admission := CandidateAdmission{PacketSHA256: digest(payloads[AttestedPacketFile]), OperationID: strings.Repeat("f", 32), BaselineSHA256: strings.Repeat("e", 64), ExpiresAt: f.now.Add(5 * time.Minute).Unix()}
	p, err := PrepareAttestedCandidate(t.Context(), o, dir, admission)
	if err != nil {
		t.Fatal("paired candidate could not be held", err)
	}
	defer p.Close()
	if err := p.revalidate(); err == nil {
		t.Fatal("paired candidate was admitted to the legacy archive installer")
	}
	if _, err := p.PackageIdentity(); err == nil {
		t.Fatal("ordinary process obtained the protected package identity")
	}
	var stream bytes.Buffer
	if err := p.WriteCandidate(t.Context(), &stream); err != nil {
		t.Fatal("held paired candidate could not reach the protected receiver", err)
	}
	received := filepath.Join(f.parent, "received-paired")
	if err := os.Mkdir(received, 0700); err != nil {
		t.Fatal(err)
	}
	if err := ReceiveCandidate(t.Context(), &stream, received, admission.PacketSHA256); err != nil {
		t.Fatal("protected stream refused the same candidate", err)
	}
	if _, err := VerifyAttestedCandidate(o, received); err != nil {
		t.Fatal("received candidate lost its attestation", err)
	}
	// The fixed helper sets this internal flag after validating its owner.
	p.options.protectedOwner = os.Getuid()
	identity, err := p.PackageIdentity()
	if err != nil || identity.ReleaseSHA256 != admission.PacketSHA256 || identity.Signer.SHA256 != digest([]byte("disposable signer")) || identity.Archive.File != archiveTarget || identity.Deb.File != "" || identity.RPM.File != "" {
		t.Fatal("protected identity differs from the attested pair", identity, err)
	}
	if err := os.WriteFile(filepath.Join(o.Directory, "bundle", "fased-signer"), []byte("changed signer"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PackageIdentity(); err == nil {
		t.Fatal("modified signer was accepted after original bundle verification")
	}
}

func TestPublicPairedBundleUsesPackageController(t *testing.T) {
	_, o, payloads := attestedFixture(t, "paired-staged")
	if p, err := Prepare(t.Context(), o); err == nil {
		p.Close()
		t.Fatal("paired release reached legacy installer")
	}
	p, err := PrepareNative(t.Context(), o)
	if err != nil {
		t.Fatal("package-aware public acquisition refused paired bundle", err)
	}
	defer p.Close()
	r := p.Report()
	if !r.ProvenanceVerified || !r.LinuxBundleVerified || r.LinuxPackageDigestsVerified || r.DescriptorSHA256 != digest(payloads[AttestedPacketFile]) {
		t.Fatal("paired public release identity differs", r)
	}
	remoteHash := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00remote", os.Getuid(), r.DescriptorSHA256)))
	if err := p.ValidateFreshPackageCandidate("remote", fmt.Sprintf("%x", remoteHash)[:32]); err != nil {
		t.Fatal("paired public release cannot enter its exact Remote setup", err)
	}
	if err := p.ValidateFreshPackageCandidate("remote", strings.Repeat("f", 32)); err == nil {
		t.Fatal("paired public release accepted another Remote instance")
	}
	var stream bytes.Buffer
	if err := p.WriteCandidate(t.Context(), &stream); err != nil {
		t.Fatal("paired public release could not reach the protected receiver", err)
	}
}

func TestNativeLinuxCandidateInspectionBindsBothPackagesWithoutInstalling(t *testing.T) {
	f, o, payloads := attestedFixture(t, "staged")
	var packet AttestedPacket
	if err := update.StrictJSON(payloads[AttestedPacketFile], &packet); err != nil {
		t.Fatal(err)
	}
	packet.Version = 3
	deb, rpm := []byte("disposable Debian package"), []byte("disposable RPM package")
	packet.Deb = update.Artifact{File: "fased-app-linux-amd64.deb", Size: int64(len(deb)), SHA256: digest(deb)}
	packet.RPM = update.Artifact{File: "fased-app-linux-amd64.rpm", Size: int64(len(rpm)), SHA256: digest(rpm)}
	d := Descriptor{ProductVersion: packet.ProductVersion, Source: packet.Source, Archive: packet.Archive, Release: packet.Release}
	expected := d.provenanceExpected()
	expected.DebSHA256, expected.RPMSHA256 = packet.Deb.SHA256, packet.RPM.SHA256
	policy, proof, err := fixture.New(*f.seed.Provenance, expected, f.now)
	if err != nil {
		t.Fatal(err)
	}
	seed := f.seed
	seed.Provenance = &policy
	o.Seed = jsonBytes(t, seed)
	o.SeedSHA256 = digest(o.Seed)
	packet.Release.Provenance = update.Artifact{File: provenanceFile, Size: int64(len(proof)), SHA256: digest(proof)}
	payloads[AttestedPacketFile] = jsonBytes(t, packet)
	payloads[provenanceFile], payloads[packet.Deb.File], payloads[packet.RPM.File] = proof, deb, rpm
	dir := filepath.Join(f.parent, "native-candidate")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range payloads {
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := VerifyAttestedCandidate(o, dir)
	if err != nil || !report.LinuxPackageDigestsVerified || !report.ProvenanceVerified || report.InstallerInvoked {
		t.Fatalf("valid native candidate inspection failed: %+v %v", report, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "first-install-seed.json"), o.Seed, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "go", "test", "./cmd/fased-install", "-run", "^(TestAttestedCandidateCLI|TestPackageCandidateStreamCLI)$", "-count=1")
	command.Dir = filepath.Join("..", "..")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly -buildvcs=false", "GOPROXY=off", "GOSUMDB=off", "FASED_ATTESTED_CANDIDATE_FIXTURE=" + dir, "FASED_NATIVE_PACKAGE_CANDIDATE=1"}
	if cache := os.Getenv("GOCACHE"); cache != "" {
		command.Env = append(command.Env, "GOCACHE="+cache)
	}
	if output, e := command.CombinedOutput(); e != nil {
		t.Fatalf("native candidate installer entry: %v %s", e, output)
	}
	if err = os.WriteFile(filepath.Join(dir, packet.RPM.File), []byte("changed RPM"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyAttestedCandidate(o, dir); err == nil {
		t.Fatal("modified RPM accepted")
	}
	if err = os.WriteFile(filepath.Join(dir, packet.RPM.File), rpm, 0600); err != nil {
		t.Fatal(err)
	}
	alteredRPM := []byte("different RPM with a matching inventory")
	changed := packet
	changed.RPM.Size, changed.RPM.SHA256 = int64(len(alteredRPM)), digest(alteredRPM)
	if err = os.WriteFile(filepath.Join(dir, changed.RPM.File), alteredRPM, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, AttestedPacketFile), jsonBytes(t, changed), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyAttestedCandidate(o, dir); err == nil {
		t.Fatal("changed RPM and inventory bypassed the original signed proof")
	}
	if err = os.WriteFile(filepath.Join(dir, packet.RPM.File), rpm, 0600); err != nil {
		t.Fatal(err)
	}
	legacy := packet
	legacy.Version = 2
	if err = os.WriteFile(filepath.Join(dir, AttestedPacketFile), jsonBytes(t, legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyAttestedCandidate(o, dir); err == nil {
		t.Fatal("legacy candidate admitted package fields without native proof")
	}
	if err = os.WriteFile(filepath.Join(dir, AttestedPacketFile), payloads[AttestedPacketFile], 0600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "linux" {
		return // The protected installer and update entry are Linux-only.
	}
	// Preparation retains both attested packages, but the legacy installed
	// controller must not execute the archive as a package installation.
	o.githubFixture = nil
	o.Directory = filepath.Join(f.parent, "native-held")
	a := CandidateAdmission{PacketSHA256: digest(payloads[AttestedPacketFile]), OperationID: strings.Repeat("1", 32), BaselineSHA256: strings.Repeat("2", 64), ExpiresAt: f.now.Add(10 * time.Minute).Unix()}
	admitted, err := PrepareAttestedCandidate(t.Context(), o, dir, a)
	if err != nil {
		t.Fatal("native candidate preparation failed", err)
	}
	defer admitted.Close()
	if got := admitted.Report(); !got.LinuxPackageDigestsVerified || got.DescriptorSHA256 != a.PacketSHA256 {
		t.Fatal("native package identity was not retained")
	}
	if _, err = admitted.PackageIdentity(); err == nil {
		t.Fatal("ordinary candidate was presented as protected helper verification")
	}
	if _, _, err = admitted.AcquireMaintenanceInstaller(t.Context()); err == nil {
		t.Fatal("ordinary candidate gained protected installer acquisition")
	}
	// Only the installed helper sets this internal flag. This controlled test
	// exercises the same proof and held-file reader without running as root.
	admitted.options.protectedOwner = os.Getuid()
	identity, err := admitted.PackageIdentity()
	if err != nil || identity.ReleaseSHA256 != a.PacketSHA256 || identity.RPM != packet.RPM || identity.Deb != packet.Deb || identity.InstallerSHA256 != packet.Installer.SHA256 {
		t.Fatal("protected native identity differs from the original proof", err)
	}
	installerPath, installer, err := admitted.AcquireMaintenanceInstaller(t.Context())
	if err != nil || installer != packet.Installer {
		t.Fatal("protected native candidate could not retain its exact installer", err)
	}
	if got, readErr := os.ReadFile(installerPath); readErr != nil || digest(got) != packet.Installer.SHA256 {
		t.Fatal("protected installer differs from the signed candidate", readErr)
	}
	if _, err = admitted.ApplicationRelease(); err == nil {
		t.Fatal("native package reached the legacy application updater")
	}
	if err = admitted.Run(t.Context(), InstallOptions{Profile: "local"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("native package reached the legacy installer")
	}
	var stream bytes.Buffer
	if err = admitted.WriteCandidate(t.Context(), &stream); err != nil {
		t.Fatal("native candidate stream failed", err)
	}
	received := filepath.Join(f.parent, "native-received")
	if err = os.Mkdir(received, 0700); err != nil {
		t.Fatal(err)
	}
	if err = ReceiveCandidate(t.Context(), bytes.NewReader(stream.Bytes()), received, a.PacketSHA256); err != nil {
		t.Fatal("native candidate receiver failed", err)
	}
	if _, err = VerifyAttestedCandidate(o, received); err != nil {
		t.Fatal("native candidate stream lost its provenance", err)
	}
	bad := append([]byte(nil), stream.Bytes()...)
	index := bytes.Index(bad, rpm)
	if index < 0 {
		t.Fatal("RPM package was not streamed")
	}
	bad[index] ^= 1
	corrupt := filepath.Join(f.parent, "native-corrupt")
	if err = os.Mkdir(corrupt, 0700); err != nil {
		t.Fatal(err)
	}
	if err = ReceiveCandidate(t.Context(), bytes.NewReader(bad), corrupt, a.PacketSHA256); err == nil {
		t.Fatal("changed package crossed the candidate receiver")
	}
	if err = os.WriteFile(filepath.Join(o.Directory, "candidate-rpm"), []byte("changed RPM"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = admitted.PackageIdentity(); err == nil {
		t.Fatal("changed native package retained protected identity")
	}
	if _, _, err = admitted.AcquireMaintenanceInstaller(t.Context()); err == nil {
		t.Fatal("changed native package retained installer acquisition")
	}
	if err = admitted.WriteCandidate(t.Context(), io.Discard); err == nil {
		t.Fatal("changed held package was streamed")
	}
}

func TestAttestedPublicNativeMetadataKeepsPackageIdentity(t *testing.T) {
	for _, variant := range []string{"native-published", "native-wrong-subject", "native-missing", "legacy-extra"} {
		t.Run(variant, func(t *testing.T) {
			_, o, payloads := attestedFixture(t, variant)
			descriptor, identity, err := Inspect(t.Context(), o)
			if variant != "native-published" {
				if err == nil {
					t.Fatal("published package inventory bypassed its original provenance")
				}
				return
			}
			if err != nil {
				t.Fatal("authenticated native public metadata was rejected", err)
			}
			if identity != digest(payloads[AttestedPacketFile]) || descriptor.Sequence == 0 {
				t.Fatal("public native package identity differs from the candidate")
			}
			if ApplicationDescriptorMatches(descriptor, ApplicationDescriptorIdentity(descriptor)) {
				t.Fatal("native release accepted the legacy archive identity")
			}
			if p, err := Prepare(t.Context(), o); err == nil {
				p.Close()
				t.Fatal("legacy archive installer accepted a native package release")
			}
			if _, err := os.Lstat(o.Directory); !os.IsNotExist(err) {
				t.Fatal("unsupported native release created installation output", err)
			}
		})
	}
}

func TestPublicNativeReleaseRetainsAttestedPackagesForTheInstalledController(t *testing.T) {
	_, o, payloads := attestedFixture(t, "native-published")
	p, err := PrepareNative(t.Context(), o)
	if err != nil {
		t.Fatalf("authenticated native release acquisition: %v", err)
	}
	defer p.Close()
	if !p.Report().LinuxPackageDigestsVerified || p.Report().DescriptorSHA256 != digest(payloads[AttestedPacketFile]) {
		t.Fatal("native acquisition lost its signed release identity")
	}
	instance := strings.Repeat("a", 32)
	if err = p.ValidateFreshPackageCandidate("local", instance); err != nil {
		t.Fatal("published native package cannot enter fresh Local setup", err)
	}
	if err = p.ValidateFreshPackageCandidate("remote", instance); err == nil {
		t.Fatal("Local package verification admitted Remote setup")
	}
	remoteHash := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00remote", os.Getuid(), digest(payloads[AttestedPacketFile]))))
	remoteInstance := fmt.Sprintf("%x", remoteHash)[:32]
	if err = p.ValidateFreshPackageCandidate("remote", remoteInstance); err != nil {
		t.Fatal("authenticated public release cannot enter its exact Remote setup", err)
	}
	for held, name := range map[string]string{
		"candidate-deb":       "fased-app-linux-amd64.deb",
		"candidate-rpm":       "fased-app-linux-amd64.rpm",
		"candidate-installer": "fased-install-linux-amd64",
		"candidate-entry":     "install.sh",
	} {
		got, err := os.ReadFile(filepath.Join(o.Directory, held))
		if err != nil || !bytes.Equal(got, payloads[name]) {
			t.Fatalf("held %s differs from the signed release: %v", held, err)
		}
	}
	// The protected helper must reuse the retained attested installer. The
	// public asset may disappear after acquisition without changing this update.
	p.options.protectedOwner = os.Getuid()
	originalInstaller := payloads["fased-install-linux-amd64"]
	payloads["fased-install-linux-amd64"] = []byte("unavailable after acquisition")
	defer func() { payloads["fased-install-linux-amd64"] = originalInstaller }()
	installerPath, installer, err := p.AcquireMaintenanceInstaller(t.Context())
	if err != nil || installer.SHA256 != digest(originalInstaller) {
		t.Fatal("verified native installer was downloaded again", err)
	}
	if got, readErr := os.ReadFile(installerPath); readErr != nil || !bytes.Equal(got, originalInstaller) {
		t.Fatal("retained native installer differs", readErr)
	}
	if _, err = p.ApplicationRelease(); err == nil {
		t.Fatal("native packages reached the legacy application updater")
	}
	release, installerHash, err := p.PackageApplicationRelease()
	if err != nil || release.ManifestSHA256 != digest(payloads[AttestedPacketFile]) || release.Sequence != p.Report().Sequence || installerHash != digest(originalInstaller) {
		t.Fatalf("package updater lost the authenticated release identity: %+v %v", release, err)
	}
	if err = p.Run(t.Context(), InstallOptions{Profile: "local"}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("native packages reached the legacy archive installer")
	}
	var stream bytes.Buffer
	if err = p.WriteCandidate(t.Context(), &stream); err != nil {
		t.Fatalf("verified package handoff: %v", err)
	}
	received := t.TempDir()
	if err = os.Chmod(received, 0700); err != nil {
		t.Fatal(err)
	}
	if err = ReceiveCandidate(t.Context(), bytes.NewReader(stream.Bytes()), received, p.Report().DescriptorSHA256); err != nil {
		t.Fatalf("package receiver: %v", err)
	}
	if _, err = VerifyAttestedCandidate(o, received); err != nil {
		t.Fatalf("receiver lost the original release proof: %v", err)
	}
	if err = os.WriteFile(filepath.Join(o.Directory, "candidate-rpm"), []byte("changed package"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = p.WriteCandidate(t.Context(), io.Discard); err == nil {
		t.Fatal("changed native package crossed the handoff")
	}
	if _, _, err = p.PackageApplicationRelease(); err == nil {
		t.Fatal("changed package was offered as an update")
	}
}

func TestPublicNativeAcquisitionRejectsChangedDownload(t *testing.T) {
	_, o, payloads := attestedFixture(t, "native-published")
	// The release's immutable asset identity was recorded before this change.
	payloads["fased-app-linux-amd64.rpm"] = []byte("different RPM")
	if p, err := PrepareNative(t.Context(), o); err == nil {
		p.Close()
		t.Fatal("changed package was acquired")
	}
	if _, err := os.Lstat(o.Directory); !os.IsNotExist(err) {
		t.Fatal("failed acquisition left a package directory", err)
	}
}

func TestNativeAcquisitionRefusesLegacyArchive(t *testing.T) {
	_, o, _ := attestedFixture(t, "")
	if p, err := PrepareNative(t.Context(), o); err == nil {
		p.Close()
		t.Fatal("native acquisition accepted a legacy archive")
	}
	if _, err := os.Lstat(o.Directory); !os.IsNotExist(err) {
		t.Fatal("refused legacy archive created package output", err)
	}
}

func TestOlderPublicInspectionStillRequiresAuthenticProof(t *testing.T) {
	previous := core.Version
	core.Version = "1.2.4"
	t.Cleanup(func() { core.Version = previous })
	_, o, _ := attestedFixture(t, "bad-proof")
	if _, _, err := Inspect(t.Context(), o); err == nil {
		t.Fatal("older public metadata bypassed provenance validation")
	}
}

func TestLegacyBootstrapSequenceIsNotAnAttestedProductVersion(t *testing.T) {
	previous := core.Version
	core.Version = "9999.0.0"
	t.Cleanup(func() { core.Version = previous })
	f := newFirstFixture(t)
	p, err := Prepare(t.Context(), f.options())
	if err != nil {
		t.Fatal("legacy metadata sequence was compared to a product version", err)
	}
	p.Close()
}

func attestedFixture(t *testing.T, variant string, profile ...string) (*firstFixture, Options, map[string][]byte) {
	t.Helper()
	f := newFirstFixture(t, profile...)
	f.archive, f.descriptor.BundleSHA256 = releaseArchive(t, f.archive, false)
	if strings.HasPrefix(variant, "paired-") {
		f.archive, f.descriptor.BundleSHA256 = pairedReleaseArchive(t, f.archive)
	}
	policy := releaseops.GitHubPolicy{Version: 1, Repository: "fased-ai/fased-app", RepositoryID: "7", MetadataBranch: "release-metadata", MetadataHost: "raw.githubusercontent.com", APIHost: "api.github.com", CDNHost: "release-assets.githubusercontent.com", CDNPathPrefix: "/github-production-release-asset/7/", APIVersion: releaseops.GitHubAPIVersion}
	f.seed.Binding = Binding{Version: 4, Purpose: AttestedPurpose, Repository: policy.Repository, Channel: "stable", Target: update.Target{OS: "linux", Arch: "amd64"}}
	f.seed.Root = nil
	f.seed.Origin = ""
	f.seed.SigningProfile = GitHubAttestationProfile
	f.seed.GitHub = policy
	release := ProductionRelease{SourceTree: strings.Repeat("b", 40), TagObject: strings.Repeat("c", 40), PackageLockSHA256: strings.Repeat("d", 64), GoSumSHA256: strings.Repeat("e", 64), WorkflowCommit: strings.Repeat("a", 40), RunID: "23", RunAttempt: "1"}
	archive := update.Artifact{File: archiveTarget, Size: int64(len(f.archive)), SHA256: digest(f.archive)}
	installer, entry := []byte("inert disposable installer"), []byte("inert disposable entry")
	if variant == "running-installer" || variant == "staged-running-installer" || variant == "native-running-installer" {
		var err error
		installer, err = os.ReadFile("/proc/self/exe")
		if err != nil {
			t.Fatal(err)
		}
	}
	if variant == "staged-installer-file" {
		var err error
		installer, err = os.ReadFile(os.Getenv("FASED_TEST_CANDIDATE_INSTALLER"))
		if err != nil {
			t.Fatal(err)
		}
	}
	release.InstallerSHA256 = digest(installer)
	expected := releaseprovenance.Expected{InstallerSHA256: digest(installer), ArchiveName: archive.File, ArchiveSHA256: archive.SHA256, SourceCommit: release.WorkflowCommit, WorkflowCommit: release.WorkflowCommit, Tag: "v1.2.3", RunID: "23", RunAttempt: "1"}
	tagObject := release.TagObject
	packetVersion := 1
	if variant == "staged" || variant == "staged-running-installer" || variant == "staged-installer-file" || variant == "legacy-extra" || strings.HasPrefix(variant, "native-") || strings.HasPrefix(variant, "paired-") {
		release.BuildRef, release.TagObject, expected.BuildRef, packetVersion = "refs/heads/main", "", "refs/heads/main", 2
	}
	native := strings.HasPrefix(variant, "native-")
	if strings.HasPrefix(variant, "paired-") {
		packetVersion = 4
	}
	deb, rpm := []byte("disposable Debian package"), []byte("disposable RPM package")
	if native {
		packetVersion = 3
		expected.DebSHA256, expected.RPMSHA256 = digest(deb), digest(rpm)
	}
	pub, proof, err := fixture.NewWithOptions(releaseprovenance.Policy{Version: releaseprovenance.PolicyVersion, Repository: policy.Repository, RepositoryID: "7", OwnerID: "8", Workflow: releaseprovenance.WorkflowPath}, expected, f.now, fixture.Options{})
	if err != nil {
		t.Fatal(err)
	}
	f.seed.Provenance = &pub
	release.Provenance = update.Artifact{File: provenanceFile, Size: int64(len(proof)), SHA256: digest(proof)}
	packet := AttestedPacket{Version: packetVersion, ProductVersion: "1.2.3", Source: release.WorkflowCommit, BundleSHA256: f.descriptor.BundleSHA256, Archive: archive, Installer: update.Artifact{File: "fased-install-linux-amd64", Size: int64(len(installer)), SHA256: digest(installer)}, Entry: update.Artifact{File: "install.sh", Size: int64(len(entry)), SHA256: digest(entry)}, Release: release}
	if native {
		packet.Deb = update.Artifact{File: "fased-app-linux-amd64.deb", Size: int64(len(deb)), SHA256: digest(deb)}
		packet.RPM = update.Artifact{File: "fased-app-linux-amd64.rpm", Size: int64(len(rpm)), SHA256: digest(rpm)}
		if variant == "native-wrong-subject" {
			rpm = []byte("different RPM after the original provenance")
			packet.RPM.Size, packet.RPM.SHA256 = int64(len(rpm)), digest(rpm)
		}
	}
	payloads := map[string][]byte{archiveTarget: f.archive, provenanceFile: proof, "fased-install-linux-amd64": installer, "install.sh": entry, AttestedPacketFile: jsonBytes(t, packet)}
	if native {
		payloads[packet.Deb.File], payloads[packet.RPM.File] = deb, rpm
	}
	if variant == "legacy-extra" {
		payloads["fased-app-linux-amd64.deb"], payloads["fased-app-linux-amd64.rpm"] = deb, rpm
	}
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
	if native {
		names = append(names, packet.Deb.File)
		if variant != "native-missing" {
			names = append(names, packet.RPM.File)
		}
		if variant == "native-dual-arch" {
			sibling := siblingArch(f.seed.Target.Arch)
			for _, name := range []string{attestedArchiveFile(sibling), attestedInstallerFile(sibling), attestedProvenanceFile(sibling), AttestedPacketAsset(sibling)} {
				payloads[name] = []byte("disposable sibling " + name)
				names = append(names, name)
			}
			// Also strip deb/rpm from names and replace with sibling only? Wait: keep deb+rpm would make 11 assets; replace nothing and test 9 assets separately!
		}
	} else if variant == "legacy-extra" {
		names = append(names, "fased-app-linux-amd64.deb", "fased-app-linux-amd64.rpm")
	} else if strings.HasPrefix(variant, "paired-dual-arch") {
		sibling := siblingArch(f.seed.Target.Arch)
		siblings := []string{attestedArchiveFile(sibling), attestedInstallerFile(sibling), attestedProvenanceFile(sibling), AttestedPacketAsset(sibling)}
		for _, name := range siblings {
			payloads[name] = []byte("disposable sibling " + name)
		}
		switch variant {
		case "paired-dual-arch":
			names = append(names, siblings...)
		case "paired-dual-arch-stray":
			siblings[3] = "stray.bin"
			payloads["stray.bin"] = []byte("unexpected extra asset")
			names = append(names, siblings...)
		case "paired-dual-arch-six":
			payloads["stray.bin"] = []byte("unexpected extra asset")
			names = append(names, "stray.bin")
		case "paired-dual-arch-eight":
			names = append(names, siblings[:3]...)
		case "paired-dual-arch-ten":
			payloads["extra.bin"] = []byte("tenth asset")
			names = append(names, siblings...)
			names = append(names, "extra.bin")
		}
	}
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
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"type": "tag", "sha": tagObject}})
		case "/repos/fased-ai/fased-app/git/tags/" + tagObject:
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
	if _, _, err = p.PackageApplicationRelease(); err == nil {
		t.Fatal("legacy archive was offered as a native package")
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
func TestAttestedStagedBootstrapAndCandidate(t *testing.T) {
	f, o, payloads := attestedFixture(t, "staged")
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
	command.Env = append(command.Env, "FASED_FRESH_CANDIDATE_MISMATCH=1")
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

func TestAttestedCandidateRefusesBelowCompiledTrustFloor(t *testing.T) {
	f, options, payloads := attestedFixture(t, "staged")
	directory := filepath.Join(f.parent, "below-floor-candidate")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for name, raw := range payloads {
		if err := os.WriteFile(filepath.Join(directory, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var seed Seed
	if err := json.Unmarshal(options.Seed, &seed); err != nil {
		t.Fatal(err)
	}
	seed.MinimumSequence = 1000002000005
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	options.Seed = raw
	options.SeedSHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	if _, err = VerifyAttestedCandidate(options, directory); err == nil {
		t.Fatal("candidate below compiled trust floor was admitted")
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

func TestAttestedDualArchClosedSet(t *testing.T) {
	if AttestedPacketAsset("amd64") != "fased-release.json" || AttestedPacketAsset("arm64") != "fased-release-linux-arm64.json" {
		t.Fatal("unexpected packet asset names")
	}
	_, o, payloads := attestedFixture(t, "paired-dual-arch")
	d, identity, err := Inspect(t.Context(), o)
	if err != nil || identity == "" || !d.LinuxBundle || d.ProductVersion != "1.2.3" {
		t.Fatalf("9-asset dual-arch release rejected on Inspect: %v %v", d, err)
	}
	p, err := PrepareNative(t.Context(), o)
	if err != nil {
		t.Fatalf("9-asset dual-arch release rejected on PrepareNative: %v", err)
	}
	defer p.Close()
	if !p.Report().ProvenanceVerified || !p.Report().LinuxBundleVerified {
		t.Fatalf("unexpected report: %+v", p.Report())
	}

	for _, bad := range []string{"paired-dual-arch-stray", "paired-dual-arch-six", "paired-dual-arch-eight", "paired-dual-arch-ten"} {
		t.Run(bad, func(t *testing.T) {
			_, badOpts, _ := attestedFixture(t, bad)
			if _, _, err := Inspect(t.Context(), badOpts); err == nil {
				t.Fatalf("%s was unexpectedly accepted", bad)
			}
			if p, err := PrepareNative(t.Context(), badOpts); err == nil {
				p.Close()
				t.Fatalf("%s was unexpectedly prepared", bad)
			}
		})
	}

	t.Run("v3-refuses-9-assets", func(t *testing.T) {
		f, _, v3Payloads := attestedFixture(t, "native-staged")
		var v3Packet AttestedPacket
		if err := update.StrictJSON(v3Payloads[AttestedPacketFile], &v3Packet); err != nil {
			t.Fatal(err)
		}
		v3Packet.Version = 3
		v3Raw := jsonBytes(t, v3Packet)
		sibling := siblingArch(f.seed.Target.Arch)
		assets := map[string]releaseops.GitHubAsset{
			AttestedPacketFile:           {RepositoryID: f.seed.GitHub.RepositoryID, ReleaseID: "11", AssetID: "1", Name: AttestedPacketFile, Size: int64(len(v3Raw)), SHA256: digest(v3Raw)},
			archiveTarget:                {RepositoryID: f.seed.GitHub.RepositoryID, ReleaseID: "11", AssetID: "2", Name: archiveTarget, Size: v3Packet.Archive.Size, SHA256: v3Packet.Archive.SHA256},
			"fased-install-linux-amd64":  {RepositoryID: f.seed.GitHub.RepositoryID, ReleaseID: "11", AssetID: "3", Name: "fased-install-linux-amd64", Size: v3Packet.Installer.Size, SHA256: v3Packet.Installer.SHA256},
			"install.sh":                 {RepositoryID: f.seed.GitHub.RepositoryID, ReleaseID: "11", AssetID: "4", Name: "install.sh", Size: v3Packet.Entry.Size, SHA256: v3Packet.Entry.SHA256},
			provenanceFile:               {RepositoryID: f.seed.GitHub.RepositoryID, ReleaseID: "11", AssetID: "5", Name: provenanceFile, Size: v3Packet.Release.Provenance.Size, SHA256: v3Packet.Release.Provenance.SHA256},
			v3Packet.Deb.File:            {RepositoryID: f.seed.GitHub.RepositoryID, ReleaseID: "11", AssetID: "6", Name: v3Packet.Deb.File, Size: v3Packet.Deb.Size, SHA256: v3Packet.Deb.SHA256},
			v3Packet.RPM.File:            {RepositoryID: f.seed.GitHub.RepositoryID, ReleaseID: "11", AssetID: "7", Name: v3Packet.RPM.File, Size: v3Packet.RPM.Size, SHA256: v3Packet.RPM.SHA256},
			attestedArchiveFile(sibling): {RepositoryID: f.seed.GitHub.RepositoryID, ReleaseID: "11", AssetID: "8", Name: attestedArchiveFile(sibling), Size: 10, SHA256: strings.Repeat("a", 64)},
			AttestedPacketAsset(sibling): {RepositoryID: f.seed.GitHub.RepositoryID, ReleaseID: "11", AssetID: "9", Name: AttestedPacketAsset(sibling), Size: 10, SHA256: strings.Repeat("b", 64)},
		}
		pub := releaseops.PublishedRelease{ID: "11", Tag: "v" + v3Packet.ProductVersion, Source: v3Packet.Source, Assets: assets}
		if _, err := attestedDescriptor(f.seed, map[string][]byte{AttestedPacketFile: v3Raw, "github-release.json": jsonBytes(t, pub)}); err == nil {
			t.Fatal("packet v3 accepted 9-asset release")
		}
	})
	_ = payloads
}
