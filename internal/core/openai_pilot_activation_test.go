package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func preparedPilot(t *testing.T) OpenAIPilotPreparation {
	t.Helper()
	root := fixtureDir(t)
	p, e := PrepareOpenAIPilotActivation(t.Context(), filepath.Join(root, "instance"), filepath.Join(root, "review"))
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestOpenAIPilotPreparationInertAndExclusive(t *testing.T) {
	p := preparedPilot(t)
	root := filepath.Dir(p.Target.Directory)
	if p.State != "review-required-not-applied" || p.RealAIEnabled || p.CredentialsCreated || p.PermitCreated || openAIPilotActivationEnabled || compiledOpenAIPilotBinding != (pilotActivationBinding{}) {
		t.Fatal("preparation activated capability")
	}
	patch, e := ReadPrivateFile(filepath.Join(root, "review/activation.patch"))
	if e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(patch)
	if hex.EncodeToString(h[:]) != p.PatchSHA256 || !bytes.Contains(patch, []byte("+const openAIPilotActivationEnabled = true")) || !bytes.Contains(patch, []byte(p.Target.InstanceID)) {
		t.Fatal("unreviewable activation patch")
	}
	command := exec.Command("git", "apply", "--check", filepath.Join(root, "review/activation.patch"))
	command.Dir = "../.."
	if b, e := command.CombinedOutput(); e != nil {
		t.Fatal("patch does not apply", e, string(b))
	}
	actual, e := os.ReadFile("openai_pilot_target.go")
	if e != nil || !bytes.Equal(actual, closedPilotTargetSource) {
		t.Fatal("preparation changed target source")
	}
	s, e := OpenStore(p.Target.Directory)
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.AIConnection()
	if e != nil || v.Ref != "" || v.LiveCallsEnabled || v.PilotAvailable || v.PilotEnrollment || s.pilot.target != nil {
		t.Fatal("prepared metadata acted as runtime configuration", e)
	}
	for _, name := range []string{"ai-credentials.json", "claim-code.json", "instance.lock"} {
		if _, e := os.Stat(filepath.Join(p.Target.Directory, name)); !os.IsNotExist(e) {
			t.Fatal("unexpected credential/owner/service state", name)
		}
	}
	_ = s.Close()
	before, _ := os.ReadFile(filepath.Join(p.Target.Directory, "core.sqlite"))
	if _, e := PrepareOpenAIPilotActivation(t.Context(), p.Target.Directory, filepath.Join(root, "other-review")); e == nil {
		t.Fatal("adopted existing target")
	}
	after, _ := os.ReadFile(filepath.Join(p.Target.Directory, "core.sqlite"))
	if !bytes.Equal(before, after) {
		t.Fatal("existing metadata modified")
	}
}

func TestOpenAIPilotActivationPreflightPreservesWrongTarget(t *testing.T) {
	p := preparedPilot(t)
	dbPath := filepath.Join(p.Target.Directory, "core.sqlite")
	before, _ := os.ReadFile(dbPath)
	for _, mutate := range []func(*pilotActivationBinding){
		func(b *pilotActivationBinding) {
			b.Directory = filepath.Join(filepath.Dir(b.Directory), "never-create")
		},
		func(b *pilotActivationBinding) { b.InstanceID = strings.Repeat("x", 43) },
		func(b *pilotActivationBinding) { b.ConnectionRef = "invalid" },
		func(b *pilotActivationBinding) { b.Model = legacyOpenAIModel },
		func(b *pilotActivationBinding) { b.PolicyID = "changed" },
	} {
		bad := p.Target
		mutate(&bad)
		if e := preflightOpenAIPilotBinding(p.Target.Directory, bad, true); e == nil {
			t.Fatal("wrong binding admitted")
		}
	}
	if e := preflightOpenAIPilotBinding(p.Target.Directory, p.Target, true); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(dbPath)
	if !bytes.Equal(before, after) {
		t.Fatal("read-only preflight changed database")
	}
	if _, e := os.Stat(filepath.Join(filepath.Dir(p.Target.Directory), "never-create")); !os.IsNotExist(e) {
		t.Fatal("wrong target created")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, e := os.Stat(dbPath + suffix); !os.IsNotExist(e) {
			t.Fatal("preflight created journal sidecar", suffix)
		}
	}
}

func TestOpenAIPilotActivationRuntimeOptions(t *testing.T) {
	if !pilotStandaloneOptionsAllowed(false, false, "", nil) || !pilotStandaloneOptionsAllowed(false, false, "", []string{""}) {
		t.Fatal("ordinary Local CLI empty optional signer was rejected")
	}
	for _, options := range []struct {
		fixture, session bool
		origin           string
		custody          []string
	}{
		{fixture: true}, {session: true}, {origin: "https://private.example"}, {custody: []string{"signer.json"}}, {custody: []string{"", ""}},
	} {
		if pilotStandaloneOptionsAllowed(options.fixture, options.session, options.origin, options.custody) {
			t.Fatal("pilot admitted fixture/remote/signer mode")
		}
	}
	p := preparedPilot(t)
	var calls atomic.Int32
	s := boundPilotFixture(t, p, &calls)
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	server := &Server{Store: s, HTTP: &http.Server{Handler: h}}
	if server.ConfigureConnection("local") != nil || server.ConfigureWENResearch("") != nil {
		t.Fatal("closed external inputs broke Local setup")
	}
	if server.ConfigureConnection("remote") == nil || server.ConfigureWENResearch("http://127.0.0.1:1234") == nil {
		t.Fatal("target widened to Remote/external research")
	}
}

func TestOpenAIPilotActivationSyntheticInputsOnly(t *testing.T) {
	p := preparedPilot(t)
	s, e := OpenStore(p.Target.Directory)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.now = func() time.Time { return time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC) }
	v := newsAdd(t, s, "trading-analyst")
	v = agentApply(t, s, AgentCommand{Action: "news-fetch", ProfileID: v.ProfileID, ID: v.NewsSources[0].ID})
	b := p.Target
	if e = preflightOpenAIPilotBinding(s.dir, b, true); e != nil {
		t.Fatal("identity precondition", e)
	}
	policy := openAIModelPolicy()
	if e = validateAIModelPolicy(AgentAIRequest{Model: b.Model, ConnectionRef: b.ConnectionRef, Policy: &policy, Limits: p.Limits}, s.now().Unix()); e != nil {
		t.Fatal("policy precondition", e)
	}
	if e = s.bindOpenAIPilot(b, true); e == nil || !strings.Contains(e.Error(), "synthetic local inputs") {
		t.Fatal("news isolation did not cause refusal", e)
	}
	// Defense in depth for an already-queued fetch; this private test assignment
	// cannot occur through a runtime command or preparation artifact.
	s.pilot.binding = &b
	before := retainedRevisionState(t, s)
	if _, e = s.ChangeAgents(t.Context(), AgentCommand{Action: "news-source-add", Revision: v.Revision, ProfileID: v.ProfileID, Name: "Would fetch", Source: fixtureNewsURL, NewsMaxAgeSeconds: 600}); e == nil {
		t.Fatal("pilot accepted source configuration")
	}
	if e = s.advanceAgentNews(t.Context(), aiTestTransport(func(*http.Request) (*http.Response, error) {
		t.Error("pilot contacted news endpoint")
		return nil, os.ErrPermission
	}), true); e != nil {
		t.Fatal(e)
	}
	if before != retainedRevisionState(t, s) {
		t.Fatal("blocked source/fetch changed metadata")
	}
}

