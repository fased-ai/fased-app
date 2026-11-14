package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

// CandidateAdmission is a maintainer's bounded acceptance request, not a
// customer update channel or a transferable execution permit. An update binds
// its original baseline; a fresh Local install has no existing baseline and is
// explicitly marked. The compiled trust independently verifies packet/proof.
type CandidateAdmission struct {
	PacketSHA256   string `json:"packetSha256"`
	OperationID    string `json:"operationId"`
	BaselineSHA256 string `json:"baselineSha256"`
	ExpiresAt      int64  `json:"expiresAt"`
	FreshProfile   string `json:"freshProfile,omitempty"`
	FreshOwner     int    `json:"freshOwner,omitempty"`
	FreshRoot      string `json:"freshRoot,omitempty"`
	FreshData      string `json:"freshData,omitempty"`
}

func (a CandidateAdmission) validate(now time.Time) error {
	updateScope := a.FreshProfile == "" && a.FreshOwner == 0 && a.FreshRoot == "" && a.FreshData == "" && hashOK(a.BaselineSHA256)
	freshScope := a.FreshProfile == "local" && a.FreshOwner > 0 && a.BaselineSHA256 == "" && freshCandidatePath(a.FreshRoot) && freshCandidatePath(a.FreshData)
	if freshScope {
		for _, pair := range [][2]string{{a.FreshRoot, a.FreshData}, {a.FreshData, a.FreshRoot}} {
			rel, err := filepath.Rel(pair[0], pair[1])
			if err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				freshScope = false
			}
		}
	}
	scope := updateScope || freshScope
	if !hashOK(a.PacketSHA256) || !scope || !update.IDOK(a.OperationID) || a.ExpiresAt <= now.Unix() || a.ExpiresAt > now.Add(15*time.Minute).Unix() {
		return errors.New("exact candidate, installation scope and at most 15-minute admission required")
	}
	return nil
}

func freshCandidatePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != string(filepath.Separator) && !strings.ContainsAny(path, "\x00\r\n")
}

