package core

import (
	"errors"
	"net/http"
	"strconv"
)

func (h *HTTPHandler) agentsAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		if q.Has("history") || q.Has("before") || q.Has("history-task") || q.Has("history-q") {
			for key, values := range q {
				if (key != "profile" && key != "history" && key != "before" && key != "history-task" && key != "history-q") || len(values) != 1 {
					writeError(w, 400, "one profile and history page or task expected")
					return
				}
			}
			if q.Get("history") != "tasks" && q.Get("history") != "conversations" && q.Get("history") != "memories" {
				writeError(w, 400, "select task history")
				return
			}
			before := int64(0)
			var err error
			if q.Has("before") {
				before, err = strconv.ParseInt(q.Get("before"), 10, 64)
				if err != nil || before < 0 || strconv.FormatInt(before, 10) != q.Get("before") {
					writeError(w, 400, "invalid history cursor")
					return
				}
			}
			if q.Has("history-task") && q.Get("history-task") == "" {
				writeError(w, 400, "select one archived task")
				return
			}
			view, err := h.store.AgentTaskHistory(r.Context(), q.Get("profile"), before, q.Get("history-task"), q.Get("history"), q.Get("history-q"))
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, view)
			return
		}
		if q.Has("playbook-draft") || q.Has("playbook-version") {
			for key, values := range q {
				if (key != "profile" && key != "playbook-draft" && key != "playbook-version") || len(values) != 1 {
					writeError(w, 400, "one profile, playbook and version expected")
					return
				}
			}
			n, err := strconv.Atoi(q.Get("playbook-version"))
			if err != nil || strconv.Itoa(n) != q.Get("playbook-version") {
				writeError(w, 400, "select a saved playbook version")
				return
			}
			v, err := h.store.AgentPlaybookDraft(r.Context(), q.Get("profile"), q.Get("playbook-draft"), n)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, v)
			return
		}
		if q.Has("memory-restore-draft") || q.Has("memory-revision") {
			for key, values := range q {
				if (key != "profile" && key != "memory-restore-draft" && key != "memory-revision") || len(values) != 1 {
					writeError(w, 400, "one profile, memory and retained revision expected")
					return
				}
			}
			index, err := strconv.Atoi(q.Get("memory-revision"))
			if err != nil || strconv.Itoa(index) != q.Get("memory-revision") {
				writeError(w, 400, "select a retained revision (0–7)")
				return
			}
			v, err := h.store.AgentMemoryRestore(r.Context(), q.Get("profile"), q.Get("memory-restore-draft"), index)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, v)
			return
		}
		if q.Has("reminder-draft") {
			for key, values := range q {
				if (key != "profile" && key != "reminder-draft") || len(values) != 1 {
					writeError(w, 400, "one profile and saved plain reminder expected")
					return
				}
			}
			v, err := h.store.AgentReminderDraft(r.Context(), q.Get("profile"), q.Get("reminder-draft"))
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, v)
			return
		}
		if q.Has("evidence") || q.Has("evidence-id") || q.Has("evidence-item") {
			for key, values := range q {
				if (key != "profile" && key != "evidence" && key != "evidence-id" && key != "evidence-item") || len(values) != 1 {
					writeError(w, 400, "one profile, evidence kind/source and optional headline item expected")
					return
				}
			}
			if q.Has("evidence-item") && q.Get("evidence") != "headline" {
				writeError(w, 400, "item identity is only for a headline")
				return
			}
			v, err := h.store.AgentEvidence(r.Context(), q.Get("profile"), q.Get("evidence"), q.Get("evidence-id"), q.Get("evidence-item"))
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, v)
			return
		}
		if q.Has("ai-draft") {
			for key, values := range q {
				if (key != "profile" && key != "ai-draft") || len(values) != 1 {
					writeError(w, 400, "one profile and saved manual request expected")
					return
				}
			}
			v, err := h.store.AgentAIRequestDraft(r.Context(), q.Get("profile"), q.Get("ai-draft"))
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, v)
			return
		}
		if q.Has("memory-search") || q.Has("memory-source") {
			for key, values := range q {
				if (key != "profile" && key != "memory-search" && key != "memory-source") || len(values) != 1 {
					writeError(w, 400, "one profile, memory search and optional memory identity expected")
					return
				}
			}
			if q.Has("memory-source") && q.Get("memory-source") == "" {
				writeError(w, 400, "select one memory identity")
				return
			}
			v, err := h.store.AgentMemories(r.Context(), q.Get("profile"), q.Get("memory-search"), q.Get("memory-source"))
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			writeJSON(w, 200, v)
			return
		}
		for k, v := range r.URL.Query() {
			if (k != "profile" && k != "q") || len(v) != 1 {
				writeError(w, 400, "one profile and optional session search expected")
				return
			}
		}
		v, e := h.store.Agents(r.Context(), r.URL.Query().Get("profile"), r.URL.Query().Get("q"))
		if e != nil {
			writeError(w, 400, "agent workspace unavailable; verify profile and preserve state")
			return
		}
		v.AIFixtureAvailable = h.agentFixture
		writeJSON(w, 200, v)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, 400, "agent command does not accept query arguments")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	var c AgentCommand
	if !decodeJSON(w, r, &c) {
		return
	}
	if c.Action == "wen-research-chat" {
		if err := h.authorizeResearchChat(&c); err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	v, e := h.store.changeAgents(r.Context(), c, h.agentFixture)
	if errors.Is(e, ErrConflict) || errors.Is(e, ErrCorePaused) {
		writeError(w, 409, e.Error())
		return
	}
	if e != nil {
		writeError(w, 400, e.Error())
		return
	}
	v.AIFixtureAvailable = h.agentFixture
	writeJSON(w, 200, v)
}
