package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"go/format"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The emitted review is not runtime configuration. There is no loader for it.
type pilotActivationBinding struct {
	Directory     string `json:"directory"`
	InstanceID    string `json:"instanceId"`
	ConnectionRef string `json:"connectionRef"`
	ProfileID     string `json:"profileId"`
	Model         string `json:"model"`
	PolicyID      string `json:"policyId"`
}

//go:embed openai_pilot_target.go
var closedPilotTargetSource []byte

type OpenAIPilotPreparation struct {
	Version            string                 `json:"version"`
	State              string                 `json:"state"`
	Target             pilotActivationBinding `json:"target"`
	Schema             int                    `json:"schema"`
	Policy             OpenAIModelPolicy      `json:"policy"`
	Limits             AILimits               `json:"limits"`
	MaxRequests        int                    `json:"maxRequests"`
	BudgetMicrousd     int64                  `json:"budgetMicrousd"`
	PatchSHA256        string                 `json:"patchSha256"`
	CredentialsCreated bool                   `json:"credentialsCreated"`
	PermitCreated      bool                   `json:"permitCreated"`
	RealAIEnabled      bool                   `json:"realAIEnabled"`
}

func preflightOpenAIPilotBinding(dir string, b pilotActivationBinding, enabled bool) error {
	if !enabled {
		return nil
	}
	abs, e := filepath.Abs(dir)
	if e != nil || abs != b.Directory || !filepath.IsAbs(b.Directory) || !validConnectionRef(b.InstanceID) || !validConnectionRef(b.ConnectionRef) || b.ProfileID == "" || b.Model != admittedOpenAIModel || b.PolicyID != openAIModelPolicy().ID {
		return errors.New("pilot target mismatch; ordinary instance preserved")
	}
	canonical, e := filepath.EvalSymlinks(abs)
	if e != nil || canonical != abs {
		return errors.New("canonical pilot directory required")
	}
	// Refuse a swapped database before migrations, WAL setup or any state write.
	u := url.URL{Scheme: "file", Path: filepath.Join(abs, "core.sqlite")}
	// Read only the prepared identity/schema pages, without creating WAL/SHM.
	// The ordinary connection later recovers all journal state authoritatively.
	db, e := sql.Open("sqlite", u.String()+"?mode=ro&immutable=1")
	if e != nil {
		return errors.New("prepared pilot metadata unavailable")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var instance string
	var schema int
	if db.QueryRowContext(ctx, "SELECT instance_id FROM instance WHERE id=1").Scan(&instance) != nil || instance != b.InstanceID || db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema) != nil || schema != StateSchema {
		return errors.New("pilot identity/schema mismatch; preserve prepared metadata")
	}
	return nil
}

func (s *Store) bindOpenAIPilot(b pilotActivationBinding, enabled bool) error {
	if !enabled {
		return nil
	}
	if e := preflightOpenAIPilotBinding(s.dir, b, true); e != nil {
		return e
	}
	d, _, e := readAgents(context.Background(), s.db)
	if e != nil || agentProfile(d, b.ProfileID) < 0 {
		return errors.New("prepared pilot agent unavailable")
	}
	if len(d.NewsSources) > 0 || len(d.NewsFetches) > 0 {
		return errors.New("pilot target must contain synthetic local inputs only")
	}
	policy := openAIModelPolicy()
	if validateAIModelPolicy(AgentAIRequest{Model: b.Model, ConnectionRef: b.ConnectionRef, Policy: &policy, Limits: AILimits{2000, 256, 5, 100000}}, s.now().Unix()) != nil {
		return errors.New("pilot model policy expired; review activation again")
	}
	col, e := s.readAIVaultCollection()
	absent := errors.Is(e, os.ErrNotExist)
	if !absent && (e != nil || len(col.Accounts) != 1 || col.Default != b.ConnectionRef || col.Accounts[0].Ref != b.ConnectionRef || aiVaultProvider(col.Accounts[0]) != "openai" || aiVaultMode(col.Accounts[0]) != "openai") {
		return errors.New("pilot account mismatch; preserve credential vault")
	}
	history, e := pilotProfileHasHistory(context.Background(), s.db, b.ProfileID)
	if e != nil {
		return e
	}
	if absent && (len(d.AIRequests) > 0 || history) {
		return errors.New("pilot credential missing after recorded requests; preserve holds")
	}
	transport := newOpenAITransport()
	transport.enabled = true // Reached only by reviewed compiled binding; never ordinary runtime.
	s.pilot.binding = &b
	s.pilot.bootstrap = absent
	s.pilot.transport = transport
	s.pilot.target = &openAIPilotTarget{b.InstanceID, b.ConnectionRef, b.ProfileID, b.Model}
	return nil
}

// Any immutable conversation history disqualifies this one-time, unused-profile
// pilot. This includes failed zero-cost attempts; aggregate spending is not an
// attempt ledger. A corrupt/missing history table also fails closed.
func pilotProfileHasHistory(ctx context.Context, reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, profile string) (bool, error) {
	var found bool
	e := reader.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agent_history WHERE profile=? AND kind='conversations')", profile).Scan(&found)
	return found, e
}

func (s *Store) pilotBindingActiveLocked() bool {
	b := s.pilot.binding
	return b != nil && b.Directory == s.dir && b.InstanceID == s.instanceID && b.Model == admittedOpenAIModel && b.PolicyID == openAIModelPolicy().ID && s.pilotTargetLocked(b.ConnectionRef)
}