// PrepareAttestedCandidate admits authenticated CI bytes for one explicit
// fresh Local install or the existing installed controller. It never fetches a
// substitute endpoint, fabricates a release, starts a service or writes state.
func PrepareAttestedCandidate(ctx context.Context, o Options, directory string, admission CandidateAdmission) (p *Prepared, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if os.Geteuid() == 0 || o.Development || len(o.TLSCA) != 0 || o.githubFixture != nil {
		return nil, errors.New("ordinary maintainer and pinned production trust required")
	}
	if err = admission.validate(o.clock()); err != nil {
		return nil, err
	}
	if admission.FreshProfile != "" && (os.Getuid() != admission.FreshOwner || os.Geteuid() != admission.FreshOwner) {
		return nil, errors.New("fresh candidate belongs to another OS owner")
	}
	if _, err = privateDirectory(directory); err != nil || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, errors.New("private absolute candidate directory required")
	}
	if !filepath.IsAbs(o.Directory) || filepath.Clean(o.Directory) != o.Directory {
		return nil, errors.New("exclusive absolute candidate output required")
	}
	// Reuse the shipped preflight, including every artifact, original proof and
	// inner bundle profile. Copying/rechecking below binds the held output too.
	if _, err = VerifyAttestedCandidate(o, directory); err != nil {
		return nil, err
	}
	o.Seed = append([]byte(nil), o.Seed...)
	seed, err := parseSeed(o.Seed, o.SeedSHA256, o.clock())
	if err != nil {
		return nil, err
	}
	raw, err := update.ReadDocument(filepath.Join(directory, AttestedPacketFile))
	if err != nil || digest(raw) != admission.PacketSHA256 {
		return nil, errors.New("candidate differs from the reviewed packet")
	}
	var packet AttestedPacket
	if update.StrictJSON(raw, &packet) != nil || packet.Version != 2 || !attestedBuildIdentity(packet, "") {
		return nil, errors.New("only protected main-dispatch candidates admit installed acceptance")
	}
	sequence, err := releaseSequence(packet.ProductVersion)
	if err != nil || sequence < seed.MinimumSequence {
		return nil, errors.New("candidate below trusted floor")
	}
	if err = checkReaderRelease(sequence); err != nil {
		return nil, err
	}
	d := Descriptor{Binding: seed.Binding, Sequence: sequence, ProductVersion: packet.ProductVersion, Source: packet.Source, BundleSHA256: packet.BundleSHA256, Archive: packet.Archive, Release: packet.Release}
	proof, err := update.ReadDocument(filepath.Join(directory, provenanceFile))
	if err != nil {
		return nil, err
	}
	decision, err := productionProof(seed, d, proof, o.clock())
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(o.Directory)
	before, err := privateDirectory(parent)
	if err != nil {
		return nil, err
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return nil, err
	}
	defer parentRoot.Close()
	if after, e := parentRoot.Stat("."); e != nil || !os.SameFile(before, after) {
		return nil, errors.New("candidate output parent changed")
	}
	base := filepath.Base(o.Directory)
	if err = parentRoot.Mkdir(base, 0700); err != nil {
		return nil, err
	}
	created, err := parentRoot.Lstat(base)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err == nil {
			return
		}
		if p != nil {
			_ = p.Close()
		}
		if current, e := parentRoot.Lstat(base); e == nil && os.SameFile(created, current) {
			err = errors.Join(err, parentRoot.RemoveAll(base))
		}
		p = nil
	}()
	root, err := parentRoot.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	doc, _ := json.Marshal(d)
	p = &Prepared{options: o, seed: seed, descriptor: d, directoryInfo: created, root: root, metadata: map[string][]byte{AttestedPacketFile: raw, "descriptor.json": doc, "provenance.sigstore.json": proof}, startedAt: o.clock(), candidate: &admission}
	sourceRoot, err := os.OpenRoot(directory)
	if err != nil {
		return p, err
	}
	defer sourceRoot.Close()
	in, err := sourceRoot.Open(packet.Archive.File)
	if err != nil {
		return p, err
	}
	defer in.Close()
	out, err := root.OpenFile("download.tar.gz", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return p, err
	}
	n, copyErr := io.Copy(out, io.LimitReader(in, packet.Archive.Size+1))
	if err = errors.Join(copyErr, out.Sync(), out.Close(), ctx.Err()); err != nil {
		return p, err
	}
	if n != packet.Archive.Size {
		return p, errors.New("candidate archive copy incomplete")
	}
	installer, err := sourceRoot.Open(packet.Installer.File)
	if err != nil {
		return p, err
	}
	heldInstaller, err := root.OpenFile("candidate-installer", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		installer.Close()
		return p, err
	}
	h := sha256.New()
	if err = heldInstaller.Chmod(0600); err == nil {
		n, err = io.CopyBuffer(io.MultiWriter(heldInstaller, h), io.LimitReader(installer, packet.Installer.Size+1), make([]byte, 32<<10))
	}
	err = errors.Join(err, installer.Close(), heldInstaller.Sync(), heldInstaller.Close(), ctx.Err())
	if err != nil || n != packet.Installer.Size || hex.EncodeToString(h.Sum(nil)) != packet.Installer.SHA256 {
		return p, errors.New("candidate installer copy differs from the attested pair")
	}
	m, err := bundle.ExtractVerifiedArchive(filepath.Join(o.Directory, "download.tar.gz"), filepath.Join(o.Directory, "bundle"), bundle.File{SHA256: packet.Archive.SHA256, Bytes: packet.Archive.Size}, packet.BundleSHA256)
	if err != nil || m.Version != packet.ProductVersion || m.Source != packet.Source {
		return p, errors.New("candidate held bundle differs from reviewed source")
	}
	if err = verifyBundleProfile(seed, m); err != nil {
		return p, err
	}
	engine, _ := bundle.Names()
	p.engine = m.Files[engine]
	p.report = Report{Version: 4, Purpose: "fased-app-installed-candidate-acceptance:v1", MetadataVerified: true, ArchiveVerified: true, BundleVerified: true, Sequence: sequence, Target: d.Target, SeedSHA256: o.SeedSHA256, DescriptorSHA256: ApplicationDescriptorIdentity(d), ArchiveSHA256: d.Archive.SHA256, BundleSHA256: d.BundleSHA256, BundleDirectory: filepath.Join(o.Directory, "bundle"), ProvenanceVerified: true, ProvenanceSHA256: decision.BundleSHA256, TrustPolicySHA256: decision.PolicySHA256}
	for name, data := range p.metadata {
		if err = writeNew(root, name, data); err != nil {
			return p, err
		}
	}
	if err = writeNew(root, "seed.json", o.Seed); err != nil {
		return p, err
	}
	return p, p.revalidate()
}

