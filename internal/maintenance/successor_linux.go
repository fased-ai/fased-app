//go:build linux

package maintenance

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/fased-ai/fased-app/internal/update"
)

// Successor records one administrator-admitted repair of one existing
// transaction. It does not replace the original deployment approval.
type Successor struct {
	Version            int    `json:"version"`
	Owner              int    `json:"owner"`
	OperationID        string `json:"operationId"`
	PreviousImage      string `json:"previousImage"`
	SuccessorImage     string `json:"successorImage"`
	ApprovalDigest     string `json:"approvalDigest"`
	InstallationDigest string `json:"installationDigest"`
}

// ErrNoSuccessor means only that the successor record itself is absent.
// Missing authority, approvals, images or receipts must never mean fresh setup.
var ErrNoSuccessor = errors.New("no protected successor is registered")

func successorPath(owner int) string { return OwnerRoot(owner) + "/successor.json" }

func canonicalHash(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

// validateSuccessor checks immutable identities only. The caller must also
// inspect the actual protected deployment, signer and core before advancing.
func validateSuccessor(h Successor, a Authority, r update.MaintenanceRecord, approval update.MaintenanceApproval) error {
	return validateSuccessorTransition(h, a, r, approval, false, false)
}

// A rotated authority is accepted only behind the exact operation-bound
// restart fence and after the original signer transaction has completed.
func validateSuccessorTransition(h Successor, a Authority, r update.MaintenanceRecord, approval update.MaintenanceApproval, fenced, archived bool) error {
	if h.Version != 1 || h.Owner <= 0 || h.Owner != a.Owner || h.Owner != r.Owner || !update.IDOK(h.OperationID) || h.OperationID != r.OperationID || !canonicalHash(h.PreviousImage) || !canonicalHash(h.SuccessorImage) || h.PreviousImage == h.SuccessorImage || !canonicalHash(h.ApprovalDigest) || !canonicalHash(h.InstallationDigest) {
		return errors.New("invalid protected successor identity")
	}
	if a.ImageSHA256 != h.PreviousImage && !(a.ImageSHA256 == h.SuccessorImage && (fenced || archived) && r.Phase == "completed" && r.Installation != nil && r.Installation.Phase == "completed") {
		return errors.New("successor does not belong to this maintenance authority")
	}
	if approval.Validate() != nil || approval.Owner != h.Owner || approval.OperationID != h.OperationID || approval.HelperSHA256 != h.PreviousImage || approval.Digest() != h.ApprovalDigest || r.Approval == nil || r.Approval.Digest() != h.ApprovalDigest {
		return errors.New("original protected approval differs")
	}
	if r.Package == nil || r.Package.Sequence != approval.Plan.Sequence || r.Package.ReleaseSHA256 != approval.ReleaseSHA256 || r.Package.Source != approval.Source || r.Staged == nil || r.Staged.ID != h.OperationID || r.Staged.ManifestSHA256 != h.ApprovalDigest || r.Staged.Sequence != approval.Plan.Sequence {
		return errors.New("original protected target differs")
	}
	if r.Recovery == nil || r.Installation == nil || !update.ValidInstallation(*r.Installation) || r.Installation.Digest != h.InstallationDigest || r.Installation.Binding.InstallationID != approval.InstallationID || update.SnapshotDigest("successor-recovery", *r.Recovery) != update.SnapshotDigest("successor-recovery", r.Installation.Binding.Index) {
		return errors.New("original signer hold or backup differs")
	}
	switch r.Phase {
	case "ready", "signer-healthy-held", "signer-committed-held", "signer-completed", "completed":
		if r.Start != "target" {
			return errors.New("target start decision differs")
		}
	case "quiescing", "replacing":
		if r.Start != "" {
			return errors.New("stopped transition cannot start a pair")
		}
	case "base-recovery":
		if r.Start != "base" {
			return errors.New("base recovery decision differs")
		}
	default:
		return errors.New("unsupported successor transaction phase")
	}
	return nil
}

func readSuccessorReceipt(h Successor) (update.MaintenanceRecord, error) {
	var r update.MaintenanceRecord
	f, err := update.OpenProtected(update.MaintenanceDirectory(h.Owner, h.OperationID)+"/receipt.json", 256<<10, 0644)
	if err != nil {
		return r, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (256<<10)+1))
	if err != nil || len(b) > 256<<10 || update.StrictJSON(b, &r) != nil || r.Version != 1 || r.Owner != h.Owner || r.OperationID != h.OperationID || r.Phase != "completed" || r.Start != "target" {
		return r, errors.New("original successor receipt differs")
	}
	return r, nil
}

