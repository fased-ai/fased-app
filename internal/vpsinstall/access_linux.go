//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
	"time"
)

const accessJournalName = "private-access.json"

type accessJournal struct {
	Version    int      `json:"version"`
	ID         string   `json:"id"`
	Phase      string   `json:"phase"`
	InstanceID string   `json:"instanceId"`
	Previous   Manifest `json:"previous"`
	Next       Manifest `json:"next"`
}

func accessJournalValid(j accessJournal) bool {
	a, b := j.Previous, j.Next
	a.PublicOrigin = b.PublicOrigin
	return j.Version == 1 && update.IDOK(j.ID) && j.InstanceID != "" && j.Previous.validate() == nil && j.Next.validate() == nil && a == b && j.Previous.PublicOrigin != j.Next.PublicOrigin && (j.Phase == "prepared" || j.Phase == "switched" || j.Phase == "rollback" || j.Phase == "committed" || j.Phase == "rolled-back")
}
func accessIdle(o hostOps, m Manifest) error {
	b, e := o.readJournal(accessJournalName)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var j accessJournal
	if len(b) > 32*1024 || strict(b, &j) != nil || !accessJournalValid(j) || j.Previous.ID != m.ID || j.Previous.OwnerUID != m.OwnerUID {
		return errors.New("unknown private access transaction; preserving it")
	}
	if j.Phase != "committed" && j.Phase != "rolled-back" {
		return errors.New("finish private access recovery before updating")
	}
	return nil
}
func maintenanceIdle(o hostOps, m Manifest) error {
	b, e := o.readJournal("application-maintenance.json")
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var j applicationMaintenanceRequest
	if len(b) > 16384 || strict(b, &j) != nil || j.Version != 1 || j.InstallationID != m.ID || !update.IDOK(j.ID) || j.Finished == "" {
		return errors.New("finish update maintenance before changing access")
	}
	return nil
}
func accessFiles(m Manifest) map[string][]byte {
	return map[string][]byte{m.Config: encoded(m), filepath.Join(m.Root, "intent.json"): encoded(intentFor(m)), filepath.Join(m.Root, m.Unit): unit(m), m.UnitPath: unit(m)}
}
func accessStage(j accessJournal) string { return j.Previous.Root + "-access-" + j.ID }
func accessGenerationKnown(o hostOps, j accessJournal) error {
	previous, next := accessFiles(j.Previous), accessFiles(j.Next)
	for path, b := range previous {
		f, e := rootFile(o, path)
		if e != nil {
			return e
		}
		if !known(f, b, 0644) && !known(f, next[path], 0644) {
			return errors.New("private access files changed externally; preserving them")
		}
	}
	return nil
}
func switchAccess(ctx context.Context, o hostOps, j accessJournal, target Manifest, prefix string) error {
	if e := accessGenerationKnown(o, j); e != nil {
		return e
	}
	if e := loaded(ctx, o, j.Previous); e != nil {
		if nextErr := loaded(ctx, o, j.Next); nextErr != nil {
			return e
		}
	}
	stage := accessStage(j)
	if e := directory(ctx, o, stage, 0, 0, 0700); e != nil {
		return e
	}
	if _, e := o.run(ctx, nil, "/usr/bin/systemctl", "stop", "--", target.Unit); e != nil {
		return e
	}
	// Use fixed ordered names, not filesystem enumeration or caller-selected paths.
	for i, path := range []string{target.Config, filepath.Join(target.Root, "intent.json"), filepath.Join(target.Root, target.Unit), target.UnitPath} {
		candidate := filepath.Join(stage, fmt.Sprintf("%s-%d", prefix, i))
		if e := publish(ctx, o, candidate, j.ID, accessFiles(target)[path], 0644); e != nil {
			return e
		}
		if _, e := o.run(ctx, nil, "/usr/bin/mv", "--force", "--no-target-directory", "--", candidate, path); e != nil {
			return e
		}
		if _, e := o.run(ctx, nil, "/usr/bin/sync", "-f", filepath.Dir(path)); e != nil {
			return e
		}
	}
	if _, e := o.run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
		return e
	}
	if e := start(ctx, o, target); e != nil {
		return e
	}
	state, e := status(ctx, o, target)
	if e != nil {
		return e
	}
	if state.InstanceID != j.InstanceID {
		return errors.New("workspace identity changed; preserve data for recovery")
	}
	return nil
}
func cleanupAccessStage(ctx context.Context, o hostOps, j accessJournal) error {
	stage := accessStage(j)
	if _, e := o.read(stage); errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e := checkDirectory(o, stage, 0, 0, 0700); e != nil {
		return e
	}
	for prefix, m := range map[string]Manifest{"next": j.Next, "previous": j.Previous} {
		for i, path := range []string{m.Config, filepath.Join(m.Root, "intent.json"), filepath.Join(m.Root, m.Unit), m.UnitPath} {
			base := fmt.Sprintf("%s-%d", prefix, i)
			for _, name := range []string{base, "." + base + "-" + j.ID + ".pending"} {
				candidate := filepath.Join(stage, name)
				f, e := rootFile(o, candidate)
				if errors.Is(e, os.ErrNotExist) {
					continue
				}
				if e != nil {
					return e
				}
				if !known(f, accessFiles(m)[path], 0644) {
					return errors.New("private access staging changed; preserving it")
				}
				if _, e = o.run(ctx, nil, "/usr/bin/rm", "--", candidate); e != nil {
					return e
				}
			}
		}
	}
	_, e := o.run(ctx, nil, "/usr/bin/rm", "--dir", "--", stage)
	return e
}

