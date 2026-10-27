// Package bootstrap independently verifies first-install bundles. Production
// admission is implemented but ordinary dispatch remains disabled.
package bootstrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/releaseprovenance"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/trustedmetadata"
)

const Purpose = "fased-app-first-install-development:v1"
const GitHubPurpose = "fased-app-first-install-github-controlled:v1"
const ProductionPurpose = "fased-app-first-install-production:v1"
const descriptorTarget = "bootstrap/descriptor.json"
const archiveTarget = "fased-app-linux-amd64.tar.gz"
const maxArchiveBytes = 128 << 20

type Binding struct {
	Version         int           `json:"version"`
	Purpose         string        `json:"purpose"`
	DevelopmentOnly bool          `json:"developmentOnly"`
	Repository      string        `json:"repository"`
	Channel         string        `json:"channel"`
	Target          update.Target `json:"target"`
}

func (b Binding) valid() bool {
	profile := b.Version == 1 && b.Purpose == Purpose || b.Version == 2 && b.Purpose == GitHubPurpose
	development := profile && b.DevelopmentOnly && b.Channel == "development"
	production := b.Version == 3 && b.Purpose == ProductionPurpose && !b.DevelopmentOnly && b.Channel == "stable"
	return (development || production) && b.Repository == "fased-ai/fased-app" && b.Target == (update.Target{OS: "linux", Arch: "amd64"})
}

type Seed struct {
	Binding
	Origin          string                    `json:"origin"`
	Root            []byte                    `json:"root"`
	CreatedAt       int64                     `json:"createdAt"`
	ExpiresAt       int64                     `json:"expiresAt"`
	MinimumSequence uint64                    `json:"minimumSequence,string"`
	GitHub          releaseops.GitHubPolicy   `json:"github,omitzero"`
	Provenance      *releaseprovenance.Policy `json:"provenance,omitempty"`
}

type Descriptor struct {
	Binding
	Sequence       uint64                 `json:"sequence,string"`
	ProductVersion string                 `json:"productVersion"`
	Source         string                 `json:"source"`
	BundleSHA256   string                 `json:"bundleSha256"`
	Archive        update.Artifact        `json:"archive"`
	GitHub         releaseops.GitHubAsset `json:"github,omitzero"`
	Release        ProductionRelease      `json:"release,omitzero"`
}

func digest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func hashOK(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == s
}

func metadataShape(b []byte, kind string) error {
	var envelope struct {
		Signed     json.RawMessage `json:"signed"`
		Signatures []struct {
			KeyID string `json:"keyid"`
			Sig   string `json:"sig"`
		} `json:"signatures"`
	}
	if update.StrictJSON(b, &envelope) != nil || len(envelope.Signatures) < 1 || len(envelope.Signatures) > 8 {
		return errors.New("invalid bounded TUF metadata envelope")
	}
	seen := map[string]bool{}
	for _, sig := range envelope.Signatures {
		decoded, err := hex.DecodeString(sig.Sig)
		if !hashOK(sig.KeyID) || err != nil || len(decoded) != 64 || seen[sig.KeyID] {
			return errors.New("duplicate or invalid TUF signature identity")
		}
		seen[sig.KeyID] = true
	}
	var body map[string]json.RawMessage
	if update.StrictJSON(envelope.Signed, &body) != nil {
		return errors.New("invalid TUF signed body")
	}
	allowed := map[string]bool{"_type": true, "spec_version": true, "version": true, "expires": true}
	switch kind {
	case "root":
		allowed["keys"], allowed["roles"], allowed["consistent_snapshot"] = true, true, true
	case "targets":
		allowed["targets"] = true // Delegations are not an initial-install bypass.
	default:
		allowed["meta"] = true
	}
	for name := range body {
		if !allowed[name] {
			return errors.New("unsupported TUF metadata field")
		}
	}
	var actual string
	var spec string
	var version int64
	if json.Unmarshal(body["_type"], &actual) != nil || actual != kind || json.Unmarshal(body["spec_version"], &spec) != nil || spec != metadata.SPECIFICATION_VERSION || json.Unmarshal(body["version"], &version) != nil || version < 1 || version > 1<<31-1 {
		return errors.New("wrong TUF role")
	}
	return nil
}