func boundPilotFixture(t *testing.T, p OpenAIPilotPreparation, calls *atomic.Int32) *Store {
	t.Helper()
	s, e := OpenStore(p.Target.Directory)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	if e = s.bindOpenAIPilot(p.Target, true); e != nil {
		t.Fatal(e)
	}
	// Only this Go test swaps the fixed-origin transport's TCP destination/CA.
	// The ordinary build remains closed; no enabled candidate is built or run.
	s.pilot.transport = pilotHTTPS(t, calls).(*openAITransport)
	return s
}

func TestOpenAIPilotActivationBootstrapUsesExistingCore(t *testing.T) {
	p := preparedPilot(t)
	var calls atomic.Int32
	s := boundPilotFixture(t, p, &calls)
	view, e := s.AIConnection()
	if e != nil || !view.PilotEnrollment || len(view.CredentialModes) != 1 || view.CredentialModes[0] != "openai" || len(view.Catalog.Providers) != 1 || len(view.Catalog.Models) != 1 {
		t.Fatal("unscoped enrollment", e)
	}
	view, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Provider: "openai", CredentialMode: "openai", Key: disposableAPIInput, Passphrase: testVaultPassword, Name: "Pilot"})
	if e != nil || view.Ref != p.Target.ConnectionRef || view.Locked || calls.Load() != 0 || view.LiveCallsEnabled || s.aiExecutionAvailable(false) {
		t.Fatal("bootstrap dispatched or retargeted", e)
	}
	vault, _ := os.ReadFile(s.aiVaultPath())
	if bytes.Contains(vault, []byte(disposableAPIInput)) {
		t.Fatal("unencrypted credential")
	}
	for _, command := range []AIConnectionCommand{
		{Action: "add", Provider: "anthropic", CredentialMode: "anthropic", Key: disposableAPIInput, Passphrase: testVaultPassword},
		{Action: "add", Provider: "openai", CredentialMode: "openai", Key: disposableAPIInput, Passphrase: testVaultPassword},
		{Action: "save", Ref: view.Ref, Provider: "openai", CredentialMode: "openai", Key: disposableAPIInput + "replacement", Passphrase: testVaultPassword},
	} {
		if _, e = s.ChangeAIConnection(t.Context(), command); e == nil {
			t.Fatal("extra/replacement account admitted")
		}
	}
	after, _ := os.ReadFile(s.aiVaultPath())
	if !bytes.Equal(vault, after) {
		t.Fatal("refused enrollment changed vault")
	}
	v, _ := s.Agents(t.Context(), p.Target.ProfileID, "")
	review, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-review", Ref: view.Ref, ProfileID: v.ProfileID, Revision: v.Revision})
	if e != nil {
		t.Fatal(e)
	}
	allowance := *review.PilotReview
	view, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-allow", Ref: view.Ref, Pilot: &allowance, ConsentDigest: allowance.ID, Passphrase: testVaultPassword})
	if e != nil || calls.Load() != 1 || !view.LiveCallsEnabled || view.Catalog.State != "ready" || len(view.Catalog.Models) != 1 || !view.Catalog.Models[0].Selectable {
		t.Fatal("one scoped listing was not joined to consent", e, view.Catalog.State, calls.Load())
	}
	_, permission := pilotChat(t, s, allowance)
	v = agentApply(t, s, AgentCommand{Action: "chat-send", ProfileID: allowance.ProfileID, ID: permission.SessionID, Text: "A fictional market note.", ConsentDigest: permission.ID})
	if e = s.advanceAgentAI(t.Context(), s.providerRuntimeTransport(), false); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), allowance.ProfileID, "")
	if v.AIRequests[len(v.AIRequests)-1].State != "completed" || calls.Load() != 4 {
		t.Fatal("scoped runtime did not use existing worker", calls.Load())
	}
	v = agentApply(t, s, AgentCommand{Action: "task-ai-prepare", ProfileID: allowance.ProfileID, Model: allowance.Model, ConnectionRef: allowance.ConnectionRef, ID: permission.SessionID, Name: "Pilot task", Text: "Summarize the fictional note.", IntervalSeconds: 60, MaxRuns: 1, AuthorizationSeconds: 300, TotalBudgetMicrousd: 100000, Limits: &allowance.Limits})
	v = taskAIAuthorize(t, s, v)
	task := v.Tasks[len(v.Tasks)-1]
	grant, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-review", Ref: view.Ref, ProfileID: v.ProfileID, Revision: v.Revision, TaskIDs: []string{task.ID}, AuthorizationSeconds: 300})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "task-access-allow", Ref: view.Ref, Allowance: grant.AccessReview, ConsentDigest: grant.AccessReview.ID, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	v = taskAIRun(t, s, v)
	if e = s.advanceAgentAI(t.Context(), s.providerRuntimeTransport(), false); e != nil {
		t.Fatal(e)
	}
	v, _ = s.Agents(t.Context(), allowance.ProfileID, "")
	if v.Tasks[len(v.Tasks)-1].State != "completed" || calls.Load() != 7 {
		t.Fatal("task bypassed scoped shared engine", calls.Load())
	}
	v = agentApply(t, s, AgentCommand{Action: "task-run-explain", ProfileID: v.ProfileID, ID: v.Runs[len(v.Runs)-1].ID})
	v = agentApply(t, s, AgentCommand{Action: "memory-chat-save", ProfileID: v.ProfileID, ID: v.Messages[len(v.Messages)-1].ID, Source: "message", Text: "Retain the fictional note."})
	if len(v.Memories) != 1 {
		t.Fatal("memory source missing")
	}
	if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-revoke", Ref: view.Ref}); e != nil {
		t.Fatal(e)
	}
	_, _ = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "models-refresh", Ref: view.Ref})
	if calls.Load() != 7 {
		t.Fatal("post-revocation network call")
	}
	_ = s.Close()
	n := boundPilotFixture(t, p, &calls)
	if n.aiExecutionAvailable(false) || n.pilot.permit != nil {
		t.Fatal("restart restored pilot authority")
	}
	now, _ := n.Agents(t.Context(), p.Target.ProfileID, "")
	if len(now.Memories) != 1 || len(now.AIRequests) != 2 {
		t.Fatal("restart lost source records")
	}
	if _, e = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: p.Target.ConnectionRef, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	if _, e = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-review", Ref: p.Target.ConnectionRef, ProfileID: now.ProfileID, Revision: now.Revision}); e == nil {
		t.Fatal("used profile rearmed after restart")
	}
	if calls.Load() != 7 {
		t.Fatal("restart or unlock replay")
	}
}

