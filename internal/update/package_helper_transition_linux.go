//go:build linux

package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

const PackageHelperTransitionFile = "helper-transition.json"

// PackageHelperTransition records one instance's replacement and the terminal
// journal it inherited. It grants no signer maintenance access.
type PackageHelperTransition struct {
	Version        int    `json:"version"`
	Owner          int    `json:"owner"`
	Instance       string `json:"instance"`
	PreviousHelper string `json:"previousHelperSha256"`
	NextHelper     string `json:"nextHelperSha256"`
	JournalSHA256  string `json:"journalSha256"`
}

func (t PackageHelperTransition) Valid(owner int, instance string) bool {
	return t.Version == 1 && t.Owner == owner && owner > 0 && t.Instance == instance && IDOK(instance) &&
		hashOK(t.PreviousHelper) && hashOK(t.NextHelper) && t.PreviousHelper != t.NextHelper && hashOK(t.JournalSHA256)
}

func (t PackageHelperTransition) Inherits(r MaintenanceRecord) bool {
	return t.Owner == r.Owner && (r.Phase == "idle" || r.Phase == "completed" && r.Start == "target") &&
		t.JournalSHA256 == SnapshotDigest("package-helper-journal:v1", r)
}

func ReadPackageHelperTransition(owner int, instance string) (PackageHelperTransition, error) {
	var t PackageHelperTransition
	root, err := PackageMaintenanceRoot(owner, "local", instance)
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
func ReadInheritedPackageApproval(owner int, instance, activeHelper string, r MaintenanceRecord) (MaintenanceApproval, error) {
	t, err := ReadPackageHelperTransition(owner, instance)
	if err != nil || t.NextHelper != activeHelper || !t.Inherits(r) || r.Phase != "completed" || r.Approval == nil || r.Approval.HelperSHA256 != t.PreviousHelper {
		return MaintenanceApproval{}, errors.New("the original completed package approval is unavailable")
	}
	a, err := readPackageApproval(owner, "local", instance, r.OperationID)
	if err != nil || a.Digest() != r.Approval.Digest() {
		return MaintenanceApproval{}, errors.New("completed predecessor approval differs")
	}
	image, err := OpenProtected(MaintenanceImagePath(t.PreviousHelper), 128<<20, 0555)
	if err != nil {
		return MaintenanceApproval{}, err
	}
	defer image.Close()
	h := sha256.New()
	if _, err = io.CopyBuffer(h, io.LimitReader(image, (128<<20)+1), make([]byte, 32<<10)); err != nil || hex.EncodeToString(h.Sum(nil)) != t.PreviousHelper {
		return MaintenanceApproval{}, errors.New("the original protected helper differs")
	}
	return a, nil
}
