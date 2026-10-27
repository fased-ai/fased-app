package core

import (
	"context"
	"net/http"

	"github.com/fased-ai/fased-app/internal/update"
)

type privateIPCKey struct{}

func privateIPCHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), privateIPCKey{}, true)))
	})
}
func (h *HTTPHandler) pauseAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Context().Value(privateIPCKey{}) != true {
		writeError(w, 403, "core pause requires private CLI IPC")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	var c update.CorePauseCommand
	if !decodeJSON(w, r, &c) {
		return
	}
	p, e := h.store.UpdatePause(r.Context(), c)
	if e != nil {
		writeError(w, 409, e.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (h *HTTPHandler) backupAPI(w http.ResponseWriter, r *http.Request, token string) {
	if r.Context().Value(privateIPCKey{}) != true {
		writeError(w, 403, "metadata backup requires private CLI IPC")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	var p update.Preflight
	if r.URL.Path == "/api/v1/updates/recovery" {
		var c update.RecoveryCheck
		if !decodeJSON(w, r, &c) {
			return
		}
		i, e := h.store.VerifyRecovery(r.Context(), c)
		if e != nil {
			writeError(w, 409, e.Error())
			return
		}
		writeJSON(w, 200, i)
		return
	}
	if !decodeJSON(w, r, &p) {
		return
	}
	i, e := h.store.BackupMetadata(r.Context(), p)
	if e != nil {
		writeError(w, 409, "metadata backup failed; state preserved: "+e.Error())
		return
	}
	writeJSON(w, 200, i)
}
