package bootstrap

import (
	"encoding/hex"
	"errors"
	"runtime"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

const productionDescriptorTarget = "ubuntu-first-install/descriptor.json"

var productionArchiveTarget = "ubuntu-first-install/" + archiveTarget

const productionProvenanceTarget = "ubuntu-first-install/provenance.sigstore.json"

var provenanceFile = "fased-app-linux-" + runtime.GOARCH + ".provenance.sigstore.json"

// ProductionRelease is comparable so final descriptor revalidation cannot drop
// any approved identity. These fields grant no signer/update/privileged helper
// authority. Tree/tag-object/lock hashes are TUF-approved assertions, not claims
// invented by the standard GitHub Actions provenance format.
type ProductionRelease struct {
	SourceTree        string          `json:"sourceTree"`
	TagObject         string          `json:"tagObject"`
	PackageLockSHA256 string          `json:"packageLockSha256"`
	GoSumSHA256       string          `json:"goSumSha256"`
	WorkflowCommit    string          `json:"workflowCommit"`
	RunID             string          `json:"runId"`
	RunAttempt        string          `json:"runAttempt"`
	Provenance        update.Artifact `json:"provenance"`
}

func targetNames(seed Seed) []string {
	if seed.Version == 3 {
		return []string{productionDescriptorTarget, productionArchiveTarget, productionProvenanceTarget}
	}
	return []string{descriptorTarget, archiveTarget}
}

func descriptorPath(seed Seed, hash []byte) string {
	prefix := "targets/bootstrap/"
	if seed.Version == 3 {
		prefix = "targets/production-bootstrap/"
	}
	return prefix + hex.EncodeToString(hash) + ".descriptor.json"
}

func sourceHash(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 20 && hex.EncodeToString(b) == s
}

func (d Descriptor) provenanceExpected() releaseprovenance.Expected {
	return releaseprovenance.Expected{ArchiveName: d.Archive.File, ArchiveSHA256: d.Archive.SHA256, SourceCommit: d.Source, WorkflowCommit: d.Release.WorkflowCommit, Tag: "v" + d.ProductVersion, RunID: d.Release.RunID, RunAttempt: d.Release.RunAttempt}
}

func validateProductionDescriptor(d Descriptor, target *metadata.TargetFiles) error {
	p := d.Release.Provenance
	if d.provenanceExpected().Validate() != nil || !sourceHash(d.Release.SourceTree) || !sourceHash(d.Release.TagObject) || !hashOK(d.Release.PackageLockSHA256) || !hashOK(d.Release.GoSumSHA256) || p.File != provenanceFile || target == nil || p.Size != target.Length || p.Size <= 0 || p.Size > releaseprovenance.MaxBundleBytes || p.SHA256 != hex.EncodeToString(target.Hashes["sha256"]) {
		return errors.New("production release/provenance identity differs from authenticated targets")
	}
	return nil
}

func productionProof(seed Seed, d Descriptor, proof []byte, now time.Time) (releaseprovenance.Decision, error) {
	if seed.Version != 3 {
		return releaseprovenance.Decision{}, nil
	}
	if seed.Provenance == nil || int64(len(proof)) != d.Release.Provenance.Size || digest(proof) != d.Release.Provenance.SHA256 {
		return releaseprovenance.Decision{}, errors.New("production provenance bytes differ from authenticated target")
	}
	return releaseprovenance.Verify(*seed.Provenance, proof, d.provenanceExpected(), now)
}

func verifyBundleProfile(seed Seed, m bundle.Manifest) error {
	if seed.Version == 3 {
		if m.Schema != 2 || m.UnsignedEngineering || m.Desktop {
			return errors.New("production first install requires the distinct headless Ubuntu release-bundle format")
		}
	} else if m.Schema != 1 || !m.UnsignedEngineering {
		return errors.New("development bootstrap cannot acquire release-bundle authority")
	}
	return nil
}
