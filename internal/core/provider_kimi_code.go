package core

// Kimi Code membership is a separate personal interactive connector. It never
// acquires task authority or a fabricated per-token dollar tariff.
import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed provider_kimi_code.json
var kimiCodeSource []byte
var currentKimiCodeModels = func() modelRegistry {
	var r modelRegistry
	if json.Unmarshal(kimiCodeSource, &r) != nil || r.Provider != "kimi-code" || r.Version != 1 || r.Policy != "latest-only" || r.ExpiresAt <= r.ReviewedAt || len(r.Models) != 2 {
		panic("invalid Kimi Code registry")
	}
	seen := map[string]bool{}
	for _, m := range r.Models {
		if m.Provider != r.Provider || !strings.HasPrefix(m.ID, "kimi-code/k3.1-pro") || !validProviderModelID(m.ID) || seen[m.ID] || m.Input != 0 || m.Output != 0 || m.Cached != 0 || m.Currency != "" || m.ContextTokens != 262144 || m.OutputTokens != 8192 || m.Reasoning != "low" {
			panic("invalid Kimi Code membership model")
		}
		seen[m.ID] = true
	}
	return r
}()

func kimiCodePolicy(p *OpenAIModelPolicy) bool {
	return p != nil && p.Provider == "kimi-code" && p.WireVersion == "kimi-code-chat-v1"
}
