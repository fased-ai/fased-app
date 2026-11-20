//go:build linux

package custodyinstall

import (
	"errors"
	"os"
	"strconv"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/hostplatform"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/update"
)

const automaticPredecessorTarget = "f009a6ad164403688e062e384f23c4ead7d994f1f94d339b8fdf4ee82ffb4725"

// ControlledAutomaticOperation gates the test-only exception in both the
// installed controller and its privileged helper. Ordinary builds fail closed.
func ControlledAutomaticOperation(m Manifest, base *update.MaintenancePackage, o core.ApplicationUpdateOperation) bool {
	return hostplatform.AutomaticPredecessorAllowed(m.Owner, m.Location, m.ID, m.PublicOrigin) &&
		m.Layout == PackageLayout && m.Network == "" &&
		base != nil && base.Bundle != nil && !base.DevelopmentOnly &&
		base.Version == "0.1.22" && base.Sequence == 1000023 && validHex(base.ReleaseSHA256, 32) &&
		o.Automatic && update.IDOK(o.ID) && validHex(o.PolicySHA256, 32) &&
		o.Release != nil && o.Release.ManifestSHA256 == automaticPredecessorTarget &&
		o.Release.Version == "0.1.23" && o.Release.Sequence == 1000024 &&
		o.Release.Target == (update.Target{OS: "linux", Arch: "amd64"}) && validHex(o.Release.EngineSHA256, 32)
}

func matchesAutomaticRemoteApproval(m Manifest, base *update.MaintenancePackage, approval privateaccess.ProtectedInitialApproval) bool {
	return approval.MatchesPackage(m.Owner, m.ID, m.PublicOrigin, m.Listen, base.Sequence, base.ReleaseSHA256, m.CoreHash) &&
		hostplatform.AutomaticPredecessorDeviceAllowed(approval.Device)
}

func verifyAutomaticRemoteApproval(m Manifest, base *update.MaintenancePackage) error {
	if m.Location != "remote" {
		return nil
	}
	approval, err := privateaccess.ReadProtectedPackageInitialApproval(m.Owner, m.ID)
	if err != nil || !matchesAutomaticRemoteApproval(m, base, approval) {
		return errors.New("the original pinned Remote device approval is required for controlled Automatic")
	}
	return nil
}

type automaticPredecessorAdmission struct {
	OperationID  string                  `json:"operationId"`
	BaseSHA256   string                  `json:"baseSha256"`
	BasePacket   string                  `json:"basePacketSha256"`
	PolicySHA256 string                  `json:"policySha256"`
	Release      core.ApplicationRelease `json:"release"`
}

func controlledAutomaticAdmission(m Manifest, base *update.MaintenancePackage, view core.ApplicationOwnerView) (automaticPredecessorAdmission, error) {
	o, policy := view.View.Operation, view.View.Policy
	if !ControlledAutomaticOperation(m, base, o) || view.RunningSHA256 != m.CoreHash || policy.Mode != "automatic" ||
		policy.Pin == nil || policy.Pin.ManifestSHA256 != o.Release.ManifestSHA256 ||
		policy.Pin.Sequence != strconv.FormatUint(o.Release.Sequence, 10) ||
		o.PolicySHA256 != update.SnapshotDigest("application-update-policy", policy) {
		return automaticPredecessorAdmission{}, errors.New("exact controlled Automatic operation and policy pin required")
	}
	return automaticPredecessorAdmission{
		OperationID: o.ID, BaseSHA256: sum(mustJSON(m)), BasePacket: base.ReleaseSHA256, PolicySHA256: o.PolicySHA256, Release: *o.Release,
	}, nil
}

// RecordControlledAutomatic is called only under the package lock after the
// existing worker has authenticated the candidate and original operation.
// One immutable record per test instance survives reboot and excludes a
// second Automatic operation. It does not change any release journal schema.
func RecordControlledAutomatic(m Manifest, base *update.MaintenancePackage, view core.ApplicationOwnerView) error {
	if !view.View.Operation.Automatic && !hostplatform.AutomaticPredecessorBuild {
		return nil
	}
	root, err := update.PackageMaintenanceRoot(m.Owner, m.Location, m.ID)
	if err != nil {
		return err
	}
	path := root + "/controlled-automatic.json"
	if !view.View.Operation.Automatic {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errors.New("the original controlled Automatic admission must be preserved")
	}
	admission, err := controlledAutomaticAdmission(m, base, view)
	if err != nil {
		return err
	}
	if err = verifyAutomaticRemoteApproval(m, base); err != nil {
		return err
	}
	return maintenance.PublishProtected(path, mustJSON(admission), 0644)
}

func readControlledAutomatic(base Manifest, r update.MaintenanceRecord) (*automaticPredecessorAdmission, error) {
	if !hostplatform.AutomaticPredecessorBuild {
		return nil, nil
	}
	root, err := update.PackageMaintenanceRoot(base.Owner, base.Location, base.ID)
	if err != nil {
		return nil, err
	}
	f, err := readRoot(root + "/controlled-automatic.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var a automaticPredecessorAdmission
	if err != nil || f.Mode != 0644 || len(f.Data) > 4096 || update.StrictJSON(f.Data, &a) != nil ||
		!known(f, mustJSON(a), 0644) || !a.matchesRecord(base, r) {
		return nil, errors.New("original controlled Automatic recovery authority differs; preserve it")
	}
	if err = verifyAutomaticRemoteApproval(base, r.PreviousPackage); err != nil {
		return nil, err
	}
	return &a, nil
}

func (a automaticPredecessorAdmission) matchesRecord(base Manifest, r update.MaintenanceRecord) bool {
	o := core.ApplicationUpdateOperation{ID: a.OperationID, Automatic: true, PolicySHA256: a.PolicySHA256, Release: &a.Release}
	if !ControlledAutomaticOperation(base, r.PreviousPackage, o) || a.BasePacket != r.PreviousPackage.ReleaseSHA256 ||
		r.Owner != base.Owner || a.OperationID != r.OperationID ||
		a.BaseSHA256 != sum(mustJSON(base)) || r.Package == nil || a.Release.ManifestSHA256 != r.Package.ReleaseSHA256 ||
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

func (a *automaticPredecessorAdmission) matchesView(view core.ApplicationOwnerView) bool {
	o := view.View.Operation
	if a == nil {
		return !o.Automatic
	}
	return o.Automatic && o.ID == a.OperationID && o.PolicySHA256 == a.PolicySHA256 &&
		o.PolicySHA256 == update.SnapshotDigest("application-update-policy", view.View.Policy) &&
		o.Release != nil && *o.Release == a.Release
}
