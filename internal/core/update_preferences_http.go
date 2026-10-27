package core

import (
	"errors"
	"io"
	"net/http"

	"github.com/fased-ai/fased-app/internal/update"
)

func (h *HTTPHandler) updatePreferencesAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method == http.MethodGet {
		v, e := h.store.UpdatePreferences(r.Context())
		if e != nil {
			writeError(w, 500, "update preferences unavailable; preserve metadata")
			return
		}
		writeJSON(w, 200, v)
		return
	}
	if r.Method != http.MethodPut {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	defer r.Body.Close()
	b, e := io.ReadAll(r.Body)
	var input UpdatePreferencesRequest
	if e != nil || update.StrictJSON(b, &input) != nil {
		writeError(w, 400, "invalid update metadata; unknown fields, permissions and duplicate names are refused")
		return
	}
	v, e := h.store.ChangeUpdatePreferences(r.Context(), input)
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
