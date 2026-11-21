//go:build linux

package custodyinstall

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/update"
)

// AutomaticPackageOperation enforces the format-4 empty-signer boundary for
// opt-in Automatic updates across any advancing Linux release pair.
func AutomaticPackageOperation(m Manifest, base *update.MaintenancePackage, o core.ApplicationUpdateOperation) bool {
	return m.Layout == PackageLayout && (m.Location == "local" || m.Location == "remote") && m.Network == "" &&
		base != nil && !base.DevelopmentOnly && base.Sequence > 0 && validHex(base.ReleaseSHA256, 32) &&
		o.Automatic && update.IDOK(o.ID) && validHex(o.PolicySHA256, 32) &&
		o.Release != nil && o.Release.Sequence > base.Sequence && o.Release.ManifestSHA256 != base.ReleaseSHA256 &&
		validHex(o.Release.ManifestSHA256, 32) && validHex(o.Release.EngineSHA256, 32) &&
		o.Release.Target == (update.Target{OS: "linux", Arch: runtime.GOARCH})
}

type packageAutomaticAdmission struct {
	Version      int                     `json:"version"`
	OperationID  string                  `json:"operationId"`
	BaseSHA256   string                  `json:"baseSha256"`
	BasePacket   string                  `json:"basePacketSha256"`
	PolicySHA256 string                  `json:"policySha256"`
	Release      core.ApplicationRelease `json:"release"`
}

func verifyPackageAutomaticRemoteApproval(m Manifest) error {
	return verifyPackageAutomaticRemoteApprovalWith(m, privateaccess.ReadProtectedPackageInitialApproval)
}

func verifyPackageAutomaticRemoteApprovalWith(m Manifest, readApproval func(int, string) (privateaccess.ProtectedInitialApproval, error)) error {
	if m.Location != "remote" {
		return nil
	}
	approval, err := readApproval(m.Owner, m.ID)
	if err != nil || approval.Owner != m.Owner || approval.Instance != m.ID ||
		approval.Origin != m.PublicOrigin || approval.Listen != m.Listen ||
		approval.Device.UserID <= 0 || approval.Device.NodeID == "" {
		return errors.New("the original Remote package device approval is required for Automatic updates")
	}
	return nil
}

func packageAutomaticAdmissionFor(m Manifest, base *update.MaintenancePackage, view core.ApplicationOwnerView) (packageAutomaticAdmission, error) {
	o, policy := view.View.Operation, view.View.Policy
	if !AutomaticPackageOperation(m, base, o) || view.RunningSHA256 != m.CoreHash || policy.Mode != "automatic" ||
		o.PolicySHA256 != update.SnapshotDigest("application-update-policy", policy) {
		return packageAutomaticAdmission{}, errors.New("exact Automatic package operation and policy required")
	}
	if policy.Pin != nil && (policy.Pin.Sequence != strconv.FormatUint(o.Release.Sequence, 10) ||
		policy.Pin.ManifestSHA256 != o.Release.ManifestSHA256) {
		return packageAutomaticAdmission{}, errors.New("Automatic release differs from the saved version pin")
	}
	return packageAutomaticAdmission{
		Version:      1,
		OperationID:  o.ID,
		BaseSHA256:   sum(mustJSON(m)),
		BasePacket:   base.ReleaseSHA256,
		PolicySHA256: o.PolicySHA256,
		Release:      *o.Release,
	}, nil
}

// RecordPackageAutomaticOperation stores per-operation Automatic admission under
// the protected operation directory after verifying the package and policy.
func RecordPackageAutomaticOperation(m Manifest, base *update.MaintenancePackage, view core.ApplicationOwnerView) error {
	if !view.View.Operation.Automatic {
		return nil
	}
	admission, err := packageAutomaticAdmissionFor(m, base, view)
	if err != nil {
		return err
	}
	if err = verifyPackageAutomaticRemoteApproval(m); err != nil {
		return err
	}
	opDir, err := update.PackageMaintenanceDirectory(m.Owner, m.Location, m.ID, view.View.Operation.ID)
	if err != nil {
		return err
	}
	for _, dir := range []string{filepath.Dir(opDir), opDir} {
		if err = maintenance.EnsureProtectedDirectory(dir); err != nil {
			return err
		}
	}
	return maintenance.PublishProtected(filepath.Join(opDir, "automatic-operation.json"), mustJSON(admission), 0644)
}

func readPackageAutomaticOperation(base Manifest, r update.MaintenanceRecord) (*packageAutomaticAdmission, error) {
	opDir, err := update.PackageMaintenanceDirectory(base.Owner, base.Location, base.ID, r.OperationID)
	if err != nil {
		return nil, err
	}
	f, err := readRoot(filepath.Join(opDir, "automatic-operation.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var a packageAutomaticAdmission
	if err != nil || f.Mode != 0644 || len(f.Data) > 4096 || update.StrictJSON(f.Data, &a) != nil ||
		!known(f, mustJSON(a), 0644) || !a.matchesRecord(base, r) {
		return nil, errors.New("original package Automatic recovery authority differs; preserve it")
	}
	if err = verifyPackageAutomaticRemoteApproval(base); err != nil {
		return nil, err
	}
	return &a, nil
}

func (a packageAutomaticAdmission) matchesRecord(base Manifest, r update.MaintenanceRecord) bool {
	o := core.ApplicationUpdateOperation{
		ID:           a.OperationID,
		Automatic:    true,
		PolicySHA256: a.PolicySHA256,
		Release:      &a.Release,
	}
	if a.Version != 1 || !AutomaticPackageOperation(base, r.PreviousPackage, o) ||
		a.BasePacket != r.PreviousPackage.ReleaseSHA256 || r.Owner != base.Owner ||
		a.OperationID != r.OperationID || a.BaseSHA256 != sum(mustJSON(base)) ||
		r.Package == nil || a.Release.ManifestSHA256 != r.Package.ReleaseSHA256 ||
		a.Release.Sequence != r.Package.Sequence || a.Release.Version != r.Package.Version {
		return false
	}
	for _, component := range r.Package.Components {
		if component.Contract.Component == "core" && component.Artifact.SHA256 == a.Release.EngineSHA256 {
			return true
		}
	}
	return false
}

func (a *packageAutomaticAdmission) matchesView(view core.ApplicationOwnerView) bool {
	o := view.View.Operation
	if a == nil {
		return !o.Automatic
	}
	return o.Automatic && o.ID == a.OperationID && o.PolicySHA256 == a.PolicySHA256 &&
		o.PolicySHA256 == update.SnapshotDigest("application-update-policy", view.View.Policy) &&
		view.View.Policy.Mode == "automatic" && o.Release != nil && *o.Release == a.Release
}
