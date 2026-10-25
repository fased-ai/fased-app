package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Progress is a non-authoritative, bounded projection. Only a fully validated
// completed response becomes an answer or review card in durable state.
func partialAIAnswer(raw string) string {
	d := json.NewDecoder(strings.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return ""
	}
	token, e = d.Token()
	if e != nil || token != "answer" {
		return ""
	}
	tail := strings.TrimSpace(raw[d.InputOffset():])
	if !strings.HasPrefix(tail, ":") {
		return ""
	}
	tail = strings.TrimSpace(tail[1:])
	if !strings.HasPrefix(tail, "\"") {
		return ""
	}
	escaped := false
	end := 1
	for end < len(tail) {
		c := tail[end]
		if c == '"' && !escaped {
			var text string
			if json.Unmarshal([]byte(tail[:end+1]), &text) == nil && len(text) <= 2000 {
				return text
			}
			return ""
		}
		if c == '\\' && !escaped {
			escaped = true
		} else {
			escaped = false
		}
		end++
	}
	// Trim an unfinished escape or UTF-8 rune; never display raw JSON fragments.
	for end > 1 {
		var text string
		candidate := tail[:end] + "\""
		if json.Unmarshal([]byte(candidate), &text) == nil && utf8.ValidString(text) {
			if len(text) > 2000 {
				return ""
			}
			return text
		}
		end--
	}
	return ""
}
func readAIStream(body io.Reader, model string, onDelta func(string)) ([]byte, error) {
	scanner := bufio.NewScanner(io.LimitReader(body, 262145))
	scanner.Buffer(make([]byte, 4096), 32768)
	var data, event, identity, output string
	var completed []byte
	sequence := int64(-1)
	total := 0
	seenCreated, seenDone := false, false
	fail := func() ([]byte, error) {
		return nil, errors.New("provider stream incomplete or invalid; reservation retained")
	}
	consume := func() bool {
		if data == "" {
			event = ""
			return true
		}
		if data == "[DONE]" {
			data, event = "", ""
			return completed != nil
		}
		if completed != nil || !utf8.ValidString(data) || !uniqueAIJSON([]byte(data)) {
			return false
		}
		var frame struct {
			Type         string          `json:"type"`
			Sequence     *int64          `json:"sequence_number"`
			Delta        string          `json:"delta"`
			Text         string          `json:"text"`
			OutputIndex  int             `json:"output_index"`
			ContentIndex int             `json:"content_index"`
			Response     json.RawMessage `json:"response"`
		}
		if json.Unmarshal([]byte(data), &frame) != nil || frame.Type == "" || (event != "" && event != frame.Type) || frame.Sequence == nil || *frame.Sequence != sequence+1 {
			return false
		}
		sequence = *frame.Sequence
		switch frame.Type {
		case "response.created":
			if seenCreated || sequence != 0 {
				return false
			}
			var r struct{ ID, Model string }
			if json.Unmarshal(frame.Response, &r) != nil || r.ID == "" || r.Model != model {
				return false
			}
			identity = r.ID
			seenCreated = true
		case "response.in_progress", "response.output_item.added", "response.content_part.added", "response.content_part.done", "response.output_item.done":
			if !seenCreated {
				return false
			}
		case "response.output_text.delta":
			if !seenCreated || seenDone || frame.OutputIndex != 0 || frame.ContentIndex != 0 || frame.Delta == "" {
				return false
			}
			output += frame.Delta
			if len(output) > 16384 {
				return false
			}
			if onDelta != nil {
				onDelta(partialAIAnswer(output))
			}
		case "response.output_text.done":
			if !seenCreated || seenDone || frame.OutputIndex != 0 || frame.ContentIndex != 0 || frame.Text != output {
				return false
			}
			seenDone = true
		case "response.completed":
			var r struct {
				ID, Model, Status string
				Output            []struct{ Content []struct{ Text string } }
			}
			if !seenDone || json.Unmarshal(frame.Response, &r) != nil || r.ID != identity || r.Model != model || r.Status != "completed" || len(r.Output) != 1 || len(r.Output[0].Content) != 1 || r.Output[0].Content[0].Text != output || len(frame.Response) > 16384 {
				return false
			}
			completed = append([]byte(nil), frame.Response...)
		default:
			return false
		}
		data, event = "", ""
		return true
	}
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > 262144 {
			return fail()
		}
		if line == "" {
			if !consume() {
				return fail()
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			if event != "" {
				return fail()
			}
			event = strings.TrimSpace(line[6:])
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data != "" {
				return fail()
			}
			data = strings.TrimPrefix(line[5:], " ")
			continue
		}
		return fail()
	}
	if scanner.Err() != nil || !consume() || completed == nil {
		return fail()
	}
	return completed, nil
}
func fixtureAIStream(r *http.Request, body, output []byte) *http.Response {
	reader, writer := io.Pipe()
	go func() {
		defer writer.Close()
		sequence := 0
		send := func(kind string, fields map[string]any) bool {
			fields["type"] = kind
			fields["sequence_number"] = sequence
			sequence++
			b, _ := json.Marshal(fields)
			_, e := writer.Write(append(append([]byte("event: "+kind+"\ndata: "), b...), []byte("\n\n")...))
			return e == nil
		}
		if !send("response.created", map[string]any{"response": map[string]any{"id": "fixture-response", "model": func() string { var v struct{ Model string }; _ = json.Unmarshal(body, &v); return v.Model }()}}) {
			return
		}
		for start := 0; start < len(output); {
			end := start + 12
			if end > len(output) {
				end = len(output)
			}
			for end < len(output) && !utf8.RuneStart(output[end]) {
				end++
			}
			select {
			case <-r.Context().Done():
				writer.CloseWithError(r.Context().Err())
				return
			case <-time.After(35 * time.Millisecond):
			}
			if !send("response.output_text.delta", map[string]any{"delta": string(output[start:end]), "output_index": 0, "content_index": 0}) {
				return
			}
			start = end
		}
		if !send("response.output_text.done", map[string]any{"text": string(output), "output_index": 0, "content_index": 0}) {
			return
		}
		send("response.completed", map[string]any{"response": json.RawMessage(body)})
	}()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader, Request: r}
}

// Withhold a key-length suffix across reads so a credential split across chunks
// cannot escape redaction. This guard never buffers a completed stream first.
type guardedAIBody struct {
	body                io.ReadCloser
	key, pending, ready []byte
	total               int
	eof                 bool
}

func (b *guardedAIBody) Read(dst []byte) (int, error) {
	for len(b.ready) == 0 {
		if b.eof {
			return 0, io.EOF
		}
		chunk := make([]byte, 4096)
		n, e := b.body.Read(chunk)
		b.total += n
		b.pending = append(b.pending, chunk[:n]...)
		if b.total > 262144 || bytes.Contains(b.pending, b.key) {
			clear(b.pending)
			return 0, errors.New("provider response refused")
		}
		safe := len(b.pending) - len(b.key) + 1
		if e == io.EOF {
			safe = len(b.pending)
			b.eof = true
		} else if e != nil {
			return 0, errors.New("provider response unavailable")
		}
		if safe > 0 {
			b.ready = append(b.ready, b.pending[:safe]...)
			b.pending = append(b.pending[:0], b.pending[safe:]...)
		}
	}
	n := copy(dst, b.ready)
	b.ready = b.ready[n:]
	return n, nil
}
func (b *guardedAIBody) Close() error {
	clear(b.key)
	clear(b.pending)
	clear(b.ready)
	return b.body.Close()
}
