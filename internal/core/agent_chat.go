package core

// Chat permission covers a finite sequence of replies, not tools or task
// schedules. It is boot-, profile-, conversation-, provider- and model-bound.
import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fased-ai/fased-app/internal/update"
)

type ChatPermission struct {
	ConnectionEpoch   uint64   `json:"connectionEpoch,omitempty"`
	AccessEpoch       uint64   `json:"accessEpoch"`
	ID                string   `json:"id"`
	ProfileID         string   `json:"profileId"`
	SessionID         string   `json:"sessionId"`
	ConnectionRef     string   `json:"connectionRef"`
	Model             string   `json:"model"`
	MemoryMode        string   `json:"memoryMode"`
	PersonaDigest     string   `json:"personaDigest"`
	BootID            string   `json:"bootId"`
	Revision          int64    `json:"revision"`
	CreatedAt         int64    `json:"createdAt"`
	ExpiresAt         int64    `json:"expiresAt"`
	Limits            AILimits `json:"limits"`
	MaxRequests       int      `json:"maxRequests"`
	BudgetMicrousd    int64    `json:"budgetMicrousd"`
	UsedRequests      int      `json:"usedRequests"`
	AllocatedMicrousd int64    `json:"allocatedMicrousd"`
	Revoked           bool     `json:"revoked"`
}

