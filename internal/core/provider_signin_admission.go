package core

// Plan access has no API-key tariff and exposes no token-count or quota API.
// Its consent binds finite requests, time and application acceptance limits.
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

const planSignInWire = "openai-signin-v2"
const planUsageCostBasis = "chatgpt-plan-reported-usage"
const planFailurePrefix = "OpenAI plan request stopped. Diagnostics: "

func planSignInPolicy(policy *OpenAIModelPolicy) bool {
	return policy != nil && policyProvider(policy) == "openai" && policy.WireVersion == planSignInWire
}
func planSignInRequest(q AgentAIRequest) bool { return planSignInPolicy(q.Policy) }
func validPolicyLimits(l AILimits, p *OpenAIModelPolicy) bool {
	if !planSignInPolicy(p) {
		return validAILimits(l)
	}
	return l.MaxInputTokens >= 1000 && l.MaxInputTokens <= 32000 && l.MaxOutputTokens >= 128 && l.MaxOutputTokens <= 1024 && l.DeadlineSeconds >= 1 && l.DeadlineSeconds <= 5 && l.BudgetMicrousd == 0
}
func validPolicyTotal(p *OpenAIModelPolicy, budget int64) bool {
	if planSignInPolicy(p) {
		return budget == 0
	}
	model := ""
	if p != nil {
		model = p.Model
	}
	return budget >= 1 && budget <= maxModelBudget(model)
}

// This predicate also protects nonmonetary uncertain usage. A missing dollar
// hold must not make a dispatched, unverified plan request safe to replay/archive.
func aiHasUsageHold(q AgentAIRequest) bool {
	if q.HeldMicrousd > 0 {
		return true
	}
	if !planSignInRequest(q) {
		return false
	}
	return q.State == "queued" || q.State == "running" || q.State == "unknown" || q.State == "cancelled" && q.DispatchBoot != "" && q.CostBasis != planUsageCostBasis
}

type planProviderFailure struct {
	Status     int    `json:"status"`
	Body       string `json:"body"`
	Code       string `json:"code,omitempty"`
	Param      string `json:"param,omitempty"`
	RequestID  string `json:"requestId,omitempty"`
	SHA256     string `json:"bodySha256,omitempty"`
	Late       bool   `json:"late"`
	definitive bool
	usage      *planTerminalUsage // transient, identity-checked receipt; never diagnostic JSON
}

type planTerminalUsage struct{ input, output, cached int64 }

