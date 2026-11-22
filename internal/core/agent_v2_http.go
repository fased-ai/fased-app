package core

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (h *HTTPHandler) v2Mutation(w http.ResponseWriter, r *http.Request, token string, requireJSON bool) bool {
	if !h.origins[r.Header.Get("Origin")] {
		writeError(w, 403, "same-origin request required")
		return false
	}
	if requireJSON {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, 415, "JSON content type required")
			return false
		}
	}
	o, _, e := readApplicationUpdate(r.Context(), h.store.db)
	if e != nil || oneOf(o.Phase, "applying", "recovery") {
		writeError(w, 409, "Update in progress. Inspect recovery before changing this instance.")
		return false
	}
	if phase, _, pErr := readPause(r.Context(), h.store.db); pErr != nil {
		writeError(w, 500, "pause state unavailable")
		return false
	} else if phase == "paused" {
		writeError(w, 409, ErrCorePaused.Error())
		return false
	}
	return h.csrf(w, r, token)
}

func (h *HTTPHandler) acquireAgentV2Turn(w http.ResponseWriter, r *http.Request) (func(), bool) {
	if !h.allowRate(r, "agent-v2", 30) {
		writeError(w, 429, "Too many requests. Wait before starting another agent turn.")
		return nil, false
	}
	if h.aiSlots != nil {
		select {
		case h.aiSlots <- struct{}{}:
			return func() { <-h.aiSlots }, true
		default:
			writeError(w, 429, "AI requests are already in progress.")
			return nil, false
		}
	}
	return func() {}, true
}

func decodeV2JSON(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, 400, "invalid JSON request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, 400, "one JSON object required")
		return false
	}
	return true
}

func v2WantsSSE(r *http.Request) bool {
	if r.URL.Query().Get("stream") == "1" {
		return true
	}
	if r.URL.Query().Get("stream") == "0" {
		return false
	}
	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	if strings.Contains(accept, "text/event-stream") {
		return true
	}
	if strings.Contains(accept, "application/json") {
		return false
	}
	return accept == "" || strings.Contains(accept, "*/*")
}

func startV2SSE(w http.ResponseWriter) (func(AgentStreamEvent), *http.ResponseController) {
	rc := http.NewResponseController(w)
	// Clear per-request write deadlines so long streams/tool loops are never truncated by Server.WriteTimeout.
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_ = rc.Flush()

	emit := func(ev AgentStreamEvent) {
		data, err := json.Marshal(ev)
		if err != nil {
			return
		}
		_, _ = w.Write([]byte("event: " + ev.Type + "\ndata: "))
		_, _ = w.Write(data)
		_, _ = w.Write([]byte("\n\n"))
		_ = rc.Flush()
	}
	return emit, rc
}

