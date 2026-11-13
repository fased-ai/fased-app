//go:build linux

package enrollment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// ProtectedSource is derived by the helper from the existing Remote manifest,
// never from a maintenance request. Local identity comes from its authority.
type ProtectedSource struct {
	Data, Listen, PublicOrigin string
	UID, GID                   int
}

func openOwnerEnrollmentJournal(a maintenance.Authority, requireLock bool) (journal, error) {
	var j journal
	homeFD, err := unix.Open(a.Home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return j, err
	}
	home := os.NewFile(uintptr(homeFD), "owner-home")
	defer home.Close()
	var st unix.Stat_t
	if unix.Fstat(homeFD, &st) != nil || int(st.Uid) != a.Owner || st.Mode&0022 != 0 {
		return j, errState
	}
	configFD, err := unix.Openat(homeFD, ".config", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return j, err
	}
	config := os.NewFile(uintptr(configFD), "owner-config")
	defer config.Close()
	if unix.Fstat(configFD, &st) != nil || int(st.Uid) != a.Owner || st.Mode&0022 != 0 {
		return j, errState
	}
	dirFD, err := unix.Openat(configFD, "fased-app-enrollment", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return j, err
	}
	dir := os.NewFile(uintptr(dirFD), "owner-enrollment")
	defer dir.Close()
	if unix.Fstat(dirFD, &st) != nil || int(st.Uid) != a.Owner || st.Mode&07777 != 0700 {
		return j, errState
	}
	lockFD, err := unix.Openat(dirFD, "controller.lock", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return j, err
	}
	lock := os.NewFile(uintptr(lockFD), "owner-enrollment-lock")
	defer lock.Close()
	if unix.Fstat(lockFD, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != a.Owner || st.Mode&07777 != 0600 || st.Nlink != 1 {
		return j, errState
	}
	if requireLock {
		if err = unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			_ = unix.Flock(lockFD, unix.LOCK_UN)
			return j, errors.New("original enrollment controller lock is required")
		} else if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return j, err
		}
	}
	fd, err := unix.Openat(dirFD, "state.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return j, err
	}
	f := os.NewFile(uintptr(fd), "owner-enrollment-state")
	defer f.Close()
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != a.Owner || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Size <= 0 || st.Size > 16384 {
		return j, errState
	}
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(b) > 16384 || update.StrictJSON(b, &j) != nil || !j.valid(a.Owner) {
		return journal{}, errState
	}
	return j, nil
}

// ProtectedOwnerJournal is read-only. It permits Remote inspection while the
// controller is stopped, so an interrupted enrollment can be resumed.
func ProtectedOwnerJournal(owner int) ([]byte, error) {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return nil, err
	}
	j, err := openOwnerEnrollmentJournal(a, false)
	if err != nil {
		return nil, err
	}
	return json.Marshal(j)
}

// ProtectedRemoteIntent derives recovery intent from the held original
// controller journal and the separately protected Remote manifest.
func ProtectedRemoteIntent(owner int, source ProtectedSource, action string) (bool, bool, error) {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil || a.Profile != "remote" {
		return false, false, errState
	}
	j, err := openOwnerEnrollmentJournal(a, true)
	if err != nil {
		return false, false, errState
	}
	return remoteEnrollmentIntent(j, source, action)
}

func remoteEnrollmentIntent(j journal, source ProtectedSource, action string) (bool, bool, error) {
	if j.Source.Profile != "remote" || j.Source.Data != source.Data || j.Source.Listen != source.Listen || j.Source.PublicOrigin != source.PublicOrigin || j.Source.UID != source.UID || j.Source.GID != source.GID {
		return false, false, errState
	}
	if action == "suspend" && j.Phase != "suspending" && j.Phase != "suspended" || action == "restore" && j.Phase != "suspending" && j.Phase != "suspended" || action != "suspend" && action != "restore" {
		return false, false, errState
	}
	return j.Source.Running, j.Source.Autostart, nil
}

