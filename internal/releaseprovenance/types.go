// Package releaseprovenance verifies offline production build evidence. Its
// decisions authorize no installation, signing, update or release by themselves.
package releaseprovenance

import "time"

const PolicyVersion = 1
const Issuer = "https://token.actions.githubusercontent.com"
const BuildType = "https://slsa-framework.github.io/github-actions-buildtypes/workflow/v1"
const BuilderID = "https://github.com/actions/runner/github-hosted"
const WorkflowPath = ".github/workflows/release.yml"
const MaxBundleBytes = 64 << 10
const MaxTrustedRootBytes = 32 << 10

// Policy is independently pinned by the first-install seed. A bundle cannot
// supply or replace this material. Production values have no automatic default.
type Policy struct {
	Version      int    `json:"version"`
	Repository   string `json:"repository"`
	RepositoryID string `json:"repositoryId"`
	OwnerID      string `json:"ownerId"`
	Workflow     string `json:"workflow"`
	TrustedRoot  []byte `json:"trustedRoot"`
}

// Expected comes only from a TUF-authorized descriptor, never from the bundle.
// It includes only identities actually proved by the standard GitHub statement
// and verified certificate. Tree/tag-object/lock assertions belong to TUF.
type Expected struct {
	ArchiveName    string `json:"archiveName"`
	ArchiveSHA256  string `json:"archiveSha256"`
	SourceCommit   string `json:"sourceCommit"`
	WorkflowCommit string `json:"workflowCommit"`
	Tag            string `json:"tag"`
	RunID          string `json:"runId"`
	RunAttempt     string `json:"runAttempt"`
}

// Decision contains public verified identities only; it is not a replayable
// installation permit. The bootstrap re-verifies retained original bytes.
type Decision struct {
	BundleSHA256    string
	PolicySHA256    string
	Identity        string
	VerifiedAt      time.Time
	TimestampSource string
}