func (h *HTTPHandler) agentsV2API(w http.ResponseWriter, r *http.Request, token string) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/v1/agents/v2")
	switch {
	case rel == "/tools":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		writeJSON(w, 200, map[string]any{"tools": agentV2ToolSpecs()})

	case rel == "/usage":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		q := r.URL.Query()
		days := 30
		if rawDays := strings.TrimSpace(q.Get("days")); rawDays != "" {
			d, err := strconv.Atoi(rawDays)
			if err != nil || d < 0 || d > 365 {
				writeError(w, 400, "days must be an integer between 0 and 365")
				return
			}
			days = d
		}
		summary, err := h.store.GetAgentV2Usage(r.Context(), q.Get("profileId"), days)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, summary)

	case rel == "/task-threads":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		links, err := h.store.ListTaskThreadLinks(r.Context(), r.URL.Query().Get("profileId"))
		if err != nil {
			writeError(w, 500, "task threads unavailable")
			return
		}
		writeJSON(w, 200, links)

	case rel == "/threads":
		switch r.Method {
		case http.MethodGet:
			q := r.URL.Query()
			archived := q.Get("archived") == "1" || strings.EqualFold(q.Get("archived"), "true")
			threads, err := h.store.ListThreads(r.Context(), q.Get("profileId"), archived)
			if err != nil {
				writeError(w, 500, "threads unavailable")
				return
			}
			writeJSON(w, 200, map[string]any{"threads": threads})
		case http.MethodPost:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var req struct {
				ProfileID     string `json:"profileId"`
				Title         string `json:"title"`
				ConnectionRef string `json:"connectionRef"`
				Model         string `json:"model"`
			}
			if !decodeV2JSON(w, r, &req, 16*1024) {
				return
			}
			t, err := h.store.CreateThread(r.Context(), req.ProfileID, req.Title, req.ConnectionRef, req.Model)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 201, map[string]any{"thread": t})
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasPrefix(rel, "/threads/"):
		sub := strings.TrimPrefix(rel, "/threads/")
		if strings.HasSuffix(sub, "/messages") {
			threadID := strings.TrimSuffix(sub, "/messages")
			if threadID == "" || strings.Contains(threadID, "/") {
				writeError(w, 404, "thread not found")
				return
			}
			h.agentV2MessagesAPI(w, r, token, threadID)
			return
		}
		if strings.HasSuffix(sub, "/compact") {
			threadID := strings.TrimSuffix(sub, "/compact")
			if threadID == "" || strings.Contains(threadID, "/") {
				writeError(w, 404, "thread not found")
				return
			}
			h.agentV2CompactAPI(w, r, token, threadID)
			return
		}
		if strings.HasSuffix(sub, "/side") {
			threadID := strings.TrimSuffix(sub, "/side")
			if threadID == "" || strings.Contains(threadID, "/") {
				writeError(w, 404, "thread not found")
				return
			}
			h.agentV2SideAPI(w, r, token, threadID)
			return
		}
		if sub == "" || strings.Contains(sub, "/") {
			writeError(w, 404, "thread not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			detail, err := h.store.GetThread(r.Context(), sub)
			if errors.Is(err, ErrThreadNotFound) {
				writeError(w, 404, err.Error())
				return
			}
			if err != nil {
				writeError(w, 500, "thread unavailable")
				return
			}
			writeJSON(w, 200, detail)
		case http.MethodPatch, http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var patch ThreadUpdateInput
			if !decodeV2JSON(w, r, &patch, 16*1024) {
				return
			}
			t, err := h.store.UpdateThread(r.Context(), sub, patch)
			if errors.Is(err, ErrThreadNotFound) {
				writeError(w, 404, err.Error())
				return
			}
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"thread": t})
		case http.MethodDelete:
			if !h.v2Mutation(w, r, token, false) {
				return
			}
			err := h.store.DeleteThread(r.Context(), sub)
			if errors.Is(err, ErrThreadNotFound) {
				writeError(w, 404, err.Error())
				return
			}
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]bool{"deleted": true})
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasPrefix(rel, "/approvals/"):
		approvalID := strings.TrimPrefix(rel, "/approvals/")
		if approvalID == "" || strings.Contains(approvalID, "/") {
			writeError(w, 404, "approval not found")
			return
		}
		h.agentV2ApprovalAPI(w, r, token, approvalID)

	case rel == "/memories":
		switch r.Method {
		case http.MethodGet:
			q := r.URL.Query()
			limit := 20
			if rawLimit := q.Get("limit"); rawLimit != "" {
				n, err := strconv.Atoi(rawLimit)
				if err != nil || n < 1 || n > 100 {
					writeError(w, 400, "invalid limit")
					return
				}
				limit = n
			}
			qStr := strings.TrimSpace(q.Get("q"))
			scope := strings.TrimSpace(q.Get("scope"))
			excludeDrafts := q.Get("excludeDrafts") == "1" || q.Get("excludeDrafts") == "true"
			fetchLimit := limit
			if excludeDrafts && scope == "" {
				fetchLimit = 100
			}
			var (
				mems []AgentV2Memory
				err  error
			)
			if scope != "" && qStr == "" {
				mems, err = h.store.ListMemoriesByScope(r.Context(), q.Get("profileId"), scope, fetchLimit)
			} else {
				mems, err = h.store.SearchMemories(r.Context(), q.Get("profileId"), qStr, fetchLimit)
				if err == nil && scope != "" {
					filtered := make([]AgentV2Memory, 0, len(mems))
					for _, m := range mems {
						if m.Scope == scope {
							filtered = append(filtered, m)
						}
					}
					mems = filtered
				}
			}
			if err == nil && excludeDrafts && scope == "" {
				filtered := make([]AgentV2Memory, 0, min(len(mems), limit))
				for _, m := range mems {
					if isV2DraftScope(m.Scope) {
						continue
					}
					filtered = append(filtered, m)
					if len(filtered) >= limit {
						break
					}
				}
				mems = filtered
			}
			if err != nil {
				writeError(w, 500, "memories unavailable")
				return
			}
			writeJSON(w, 200, map[string]any{"memories": mems})
		case http.MethodPost:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in MemoryInput
			if !decodeV2JSON(w, r, &in, 32*1024) {
				return
			}
			m, err := h.store.CreateMemory(r.Context(), in)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 201, map[string]any{"memory": m})
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasPrefix(rel, "/memories/"):
		memID := strings.TrimPrefix(rel, "/memories/")
		if memID == "" || strings.Contains(memID, "/") {
			writeError(w, 404, "memory not found")
			return
		}
		switch r.Method {
		case http.MethodPatch:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in MemoryUpdateInput
			if !decodeV2JSON(w, r, &in, 32*1024) {
				return
			}
			m, err := h.store.UpdateMemory(r.Context(), memID, in)
			if errors.Is(err, ErrMemoryNotFound) {
				writeError(w, 404, err.Error())
				return
			}
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]any{"memory": m})
		case http.MethodDelete:
			if !h.v2Mutation(w, r, token, false) {
				return
			}
			err := h.store.DeleteMemory(r.Context(), memID)
			if errors.Is(err, ErrMemoryNotFound) {
				writeError(w, 404, err.Error())
				return
			}
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, map[string]bool{"deleted": true})
		default:
			writeError(w, 405, "method not allowed")
		}

	default:
		writeError(w, 404, "unknown API operation")
	}
}

