package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/update"
)

const AttestedPacketFile = "fased-release.json"

// AttestedPacketAsset returns the release asset name for the attested packet
// matching arch ("fased-release-linux-arm64.json" for arm64, "fased-release.json"
// for amd64 so single-arch 0.1.x paths remain exact).
func AttestedPacketAsset(arch string) string {
	if arch == "arm64" {
		return "fased-release-linux-arm64.json"
	}
	return AttestedPacketFile
}

func attestedArchiveFile(arch string) string   { return "fased-app-linux-" + arch + ".tar.gz" }
func attestedInstallerFile(arch string) string { return "fased-install-linux-" + arch }
func attestedProvenanceFile(arch string) string {
	return "fased-app-linux-" + arch + ".provenance.sigstore.json"
}
func attestedDebFile(arch string) string { return "fased-app-linux-" + arch + ".deb" }
func attestedRPMFile(arch string) string { return "fased-app-linux-" + arch + ".rpm" }
func siblingArch(arch string) string {
	if arch == "arm64" {
		return "amd64"
	}
	return "arm64"
}

// AttestedPacket is generated from the one tag-bound build. GitHub's immutable
// release binds this inventory; the offline Sigstore proof authenticates the
// archive's repository, workflow, tag, commit and exact bytes independently.
type AttestedPacket struct {
	Version        int               `json:"version"`
	ProductVersion string            `json:"productVersion"`
	Source         string            `json:"source"`
	BundleSHA256   string            `json:"bundleSha256"`
	Archive        update.Artifact   `json:"archive"`
	Installer      update.Artifact   `json:"installer"`
	Entry          update.Artifact   `json:"entry"`
	Deb            update.Artifact   `json:"deb,omitzero"`
	RPM            update.Artifact   `json:"rpm,omitzero"`
	Release        ProductionRelease `json:"release"`
}

// Main-dispatched candidates are tagged only after installed acceptance. The
// immutable published tag still must resolve to packet.Source in LatestPublished.
func attestedBuildIdentity(packet AttestedPacket, tagObject string) bool {
	if packet.Version == 1 {
		return packet.Release.BuildRef == "" && sourceHash(packet.Release.TagObject) && packet.Release.TagObject == tagObject
	}
	return (packet.Version == 2 || packet.Version == 3 || packet.Version == 4) && packet.Release.BuildRef == "refs/heads/main" && packet.Release.TagObject == ""
}

func attestedDescriptor(seed Seed, docs map[string][]byte) (Descriptor, error) {
	var packet AttestedPacket
	var release releaseops.PublishedRelease
	var d Descriptor
	if update.StrictJSON(docs[AttestedPacketFile], &packet) != nil || update.StrictJSON(docs["github-release.json"], &release) != nil {
		return d, errors.New("attested release packet invalid")
	}
	sequence, err := releaseSequence(packet.ProductVersion)
	if _, e := strconv.ParseUint(release.ID, 10, 64); e != nil || release.ID == "0" {
		return d, errors.New("release ID invalid")
	}
	if packet.Release.InstallerSHA256 != packet.Installer.SHA256 || !hashOK(packet.Release.InstallerSHA256) {
		return d, errors.New("attested installer digest required")
	}
	if err != nil || sequence < seed.MinimumSequence || (packet.Version != 1 && packet.Version != 2 && packet.Version != 3 && packet.Version != 4) || release.Tag != "v"+packet.ProductVersion || packet.Source != release.Source || !attestedBuildIdentity(packet, release.TagObject) || !sourceHash(packet.Source) || !sourceHash(packet.Release.SourceTree) || !hashOK(packet.Release.PackageLockSHA256) || !hashOK(packet.Release.GoSumSHA256) || !hashOK(packet.BundleSHA256) {
		return d, errors.New("attested source/tag/sequence mismatch")
	}
	arch := seed.Target.Arch
	packetAsset := AttestedPacketAsset(arch)
	archiveFile := attestedArchiveFile(arch)
	installerFile := attestedInstallerFile(arch)
	provFile := attestedProvenanceFile(arch)
	expected := map[string]update.Artifact{archiveFile: packet.Archive, installerFile: packet.Installer, "install.sh": packet.Entry, provFile: packet.Release.Provenance}
	if packet.Version == 3 {
		if packet.Deb.File != attestedDebFile(arch) || packet.RPM.File != attestedRPMFile(arch) || !hashOK(packet.Deb.SHA256) || !hashOK(packet.RPM.SHA256) || packet.Deb.Size <= 0 || packet.Deb.Size > maxArchiveBytes || packet.RPM.Size <= 0 || packet.RPM.Size > maxArchiveBytes {
			return d, errors.New("both bounded native package identities are required")
		}
		expected[packet.Deb.File], expected[packet.RPM.File] = packet.Deb, packet.RPM
	} else if packet.Deb != (update.Artifact{}) || packet.RPM != (update.Artifact{}) {
		return d, errors.New("legacy release cannot authorize native packages")
	}
	singleArch := len(release.Assets) == len(expected)+1
	sibling := siblingArch(arch)
	dualArchAssets := []string{attestedArchiveFile(sibling), attestedInstallerFile(sibling), attestedProvenanceFile(sibling), AttestedPacketAsset(sibling)}
	dualArch := packet.Version == 4 && len(release.Assets) == len(expected)+1+len(dualArchAssets)
	if !singleArch && !dualArch {
		return d, errors.New("attested release inventory incomplete")
	}
	for name, identity := range expected {
		a, ok := release.Assets[name]
		if !ok || identity.File != name || identity.Size <= 0 || identity.SHA256 != a.SHA256 || identity.Size != a.Size || a.RepositoryID != seed.GitHub.RepositoryID || a.ReleaseID != release.ID {
			return d, errors.New("attested inventory differs from immutable release")
		}
	}
	if dualArch {
		for _, name := range dualArchAssets {
			a, ok := release.Assets[name]
			if !ok || a.Name != name || a.Size <= 0 || a.RepositoryID != seed.GitHub.RepositoryID || a.ReleaseID != release.ID {
				return d, errors.New("attested dual-arch inventory incomplete")
			}
		}
	}
	p, ok := release.Assets[packetAsset]
	if !ok || p.SHA256 != digest(docs[AttestedPacketFile]) || p.Size != int64(len(docs[AttestedPacketFile])) || p.RepositoryID != seed.GitHub.RepositoryID || p.ReleaseID != release.ID {
		return d, errors.New("attested packet differs from immutable digest")
	}
	d = Descriptor{Binding: seed.Binding, Sequence: sequence, ProductVersion: packet.ProductVersion, Source: packet.Source, BundleSHA256: packet.BundleSHA256, Archive: packet.Archive, Deb: packet.Deb, RPM: packet.RPM, LinuxBundle: packet.Version == 4, GitHub: release.Assets[archiveFile], Release: packet.Release}
	if d.provenanceExpected().Validate() != nil {
		return d, errors.New("attested build identity invalid")
	}
	return d, nil
}

