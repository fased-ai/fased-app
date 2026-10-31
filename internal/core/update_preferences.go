package core

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/fased-ai/fased-app/internal/update"
)

// Optional setup metadata. No signer, scheduler, downloader or owner action
// consumes these records as permission. Existing snapshots hash the entire setup
// JSON and SQLite pause guards/backups already cover it.
type UpdatePolicy struct {
	Mode   string             `json:"mode,omitempty"`
	Notify bool               `json:"notify"`
	Pin    *UpdatePin         `json:"pin,omitempty"`
	Window *MaintenanceWindow `json:"window,omitempty"`
}
type UpdatePin struct {
	Sequence       string `json:"sequence"`
	ManifestSHA256 string `json:"manifestSha256"`
}
type MaintenanceWindow struct {
	TimeZone string `json:"timeZone"`
	Days     []int  `json:"days"`
	Start    string `json:"start"`
	End      string `json:"end"`
}
type UpdateCandidate struct {
	Release        string        `json:"release"`
	Sequence       string        `json:"sequence"`
	ManifestSHA256 string        `json:"manifestSha256"`
	Target         update.Target `json:"target"`
}
type StoredUpdateProposal struct {
	Candidate  UpdateCandidate `json:"candidate"`
	Digest     string          `json:"digest"`
	State      string          `json:"state"`
	CreatedAt  time.Time       `json:"createdAt"`
	DecisionAt *time.Time      `json:"decisionAt,omitempty"`
}
type UpdatePreferences struct {
	Version   int                    `json:"version"`
	Policy    UpdatePolicy           `json:"policy"`
	Proposals []StoredUpdateProposal `json:"proposals"`
}
type UpdatePreferencesRequest struct {
	Revision  int64            `json:"revision"`
	Action    string           `json:"action"`
	Policy    *UpdatePolicy    `json:"policy,omitempty"`
	Candidate *UpdateCandidate `json:"candidate,omitempty"`
	Digest    string           `json:"digest,omitempty"`
}
type UpdateProposalView struct {
	StoredUpdateProposal
	PinMatch bool `json:"pinMatch"`
	Verified bool `json:"verified"`
}
type UpdatePreferencesView struct {
	Version            int                  `json:"version"`
	Revision           int64                `json:"revision"`
	Policy             UpdatePolicy         `json:"policy"`
	Proposals          []UpdateProposalView `json:"proposals"`
	Notifications      []string             `json:"notifications"`
	EvaluatedAt        time.Time            `json:"evaluatedAt"`
	Window             string               `json:"window"`
	InstallAllowed     bool                 `json:"installAllowed"`
	ResumeAllowed      bool                 `json:"resumeAllowed"`
	FinancialExecution bool                 `json:"financialExecution"`
	AutomaticUpdates   bool                 `json:"automaticUpdates"`
}

const maxUpdateProposals = 8
const preserveUpdatePreferences = `CREATE TRIGGER IF NOT EXISTS preserve_update_preferences BEFORE UPDATE OF setup ON instance
WHEN json_type(OLD.setup,'$.updates') IS NOT NULL AND (json_type(NEW.setup,'$.updates') IS NULL OR json_type(NEW.setup,'$.updates') != 'object')
BEGIN SELECT RAISE(ABORT,'update preferences must be preserved; use a capable core'); END;`

func canonicalSequence(s string) bool {
	n, e := strconv.ParseUint(s, 10, 64)
	return e == nil && n > 0 && strconv.FormatUint(n, 10) == s
}
func validPin(p UpdatePin) bool {
	return canonicalSequence(p.Sequence) && hexValue(p.ManifestSHA256, 32)
}
func minute(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	for _, n := range []int{0, 1, 3, 4} {
		if s[n] < '0' || s[n] > '9' {
			return 0, false
		}
	}
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[3:])
	return h*60 + m, h < 24 && m < 60
}
func (w MaintenanceWindow) Validate() error {
	if len(w.TimeZone) > 64 || (w.TimeZone != "UTC" && !strings.Contains(w.TimeZone, "/")) {
		return errors.New("use UTC or an IANA region timezone")
	}
	if _, e := time.LoadLocation(w.TimeZone); e != nil {
		return errors.New("unknown IANA timezone")
	}
	start, a := minute(w.Start)
	end, b := minute(w.End)
	duration := (end - start + 1440) % 1440
	if !a || !b || duration < 15 || duration > 720 {
		return errors.New("window requires HH:MM bounds and 15 minutes to 12 hours")
	}
	if len(w.Days) < 1 || len(w.Days) > 7 {
		return errors.New("choose at least one start day")
	}
	previous := -1
	for _, d := range w.Days {
		if d < 0 || d > 6 || d <= previous {
			return errors.New("window days must be unique ascending weekdays 0 to 6")
		}
		previous = d
	}
	return nil
}

