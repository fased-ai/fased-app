package providers

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// PartialAIAnswer extracts the bounded progressive "answer" string field from an
// in-flight structured JSON model response without displaying raw JSON fragments.
func PartialAIAnswer(raw string) string {
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
