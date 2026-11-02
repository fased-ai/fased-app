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
	seed, err := parseSeed(o.Seed, o.SeedSHA256, o.clock())
	if err != nil || seed.Version != 4 {
		return report, errors.New("compiled attested release trust required")
	}
	raw, err := update.ReadDocument(filepath.Join(directory, AttestedPacketFile))
	if err != nil {
		return report, err
	}
	var packet AttestedPacket
	if update.StrictJSON(raw, &packet) != nil || packet.Version != 1 || !hashOK(packet.BundleSHA256) || !sourceHash(packet.Release.SourceTree) || !hashOK(packet.Release.PackageLockSHA256) || !hashOK(packet.Release.GoSumSHA256) {
		return report, errors.New("candidate inventory invalid")
	}
	if packet.Release.InstallerSHA256 != packet.Installer.SHA256 || !hashOK(packet.Release.InstallerSHA256) {
		return report, errors.New("attested candidate installer digest required")
	}
	d := Descriptor{Binding: seed.Binding, ProductVersion: packet.ProductVersion, Source: packet.Source, BundleSHA256: packet.BundleSHA256, Archive: packet.Archive, Release: packet.Release}
	for _, a := range []update.Artifact{packet.Archive, packet.Installer, packet.Entry, packet.Release.Provenance} {
		if filepath.Base(a.File) != a.File || a.Size <= 0 || a.Size > maxArchiveBytes || !hashOK(a.SHA256) {
			return report, errors.New("candidate artifact bound invalid")
		}
		identity, e := bundle.Inspect(filepath.Join(directory, a.File))
		if e != nil || identity.SHA256 != a.SHA256 || identity.Bytes != a.Size {
			return report, errors.New("candidate artifact changed")
		}
	}
	if packet.Archive.File != archiveTarget || packet.Installer.File != "fased-install-linux-amd64" || packet.Entry.File != "install.sh" || packet.Release.Provenance.File != provenanceFile {
		return report, errors.New("candidate target inventory invalid")
	}
	proof, err := update.ReadDocument(filepath.Join(directory, provenanceFile))
	if err != nil {
		return report, err
	}
	decision, err := productionProof(seed, d, proof, o.clock())
	if err != nil {
		return report, err
	}
	scratch, err := os.MkdirTemp(directory, ".candidate-check-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(scratch)
	m, err := bundle.ExtractVerifiedArchive(filepath.Join(directory, archiveTarget), filepath.Join(scratch, "bundle"), bundle.File{SHA256: packet.Archive.SHA256, Bytes: packet.Archive.Size}, packet.BundleSHA256)
	if err != nil {
		return report, err
	}
	if m.Version != packet.ProductVersion || m.Source != packet.Source {
		return report, errors.New("candidate inner source/version differs from provenance")
	}
	if err = verifyBundleProfile(seed, m); err != nil {
		return report, err
	}
	return Report{Version: 4, Purpose: AttestedPurpose, ArchiveVerified: true, BundleVerified: true, ProvenanceVerified: true, SeedSHA256: o.SeedSHA256, ArchiveSHA256: packet.Archive.SHA256, BundleSHA256: packet.BundleSHA256, ProvenanceSHA256: decision.BundleSHA256, TrustPolicySHA256: decision.PolicySHA256}, nil
}