// Called only for a terminal frame whose stream identity, exact model and status
// match. These counters confer no output or execution authority.
func readPlanTerminalUsage(raw []byte) *planTerminalUsage {
	var response struct {
		Usage *struct {
			Input   *int64 `json:"input_tokens"`
			Output  *int64 `json:"output_tokens"`
			Total   *int64 `json:"total_tokens"`
			Details *struct {
				Cached *int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Usage == nil {
		return nil
	}
	u := response.Usage
	if u.Input == nil || u.Output == nil || u.Total == nil || *u.Input < 0 || *u.Output < 0 || *u.Total < 0 || *u.Total != *u.Input+*u.Output {
		return nil
	}
	cached := int64(0)
	if u.Details != nil {
		if u.Details.Cached == nil || *u.Details.Cached < 0 || *u.Details.Cached > *u.Input {
			return nil
		}
		cached = *u.Details.Cached
	}
	return &planTerminalUsage{input: *u.Input, output: *u.Output, cached: cached}
}

func (e *planProviderFailure) Error() string {
	raw, _ := json.Marshal(e)
	return planFailurePrefix + string(raw)
}
func safePlanIdentifier(value string, bound int) string {
	if len(value) > bound || strings.HasPrefix(value, "sk-") || strings.HasPrefix(value, "fased-test-") {
		return ""
	}
	for _, b := range []byte(value) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("_.-[]", rune(b))) {
			return ""
		}
	}
	return value
}
func planFailure(status int, media, requestID string, raw []byte, late bool) *planProviderFailure {
	e := &planProviderFailure{Status: status, Body: "text", RequestID: safePlanIdentifier(requestID, 96), Late: late}
	if len(raw) > 0 {
		e.SHA256 = newsHash(raw)
	} else {
		e.Body = "empty"
	}
	kind, _, _ := mime.ParseMediaType(media)
	if kind == "text/event-stream" {
		e.Body = "sse"
	} else if kind == "text/html" {
		e.Body = "html"
	} else if kind == "application/json" && uniqueAIJSON(raw, 16384) {
		var root map[string]json.RawMessage
		if json.Unmarshal(raw, &root) == nil && root != nil {
			e.Body = "json"
			if detail, ok := root["detail"]; ok {
				var ignored string
				if json.Unmarshal(detail, &ignored) == nil {
					e.Body = "json-detail"
				}
			}
			if value, ok := root["error"]; ok {
				var detail struct {
					Code  string `json:"code"`
					Param string `json:"param"`
				}
				if json.Unmarshal(value, &detail) == nil {
					e.Body = "json-error"
					e.Code = safePlanIdentifier(detail.Code, 96)
					e.Param = safePlanIdentifier(detail.Param, 96)
				}
			}
		}
	}
	e.definitive = !late && status >= 400 && status < 500
	if !late && status == 503 && (e.Code == "subscription_sharing_usage_unavailable" || e.Code == "subscription_sharing_user_unavailable" || e.Body == "json-detail") {
		e.definitive = true
	}
	return e
}
func planStreamFailure(response, frame []byte) *planProviderFailure {
	e := planFailure(200, "application/json", "", response, true)
	if e.Code == "" {
		var top struct {
			Code  string `json:"code"`
			Param string `json:"param"`
		}
		if json.Unmarshal(response, &top) == nil {
			e.Code = safePlanIdentifier(top.Code, 96)
			e.Param = safePlanIdentifier(top.Param, 96)
		}
	}
	e.Body, e.SHA256 = "sse", newsHash(frame)
	return e
}
func localPlanFailure(code, param string) *planProviderFailure {
	return &planProviderFailure{Body: "local", Code: code, Param: param, definitive: true}
}

func boundSignInAdmission(rt http.RoundTripper, q AgentAIRequest, now int64) bool {
	switch value := rt.(type) {
	case aiMemoryReviewTransport:
		return boundSignInAdmission(value.next, q, now)
	case authenticatedAITransport:
		if value.store == nil || value.request == nil || value.ref != q.ConnectionRef || value.request.ID != q.ID || value.request.Digest != q.Digest {
			return false
		}
		s := value.store
		s.provider.mu.Lock()
		defer s.provider.mu.Unlock()
		v, err := s.aiConnectionLocked(value.ref)
		state := s.credentialLocked(value.ref)
		credential, ok := parseProviderSignInCredential(state.key)
		if err != nil || !ok || v.Ref != q.ConnectionRef || v.Provider != "openai" || v.AuthMethod != "sign-in" || v.CredentialMode != "openai-signin" || !credential.planUse() || credential.ExpiresAt <= now || !(s.providerFixture || s.provider.apiCredentials) {
			return false
		}
		if q.TaskID != "" {
			return s.taskAccessMatches(q, state.taskAccess, q.taskAccessID != "", state)
		}
		return !v.Locked
	}
	return false
}

func admitSignIn(ctx context.Context, q *AgentAIRequest, rt http.RoundTripper, now func() time.Time) bool {
	wire, err := aiWire(*q)
	a := &OpenAIAdmission{State: "checking", WireSHA256: newsHash(wire)}
	q.Admission = a
	refuse := func(failure *planProviderFailure) bool {
		a.State = "refused"
		q.State = "failed"
		q.HeldMicrousd = 0
		q.Failure = failure.Error()
		return false
	}
	if err != nil || !planSignInRequest(*q) || validateAIModelPolicy(*q, now().Unix()) != nil || !boundSignInAdmission(rt, *q, now().Unix()) {
		return refuse(localPlanFailure("plan_permission_required", "account"))
	}
	if len(wire) > 65536 || int64(len(wire)) > q.Limits.MaxInputTokens || ctx.Err() != nil {
		return refuse(localPlanFailure("input_limit_exceeded", "input"))
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.openai.com/v1/models", nil)
	client := http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("plan redirect refused") }}
	res, err := client.Do(req)
	if err != nil {
		return refuse(&planProviderFailure{Body: "transport", Code: "model_discovery_unavailable"})
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 262145))
	if err != nil || len(raw) > 262144 {
		return refuse(&planProviderFailure{Status: res.StatusCode, Body: "invalid", RequestID: safePlanIdentifier(res.Header.Get("X-Request-Id"), 96), Code: "model_response_unverified"})
	}
	if res.StatusCode != 200 {
		return refuse(planFailure(res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("X-Request-Id"), raw, false))
	}
	kind, _, mediaErr := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if mediaErr != nil || kind != "application/json" || res.Header.Get("Content-Encoding") != "" || !uniqueAIJSON(raw, 32768) {
		return refuse(localPlanFailure("model_response_unverified", "model"))
	}
	models, err := parseProviderSignInModels(raw, now().Unix())
	if err != nil {
		return refuse(localPlanFailure("model_response_unverified", "model"))
	}
	found := false
	seen := map[string]bool{}
	for _, m := range models {
		if seen[m.ID] {
			return refuse(localPlanFailure("model_response_unverified", "model"))
		}
		seen[m.ID] = true
		if m.ID == q.Model {
			found = true
		}
	}
	if !found {
		return refuse(localPlanFailure("model_unavailable", "model"))
	}
	a.AccessAt, a.ModelRecordSHA256 = now().Unix(), newsHash(raw)
	// This is a submitted-text acceptance bound, not a provider token count or
	// forecast of plan quota. InputTokens remains zero until usage is reported.
	a.CountPayloadSHA256 = newsHash(wire)
	if ctx.Err() != nil || !boundSignInAdmission(rt, *q, now().Unix()) || validateAIModelPolicy(*q, now().Unix()) != nil {
		return refuse(localPlanFailure("plan_scope_changed", "account"))
	}
	a.State, a.GenerationAttempted = "admitted", true
	return true
}

func reconcilePlanFailure(q AgentAIRequest, err error, requestID string) (AgentAIRequest, error) {
	var detail *planProviderFailure
	if !errors.As(err, &detail) {
		detail = &planProviderFailure{Body: "transport", Code: "response_unverified"}
	}
	copy := *detail
	if copy.RequestID == "" {
		copy.RequestID = safePlanIdentifier(requestID, 96)
	}
	q.State, q.Answer, q.Cards, q.SpentMicrousd, q.HeldMicrousd = "unknown", "", []AIActionCard{}, 0, 0
	if copy.definitive {
		q.State = "failed"
	}
	if u := copy.usage; planSignInRequest(q) && u != nil && u.input <= q.Policy.ContextTokens && u.output <= q.Policy.OutputTokens {
		q.InputTokens, q.OutputTokens, q.CachedInputTokens = u.input, u.output, u.cached
		q.CostBasis, q.State = planUsageCostBasis, "failed"
	}
	q.Failure = copy.Error()
	return q, nil
}

func planOutputLimitFailure(q AgentAIRequest) AgentAIRequest {
	q.State, q.Answer, q.Cards = "failed", "", []AIActionCard{}
	q.Failure = localPlanFailure("reported_usage_exceeds_acceptance_limit", "output").Error()
	return q
}
