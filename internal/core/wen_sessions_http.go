package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	"github.com/fased-ai/fased-app/internal/update"
)

// Complete bounded workspace history plus the finite compiled scenario catalog.
// Command/review input remains 64 KiB; other client endpoint limits are unchanged.
const WENSessionResponseLimit = agentDocumentLimit + 64*1024

func writeWENSession(w http.ResponseWriter, result any, review bool) {
	limit := WENSessionResponseLimit
	if review {
		limit = update.MaxDocument
	}
	b, e := json.Marshal(result)
	if e != nil || len(b)+1 > limit {
		writeError(w, 400, "complete session response exceeds its bound; no records clipped")
		return
	}
	writeJSON(w, 200, result)
}

// DecodeWENSession uses the same complete strict shape for private HTTP and CLI.
// Nullable control selectors are explicit null, never grants or default input.
func DecodeWENSession(raw []byte, out any) error {
	if len(raw) > update.MaxDocument {
		return errors.New("complete session command exceeds 64 KiB")
	}
	if e := update.StrictJSON(raw, out); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return errors.New("session object required")
	}
	t := reflect.TypeOf(out).Elem()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		value, ok := fields[name]
		if !ok {
			return errors.New("complete session command required: " + name)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) && f.Type.Kind() != reflect.Pointer {
			return errors.New("null session field: " + name)
		}
	}
	return nil
}
func (h *HTTPHandler) wenSessionsAPI(w http.ResponseWriter, r *http.Request, token string, action string) {
	fail := func(e error) {
		code := 400
		if errors.Is(e, ErrConflict) || errors.Is(e, ErrCorePaused) {
			code = 409
		}
		writeError(w, code, e.Error())
	}
	if action == "view" {
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		q, qe := url.ParseQuery(r.URL.RawQuery)
		if qe != nil || len(q) != 1 || len(q["profile"]) != 1 {
			writeError(w, 400, "select one profile")
			return
		}
		v, e := h.store.WENSessions(r.Context(), q.Get("profile"))
		if e != nil {
			fail(e)
			return
		}
		writeWENSession(w, v, false)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method not allowed")
		return
	}
	if !h.mutation(w, r) || !h.csrf(w, r, token) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, 400, "unexpected session query")
		return
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, update.MaxDocument+1))
	if e != nil {
		fail(e)
		return
	}
	var result any
	switch action {
	case "review":
		var c WENSessionPrepare
		if e = DecodeWENSession(raw, &c); e == nil {
			result, e = h.ReviewWENSession(r.Context(), c)
		}
	case "create":
		var c WENSessionReview
		if e = DecodeWENSession(raw, &c); e == nil {
			result, e = h.CreateWENSession(r.Context(), c)
		}
	case "step":
		var c WENSessionStep
		if e = DecodeWENSession(raw, &c); e == nil {
			result, e = h.StepWENSession(r.Context(), c)
		}
	case "action-review":
		var c WENSessionActionInput
		if e = DecodeWENSession(raw, &c); e == nil {
			result, e = h.ReviewWENSessionAction(r.Context(), c)
		}
	case "control":
		var c WENSessionControl
		if e = DecodeWENSession(raw, &c); e == nil {
			result, e = h.ControlWENSession(r.Context(), c)
		}
	default:
		e = errors.New("unknown session action")
	}
	if e != nil {
		fail(e)
		return
	}
	writeWENSession(w, result, action == "review" || action == "action-review")
}
