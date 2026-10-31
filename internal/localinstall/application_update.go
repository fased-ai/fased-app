package localinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
)

// ApplicationUpdate is constructed only by the authenticated release driver.
// It is not a JSON/IPC request or a source of release, path or OS authority.
// Only existing fixed application filenames are replaced; state never moves.
type ApplicationUpdate struct {
	ReaderBootstrap                     bool // Trusted driver classification, never UI/CLI authority.
	Sequence                            uint64
	ReleaseSHA256                       string
	ID, Version, ExpectedManifestSHA256 string
	Engine, EngineSHA256                string
	Desktop, DesktopSHA256              string
}
type ApplicationUpdateStatus struct {
	ReaderBootstrap        bool   `json:"readerBootstrap,omitempty"`
	Sequence               uint64 `json:"sequence"`
	ReleaseSHA256          string `json:"releaseSha256"`
	Version                int    `json:"version"`
	ID                     string `json:"id"`
	Phase                  string `json:"phase"`
	PreviousManifestSHA256 string `json:"previousManifestSha256"`
	TargetManifestSHA256   string `json:"targetManifestSha256"`
	EngineSHA256           string `json:"engineSha256"`
	DesktopSHA256          string `json:"desktopSha256,omitempty"`
}

// BeforeStop owns authenticated consent, the exact metadata fence/backup and
// proof that this is an app-only installation. Complete releases only that same
// fence, idempotently. Neither this adapter nor a callback may restore databases
// or signer state; RollbackAllowed must first establish metadata compatibility.
type ApplicationUpdateCallbacks struct {
	BeforeStage     func(context.Context) error // Optional exact owner dispatch recheck under installer lock, before a new journal.
	BeforeStop      func(context.Context, Manifest, Manifest) error
	AfterStop       func(context.Context, Manifest, Manifest) error // Optional exact reader-bootstrap; old process must be stopped.
	Health          func(context.Context, Manifest) error
	RollbackAllowed func(context.Context, Manifest, Manifest) error
	Complete        func(context.Context, Manifest, ApplicationUpdateStatus) error
}
type applicationUpdateJournal struct {
	Status   ApplicationUpdateStatus `json:"status"`
	Previous Manifest                `json:"previous"`
	Target   Manifest                `json:"target"`
}
type applicationServices struct {
	stop  func(context.Context, Manifest) error
	start func(context.Context, Manifest) error
}

const maxApplicationBinary = 512 << 20

