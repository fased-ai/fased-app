package localinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// ApplicationUpdateNeedsSource requests fresh admission only for absent next
// code in a prepared exact transaction. Published mismatches stay refused.
func ApplicationUpdateNeedsSource(paths Paths, id string) (bool, error) {
	p, e := normalize(paths)
	if e != nil {
		return false, e
	}
	j, e := readApplicationJournal(p)
	if e != nil || j.Status.ID != id {
		return false, errors.New("exact existing application staging journal required")
	}
	if j.Status.Phase != "prepared" {
		return false, nil
	}
	missing := false
	for name, want := range map[string]string{engineName: j.Status.EngineSHA256, desktopName: j.Status.DesktopSHA256} {
		if want == "" {
			continue
		}
		got, err := updateFileDigest(filepath.Join(applicationUpdateDirectory(p), id, "next-"+name))
		if errors.Is(err, os.ErrNotExist) {
			missing = true
		} else if err != nil || got != want {
			return false, errors.New("published application staging differs from its admitted journal")
		}
	}
	return missing, nil
}

// RejectUnstagedApplication serializes an unsent rejection with journal
// publication. Core still independently refuses a crossed metadata fence.
func RejectUnstagedApplication(ctx context.Context, paths Paths, id string, sequence uint64, expected string, reject func() error) error {
	if ctx.Err() != nil || !applicationUpdateID(id) || sequence == 0 || reject == nil {
		return errors.New("exact unsent owner dispatch required")
	}
	m, lock, e := locked(paths)
	if e != nil {
		return e
	}
	defer lock.Close()
	if m.Phase != "ready" || ManifestSHA256(m) != expected {
		return errors.New("unsent installation baseline changed")
	}
	if e = verify(m); e != nil {
		return e
	}
	j, e := readApplicationJournal(Paths{Root: m.Root, Data: m.Data})
	if e == nil {
		terminal := j.Status.TargetManifestSHA256
		if j.Status.Phase == "rolled-back" {
			terminal = j.Status.PreviousManifestSHA256
		}
		if j.Status.ID == id || (j.Status.Phase != "completed" && j.Status.Phase != "rolled-back") || j.Status.Sequence >= sequence || terminal != expected {
			return errors.New("an installation journal prevents unsent rejection")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return reject()
}

// RecoverApplication is owner-terminal-only compatible code recovery. Its sole
// selector is the existing opaque operation. No payload/URL/seed/new candidate
// is accepted, and the consumed release floor remains in the original journal.
func RecoverApplication(ctx context.Context, paths Paths, id string, h ApplicationUpdateCallbacks) (Manifest, error) {
	if runtime.GOOS != "linux" {
		return Manifest{}, errors.New("application recovery supports Linux Local only")
	}
	return resumeRetainedApplication(ctx, paths, id, h, applicationServices{stop: stop, start: start}, true)
}

// ResumeApplication resumes only a previously admitted exact transaction.
func ResumeApplication(ctx context.Context, paths Paths, id string, h ApplicationUpdateCallbacks) (Manifest, error) {
	if runtime.GOOS != "linux" {
		return Manifest{}, errors.New("application recovery supports Linux Local only")
	}
	return resumeRetainedApplication(ctx, paths, id, h, applicationServices{stop: stop, start: start}, false)
}
func resumeRetainedApplication(ctx context.Context, paths Paths, id string, h ApplicationUpdateCallbacks, services applicationServices, rollback bool) (Manifest, error) {
	if !applicationUpdateID(id) {
		return Manifest{}, errors.New("one existing application recovery identity required")
	}
	p, e := normalize(paths)
	if e != nil {
		return Manifest{}, e
	}
	j, e := readApplicationJournal(p)
	if e != nil {
		return Manifest{}, e
	}
	if j.Status.ID != id || rollback && j.Status.Phase == "completed" {
		return Manifest{}, errors.New("exact pending or previously rolled-back operation required")
	}
	dir := filepath.Join(applicationUpdateDirectory(p), id)
	c := ApplicationUpdate{ReaderBootstrap: j.Status.ReaderBootstrap, Sequence: j.Status.Sequence, ReleaseSHA256: j.Status.ReleaseSHA256, ID: id, Version: j.Target.Version, ExpectedManifestSHA256: j.Status.PreviousManifestSHA256, Engine: filepath.Join(dir, "next-"+engineName), EngineSHA256: j.Status.EngineSHA256}
	if j.Status.DesktopSHA256 != "" {
		c.Desktop = filepath.Join(dir, "next-"+desktopName)
		c.DesktopSHA256 = j.Status.DesktopSHA256
	}
	return replaceApplication(ctx, p, c, h, services, nil, rollback)
}
