package bootstrap

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/update"
)

// VerifyAttestedCandidate is read-only publisher preflight through the shipped
// installer. It cannot install, upgrade or return an executable capability.
func VerifyAttestedCandidate(o Options, directory string) (Report, error) {
	var report Report
	if os.Geteuid() == 0 || !filepath.IsAbs(directory) {
		return report, errors.New("ordinary-owner absolute candidate directory required")
	}
	return verifyAttestedCandidate(o, directory)
}

// verifyAttestedCandidate is also used by the fixed installed maintenance
// helper after its caller and protected input directory have been validated.
func verifyAttestedCandidate(o Options, directory string) (Report, error) {
	var report Report
	seed, err := parseSeed(o.Seed, o.SeedSHA256, o.clock())
	if err != nil || seed.Version != 4 {
		return report, errors.New("compiled attested release trust required")
	}
	arch := seed.Target.Arch
	packetAsset := AttestedPacketAsset(arch)
	archiveFile := attestedArchiveFile(arch)
	installerFile := attestedInstallerFile(arch)
	provFile := attestedProvenanceFile(arch)
	raw, err := update.ReadDocument(filepath.Join(directory, packetAsset))
	if err != nil {
		return report, err
	}
	var packet AttestedPacket
	if update.StrictJSON(raw, &packet) != nil || !attestedBuildIdentity(packet, packet.Release.TagObject) || !hashOK(packet.BundleSHA256) || !sourceHash(packet.Release.SourceTree) || !hashOK(packet.Release.PackageLockSHA256) || !hashOK(packet.Release.GoSumSHA256) {
		return report, errors.New("candidate inventory invalid")
	}
	sequence, sequenceErr := releaseSequence(packet.ProductVersion)
	if sequenceErr != nil || sequence < seed.MinimumSequence {
		return report, errors.New("candidate version is below the trusted minimum")
	}
	if packet.Release.InstallerSHA256 != packet.Installer.SHA256 || !hashOK(packet.Release.InstallerSHA256) {
		return report, errors.New("attested candidate installer digest required")
	}
	d := Descriptor{Binding: seed.Binding, ProductVersion: packet.ProductVersion, Source: packet.Source, BundleSHA256: packet.BundleSHA256, Archive: packet.Archive, Release: packet.Release}
	artifacts := []update.Artifact{packet.Archive, packet.Installer, packet.Entry, packet.Release.Provenance}
	expected := d.provenanceExpected()
	if packet.Version == 3 {
		if packet.Deb.File != attestedDebFile(arch) || packet.RPM.File != attestedRPMFile(arch) {
			return report, errors.New("both fixed Linux package targets are required")
		}
		artifacts = append(artifacts, packet.Deb, packet.RPM)
		expected.DebSHA256, expected.RPMSHA256 = packet.Deb.SHA256, packet.RPM.SHA256
	} else if packet.Deb != (update.Artifact{}) || packet.RPM != (update.Artifact{}) {
		return report, errors.New("legacy candidate cannot carry unverified Linux packages")
	}
	for _, a := range artifacts {
		if filepath.Base(a.File) != a.File || a.Size <= 0 || a.Size > maxArchiveBytes || !hashOK(a.SHA256) {
			return report, errors.New("candidate artifact bound invalid")
		}
		identity, e := bundle.Inspect(filepath.Join(directory, a.File))
		if e != nil || identity.SHA256 != a.SHA256 || identity.Bytes != a.Size {
			return report, errors.New("candidate artifact changed")
		}
	}
	if packet.Archive.File != archiveFile || packet.Installer.File != installerFile || packet.Entry.File != "install.sh" || packet.Release.Provenance.File != provFile {
		return report, errors.New("candidate target inventory invalid")
	}
	proof, err := update.ReadDocument(filepath.Join(directory, provFile))
	if err != nil {
		return report, err
	}
	decision, err := productionProofExpected(seed, d, proof, o.clock(), expected)
	if err != nil {
		return report, err
	}
	scratch, err := os.MkdirTemp(directory, ".candidate-check-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(scratch)
	m, err := bundle.ExtractVerifiedArchive(filepath.Join(directory, archiveFile), filepath.Join(scratch, "bundle"), bundle.File{SHA256: packet.Archive.SHA256, Bytes: packet.Archive.Size}, packet.BundleSHA256)
	if err != nil {
		return report, err
	}
	if m.Version != packet.ProductVersion || m.Source != packet.Source {
		return report, errors.New("candidate inner source/version differs from provenance")
	}
	if err = verifyAttestedBundleProfile(seed, m, packet.Version); err != nil {
		return report, err
	}
	return Report{Version: 4, Purpose: AttestedPurpose, ArchiveVerified: true, BundleVerified: true, ProvenanceVerified: true, LinuxPackageDigestsVerified: packet.Version == 3, LinuxBundleVerified: packet.Version == 4, SeedSHA256: o.SeedSHA256, ArchiveSHA256: packet.Archive.SHA256, BundleSHA256: packet.BundleSHA256, ProvenanceSHA256: decision.BundleSHA256, TrustPolicySHA256: decision.PolicySHA256}, nil
}