func TestOpenAIPilotActivationInterruptedEnrollmentPreservesState(t *testing.T) {
	p := preparedPilot(t)
	var calls atomic.Int32
	s := boundPilotFixture(t, p, &calls)
	if e := os.Mkdir(s.aiVaultPath(), 0700); e != nil {
		t.Fatal(e)
	}
	c := AIConnectionCommand{Action: "save", Provider: "openai", CredentialMode: "openai", Key: disposableAPIInput, Passphrase: testVaultPassword}
	if _, e := s.ChangeAIConnection(t.Context(), c); e == nil {
		t.Fatal("foreign credential path overwritten")
	}
	if e := os.Remove(s.aiVaultPath()); e != nil {
		t.Fatal(e)
	}
	v, e := s.ChangeAIConnection(t.Context(), c)
	if e != nil || v.Ref != p.Target.ConnectionRef || calls.Load() != 0 {
		t.Fatal("recoverable first save failed", e)
	}
	_ = s.Close()
	n := boundPilotFixture(t, p, &calls)
	if _, e = n.ChangeAIConnection(t.Context(), c); e == nil {
		t.Fatal("interrupted enrollment replaced committed vault")
	}
	if _, e = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: v.Ref, Passphrase: testVaultPassword}); e != nil {
		t.Fatal(e)
	}
	if n.pilot.permit != nil || calls.Load() != 0 {
		t.Fatal("unlock became permission")
	}
	var envelope aiVaultEnvelope
	raw, _ := os.ReadFile(n.aiVaultPath())
	if json.Unmarshal(raw, &envelope) != nil || envelope.Ref != p.Target.ConnectionRef || envelope.Version != 2 {
		t.Fatal("vault identity lost")
	}
}

