//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
	"path/filepath"
	"strconv"
)

func successorOverride(owner int, id string, worker bool, image string) (string, []byte) {
	unit, action := protectedRecoveryUnit(owner), "recover"
	args := " --operation " + id
	if worker {
		unit = fmt.Sprintf("fased-maintenance-%d@.service", owner)
		action = "worker"
		args = " --operation %i"
	}
	path := filepath.Join("/etc/systemd/system", unit+".d", "30-fased-successor.conf")
	body := fmt.Sprintf("[Service]\nExecStart=\nExecStart=%s maintenance-successor --owner %d --action %s%s\n", image, owner, action, args)
	return path, []byte(body)
}

// The successor does not change the original app/signer gates. Only these two
// exact root-owned service overrides may dispatch the bound recovery image.
func verifySuccessorRootUnits(ctx context.Context, a maintenance.Authority, id, image string) error {
	h, err := maintenance.RunningSuccessor(a.Owner)
	if err != nil || h.OperationID != id || image != maintenance.ImagePath(h.SuccessorImage) || a.ImageSHA256 != h.PreviousImage {
		return errors.New("exact protected successor operation required")
	}
	r, err := update.ReadMaintenanceRecord(a.Owner)
	if err != nil {
		return err
	}
	base, _, err := protectedRecordManifests(r)
	if err != nil {
		return err
	}
	original, err := protectedMaintenanceFiles(a, base)
	if err != nil {
		return err
	}
	u := strconv.Itoa(a.Owner)
	for _, v := range []struct {
		unit, fragment string
		worker         bool
		args           []string
	}{
		{protectedRecoveryUnit(a.Owner), protectedRecoveryUnit(a.Owner), false, []string{image, "maintenance-successor", "--owner", u, "--action", "recover", "--operation", id}},
		{protectedWorkerUnit(a.Owner, id), "fased-maintenance-" + u + "@.service", true, []string{image, "maintenance-successor", "--owner", u, "--action", "worker", "--operation", id}},
	} {
		fragment := "/etc/systemd/system/" + v.fragment
		originalFile, err := readRoot(fragment)
		if err != nil || !known(originalFile, original[fragment], 0644) {
			return errors.New("original protected maintenance unit differs")
		}
		path, want := successorOverride(a.Owner, id, v.worker, image)
		f, err := readRoot(path)
		if err != nil || !known(f, want, 0644) {
			return errors.New("protected successor service override differs")
		}
		fields, commands, err := atomicUnitInspection(ctx, v.unit)
		if err != nil {
			return err
		}
		if fields["LoadState"] != "loaded" || fields["FragmentPath"] != fragment || fields["DropInPaths"] != path || fields["NeedDaemonReload"] != "no" || fields["Type"] != "oneshot" || fields["User"] != "" || fields["Group"] != "" || fields["NoNewPrivileges"] != "yes" {
			return errors.New("foreign or stale successor service; preserve it")
		}
		if err = validateAtomicCommandRows(commands, []atomicPairCommand{{argv: v.args}}); err != nil {
			return err
		}
	}
	return nil
}

func verifyProtectedRootUnits(ctx context.Context, a maintenance.Authority) error {
	r, err := update.ReadMaintenanceRecord(a.Owner)
	if err != nil {
		return err
	}
	u := strconv.Itoa(a.Owner)
	image := maintenance.ImagePath(a.ImageSHA256)
	for _, v := range []struct {
		unit, fragment string
		args           []string
	}{{protectedRecoveryUnit(a.Owner), protectedRecoveryUnit(a.Owner), []string{image, "maintenance-recover", "--owner", u}}, {protectedWorkerUnit(a.Owner, r.OperationID), "fased-maintenance-" + u + "@.service", []string{image, "maintenance-worker", "--owner", u, "--operation", r.OperationID}}} {
		fields, commands, err := atomicUnitInspection(ctx, v.unit)
		if err != nil {
			return err
		}
		if fields["LoadState"] != "loaded" || fields["FragmentPath"] != "/etc/systemd/system/"+v.fragment || fields["DropInPaths"] != "" || fields["NeedDaemonReload"] != "no" || fields["Type"] != "oneshot" || fields["User"] != "" || fields["Group"] != "" || fields["NoNewPrivileges"] != "yes" {
			return errors.New("foreign or stale protected maintenance service; preserve it")
		}
		if err = validateAtomicCommandRows(commands, []atomicPairCommand{{argv: v.args}}); err != nil {
			return err
		}
	}
	return nil
}
