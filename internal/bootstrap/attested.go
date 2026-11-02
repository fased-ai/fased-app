package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/update"
)

const AttestedPacketFile = "fased-release.json"

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
	Release        ProductionRelease `json:"release"`
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
	if current, e := releaseSequence(core.Version); e == nil && sequence < current {
		return d, errors.New("release is older than this verified reader")
	}
	if packet.Release.InstallerSHA256 != packet.Installer.SHA256 || !hashOK(packet.Release.InstallerSHA256) {
		return d, errors.New("attested installer digest required")
	}
	if err != nil || sequence < seed.MinimumSequence || packet.Version != 1 || release.Tag != "v"+packet.ProductVersion || packet.Source != release.Source || packet.Release.TagObject != release.TagObject || !sourceHash(packet.Source) || !sourceHash(packet.Release.SourceTree) || !hashOK(packet.Release.PackageLockSHA256) || !hashOK(packet.Release.GoSumSHA256) || !hashOK(packet.BundleSHA256) {
		return d, errors.New("attested source/tag/sequence mismatch")
	}
	expected := map[string]update.Artifact{archiveTarget: packet.Archive, "fased-install-linux-amd64": packet.Installer, "install.sh": packet.Entry, provenanceFile: packet.Release.Provenance}
	if len(release.Assets) != 5 {
		return d, errors.New("attested release inventory incomplete")
	}
	for name, identity := range expected {
		a, ok := release.Assets[name]
		if !ok || identity.File != name || identity.Size <= 0 || identity.SHA256 != a.SHA256 || identity.Size != a.Size || a.RepositoryID != seed.GitHub.RepositoryID || a.ReleaseID != release.ID {
			return d, errors.New("attested inventory differs from immutable release")
		}
	}
	p, ok := release.Assets[AttestedPacketFile]
	if !ok || p.SHA256 != digest(docs[AttestedPacketFile]) || p.Size != int64(len(docs[AttestedPacketFile])) {
		return d, errors.New("attested packet differs from immutable digest")
	}
	d = Descriptor{Binding: seed.Binding, Sequence: sequence, ProductVersion: packet.ProductVersion, Source: packet.Source, BundleSHA256: packet.BundleSHA256, Archive: packet.Archive, GitHub: release.Assets[archiveTarget], Release: packet.Release}
	if d.provenanceExpected().Validate() != nil {
		return d, errors.New("attested build identity invalid")
	}
	return d, nil
}

func attestedInputs(ctx context.Context, g *releaseops.GitHubTransport, seed Seed) (Descriptor, map[string][]byte, error) {
	var d Descriptor
	docs := map[string][]byte{}
	head, err := g.LatestPublished(ctx, seed.Provenance.OwnerID)
	if err != nil {
		return d, docs, err
	}
	for _, name := range []string{AttestedPacketFile, provenanceFile} {
		asset, ok := head.Assets[name]
		if !ok || asset.Size > update.MaxDocument {
			return d, docs, errors.New("bounded attested metadata asset required")
		}
		key := name
		if name == provenanceFile {
			key = "provenance.sigstore.json"
		}
		docs[key], err = g.PublishedAsset(ctx, asset)
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