func selectSuccessorRecord(h Successor, a Authority, current, receipt update.MaintenanceRecord, archived, fenced bool) (update.MaintenanceRecord, error) {
	if current.OperationID != h.OperationID {
		if fenced || !archived || a.ImageSHA256 != h.SuccessorImage {
			return update.MaintenanceRecord{}, errors.New("original successor receipt unavailable")
		}
		return receipt, nil
	}
	if archived && update.SnapshotDigest("successor-receipt", receipt) != update.SnapshotDigest("successor-receipt", current) {
		return update.MaintenanceRecord{}, errors.New("original successor receipt changed")
	}
	return current, nil
}

// SuccessorFence is a denial-only record. Existing predecessors reject its
// presence before they can replace the active custody journal.
func SuccessorFence(h Successor) ([]byte, error) {
	if h.Version != 1 || h.Owner <= 0 || !update.IDOK(h.OperationID) || !canonicalHash(h.PreviousImage) || !canonicalHash(h.SuccessorImage) || h.PreviousImage == h.SuccessorImage || !canonicalHash(h.ApprovalDigest) || !canonicalHash(h.InstallationDigest) {
		return nil, errors.New("exact successor fence identity required")
	}
	b, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func readSuccessorFence(h Successor) (bool, error) {
	want, err := SuccessorFence(h)
	if err != nil {
		return false, err
	}
	root := "/var/lib/fased-app-custody-" + strconv.Itoa(h.Owner)
	before, err := os.Lstat(root)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("successor custody installation is unavailable")
	}
	current, err := update.ReadMaintenanceRecord(h.Owner)
	if err != nil {
		return false, errors.New("successor custody journal is unavailable")
	}
	approval, err := readOriginalApproval(h.Owner, h.OperationID)
	if err != nil || approval.Digest() != h.ApprovalDigest {
		return false, errors.New("original successor approval is unavailable")
	}
	manifest, err := update.OpenProtected(root+"/install.json", 64<<10, 0644)
	if err != nil {
		return false, errors.New("successor custody manifest is unavailable")
	}
	body, readErr := io.ReadAll(io.LimitReader(manifest, (64<<10)+1))
	closeErr := manifest.Close()
	if readErr != nil || closeErr != nil || validateSuccessorManifest(h, approval, current, body) != nil {
		return false, errors.New("successor custody manifest differs")
	}
	path := root + "/update-start-blocked.json"
	f, err := update.OpenProtected(path, 8192, 0644)
	if errors.Is(err, os.ErrNotExist) {
		after, statErr := os.Lstat(root)
		if statErr != nil || !os.SameFile(before, after) {
			return false, errors.New("successor custody directory changed")
		}
		return false, nil
	}
	if err != nil {
		return false, errors.New("foreign successor fence; preserve it")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || !bytes.Equal(b, want) {
		return false, errors.New("foreign successor fence; preserve it")
	}
	after, err := os.Lstat(root)
	if err != nil || !os.SameFile(before, after) {
		return false, errors.New("successor custody directory changed")
	}
	return true, nil
}

func validateSuccessorManifest(h Successor, approval update.MaintenanceApproval, current update.MaintenanceRecord, body []byte) error {
	var fields map[string]json.RawMessage
	var owner int
	var root, id string
	if len(body) > 64<<10 || update.StrictJSON(body, &fields) != nil || json.Unmarshal(fields["owner"], &owner) != nil || json.Unmarshal(fields["root"], &root) != nil || json.Unmarshal(fields["id"], &id) != nil || owner != h.Owner || current.Owner != h.Owner || root != "/var/lib/fased-app-custody-"+strconv.Itoa(h.Owner) || id != approval.InstallationID {
		return errors.New("successor custody identity differs")
	}
	compact := func(data []byte) []byte {
		var out bytes.Buffer
		if json.Compact(&out, data) != nil {
			return nil
		}
		return out.Bytes()
	}
	actual, base, target := compact(body), compact(current.Base), compact(current.Target)
	if actual == nil || base == nil || target == nil {
		return errors.New("successor custody manifest unavailable")
	}
	switch current.Start {
	case "base":
		if bytes.Equal(actual, base) {
			return nil
		}
	case "target":
		if bytes.Equal(actual, target) {
			return nil
		}
	case "":
		if bytes.Equal(actual, base) || bytes.Equal(actual, target) {
			return nil
		}
	}
	return errors.New("successor custody manifest is not the recorded base or target")
}

// SuccessorFenced verifies the root-owned denial marker for this exact
// successor. The marker itself grants no signing or update authority.
func SuccessorFenced(h Successor) (bool, error) { return readSuccessorFence(h) }

func readOriginalApproval(owner int, id string) (update.MaintenanceApproval, error) {
	var approval update.MaintenanceApproval
	f, err := update.OpenProtected(update.MaintenanceDirectory(owner, id)+"/approval.json", 64<<10, 0644)
	if err != nil {
		return approval, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(b) > 64<<10 || update.StrictJSON(b, &approval) != nil || approval.Validate() != nil {
		return approval, errors.New("original protected approval unavailable")
	}
	return approval, nil
}

// ReadSuccessor independently checks the root-owned handoff, original
// approval, existing authority and durable transaction on every use.
func ReadSuccessor(owner int) (Successor, error) {
	return readSuccessor(owner, func() (io.ReadCloser, error) {
		return update.OpenProtected(successorPath(owner), 8192, 0644)
	})
}

func readSuccessor(owner int, openRecord func() (io.ReadCloser, error)) (Successor, error) {
	var h Successor
	f, err := openRecord()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return h, ErrNoSuccessor
		}
		return h, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(b) > 8192 || update.StrictJSON(b, &h) != nil {
		return h, errors.New("invalid protected successor")
	}
	a, err := ReadAuthority(owner)
	if err != nil {
		return h, err
	}
	if err = ValidateOwnerIdentity(a); err != nil {
		return h, err
	}
	current, err := update.ReadMaintenanceRecord(owner)
	if err != nil {
		return h, err
	}
	fenced, err := readSuccessorFence(h)
	if err != nil {
		return h, err
	}
	receipt, receiptErr := readSuccessorReceipt(h)
	archived := receiptErr == nil
	if receiptErr != nil && !errors.Is(receiptErr, os.ErrNotExist) {
		return h, receiptErr
	}
	r, err := selectSuccessorRecord(h, a, current, receipt, archived, fenced)
	if err != nil {
		return h, err
	}
	approval, err := readOriginalApproval(owner, h.OperationID)
	if err != nil {
		return h, err
	}
	if err = validateSuccessorTransition(h, a, r, approval, fenced, archived); err != nil {
		return h, err
	}
	for _, hash := range []string{h.PreviousImage, h.SuccessorImage} {
		candidate := a
		candidate.ImageSHA256 = hash
		if err = VerifyImage(candidate); err != nil {
			return h, err
		}
	}
	return h, nil
}

// RunningSuccessor verifies the actual running protected image; callers cannot
// select an executable or change the original authority.
func RunningSuccessor(owner int) (Successor, error) {
	h, err := ReadSuccessor(owner)
	if err != nil {
		return h, err
	}
	image, err := os.Executable()
	if err != nil || image != ImagePath(h.SuccessorImage) {
		return h, errors.New("protected successor executable differs")
	}
	return h, nil
}

// NewSuccessor is called only by the root-owned, verified installer bootstrap
// while it holds the existing OS transaction lock. It never selects a package
// or changes the held deployment.
func NewSuccessor(owner int) (Successor, error) {
	var h Successor
	if os.Getuid() != 0 || os.Geteuid() != 0 || owner <= 0 {
		return h, errors.New("protected successor bootstrap required")
	}
	image, err := os.Executable()
	if err != nil || !filepath.IsAbs(image) || filepath.Clean(image) != image {
		return h, errors.New("protected successor executable required")
	}
	a, err := ReadAuthority(owner)
	if err != nil {
		return h, err
	}
	if err = ValidateOwnerIdentity(a); err != nil {
		return h, err
	}
	if caller := os.Getenv("SUDO_UID"); caller != "" && caller != strconv.Itoa(owner) && caller != "0" {
		return h, errors.New("successor caller does not own this installation")
	}
	r, err := update.ReadMaintenanceRecord(owner)
	if err != nil {
		return h, err
	}
	approval, err := update.ReadMaintenanceApproval(owner, r.OperationID)
	if err != nil || r.Installation == nil {
		return h, errors.New("original protected approval and signer hold required")
	}
	h = Successor{Version: 1, Owner: owner, OperationID: r.OperationID, PreviousImage: a.ImageSHA256, SuccessorImage: filepath.Base(filepath.Dir(image)), ApprovalDigest: approval.Digest(), InstallationDigest: r.Installation.Digest}
	if image != ImagePath(h.SuccessorImage) || r.Phase != "ready" || r.Start != "target" || r.Installation.Phase != "active" {
		return h, errors.New("only the exact held target can admit a successor")
	}
	if err = validateSuccessor(h, a, r, approval); err != nil {
		return h, err
	}
	for _, hash := range []string{h.PreviousImage, h.SuccessorImage} {
		candidate := a
		candidate.ImageSHA256 = hash
		if err = VerifyImage(candidate); err != nil {
			return h, err
		}
	}
	return h, nil
}

func PublishSuccessor(h Successor) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return PublishProtected(successorPath(h.Owner), append(b, '\n'), 0644)
}