func (h *HTTPHandler) agentV2MessagesAPI(w http.ResponseWriter, r *http.Request, token, threadID string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.v2Mutation(w, r, token, true) {
		return
	}
	var in AgentV2PromptInput
	if !decodeV2JSON(w, r, &in, 48*1024) {
		return
	}
	content := strings.TrimSpace(in.Content)
	if content == "" || len(content) > agentV2MaxPromptBytes || !utf8.ValidString(content) {
		writeError(w, 400, "prompt content must be non-empty UTF-8 within 32 KiB")
		return
	}
	if in.ConnectionRef != "" && !validConnectionRef(in.ConnectionRef) {
		writeError(w, 400, "invalid AI connection reference")
		return
	}
	if in.Model != "" && !validProviderModelID(in.Model) {
		writeError(w, 400, "invalid model identifier")
		return
	}
	detail, err := h.store.GetThread(r.Context(), threadID)
	if errors.Is(err, ErrThreadNotFound) {
		writeError(w, 404, err.Error())
		return
	}
	if err != nil {
		writeError(w, 500, "thread unavailable")
		return
	}
	if _, err := resolveAgentV2Provider(r.Context(), h.store, in.ConnectionRef, detail.Thread.ConnectionRef, in.Model, detail.Thread.Model, detail.Thread.ProfileID); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	release, ok := h.acquireAgentV2Turn(w, r)
	if !ok {
		return
	}
	defer release()

	if !v2WantsSSE(r) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		res, err := h.store.RunAgentV2Turn(r.Context(), threadID, in, nil)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)
		return
	}

	emit, _ := startV2SSE(w)
	_, err = h.store.RunAgentV2Turn(r.Context(), threadID, in, emit)
	if err != nil && r.Context().Err() == nil {
		emit(AgentStreamEvent{
			Type:     "error",
			ThreadID: threadID,
			Error:    err.Error(),
		})
	}
}

