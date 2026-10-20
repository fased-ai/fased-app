package update

import "errors"

// Recovery records describe app metadata only. They can never authorize a
// financial restore, an installed sequence, binary replacement or resumption.
type RecoveryBinding struct {
	Version int              `json:"version"`
	Pause   CorePauseReceipt `json:"pause"`
	Signer  SignerSnapshot   `json:"signer"`
	Files   []StageFile      `json:"files"`
}
type MetadataSnapshot struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type RecoveryIndex struct {
	Version            int              `json:"version"`
	Phase              string           `json:"phase"`
	Binding            RecoveryBinding  `json:"binding"`
	BindingSHA256      string           `json:"bindingSha256"`
	Snapshot           MetadataSnapshot `json:"snapshot"`
	InstallAllowed     bool             `json:"installAllowed"`
	ResumeAllowed      bool             `json:"resumeAllowed"`
	FinancialExecution bool             `json:"financialExecution"`
}

// Boot/challenge are freshly checked by the owner on each call, but are not
// durable identity: restarting either service must not create another backup.
func BackupBinding(p Preflight) (RecoveryBinding, error) {
	b := RecoveryBinding{Version: 1, Pause: p.Pause, Signer: p.Signer, Files: p.Files}
	want := p.BindingSHA256
	p.BindingSHA256 = ""
	r := p.Pause.Request
	if p.Version != 1 || !p.Verified || p.InstallAllowed || p.ResumeAllowed || p.FinancialExecution || want != SnapshotDigest("preflight:v1", p) || r == nil || !ValidPauseRequest(*r) || p.IntentID != r.IntentID || p.Sequence != r.Sequence || p.ManifestSHA256 != r.ManifestSHA256 || p.Pause.State != "paused" || p.Pause.Version != 1 || !IDOK(p.Pause.PauseID) || !IDOK(p.Pause.Core.BootID) || !IDOK(p.Pause.Core.ProbeID) || !SameCoreMetadata(p.Pause.Core, r.Expected) || p.Signer.Version != 1 || !IDOK(p.Signer.BootID) || p.Signer.ProbeID != p.Pause.Core.ProbeID || p.Signer.ActiveIntentID != p.IntentID || p.Signer.SequenceFloor != p.Sequence || !hashOK(p.Signer.AuthoritySHA256) || len(p.Signer.States) != 3 || len(p.Files) != 2 {
		return b, errors.New("verified current held preflight required for metadata backup")
	}
	seen := map[string]bool{}
	for _, f := range p.Files {
		a := f.Artifact
		if !tokenOK(a.File) || a.File == "." || a.File == ".." || a.Size <= 0 || a.Size > MaxArtifact || !hashOK(a.SHA256) || f.Bytes != a.Size || seen[a.File] {
			return b, errors.New("complete staged artifact identities required")
		}
		seen[a.File] = true
	}
	b.Pause.Core.BootID, b.Pause.Core.ProbeID = "", ""
	b.Signer.BootID, b.Signer.ProbeID = "", ""
	return b, nil
}
func RecoveryDigest(b RecoveryBinding) string { return SnapshotDigest("recovery-binding:v1", b) }
func ValidRecoveryIndex(i RecoveryIndex, b RecoveryBinding) bool {
	return i.Version == 1 && i.Phase == "metadata-backed-up" && !i.InstallAllowed && !i.ResumeAllowed && !i.FinancialExecution && i.BindingSHA256 == RecoveryDigest(b) && RecoveryDigest(i.Binding) == i.BindingSHA256 && i.Snapshot.File == "metadata.sqlite" && i.Snapshot.Size > 0 && i.Snapshot.Size <= MaxMetadataBackup && hashOK(i.Snapshot.SHA256)
}

const MaxMetadataBackup = 64 << 20
