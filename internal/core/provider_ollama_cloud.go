package core

// Direct hosted inference is distinct from a local server and its vendor login.
// Subscription quota has no API-dollar tariff. Model output remains untrusted.
import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed provider_ollama_cloud.json
var ollamaCloudSource []byte
var currentOllamaCloudModels = func() modelRegistry {
	var r modelRegistry
	if json.Unmarshal(ollamaCloudSource, &r) != nil || r.Version != 1 || r.Provider != "ollama-cloud" || r.Policy != "latest-only" || r.ExpiresAt <= r.ReviewedAt || len(r.Models) != 1 {
		panic("invalid Ollama Cloud registry")
	}
	m := r.Models[0]
	if m.Provider != r.Provider || !strings.HasPrefix(m.ID, "ollama-cloud/gemma") || !validProviderModelID(m.ID) || m.Input != 0 || m.Output != 0 || m.Cached != 0 || m.Currency != "" || m.ContextTokens != 262144 || m.OutputTokens != 8192 || m.Reasoning != "none" {
		panic("invalid Ollama Cloud policy")
	}
	return r
}()

func ollamaRelayPolicy(p *OpenAIModelPolicy) bool { return localPolicy(p) && p.Model == "gemma4:cloud" }
func ollamaCloudPolicy(p *OpenAIModelPolicy) bool {
	return p != nil && p.Provider == "ollama-cloud" && p.WireVersion == "ollama-cloud-chat-v1"
}
