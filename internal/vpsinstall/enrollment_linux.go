//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/update"
)

// The lease remains held through fencing; callbacks do not recursively acquire
// it. Manifest/code/unit identity can still be inspected after data is fenced.
func EnrollmentLease(ctx context.Context, m Manifest, s *custodyinstall.System) (io.Closer, error) {
	if s == nil {
		return nil, errors.New("owner OS authorization required")
	}
	o := liveOps(s)
	return enrollmentLease(ctx, o, m)
}

func EnrollmentStatus(ctx context.Context, m Manifest, s *custodyinstall.System) (ServiceStatus, error) {
	if s == nil {
		return ServiceStatus{}, errors.New("owner OS authorization required")
	}
	return enrollmentStatus(ctx, liveOps(s), m)
}
func enrollmentStatus(ctx context.Context, o hostOps, m Manifest) (ServiceStatus, error) {
	if err := verifyFiles(o, m); err == nil {
		return status(ctx, o, m)
	}
	result := ServiceStatus{Installed: true, Unit: m.Unit}
	if err := ownedBy(o, m); err != nil {
		return result, err
	}
	if err := verifyEnrollmentFiles(o, m, true); err != nil {
		return result, err
	}
	if err := verifyAccount(ctx, o, m); err != nil {
		return result, err
	}
	if err := loaded(ctx, o, m); err != nil {
		return result, err
	}
	// A source fence is installation metadata, never evidence that either
	// runtime is active or authenticated. Run restores its saved intent.
	result.State = "enrollment-pending"
	if _, err := committedEnrollment(o, m); err == nil {
		result.State = "enrolled"
	}
	return result, nil
}
func enrollmentLease(ctx context.Context, o hostOps, m Manifest) (io.Closer, error) {
	if e := ownedBy(o, m); e != nil {
		return nil, e
	}
	l, e := o.lock()
	if e != nil {
		return nil, e
	}
	if e = verifyEnrollmentFiles(o, m, true); e == nil {
		e = verifyAccount(ctx, o, m)
	}
	if e == nil {
		e = loaded(ctx, o, m)
	}
	if e == nil {
		e = enrollmentNetworkSettled(ctx, o, m)
	}
	if e != nil {
		l.Close()
		return nil, e
	}
	return l, nil
}
func ValidateEnrollment(ctx context.Context, m Manifest, s *custodyinstall.System) error {
	if s == nil {
		return errors.New("owner OS authorization required")
	}
	o := liveOps(s)
	if e := verify(ctx, o, m); e != nil {
		return e
	}
	if e := enrollmentNetworkSettled(ctx, o, m); e != nil {
		return e
	}
	return loaded(ctx, o, m)
}

type enrollmentSourceBinding struct {
	Profile            string `json:"profile"`
	Data               string `json:"data"`
	Listen             string `json:"listen"`
	PublicOrigin       string `json:"publicOrigin,omitempty"`
	ExpectedInstanceID string `json:"expectedInstanceId"`
	UID                int    `json:"uid"`
	GID                int    `json:"gid"`
	Running            bool   `json:"running"`
	Autostart          bool   `json:"autostart"`
}
type enrollmentNodeBinding struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	Mode   uint32 `json:"mode"`
}
type enrollmentFenceBinding struct {
	Version int                     `json:"version"`
	ID      string                  `json:"id"`
	Owner   int                     `json:"owner"`
	Source  enrollmentSourceBinding `json:"source"`
	Node    enrollmentNodeBinding   `json:"node"`
	Digest  string                  `json:"digest"`
	Target  custodyinstall.Manifest `json:"target"`
}
type enrollmentJournalBinding struct {
	enrollmentFenceBinding
	Phase      string `json:"phase"`
	CoreHash   string `json:"coreHash"`
	SignerHash string `json:"signerHash"`
	Network    string `json:"network"`
}

func bindEnrollmentOps(o *hostOps) {
	o.enrolledTarget = enrollment.EnrolledTarget
	o.custodyTarget = func(uid int) (custodyinstall.Manifest, error) {
		m, err := custodyinstall.Load(uid)
		if err == nil {
			err = custodyinstall.Verify(m)
		}
		return m, err
	}
	o.enrollmentJournal = func() ([]byte, error) {
		dir, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, "fased-app-enrollment", "state.json")
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || int(st.Uid) != os.Getuid() || st.Nlink != 1 || info.Size() > 16384 {
			return nil, errors.New("invalid owner enrollment journal")
		}
		return os.ReadFile(path)
	}
}

