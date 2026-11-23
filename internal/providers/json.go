package providers

import (
	"bytes"
	"encoding/json"
	"io"
)

// UniqueJSON verifies that raw decodes as a single JSON value with unique object keys
// within bounded depth (<=16) and token count (<=4096 by default).
func UniqueJSON(raw []byte, budgets ...int) bool {
	budget := 4096
	if len(budgets) > 0 {
		budget = budgets[0]
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	tokens := 0
	var value func(int) bool
	value = func(depth int) bool {
		tokens++
		if depth > 16 || tokens > budget {
			return false
		}
		t, e := d.Token()
		if e != nil {
			return false
		}
		open, container := t.(json.Delim)
		if !container {
			return true
		}
		keys := map[string]bool{}
		for d.More() {
			if open == '{' {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || keys[name] {
					return false
				}
				keys[name] = true
			}
			if !value(depth + 1) {
				return false
			}
		}
		closeDelim, err := d.Token()
		return err == nil && ((open == '{' && closeDelim == json.Delim('}')) || (open == '[' && closeDelim == json.Delim(']')))
	}
	if !value(0) {
		return false
	}
	_, e := d.Token()
	return e == io.EOF
}
