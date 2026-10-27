package core

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Local lexical recall: full current notes only, no embeddings/model call and no
// inferred facts. Keep automatic context small and exclude expired/foreign notes.
func relevantMemoryIDs(d agentDocument, profile, query string, now int64) []string {
	stop := map[string]bool{}
	for _, v := range strings.Fields("the and for are with that this what about your you how from have my is do to a an of me it in on") {
		stop[v] = true
	}
	terms := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if utf8.RuneCountInString(word) >= 2 && !stop[word] {
			terms[word] = true
		}
		runes := []rune(word)
		for i := 1; i < len(runes); i++ {
			if unicode.Is(unicode.Han, runes[i-1]) && unicode.Is(unicode.Han, runes[i]) {
				terms[string(runes[i-1:i+1])] = true
			}
		}
	}
	type ranked struct {
		m     AgentMemory
		score int
	}
	var candidates []ranked
	all := strings.Contains(strings.ToLower(query), "my preferences") || strings.Contains(strings.ToLower(query), "remember about")
	for _, m := range d.Memories {
		if m.ProfileID != profile || (m.ExpiresAt > 0 && m.ExpiresAt <= now) {
			continue
		}
		score := 0
		text := strings.ToLower(m.Text)
		for term := range terms {
			if strings.Contains(text, term) {
				score++
			}
		}
		if score > 0 || all {
			candidates = append(candidates, ranked{m, score})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.m.UpdatedAt != b.m.UpdatedAt {
			return a.m.UpdatedAt > b.m.UpdatedAt
		}
		return a.m.ID < b.m.ID
	})
	ids := []string{}
	used := 0
	for _, item := range candidates {
		m := item.m
		raw, _ := json.Marshal(aiSelectedMemory{m.ID, m.Text, m.Source, m.UpdatedAt, m.ExpiresAt})
		if used+len(raw) > 2400 {
			continue
		}
		used += len(raw)
		ids = append(ids, m.ID)
		if len(ids) == 5 {
			break
		}
	}
	sort.Strings(ids)
	return ids
}

// /remember is an explicit owner save command; ordinary messages, model output
// and news are never harvested. The existing source-bound memory writer owns it.
func rememberOwnerMessage(d *agentDocument, profile, messageID, text string, now int64) (bool, error) {
	if !strings.HasPrefix(strings.TrimSpace(text), "/remember") {
		return false, nil
	}
	command := strings.TrimSpace(text)
	if command == "/remember" {
		return false, errors.New("add the fact after /remember")
	}
	rest := strings.TrimPrefix(command, "/remember")
	first, _ := utf8.DecodeRuneInString(rest)
	if !unicode.IsSpace(first) {
		return false, nil
	}
	note := strings.TrimSpace(strings.TrimPrefix(command, "/remember"))
	if note == "" {
		return false, errors.New("add the fact after /remember")
	}
	for _, m := range d.Memories {
		if m.ProfileID == profile && m.Text == note && (m.ExpiresAt == 0 || m.ExpiresAt > now) {
			return true, nil
		}
	}
	if e := saveChatMemory(d, AgentCommand{ID: messageID, Source: "message", Text: note}, profile, "remember:"+messageID, now); e != nil {
		return false, e
	}
	return true, nil
}