// Membership uses wall-clock minutes: both repeated DST minutes qualify and
// skipped minutes never occur. Overnight windows belong to their starting day.
// This is review guidance only, never a timer or an execution trigger.
func (w MaintenanceWindow) Contains(now time.Time) bool {
	if w.Validate() != nil {
		return false
	}
	zone, _ := time.LoadLocation(w.TimeZone)
	local := now.In(zone)
	n := local.Hour()*60 + local.Minute()
	day := int(local.Weekday())
	start, _ := minute(w.Start)
	end, _ := minute(w.End)
	if end > start {
		return hasDay(w.Days, day) && n >= start && n < end
	}
	return n >= start && hasDay(w.Days, day) || n < end && hasDay(w.Days, (day+6)%7)
}
func hasDay(days []int, day int) bool {
	for _, d := range days {
		if d == day {
			return true
		}
	}
	return false
}
func (p UpdatePolicy) Validate() error {
	if p.Mode != "" && p.Mode != "manual" && p.Mode != "automatic" {
		return errors.New("choose Manual or Automatic")
	}
	if p.Pin != nil && !validPin(*p.Pin) {
		return errors.New("pin requires a positive canonical sequence and lowercase SHA-256")
	}
	if p.Window != nil {
		return p.Window.Validate()
	}
	return nil
}
func (c UpdateCandidate) Validate() error {
	if !validName(c.Release) || !validPin(UpdatePin{c.Sequence, c.ManifestSHA256}) || !oneOf(c.Target.OS, "linux", "darwin", "windows") || !oneOf(c.Target.Arch, "amd64", "arm64") {
		return errors.New("proposal requires a public release label, canonical sequence, lowercase SHA-256 and supported target")
	}
	return nil
}
func candidateDigest(c UpdateCandidate) string {
	return update.SnapshotDigest("review-only-update-proposal-v1", c)
}
func defaultUpdatePreferences() UpdatePreferences {
	return UpdatePreferences{Version: 1, Policy: UpdatePolicy{Notify: true}, Proposals: []StoredUpdateProposal{}}
}
func (p UpdatePreferences) Validate() error {
	if p.Version != 1 || p.Proposals == nil || len(p.Proposals) > maxUpdateProposals {
		return errors.New("unsupported update preferences; preserve metadata")
	}
	if e := p.Policy.Validate(); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, r := range p.Proposals {
		if r.Candidate.Validate() != nil || r.Digest != candidateDigest(r.Candidate) || seen[r.Digest] || r.CreatedAt.IsZero() || !oneOf(r.State, "pending", "reviewed", "dismissed") || (r.State == "pending") != (r.DecisionAt == nil) || r.DecisionAt != nil && r.DecisionAt.IsZero() {
			return errors.New("damaged review-only proposal; preserve metadata")
		}
		seen[r.Digest] = true
	}
	return nil
}
func preferencesView(record SetupRecord, now time.Time) UpdatePreferencesView {
	p := defaultUpdatePreferences()
	if record.Settings.Updates != nil {
		p = *record.Settings.Updates
	}
	v := UpdatePreferencesView{Version: 1, Revision: record.Revision, Policy: p.Policy, Proposals: []UpdateProposalView{}, Notifications: []string{}, EvaluatedAt: now.UTC(), Window: "unrestricted"}
	if p.Policy.Window != nil {
		v.Window = "closed"
		if p.Policy.Window.Contains(now) {
			v.Window = "open"
		}
	}
	for _, r := range p.Proposals {
		match := p.Policy.Pin == nil || *p.Policy.Pin == (UpdatePin{r.Candidate.Sequence, r.Candidate.ManifestSHA256})
		v.Proposals = append(v.Proposals, UpdateProposalView{StoredUpdateProposal: r, PinMatch: match})
		if p.Policy.Notify && r.State == "pending" {
			v.Notifications = append(v.Notifications, r.Digest)
		}
	}
	return v
}
func (s *Store) UpdatePreferences(ctx context.Context) (UpdatePreferencesView, error) {
	r, e := s.Setup(ctx)
	if e != nil {
		return UpdatePreferencesView{}, e
	}
	return preferencesView(r, s.now()), nil
}
func (s *Store) ChangeUpdatePreferences(ctx context.Context, request UpdatePreferencesRequest) (UpdatePreferencesView, error) {
	s.appUpdates.mu.Lock()
	defer s.appUpdates.mu.Unlock()
	if request.Revision < 0 {
		return UpdatePreferencesView{}, errors.New("invalid revision")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return UpdatePreferencesView{}, e
	}
	defer tx.Rollback()
	r, e := readSetupRecord(ctx, tx)
	if e != nil {
		return UpdatePreferencesView{}, e
	}
	if r.Revision != request.Revision {
		return UpdatePreferencesView{}, ErrConflict
	}
	p := defaultUpdatePreferences()
	if r.Settings.Updates != nil {
		p = *r.Settings.Updates
		p.Proposals = append([]StoredUpdateProposal{}, p.Proposals...)
	}
	before := update.SnapshotDigest("preferences", p)
	now := s.now().UTC()
	switch request.Action {
	case "policy":
		if request.Policy == nil || request.Candidate != nil || request.Digest != "" {
			return UpdatePreferencesView{}, errors.New("exact policy input required")
		}
		p.Policy = *request.Policy
	case "propose":
		if request.Candidate == nil || request.Policy != nil || request.Digest != "" || request.Candidate.Validate() != nil {
			return UpdatePreferencesView{}, errors.New("exact public candidate metadata required; no verified origin is asserted")
		}
		digest := candidateDigest(*request.Candidate)
		found := false
		for _, r := range p.Proposals {
			if r.Digest == digest {
				found = true
			}
			if r.Candidate.Sequence == request.Candidate.Sequence && r.Candidate.ManifestSHA256 == request.Candidate.ManifestSHA256 && r.Digest != digest {
				return UpdatePreferencesView{}, errors.New("this claimed manifest already has different proposal metadata")
			}
		}
		if !found {
			if len(p.Proposals) >= maxUpdateProposals {
				return UpdatePreferencesView{}, errors.New("proposal limit reached; dismiss and remove an old record explicitly")
			}
			p.Proposals = append(p.Proposals, StoredUpdateProposal{Candidate: *request.Candidate, Digest: digest, State: "pending", CreatedAt: now})
		}
	case "review", "dismiss", "remove":
		if request.Policy != nil || request.Candidate != nil || !hexValue(request.Digest, 32) {
			return UpdatePreferencesView{}, errors.New("exact review digest required")
		}
		index := -1
		for n, r := range p.Proposals {
			if r.Digest == request.Digest {
				index = n
				break
			}
		}
		if index < 0 {
			return UpdatePreferencesView{}, errors.New("proposal digest not found; reload before reviewing")
		}
		entry := &p.Proposals[index]
		if request.Action == "remove" {
			if entry.State != "dismissed" {
				return UpdatePreferencesView{}, errors.New("dismiss a proposal before removing it")
			}
			p.Proposals = append(p.Proposals[:index], p.Proposals[index+1:]...)
		} else {
			state := "reviewed"
			if request.Action == "dismiss" {
				state = "dismissed"
			}
			if entry.State == "dismissed" && state == "reviewed" {
				return UpdatePreferencesView{}, errors.New("dismissed proposals cannot be reviewed")
			}
			if entry.State != state {
				entry.State = state
				entry.DecisionAt = &now
			}
		}
	default:
		return UpdatePreferencesView{}, errors.New("unsupported metadata action; installation and automatic execution are unavailable")
	}
	if e = p.Validate(); e != nil {
		return UpdatePreferencesView{}, e
	}
	if r.Settings.Updates != nil && before == update.SnapshotDigest("preferences", p) {
		return preferencesView(r, now), nil
	}
	r.Settings.Updates = &p
	b, e := json.Marshal(r.Settings)
	if e != nil {
		return UpdatePreferencesView{}, e
	}
	if len(b) > 12*1024 {
		return UpdatePreferencesView{}, errors.New("update metadata limit reached")
	}
	// Transactional publication also installs the older-writer preservation guard.
	if _, e = tx.ExecContext(ctx, preserveUpdatePreferences); e != nil {
		return UpdatePreferencesView{}, e
	}
	result, e := tx.ExecContext(ctx, "UPDATE instance SET setup=?,revision=revision+1 WHERE id=1 AND revision=?", string(b), r.Revision)
	if e != nil {
		return UpdatePreferencesView{}, metadataWriteError(e)
	}
	n, e := result.RowsAffected()
	if e != nil {
		return UpdatePreferencesView{}, e
	}
	if n != 1 {
		return UpdatePreferencesView{}, ErrConflict
	}
	if e = tx.Commit(); e != nil {
		return UpdatePreferencesView{}, e
	}
	r.Revision++
	return preferencesView(r, now), nil
}
