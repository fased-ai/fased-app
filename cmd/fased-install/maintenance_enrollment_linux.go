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
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
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
		return errors.New("legacy Remote source export has been retired")
	}
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
	c.WaitDelay = 2 * time.Second
	return runProtectedEnrollmentImage(c, q.Action)
}

// Only the fixed conflict diagnostic may cross the privileged boundary.
// Other child output may contain private state, so retain it in bounded memory
// and report a generic failure instead of forwarding it.
func runProtectedEnrollmentImage(c *exec.Cmd, action string) error {
	var diagnostic enrollmentDiagnostic
	c.Stderr = &diagnostic
	if err := c.Run(); err != nil {
		const conflict = "enrollment import stage: invalid or conflicting enrollment image; preserve original state"
		var exit *exec.ExitError
		if action == "import" && errors.As(err, &exit) && exit.ExitCode() == 1 && !diagnostic.overflow && string(diagnostic.data) == "fased-app: "+conflict+"\n" {
			return errors.New(conflict)
		}
		return errors.New("protected enrollment image operation failed; preserve original state")
	}
	return nil
}

type enrollmentDiagnostic struct {
	data     []byte
	overflow bool
}

func (d *enrollmentDiagnostic) Write(p []byte) (int, error) {
	if !d.overflow {
		if len(p) > 4096-len(d.data) {
			d.data, d.overflow = nil, true
		} else {
			d.data = append(d.data, p...)
		}
	}
	return len(p), nil
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
