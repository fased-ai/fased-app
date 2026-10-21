package core

import (
	"errors"
	"net/http"
)

func (h *HTTPHandler) agentsAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method == http.MethodGet {
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
