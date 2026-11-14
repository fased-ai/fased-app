package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strings"
	"time"
)

// A short-lived review permits only one exact, inert message at one profile and
// workspace revision. It is not stored authority, a model request or a mandate.
type WENResearchChatReview struct {
	Schema    string `json:"schema"`
	ProfileID string `json:"profileId"`
	Revision  int64  `json:"revision"`
	ExpiresAt int64  `json:"expiresAt"`
	Text      string `json:"text"`
	Source    string `json:"source"`
}

var researchCursor = regexp.MustCompile(`^(0|[1-9][0-9]{0,19})$`)
var researchDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var researchSignedMetric = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,19})$`)
var researchMetric = regexp.MustCompile(`^(0|[1-9][0-9]{0,19})$`)

func (h *HTTPHandler) researchChatSignature(profile string, revision, expires int64, text string) string {
	payload, _ := json.Marshal(struct {
		Domain, Profile, Text string
		Revision, Expires     int64
	}{"wen-research-chat-review/1", profile, text, revision, expires})
	mac := hmac.New(sha256.New, h.wenResearchKey[:])
	_, _ = mac.Write(payload)
	return "wen-research-review-v1:" + hex.EncodeToString(mac.Sum(nil))
}

func (h *HTTPHandler) authorizeResearchChat(c *AgentCommand) error {
	refused := errors.New("research review changed, expired or belongs to another core; select the source again")
	now := h.store.now().Unix()
	if c.ProfileID == "" || c.ExpiresAt <= now || c.ExpiresAt > now+120 || !validAgentText(c.Text, 4000, true) || !onlyResearchChatFields(*c) {
		return refused
	}
	expected := h.researchChatSignature(c.ProfileID, c.Revision, c.ExpiresAt, c.Text)
	if !hmac.Equal([]byte(expected), []byte(c.Source)) {
		return refused
	}
	c.wenResearchValidated = true
	return nil
}

func onlyResearchChatFields(c AgentCommand) bool {
	c.Action, c.ProfileID, c.Text, c.Source = "", "", "", ""
	c.Revision, c.ExpiresAt, c.wenResearchValidated = 0, 0, false
	return c == (AgentCommand{})
}

func (s *Store) captureWENResearchContext(ctx context.Context, r snapshotReader, d *agentDocument, c AgentCommand, profile, id string, now int64) error {
	if !c.wenResearchValidated || !onlyResearchChatFields(c) {
		return errors.New("research context must be reviewed through the authenticated core")
	}
	if len(d.Sessions) >= 100 || len(d.Messages) >= 200 {
		return errors.New("conversation/message capacity reached; no research saved")
	}
	title := "Research: Kraken BTC/USD"
	if strings.HasPrefix(c.Text, savedChatPrefix) {
		var projection savedChatProjection
		lines := strings.Split(c.Text, "\n")
		if len(lines) != 3 || json.Unmarshal([]byte(lines[1]), &projection) != nil || projection.Selection.Profile != profile {
			return errors.New("saved context changed; select the source again")
		}
		text, name, err := s.savedWENChatText(ctx, r, projection.Selection, c.Revision, projection.PreparedAt)
		if err != nil {
			return err
		}
		if text != c.Text {
			return errors.New("saved context changed; select the source again")
		}
		title = name
	}
	d.Sessions = append(d.Sessions, AgentSession{ID: id, ProfileID: profile, Name: title, CreatedAt: now})
	d.Messages = append(d.Messages, AgentMessage{ID: id + "-research", ProfileID: profile, SessionID: id, Role: "user", Text: c.Text, CreatedAt: now})
	return nil
}

func (h *HTTPHandler) reviewResearch(body []byte, profile string, revision int64, lookback string) (*WENResearchChatReview, error) {
	bad := errors.New("invalid research provenance; no chat review prepared")
	if err := unambiguousResearchJSON(body); err != nil {
		return nil, bad
	}
	var exact map[string]json.RawMessage
	if json.Unmarshal(body, &exact) != nil || !researchFields(exact, "schema", "execution", "finality", "method", "generatedAt", "sourceCursor", "connection", "data") {
		return nil, bad
	}
	var exactData map[string]json.RawMessage
	if json.Unmarshal(exact["data"], &exactData) != nil || !researchFields(exactData, "method", "quality", "authoritative", "quote", "quoteEventAt", "quoteReceivedAt", "inputDigest", "candles", "observationIds", "metrics") {
		return nil, bad
	}
	var p struct {
		Schema, Execution, Finality, Method, GeneratedAt, SourceCursor, Connection string
		Data                                                                       struct {
			Method, Quality, InputDigest, QuoteEventAt, QuoteReceivedAt string
			Authoritative                                               *bool
			Quote                                                       *json.Number
			Candles                                                     *[]json.RawMessage
			ObservationIDs                                              []string
			Metrics                                                     json.RawMessage
		}
	}
	if json.Unmarshal(body, &p) != nil || p.Schema != "wen-research-data/1" || p.Execution != "disabled" || p.Finality != "research" || p.Method != "wen-kraken-research/1" || p.Data.Method != p.Method || p.Data.Authoritative == nil || *p.Data.Authoritative || !researchCursor.MatchString(p.SourceCursor) || !researchDigest.MatchString(p.Data.InputDigest) || p.Data.Candles == nil || len(*p.Data.Candles) > 1441 || p.Data.ObservationIDs == nil || len(p.Data.ObservationIDs) > 1442 || len(p.Data.Metrics) == 0 {
		return nil, bad
	}
	for _, id := range p.Data.ObservationIDs {
		if !validAgentText(id, 128, true) {
			return nil, bad
		}
	}
	var metrics *struct {
		ReturnBps             string `json:"returnBps"`
		RealizedVolatilityBps string `json:"realizedVolatilityBps"`
	}
	if json.Unmarshal(p.Data.Metrics, &metrics) != nil || (metrics != nil && (!researchSignedMetric.MatchString(metrics.ReturnBps) || !researchMetric.MatchString(metrics.RealizedVolatilityBps))) {
		return nil, bad
	}
	if metrics != nil {
		var fields map[string]json.RawMessage
		if json.Unmarshal(p.Data.Metrics, &fields) != nil || !researchFields(fields, "returnBps", "realizedVolatilityBps") {
			return nil, bad
		}
	}
	if p.Data.Quality != "valid" && p.Data.Quality != "stale" && p.Data.Quality != "invalid" {
		return nil, bad
	}
	if p.Data.Quality != "valid" || p.Data.Quote == nil || p.Connection != "connected" {
		return nil, nil
	}
	quote, err := p.Data.Quote.Float64()
	if err != nil || math.IsInf(quote, 0) || math.IsNaN(quote) || quote <= 0 {
		return nil, bad
	}
	now := h.store.now()
	for i, stamp := range []string{p.GeneratedAt, p.Data.QuoteEventAt, p.Data.QuoteReceivedAt} {
		at, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || !strings.HasSuffix(stamp, "Z") {
			return nil, bad
		}
		maxAge := 5 * time.Second
		if i == 0 {
			maxAge = 10 * time.Second
		}
		if now.Sub(at) > maxAge || at.Sub(now) > time.Second {
			return nil, nil
		}
	}
	projection := struct {
		Schema           string      `json:"schema"`
		ProfileID        string      `json:"profileId"`
		Revision         int64       `json:"workspaceRevision"`
		PreparedAt       string      `json:"preparedAt"`
		Source           string      `json:"source"`
		Method           string      `json:"method"`
		LookbackMinutes  string      `json:"lookbackMinutes"`
		GeneratedAt      string      `json:"generatedAt"`
		SourceCursor     string      `json:"sourceCursor"`
		InputDigest      string      `json:"inputDigest"`
		ResponseSHA256   string      `json:"sourceResponseSHA256"`
		Quote            json.Number `json:"btcUsdMidpoint"`
		QuoteEventAt     string      `json:"quoteEventAt"`
		QuoteReceivedAt  string      `json:"quoteReceivedAt"`
		Metrics          any         `json:"metrics"`
		CandleCount      int         `json:"candleCount"`
		ObservationCount int         `json:"observationCount"`
		FreshAtRead      bool        `json:"freshAtRead"`
	}{"wen-research-chat-context/1", profile, revision, now.UTC().Format(time.RFC3339Nano), "Kraken BTC/USD BBO", p.Method, lookback, p.GeneratedAt, p.SourceCursor, p.Data.InputDigest, wenHash(body), *p.Data.Quote, p.Data.QuoteEventAt, p.Data.QuoteReceivedAt, metrics, len(*p.Data.Candles), len(p.Data.ObservationIDs), true}
	b, err := json.Marshal(projection)
	if err != nil {
		return nil, bad
	}
	text := "Frozen Kraken BTC/USD research — unverified venue data, not settlement observations or a fresh task input. Summary only; candles, ticks and private history are excluded. No AI, task or financial authority.\n" + string(b)
	text += "\nSnapshot SHA-256: " + wenHash([]byte(text))
	if !validAgentText(text, 4000, true) {
		return nil, bad
	}
	r := &WENResearchChatReview{Schema: "wen-research-chat-review/1", ProfileID: profile, Revision: revision, ExpiresAt: now.Add(120 * time.Second).Unix(), Text: text}
	r.Source = h.researchChatSignature(profile, revision, r.ExpiresAt, text)
	return r, nil
}

func researchFields(m map[string]json.RawMessage, fields ...string) bool {
	for _, field := range fields {
		if _, ok := m[field]; !ok {
			return false
		}
	}
	return true
}

// Research quotes use decimal JSON numbers; the financial F1 integer parser is
// deliberately not widened. Reject ambiguous keys, deep nesting and trailers.
func unambiguousResearchJSON(body []byte) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return errors.New("research nesting limit")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[strings.ToLower(name)] {
						return errors.New("ambiguous research key")
					}
					seen[strings.ToLower(name)] = true
					if err = walk(depth + 1); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
			default:
				return errors.New("unexpected delimiter")
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("research trailer")
	}
	return nil
}