func parseSeed(b []byte, pin string, now time.Time) (Seed, error) {
	var seed Seed
	if !hashOK(pin) || digest(b) != pin || update.StrictJSON(b, &seed) != nil || !seed.Binding.valid() || seed.CreatedAt <= 0 || seed.CreatedAt > now.Unix() || seed.ExpiresAt <= now.Unix() || seed.ExpiresAt-seed.CreatedAt > 365*86400 || seed.MinimumSequence == 0 || len(seed.Root) > update.MaxDocument {
		return seed, errors.New("current independently pinned first-install seed required")
	}
	if seed.Version == 3 {
		if seed.Provenance == nil || seed.Provenance.Validate() != nil || seed.Provenance.Repository != seed.Repository || seed.Provenance.RepositoryID != seed.GitHub.RepositoryID {
			return seed, errors.New("production seed requires pinned matching provenance trust and repository identity")
		}
	} else if seed.Provenance != nil {
		return seed, errors.New("development seed cannot acquire production provenance authority")
	}
	if seed.Version == 1 {
		u, err := update.DevelopmentAcquisitionURL(seed.Origin)
		if err != nil || u.Path != "" || seed.GitHub != (releaseops.GitHubPolicy{}) {
			return seed, errors.New("version1 seed must lock an exact development HTTPS origin only")
		}
	} else if seed.Origin != "" || seed.GitHub.Validate() != nil {
		return seed, errors.New("GitHub seed requires one explicit pinned policy and no legacy origin")
	}
	if err := metadataShape(seed.Root, "root"); err != nil {
		return seed, err
	}
	root, err := metadata.Root().FromBytes(seed.Root)
	if err != nil || !root.Signed.ConsistentSnapshot || len(root.Signed.Roles) != 4 || len(root.Signed.Keys) != 8 || !root.Signed.Expires.After(now) || root.Signed.Expires.After(time.Unix(seed.CreatedAt, 0).Add(365*24*time.Hour)) || seed.ExpiresAt > root.Signed.Expires.Unix() {
		return seed, errors.New("invalid current first-install TUF root")
	}
	ids, pubs := map[string]bool{}, map[string]bool{}
	for role, count := range map[string]int{"root": 3, "targets": 3, "snapshot": 1, "timestamp": 1} {
		r := root.Signed.Roles[role]
		threshold := 1
		if count == 3 {
			threshold = 2
		}
		if r == nil || r.Threshold != threshold || len(r.KeyIDs) != count {
			return seed, errors.New("root/targets require two of three separate keys; online roles require their own key")
		}
		for _, id := range r.KeyIDs {
			key := root.Signed.Keys[id]
			if key == nil || key.Type != "ed25519" || key.Scheme != "ed25519" || ids[id] {
				return seed, errors.New("TUF role keys must be distinct Ed25519 identities")
			}
			pub, err := hex.DecodeString(key.Value.PublicKey)
			canonical := hex.EncodeToString(pub)
			if err != nil || len(pub) != 32 || canonical != key.Value.PublicKey || pubs[canonical] {
				return seed, errors.New("invalid TUF public key")
			}
			ids[id], pubs[canonical] = true, true
		}
	}
	if err = root.VerifyDelegate("root", root); err != nil {
		return seed, err
	}
	if _, err = trustedmetadata.New(seed.Root); err != nil {
		return seed, err
	}
	return seed, nil
}