func (s *Store) pilotBootstrapAllowedLocked(c AIConnectionCommand, provider, mode string) bool {
	if !s.pilotBindingActiveLocked() || !s.pilot.bootstrap || s.pilot.used || (c.Action != "save" && c.Action != "add") || c.Ref != "" || provider != "openai" || mode != "openai" {
		return false
	}
	_, e := s.readAIVaultCollection()
	return errors.Is(e, os.ErrNotExist)
}

func (s *Store) pilotTransport(ref string) *openAITransport {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	if !s.pilotBindingActiveLocked() || (ref != "" && ref != s.pilot.binding.ConnectionRef) {
		return nil
	}
	return s.pilot.transport
}
func (s *Store) providerRuntimeTransport() http.RoundTripper {
	if t := s.pilotTransport(""); t != nil {
		return t
	}
	return newOpenAITransport()
}
func pilotStandaloneOptionsAllowed(fixture, sessionFixture bool, publicOrigin string, custodyConfig []string) bool {
	return !fixture && !sessionFixture && publicOrigin == "" && (len(custodyConfig) == 0 || len(custodyConfig) == 1 && custodyConfig[0] == "")
}
func (s *Store) providerLiveEnabled() bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	return providerActivationEnabled && s.provider.apiCredentials || s.pilotBindingActiveLocked() && s.pilotCurrentLocked()
}
func (s *Store) deferPilotDiscovery() bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	return s.pilotBindingActiveLocked() && !s.pilotCurrentLocked()
}
func (s *Store) pilotSyntheticOnly() bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	return s.pilot.binding != nil
}

// Preparation creates fresh metadata with the existing engine, no listener,
// credential, owner password or grant. Existing paths are never adopted.
func PrepareOpenAIPilotActivation(ctx context.Context, instanceDir, reviewDir string) (OpenAIPilotPreparation, error) {
	var result OpenAIPilotPreparation
	if os.Geteuid() == 0 || openAIPilotActivationEnabled || compiledOpenAIPilotBinding != (pilotActivationBinding{}) {
		return result, errors.New("prepare from the ordinary closed build as an ordinary user")
	}
	canonicalNew := func(dir string) (string, error) {
		abs, e := filepath.Abs(dir)
		if e != nil {
			return "", e
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(abs))
		if e != nil {
			return "", e
		}
		abs = filepath.Join(parent, filepath.Base(abs))
		if _, e = os.Lstat(abs); !errors.Is(e, os.ErrNotExist) {
			return "", errors.New("preparation requires a new private directory")
		}
		return abs, nil
	}
	instance, e := canonicalNew(instanceDir)
	if e != nil {
		return result, e
	}
	review, e := canonicalNew(reviewDir)
	if e != nil {
		return result, e
	}
	if instance == review || strings.HasPrefix(instance, review+string(os.PathSeparator)) || strings.HasPrefix(review, instance+string(os.PathSeparator)) {
		return result, errors.New("separate new instance and review directories required")
	}
	if e = os.Mkdir(review, 0700); e != nil {
		return result, e
	}
	if e = os.Mkdir(instance, 0700); e != nil {
		return result, e
	}
	s, e := OpenStore(instance)
	if e != nil {
		return result, e
	}
	defer s.Close()
	ref, e := randomToken()
	if e != nil {
		return result, e
	}
	b := pilotActivationBinding{instance, s.instanceID, ref, "trading-analyst", admittedOpenAIModel, openAIModelPolicy().ID}
	policy := openAIModelPolicy()
	limits := AILimits{2000, 256, 5, 100000}
	if validateAIModelPolicy(AgentAIRequest{Model: b.Model, ConnectionRef: b.ConnectionRef, Policy: &policy, Limits: limits}, s.now().Unix()) != nil {
		return result, errors.New("review current model policy before preparing activation")
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	newSource, e := format.Source([]byte(fmt.Sprintf("package core\n\n// Target-specific candidate: APPLY ONLY AFTER SEPARATE LIVE AUTHORIZATION.\nconst openAIPilotActivationEnabled = true\n\nvar compiledOpenAIPilotBinding = pilotActivationBinding{Directory:%q,InstanceID:%q,ConnectionRef:%q,ProfileID:%q,Model:%q,PolicyID:%q}\n", b.Directory, b.InstanceID, b.ConnectionRef, b.ProfileID, b.Model, b.PolicyID)))
	if e != nil {
		return result, e
	}
	oldLines := strings.Split(strings.TrimSuffix(string(closedPilotTargetSource), "\n"), "\n")
	newLines := strings.Split(strings.TrimSuffix(string(newSource), "\n"), "\n")
	patch := fmt.Sprintf("diff --git a/internal/core/openai_pilot_target.go b/internal/core/openai_pilot_target.go\n--- a/internal/core/openai_pilot_target.go\n+++ b/internal/core/openai_pilot_target.go\n@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines))
	for _, line := range oldLines {
		patch += "-" + line + "\n"
	}
	for _, line := range newLines {
		patch += "+" + line + "\n"
	}
	hash := sha256.Sum256([]byte(patch))
	result = OpenAIPilotPreparation{Version: "fased-openai-pilot-activation-review:v1", State: "review-required-not-applied", Target: b, Schema: StateSchema, Policy: policy, Limits: limits, MaxRequests: 4, BudgetMicrousd: 500000, PatchSHA256: hex.EncodeToString(hash[:])}
	f, e := os.OpenFile(filepath.Join(review, "activation.patch"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return result, e
	}
	_, writeErr := f.WriteString(patch)
	e = errors.Join(writeErr, f.Sync(), f.Close())
	if e != nil {
		return result, e
	}
	if e = WritePrivateJSON(filepath.Join(review, "review.json"), result); e != nil {
		return result, e
	}
	return result, nil
}