func chatDigest(p ChatPermission) string {
	p.ID = ""
	p.UsedRequests = 0
	p.AllocatedMicrousd = 0
	p.Revoked = false
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func personaDigest(p AgentProfile) string {
	h := sha256.Sum256([]byte(p.Persona))
	return hex.EncodeToString(h[:])
}
func validAILimits(l AILimits) bool {
	return l.MaxInputTokens >= 1000 && l.MaxInputTokens <= 32000 && l.MaxOutputTokens >= 128 && l.MaxOutputTokens <= 1024 && l.DeadlineSeconds >= 1 && l.DeadlineSeconds <= 120 && l.BudgetMicrousd >= 1 && l.BudgetMicrousd <= 10000000
}
func validChatPermission(p ChatPermission, stored ...bool) bool {
	return validChatPermissionWithPolicy(p, chatModelPolicy(p.Model), stored...)
}
func validChatPermissionWithPolicy(p ChatPermission, policy *OpenAIModelPolicy, stored ...bool) bool {
	_, current := currentProviderModel(p.Model)
	modelOK := p.Model != legacyOpenAIModel && (p.Model == fixtureAIModel || current || (policy != nil && isDiscoveredPolicy(policy))) && p.Limits.BudgetMicrousd <= maxModelBudget(p.Model)
	reservation := aiRequestReservation(AgentAIRequest{Model: p.Model, Policy: policy, Limits: p.Limits})
	if len(stored) > 0 && stored[0] {
		modelOK = validProviderModelID(p.Model)
		reservation = 0
	}
	limitsOK := validAILimits(p.Limits)
	if len(stored) > 0 && stored[0] && p.ConnectionRef != "" && p.Limits.BudgetMicrousd == 0 && p.BudgetMicrousd == 0 && p.AllocatedMicrousd == 0 {
		// Structural storage admission is not execution authority. Every runtime
		// consumer below also resolves the immutable account's auth method.
		limitsOK = validPolicyLimits(p.Limits, &OpenAIModelPolicy{Provider: "local", WireVersion: localWire})
	}
	return len(p.ID) == 64 && len(p.PersonaDigest) == 64 && len(p.BootID) == 32 && (len(p.ConnectionRef) == 0 || len(p.ConnectionRef) == 43) && (p.Model == fixtureAIModel || p.ConnectionRef != "") && p.ID == chatDigest(p) && p.ProfileID != "" && p.SessionID != "" && len(p.BootID) > 0 && p.Revision >= 0 && p.CreatedAt > 0 && p.ExpiresAt >= p.CreatedAt+60 && p.ExpiresAt <= p.CreatedAt+86400 &&
		(p.MemoryMode == "relevant" || p.MemoryMode == "none") && modelOK && limitsOK && p.MaxRequests >= 1 && p.MaxRequests <= 20 && p.Limits.BudgetMicrousd >= reservation && p.BudgetMicrousd >= reservation && p.BudgetMicrousd <= 20000000 && p.UsedRequests >= 0 && p.UsedRequests <= p.MaxRequests && p.AllocatedMicrousd >= 0 && p.AllocatedMicrousd <= p.BudgetMicrousd
}
func isDiscoveredPolicy(p *OpenAIModelPolicy) bool {
	if p == nil || !validProviderModelID(p.Model) || p.Model == fixtureAIModel || p.Model == legacyOpenAIModel {
		return false
	}
	prov := policyProvider(p)
	return p.ID == fmt.Sprintf("%s-discovered-v1:%s", prov, p.Model) ||
		(prov == "openai" && p.ID == fmt.Sprintf("openai-discovered-v1:%s:sign-in-plan-v2", p.Model))
}
func registryCeilingPricing(reg modelRegistry) (input, cached, output int64, ok bool) {
	for _, m := range reg.Models {
		if (m.Currency != "" && m.Currency != "USD") || m.Input <= 0 || m.Output <= 0 {
			continue
		}
		if !ok || m.Input > input {
			input = m.Input
		}
		if m.Cached > cached {
			cached = m.Cached
		}
		if m.Output > output {
			output = m.Output
		}
		ok = true
	}
	return input, cached, output, ok
}
func dynamicModelPolicy(provider, model, method string) *OpenAIModelPolicy {
	if m, ok := currentProviderModel(model); ok && modelProvider(m) == provider {
		return chatModelPolicyForMethod(model, method)
	}
	if !validProviderModelID(model) || model == legacyOpenAIModel || model == fixtureAIModel || provider == "" || provider == "none" {
		return nil
	}
	if provider != "local" {
		if _, ok := providerDefinitionFor(provider); !ok {
			return nil
		}
	}
	reg := registryForProvider(provider)
	reviewedAt := reg.ReviewedAt
	if reviewedAt <= 0 {
		reviewedAt = 1
	}
	p := OpenAIModelPolicy{
		ID:            fmt.Sprintf("%s-discovered-v1:%s", provider, model),
		Model:         model,
		ReviewedAt:    reviewedAt,
		ExpiresAt:     0,
		ContextTokens: 128000,
		OutputTokens:  16384,
		Reasoning:     "none",
		ModelSource:   providerOrigin(provider) + providerModelsPath(provider),
		TariffSource:  reg.Sources["pricing"],
		TokenSource:   reg.Sources["contract"],
	}
	switch {
	case provider == "local":
		p.Provider = "local"
		p.WireVersion = localWire
		p.ModelSource = reg.Sources["discovery"]
		p.TokenSource = reg.Sources["discovery"]
		return &p
	case provider == "openai" && method == "sign-in":
		p.ID += ":sign-in-plan-v2"
		p.WireVersion = planSignInWire
		p.Reasoning = ""
		p.TariffSource = "https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference"
		p.TokenSource = "https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations"
		return &p
	case provider == "openai":
		in, cached, out, ok := registryCeilingPricing(reg)
		if !ok {
			return nil
		}
		p.Reasoning = ""
		p.InputMicrousdPerMillion, p.CachedMicrousdPerMillion, p.OutputMicrousdPerMillion = in, cached, out
		p.TokenSource = "https://developers.openai.com/api/reference/resources/responses/subresources/input_tokens/methods/count"
		return &p
	case provider == "kimi-code":
		p.Provider = "kimi-code"
		p.WireVersion = "kimi-code-chat-v1"
		p.Reasoning = "low"
		return &p
	case provider == "ollama-cloud":
		p.Provider = "ollama-cloud"
		p.WireVersion = "ollama-cloud-chat-v1"
		return &p
	case compatibleProvider(provider):
		in, cached, out, ok := registryCeilingPricing(reg)
		if !ok {
			return nil
		}
		p.Provider = provider
		p.WireVersion = provider + "-chat-v1"
		p.MaxRequestBudgetMicrousd = maxModelBudget(model)
		p.InputMicrousdPerMillion, p.CachedMicrousdPerMillion, p.OutputMicrousdPerMillion = in, cached, out
		if provider == "moonshot-intl" {
			p.CacheMode = "kimi-implicit-5m"
		}
		if provider == "openrouter" {
			p.CachedMicrousdPerMillion = in
		}
		return &p
	case provider == "anthropic":
		in, cached, out, ok := registryCeilingPricing(reg)
		if !ok {
			return nil
		}
		p.Provider = "anthropic"
		p.WireVersion = "anthropic-messages-v1"
		p.APIVersion = "2026-10-21"
		p.Reasoning = "low"
		p.MaxRequestBudgetMicrousd = maxModelBudget(model)
		p.InputMicrousdPerMillion, p.CachedMicrousdPerMillion, p.OutputMicrousdPerMillion = in, cached, out
		p.TokenSource = "https://platform.claude.com/docs/en/api/messages/count_tokens"
		return &p
	case provider == "xai":
		in, cached, out, ok := registryCeilingPricing(reg)
		if !ok {
			return nil
		}
		p.Provider = "xai"
		p.WireVersion = "xai-responses-v1"
		p.Reasoning = "low"
		p.MaxRequestBudgetMicrousd = maxModelBudget(model)
		p.InputMicrousdPerMillion, p.CachedMicrousdPerMillion, p.OutputMicrousdPerMillion = in, cached, out
		p.TokenSource = "https://docs.x.ai/developers/rest-api-reference/inference/other"
		return &p
	default:
		return nil
	}
}
func chatModelPolicy(model string) *OpenAIModelPolicy {
	if _, ok := currentProviderModel(model); ok || model == legacyOpenAIModel {
		p := openAIModelPolicy(model)
		return &p
	}
	return nil
}
func chatModelPolicyForMethod(model, method string) *OpenAIModelPolicy {
	p := chatModelPolicy(model)
	if method == "oauth" && p != nil && policyProvider(p) == "google" {
		return p
	}
	if method == "" || method == "api-key" || method == "local" && localPolicy(p) {
		return p
	}
	m, current := currentProviderModel(model)
	if method != "sign-in" || p == nil || !current || modelProvider(m) != "openai" {
		return nil
	}
	copy := *p
	copy.ID += ":sign-in-plan-v2"
	copy.WireVersion = planSignInWire
	copy.CacheMode = ""
	copy.InputMicrousdPerMillion, copy.CachedMicrousdPerMillion, copy.OutputMicrousdPerMillion = 0, 0, 0
	copy.TariffSource = "https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference"
	copy.TokenSource = "https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations"
	return &copy
}
func legacySignInModelPolicy(model string) *OpenAIModelPolicy {
	p := chatModelPolicy(model)
	m, current := currentProviderModel(model)
	if p == nil || !current || modelProvider(m) != "openai" {
		return nil
	}
	copy := *p
	copy.ID += ":sign-in-fixture"
	copy.WireVersion = "openai-signin-v1"
	copy.CacheMode = ""
	copy.TariffSource = "local:synthetic-fixture-allocation"
	copy.TokenSource = "local:conservative-signin-fixture-bound"
	return &copy
}
func (s *Store) chatPolicy(p ChatPermission) *OpenAIModelPolicy {
	if p.ConnectionRef != "" {
		connection, err := s.AIConnection(p.ConnectionRef)
		if err != nil || connection.Ref != p.ConnectionRef {
			return nil
		}
		return dynamicModelPolicy(connection.Provider, p.Model, connection.AuthMethod)
	}
	return chatModelPolicyForMethod(p.Model, "api-key")
}
func validChatPermissionPolicy(p ChatPermission, policy *OpenAIModelPolicy) bool {
	if !unmeteredPolicy(policy) {
		return validChatPermissionWithPolicy(p, policy)
	}
	return validChatPermissionWithPolicy(p, policy, true) && p.Limits.BudgetMicrousd == 0 && p.BudgetMicrousd == 0 && p.AllocatedMicrousd == 0 && validPolicyLimits(p.Limits, policy)
}
func (s *Store) validChatPermission(p ChatPermission) bool {
	return validChatPermissionPolicy(p, s.chatPolicy(p))
}
func (s *Store) chatPermissionReason(d agentDocument, p *ChatPermission, now int64) string {
	if p == nil {
		return "permission-required"
	}
	return chatPermissionReason(d, p, s.bootID, now, s.chatPolicy(*p))
}
func chatSession(d agentDocument, profile, id string) int {
	for i, s := range d.Sessions {
		if s.ProfileID == profile && s.ID == id {
			return i
		}
	}
	return -1
}
func chatPermissionReason(d agentDocument, p *ChatPermission, boot string, now int64, policies ...*OpenAIModelPolicy) string {
	if p == nil {
		return "permission-required"
	}
	policy := chatModelPolicy(p.Model)
	if len(policies) > 0 {
		policy = policies[0]
	}
	if !validChatPermissionPolicy(*p, policy) || p.Revoked {
		return "revoked"
	}
	if p.BootID != boot {
		return "restart-locked"
	}
	if now < p.CreatedAt || now >= p.ExpiresAt {
		return "expired"
	}
	pi := agentProfile(d, p.ProfileID)
	if pi < 0 || p.PersonaDigest != personaDigest(d.Profiles[pi]) {
		return "scope-changed"
	}
	cost := aiRequestReservation(AgentAIRequest{Model: p.Model, Policy: policy, Limits: p.Limits})
	if p.UsedRequests >= p.MaxRequests || p.AllocatedMicrousd+cost > p.BudgetMicrousd {
		return "exhausted"
	}
	return "ready"
}
func (s *Store) chatReview(d agentDocument, c AgentCommand, profile string, revision, now int64) (ChatPermission, error) {
	if c.Limits == nil || c.MemorySelection == nil || len(c.MemorySelection.IDs) != 0 || c.EvidenceSelection != nil || c.NewsSourceID != "" || chatSession(d, profile, c.ID) < 0 || c.AuthorizationSeconds < 60 || c.AuthorizationSeconds > 86400 {
		return ChatPermission{}, errors.New("review one conversation, memory scope, finite request/time/cost limits and no external inputs")
	}
	for _, q := range d.AIRequests {
		if q.ProfileID == profile && aiHasUsageHold(q) {
			return ChatPermission{}, errors.New("profile has pending or uncertain usage; inspect it before another reply")
		}
	}
	model := c.Model
	if model == "" {
		model = fixtureAIModel
	}
	p := ChatPermission{ProfileID: profile, SessionID: c.ID, ConnectionRef: c.ConnectionRef, Model: model, MemoryMode: c.MemorySelection.Mode, PersonaDigest: personaDigest(d.Profiles[agentProfile(d, profile)]), BootID: s.bootID, Revision: revision, CreatedAt: now, ExpiresAt: now + c.AuthorizationSeconds, Limits: *c.Limits, MaxRequests: c.MaxRuns, BudgetMicrousd: c.TotalBudgetMicrousd}
	p.AccessEpoch = s.chatEpoch.Load()
	p.ConnectionEpoch = s.connectionAccessEpoch(p.ConnectionRef)
	p.ID = chatDigest(p)
	if !s.validChatPermission(p) || validateAIModelPolicy(AgentAIRequest{Model: p.Model, ConnectionRef: p.ConnectionRef, Policy: s.chatPolicy(p), Limits: p.Limits}, now) != nil || !s.aiConnectionValid(p.ConnectionRef) || !s.pilotGrantAllowed(p.ConnectionRef, profile, p.Model, p.ExpiresAt, p.MaxRequests, p.BudgetMicrousd, p.Limits, false) {
		return p, errors.New("invalid Chat limits or locked connection")
	}
	return p, nil
}
func (s *Store) changeChat(d *agentDocument, c AgentCommand, profile, id string, revision, now int64, fixture bool) error {
	si := chatSession(*d, profile, c.ID)
	if si < 0 {
		return errors.New("profile conversation not found")
	}
	session := &d.Sessions[si]
	switch c.Action {
	case "chat-allow":
		p := c.ChatPermission
		if p != nil && !s.pilotGrantAllowed(p.ConnectionRef, profile, p.Model, p.ExpiresAt, p.MaxRequests, p.BudgetMicrousd, p.Limits, false) {
			return errors.New("Chat exceeds pilot allowance")
		}
		if p == nil || !s.validChatPermission(*p) || p.ID != c.ConsentDigest || p.ProfileID != profile || p.SessionID != c.ID || p.BootID != s.bootID || !s.chatAccessCurrent(p) || p.Revision != revision || p.CreatedAt > now || now-p.CreatedAt > 300 || p.ExpiresAt <= now || p.UsedRequests != 0 || p.AllocatedMicrousd != 0 || p.Revoked || p.PersonaDigest != personaDigest(d.Profiles[agentProfile(*d, profile)]) || !s.aiConnectionValid(p.ConnectionRef) {
			return errors.New("Chat scope changed; review permission again")
		}
		for _, q := range d.AIRequests {
			if q.ProfileID == profile && aiHasUsageHold(q) {
				return errors.New("pending or uncertain usage must remain held before new permission")
			}
		}
		copy := *p
		session.Chat = &copy
		return nil
	case "chat-revoke":
		if session.Chat == nil || c.ConsentDigest != session.Chat.ID {
			return errors.New("select current Chat permission")
		}
		session.Chat.Revoked = true
		for i := range d.AIRequests {
			q := &d.AIRequests[i]
			if q.ChatPermissionID == session.Chat.ID && (q.State == "queued" || q.State == "running") {
				if q.State == "queued" {
					q.HeldMicrousd = 0
				}
				q.State = "cancelled"
				q.Failure = "Chat permission revoked; dispatched usage remains held."
			}
		}
		return nil
	case "chat-send":
		p := session.Chat
		if !fixture {
			return errors.New("provider execution unavailable")
		}
		if p == nil || c.ConsentDigest != p.ID || (s.chatPermissionReason(*d, p, now) != "ready" || !s.chatAccessCurrent(p)) || !s.aiConnectionValid(p.ConnectionRef) {
			return errors.New("Chat permission unavailable; review current scope and connection")
		}
		for _, q := range d.AIRequests {
			if q.ProfileID == profile && aiHasUsageHold(q) {
				return errors.New("profile has pending or uncertain usage; inspect it before another reply")
			}
		}
		if taskAIRequestSlots(*d) >= 10 {
			return errors.New("Chat capacity reached; archive a settled conversation in History; linked or uncertain records stay protected")
		}
		cmd := AgentCommand{Action: "ai-prepare", ID: c.ID, Text: c.Text, Model: p.Model, ConnectionRef: p.ConnectionRef, Limits: &p.Limits, MemorySelection: &AIMemorySelection{Mode: p.MemoryMode}}
		if p.ConnectionRef != "" {
			connection, err := s.AIConnection(p.ConnectionRef)
			if err != nil || connection.Ref != p.ConnectionRef {
				return errors.New("selected Chat account unavailable")
			}
			cmd.providerName = connection.Provider
			cmd.providerAuthMethod = connection.AuthMethod
		}
		if e := changeAgentAI(d, cmd, profile, id, now, true, nil); e != nil {
			return e
		}
		q := &d.AIRequests[len(d.AIRequests)-1]
		q.ChatPermissionID = p.ID
		q.Stream = true
		q.Disclosure = ""
		wire, e := aiWire(*q)
		if e != nil || !aiWireWithinBound(*q, wire) {
			return errors.New("reply exceeds Chat input allowance; start a fresh conversation or review larger limits")
		}
		q.Disclosure = string(wire)
		q.Digest = aiDigest(*q)
		q.State = "queued"
		q.HeldMicrousd = aiRequestReservation(*q)
		p.UsedRequests++
		p.AllocatedMicrousd += q.HeldMicrousd
		return nil
	}
	return errors.New("unknown Chat operation")
}
func (s *Store) chatRequestValid(d agentDocument, q AgentAIRequest, now int64) bool {
	if q.ChatPermissionID == "" {
		return !q.Stream
	}
	si := chatSession(d, q.ProfileID, q.SessionID)
	if si < 0 {
		return false
	}
	p := d.Sessions[si].Chat
	// Exhaustion prevents new sends, not the already reserved final request.
	return p != nil && s.validChatPermission(*p) && !p.Revoked && p.ID == q.ChatPermissionID && p.BootID == s.bootID && s.chatAccessCurrent(p) && now >= p.CreatedAt && now < p.ExpiresAt && p.PersonaDigest == personaDigest(d.Profiles[agentProfile(d, q.ProfileID)]) && p.ConnectionRef == q.ConnectionRef && p.Model == q.Model && p.Limits == q.Limits && q.Stream && p.UsedRequests > 0
}
func validateChatStorage(raw []byte, d agentDocument) error {
	var root struct {
		Sessions []map[string]json.RawMessage `json:"sessions"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return errors.New("invalid Chat storage")
	}
	for _, ss := range root.Sessions {
		for key, value := range ss {
			if strings.EqualFold(key, "chat") {
				var p ChatPermission
				if key != "chat" || update.StrictJSON(value, &p) != nil || !validChatPermission(p, true) {
					return errors.New("unsupported Chat permission; preserve records")
				}
			}
		}
	}
	for _, ss := range d.Sessions {
		if ss.Chat != nil && (ss.Chat.ProfileID != ss.ProfileID || ss.Chat.SessionID != ss.ID || !validChatPermission(*ss.Chat, true)) {
			return errors.New("invalid Chat permission binding; preserve records")
		}
	}
	for _, q := range d.AIRequests {
		if q.Stream != (q.ChatPermissionID != "") || (q.ChatPermissionID != "" && len(q.ChatPermissionID) != 64) || q.PartialAnswer != "" {
			return errors.New("unsupported durable Chat request; preserve records")
		}
	}
	return nil
}
func migrateAgentChat(ctx context.Context, db *sql.DB) error {
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
		return errors.New("finish schema 18 recovery before Chat migration")
	}
	var raw string
	if e = tx.QueryRowContext(ctx, "SELECT document FROM agent_workspace WHERE id=1").Scan(&raw); e != nil {
		return e
	}
	var d agentDocument
	var unprotected struct {
		Sessions []map[string]json.RawMessage `json:"sessions"`
		Requests []map[string]json.RawMessage `json:"aiRequests"`
	}
	if json.Unmarshal([]byte(raw), &unprotected) != nil {
		return errors.New("preserve invalid pre-Chat records")
	}
	for _, ss := range unprotected.Sessions {
		for key := range ss {
			if strings.EqualFold(key, "chat") {
				return errors.New("Chat fields present in unprotected schema; preserve records")
			}
		}
	}
	for _, q := range unprotected.Requests {
		for key := range q {
			if strings.EqualFold(key, "chatPermissionId") || strings.EqualFold(key, "stream") || strings.EqualFold(key, "partialAnswer") {
				return errors.New("Chat fields present in unprotected schema; preserve records")
			}
		}
	}
	if json.Unmarshal([]byte(raw), &d) != nil {
		return errors.New("preserve invalid pre-Chat records")
	}
	for _, ss := range d.Sessions {
		if ss.Chat != nil {
			return errors.New("Chat permission present in unprotected schema; preserve records")
		}
	}
	for _, q := range d.AIRequests {
		if q.ChatPermissionID != "" || q.Stream || q.PartialAnswer != "" {
			return errors.New("Chat request present in unprotected schema; preserve records")
		}
	}
	if _, e = tx.ExecContext(ctx, "PRAGMA user_version=19"); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) chatView(v AgentView) AgentView {
	v.AIExecutionAvailable = s.aiExecutionAvailable(false)
	v.ProviderCallsEnabled = s.providerLiveEnabled()
	d := agentDocument{Profiles: v.Profiles, Sessions: v.Sessions}
	v.ChatReadiness = map[string]string{}
	for _, ss := range v.Sessions {
		reason := s.chatPermissionReason(d, ss.Chat, s.now().Unix())
		if ss.Chat != nil && !s.chatAccessCurrent(ss.Chat) {
			reason = "locked"
		}
		if reason == "ready" && !s.aiConnectionValid(ss.Chat.ConnectionRef) {
			reason = "connection-locked"
		}
		v.ChatReadiness[ss.ID] = reason
	}
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	for i := range v.AIRequests {
		q := &v.AIRequests[i]
		if q.State == "running" {
			q.PartialAnswer = s.aiProgress[q.ID]
		}
	}
	return v
}

func (s *Store) chatAccessCurrent(p *ChatPermission) bool {
	return p != nil && p.AccessEpoch == s.chatEpoch.Load() && p.ConnectionEpoch == s.connectionAccessEpoch(p.ConnectionRef)
}
