//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"os"
)

func RejectUnstagedApplication(ctx context.Context, m Manifest, id string, sequence uint64, reject func() error, s *custodyinstall.System) error {
	if s == nil {
		return errors.New("ordinary owner authorization required")
	}
	return rejectUnstagedApplication(ctx, liveOps(s), m, id, sequence, reject)
}
func rejectUnstagedApplication(ctx context.Context, o hostOps, m Manifest, id string, sequence uint64, reject func() error) error {
	if ctx.Err() != nil || ownedBy(o, m) != nil || !canonicalHex(id, 16) || sequence == 0 || reject == nil {
		return errors.New("exact unsent Remote dispatch required")
	}
	lock, e := o.lock()
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = verify(ctx, o, m); e != nil {
		return e
	}
	j, e := readApplicationJournal(o)
	if e == nil {
		terminal := j.Status.TargetManifestSHA256
		if j.Status.Phase == "rolled-back" {
			terminal = j.Status.PreviousManifestSHA256
		}
		if j.Status.ID == id || (j.Status.Phase != "completed" && j.Status.Phase != "rolled-back") || j.Status.Sequence >= sequence || terminal != ManifestSHA256(m) {
			return errors.New("Remote installation journal prevents unsent rejection")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return reject()
}

func ApplicationUpdateNeedsSource(owner int, id string) (bool, error) {
	return applicationUpdateNeedsSource(liveOps(nil), owner, id)
}
func applicationUpdateNeedsSource(o hostOps, owner int, id string) (bool, error) {
	if ordinary(o) != nil || o.uid != owner {
		return false, errors.New("existing Remote owner required")
	}
	j, e := readApplicationJournal(o)
	if e != nil || j.Status.ID != id {
		return false, errors.New("exact existing Remote staging journal required")
	}
	if j.Status.Phase != "prepared" {
		return false, nil
	}
	f, e := rootFile(o, j.Previous.Root+"-update-"+id+"/next-core")
	if errors.Is(e, os.ErrNotExist) {
		return true, nil
	}
	if e != nil || f.Mode.Perm() != 0755 || hash(f.Data) != j.Status.EngineSHA256 {
		return false, errors.New("published Remote staging differs from its admitted journal")
	}
	return false, nil
}

// RecoverApplication selects only the existing owner journal and fixed retained
// files. It is not release admission, a new update, DB restore or signing scope.
func RecoverApplication(ctx context.Context, m Manifest, id string, h ApplicationUpdateCallbacks, s *custodyinstall.System) (Manifest, error) {
	if s == nil {
		return Manifest{}, errors.New("owner OS authorization required")
	}
	return recoverApplication(ctx, liveOps(s), m, id, h)
}
func ResumeApplication(ctx context.Context, m Manifest, id string, h ApplicationUpdateCallbacks, s *custodyinstall.System) (Manifest, error) {
	if s == nil {
		return Manifest{}, errors.New("owner OS authorization required")
	}
	return resumeRemoteApplication(ctx, liveOps(s), m, id, h, false)
}
func recoverApplication(ctx context.Context, o hostOps, m Manifest, id string, h ApplicationUpdateCallbacks) (Manifest, error) {
	return resumeRemoteApplication(ctx, o, m, id, h, true)
}
func resumeRemoteApplication(ctx context.Context, o hostOps, m Manifest, id string, h ApplicationUpdateCallbacks, rollback bool) (Manifest, error) {
	if !canonicalHex(id, 16) {
		return Manifest{}, errors.New("one existing Remote recovery identity required")
	}
	if e := ownedBy(o, m); e != nil {
		return Manifest{}, e
	}
	j, e := readApplicationJournal(o)
	if e != nil {
		return Manifest{}, e
	}
	if j.Status.ID != id || rollback && j.Status.Phase == "completed" {
		return Manifest{}, errors.New("exact pending or previously rolled-back Remote operation required")
	}
	c := ApplicationUpdate{ReaderBootstrap: j.Status.ReaderBootstrap, Sequence: j.Status.Sequence, ReleaseSHA256: j.Status.ReleaseSHA256, ID: id, Version: j.ProductVersion, ExpectedManifestSHA256: j.Status.PreviousManifestSHA256, Engine: j.Previous.Root + "-update-" + id + "/next-core", EngineSHA256: j.Status.EngineSHA256}
	return replaceApplication(ctx, o, m, c, h, rollback)
}
