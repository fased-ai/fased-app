//go:build linux

package enrollment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// ProtectedSource is derived by the helper from the existing Remote manifest,
// never from a maintenance request. Local identity comes from its authority.
type ProtectedSource struct {
	Data, Listen, PublicOrigin string
	ApplicationSequence        uint64
	ApplicationReleaseSHA256   string
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
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return j, err
	}
	pending, err := pendingOwnerJournals(entries)
	if err != nil {
		return j, err
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
	if err != nil || len(b) > 16384 || update.StrictJSON(b, &j) != nil {
		return journal{}, errState
	}
	valid := j.valid(a.Owner)
	if !valid && !requireLock && j.Phase == "source-lock-lost" {
		// Read-only Remote status/network recovery may inspect a held lease;
		// mutating controller and activation entries continue to reject it.
		checked := j
		checked.Phase = "fencing"
		valid = checked.valid(a.Owner)
	}
	if !valid {
		return journal{}, errState
	}
	if _, err = verifyRetainedPrepared(dirFD, pending, j, a.Owner); err != nil {
		return journal{}, err
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

// ValidateProtectedPrepare ties acquisition to the original controller and
// its held operation before any release is downloaded or privileged files are
// created. It does not grant authority to change that operation.
func ValidateProtectedPrepare(a maintenance.Authority, q maintenance.CustodyEnrollmentRequest, controlled bool) error {
	j, err := openOwnerEnrollmentJournal(a, true)
	if err != nil {
		return err
	}
	return validateProtectedPrepareJournal(a, j, q, controlled)
}

func validateProtectedPrepareJournal(a maintenance.Authority, j journal, q maintenance.CustodyEnrollmentRequest, controlled bool) error {
	provenance := signer.OwnerKeyProvenance
	if controlled {
		provenance = signer.ControlledCustodyProfile
	}
	if j.Owner != a.Owner || j.Source.Profile != a.Profile || j.Phase != "prepared" || j.Target.ID != "" ||
		j.Mode != signer.OrdinaryCustodyMode || j.Profile != signer.OwnerCustodyProfile || j.Provenance != provenance || q.OperationID != j.ID || q.Network != j.Network || q.Listen != j.Source.Listen ||
		q.PublicOrigin != j.Source.PublicOrigin || q.NativeOwnerAccess != j.NativeOwnerAccess || q.CoreSHA256 != j.CoreHash || q.SignerSHA256 != j.SignerHash ||
		q.Sequence != j.Source.ApplicationSequence || q.ReleaseSHA256 != j.Source.ApplicationReleaseSHA256 || !controlled && q.Sequence == 0 {
		return errors.New("enrollment acquisition differs from the held owner operation")
	}
	return nil
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

func protectedSourceReleaseMatches(j sourceRecord, source ProtectedSource) bool {
	return j.ApplicationSequence == 0 && j.ApplicationReleaseSHA256 == "" || j.ApplicationSequence == source.ApplicationSequence && j.ApplicationReleaseSHA256 == source.ApplicationReleaseSHA256
}

func remoteEnrollmentIntent(j journal, source ProtectedSource, action string) (bool, bool, error) {
	if j.Source.Profile != "remote" || j.Source.Data != source.Data || j.Source.Listen != source.Listen || j.Source.PublicOrigin != source.PublicOrigin || j.Source.UID != source.UID || j.Source.GID != source.GID || !protectedSourceReleaseMatches(j.Source, source) {
		return false, false, errState
	}
	if action == "suspend" && j.Phase != "suspending" && j.Phase != "suspended" || action == "restore" && j.Phase != "suspending" && j.Phase != "suspended" || action != "suspend" && action != "restore" {
		return false, false, errState
	}
	return j.Source.Running, j.Source.Autostart, nil
}

func protectedReservationMatches(j journal, raw []byte) bool {
	want := maintenance.CustodyEnrollmentRequest{OperationID: j.ID, Network: j.Network, Listen: j.Source.Listen, PublicOrigin: j.Source.PublicOrigin, NativeOwnerAccess: j.NativeOwnerAccess, CoreSHA256: j.CoreHash, SignerSHA256: j.SignerHash, Sequence: j.Source.ApplicationSequence, ReleaseSHA256: j.Source.ApplicationReleaseSHA256}
	b, err := json.Marshal(want)
	return err == nil && bytes.Equal(bytes.TrimSpace(raw), b)
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
	reservation, err := update.OpenProtected(update.MaintenanceDirectory(owner, id)+"/enrollment-acquisition-reservation.json", 32<<10, 0644)
	if err != nil {
		return errors.New("original protected enrollment reservation unavailable; preserve it")
	}
	raw, err := io.ReadAll(io.LimitReader(reservation, (32<<10)+1))
	err = errors.Join(err, reservation.Close())
	if err != nil || !protectedReservationMatches(j, raw) {
		return errors.New("original protected enrollment reservation differs; preserve it")
	}
	if a.Profile == "local" {
		if j.Source.UID != a.Owner || j.Source.GID != a.Group || j.Source.PublicOrigin != "" {
			return errState
		}
	} else if a.Profile == "remote" {
		if j.Source.Data != source.Data || j.Source.Listen != source.Listen || j.Source.PublicOrigin != source.PublicOrigin || j.Source.UID != source.UID || j.Source.GID != source.GID || !protectedSourceReleaseMatches(j.Source, source) {
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
		authority, err := openProtectedFenceAuthority(owner, j, m)
		if err != nil {
			return err
		}
		defer authority.dir.Close()
		switch step {
		case "reserve":
			if j.Phase != "fencing" {
				return errState
			}
			return authority.reserve(h, j)
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
			// If the prior fence was interrupted, restore only the exact archived
			// original before reacquiring and comparing a new source snapshot.
			if n, err := realNode(h.pinned(h.base)); err != nil || n != j.Node {
				if _, e := os.Lstat(m.Root + "/enrollment-ready.json"); !errors.Is(e, os.ErrNotExist) {
					return errors.New("a committed target must not restore the original source")
				}
				if _, e := update.ReadMaintenanceRecord(owner); !errors.Is(e, os.ErrNotExist) {
					return errors.New("existing maintenance state must be preserved")
				}
				if err = restoreProtectedFence(h, j, 0, 0, authority); err != nil {
					return err
				}
			}
			// The staged image above was verified as the isolated app UID. Bind
			// the source frame to its exact digest without opening SQLite as root.
			lease, err := h.export(ctx, m, j.Source, j.Digest)
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
			if err = protectedFence(h, j, 0, 0, authority); err != nil {
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
			if err := authority.verifyAt(h, j, h.base); err != nil {
				return err
			}
			return h.checkFence(ctx, j)
		case "publish":
			if j.Phase != "publishing" {
				return errState
			}
			if err := custodyinstall.CheckProtectedEnrollmentSigner(owner, id); err != nil {
				return err
			}
			if err := authority.verifyAt(h, j, h.base); err != nil {
				return err
			}
			return h.publish(ctx, j)
		case "finish":
			if j.Phase != "activation-requested" && j.Phase != "complete" {
				return errState
			}
			c, err := custodyinstall.ReadEnrollmentCommit(m)
			if err != nil || c.InstanceID != j.Source.ExpectedInstanceID || c.Digest != j.Digest {
				return errState
			}
			return finishProtectedFence(h, j, 0, 0, authority)
		default:
			return errState
		}
	})
}

// restoreProtectedFence is a pre-activation rollback, not a new enrollment.
// It uses only descriptors beneath the already pinned parent, checks the exact
// original inode and holds its instance lock while moving it back. The caller
// must reacquire the ordinary exporter and compare the complete digest before
// trying the fence again.
func restoreProtectedFence(h *runtimeHost, j journal, managerUID, managerGID int, authority *protectedFenceAuthority) error {
	if err := h.unchangedParent(); err != nil {
		return err
	}
	parent := int(h.parent.Fd())
	pending := ".enrollment-" + j.ID
	name := h.base
	if _, err := os.Lstat(h.pinned(name)); errors.Is(err, os.ErrNotExist) {
		name = pending
	} else if err != nil {
		return err
	}
	fenceDir, err := openEnrollmentDirectory(parent, name, managerUID, managerGID, 0)
	if err != nil {
		return errors.New("interrupted source fence differs; preserve it")
	}
	defer fenceDir.Close()
	if err = authority.verify(h, j, int(fenceDir.Fd())); err != nil {
		return err
	}
	if err = verifyEnrollmentFenceRecord(int(fenceDir.Fd()), j, managerUID, managerGID); err != nil {
		return err
	}
	original, err := openEnrollmentDirectory(int(fenceDir.Fd()), ".original", managerUID, managerGID, 0700)
	if err != nil {
		return err
	}
	defer original.Close()
	data, err := openEnrollmentDirectory(int(original.Fd()), "data", j.Node.UID, j.Node.GID, 0700)
	if err != nil {
		return errors.New("original source archive unavailable; preserve it")
	}
	defer data.Close()
	var st unix.Stat_t
	if unix.Fstat(int(data.Fd()), &st) != nil || uint64(st.Dev) != j.Node.Device || st.Ino != j.Node.Inode {
		return errors.New("original source archive identity differs; preserve it")
	}
	lockFD, err := unix.Openat(int(data.Fd()), "instance.lock", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	lock := os.NewFile(uintptr(lockFD), "archived-instance-lock")
	defer lock.Close()
	if unix.Fstat(lockFD, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != j.Node.UID || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Size != 0 {
		return errors.New("original instance lock changed; preserve it")
	}
	if err = unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("original instance is still in use; preserve it")
	}
	defer unix.Flock(lockFD, unix.LOCK_UN)
	if !sameEnrollmentEntry(parent, name, int(fenceDir.Fd())) {
		return errState
	}
	if name == h.base {
		if err = unix.Renameat2(parent, h.base, parent, pending, unix.RENAME_NOREPLACE); err != nil {
			return err
		}
		if !sameEnrollmentEntry(parent, pending, int(fenceDir.Fd())) {
			return errState
		}
		if err = unix.Fsync(parent); err != nil {
			return err
		}
	}
	if err = unix.Renameat2(int(original.Fd()), "data", parent, h.base, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	if !sameEnrollmentEntry(parent, h.base, int(data.Fd())) {
		return errState
	}
	return errors.Join(unix.Fsync(int(original.Fd())), unix.Fsync(parent))
}

func openEnrollmentDirectory(parent int, name string, uid, gid int, mode uint32) (*os.File, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "enrollment-directory")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || int(st.Uid) != uid || int(st.Gid) != gid || (mode == 0 && st.Mode&07777 != 0700 && st.Mode&07777 != 0755) || (mode != 0 && st.Mode&07777 != mode) {
		f.Close()
		return nil, errState
	}
	return f, nil
}

func sameEnrollmentEntry(parent int, name string, fd int) bool {
	var entry, held unix.Stat_t
	return unix.Fstatat(parent, name, &entry, unix.AT_SYMLINK_NOFOLLOW) == nil && unix.Fstat(fd, &held) == nil && entry.Dev == held.Dev && entry.Ino == held.Ino && entry.Mode&unix.S_IFMT == unix.S_IFDIR
}

func verifyEnrollmentFenceRecord(dir int, j journal, uid, gid int) error {
	fd, err := unix.Openat(dir, "enrollment.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "original-enrollment-binding")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != uid || int(st.Gid) != gid || st.Mode&07777 != 0644 || st.Nlink != 1 || st.Size <= 0 || st.Size > 16384 {
		return errState
	}
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || !bytes.Equal(bytes.TrimSpace(b), jsonBytes(fenceFor(j))) {
		return errors.New("interrupted source binding differs; preserve it")
	}
	return nil
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