func TestOpenAIPilotActivationArchivedAttemptsCannotRearm(t *testing.T) {
	for _, outcome := range []string{"completed", "zero-cost-failed"} {
		t.Run(outcome, func(t *testing.T) {
			p := preparedPilot(t)
			var calls atomic.Int32
			s := boundPilotFixture(t, p, &calls)
			account, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "save", Provider: "openai", CredentialMode: "openai", Key: disposableAPIInput, Passphrase: testVaultPassword})
			if e != nil {
				t.Fatal(e)
			}
			v, _ := s.Agents(t.Context(), p.Target.ProfileID, "")
			r, e := s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-review", Ref: account.Ref, ProfileID: v.ProfileID, Revision: v.Revision})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-allow", Ref: account.Ref, Pilot: r.PilotReview, ConsentDigest: r.PilotReview.ID, Passphrase: testVaultPassword}); e != nil {
				t.Fatal(e)
			}
			_, permission := pilotChat(t, s, *r.PilotReview)
			agentApply(t, s, AgentCommand{Action: "chat-send", ProfileID: v.ProfileID, ID: permission.SessionID, Text: "Fictional archive test.", ConsentDigest: permission.ID})
			if outcome == "zero-cost-failed" {
				s.pilot.transport = httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.URL.Path == "/v1/models/"+p.Target.Model {
						admissionReply(w, map[string]any{"id": p.Target.Model, "object": "model", "created": 1744588800, "owned_by": "system", "shutdown_date": nil})
					} else if r.URL.Path == "/v1/responses/input_tokens" {
						admissionReply(w, map[string]any{"object": "response.input_tokens", "input_tokens": 2001})
					} else {
						t.Error("refused input generated response")
					}
				}).(testOpenAITransport).next
			}
			if e = s.advanceAgentAI(t.Context(), s.providerRuntimeTransport(), false); e != nil {
				t.Fatal(e)
			}
			v, _ = s.Agents(t.Context(), v.ProfileID, "")
			q := v.AIRequests[len(v.AIRequests)-1]
			if q.DispatchBoot == "" || q.HeldMicrousd != 0 || (outcome == "zero-cost-failed" && (q.State != "failed" || q.SpentMicrousd != 0)) {
				t.Fatal("invalid attempted fixture", q.State, q.SpentMicrousd)
			}
			agentApply(t, s, AgentCommand{Action: "chat-revoke", ProfileID: v.ProfileID, ID: permission.SessionID, ConsentDigest: permission.ID})
			v = agentApply(t, s, AgentCommand{Action: "session-archive", ProfileID: v.ProfileID, ID: permission.SessionID})
			if len(v.AIRequests) != 0 {
				t.Fatal("request did not enter immutable history")
			}
			_ = s.Close()
			n := boundPilotFixture(t, p, &calls)
			if _, e = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "unlock", Ref: account.Ref, Passphrase: testVaultPassword}); e != nil {
				t.Fatal(e)
			}
			after, _ := n.Agents(t.Context(), v.ProfileID, "")
			if _, e = n.ChangeAIConnection(t.Context(), AIConnectionCommand{Action: "pilot-review", Ref: account.Ref, ProfileID: after.ProfileID, Revision: after.Revision}); e == nil {
				t.Fatal("archived attempt replenished pilot")
			}
		})
	}
}
