//go:build linux

package update

import (
	"errors"
	"io"
	"os"
	"path/filepath"
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

func ReadMaintenanceRecord(owner int) (MaintenanceRecord, error) {
	var r MaintenanceRecord
	if owner <= 0 {
		return r, errors.New("maintenance owner required")
	}
	f, err := OpenProtected(MaintenanceOwnerRoot(owner)+"/custody.json", 256<<10, 0644)
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
