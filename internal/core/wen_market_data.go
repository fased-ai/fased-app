package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ConfigureWENResearch opts into an ordinary local read service, before Serve.
// No credentials, cookies, user records or mutable methods reach this service.
func (s *Server) ConfigureWENResearch(origin string) error {
	if origin == "" {
		return nil
	}
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("WEN research requires exact loopback HTTP origin")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		return errors.New("invalid research port")
	}
	h, ok := s.HTTP.Handler.(*HTTPHandler)
	if !ok {
		return errors.New("research handler unavailable")
	}
	h.wenResearchOrigin = origin
	return nil
}
func (h *HTTPHandler) wenMarketDataAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "read only")
		return
	}
	q := r.URL.Query()
	if len(q) > 2 || len(q["lookback"]) > 1 || len(q["profile"]) > 1 || (q.Has("profile") && q.Get("profile") == "") {
		writeError(w, 400, "invalid research query")
		return
	}
	for k := range q {
		if k != "lookback" && k != "profile" {
			writeError(w, 400, "invalid research query")
			return
		}
	}
	n := q.Get("lookback")
	if n == "" {
		n = "1"
	}
	if n != "1" && n != "30" && n != "60" && n != "240" && n != "720" && n != "1440" {
		writeError(w, 400, "invalid research lookback")
		return
	}
	var revision int64
	profile := q.Get("profile")
	if profile != "" {
		v, err := h.store.Agents(r.Context(), profile, "")
		if err != nil {
			writeError(w, 400, "research profile unavailable")
			return
		}
		revision = v.Revision
	}
	if h.wenResearchOrigin == "" {
		writeError(w, 503, "shared research service not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", h.wenResearchOrigin+"/api/mining/v1/research?lookback="+n, nil)
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("research redirect refused") }}
	response, e := client.Do(request)
	if e != nil {
		writeError(w, 503, "research unavailable")
		return
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, 262145))
	if e != nil || len(body) > 262144 || response.StatusCode != 200 {
		writeError(w, 503, "invalid research response")
		return
	}
	var envelope struct {
		Schema    string `json:"schema"`
		Execution string `json:"execution"`
		Method    string `json:"method"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Schema != "wen-research-data/1" || envelope.Execution != "disabled" || envelope.Method != "wen-kraken-research/1" {
		writeError(w, 503, "unsupported research data")
		return
	}
	if profile != "" {
		review, err := h.reviewResearch(body, profile, revision, n)
		if err != nil {
			writeError(w, 503, err.Error())
			return
		}
		var result map[string]json.RawMessage
		if json.Unmarshal(body, &result) != nil {
			writeError(w, 503, "invalid research response")
			return
		}
		result["chatContext"], _ = json.Marshal(review)
		body, _ = json.Marshal(result)
		if len(body) > 262144 {
			writeError(w, 503, "research review exceeds response capacity")
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
