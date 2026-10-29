package core

// Workspace routing is part of the encrypted credential, not a mutable request
// header. Existing workspace-scoped API keys retain their original v4 bytes.
import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type anthropicWorkspaceCredential struct {
	Version   int    `json:"version"`
	APIKey    string `json:"apiKey"`
	Workspace string `json:"workspace"`
}
type anthropicWorkspaceContextKey struct{}

func validAnthropicWorkspace(id string) bool {
	if !strings.HasPrefix(id, "wrkspc_") || len(id) < 27 || len(id) > 71 {
		return false
	}
	for _, c := range id[7:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func parseAnthropicWorkspace(raw []byte, mode string) (anthropicWorkspaceCredential, bool) {
	var c anthropicWorkspaceCredential
	if len(raw) > 1024 || !uniqueAIJSON(raw, 64) {
		return c, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != 1 || !validAnthropicWorkspace(c.Workspace) || (mode != "test" && mode != "anthropic") || !validAICredential(mode, c.APIKey) {
		return anthropicWorkspaceCredential{}, false
	}
	return c, true
}