func enrollmentInstanceID(id string) bool {
	b, e := base64.RawURLEncoding.DecodeString(id)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == id
}

func boundEnrollment(f enrollmentFenceBinding, m Manifest) bool {
	s, t := f.Source, f.Target
	return f.Version == 1 && canonicalHex(f.ID, 16) && f.Owner == m.OwnerUID && canonicalHex(f.Digest, 32) && s.Profile == "remote" && s.Data == m.Data && s.Listen == m.Listen && s.PublicOrigin == m.PublicOrigin && s.UID == m.AppUID && s.GID == m.AppGID && enrollmentInstanceID(s.ExpectedInstanceID) && f.Node.Inode > 0 && f.Node.UID == m.AppUID && f.Node.GID == m.AppGID && f.Node.Mode == 0700 && t.Enrollment && t.Owner == m.OwnerUID && t.Location == "remote" && t.Listen == m.Listen && t.PublicOrigin == m.PublicOrigin && t.App.UID != m.AppUID && t.Signer.UID != m.AppUID
}

func committedEnrollment(o hostOps, m Manifest) (enrollmentFenceBinding, error) {
	var f enrollmentFenceBinding
	if o.enrolledTarget == nil {
		return f, errors.New("no completed enrollment binding")
	}
	target, err := o.enrolledTarget(m.Data, m.OwnerUID)
	if err != nil {
		return f, err
	}
	if err = checkDirectory(o, m.Data, 0, 0, 0755); err != nil {
		return f, err
	}
	b, err := rootFile(o, m.Data+"/enrollment.json")
	if err != nil || update.StrictJSON(b.Data, &f) != nil || !boundEnrollment(f, m) || custodyinstall.EnrollmentIdentity(f.Target) != custodyinstall.EnrollmentIdentity(target) {
		return enrollmentFenceBinding{}, errors.New("enrolled target does not bind this original Remote installation")
	}
	return f, nil
}

func pendingEnrollment(o hostOps, m Manifest) (enrollmentFenceBinding, string, error) {
	var j enrollmentJournalBinding
	if o.enrollmentJournal == nil || o.custodyTarget == nil {
		return j.enrollmentFenceBinding, "", errors.New("no pending enrollment binding")
	}
	b, err := o.enrollmentJournal()
	if err != nil || len(b) > 16384 || update.StrictJSON(b, &j) != nil || !boundEnrollment(j.enrollmentFenceBinding, m) || j.CoreHash != j.Target.CoreHash || j.SignerHash != j.Target.SignerHash || j.Network != j.Target.Network {
		return enrollmentFenceBinding{}, "", errors.New("pending enrollment journal differs from this Remote installation")
	}
	switch j.Phase {
	case "fencing", "fenced", "publishing", "committed", "activation-requested", "complete", "source-lock-lost":
	default:
		return enrollmentFenceBinding{}, "", errors.New("enrollment has not begun source fencing")
	}
	target, err := o.custodyTarget(m.OwnerUID)
	if err != nil || custodyinstall.EnrollmentIdentity(target) != custodyinstall.EnrollmentIdentity(j.Target) {
		return enrollmentFenceBinding{}, "", errors.New("pending enrollment target changed")
	}
	sibling := ".enrollment-" + j.ID
	_, se := o.read(m.Root + "/" + sibling)
	data, de := o.read(m.Data)
	if de == nil && data.Directory && data.UID == 0 && data.GID == 0 && data.Mode.Perm() == 0755 && data.Mode&os.ModeSymlink == 0 {
		if !errors.Is(se, os.ErrNotExist) {
			return enrollmentFenceBinding{}, "", errors.New("both pending and published source fences exist")
		}
		if _, re := o.read(m.Data + "/ready.json"); !errors.Is(re, os.ErrNotExist) {
			return enrollmentFenceBinding{}, "", errors.New("completed enrollment must pass its public target binding")
		}
		if err = verifyEnrollmentFence(o, m.Data, j.enrollmentFenceBinding, true); err != nil {
			return enrollmentFenceBinding{}, "", err
		}
		return j.enrollmentFenceBinding, "", nil
	}
	if j.Phase != "fencing" || se != nil {
		return enrollmentFenceBinding{}, "", errors.New("source fence is absent or foreign")
	}
	if de != nil && !errors.Is(de, os.ErrNotExist) {
		return enrollmentFenceBinding{}, "", de
	}
	if de == nil && (checkDirectory(o, m.Data, m.AppUID, m.AppGID, 0700) != nil) {
		return enrollmentFenceBinding{}, "", errors.New("source data ownership changed")
	}
	if err = verifyEnrollmentFence(o, m.Root+"/"+sibling, j.enrollmentFenceBinding, false); err != nil {
		return enrollmentFenceBinding{}, "", err
	}
	return j.enrollmentFenceBinding, sibling, nil
}

