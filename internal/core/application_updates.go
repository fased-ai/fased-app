package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

// A controller is installed by trusted Go composition only. There is no HTTP,
// CLI, environment, endpoint or JSON mechanism to supply one. Ordinary builds
// have no configured channel/controller and cannot activate installed updates.
// The controller independently authenticates releases, owns the OS transaction,
// rechecks policy/compatibility and returns an actual durable installation result.
var ErrApplicationUpdateNotStarted = errors.New("update was refused before installation began")

type ApplicationUpdateController interface {
	Check(context.Context) (*ApplicationRelease, error)
	Apply(context.Context, ApplicationUpdateOperation) (ApplicationUpdateReceipt, error)
	Status(context.Context, ApplicationUpdateOperation) (ApplicationUpdateReceipt, error)
}
type ApplicationRelease struct {
	EngineSHA256   string        `json:"engineSha256"`
	Version        string        `json:"version"`
	Sequence       uint64        `json:"sequence,string"`
	ManifestSHA256 string        `json:"manifestSha256"`
	Target         update.Target `json:"target"`
}
type ApplicationUpdateOperation struct {
	NoticeReadPhase string                    `json:"noticeReadPhase,omitempty"`
	OwnerLease      string                    `json:"ownerLease,omitempty"`
	Receipt         *ApplicationUpdateReceipt `json:"receipt,omitempty"`
	ID              string                    `json:"id"`
	Phase           string                    `json:"phase"`
	Release         *ApplicationRelease       `json:"release,omitempty"`
	Automatic       bool                      `json:"automatic,omitempty"`
	PolicySHA256    string                    `json:"policySha256,omitempty"`
	BootID          string                    `json:"bootId,omitempty"`
	CheckedAt       int64                     `json:"checkedAt,omitempty"`
	FinishedAt      int64                     `json:"finishedAt,omitempty"`
	Failure         string                    `json:"failure,omitempty"`
}
type ApplicationUpdateReceipt struct {
	EngineSHA256   string `json:"engineSha256"`
	ID             string `json:"id"`
	ManifestSHA256 string `json:"manifestSha256"`
	Sequence       uint64 `json:"sequence,string"`
	Phase          string `json:"phase"`
}
type ApplicationUpdateNotice struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Read  bool   `json:"read"`
}
type ApplicationUpdateView struct {
	Notice              *ApplicationUpdateNotice   `json:"notice,omitempty"`
	InstanceID          string                     `json:"instanceId"`
	Schema              int                        `json:"schema"`
	Version             int                        `json:"version"`
	Revision            int64                      `json:"revision"`
	InstalledVersion    string                     `json:"installedVersion"`
	ControllerAvailable bool                       `json:"controllerAvailable"`
	ActivationEnabled   bool                       `json:"activationEnabled"`
	Operation           ApplicationUpdateOperation `json:"operation"`
	Policy              UpdatePolicy               `json:"policy"`
	PolicyRevision      int64                      `json:"policyRevision"`
}
type ApplicationUpdateCommand struct {
	Action         string `json:"action"`
	Revision       int64  `json:"revision"`
	PolicyRevision int64  `json:"policyRevision"`
	ManifestSHA256 string `json:"manifestSha256,omitempty"`
}
type applicationUpdates struct {
	mu         sync.Mutex
	controller ApplicationUpdateController
	configured atomic.Bool
}