// OpenSuccessorLock accepts only the descriptor explicitly inherited from the
// protected root conductor. The AppUID cannot open the root-only lock itself.
// The same open-file-description flock remains held if the conductor exits.
func OpenSuccessorLock(owner int) (*os.File, error) {
	if owner <= 0 {
		return nil, errors.New("original successor lock required")
	}
	path := OwnerRoot(owner) + "/operation.lock"
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		i, err := os.Lstat(dir)
		if err != nil {
			return nil, err
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0022 != 0 {
			return nil, errors.New("foreign successor lock directory")
		}
		if dir == "/" {
			break
		}
	}
	f := os.NewFile(3, "successor-operation-lock")
	if f == nil {
		return nil, errors.New("inherited successor lock required")
	}
	actual, err := f.Stat()
	if err != nil {
		return nil, errors.New("inherited successor lock required")
	}
	want, err := os.Lstat(path)
	st, ok := actual.Sys().(*syscall.Stat_t)
	if err != nil || !ok || st.Uid != 0 || st.Gid != 0 || st.Nlink != 1 || !actual.Mode().IsRegular() || actual.Mode().Perm() != 0600 || !want.Mode().IsRegular() || !os.SameFile(actual, want) {
		return nil, errors.New("inherited successor lock differs")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.New("original successor lock is not held")
	}
	syscall.CloseOnExec(int(f.Fd()))
	return f, nil
}
