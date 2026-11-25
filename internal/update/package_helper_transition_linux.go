//go:build linux

package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"slices"
)

const (
	PackageHelperTransitionFile  = "helper-transition.json"
	maxHelperTransitionAncestors = 64
)

// PackageHelperTransition records an instance's helper replacement chain and the
// terminal journal it inherited. It grants no signer maintenance access.
type PackageHelperTransition struct {
	Version        int      `json:"version"`
	Owner          int      `json:"owner"`
	Instance       string   `json:"instance"`
	PreviousHelper string   `json:"previousHelperSha256"`
	NextHelper     string   `json:"nextHelperSha256"`
	Ancestors      []string `json:"ancestorHelpersSha256,omitempty"`
	JournalSHA256  string   `json:"journalSha256"`
}

func (t PackageHelperTransition) Valid(owner int, instance string) bool {
	if !(t.Version == 1 && t.Owner == owner && owner > 0 && t.Instance == instance && IDOK(instance) &&
		hashOK(t.PreviousHelper) && hashOK(t.NextHelper) && t.PreviousHelper != t.NextHelper && hashOK(t.JournalSHA256) &&
		len(t.Ancestors) <= maxHelperTransitionAncestors) {
		return false
	}
	seen := make(map[string]struct{}, len(t.Ancestors))
	for _, h := range t.Ancestors {
		if !hashOK(h) || h == t.PreviousHelper || h == t.NextHelper {
			return false
		}
		if _, dup := seen[h]; dup {
			return false
		}
		seen[h] = struct{}{}
	}
	return true
}

func (t PackageHelperTransition) Equal(other PackageHelperTransition) bool {
	return t.Version == other.Version &&
		t.Owner == other.Owner &&
		t.Instance == other.Instance &&
		t.PreviousHelper == other.PreviousHelper &&
		t.NextHelper == other.NextHelper &&
		t.JournalSHA256 == other.JournalSHA256 &&
		slices.Equal(t.Ancestors, other.Ancestors)
}

func (t PackageHelperTransition) IncludesPredecessor(helper string) bool {
	return hashOK(helper) && (helper == t.PreviousHelper || slices.Contains(t.Ancestors, helper))
}

func (t PackageHelperTransition) ChainPredecessors() []string {
	out := make([]string, 0, len(t.Ancestors)+1)
	out = append(out, t.Ancestors...)
	out = append(out, t.PreviousHelper)
	return out
}

func (t PackageHelperTransition) Inherits(r MaintenanceRecord) bool {
	return t.Owner == r.Owner && (r.Phase == "idle" || r.Phase == "completed" && r.Start == "target") &&
		t.JournalSHA256 == SnapshotDigest("package-helper-journal:v1", r)
}

func ReadPackageHelperTransition(owner int, location, instance string) (PackageHelperTransition, error) {
	var t PackageHelperTransition
	root, err := PackageMaintenanceRoot(owner, location, instance)
	if err != nil {
		return t, err
	}
	f, err := OpenProtected(root+"/"+PackageHelperTransitionFile, 8192, 0644)
	if err != nil {
		return t, err
	}
	b, readErr := io.ReadAll(io.LimitReader(f, 8193))
	if err = errors.Join(readErr, f.Close()); err != nil || StrictJSON(b, &t) != nil || !t.Valid(owner, instance) {
		return PackageHelperTransition{}, errors.New("invalid protected package helper transition")
	}
	return t, nil
}

// ReadInheritedPackageApproval is limited to completed service selection.
// Active signer and core approval readers still require the current helper.
func ReadInheritedPackageApproval(owner int, location, instance, activeHelper string, r MaintenanceRecord) (MaintenanceApproval, error) {
	t, err := ReadPackageHelperTransition(owner, location, instance)
	if err != nil || !t.Inherits(r) {
		return MaintenanceApproval{}, errors.New("the original completed package approval is unavailable")
	}
	return readTransitionPredecessorApproval(t, owner, location, instance, activeHelper, r, false)
}

// ReadArchivedPredecessorApproval verifies completed evidence already stored
// in the protected candidate archive. It is never used for live approval.
func ReadArchivedPredecessorApproval(owner int, location, instance, activeHelper string, r MaintenanceRecord) (MaintenanceApproval, error) {
	t, err := ReadPackageHelperTransition(owner, location, instance)
	if err != nil {
		return MaintenanceApproval{}, err
	}
	return readTransitionPredecessorApproval(t, owner, location, instance, activeHelper, r, true)
}

func readTransitionPredecessorApproval(t PackageHelperTransition, owner int, location, instance, activeHelper string, r MaintenanceRecord, allowAncestors bool) (MaintenanceApproval, error) {
	if !t.Valid(owner, instance) || t.NextHelper != activeHelper || r.Owner != owner || r.Phase != "completed" || r.Start != "target" || r.Approval == nil {
		return MaintenanceApproval{}, errors.New("the original completed package approval is unavailable")
	}
	if allowAncestors {
		if !t.IncludesPredecessor(r.Approval.HelperSHA256) {
			return MaintenanceApproval{}, errors.New("the original completed package approval is unavailable")
		}
	} else if r.Approval.HelperSHA256 != t.PreviousHelper {
		return MaintenanceApproval{}, errors.New("the original completed package approval is unavailable")
	}
	helperHash := r.Approval.HelperSHA256
	a, err := readPackageApproval(owner, location, instance, r.OperationID)
	if err != nil || a.Digest() != r.Approval.Digest() {
		return MaintenanceApproval{}, errors.New("completed predecessor approval differs")
	}
	image, err := OpenProtected(MaintenanceImagePath(helperHash), 128<<20, 0555)
	if err != nil {
		return MaintenanceApproval{}, err
	}
	defer image.Close()
	h := sha256.New()
	if _, err = io.CopyBuffer(h, io.LimitReader(image, (128<<20)+1), make([]byte, 32<<10)); err != nil || hex.EncodeToString(h.Sum(nil)) != helperHash {
		return MaintenanceApproval{}, errors.New("the original protected helper differs")
	}
	return a, nil
}