func ManifestSHA256(m Manifest) string { b, _ := json.Marshal(m); return bytesDigest(b) }
func applicationUpdateDirectory(p Paths) string {
	return filepath.Join(filepath.Dir(p.Root), "."+filepath.Base(p.Root)+"-updates")
}
func applicationUpdateID(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 16 && hex.EncodeToString(b) == s
}
func (c ApplicationUpdate) validate() error {
	if c.Sequence == 0 || !validExpectedDigest(c.ReleaseSHA256) || !applicationUpdateID(c.ID) || c.Version == "" || len(c.Version) > 128 || strings.ContainsAny(c.Version, "/\\\x00\r\n\t ") || !validExpectedDigest(c.ExpectedManifestSHA256) || !validExpectedDigest(c.EngineSHA256) || c.Engine == "" || (c.Desktop == "") != (c.DesktopSHA256 == "") || c.Desktop != "" && !validExpectedDigest(c.DesktopSHA256) {
		return errors.New("complete authenticated application update identity required")
	}
	for _, p := range []string{c.Engine, c.Desktop} {
		if p != "" && (!filepath.IsAbs(p) || filepath.Clean(p) != p) {
			return errors.New("authenticated artifact paths must be absolute and clean")
		}
	}
	return nil
}
func updateCallbacksValid(h ApplicationUpdateCallbacks) bool {
	return h.BeforeStop != nil && h.Health != nil && h.RollbackAllowed != nil && h.Complete != nil
}
func validApplicationJournal(j applicationUpdateJournal, p Paths) bool {
	s := j.Status
	if s.Sequence == 0 || !validExpectedDigest(s.ReleaseSHA256) || j.Target.ApplicationSequence != s.Sequence || j.Target.ApplicationReleaseSHA256 != s.ReleaseSHA256 || s.Version != 1 || !applicationUpdateID(s.ID) || s.PreviousManifestSHA256 != ManifestSHA256(j.Previous) || s.TargetManifestSHA256 != ManifestSHA256(j.Target) || s.EngineSHA256 != j.Target.Files[engineName] || s.DesktopSHA256 != j.Target.Files[desktopName] {
		return false
	}
	if j.Previous.Root != p.Root || j.Previous.Data != p.Data || j.Target.Root != p.Root || j.Target.Data != p.Data || j.Previous.Phase != "ready" || j.Target.Phase != "ready" || j.Previous.Autostart != j.Target.Autostart || j.Previous.Listen != j.Target.Listen || j.Previous.PublicOrigin != j.Target.PublicOrigin || j.Previous.Location != j.Target.Location || len(j.Previous.Files) != len(j.Target.Files) {
		return false
	}
	for n, h := range j.Previous.Files {
		if n != engineName && n != desktopName && j.Target.Files[n] != h {
			return false
		}
	}
	switch s.Phase {
	case "prepared", "applying", "recovery", "healthy-held", "rolled-back-held", "completed", "rolled-back":
		return true
	}
	return false
}
func readApplicationJournal(p Paths) (applicationUpdateJournal, error) {
	b, e := core.ReadPrivateFile(filepath.Join(applicationUpdateDirectory(p), "current.json"))
	if e != nil {
		return applicationUpdateJournal{}, e
	}
	var j applicationUpdateJournal
	if len(b) > 64*1024 || json.Unmarshal(b, &j) != nil || !validApplicationJournal(j, p) {
		return j, errors.New("unknown application update journal; preserve installation and backups")
	}
	return j, nil
}
func ApplicationUpdateState(paths Paths) (ApplicationUpdateStatus, error) {
	p, e := normalize(paths)
	if e != nil {
		return ApplicationUpdateStatus{}, e
	}
	j, e := readApplicationJournal(p)
	return j.Status, e
}
func storeApplicationJournal(p Paths, j applicationUpdateJournal) error {
	return core.WritePrivateJSON(filepath.Join(applicationUpdateDirectory(p), "current.json"), j)
}
func updateInput(path string) (*os.File, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 || i.Size() <= 0 || i.Size() > maxApplicationBinary {
		return nil, errors.New("application update input must be a bounded private regular file")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	actual, e := f.Stat()
	if e != nil || !os.SameFile(i, actual) {
		f.Close()
		return nil, errors.New("application update input changed")
	}
	return f, nil
}
func updateFileDigest(path string) (string, error) {
	f, e := updateInput(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, maxApplicationBinary+1))
	if e != nil {
		return "", e
	}
	if n > maxApplicationBinary {
		return "", errors.New("oversized update artifact")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyUpdateFile(source, target, want string) error {
	if got, e := updateFileDigest(target); e == nil {
		if got == want {
			return nil
		}
		return errors.New("retained update artifact changed; preserve it")
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	f, e := updateInput(source)
	if e != nil {
		return e
	}
	defer f.Close()
	// Each authenticated retry owns only its newly created scratch file. A
	// crashed copy (or unclassified old scratch) is retained, never overwritten.
	stage, e := os.CreateTemp(filepath.Dir(target), ".staging-"+filepath.Base(target)+"-*")
	if e != nil {
		return e
	}
	defer os.Remove(stage.Name())
	if e = stage.Chmod(0700); e != nil {
		stage.Close()
		return e
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(stage, h), io.LimitReader(f, maxApplicationBinary+1))
	if e = errors.Join(copyErr, stage.Sync(), stage.Close()); e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return errors.New("authenticated update source changed while staging")
	}
	// Exclusive publication cannot replace an unknown final artifact.
	if e = os.Link(stage.Name(), target); e != nil {
		return e
	}
	return syncApplicationDirectory(filepath.Dir(target))
}
func syncApplicationDirectory(dir string) error {
	f, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func applyUpdateFile(source, target, want, previous string) error {
	got, e := updateFileDigest(target)
	if e != nil {
		return e
	}
	if got == want {
		return nil
	}
	if got != previous {
		return errors.New("application target changed outside the update; preserve it")
	}
	f, e := updateInput(source)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = writeNew(target, io.LimitReader(f, maxApplicationBinary+1), 0700, want); e != nil {
		return e
	}
	return syncApplicationDirectory(filepath.Dir(target))
}

// ReplaceApplication is the app-only Local transaction. Initial installation
// remains immutable; callers cannot turn install retry into a binary update.
func ReplaceApplication(ctx context.Context, paths Paths, c ApplicationUpdate, h ApplicationUpdateCallbacks) (Manifest, error) {
	if runtime.GOOS != "linux" {
		return Manifest{}, errors.New("application updates currently support Ubuntu/Linux Local only")
	}
	return replaceApplication(ctx, paths, c, h, applicationServices{stop: stop, start: start}, nil, false)
}
func RollbackApplication(ctx context.Context, paths Paths, c ApplicationUpdate, h ApplicationUpdateCallbacks) (Manifest, error) {
	if runtime.GOOS != "linux" {
		return Manifest{}, errors.New("application recovery currently supports Ubuntu/Linux Local only")
	}
	return replaceApplication(ctx, paths, c, h, applicationServices{stop: stop, start: start}, nil, true)
}
func replaceApplication(ctx context.Context, paths Paths, c ApplicationUpdate, h ApplicationUpdateCallbacks, services applicationServices, fault func(string) error, rollback bool) (Manifest, error) {
	if e := supported(); e != nil {
		return Manifest{}, e
	}
	if e := ctx.Err(); e != nil {
		return Manifest{}, e
	}
	if e := c.validate(); e != nil {
		return Manifest{}, e
	}
	if !updateCallbacksValid(h) || services.stop == nil || services.start == nil {
		return Manifest{}, errors.New("authenticated metadata/health/recovery callbacks required")
	}
	p, e := normalize(paths)
	if e != nil {
		return Manifest{}, e
	}
	if info, err := os.Lstat(p.Root); err != nil || !info.IsDir() {
		return Manifest{}, errors.New("an existing private installation is required for updates")
	}
	if e = core.EnsurePrivateDirectory(p.Root); e != nil {
		return Manifest{}, e
	}
	lock, e := core.LockInstance(filepath.Join(p.Root, "install.lock"))
	if e != nil {
		return Manifest{}, errors.New("another installation operation is active")
	}
	defer lock.Close()
	if _, err := os.Lstat(filepath.Join(p.Data, "enrollment.json")); !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, errors.New("custody enrollment exists or cannot be excluded; app-only update refused")
	}
	dir := applicationUpdateDirectory(p)
	for _, pair := range [][2]string{{dir, p.Data}, {p.Data, dir}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return Manifest{}, errors.New("update controller directory must be separate from application state")
		}
	}
	if e = core.EnsurePrivateDirectory(dir); e != nil {
		return Manifest{}, e
	}
	j, e := readApplicationJournal(p)
	if e == nil && j.Status.ID != c.ID {
		if c.Sequence <= j.Status.Sequence {
			return Manifest{}, errors.New("application release sequence was already consumed; exact same identity may resume")
		}
		if j.Status.Phase != "completed" && j.Status.Phase != "rolled-back" {
			return Manifest{}, errors.New("another application update needs recovery")
		}
		e = os.ErrNotExist
	}
	if errors.Is(e, os.ErrNotExist) {
		if c.ReaderBootstrap != (h.AfterStop != nil) {
			return Manifest{}, errors.New("reader-bootstrap hook classification required before staging")
		}
		if rollback {
			return Manifest{}, errors.New("no matching update to roll back")
		}
		m, err := Load(p)
		if err != nil {
			return Manifest{}, err
		}
		if c.Sequence <= m.ApplicationSequence {
			return m, errors.New("application release sequence rollback refused")
		}
		if m.Phase != "ready" || ManifestSHA256(m) != c.ExpectedManifestSHA256 {
			return m, errors.New("installed manifest differs from reviewed update")
		}
		if err = verify(m); err != nil {
			return m, err
		}
		if (m.Files[desktopName] != "") != (c.Desktop != "") {
			return m, errors.New("update cannot add or remove a desktop installation")
		}
		if h.BeforeStage != nil {
			if e = h.BeforeStage(ctx); e != nil {
				return m, e
			}
		}
		next := m
		next.Version = c.Version
		next.ApplicationSequence = c.Sequence
		next.ApplicationReleaseSHA256 = c.ReleaseSHA256
		next.Files = map[string]string{}
		for n, v := range m.Files {
			next.Files[n] = v
		}
		next.Files[engineName] = c.EngineSHA256
		if c.Desktop != "" {
			next.Files[desktopName] = c.DesktopSHA256
		}
		j = applicationUpdateJournal{Previous: m, Target: next, Status: ApplicationUpdateStatus{ReaderBootstrap: c.ReaderBootstrap, Sequence: c.Sequence, ReleaseSHA256: c.ReleaseSHA256, Version: 1, ID: c.ID, Phase: "prepared", PreviousManifestSHA256: ManifestSHA256(m), TargetManifestSHA256: ManifestSHA256(next), EngineSHA256: c.EngineSHA256, DesktopSHA256: c.DesktopSHA256}}
		// Publish the identity first. Interruption can finish staging only these
		// exact artifacts, never replace the journal with a new candidate.
		if e = storeApplicationJournal(p, j); e != nil {
			return m, e
		}
		if fault != nil {
			if e = fault("journal-prepared"); e != nil {
				return m, e
			}
		}
	} else if e != nil {
		return Manifest{}, e
	}
	if j.Status.ReaderBootstrap != c.ReaderBootstrap || j.Status.Sequence != c.Sequence || j.Status.ReleaseSHA256 != c.ReleaseSHA256 || j.Status.ID != c.ID || j.Status.PreviousManifestSHA256 != c.ExpectedManifestSHA256 || j.Target.Version != c.Version || j.Status.EngineSHA256 != c.EngineSHA256 || j.Status.DesktopSHA256 != c.DesktopSHA256 {
		return Manifest{}, errors.New("update identity changed; preserve original transaction")
	}
	if j.Status.Phase == "healthy-held" || j.Status.Phase == "rolled-back-held" {
		return completeHeldApplication(ctx, p, j, h)
	}
	if j.Status.Phase == "completed" || j.Status.Phase == "rolled-back" {
		m, e := Load(p)
		if e != nil {
			return m, e
		}
		want := j.Target
		if j.Status.Phase == "rolled-back" {
			want = j.Previous
		}
		if ManifestSHA256(m) != ManifestSHA256(want) {
			return m, errors.New("completed installation manifest changed")
		}
		return m, verify(m)
	}
	if c.ReaderBootstrap != (h.AfterStop != nil) {
		return Manifest{}, errors.New("reader-bootstrap hook differs from the original transaction")
	}
	current, e := Load(p)
	if e != nil {
		return Manifest{}, e
	}
	currentHash := ManifestSHA256(current)
	if currentHash != j.Status.PreviousManifestSHA256 && currentHash != j.Status.TargetManifestSHA256 {
		return Manifest{}, errors.New("installed manifest changed outside the pending update")
	}
	for name, want := range j.Previous.Files {
		if name == engineName || name == desktopName {
			continue
		}
		if got, e := updateFileDigest(filepath.Join(p.Root, name)); e != nil || got != want {
			return Manifest{}, errors.New("installation service inventory changed")
		}
	}
	transaction := filepath.Join(dir, c.ID)
	if e = core.EnsurePrivateDirectory(transaction); e != nil {
		return Manifest{}, e
	}
	// A prepared journal may be interrupted while copying; existing mismatched
	// artifacts are refused. No application data is read/copied/restored here.
	for _, v := range []struct{ name, path, hash string }{{engineName, c.Engine, c.EngineSHA256}, {desktopName, c.Desktop, c.DesktopSHA256}} {
		if v.path == "" {
			continue
		}
		if e = copyUpdateFile(v.path, filepath.Join(transaction, "next-"+v.name), v.hash); e != nil {
			return Manifest{}, e
		}
		backup := filepath.Join(transaction, "previous-"+v.name)
		if _, err := os.Lstat(backup); errors.Is(err, os.ErrNotExist) {
			if e = copyUpdateFile(filepath.Join(p.Root, v.name), backup, j.Previous.Files[v.name]); e != nil {
				return Manifest{}, e
			}
		} else if err != nil {
			return Manifest{}, err
		} else if got, e := updateFileDigest(backup); e != nil || got != j.Previous.Files[v.name] {
			return Manifest{}, errors.New("previous application backup changed")
		}
	}
	previousManifest := filepath.Join(transaction, "previous-install.json")
	if b, err := core.ReadPrivateFile(previousManifest); err == nil {
		var previous Manifest
		if json.Unmarshal(b, &previous) != nil || ManifestSHA256(previous) != j.Status.PreviousManifestSHA256 {
			return Manifest{}, errors.New("previous manifest backup changed; preserve it")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if e = core.WritePrivateJSON(previousManifest, j.Previous); e != nil {
			return Manifest{}, e
		}
	} else {
		return Manifest{}, err
	}
	if e = h.BeforeStop(ctx, j.Previous, j.Target); e != nil {
		return Manifest{}, e
	}
	if rollback {
		return rollBackApplication(ctx, p, j, h, services, fault)
	}
	j.Status.Phase = "applying"
	if e = storeApplicationJournal(p, j); e != nil {
		return Manifest{}, e
	}
	if fault != nil {
		if e = fault("before-stop"); e != nil {
			return Manifest{}, e
		}
	}
	if e = services.stop(ctx, j.Previous); e != nil {
		return Manifest{}, e
	}
	if h.AfterStop != nil {
		if e = h.AfterStop(ctx, j.Previous, j.Target); e != nil {
			return Manifest{}, e
		}
	}
	for _, name := range []string{engineName, desktopName} {
		if j.Target.Files[name] == "" {
			continue
		}
		if e = applyUpdateFile(filepath.Join(transaction, "next-"+name), filepath.Join(p.Root, name), j.Target.Files[name], j.Previous.Files[name]); e != nil {
			return Manifest{}, e
		}
		if fault != nil {
			if e = fault("replaced-" + name); e != nil {
				return Manifest{}, e
			}
		}
	}
	if e = core.WritePrivateJSON(filepath.Join(p.Root, "install.json"), j.Target); e != nil {
		return Manifest{}, e
	}
	if fault != nil {
		if e = fault("manifest"); e != nil {
			return Manifest{}, e
		}
	}
	if e = verify(j.Target); e != nil {
		return Manifest{}, e
	}
	healthErr := services.start(ctx, j.Target)
	if healthErr == nil {
		healthErr = h.Health(ctx, j.Target)
	}
	if healthErr != nil {
		j.Status.Phase = "recovery"
		if e = storeApplicationJournal(p, j); e != nil {
			return Manifest{}, e
		}
		if ctx.Err() != nil {
			return j.Target, ctx.Err()
		}
		m, e := rollBackApplication(ctx, p, j, h, services, fault)
		if e != nil {
			return m, fmt.Errorf("update health failed; compatible recovery remains held: %w", e)
		}
		return m, fmt.Errorf("update health failed; previous compatible application restored: %w", healthErr)
	}
	j.Status.Phase = "healthy-held"
	if e = storeApplicationJournal(p, j); e != nil {
		return j.Target, e
	}
	return completeHeldApplication(ctx, p, j, h)
}
func rollBackApplication(ctx context.Context, p Paths, j applicationUpdateJournal, h ApplicationUpdateCallbacks, services applicationServices, fault func(string) error) (Manifest, error) {
	if j.Status.ReaderBootstrap {
		return j.Target, errors.New("reader-bootstrap requires forward recovery; old code cannot be restored")
	}
	if e := h.RollbackAllowed(ctx, j.Previous, j.Target); e != nil {
		return j.Target, e
	}
	if e := services.stop(ctx, j.Target); e != nil {
		return j.Target, e
	}
	transaction := filepath.Join(applicationUpdateDirectory(p), j.Status.ID)
	for _, name := range []string{engineName, desktopName} {
		if j.Previous.Files[name] == "" {
			continue
		}
		if e := applyUpdateFile(filepath.Join(transaction, "previous-"+name), filepath.Join(p.Root, name), j.Previous.Files[name], j.Target.Files[name]); e != nil {
			return j.Target, e
		}
		if fault != nil {
			if e := fault("rollback-" + name); e != nil {
				return j.Target, e
			}
		}
	}
	if e := core.WritePrivateJSON(filepath.Join(p.Root, "install.json"), j.Previous); e != nil {
		return j.Target, e
	}
	if e := verify(j.Previous); e != nil {
		return j.Previous, e
	}
	if e := services.start(ctx, j.Previous); e != nil {
		return j.Previous, e
	}
	if e := h.Health(ctx, j.Previous); e != nil {
		return j.Previous, e
	}
	j.Status.Phase = "rolled-back-held"
	if e := storeApplicationJournal(p, j); e != nil {
		return j.Previous, e
	}
	return completeHeldApplication(ctx, p, j, h)
}

// Completion is a separate durable phase. A callback may have committed its
// outcome before losing the reply; retry may only verify and replay Complete.
func completeHeldApplication(ctx context.Context, p Paths, j applicationUpdateJournal, h ApplicationUpdateCallbacks) (Manifest, error) {
	target := j.Target
	phase := "completed"
	if j.Status.Phase == "rolled-back-held" {
		target = j.Previous
		phase = "rolled-back"
	}
	actual, e := Load(p)
	if e != nil || ManifestSHA256(actual) != ManifestSHA256(target) {
		return target, errors.New("held completion manifest changed; preserve recovery")
	}
	if e = verify(target); e != nil {
		return target, e
	}
	done := j.Status
	done.Phase = phase
	if e = h.Complete(ctx, target, done); e != nil {
		return target, e
	}
	j.Status = done
	if e = storeApplicationJournal(p, j); e != nil {
		return target, e
	}
	return target, nil
}
