package update

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

const MaintenanceRoot = "/var/lib/fased-maintenance"

type MaintenanceAuthority struct {
	Version     int    `json:"version"`
	Owner       int    `json:"owner"`
	Group       int    `json:"group"`
	Home        string `json:"home"`
	Profile     string `json:"profile"`
	ImageSHA256 string `json:"imageSha256"`
}

func MaintenanceOwnerRoot(owner int) string {
	return fmt.Sprintf("%s/owners/%d", MaintenanceRoot, owner)
}
func MaintenanceImagePath(hash string) string {
	return MaintenanceRoot + "/images/" + hash + "/fased-install"
}
func MaintenanceDirectory(owner int, id string) string {
	return MaintenanceOwnerRoot(owner) + "/updates/" + id
}

// MaintenanceApproval is published only by the installed, trusted OS helper
// after independent release/provenance and exact core/signer verification. It
// admits deployment bookkeeping; it never unlocks keys or permits a trade.
// This is distinct from the disposable development signature format.
type MaintenanceApproval struct {
	Version          int          `json:"version"`
	Owner            int          `json:"owner"`
	InstallationID   string       `json:"installationId"`
	OperationID      string       `json:"operationId"`
	HelperSHA256     string       `json:"helperSha256"`
	Source           string       `json:"source"`
	ProductVersion   string       `json:"productVersion"`
	ReleaseSHA256    string       `json:"releaseSha256"`
	ProvenanceSHA256 string       `json:"provenanceSha256"`
	DevelopmentOnly  bool         `json:"developmentOnly"`
	PreviousCore     string       `json:"previousCore"`
	PreviousSigner   string       `json:"previousSigner"`
	PreviousManifest string       `json:"previousManifest"`
	Core             CoreSnapshot `json:"core"`
	Plan             Manifest     `json:"plan"`
}

func (a MaintenanceApproval) Validate() error {
	src, err := hex.DecodeString(a.Source)
	if err != nil || len(src) != 20 || hex.EncodeToString(src) != a.Source || a.Version != 1 || a.Owner <= 0 || !IDOK(a.InstallationID) || !IDOK(a.OperationID) || !hashOK(a.HelperSHA256) || !hashOK(a.ReleaseSHA256) || !a.DevelopmentOnly && !hashOK(a.ProvenanceSHA256) || !hashOK(a.PreviousCore) || !hashOK(a.PreviousSigner) || !hashOK(a.PreviousManifest) {
		return errors.New("invalid protected maintenance approval")
	}
	m := a.Plan
	if m.Version != 1 || m.Product != "fased-app-maintenance" || m.Channel != "protected-installer" || m.BootstrapFrom != "" || m.Sequence == 0 || m.IssuedAt <= 0 || m.ExpiresAt <= m.IssuedAt || m.Target.OS != "linux" || (m.Target.Arch != "amd64" && m.Target.Arch != "arm64") || validateComponents(m.Components) != nil {
		return errors.New("invalid protected release contract")
	}
	if a.Core.Version != 1 || !IDOK(a.Core.BootID) || !IDOK(a.Core.ProbeID) || !tokenOK(a.Core.InstanceID) || a.Core.State.Name != "core-db" || !hashOK(a.Core.State.Anchor) {
		return errors.New("original maintenance metadata required")
	}
	for _, c := range m.Components {
		if c.Version != a.ProductVersion || c.Artifact.Size <= 0 || c.Artifact.Size > 64<<20 || c.Contract.Component == "core" && c.Artifact.File != "fased-app" || c.Contract.Component == "signer" && c.Artifact.File != "fased-signer" {
			return errors.New("maintenance requires an exact verified pair")
		}
	}
	return nil
}

func (a MaintenanceApproval) Digest() string {
	return SnapshotDigest("protected-maintenance-approval:v1", a)
}

type MaintenanceCommand struct {
	Action      string            `json:"action"`
	Phase       string            `json:"phase,omitempty"`
	OperationID string            `json:"operationId"`
	ProbeID     string            `json:"probeId,omitempty"`
	Core        *CoreSnapshot     `json:"core,omitempty"`
	Pause       *CorePauseReceipt `json:"pause,omitempty"`
	Preflight   *Preflight        `json:"preflight,omitempty"`
	Binding     *InstallBinding   `json:"binding,omitempty"`
}

// MaintenanceRecord is the helper's single durable deployment/recovery journal.
// Component identities and the financial installation hold remain independent.
type MaintenanceRecord struct {
	Version      int                  `json:"version"`
	Owner        int                  `json:"owner"`
	OperationID  string               `json:"operationId"`
	Phase        string               `json:"phase"`
	Start        string               `json:"start"`
	Base         json.RawMessage      `json:"base"`
	Target       json.RawMessage      `json:"target,omitempty"`
	Approval     *MaintenanceApproval `json:"approval,omitempty"`
	Staged       *Intent              `json:"staged,omitempty"`
	Recovery     *RecoveryIndex       `json:"recovery,omitempty"`
	Installation *Installation        `json:"installation,omitempty"`
	Package      *MaintenancePackage  `json:"package,omitempty"`
	Bootstrap    bool                 `json:"bootstrap,omitempty"`
}

type MaintenancePackage struct {
	Version          string      `json:"version"`
	Source           string      `json:"source"`
	Sequence         uint64      `json:"sequence,string"`
	ReleaseSHA256    string      `json:"releaseSha256"`
	ProvenanceSHA256 string      `json:"provenanceSha256"`
	DevelopmentOnly  bool        `json:"developmentOnly"`
	Components       []Component `json:"components"`
}

// Older signers treat this exact non-ID phase as a denial fence. Successors
// recognize it only alongside the matching protected maintenance journal.
func MaintenanceFence(id string) []byte {
	b, _ := json.Marshal(struct {
		Version int    `json:"version"`
		Digest  string `json:"digest"`
		Phase   string `json:"phase"`
	}{1, SnapshotDigest("protected-maintenance-fence:v1", id), "protected-maintenance"})
	return b
}

type MetadataMaintenanceReply struct {
	Snapshot *CoreSnapshot     `json:"snapshot,omitempty"`
	Pause    *CorePauseReceipt `json:"pause,omitempty"`
	Recovery *RecoveryIndex    `json:"recovery,omitempty"`
	Error    string            `json:"error,omitempty"`
}
