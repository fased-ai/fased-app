package core

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"net/http"
)

func (h *HTTPHandler) applicationUpdatesAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method == http.MethodGet {
		v, e := h.store.ApplicationUpdates(r.Context())
		if e != nil {
			writeError(w, 500, "Update status unavailable. Preserve the installation.")
			return
		}
		writeJSON(w, 200, v)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	defer r.Body.Close()
	b, e := io.ReadAll(r.Body)
	var c ApplicationUpdateCommand
	if e != nil || update.StrictJSON(b, &c) != nil {
		writeError(w, 400, "invalid update request")
		return
	}
	v, e := h.store.CommandApplicationUpdate(r.Context(), c)
	if errors.Is(e, ErrConflict) {
		writeError(w, 409, e.Error())
		return
	}
	if e != nil {
		writeError(w, 409, e.Error())
		return
	}
	writeJSON(w, 202, v)
}
