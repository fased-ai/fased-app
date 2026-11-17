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
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

// Only the existing root-managed helper calls this while its operation lock is
// held. Earlier named Remote acquisition directories may remain; no custody work may be
// adopted from an unclassified directory.
func verifyFreshRemoteEnrollmentInventory(o hostOps, owner int) error {
	if owner <= 0 || o.uid != owner || o.euid != owner {
		return errors.New("original Remote owner required")
	}
	protectedDir := func(path string, mode os.FileMode) error {
		f, err := o.read(path)
		permissionsOK := f.Mode.Perm() == mode
		if mode == 0700 {
			permissionsOK = f.Mode.Perm()&0077 == 0
		}
		if err != nil || !f.Directory || f.UID != 0 || f.GID != 0 || !permissionsOK ||
			f.Mode&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return errors.New("protected Remote directory changed")
		}
		return nil
	}
	root := maintenance.OwnerRoot(owner)
	if err := protectedDir(root, 0755); err != nil {
		return err
	}
	if _, err := o.read(custodyinstall.Root(owner)); !errors.Is(err, os.ErrNotExist) {
		return errors.New("existing custody target must be preserved")
	}
	names, err := o.list(root)
	if err != nil || len(names) > 16 {
		return errors.New("protected Remote inventory unavailable")
	}
	seenAuthority, seenRemote, seenLock := false, false, false
	for _, name := range names {
		switch name {
		case "authority.json":
			f, e := o.read(filepath.Join(root, name))
			if e != nil || !f.Regular || f.UID != 0 || f.GID != 0 || f.Mode.Perm() != 0644 || f.Mode&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
				return errors.New("protected authority changed")
			}
			seenAuthority = true
		case "operation.lock":
			f, e := o.read(filepath.Join(root, name))
			if e != nil || !f.Regular || f.UID != 0 || f.GID != 0 || f.Mode.Perm() != 0600 || f.Mode&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
				return errors.New("protected operation lock changed")
			}
			seenLock = true
		case "remote":
			if err := protectedDir(filepath.Join(root, name), 0700); err != nil {
				return err
			}
			seenRemote = true
		case "tailscale":
			// Bootstrap retains its own protected Tailscale preparation record.
			// It is unrelated to custody acquisition; leave it entirely intact.
			if err := protectedDir(filepath.Join(root, name), 0700); err != nil {
				return err
			}
		case "fased-app":
			app := filepath.Join(root, name)
			if err := protectedDir(app, 0700); err != nil {
				return err
			}
			children, e := o.list(app)
			if e != nil || len(children) != 1 || children[0] != "install" {
				return errors.New("unclassified protected acquisition must be preserved")
			}
			install := filepath.Join(app, "install")
			if err := protectedDir(install, 0700); err != nil {
				return err
			}
			acquisitions, e := o.list(install)
			if e != nil || len(acquisitions) > 1024 {
				return errors.New("protected acquisition inventory unavailable")
			}
			for _, acquisition := range acquisitions {
				if !strings.HasPrefix(acquisition, "verified-") || !update.IDOK(strings.TrimPrefix(acquisition, "verified-")) ||
					protectedDir(filepath.Join(install, acquisition), 0700) != nil {
					return errors.New("unclassified protected acquisition must be preserved")
				}
			}
		default:
			return errors.New("unclassified protected maintenance must be preserved")
		}
	}
	if !seenAuthority || !seenRemote || !seenLock {
		return errors.New("protected Remote authority is incomplete")
	}
	return nil
}

