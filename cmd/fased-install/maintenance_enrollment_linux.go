//go:build linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

// The protected helper chooses every image, role and path. The application
// image itself always runs after setpriv has dropped root and capabilities.
func maintenanceCustodyEnrollmentImage(ctx context.Context, a maintenance.Authority, q maintenance.CustodyEnrollmentImageRequest, in io.Reader, out io.Writer) error {
	m, err := custodyinstall.Load(a.Owner)
	if err != nil || m.Owner != a.Owner || m.ID != q.OperationID || m.Version != 3 || !m.Enrollment || m.Location != a.Profile || custodyinstall.Verify(m) != nil {
		return errors.New("verified custody enrollment target required")
	}
	uid, gid := m.App.UID, m.App.GID
	dir := m.Root + "/app-state"
	if q.Slot == "stage" {
		dir = m.Root + "/enrollment-" + m.ID + ".stage"
	}
	if q.Action == "export" {
		if a.Profile != "remote" {
			return errors.New("Local source export uses its ordinary owner")
		}
		source, err := vpsinstall.LoadProtected(a.Owner)
		if err != nil || source.Listen != m.Listen || source.PublicOrigin != m.PublicOrigin || source.AppUID == m.App.UID || source.AppUID == m.Signer.UID {
			return errors.New("original Remote source differs; preserving it")
		}
		if err := verifyEnrollmentOSTool("/usr/bin/systemctl"); err != nil {
			return err
		}
		check := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", "--property=ActiveState", "--value", "--", source.Unit)
		check.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
		state, err := check.Output()
		if err != nil || strings.TrimSpace(string(state)) != "inactive" {
			return errors.New("original Remote service must be suspended before export")
		}
		uid, gid, dir = source.AppUID, source.AppGID, source.Data
	} else {
		if err := custodyinstall.CheckProtectedEnrollmentSigner(a.Owner, q.OperationID); err != nil {
			return err
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 || int(st.Uid) != uid || int(st.Gid) != gid {
			return errors.New("enrollment image directory differs; preserving it")
		}
	}
	if uid <= 0 || gid <= 0 || uid == m.Signer.UID {
		return errors.New("non-root app identity required")
	}
	if err := verifyEnrollmentOSTool("/usr/bin/setpriv"); err != nil {
		return err
	}
	args := []string{"--reuid", strconv.Itoa(uid), "--regid", strconv.Itoa(gid), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", m.Root + "/fased-app", "enrollment-image", q.Action, "--data-dir", dir, "--disposable-fixture"}
	if q.InstanceID != "" {
		args = append(args, "--expected-instance", q.InstanceID)
	}
	if q.Digest != "" {
		args = append(args, "--digest", q.Digest)
	}
	c := exec.CommandContext(ctx, "/usr/bin/setpriv", args...)
	c.Dir = "/"
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
	c.Stdin = io.LimitReader(in, core.MaxEnrollmentEncodedBytes+1)
	c.Stdout = out
	c.Stderr = io.Discard
	c.WaitDelay = 2 * time.Second
	return c.Run()
}

func verifyEnrollmentOSTool(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if err = signer.VerifyExecutablePath(resolved); err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("trusted executable OS tool required")
	}
	return nil
}