// Inspection may authenticate an older public head while an accepted staged
// installation is ahead of it. Executable preparation and every later use
// still refuse crossing below the reader's version.
func checkReaderRelease(sequence uint64) error {
	if current, err := releaseSequence(core.Version); err == nil && sequence < current {
		return errors.New("release is older than this verified reader")
	}
	return nil
}

func attestedInputs(ctx context.Context, g *releaseops.GitHubTransport, seed Seed) (Descriptor, map[string][]byte, error) {
	var d Descriptor
	docs := map[string][]byte{}
	head, err := g.LatestPublished(ctx, seed.Provenance.OwnerID)
	if err != nil {
		return d, docs, err
	}
	packetAsset := AttestedPacketAsset(seed.Target.Arch)
	provFile := attestedProvenanceFile(seed.Target.Arch)
	for _, item := range []struct{ asset, key string }{
		{packetAsset, AttestedPacketFile},
		{provFile, "provenance.sigstore.json"},
	} {
		asset, ok := head.Assets[item.asset]
		if !ok || asset.Size > update.MaxDocument {
			return d, docs, errors.New("bounded attested metadata asset required")
		}
		docs[item.key], err = g.PublishedAsset(ctx, asset)
		if err != nil {
			return d, docs, err
		}
	}
	docs["github-release.json"], err = json.Marshal(head)
	if err != nil {
		return d, docs, err
	}
	d, err = attestedDescriptor(seed, docs)
	if err != nil {
		return d, docs, err
	}
	docs["descriptor.json"], err = json.Marshal(d)
	return d, docs, err
}

func revalidateAttested(seed Seed, docs map[string][]byte, d Descriptor, now time.Time) error {
	current, err := attestedDescriptor(seed, docs)
	if err != nil || current != d {
		return errors.New("attested release identity changed")
	}
	_, err = productionProof(seed, d, docs["provenance.sigstore.json"], now)
	return err
}

// Public package preparation retains the same bytes a protected receiver must
// independently authenticate. This is not permission to run the old archive
// installer or to execute a package.
func (p *Prepared) revalidatePublicPackages() error {
	var packet AttestedPacket
	if update.StrictJSON(p.metadata[AttestedPacketFile], &packet) != nil || (packet.Version != 3 && packet.Version != 4) ||
		digest(p.metadata[AttestedPacketFile]) != p.report.DescriptorSHA256 ||
		packet.Deb != p.descriptor.Deb || packet.RPM != p.descriptor.RPM {
		return errors.New("public native package identity changed")
	}
	items := []struct {
		artifact update.Artifact
		held     string
	}{{packet.Installer, "candidate-installer"}, {packet.Entry, "candidate-entry"}}
	if packet.Version == 3 {
		items = append(items, struct {
			artifact update.Artifact
			held     string
		}{packet.Deb, "candidate-deb"}, struct {
			artifact update.Artifact
			held     string
		}{packet.RPM, "candidate-rpm"})
	}
	for _, item := range items {
		got, err := bundle.Inspect(filepath.Join(p.options.Directory, item.held))
		if err != nil || got.SHA256 != item.artifact.SHA256 || got.Bytes != item.artifact.Size {
			return errors.New("public native release asset changed after verification")
		}
	}
	return nil
}

// Sequence is derived from canonical semantic version, never from GitHub's
// opaque numeric IDs. Three bounded base-1e6 components fit signed 64-bit state.
func releaseSequence(version string) (uint64, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return 0, errors.New("stable version required")
	}
	var sequence uint64
	for _, part := range parts {
		value, err := strconv.ParseUint(part, 10, 20)
		if err != nil || value >= 1000000 || strconv.FormatUint(value, 10) != part {
			return 0, errors.New("canonical bounded version required")
		}
		sequence = sequence*1000000 + value
	}
	return sequence + 1, nil
}