// The caller already holds this journal domain's install lock. A completed
// record is retained, but an interrupted or unclassified write remains held.
func verifyFreshRemoteJournals(o hostOps, m Manifest, dir, instance string, uid int) error {
	f, err := o.read(dir)
	if err != nil || !f.Directory || f.UID != uid || f.Mode.Perm()&0077 != 0 ||
		f.Mode&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return errors.New("Remote journal directory changed; preserve it")
	}
	names, err := o.list(dir)
	if err != nil || len(names) > 1024 {
		return errors.New("Remote journal inventory unavailable")
	}
	for _, name := range names {
		if strings.HasPrefix(name, ".pending-") {
			return errors.New("interrupted Remote journal must be preserved")
		}
	}
	if err := accessIdle(o, m); err != nil {
		return err
	}
	if err := maintenanceIdle(o, m); err != nil {
		return err
	}
	var access *accessJournal
	if b, e := o.readJournal(accessJournalName); e == nil {
		var value accessJournal
		if strict(b, &value) != nil || !accessJournalValid(value) || value.InstanceID != instance {
			return errors.New("Remote access identity changed; preserve it")
		}
		terminal := value.Next
		if value.Phase == "rolled-back" {
			terminal = value.Previous
		}
		terminal.CoreSHA256 = m.CoreSHA256
		terminal.ApplicationSequence = m.ApplicationSequence
		terminal.ApplicationReleaseSHA256 = m.ApplicationReleaseSHA256
		if terminal != m {
			return errors.New("Remote access result differs; preserve it")
		}
		access = &value
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	var application *applicationJournal
	if value, e := readApplicationJournal(o); e == nil {
		if value.Status.Phase != "completed" && value.Status.Phase != "rolled-back" {
			return errors.New("finish Remote update recovery first")
		}
		terminal := value.Target
		if value.Status.Phase == "rolled-back" {
			terminal = value.Previous
		}
		previousOrigin := terminal.PublicOrigin
		terminal.PublicOrigin = m.PublicOrigin
		if terminal != m || previousOrigin != m.PublicOrigin && (access == nil ||
			access.Previous.PublicOrigin != previousOrigin && access.Next.PublicOrigin != previousOrigin) {
			return errors.New("Remote update result differs; preserve it")
		}
		application = &value
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if b, e := o.readJournal("application-maintenance.json"); e == nil {
		var request applicationMaintenanceRequest
		if strict(b, &request) != nil || request.Version != 1 || !update.IDOK(request.ID) || request.InstallationID != m.ID ||
			!canonicalHex(request.PreviousManifestSHA256, 32) {
			return errors.New("unknown Remote maintenance; preserve it")
		}
		switch request.Finished {
		case "completed", "rolled-back":
			if application == nil || application.Status.ID != request.ID || application.Status.Phase != request.Finished || application.Status.PreviousManifestSHA256 != request.PreviousManifestSHA256 {
				return errors.New("Remote maintenance outcome differs; preserve it")
			}
		case "deferred":
			original := request.PreviousManifestSHA256 == ManifestSHA256(m)
			if access != nil {
				original = original || request.PreviousManifestSHA256 == ManifestSHA256(access.Previous) || request.PreviousManifestSHA256 == ManifestSHA256(access.Next)
			}
			if application != nil {
				original = original || request.PreviousManifestSHA256 == ManifestSHA256(application.Previous)
			}
			if !original || application != nil && application.Status.ID == request.ID {
				return errors.New("deferred Remote maintenance differs; preserve it")
			}
		default:
			return errors.New("unfinished Remote maintenance; preserve it")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return nil
}

// Called by the ordinary Remote owner while its existing installation lease is
// held; it does not acquire that lease again or alter the owner's records.
func VerifyPreparedEnrollmentOwner(m Manifest, instance string) error {
	o := liveOps(nil)
	if err := ownedBy(o, m); err != nil {
		return err
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	return verifyFreshRemoteJournals(o, m, filepath.Join(dir, "fased-app-vps-installer"), instance, o.uid)
}

// The lease remains held through fencing; callbacks do not recursively acquire
// it. Manifest/code/unit identity can still be inspected after data is fenced.
func EnrollmentLease(ctx context.Context, m Manifest, s *custodyinstall.System) (io.Closer, error) {
	if s == nil {
		return nil, errors.New("owner OS authorization required")
	}
	o := liveOps(s)
	if _, err := maintenance.ReadAuthority(m.OwnerUID); err == nil {
		if err = ownedBy(o, m); err != nil {
			return nil, err
		}
		l, err := o.lock()
		if err != nil {
			return nil, err
		}
		if _, err = protectedEnrollmentCall(ctx, m, "remote-enrollment-validate"); err != nil {
			_ = l.Close()
			return nil, err
		}
		return l, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return enrollmentLease(ctx, o, m)
}

func EnrollmentStatus(ctx context.Context, m Manifest, s *custodyinstall.System) (ServiceStatus, error) {
	if s == nil {
		return ServiceStatus{}, errors.New("owner OS authorization required")
	}
	if _, err := maintenance.ReadAuthority(m.OwnerUID); err == nil {
		return protectedEnrollmentCall(ctx, m, "remote-enrollment-status")
	} else if !errors.Is(err, os.ErrNotExist) {
		return ServiceStatus{}, err
	}
	return enrollmentStatus(ctx, liveOps(s), m)
}

func protectedEnrollmentCall(ctx context.Context, m Manifest, action string) (ServiceStatus, error) {
	var status ServiceStatus
	if m.OwnerUID != os.Getuid() || os.Getuid() != os.Geteuid() {
		return status, errors.New("Remote installation owner required")
	}
	actual, err := Load(m.OwnerUID)
	if err != nil || actual != m {
		return status, errors.New("Remote enrollment source changed; preserving it")
	}
	b, err := maintenance.Call(ctx, maintenance.Request{Version: 1, Action: action}, nil)
	if err != nil {
		return status, err
	}
	var response maintenance.Response
	if update.StrictJSON(b, &response) != nil || response.Version != 1 || response.Action != action || !response.Complete {
		return status, errors.New("protected Remote enrollment response invalid")
	}
	if action == "remote-enrollment-status" && update.StrictJSON(response.Payload, &status) != nil {
		return status, errors.New("protected Remote enrollment status invalid")
	}
	return status, nil
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
	if _, err := maintenance.ReadAuthority(m.OwnerUID); err == nil {
		_, err = protectedEnrollmentCall(ctx, m, "remote-enrollment-validate")
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
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
	Profile                  string `json:"profile"`
	Data                     string `json:"data"`
	Listen                   string `json:"listen"`
	PublicOrigin             string `json:"publicOrigin,omitempty"`
	ExpectedInstanceID       string `json:"expectedInstanceId"`
	UID                      int    `json:"uid"`
	GID                      int    `json:"gid"`
	Running                  bool   `json:"running"`
	Autostart                bool   `json:"autostart"`
	ApplicationSequence      uint64 `json:"applicationSequence,omitempty"`
	ApplicationReleaseSHA256 string `json:"applicationReleaseSha256,omitempty"`
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
	// Zero fields are the already-shipped marker form. A marker carrying a
	// release identity must match the original protected Remote manifest.
	releaseMatches := s.ApplicationSequence == 0 && s.ApplicationReleaseSHA256 == "" || s.ApplicationSequence == m.ApplicationSequence && s.ApplicationSequence != 0 && s.ApplicationReleaseSHA256 == m.ApplicationReleaseSHA256 && canonicalHex(s.ApplicationReleaseSHA256, 32)
	return releaseMatches && f.Version == 1 && canonicalHex(f.ID, 16) && f.Owner == m.OwnerUID && canonicalHex(f.Digest, 32) && s.Profile == "remote" && s.Data == m.Data && s.Listen == m.Listen && s.PublicOrigin == m.PublicOrigin && s.UID == m.AppUID && s.GID == m.AppGID && enrollmentInstanceID(s.ExpectedInstanceID) && f.Node.Inode > 0 && f.Node.UID == m.AppUID && f.Node.GID == m.AppGID && f.Node.Mode == 0700 && t.Enrollment && t.Owner == m.OwnerUID && t.Location == "remote" && t.Listen == m.Listen && t.PublicOrigin == m.PublicOrigin && t.App.UID != m.AppUID && t.Signer.UID != m.AppUID
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
	var f enrollmentFenceBinding
	if o.enrollmentJournal == nil || o.custodyTarget == nil {
		return f, "", errors.New("no pending enrollment binding")
	}
	b, err := o.enrollmentJournal()
	if err != nil {
		return f, "", errors.New("pending enrollment journal unavailable")
	}
	projection, phase, err := enrollment.RemoteJournalFence(b, m.OwnerUID)
	if err != nil || update.StrictJSON(projection, &f) != nil || !boundEnrollment(f, m) {
		return enrollmentFenceBinding{}, "", errors.New("pending enrollment journal differs from this Remote installation")
	}
	switch phase {
	case "fencing", "fenced", "publishing", "committed", "activation-requested", "complete", "source-lock-lost":
	default:
		return enrollmentFenceBinding{}, "", errors.New("enrollment has not begun source fencing")
	}
	target, err := o.custodyTarget(m.OwnerUID)
	if err != nil || custodyinstall.EnrollmentIdentity(target) != custodyinstall.EnrollmentIdentity(f.Target) {
		return enrollmentFenceBinding{}, "", errors.New("pending enrollment target changed")
	}
	sibling := ".enrollment-" + f.ID
	_, se := o.read(m.Root + "/" + sibling)
	data, de := o.read(m.Data)
	if de == nil && data.Directory && data.UID == 0 && data.GID == 0 && data.Mode.Perm() == 0755 && data.Mode&os.ModeSymlink == 0 {
		if !errors.Is(se, os.ErrNotExist) {
			return enrollmentFenceBinding{}, "", errors.New("both pending and published source fences exist")
		}
		if _, re := o.read(m.Data + "/ready.json"); !errors.Is(re, os.ErrNotExist) {
			return enrollmentFenceBinding{}, "", errors.New("completed enrollment must pass its public target binding")
		}
		if err = verifyEnrollmentFence(o, m.Data, f, true); err != nil {
			return enrollmentFenceBinding{}, "", err
		}
		return f, "", nil
	}
	if phase != "fencing" || se != nil {
		return enrollmentFenceBinding{}, "", errors.New("source fence is absent or foreign")
	}
	if de != nil && !errors.Is(de, os.ErrNotExist) {
		return enrollmentFenceBinding{}, "", de
	}
	if de == nil && (checkDirectory(o, m.Data, m.AppUID, m.AppGID, 0700) != nil) {
		return enrollmentFenceBinding{}, "", errors.New("source data ownership changed")
	}
	if err = verifyEnrollmentFence(o, m.Root+"/"+sibling, f, false); err != nil {
		return enrollmentFenceBinding{}, "", err
	}
	return f, sibling, nil
}

func verifyEnrollmentFence(o hostOps, path string, f enrollmentFenceBinding, published bool) error {
	if err := checkDirectory(o, path, 0, 0, 0755); err != nil {
		if published || checkDirectory(o, path, 0, 0, 0700) != nil {
			return err
		}
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
			if e != nil || name == "enrollment.json" && b.Mode.Perm() != 0644 || name != "enrollment.json" && b.Mode.Perm()&^0644 != 0 || !bytes.HasPrefix(expected, b.Data) || name == "enrollment.json" && !bytes.Equal(expected, b.Data) {
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
	if s == nil {
		return errors.New("owner OS authorization required")
	}
	if _, err := maintenance.ReadAuthority(m.OwnerUID); err == nil {
		_, err = protectedEnrollmentCall(ctx, m, "remote-enrollment-suspend")
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if e := ValidateEnrollment(ctx, m, s); e != nil {
		return e
	}
	_, e := s.Run(ctx, nil, "/usr/bin/systemctl", "disable", "--now", "--", m.Unit)
	return e
}
func RestoreEnrollment(ctx context.Context, m Manifest, running, autostart bool, s *custodyinstall.System) error {
	if s == nil {
		return errors.New("owner OS authorization required")
	}
	if _, err := maintenance.ReadAuthority(m.OwnerUID); err == nil {
		// The helper independently reads the original journal's saved intent.
		_, err = protectedEnrollmentCall(ctx, m, "remote-enrollment-restore")
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
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