func initialChain(seed Seed, timestamp, snapshot, targets []byte, now time.Time) (*trustedmetadata.TrustedMetadata, error) {
	chain, err := trustedmetadata.New(seed.Root)
	if err != nil {
		return nil, err
	}
	chain.RefTime = now.UTC()
	if err = metadataShape(timestamp, "timestamp"); err != nil {
		return nil, err
	}
	if _, err = chain.UpdateTimestamp(timestamp); err != nil {
		return nil, err
	}
	if len(chain.Timestamp.Signed.Meta) != 1 || !metaBound(chain.Timestamp.Signed.Meta["snapshot.json"]) || !chain.Timestamp.Signed.Expires.After(now) || chain.Timestamp.Signed.Expires.After(now.Add(48*time.Hour)) {
		return nil, errors.New("unsupported timestamp or freshness window")
	}
	if snapshot == nil {
		return chain, nil
	}
	if err = metadataShape(snapshot, "snapshot"); err != nil {
		return nil, err
	}
	if _, err = chain.UpdateSnapshot(snapshot, false); err != nil {
		return nil, err
	}
	if len(chain.Snapshot.Signed.Meta) != 1 || !metaBound(chain.Snapshot.Signed.Meta["targets.json"]) || !chain.Snapshot.Signed.Expires.After(now) || chain.Snapshot.Signed.Expires.After(now.Add(7*24*time.Hour)) {
		return nil, errors.New("unsupported snapshot or validity window")
	}
	if targets == nil {
		return chain, nil
	}
	if err = metadataShape(targets, "targets"); err != nil {
		return nil, err
	}
	if _, err = chain.UpdateTargets(targets); err != nil {
		return nil, err
	}
	t := chain.Targets["targets"]
	if len(t.Signed.Targets) != len(targetNames(seed)) || !t.Signed.Expires.After(now) || t.Signed.Expires.After(now.Add(30*24*time.Hour)) {
		return nil, errors.New("exact descriptor/archive TUF targets required")
	}
	for _, name := range targetNames(seed) {
		v := t.Signed.Targets[name]
		limit := int64(update.MaxDocument)
		if name == archiveTarget || name == productionArchiveTarget {
			limit = maxArchiveBytes
		}
		if v == nil || v.Length <= 0 || v.Length > limit || len(v.Hashes) != 1 || len(v.Hashes["sha256"]) != sha256.Size {
			return nil, errors.New("missing or unsupported bounded TUF target")
		}
	}
	return chain, nil
}

func metaBound(m *metadata.MetaFiles) bool {
	return m != nil && m.Length > 0 && m.Length <= update.MaxDocument && len(m.Hashes) == 1 && len(m.Hashes["sha256"]) == sha256.Size
}

func verifyDescriptor(b []byte, seed Seed, chain *trustedmetadata.TrustedMetadata) (Descriptor, error) {
	var d Descriptor
	targets := chain.Targets["targets"].Signed.Targets
	descriptorName, archiveName := descriptorTarget, archiveTarget
	if seed.Version == 3 {
		descriptorName, archiveName = productionDescriptorTarget, productionArchiveTarget
	}
	if err := targets[descriptorName].VerifyLengthHashes(b); err != nil {
		return d, err
	}
	if update.StrictJSON(b, &d) != nil || d.Binding != seed.Binding || d.Sequence < seed.MinimumSequence || d.ProductVersion == "" || len(d.ProductVersion) > 80 || d.Source == "" || len(d.Source) > 160 || strings.ContainsAny(d.ProductVersion+d.Source, "\r\n\x00") || !hashOK(d.BundleSHA256) || d.Archive.File != archiveTarget || d.Archive.Size != targets[archiveName].Length || d.Archive.SHA256 != hex.EncodeToString(targets[archiveName].Hashes["sha256"]) {
		return d, errors.New("first-install descriptor differs from pinned policy or signed targets")
	}
	if seed.Version == 1 && d.GitHub != (releaseops.GitHubAsset{}) {
		return d, errors.New("version1 descriptor cannot acquire GitHub authority")
	}
	if seed.Version >= 2 && (d.GitHub.Validate(seed.GitHub) != nil || d.GitHub.Name != d.Archive.File || d.GitHub.Size != d.Archive.Size || d.GitHub.SHA256 != d.Archive.SHA256) {
		return d, errors.New("signed GitHub locator differs from authorized archive")
	}
	if seed.Version == 3 {
		if err := validateProductionDescriptor(d, targets[productionProvenanceTarget]); err != nil {
			return d, err
		}
	} else if d.Release != (ProductionRelease{}) {
		return d, errors.New("development descriptor cannot acquire production release authority")
	}
	return d, nil
}

func versioned(v int64, role string) (string, error) {
	if v < 1 || v > 1<<31-1 {
		return "", errors.New("invalid bounded TUF metadata version")
	}
	return fmt.Sprintf("%d.%s.json", v, role), nil
}
