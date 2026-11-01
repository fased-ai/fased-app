//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

type applicationJournal struct {
	Status         ApplicationUpdateStatus `json:"status"`
	ProductVersion string                  `json:"productVersion"`
	Previous       Manifest                `json:"previous"`
	Target         Manifest                `json:"target"`
}

func encodedApplicationManifest(m Manifest) []byte { return encoded(m) }

const applicationJournalName = "application-update.json"

func applicationJournalValid(j applicationJournal, owner int) bool {
	s := j.Status
	if s.Sequence == 0 || !canonicalHex(s.ReleaseSHA256, 32) || j.Target.ApplicationSequence != s.Sequence || j.Target.ApplicationReleaseSHA256 != s.ReleaseSHA256 || s.Version != 1 || !canonicalHex(s.ID, 16) || j.ProductVersion == "" || j.Previous.validate() != nil || j.Target.validate() != nil || j.Previous.OwnerUID != owner || s.PreviousManifestSHA256 != ManifestSHA256(j.Previous) || s.TargetManifestSHA256 != ManifestSHA256(j.Target) || s.EngineSHA256 != j.Target.CoreSHA256 || s.DesktopSHA256 != "" {
		return false
	}
	previous := j.Previous
	previous.CoreSHA256 = j.Target.CoreSHA256
	previous.ApplicationSequence = j.Target.ApplicationSequence
	previous.ApplicationReleaseSHA256 = j.Target.ApplicationReleaseSHA256
	if previous != j.Target {
		return false
	}
	switch s.Phase {
	case "prepared", "applying", "recovery", "healthy-held", "rolled-back-held", "completed", "rolled-back":
		return true
	}
	return false
}
func readApplicationJournal(o hostOps) (applicationJournal, error) {
	b, e := o.readJournal(applicationJournalName)
	if e != nil {
		return applicationJournal{}, e
	}
	var j applicationJournal
	if len(b) > 64*1024 || strict(b, &j) != nil || !applicationJournalValid(j, o.uid) {
		return j, errors.New("unknown Remote application update journal; preserve originals")
	}
	return j, nil
}
func ApplicationUpdateState(owner int) (ApplicationUpdateStatus, error) {
	o := liveOps(nil)
	if e := ordinary(o); e != nil {
		return ApplicationUpdateStatus{}, e
	}
	if owner != o.uid {
		return ApplicationUpdateStatus{}, errors.New("Remote installation owner required")
	}
	j, e := readApplicationJournal(o)
	return j.Status, e
}
func ReplaceApplication(ctx context.Context, m Manifest, c ApplicationUpdate, h ApplicationUpdateCallbacks, s *custodyinstall.System) (Manifest, error) {
	if s == nil {
		return Manifest{}, errors.New("owner OS authorization required")
	}
	return replaceApplication(ctx, liveOps(s), m, c, h, false)
}
func RollbackApplication(ctx context.Context, m Manifest, c ApplicationUpdate, h ApplicationUpdateCallbacks, s *custodyinstall.System) (Manifest, error) {
	if s == nil {
		return Manifest{}, errors.New("owner OS authorization required")
	}
	return replaceApplication(ctx, liveOps(s), m, c, h, true)
}
func applicationCandidateValid(c ApplicationUpdate) bool {
	return c.Sequence > 0 && canonicalHex(c.ReleaseSHA256, 32) && canonicalHex(c.ID, 16) && canonicalHex(c.ExpectedManifestSHA256, 32) && canonicalHex(c.EngineSHA256, 32) && c.Desktop == "" && c.DesktopSHA256 == "" && c.Version != "" && len(c.Version) <= 128 && !strings.ContainsAny(c.Version, "/\\\x00\r\n\t ") && filepath.IsAbs(c.Engine) && filepath.Clean(c.Engine) == c.Engine
}
func applicationCallbacksValid(h ApplicationUpdateCallbacks) bool {
	return h.BeforeStop != nil && h.Health != nil && h.RollbackAllowed != nil && h.Complete != nil
}
func checkUpdateRoot(ctx context.Context, o hostOps, j applicationJournal) error {
	m := j.Previous
	if e := ownedBy(o, m); e != nil {
		return e
	}
	if e := checkDirectory(o, m.Root, 0, 0, 0755); e != nil {
		return e
	}
	if e := checkDirectory(o, m.Data, m.AppUID, m.AppGID, 0700); e != nil {
		return e
	}
	if e := verifyRootInventory(o, m, ""); e != nil {
		return e
	}
	if e := verifyAccount(ctx, o, m); e != nil {
		return e
	}
	if e := loaded(ctx, o, m); e != nil {
		return e
	}
	for _, v := range []struct {
		path      string
		old, next []byte
		mode      os.FileMode
	}{{m.Config, encoded(j.Previous), encoded(j.Target), 0644}, {m.Root + "/intent.json", encoded(intentFor(j.Previous)), encoded(intentFor(j.Target)), 0644}, {m.Root + "/" + m.Unit, unit(m), unit(m), 0644}} {
		f, e := rootFile(o, v.path)
		if e != nil || (!known(f, v.old, v.mode) && !known(f, v.next, v.mode)) {
			return errors.New("Remote update fixed metadata changed")
		}
	}
	f, e := rootFile(o, m.Core)
	if e != nil || f.Mode.Perm() != 0755 || (hash(f.Data) != j.Previous.CoreSHA256 && hash(f.Data) != j.Target.CoreSHA256) {
		return errors.New("Remote update executable changed")
	}
	marker, e := o.read(m.Root + "/app-readonly-probe")
	if e != nil || !marker.Regular || marker.UID != m.AppUID || marker.GID != m.AppGID || marker.Mode.Perm() != 0600 || len(marker.Data) != 0 {
		return errors.New("Remote runtime marker changed")
	}
	// Any source enrollment fence belongs to the separate custody transition.
	if o.enrollmentJournal != nil {
		b, err := o.enrollmentJournal()
		if err == nil {
			var r enrollmentJournalBinding
			if strict(b, &r) != nil {
				return errors.New("unknown enrollment journal; app-only update refused")
			}
			if r.Source.Data == m.Data {
				return errors.New("Remote custody enrollment exists; app-only update refused")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.New("Remote custody enrollment cannot be excluded")
		}
	}
	return nil
}
func stageRemoteUpdate(ctx context.Context, o hostOps, j applicationJournal, c ApplicationUpdate) (string, error) {
	m := j.Previous
	dir := m.Root + "-update-" + c.ID
	if e := directory(ctx, o, dir, 0, 0, 0755); e != nil {
		return "", e
	}
	source, e := o.read(c.Engine)
	if e != nil || !source.Regular || len(source.Data) == 0 || len(source.Data) > 64<<20 || hash(source.Data) != c.EngineSHA256 {
		return "", errors.New("Remote artifact differs from authenticated release")
	}
	for _, v := range []struct {
		name string
		b    []byte
		mode os.FileMode
	}{{"next-core", source.Data, 0755}, {"previous-install.json", encoded(m), 0644}, {"next-install.json", encoded(j.Target), 0644}, {"previous-intent.json", encoded(intentFor(m)), 0644}, {"next-intent.json", encoded(intentFor(j.Target)), 0644}} {
		if e = publishApplicationStage(ctx, o, dir+"/"+v.name, v.b, v.mode); e != nil {
			return "", e
		}
	}
	oldPath := dir + "/previous-core"
	if f, e := rootFile(o, oldPath); errors.Is(e, os.ErrNotExist) {
		old, e := rootFile(o, m.Core)
		if e != nil || hash(old.Data) != m.CoreSHA256 {
			return "", errors.New("previous Remote core cannot be backed up")
		}
		if e = publishApplicationStage(ctx, o, oldPath, old.Data, 0755); e != nil {
			return "", e
		}
	} else if e != nil || f.Mode.Perm() != 0755 || hash(f.Data) != m.CoreSHA256 {
		return "", errors.New("previous Remote backup changed")
	}
	if e = stageApplicationOwnerAdmission(ctx, o, j, dir); e != nil {
		return "", e
	}
	return dir, nil
}

// A retry publishes only through its fresh, exclusively owned pending name.
// Old partials remain intact; publish still refuses a foreign final artifact.
func publishApplicationStage(ctx context.Context, o hostOps, target string, b []byte, mode os.FileMode) error {
	return publish(ctx, o, target, update.NewProbe(), b, mode)
}
func swapRemoteFile(ctx context.Context, o hostOps, dir, id, target, source string, previous, next []byte, mode os.FileMode) error {
	current, e := rootFile(o, target)
	if e != nil {
		return e
	}
	if known(current, next, mode) {
		return nil
	}
	if !known(current, previous, mode) {
		return errors.New("Remote replacement destination changed")
	}
	staged, e := rootFile(o, source)
	if e != nil || !known(staged, next, mode) {
		return errors.New("Remote staged artifact changed")
	}
	replacement := dir + "/replacement-" + filepath.Base(target)
	if e = publish(ctx, o, replacement, id, next, mode); e != nil {
		return e
	}
	if _, e = o.run(ctx, nil, "/usr/bin/mv", "--force", "--no-target-directory", "--", replacement, target); e != nil {
		return e
	}
	if _, e = o.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(target)); e != nil {
		return e
	}
	actual, e := rootFile(o, target)
	if e != nil || !known(actual, next, mode) {
		return errors.New("Remote replacement failed verification")
	}
	return nil
}
func replaceApplication(ctx context.Context, o hostOps, m Manifest, c ApplicationUpdate, h ApplicationUpdateCallbacks, rollback bool) (Manifest, error) {
	if e := ctx.Err(); e != nil {
		return Manifest{}, e
	}
	if e := ownedBy(o, m); e != nil {
		return Manifest{}, e
	}
	if !applicationCandidateValid(c) || !applicationCallbacksValid(h) {
		return Manifest{}, errors.New("complete authenticated app-only update and metadata/health callbacks required")
	}
	l, e := o.lock()
	if e != nil {
		return Manifest{}, e
	}
	defer l.Close()
	if e := accessIdle(o, m); e != nil {
		return Manifest{}, e
	}
	j, e := readApplicationJournal(o)
	if e == nil && j.Status.ID != c.ID {
		if c.Sequence <= j.Status.Sequence {
			return Manifest{}, errors.New("Remote application release sequence was already consumed")
		}
		if j.Status.Phase != "completed" && j.Status.Phase != "rolled-back" {
			return Manifest{}, errors.New("another Remote update needs recovery")
		}
		e = os.ErrNotExist
	}
	if errors.Is(e, os.ErrNotExist) {
		if c.ReaderBootstrap != (h.AfterStop != nil) {
			return Manifest{}, errors.New("Remote reader-bootstrap hook classification required before staging")
		}
		if rollback {
			return Manifest{}, errors.New("no matching Remote update to roll back")
		}
		if c.Sequence <= m.ApplicationSequence {
			return Manifest{}, errors.New("Remote release sequence rollback refused")
		}
		if ManifestSHA256(m) != c.ExpectedManifestSHA256 {
			return Manifest{}, errors.New("Remote manifest differs from reviewed update")
		}
		if e = verify(ctx, o, m); e != nil {
			return Manifest{}, e
		}
		if h.BeforeStage != nil {
			if e = h.BeforeStage(ctx); e != nil {
				return Manifest{}, e
			}
		}
		next := m
		next.CoreSHA256 = c.EngineSHA256
		next.ApplicationSequence = c.Sequence
		next.ApplicationReleaseSHA256 = c.ReleaseSHA256
		j = applicationJournal{Previous: m, Target: next, ProductVersion: c.Version, Status: ApplicationUpdateStatus{ReaderBootstrap: c.ReaderBootstrap, Sequence: c.Sequence, ReleaseSHA256: c.ReleaseSHA256, Version: 1, ID: c.ID, Phase: "prepared", PreviousManifestSHA256: ManifestSHA256(m), TargetManifestSHA256: ManifestSHA256(next), EngineSHA256: c.EngineSHA256}}
		if e = o.writeJournal(applicationJournalName, j); e != nil {
			return Manifest{}, e
		}
	} else if e != nil {
		return Manifest{}, e
	}
	if j.Status.ReaderBootstrap != c.ReaderBootstrap || j.Status.Sequence != c.Sequence || j.Status.ReleaseSHA256 != c.ReleaseSHA256 || j.Status.ID != c.ID || j.Status.PreviousManifestSHA256 != c.ExpectedManifestSHA256 || j.ProductVersion != c.Version || j.Status.EngineSHA256 != c.EngineSHA256 || (m != j.Previous && m != j.Target) {
		return Manifest{}, errors.New("Remote update identity changed; preserve original transaction")
	}
	if j.Status.Phase == "healthy-held" || j.Status.Phase == "rolled-back-held" {
		return completeHeldRemoteApplication(ctx, o, j, h)
	}
	if j.Status.Phase == "completed" || j.Status.Phase == "rolled-back" {
		target := j.Target
		if j.Status.Phase == "rolled-back" {
			target = j.Previous
		}
		return target, verify(ctx, o, target)
	}
	if c.ReaderBootstrap != (h.AfterStop != nil) {
		return Manifest{}, errors.New("Remote reader-bootstrap hook differs from original transaction")
	}
	if e = checkUpdateRoot(ctx, o, j); e != nil {
		return Manifest{}, e
	}
	dir, e := stageRemoteUpdate(ctx, o, j, c)
	if e != nil {
		return Manifest{}, e
	}
	if e = h.BeforeStop(ctx, j.Previous, j.Target); e != nil {
		return Manifest{}, e
	}
	if rollback {
		return rollbackRemoteApplication(ctx, o, j, dir, h)
	}
	j.Status.Phase = "applying"
	if e = o.writeJournal(applicationJournalName, j); e != nil {
		return Manifest{}, e
	}
	if _, e = o.run(ctx, nil, "/usr/bin/systemctl", "stop", "--", m.Unit); e != nil {
		return Manifest{}, e
	}
	if h.AfterStop != nil {
		if e = h.AfterStop(ctx, j.Previous, j.Target); e != nil {
			return Manifest{}, e
		}
	}
	if e = replaceRemoteGeneration(ctx, o, j, dir, false); e != nil {
		return Manifest{}, e
	}
	healthErr := start(ctx, o, j.Target)
	if healthErr == nil {
		healthErr = h.Health(ctx, j.Target)
	}
	if healthErr != nil {
		j.Status.Phase = "recovery"
		if e = o.writeJournal(applicationJournalName, j); e != nil {
			return Manifest{}, e
		}
		if ctx.Err() != nil {
			return j.Target, ctx.Err()
		}
		previous, e := rollbackRemoteApplication(ctx, o, j, dir, h)
		if e != nil {
			return previous, fmt.Errorf("Remote target health failed; recovery held: %w", e)
		}
		return previous, fmt.Errorf("Remote target health failed; previous compatible code restored: %w", healthErr)
	}
	j.Status.Phase = "healthy-held"
	if e = o.writeJournal(applicationJournalName, j); e != nil {
		return j.Target, e
	}
	return completeHeldRemoteApplication(ctx, o, j, h)
}
func replaceRemoteGeneration(ctx context.Context, o hostOps, j applicationJournal, dir string, rollback bool) error {
	old, next := j.Previous, j.Target
	from, to := "previous-", "next-"
	if rollback {
		old, next = next, old
		from, to = to, from
	}
	before, e := rootFile(o, dir+"/"+from+"core")
	if e != nil || before.Mode.Perm() != 0755 || hash(before.Data) != old.CoreSHA256 {
		return errors.New("Remote previous core backup changed")
	}
	after, e := rootFile(o, dir+"/"+to+"core")
	if e != nil || after.Mode.Perm() != 0755 || hash(after.Data) != next.CoreSHA256 {
		return errors.New("Remote target core backup changed")
	}
	for _, v := range []struct {
		target, source string
		previous, next []byte
		mode           os.FileMode
	}{{old.Core, dir + "/" + to + "core", before.Data, after.Data, 0755}, {old.Config, dir + "/" + to + "install.json", encoded(old), encoded(next), 0644}, {old.Root + "/intent.json", dir + "/" + to + "intent.json", encoded(intentFor(old)), encoded(intentFor(next)), 0644}} {
		if e = swapRemoteFile(ctx, o, dir, j.Status.ID, v.target, v.source, v.previous, v.next, v.mode); e != nil {
			return e
		}
	}
	return verify(ctx, o, next)
}
func rollbackRemoteApplication(ctx context.Context, o hostOps, j applicationJournal, dir string, h ApplicationUpdateCallbacks) (Manifest, error) {
	if j.Status.ReaderBootstrap {
		return j.Target, errors.New("Remote reader-bootstrap requires forward recovery; old code cannot be restored")
	}
	if e := h.RollbackAllowed(ctx, j.Previous, j.Target); e != nil {
		return j.Target, e
	}
	if _, e := o.run(ctx, nil, "/usr/bin/systemctl", "stop", "--", j.Target.Unit); e != nil {
		return j.Target, e
	}
	if e := replaceRemoteGeneration(ctx, o, j, dir, true); e != nil {
		return j.Target, e
	}
	if e := start(ctx, o, j.Previous); e != nil {
		return j.Previous, e
	}
	if e := h.Health(ctx, j.Previous); e != nil {
		return j.Previous, e
	}
	j.Status.Phase = "rolled-back-held"
	if e := o.writeJournal(applicationJournalName, j); e != nil {
		return j.Previous, e
	}
	return completeHeldRemoteApplication(ctx, o, j, h)
}
func completeHeldRemoteApplication(ctx context.Context, o hostOps, j applicationJournal, h ApplicationUpdateCallbacks) (Manifest, error) {
	target := j.Target
	phase := "completed"
	if j.Status.Phase == "rolled-back-held" {
		target = j.Previous
		phase = "rolled-back"
	}
	if e := verify(ctx, o, target); e != nil {
		return target, e
	}
	done := j.Status
	done.Phase = phase
	if e := h.Complete(ctx, target, done); e != nil {
		return target, e
	}
	// Repair/retry must observe the admitted current identity too. Advance only
	// the exact old owner intent, never adopt unknown installer records.
	b, e := o.readJournal("intent.json")
	if e != nil {
		return target, e
	}
	var prior intent
	if strict(b, &prior) != nil || (prior != intentFor(j.Previous) && prior != intentFor(target)) {
		return target, errors.New("owner installer intent changed; preserve held completion")
	}
	if prior != intentFor(target) {
		if e = o.writeJournal("intent.json", intentFor(target)); e != nil {
			return target, e
		}
	}
	j.Status = done
	if e := o.writeJournal(applicationJournalName, j); e != nil {
		return target, e
	}
	return target, nil
}