func verifyEnrollmentFence(o hostOps, path string, f enrollmentFenceBinding, published bool) error {
	if err := checkDirectory(o, path, 0, 0, 0755); err != nil {
		return err
	}
	names, err := o.list(path)
	if err != nil || len(names) > 5 {
		return errors.New("unclassified source fence")
	}
	marker, original := false, false
	expected := encoded(f)
	for _, name := range names {
		switch name {
		case ".original":
			if checkDirectory(o, path+"/.original", 0, 0, 0700) != nil {
				return errors.New("source archive directory changed")
			}
			original = true
		case "enrollment.json", ".enrollment.json-" + f.ID + ".pending":
			b, e := rootFile(o, path+"/"+name)
			if e != nil || b.Mode.Perm() != 0644 || !bytes.HasPrefix(expected, b.Data) || name == "enrollment.json" && !bytes.Equal(expected, b.Data) {
				return errors.New("source fence binding changed")
			}
			marker = marker || name == "enrollment.json"
		default:
			return errors.New("unclassified source fence file")
		}
	}
	if original && !marker || published && !marker {
		return errors.New("source fence binding is incomplete")
	}
	return nil
}

func verifyEnrollmentFiles(o hostOps, m Manifest, allowPending bool) error {
	if err := verifyImmutableFiles(o, m); err != nil {
		return err
	}
	if checkDirectory(o, m.Data, m.AppUID, m.AppGID, 0700) == nil && verifyRootInventory(o, m, "") == nil {
		return nil
	}
	if _, err := committedEnrollment(o, m); err == nil {
		return verifyRootInventory(o, m, "")
	}
	if allowPending {
		_, sibling, err := pendingEnrollment(o, m)
		if err == nil {
			return verifyRootInventory(o, m, sibling)
		}
	}
	return errors.New("original Remote data or its exact enrollment binding is required")
}

func verifyNetworkInstallation(ctx context.Context, o hostOps, m Manifest, allowPending bool) error {
	if err := ownedBy(o, m); err != nil {
		return err
	}
	if err := verifyEnrollmentFiles(o, m, allowPending); err != nil {
		return err
	}
	return verifyAccount(ctx, o, m)
}

func enrollmentNetworkSettled(ctx context.Context, o hostOps, m Manifest) error {
	return enrollmentNetworkSettledAt(ctx, o, m, time.Now)
}
func enrollmentNetworkSettledAt(ctx context.Context, o hostOps, m Manifest, now func() time.Time) error {
	n := networkController{ops: o, now: now}
	a, err := n.load(m)
	if errors.Is(err, os.ErrNotExist) && a.ID == "" {
		return nil
	}
	if err != nil {
		return errors.New("resolve the pending Remote network attempt before enrollment")
	}
	status, err := n.state(ctx, m, a)
	if err != nil || status.Phase != "committed" && status.Phase != "rolled-back" {
		return errors.New("finish or roll back Remote network lockdown before enrollment")
	}
	return nil
}
func EnrollmentAutostart(m Manifest) (bool, error) {
	o := liveOps(nil)
	if e := ownedBy(o, m); e != nil {
		return false, e
	}
	if e := enableLink(o, m); e != nil {
		return false, e
	}
	_, e := o.read("/etc/systemd/system/multi-user.target.wants/" + m.Unit)
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	return e == nil, e
}
func SuspendEnrollment(ctx context.Context, m Manifest, s *custodyinstall.System) error {
	if e := ValidateEnrollment(ctx, m, s); e != nil {
		return e
	}
	_, e := s.Run(ctx, nil, "/usr/bin/systemctl", "disable", "--now", "--", m.Unit)
	return e
}
func RestoreEnrollment(ctx context.Context, m Manifest, running, autostart bool, s *custodyinstall.System) error {
	if e := ValidateEnrollment(ctx, m, s); e != nil {
		return e
	}
	action := "disable"
	if autostart {
		action = "enable"
	}
	if _, e := s.Run(ctx, nil, "/usr/bin/systemctl", action, "--", m.Unit); e != nil {
		return e
	}
	if running {
		_, e := s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", m.Unit)
		return e
	}
	return nil
}
