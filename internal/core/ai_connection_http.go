package core

import (
	"errors"
	"net/http"
)

func (h *HTTPHandler) aiConnectionAPI(w http.ResponseWriter, r *http.Request, token string) {
	if len(r.URL.Query()) != 0 {
		writeError(w, 400, "credential operations accept no query arguments")
		return
	}
	if r.Method == http.MethodGet {
		v, e := h.store.AIConnection()
		if e != nil {
			writeError(w, 409, e.Error())
			return
		}
		writeJSON(w, 200, v)
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	if !h.allowAuth() {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	select {
	case h.authSlots <- struct{}{}:
		defer func() { <-h.authSlots }()
	default:
		writeError(w, 429, "credential operation busy")
		return
	}
	var c AIConnectionCommand
	if !decodeJSON(w, r, &c) {
		return
	}
	v, e := h.store.ChangeAIConnection(r.Context(), c)
	if errors.Is(e, ErrConflict) || errors.Is(e, ErrCorePaused) {
		writeError(w, 409, e.Error())
		return
	}
	if e != nil {
		writeError(w, 400, e.Error())
		return
	}
	writeJSON(w, 200, v)
}