func (h *HTTPHandler) agentV2CompactAPI(w http.ResponseWriter, r *http.Request, token, threadID string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.v2Mutation(w, r, token, false) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	var req struct {
		KeepRecent int `json:"keepRecent"`
	}
	if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
		if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
			writeError(w, 415, "JSON content type required")
			return
		}
		decoder := json.NewDecoder(strings.NewReader(trimmed))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeError(w, 400, "invalid JSON request")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeError(w, 400, "one JSON object required")
			return
		}
		if req.KeepRecent < 0 || req.KeepRecent > 200 {
			writeError(w, 400, "keepRecent must be between 0 and 200")
			return
		}
	}
	res, err := h.store.CompactThread(r.Context(), threadID, req.KeepRecent)
	if errors.Is(err, ErrThreadNotFound) {
		writeError(w, 404, err.Error())
		return
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

func (h *HTTPHandler) agentV2SideAPI(w http.ResponseWriter, r *http.Request, token, threadID string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.v2Mutation(w, r, token, true) {
		return
	}
	var in AgentV2SideInput
	if !decodeV2JSON(w, r, &in, 48*1024) {
		return
	}
	content := strings.TrimSpace(in.Content)
	if content == "" || len(content) > agentV2MaxPromptBytes || !utf8.ValidString(content) {
		writeError(w, 400, "prompt content must be non-empty UTF-8 within 32 KiB")
		return
	}
	if in.ConnectionRef != "" && !validConnectionRef(in.ConnectionRef) {
		writeError(w, 400, "invalid AI connection reference")
		return
	}
	if in.Model != "" && !validProviderModelID(in.Model) {
		writeError(w, 400, "invalid model identifier")
		return
	}
	detail, err := h.store.GetThread(r.Context(), threadID)
	if errors.Is(err, ErrThreadNotFound) {
		writeError(w, 404, err.Error())
		return
	}
	if err != nil {
		writeError(w, 500, "thread unavailable")
		return
	}
	if _, err := resolveAgentV2Provider(r.Context(), h.store, in.ConnectionRef, detail.Thread.ConnectionRef, in.Model, detail.Thread.Model, detail.Thread.ProfileID); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	release, ok := h.acquireAgentV2Turn(w, r)
	if !ok {
		return
	}
	defer release()

	if !v2WantsSSE(r) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		res, err := h.store.RunAgentV2SideQuestion(r.Context(), threadID, in, nil)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)
		return
	}

	emit, _ := startV2SSE(w)
	_, err = h.store.RunAgentV2SideQuestion(r.Context(), threadID, in, emit)
	if err != nil && r.Context().Err() == nil {
		emit(AgentStreamEvent{
			Type:     "error",
			ThreadID: threadID,
			Error:    err.Error(),
		})
	}
}

func (h *HTTPHandler) agentV2ApprovalAPI(w http.ResponseWriter, r *http.Request, token, approvalID string) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.v2Mutation(w, r, token, true) {
		return
	}
	var req struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
		Resume   *bool  `json:"resume"`
	}
	if !decodeV2JSON(w, r, &req, 16*1024) {
		return
	}
	if req.Decision != "approve" && req.Decision != "reject" {
		writeError(w, 400, "decision must be approve or reject")
		return
	}
	ap, err := h.store.GetApproval(r.Context(), approvalID)
	if errors.Is(err, ErrApprovalNotFound) {
		writeError(w, 404, err.Error())
		return
	}
	if err != nil {
		writeError(w, 500, "approval lookup failed")
		return
	}
	if ap.Status != "pending" {
		writeError(w, 409, ErrConflict.Error())
		return
	}
	resume := true
	if req.Resume != nil {
		resume = *req.Resume
	}
	release, ok := h.acquireAgentV2Turn(w, r)
	if !ok {
		return
	}
	defer release()

	if !v2WantsSSE(r) {
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		res, err := h.store.ResolveAgentV2Approval(r.Context(), approvalID, req.Decision, req.Note, resume, nil)
		if errors.Is(err, ErrApprovalNotFound) {
			writeError(w, 404, err.Error())
			return
		}
		if errors.Is(err, ErrConflict) {
			writeError(w, 409, err.Error())
			return
		}
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, res)
		return
	}

	emit, _ := startV2SSE(w)
	_, err = h.store.ResolveAgentV2Approval(r.Context(), approvalID, req.Decision, req.Note, resume, emit)
	if err != nil && r.Context().Err() == nil {
		emit(AgentStreamEvent{
			Type:     "error",
			ThreadID: ap.ThreadID,
			Error:    err.Error(),
		})
	}
}