func withProtectedRuntime(owner int, id string, source ProtectedSource, run func(journal, *runtimeHost, custodyinstall.Manifest) error) error {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return err
	}
	j, err := openOwnerEnrollmentJournal(a, true)
	if err != nil {
		return err
	}
	m, err := custodyinstall.Load(owner)
	if err != nil || custodyinstall.Verify(m) != nil || j.ID != id || m.ID != id || j.Target != m || j.Source.Profile != a.Profile {
		return errors.New("protected enrollment identity differs; preserving it")
	}
	if a.Profile == "local" {
		if j.Source.UID != a.Owner || j.Source.GID != a.Group || j.Source.PublicOrigin != "" {
			return errState
		}
	} else if a.Profile == "remote" {
		if j.Source.Data != source.Data || j.Source.Listen != source.Listen || j.Source.PublicOrigin != source.PublicOrigin || j.Source.UID != source.UID || j.Source.GID != source.GID {
			return errState
		}
	} else {
		return errState
	}
	parent := filepath.Dir(j.Source.Data)
	real, err := filepath.EvalSymlinks(parent)
	if err != nil || real != parent {
		return errState
	}
	fd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "source-parent")
	defer f.Close()
	if a.Profile == "local" {
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || int(st.Uid) != a.Owner || st.Mode&0022 != 0 {
			return errors.New("Local source parent must be owned by the installation owner")
		}
	}
	h := &runtimeHost{parent: f, path: parent, base: filepath.Base(j.Source.Data)}
	return custodyinstall.WithProtectedEnrollmentSystem(owner, id, j.Source.UID, j.Source.GID, j.Source.Data, j.Source.ExpectedInstanceID, func(system *custodyinstall.System) error {
		h.system = system
		return run(j, h, m)
	})
}

// RunProtectedCutover reuses the existing pinned-source controller, under the
// registered helper and its caller's existing owner/controller lock.
func RunProtectedCutover(ctx context.Context, owner int, id, step string, source ProtectedSource) error {
	return withProtectedRuntime(owner, id, source, func(j journal, h *runtimeHost, m custodyinstall.Manifest) error {
		switch step {
		case "fence":
			if j.Phase != "fencing" {
				return errState
			}
			if err := custodyinstall.CheckProtectedEnrollmentSigner(owner, id); err != nil {
				return err
			}
			if err := h.image(ctx, m, "verify", stagePath(j), j.Source.ExpectedInstanceID, j.Digest, nil); err != nil {
				return err
			}
			// The helper takes its own source lease. A missing or already fenced
			// source must be preserved for interrupted-operation recovery.
			if n, err := realNode(h.pinned(h.base)); err != nil || n != j.Node {
				return errors.New("original source is unavailable; preserve the interrupted fence for recovery")
			}
			lease, err := h.export(ctx, m, j.Source)
			if err != nil {
				return err
			}
			defer lease.close()
			if core.EnrollmentImageDigest(lease.image) != j.Digest {
				return errors.New("source changed before protected cutover; preserve both images")
			}
			if err = lease.check(); err != nil {
				return err
			}
			if err = h.fence(ctx, j); err != nil {
				return err
			}
			if err = lease.check(); err != nil {
				return errors.New("source lease was lost during cutover; preserve the original for recovery")
			}
			return lease.release()
		case "check":
			if rank(j.Phase) < rank("fencing") {
				return errState
			}
			return h.checkFence(ctx, j)
		case "publish":
			if j.Phase != "publishing" {
				return errState
			}
			if err := custodyinstall.CheckProtectedEnrollmentSigner(owner, id); err != nil {
				return err
			}
			return h.publish(ctx, j)
		case "finish":
			if j.Phase != "activation-requested" && j.Phase != "complete" {
				return errState
			}
			return h.finish(ctx, j)
		default:
			return errState
		}
	})
}

type ProtectedActivation struct {
	Target             custodyinstall.Manifest
	InstanceID, Digest string
	Running, Autostart bool
}

// WithProtectedActivation binds activation to the source fence and the exact
// imported image. A pre-existing matching commit permits an idempotent retry.
func WithProtectedActivation(ctx context.Context, owner int, id, step string, source ProtectedSource, f func(ProtectedActivation) error) error {
	if f == nil {
		return errState
	}
	return withProtectedRuntime(owner, id, source, func(j journal, h *runtimeHost, m custodyinstall.Manifest) error {
		if step == "activate" && j.Phase != "activation-requested" || step == "restart" && j.Phase != "complete" || step != "activate" && step != "restart" {
			return errState
		}
		if err := h.checkFence(ctx, j); err != nil {
			return err
		}
		if _, err := os.Lstat(m.Root + "/enrollment-ready.json"); errors.Is(err, os.ErrNotExist) {
			if j.Phase == "complete" {
				return errState
			}
			if err = h.image(ctx, m, "verify", m.Root+"/app-state", j.Source.ExpectedInstanceID, j.Digest, nil); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			c, err := custodyinstall.ReadEnrollmentCommit(m)
			if err != nil || c.InstanceID != j.Source.ExpectedInstanceID || c.Digest != j.Digest {
				return errState
			}
		}
		active := ProtectedActivation{Target: m, InstanceID: j.Source.ExpectedInstanceID, Digest: j.Digest, Running: j.Source.Running, Autostart: j.Source.Autostart}
		if step == "restart" {
			active.Running, active.Autostart = true, false
		}
		return f(active)
	})
}