func rollbackAccess(ctx context.Context, o hostOps, j accessJournal, confirm func(context.Context, Manifest) error) error {
	j.Phase = "rollback"
	if e := o.writeJournal(accessJournalName, j); e != nil {
		return e
	}
	if e := switchAccess(ctx, o, j, j.Previous, "previous"); e != nil {
		return e
	}
	// Private routing may need external account repair; original loopback/SSH
	// remains usable after restoring the original protected application files.
	var routeErr error
	if confirm != nil {
		routeErr = confirm(ctx, j.Previous)
	}
	j.Phase = "rolled-back"
	if e := o.writeJournal(accessJournalName, j); e != nil {
		return e
	}
	if e := record(o, j.Previous, "ready"); e != nil {
		return e
	}
	return errors.Join(routeErr, cleanupAccessStage(ctx, o, j))
}
func changeAccess(ctx context.Context, o hostOps, m Manifest, origin string, confirm func(context.Context, Manifest) error) (Manifest, error) {
	if e := ownedBy(o, m); e != nil {
		return m, e
	}
	if e := validateEndpoint(m.Listen, origin); e != nil {
		return m, e
	}
	lock, e := o.lock()
	if e != nil {
		return m, e
	}
	defer lock.Close()
	if e := maintenanceIdle(o, m); e != nil {
		return m, e
	}
	if prior, e := readApplicationJournal(o); e == nil && prior.Status.Phase != "completed" && prior.Status.Phase != "rolled-back" {
		return m, errors.New("finish update recovery before changing access")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return m, e
	}
	if b, e := o.readJournal(accessJournalName); e == nil {
		var prior accessJournal
		if len(b) > 32*1024 || strict(b, &prior) != nil || !accessJournalValid(prior) || prior.Previous.ID != m.ID || prior.Previous.OwnerUID != m.OwnerUID {
			return m, errors.New("unknown private access transaction; preserving it")
		}
		if prior.Phase != "committed" && prior.Phase != "rolled-back" {
			recovery, done := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer done()
			if e := rollbackAccess(recovery, o, prior, confirm); e != nil {
				return m, e
			}
			return prior.Previous, errors.New("previous access restored; rerun the same setup")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return m, e
	}
	if e := verify(ctx, o, m); e != nil {
		return m, e
	}
	if e := loaded(ctx, o, m); e != nil {
		return m, e
	}
	if origin == m.PublicOrigin {
		if confirm != nil {
			e = confirm(ctx, m)
		}
		return m, e
	}
	current, e := status(ctx, o, m)
	if e != nil {
		return m, e
	}
	if !current.Active || current.InstanceID == "" {
		return m, errors.New("start Fased before changing private access")
	}
	next := m
	next.PublicOrigin = origin
	j := accessJournal{Version: 1, ID: update.NewProbe(), Phase: "prepared", InstanceID: current.InstanceID, Previous: m, Next: next}
	if e := o.writeJournal(accessJournalName, j); e != nil {
		return m, e
	}
	failure := switchAccess(ctx, o, j, next, "next")
	if failure == nil {
		j.Phase = "switched"
		failure = o.writeJournal(accessJournalName, j)
	}
	if failure == nil && confirm != nil {
		failure = confirm(ctx, next)
	}
	if failure == nil {
		j.Phase = "committed"
		failure = o.writeJournal(accessJournalName, j)
	}
	if failure != nil {
		recovery, done := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer done()
		rollbackErr := rollbackAccess(recovery, o, j, confirm)
		return m, errors.Join(failure, rollbackErr)
	}
	if e := record(o, next, "ready"); e != nil {
		return next, e
	}
	return next, cleanupAccessStage(ctx, o, j)
}

// ChangeAccess is terminal/OS-owner-only. It changes no data, account, key,
// executable, release floor or update permission. Root executes only OS tools.
func ChangeAccess(ctx context.Context, m Manifest, origin string, s *custodyinstall.System, confirm func(context.Context, Manifest) error) (Manifest, error) {
	if s == nil || s.NonInteractive {
		return m, errors.New("ordinary administrator terminal required")
	}
	s.Authorization = "Authorize private access change for this workspace. It briefly restarts Fased, preserves data and SSH recovery, and changes no wallet or update permission."
	return changeAccess(ctx, liveOps(s), m, origin, confirm)
}
