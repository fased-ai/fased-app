//go:build linux

package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
)

// OpenProtected holds the actual root-owned immutable file and rejects links,
// writable ancestry and special files. It grants no write or execution access.
func OpenProtected(path string, max int64, mode os.FileMode) (*os.File, error) {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		i, err := os.Lstat(dir)
		if err != nil {
			return nil, err
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0022 != 0 {
			return nil, errors.New("protected file has writable or foreign parents")
		}
		if dir == "/" {
			break
		}
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	i, err := f.Stat()
	if err == nil {
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || st.Gid != 0 || st.Nlink != 1 || !i.Mode().IsRegular() || i.Mode() != mode || i.Size() < 0 || i.Size() > max {
			err = errors.New("protected installed file required")
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func ReadMaintenanceApproval(owner int, id string) (MaintenanceApproval, error) {
	var a MaintenanceApproval
	if owner <= 0 || !IDOK(id) {
		return a, errors.New("exact protected maintenance identity required")
	}
	f, err := OpenProtected(MaintenanceDirectory(owner, id)+"/approval.json", 64<<10, 0644)
	if err != nil {
		return a, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || StrictJSON(b, &a) != nil || a.Validate() != nil || a.Owner != owner || a.OperationID != id {
		return a, errors.New("protected maintenance approval differs")
	}
	r, err := OpenProtected(MaintenanceOwnerRoot(owner)+"/authority.json", 8192, 0644)
	if err != nil {
		return a, err
	}
	defer r.Close()
	b, err = io.ReadAll(io.LimitReader(r, 8193))
	var authority MaintenanceAuthority
	if err != nil || StrictJSON(b, &authority) != nil || authority.Version != 1 || authority.Owner != owner || authority.ImageSHA256 != a.HelperSHA256 {
		return a, errors.New("maintenance approval is not bound to the installed helper")
	}
	return a, nil
}

// ReadPackageMaintenanceApproval never falls back to an owner-wide approval.
// The instance path and the approved installation identity must both match.
func ReadPackageMaintenanceApproval(owner int, location, instance, operation string) (MaintenanceApproval, error) {
	var a MaintenanceApproval
	root, err := PackageMaintenanceRoot(owner, location, instance)
	if err != nil || !IDOK(operation) {
		return a, errors.New("exact package maintenance identity required")
	}
	f, err := OpenProtected(root+"/updates/"+operation+"/approval.json", 64<<10, 0644)
	if err != nil {
		return a, err
	}
	b, readErr := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err = errors.Join(readErr, f.Close()); err != nil || StrictJSON(b, &a) != nil || a.Validate() != nil || a.Owner != owner || a.InstallationID != instance || a.OperationID != operation {
		return MaintenanceApproval{}, errors.New("protected package approval differs")
	}
	f, err = OpenProtected(root+"/authority.json", 8192, 0644)
	if err != nil {
		return MaintenanceApproval{}, err
	}
	b, readErr = io.ReadAll(io.LimitReader(f, 8193))
	var authority MaintenanceAuthority
	if err = errors.Join(readErr, f.Close()); err != nil || StrictJSON(b, &authority) != nil || authority.Version != 1 || authority.Owner != owner || authority.Profile != location || authority.ImageSHA256 != a.HelperSHA256 {
		return MaintenanceApproval{}, errors.New("package approval is not bound to its installed helper")
	}
	return a, nil
}

func ReadMaintenanceRecord(owner int) (MaintenanceRecord, error) {
	if owner <= 0 {
		return MaintenanceRecord{}, errors.New("maintenance owner required")
	}
	return readMaintenanceRecord(MaintenanceOwnerRoot(owner)+"/custody.json", owner)
}

func readMaintenanceRecord(path string, owner int) (MaintenanceRecord, error) {
	var r MaintenanceRecord
	f, err := OpenProtected(path, 256<<10, 0644)
	if err != nil {
		return r, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (256<<10)+1))
	if err != nil || StrictJSON(b, &r) != nil || r.Version != 1 || r.Owner != owner || !IDOK(r.OperationID) || len(r.Base) == 0 || r.Start != "" && r.Start != "base" && r.Start != "target" {
		return r, errors.New("invalid protected maintenance record")
	}
	return r, nil
}

// ReadPackageMaintenanceRecord reads only this instance's protected journal.
func ReadPackageMaintenanceRecord(owner int, location, instance string) (MaintenanceRecord, error) {
	root, err := PackageMaintenanceRoot(owner, location, instance)
	if err != nil {
		return MaintenanceRecord{}, err
	}
	r, err := readMaintenanceRecord(root+"/custody.json", owner)
	if err != nil {
		return MaintenanceRecord{}, err
	}
	if !packageRecordMatchesInstance(r, owner, location, instance) {
		return MaintenanceRecord{}, errors.New("package maintenance journal belongs to another instance")
	}
	return r, nil
}

func packageRecordMatchesInstance(r MaintenanceRecord, owner int, location, instance string) bool {
	root, err := PackageInstanceRoot(owner, location, instance)
	if err != nil || r.Owner != owner || len(r.Base) == 0 {
		return false
	}
	match := func(raw json.RawMessage) (PackageManifestIdentity, bool) {
		m, err := PackageManifestIdentityFromJSON(raw)
		return m, err == nil && m.Layout == PackageInstanceLayout && m.Version == 3 && m.Owner == owner && m.ID == instance && m.Root == root && m.Location == location
	}
	base, valid := match(r.Base)
	if !valid || len(r.Target) == 0 {
		return valid
	}
	target, valid := match(r.Target)
	return valid && SamePackageManifestIdentity(base, target) && samePackageDeployment(r.Base, r.Target)
}

// Only the two executable digests may differ across one package update. In
// particular, account names/groups, network and listener are also identity,
// even though a signer cannot independently see all of those fields.
func samePackageDeployment(base, target json.RawMessage) bool {
	decode := func(raw []byte) (map[string]any, error) {
		var m map[string]any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&m); err != nil || m == nil {
			return nil, errors.New("invalid package deployment")
		}
		delete(m, "coreHash")
		delete(m, "signerHash")
		return m, nil
	}
	a, err := decode(base)
	if err != nil {
		return false
	}
	b, err := decode(target)
	return err == nil && reflect.DeepEqual(a, b)
}
