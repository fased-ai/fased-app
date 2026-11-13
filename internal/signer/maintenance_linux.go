//go:build linux

package signer

import (
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// Maintenance runs as the original signer UID with its actual vault lock. It
// accepts only a root-installed deployment approval and maintenance actions;
// no credential, key export, financial request or development trust is accepted.
func Maintenance(c Config, q update.MaintenanceCommand) (Response, error) {
	s, err := OpenMaintenance(c, q.OperationID)
	if err != nil {
		return Response{}, err
	}
	defer s.Close()
	return s.Request(q)
}

type MaintenanceSession struct {
	v *Vault
	c Config
	a update.MaintenanceApproval
}

func OpenMaintenance(c Config, id string) (*MaintenanceSession, error) {
	a, err := update.ReadMaintenanceApproval(c.OwnerUID, id)
	if err != nil {
		return nil, err
	}
	if c.SignerID != a.InstallationID || a.DevelopmentOnly && c.Provenance != ControlledCustodyProfile {
		return nil, errors.New("maintenance approval differs from signer identity")
	}
	v, err := inspectVaultMode(c, false)
	if err != nil {
		return nil, err
	}
	return &MaintenanceSession{v: v, c: c, a: a}, nil
}
func (s *MaintenanceSession) Close() error { return s.v.Close() }
func (s *MaintenanceSession) Request(q update.MaintenanceCommand) (Response, error) {
	v, c, a := s.v, s.c, s.a
	if q.OperationID != a.OperationID {
		return Response{}, errors.New("maintenance operation cannot change")
	}
	if !update.IDOK(q.OperationID) {
		return Response{}, ErrUnavailable
	}
	var err error
	r := Response{}
	switch q.Action {
	case "inventory":
		probe := q.ProbeID
		if !update.IDOK(probe) {
			return r, ErrUnavailable
		}
		r.UpdateInventory, err = v.UpdateInventory(probe)
		if err == nil && r.UpdateInventory.ActiveIntentID != "" {
			r.UpdateIntent, err = v.UpdateIntent(r.UpdateInventory.ActiveIntentID)
		}
		r.Status = v.Status()
		return r, err
	case "prepare":
		v.mu.Lock()
		if v.closed || v.store.failedState() || v.ledger == nil || v.ledger.missingHistory {
			v.mu.Unlock()
			return r, ErrUnavailable
		}
		s, err := v.updateSnapshot(a.Core.ProbeID)
		if err == nil {
			if old, ok := v.ledger.updates[a.OperationID]; !ok && v.now().Unix() > a.Plan.ExpiresAt {
				err = errors.New("maintenance approval expired before reservation")
			} else if ok && old.ManifestSHA256 != a.Digest() {
				err = errors.New("original update intent cannot be rebound")
			} else if ok {
				if old.Sequence != a.Plan.Sequence || update.SnapshotDigest("core", old.Core) != update.SnapshotDigest("core", a.Core) || v.ledger.activeUpdate != old.ID || (old.State != "prepared" && old.State != "staged") {
					err = errors.New("original maintenance reservation changed")
				} else {
					_, err = v.heldSnapshot(old, update.NewProbe())
				}
				if err == nil {
					out := clone(old)
					r.UpdateIntent = &out
				}
				v.mu.Unlock()
				return r, err
			}
		}
		if err == nil {
			r.UpdateIntent, err = v.prepareAuthenticatedUpdate(update.IntentRequest{ID: a.OperationID, Core: a.Core, Signer: s}, a.Plan, a.Digest(), true)
		}
		v.mu.Unlock()
		return r, err
	case "stage":
		if q.Core == nil || !update.SameCoreMetadata(*q.Core, a.Core) {
			return r, errors.New("current cold metadata differs from approved original")
		}
		i, err := v.UpdateIntent(a.OperationID)
		if err != nil {
			return r, err
		}
		if i.ManifestSHA256 != a.Digest() {
			return r, errors.New("original staging intent differs")
		}
		base := update.StageRequest{IntentID: i.ID, Sequence: i.Sequence, ManifestSHA256: i.ManifestSHA256}
		status, err := v.StageUpdate("fixture-update-stage-status", base)
		if err != nil {
			return r, err
		}
		if len(status.Files) != len(i.Artifacts) {
			return r, errors.New("staged artifact inventory changed")
		}
		if i.State != "staged" {
			for index, artifact := range i.Artifacts {
				if status.Files[index].Artifact != artifact {
					return r, errors.New("staged artifact binding changed")
				}
				f, err := update.OpenProtected(filepath.Join(update.MaintenanceDirectory(c.OwnerUID, q.OperationID), artifact.File), artifact.Size, 0755)
				if err != nil {
					return r, err
				}
				offset := status.Files[index].Bytes
				if _, err = f.Seek(offset, io.SeekStart); err != nil {
					f.Close()
					return r, err
				}
				buf := make([]byte, update.StageChunk)
				for offset < artifact.Size {
					n, e := io.ReadFull(f, buf[:min(int64(len(buf)), artifact.Size-offset)])
					if e != nil {
						f.Close()
						return r, e
					}
					chunk := base
					chunk.File = artifact.File
					chunk.Offset = offset
					chunk.Data = buf[:n]
					if _, err = v.StageUpdate("fixture-update-stage-write", chunk); err != nil {
						f.Close()
						return r, err
					}
					offset += int64(n)
				}
				f.Close()
			}
		}
		base.Core = q.Core
		r.Stage, err = v.stageUpdate("fixture-update-stage-finish", base, true)
		if err == nil {
			r.UpdateIntent, err = v.UpdateIntent(a.OperationID)
		}
		return r, err
	case "preflight", "install":
		if q.Pause == nil || !update.IDOK(q.Pause.Core.ProbeID) {
			return r, errors.New("actual bound core pause required")
		}
		i, err := v.UpdateIntent(a.OperationID)
		if err != nil {
			return r, err
		}
		if i.ManifestSHA256 != a.Digest() {
			return r, errors.New("original maintenance intent differs")
		}
		s, err := v.UpdateInventory(q.Pause.Core.ProbeID)
		if err != nil {
			return r, err
		}
		p := update.PreflightRequest{IntentID: i.ID, Sequence: i.Sequence, ManifestSHA256: i.ManifestSHA256, ProbeID: s.ProbeID, Pause: *q.Pause, ExpectedSigner: *s}
		if q.Action == "preflight" {
			r.Preflight, err = v.PreflightUpdate(p)
			return r, err
		}
		if q.Binding == nil || q.Binding.Index.Binding.Pause.Request == nil || q.Binding.Index.Binding.Pause.Request.IntentID != i.ID {
			return r, errors.New("exact installation binding required")
		}
		if q.Phase != "active" && q.Phase != "healthy-held" && q.Phase != "committed-held" && q.Phase != "completed" {
			return r, errors.New("unsupported installation phase")
		}
		r.Installation, err = v.Installation(update.InstallRequest{Action: q.Phase, IntentID: i.ID, Binding: q.Binding, Preflight: &p})
		return r, err
	case "status":
		r.Installation, err = v.Installation(update.InstallRequest{Action: "status", IntentID: a.OperationID})
		return r, err
	default:
		return r, errors.New("unsupported signer maintenance action")
	}
}

// MaintenanceConfig binds the CLI to one fixed protected installation. The
// caller cannot select another config file or reinterpret the signer identity.
func MaintenanceConfig(owner int) (Config, error) {
	if owner <= 0 || os.Getuid() == 0 || os.Geteuid() != os.Getuid() {
		return Config{}, ErrUnavailable
	}
	return LoadConfig(filepath.Join("/var/lib", "fased-app-custody-"+strconv.Itoa(owner), "identity.json"))
}

func InspectMaintenanceInventory(c Config, probe string) (Response, error) {
	if !update.IDOK(probe) {
		return Response{}, ErrUnavailable
	}
	v, err := inspectVaultMode(c, true)
	if err != nil {
		return Response{}, err
	}
	defer v.Close()
	r := Response{Status: v.Status()}
	r.UpdateInventory, err = v.UpdateInventory(probe)
	if err == nil && r.UpdateInventory.ActiveIntentID != "" {
		r.UpdateIntent, err = v.UpdateIntent(r.UpdateInventory.ActiveIntentID)
		if err == nil {
			r.Installation, err = v.Installation(update.InstallRequest{Action: "status", IntentID: r.UpdateInventory.ActiveIntentID})
			if errors.Is(err, ErrNoInstallationHold) {
				err = nil
			}
		}
	}
	return r, err
}

// A root-owned deployment cannot be redirected by the legacy owner RPC.
// The signer still validates every actual phase and bound preflight.
func protectedMaintenanceRPCAllowed(c Config, q Request, owner bool) bool {
	r, err := update.ReadMaintenanceRecord(c.OwnerUID)
	if errors.Is(err, os.ErrNotExist) {
		if c.Version == 3 {
			return completedMaintenanceRPCAllowed(q, func() bool {
				return protectedMaintenanceFenceAbsent(filepath.Dir(c.StateDir))
			})
		}
		return true
	}
	if err != nil {
		return false
	}
	return protectedMaintenanceRecordRPCAllowed(c, q, owner, r, func() bool {
		return protectedMaintenanceFenceAbsent(filepath.Dir(c.StateDir))
	})
}

func protectedMaintenanceRecordRPCAllowed(c Config, q Request, owner bool, r update.MaintenanceRecord, fenceAbsent func() bool) bool {
	if r.Phase == "idle" {
		if q.Action == "status" || q.Action == "lock" {
			return true
		}
		var identity struct {
			Version    int    `json:"version"`
			Owner      int    `json:"owner"`
			ID         string `json:"id"`
			Root       string `json:"root"`
			SignerHash string `json:"signerHash"`
			Enrollment bool   `json:"enrollment"`
			Signer     struct {
				UID int `json:"uid"`
			} `json:"signer"`
		}
		if r.ValidateIdle() != nil || c.Version != 3 || r.Owner != c.OwnerUID || r.OperationID != c.SignerID ||
			json.Unmarshal(r.Base, &identity) != nil || identity.Version != 3 || !identity.Enrollment || identity.Owner != c.OwnerUID ||
			identity.ID != c.SignerID || identity.Signer.UID != c.SignerUID || identity.Root != filepath.Dir(c.StateDir) ||
			identity.Root != "/var/lib/fased-app-custody-"+strconv.Itoa(c.OwnerUID) ||
			c.Provenance == OwnerKeyProvenance && r.Package.DevelopmentOnly {
			return false
		}
		for _, component := range r.Package.Components {
			if component.Contract.Component == "signer" && component.Artifact.SHA256 != identity.SignerHash {
				return false
			}
		}
		return completedMaintenanceRPCAllowed(q, fenceAbsent)
	}
	if r.Phase == "completed" {
		return completedMaintenanceRPCAllowed(q, fenceAbsent)
	}
	if q.Action == "status" || q.Action == "lock" {
		return true
	}
	if !owner {
		return false
	}
	switch q.Action {
	case "fixture-update-inventory", "fixture-update-status":
		return true
	case "fixture-update-preflight":
		return q.Preflight != nil && q.Preflight.IntentID == r.OperationID && r.Approval != nil && q.Preflight.ManifestSHA256 == r.Approval.Digest()
	case "fixture-install-controller":
		if q.Install == nil || q.Install.IntentID != r.OperationID {
			return false
		}
		if q.Install.Action == "status" {
			return true
		}
		if r.Start != "target" || r.Installation == nil || q.Install.Binding == nil || update.InstallDigest(*q.Install.Binding) != r.Installation.Digest {
			return false
		}
		return q.Install.Action == "healthy-held" || q.Install.Action == "committed-held" || q.Install.Action == "completed"
	default:
		return false
	}
}

func completedMaintenanceRPCAllowed(q Request, fenceAbsent func() bool) bool {
	// A present, foreign or uninspectable marker retains the update fence.
	// The completed cleanup uses the app pause peer and needs no signer write.
	return q.Action == "status" || q.Action == "lock" || fenceAbsent()
}

func protectedMaintenanceFenceAbsent(root string) bool {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return false
	}
	var original os.FileInfo
	for dir := root; ; dir = filepath.Dir(dir) {
		i, err := os.Lstat(dir)
		if err != nil {
			return false
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0022 != 0 {
			return false
		}
		if dir == root {
			original = i
		}
		if dir == "/" {
			break
		}
	}
	fd, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	dir := os.NewFile(uintptr(fd), "protected-custody-directory")
	defer dir.Close()
	i, err := dir.Stat()
	if err != nil || !os.SameFile(original, i) {
		return false
	}
	return maintenanceFenceAbsentAt(root, dir)
}

// The directory descriptor anchors the missing-final-component check. A
// renamed or replaced parent is not evidence that the fence is absent.
func maintenanceFenceAbsentAt(root string, dir *os.File) bool {
	opened, err := dir.Stat()
	if err != nil {
		return false
	}
	current, err := os.Lstat(root)
	if err != nil || !current.IsDir() || !os.SameFile(opened, current) {
		return false
	}
	fd, err := syscall.Openat(int(dir.Fd()), "update-active.json", syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err == nil {
		_ = syscall.Close(fd)
		return false
	}
	if !errors.Is(err, syscall.ENOENT) {
		return false
	}
	current, err = os.Lstat(root)
	return err == nil && current.IsDir() && os.SameFile(opened, current)
}