func (p *Prepared) revalidateCandidate(seed Seed, now time.Time) error {
	if err := p.candidate.validate(now); err != nil {
		return err
	}
	if digest(p.metadata[AttestedPacketFile]) != p.candidate.PacketSHA256 {
		return errors.New("reviewed candidate identity changed")
	}
	var packet AttestedPacket
	if update.StrictJSON(p.metadata[AttestedPacketFile], &packet) != nil || packet.Version != 2 || !attestedBuildIdentity(packet, "") {
		return errors.New("candidate packet changed")
	}
	d := Descriptor{Binding: seed.Binding, Sequence: p.descriptor.Sequence, ProductVersion: packet.ProductVersion, Source: packet.Source, BundleSHA256: packet.BundleSHA256, Archive: packet.Archive, Release: packet.Release}
	if d != p.descriptor {
		return errors.New("candidate descriptor changed")
	}
	installer, err := bundle.Inspect(filepath.Join(p.options.Directory, "candidate-installer"))
	if err != nil || installer.SHA256 != packet.Installer.SHA256 || installer.Bytes != packet.Installer.Size {
		return errors.New("candidate installer changed after verification")
	}
	_, err = productionProof(seed, d, p.metadata["provenance.sigstore.json"], now)
	return err
}

func (p *Prepared) candidateInstallation(baseline, root, data string, completed localinstall.ApplicationUpdateStatus) error {
	if p.candidate == nil {
		return nil
	}
	if p.candidate.FreshProfile != "" {
		return errors.New("fresh candidate admission cannot authorize an existing installation update")
	}
	exactOperation := completed.ID == p.candidate.OperationID && completed.PreviousManifestSHA256 == p.candidate.BaselineSHA256 && completed.Sequence == p.report.Sequence && p.matchesApplicationIdentity(completed.ReleaseSHA256)
	if completed.ID == p.candidate.OperationID && completed.Phase == "rolled-back" {
		return errors.New("rolled-back candidate remains consumed; inspect the original recovery")
	}
	if completed.ID == p.candidate.OperationID && !exactOperation || completed.ID != "" && completed.ID != p.candidate.OperationID && !oneTerminalPhase(completed.Phase) {
		return errors.New("candidate admission differs from the retained operation")
	}
	exactTarget := exactOperation && completed.TargetManifestSHA256 == baseline && (completed.Phase == "completed" || !oneTerminalPhase(completed.Phase))
	if baseline != p.candidate.BaselineSHA256 && !exactTarget {
		return errors.New("candidate admission belongs to another installed baseline")
	}
	for _, path := range []string{root, data} {
		for _, pair := range [][2]string{{path, p.options.Directory}, {p.options.Directory, path}} {
			rel, err := filepath.Rel(pair[0], pair[1])
			if err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return errors.New("candidate acquisition must be separate from installed state")
			}
		}
	}
	return nil
}

// CheckCandidateInstallation is a side-effect-free preflight before the
// maintainer prepares an old-reader session. Controllers repeat the check from
// their own protected manifest/journal; caller-supplied metadata grants nothing.
func (p *Prepared) CheckCandidateInstallation(baseline, root, data string, completed localinstall.ApplicationUpdateStatus) error {
	if p == nil {
		return errors.New("held candidate required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.candidate == nil {
		return errors.New("held candidate admission required")
	}
	if err := p.revalidate(); err != nil {
		return err
	}
	return p.candidateInstallation(baseline, root, data, completed)
}