func (r ApplicationRelease) valid() bool {
	return hexValue(r.EngineSHA256, 32) && validName(r.Version) && r.Sequence > 0 && hexValue(r.ManifestSHA256, 32) && r.Target == (update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH})
}
func (o ApplicationUpdateOperation) valid() bool {
	if o.OwnerLease != "" && !update.IDOK(o.OwnerLease) {
		return false
	}
	if o.Receipt != nil {
		r := o.Receipt
		if o.Release == nil || r.ID != o.ID || r.Sequence != o.Release.Sequence || r.ManifestSHA256 != o.Release.ManifestSHA256 || !oneOf(r.Phase, "completed", "rolled-back", "recovery") {
			return false
		}
		if r.Phase != "recovery" && !hexValue(r.EngineSHA256, 32) {
			return false
		}
	}

	if !oneOf(o.Phase, "idle", "check-queued", "checking", "available", "current", "check-failed", "queued", "applying", "completed", "rolled-back", "recovery") || len(o.Failure) > 120 {
		return false
	}
	if o.Phase == "idle" {
		return o.ID == "" && o.Release == nil
	}
	if !update.IDOK(o.ID) {
		return false
	}
	if o.Release != nil && !o.Release.valid() {
		return false
	}
	if oneOf(o.Phase, "available", "queued", "applying", "completed", "rolled-back", "recovery") && o.Release == nil {
		return false
	}
	return o.PolicySHA256 == "" || hexValue(o.PolicySHA256, 32)
}
func migrateApplicationUpdates(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var phase string
	if e = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); e != nil {
		return e
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	// 34 belongs to the separately preserved swap-recovery branch. Do not pretend
	// this branch knows its extra workspace fields or silently rewrite that state.
	var schema int
	if e = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema); e != nil {
		return e
	}
	if schema != 33 {
		return errors.New("unsupported predecessor for application-update state; preserve original installation")
	}
	_, e = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS application_updates(id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL, document TEXT NOT NULL);
 INSERT OR IGNORE INTO application_updates VALUES(1,0,'{"phase":"idle","id":""}');`)
	if e != nil {
		return e
	}
	o, revision, e := readApplicationUpdate(ctx, tx)
	if e != nil || revision != 0 || o != (ApplicationUpdateOperation{Phase: "idle"}) {
		return errors.New("predecessor schema cannot protect existing application-update authority")
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA user_version=35"); e != nil {
		return e
	}
	return tx.Commit()
}
func readApplicationUpdate(ctx context.Context, r snapshotReader) (ApplicationUpdateOperation, int64, error) {
	var o ApplicationUpdateOperation
	var revision int64
	var raw string
	e := r.QueryRowContext(ctx, "SELECT revision,document FROM application_updates WHERE id=1").Scan(&revision, &raw)
	if e == nil && (update.StrictJSON([]byte(raw), &o) != nil || !o.valid()) {
		e = errors.New("damaged update record; preserve installation and recovery data")
	}
	return o, revision, e
}
func saveApplicationUpdate(ctx context.Context, tx *sql.Tx, o ApplicationUpdateOperation, revision int64) error {
	if !o.valid() {
		return errors.New("invalid update transition")
	}
	b, e := json.Marshal(o)
	if e != nil {
		return e
	}
	result, e := tx.ExecContext(ctx, "UPDATE application_updates SET document=?,revision=revision+1 WHERE id=1 AND revision=?", string(b), revision)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) ApplicationUpdates(ctx context.Context) (ApplicationUpdateView, error) {
	o, r, e := readApplicationUpdate(ctx, s.db)
	if e != nil {
		return ApplicationUpdateView{}, e
	}
	setup, e := s.Setup(ctx)
	if e != nil {
		return ApplicationUpdateView{}, e
	}
	p := defaultUpdatePreferences().Policy
	if setup.Settings.Updates != nil {
		p = setup.Settings.Updates.Policy
	}
	return ApplicationUpdateView{InstanceID: s.instanceID, Schema: StateSchema, Version: 1, Revision: r, InstalledVersion: Version, ControllerAvailable: s.appUpdates.configured.Load(), Operation: o, Policy: p, PolicyRevision: setup.Revision, Notice: applicationUpdateNotice(o, p)}, nil
}
func (s *Store) CommandApplicationUpdate(ctx context.Context, c ApplicationUpdateCommand) (ApplicationUpdateView, error) {
	s.appUpdates.mu.Lock()
	defer s.appUpdates.mu.Unlock()
	if !s.appUpdates.configured.Load() && c.Action != "acknowledge" {
		return ApplicationUpdateView{}, errors.New("trusted release channel and installation controller are not configured; activation remains disabled")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return ApplicationUpdateView{}, e
	}
	defer tx.Rollback()
	o, r, e := readApplicationUpdate(ctx, tx)
	if e != nil {
		return ApplicationUpdateView{}, e
	}
	setup, e := readSetupRecord(ctx, tx)
	if e != nil {
		return ApplicationUpdateView{}, e
	}
	if c.Revision != r || c.PolicyRevision != setup.Revision {
		return ApplicationUpdateView{}, ErrConflict
	}
	p := defaultUpdatePreferences().Policy
	if setup.Settings.Updates != nil {
		p = setup.Settings.Updates.Policy
	}
	if c.Action != "acknowledge" && (oneOf(o.Phase, "queued", "applying") || (o.Phase == "recovery" && c.Action != "recover")) {
		return ApplicationUpdateView{}, errors.New("update transaction requires completion or protected recovery")
	}
	switch c.Action {
	case "acknowledge":
		if c.ManifestSHA256 != "" || applicationUpdateNotice(o, p) == nil {
			return ApplicationUpdateView{}, errors.New("no update notice")
		}
		o.NoticeReadPhase = o.Phase
	case "recover":
		if o.Phase != "recovery" || c.ManifestSHA256 != "" {
			return ApplicationUpdateView{}, errors.New("an existing recovery transaction is required")
		}
		o.Phase = "applying"
	case "check":
		if c.ManifestSHA256 != "" || oneOf(o.Phase, "check-queued", "checking") {
			return ApplicationUpdateView{}, errors.New("one release check at a time")
		}
		o = ApplicationUpdateOperation{ID: update.NewProbe(), Phase: "check-queued"}
	case "apply":
		if o.Phase != "available" || o.Release == nil || c.ManifestSHA256 != o.Release.ManifestSHA256 {
			return ApplicationUpdateView{}, errors.New("review the current release before updating")
		}
		if !updatePinMatches(p, *o.Release) {
			return ApplicationUpdateView{}, errors.New("release differs from the saved version pin")
		}
		o.ID = update.NewProbe()
		o.OwnerLease = ""
		o.Receipt = nil
		o.Phase = "queued"
		o.Automatic = false
		o.PolicySHA256 = update.SnapshotDigest("application-update-policy", p)
	default:
		return ApplicationUpdateView{}, errors.New("unsupported update action")
	}
	if e = saveApplicationUpdate(ctx, tx, o, r); e != nil {
		return ApplicationUpdateView{}, e
	}
	if e = tx.Commit(); e != nil {
		return ApplicationUpdateView{}, e
	}
	return s.ApplicationUpdates(ctx)
}
func updatePinMatches(p UpdatePolicy, r ApplicationRelease) bool {
	return p.Pin == nil || (p.Pin.Sequence == uintSequence(r.Sequence) && p.Pin.ManifestSHA256 == r.ManifestSHA256)
}
func uintSequence(n uint64) string { return strconv.FormatUint(n, 10) }

// runApplicationUpdates is part of the existing core worker. A durable applying
// record precedes the controller call. After interruption only Status is called;
// the same operation is never automatically submitted a second time.
func (s *Store) runApplicationUpdates(ctx context.Context) error {
	if !s.appUpdates.configured.Load() || !s.appUpdates.mu.TryLock() {
		return nil
	}
	defer s.appUpdates.mu.Unlock()
	v, e := s.ApplicationUpdates(ctx)
	if e != nil {
		return e
	}
	o := v.Operation
	now := s.now().Unix()
	p := v.Policy
	if o.Phase == "recovery" {
		return nil
	}
	if p.Mode == "automatic" && !oneOf(o.Phase, "queued", "applying", "check-queued", "checking", "available") && (o.CheckedAt == 0 || now-o.CheckedAt >= 3600) {
		o = ApplicationUpdateOperation{ID: update.NewProbe(), Phase: "check-queued"}
	}
	if o.Phase == "checking" && o.BootID != s.bootID {
		o.Phase = "check-queued"
	} // metadata-only fetch is safe to repeat.
	switch o.Phase {
	case "check-queued":
		o.Phase = "checking"
		o.BootID = s.bootID
		if e = s.publishApplicationUpdate(ctx, o, v.Revision); e != nil {
			return e
		}
		v.Revision++
		release, err := s.appUpdates.controller.Check(ctx)
		o.CheckedAt = now
		o.FinishedAt = now
		o.BootID = ""
		if err != nil {
			o.Phase = "check-failed"
			o.Failure = "Release check failed. Check access and try again."
		} else if release == nil {
			o.Phase = "current"
			o.Failure = ""
		} else if !release.valid() {
			o.Phase = "check-failed"
			o.Failure = "Release identity is invalid. Preserve the current installation."
		} else {
			o.Phase = "available"
			o.Release = release
			o.Failure = ""
		}
		return s.publishApplicationUpdate(ctx, o, v.Revision)
	case "available":
		if p.Mode != "automatic" || !updatePinMatches(p, *o.Release) || (p.Window != nil && !p.Window.Contains(time.Unix(now, 0))) {
			return nil
		}
		o.ID = update.NewProbe()
		o.OwnerLease = ""
		o.Receipt = nil
		o.Phase = "queued"
		o.Automatic = true
		o.PolicySHA256 = update.SnapshotDigest("application-update-policy", p)
		return s.publishApplicationUpdate(ctx, o, v.Revision)
	case "queued":
		if busy, e := s.applicationUpdateBusy(ctx); e != nil {
			return e
		} else if busy {
			return nil
		}
		if o.PolicySHA256 != update.SnapshotDigest("application-update-policy", p) || (o.Automatic && (p.Mode != "automatic" || p.Window != nil && !p.Window.Contains(time.Unix(now, 0)))) {
			o.Phase = "available"
			o.Automatic = false
			return s.publishApplicationUpdate(ctx, o, v.Revision)
		}
		// Controller must independently recheck OS authorization, authentic release,
		// compatibility, locks, disk space and pending/unknown work before replacing.
		o.Phase = "applying"
		o.BootID = s.bootID
		if e = s.publishApplicationUpdate(ctx, o, v.Revision); e != nil {
			return e
		}
		v.Revision++
		receipt, err := s.appUpdates.controller.Apply(ctx, o)
		return s.finishApplicationUpdate(ctx, o, v.Revision, receipt, err)
	case "applying":
		receipt, err := s.appUpdates.controller.Status(ctx, o)
		return s.finishApplicationUpdate(ctx, o, v.Revision, receipt, err)
	}
	return nil
}
func (s *Store) finishApplicationUpdate(ctx context.Context, o ApplicationUpdateOperation, revision int64, r ApplicationUpdateReceipt, err error) error {
	if errors.Is(err, ErrApplicationUpdateNotStarted) {
		o.Phase = "check-failed"
		o.CheckedAt = s.now().Unix()
		o.FinishedAt = o.CheckedAt
		o.Failure = "Release is no longer eligible. Check for updates again."
		return s.publishApplicationUpdate(ctx, o, revision)
	}
	if err != nil || r.ID != o.ID || o.Release == nil || r.Sequence != o.Release.Sequence || r.ManifestSHA256 != o.Release.ManifestSHA256 || !oneOf(r.Phase, "applying", "completed", "rolled-back", "recovery") {
		o.Phase = "recovery"
		o.Failure = "Update outcome is uncertain. Inspect recovery before retrying."
	} else if r.Phase == "applying" {
		return nil
	} else {
		o.Phase = r.Phase
		o.FinishedAt = s.now().Unix()
		o.Failure = ""
		if r.Phase == "recovery" {
			o.Failure = "Update requires protected recovery."
		}
	}
	e := s.publishApplicationUpdate(ctx, o, revision)
	if errors.Is(e, ErrConflict) {
		current, _, readErr := readApplicationUpdate(ctx, s.db)
		if readErr == nil && current.ID == o.ID && oneOf(current.Phase, "completed", "rolled-back", "recovery") {
			return nil
		}
	}
	return e
}
func (s *Store) publishApplicationUpdate(ctx context.Context, o ApplicationUpdateOperation, revision int64) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = saveApplicationUpdate(ctx, tx, o, revision); e != nil {
		return e
	}
	return tx.Commit()
}

// BindApplicationUpdateController is a Go-only composition boundary for an
// authenticated owner controller, not a public activation/configuration API.
// Ordinary constructors never call it. Binding cannot replace a running driver.
func (s *Store) BindApplicationUpdateController(c ApplicationUpdateController) error {
	if c == nil {
		return errors.New("authenticated controller required")
	}
	s.appUpdates.mu.Lock()
	defer s.appUpdates.mu.Unlock()
	if s.appUpdates.configured.Load() {
		return errors.New("update controller already bound")
	}
	s.appUpdates.controller = c
	s.appUpdates.configured.Store(true)
	return nil
}

// The update waits while existing work reconciles; it does not ask for an AI
// account or turn a deterministic reminder into provider-dependent work.
func (s *Store) applicationUpdateBusy(ctx context.Context) (bool, error) {
	if !s.aiMu.TryLock() {
		return true, nil
	}
	s.aiMu.Unlock()
	if !s.newsMu.TryLock() {
		return true, nil
	}
	s.newsMu.Unlock()
	d, _, e := readAgents(ctx, s.db)
	if e != nil {
		return false, e
	}
	for _, r := range d.AIRequests {
		if r.HeldMicrousd > 0 || oneOf(r.State, "queued", "dispatching", "running", "unknown", "uncertain") {
			return true, nil
		}
	}
	return false, nil
}

func applicationUpdateNotice(o ApplicationUpdateOperation, p UpdatePolicy) *ApplicationUpdateNotice {
	if !p.Notify {
		return nil
	}
	titles := map[string]string{"available": "Update available", "completed": "Updated", "rolled-back": "Previous version restored", "recovery": "Recovery required", "check-failed": "Check failed"}
	title, ok := titles[o.Phase]
	if !ok {
		return nil
	}
	return &ApplicationUpdateNotice{ID: update.SnapshotDigest("application-update-notice", []string{o.ID, o.Phase}), Title: title, Read: o.NoticeReadPhase == o.Phase}
}
