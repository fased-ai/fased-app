package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
)

// New fixture wires require complete nested evidence as well as a complete root.
// Omitempty applies only to pre-existing source/record types, never command fields.
func DecodeWENFixtureRun(raw []byte, out any) error {
	if e := DecodeWENSession(raw, out); e != nil {
		return e
	}
	var complete func(json.RawMessage, reflect.Type) error
	complete = func(data json.RawMessage, t reflect.Type) error {
		if t.Kind() == reflect.Pointer {
			if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
				return nil
			}
			return complete(data, t.Elem())
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return errors.New("null required fixture evidence")
		}
		switch t.Kind() {
		case reflect.Struct:
			var fields map[string]json.RawMessage
			if json.Unmarshal(data, &fields) != nil {
				return errors.New("fixture evidence object required")
			}
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				parts := strings.Split(f.Tag.Get("json"), ",")
				name := parts[0]
				if name == "-" || name == "" {
					continue
				}
				v, ok := fields[name]
				if !ok {
					if strings.Contains(f.Tag.Get("json"), ",omitempty") {
						continue
					}
					return errors.New("complete nested fixture evidence required: " + name)
				}
				if e := complete(v, f.Type); e != nil {
					return e
				}
			}
		case reflect.Slice, reflect.Array:
			var values []json.RawMessage
			if json.Unmarshal(data, &values) != nil {
				return errors.New("fixture evidence array required")
			}
			for _, v := range values {
				if e := complete(v, t.Elem()); e != nil {
					return e
				}
			}
		}
		return nil
	}
	return complete(raw, reflect.TypeOf(out).Elem())
}
func (h *HTTPHandler) wenFixtureRunsAPI(w http.ResponseWriter, r *http.Request, token, action string) {
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
		q, e := url.ParseQuery(r.URL.RawQuery)
		if e != nil || len(q) != 1 || len(q["profile"]) != 1 {
			writeError(w, 400, "select exactly one profile")
			return
		}
		v, e := h.store.WENFixtureRuns(r.Context(), q.Get("profile"))
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
	raw, e := io.ReadAll(io.LimitReader(r.Body, update.MaxDocument+1))
	if e != nil {
		fail(e)
		return
	}
	var input any
	switch action {
	case "review":
		input = &WENFixtureRunPrepare{}
	case "start":
		input = &WENFixtureRunReview{}
	case "control":
		input = &WENFixtureRunControl{}
	case "reconcile":
		input = &WENFixtureRunReconcile{}
	default:
		writeError(w, 404, "unknown fixture run action")
		return
	}
	if e = DecodeWENFixtureRun(raw, input); e != nil {
		fail(e)
		return
	}
	var result any
	switch v := input.(type) {
	case *WENFixtureRunPrepare:
		result, e = h.ReviewWENFixtureRun(r.Context(), *v)
	case *WENFixtureRunReview:
		result, e = h.StartWENFixtureRun(r.Context(), *v)
	case *WENFixtureRunControl:
		result, e = h.ControlWENFixtureRun(r.Context(), *v)
	case *WENFixtureRunReconcile:
		result, e = h.ReconcileWENFixtureRun(r.Context(), *v)
	}
	if e != nil {
		fail(e)
		return
	}
	writeWENSession(w, result, action == "review")
}
