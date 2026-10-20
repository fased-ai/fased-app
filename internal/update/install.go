package update

import "errors"

// The signer owns this durable hold; root-owned host phase files describe only
// executable/service progress. Neither can restore financial history or unlock.
type InstallBinding struct {
	Contracts        []Contract    `json:"contracts"`
	Version          int           `json:"version"`
	Index            RecoveryIndex `json:"index"`
	InstallationID   string        `json:"installationId"`
	PreviousCore     string        `json:"previousCore"`
	PreviousSigner   string        `json:"previousSigner"`
	PreviousManifest string        `json:"previousManifest"`
	TargetCore       string        `json:"targetCore"`
	TargetSigner     string        `json:"targetSigner"`
}
type Installation struct {
	Version                    int            `json:"version"`
	Binding                    InstallBinding `json:"binding"`
	Digest                     string         `json:"digest"`
	Phase                      string         `json:"phase"`
	InstalledSequenceCommitted bool           `json:"installedSequenceCommitted"`
	ResumeAllowed              bool           `json:"resumeAllowed"`
	FinancialExecution         bool           `json:"financialExecution"`
}
type InstallRequest struct {
	Action    string            `json:"action"`
	IntentID  string            `json:"intentId"`
	Binding   *InstallBinding   `json:"binding,omitempty"`
	Preflight *PreflightRequest `json:"preflight,omitempty"`
}
type RecoveryCheck struct {
	Preflight Preflight     `json:"preflight"`
	Index     RecoveryIndex `json:"index"`
}

func InstallDigest(b InstallBinding) string { return SnapshotDigest("install-binding:v1", b) }
func ValidateInstallBinding(b InstallBinding) error {
	if len(b.Contracts) != 2 {
		return errors.New("signed candidate contracts required")
	}
	components := map[string]bool{}
	for _, c := range b.Contracts {
		components[c.Component] = true
	}
	if !components["core"] || !components["signer"] {
		return errors.New("candidate contract pair required")
	}
	if b.Version != 1 || !IDOK(b.InstallationID) || !ValidRecoveryIndex(b.Index, b.Index.Binding) || !hashOK(b.PreviousCore) || !hashOK(b.PreviousSigner) || !hashOK(b.PreviousManifest) || !hashOK(b.TargetCore) || !hashOK(b.TargetSigner) || len(b.Index.Binding.Files) != 2 {
		return errors.New("complete immutable installation binding required")
	}
	seen := map[string]string{}
	for _, f := range b.Index.Binding.Files {
		if f.Bytes != f.Artifact.Size || f.Bytes <= 0 || f.Bytes > MaxMetadataBackup {
			return errors.New("bounded complete pair required")
		}
		seen[f.Artifact.File] = f.Artifact.SHA256
	}
	if len(seen) != 2 || seen["fased-app"] != b.TargetCore || seen["fased-signer"] != b.TargetSigner {
		return errors.New("installation requires the fixed core/signer artifact pair")
	}
	return nil
}
func ValidInstallation(i Installation) bool {
	return i.Version == 1 && ValidateInstallBinding(i.Binding) == nil && i.Digest == InstallDigest(i.Binding) && (i.Phase == "active" || i.Phase == "healthy-held" || i.Phase == "rolled-back-held") && !i.InstalledSequenceCommitted && !i.ResumeAllowed && !i.FinancialExecution
}

// Only the operations head may advance while a signer-owned installation hold
// is active. The signer validates that journal itself. Everything financial,
// including formats/features, unknown holds and the reserved floor, stays bound.
func SameHeldState(a, b SignerSnapshot) bool {
	a.BootID, a.ProbeID, b.BootID, b.ProbeID = "", "", "", ""
	a.States = append([]State(nil), a.States...)
	b.States = append([]State(nil), b.States...)
	for n := range a.States {
		if a.States[n].Name == "operations" {
			a.States[n].Anchor = ""
		}
	}
	for n := range b.States {
		if b.States[n].Name == "operations" {
			b.States[n].Anchor = ""
		}
	}
	return SnapshotDigest("held-signer", a) == SnapshotDigest("held-signer", b)
}
func RecoveryMatches(i RecoveryIndex, p Preflight) bool {
	b, e := BackupBinding(p)
	return e == nil && ValidRecoveryIndex(i, i.Binding) && SnapshotDigest("pause", i.Binding.Pause) == SnapshotDigest("pause", b.Pause) && SameHeldState(i.Binding.Signer, b.Signer) && SnapshotDigest("files", i.Binding.Files) == SnapshotDigest("files", b.Files)
}
