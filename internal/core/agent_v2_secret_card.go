package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

const (
	secretCardKindProviderAPIKey = "provider_api_key"
	secretCardKindRPCAPIKey      = "rpc_api_key"
)

// AgentV2SecretSubmission carries an owner-entered secret only across the HTTP approval request
// to the encrypted vault layer. It must never be persisted to agents.sqlite or logged.
type AgentV2SecretSubmission struct {
	Value      string `json:"value"`
	Passphrase string `json:"passphrase,omitempty"`
	Name       string `json:"name,omitempty"`
}

type requestSecretArgs struct {
	Kind      string `json:"kind"`
	Provider  string `json:"provider"`
	AccountID string `json:"accountId"`
	Ref       string `json:"ref"`
	Label     string `json:"label"`
	Reason    string `json:"reason"`
}

type requestSecretPayload struct {
	Kind             string `json:"kind"`
	Provider         string `json:"provider,omitempty"`
	AccountID        string `json:"accountId,omitempty"`
	AccountName      string `json:"accountName,omitempty"`
	Ref              string `json:"ref,omitempty"`
	Network          string `json:"network,omitempty"`
	RPCEndpoint      string `json:"rpcEndpoint,omitempty"`
	Label            string `json:"label"`
	Reason           string `json:"reason,omitempty"`
	OSVaultAvailable bool   `json:"osVaultAvailable"`
}

func supportedSecretCardProvider(raw string) (string, string, bool) {
	id := strings.ToLower(strings.TrimSpace(raw))
	if id == "" || id == "local" || id == "test" {
		return "", "", false
	}
	def, ok := providerDefinitionFor(id)
	if !ok {
		return "", "", false
	}
	return id, def.Name, true
}

func previewRequestSecret(ctx context.Context, s *Store, _ string, rawArgs json.RawMessage) (approvalDraft, error) {
	var a requestSecretArgs
	if err := decodeToolArgs(rawArgs, &a); err != nil {
		return approvalDraft{}, err
	}
	kind := strings.TrimSpace(a.Kind)
	reason := strings.TrimSpace(a.Reason)
	if len(reason) > 280 {
		reason = sanitizeV2MemorySnippet(reason, 280)
	}
	osVault := s != nil && s.osVaultSecretsAvailable(ctx)

	switch kind {
	case secretCardKindProviderAPIKey:
		providerID, displayName, ok := supportedSecretCardProvider(a.Provider)
		if !ok {
			return approvalDraft{}, errors.New("request_secret provider_api_key requires a supported cloud provider")
		}
		label := strings.TrimSpace(a.Label)
		if label == "" {
			label = displayName + " API key"
		}
		if len(label) > 80 {
			label = sanitizeV2Title(label, displayName+" API key")
		}
		summary := "Enter " + displayName + " API key to save encrypted in vault"
		if reason != "" {
			summary = reason
		}
		payload, _ := json.Marshal(requestSecretPayload{
			Kind:             kind,
			Provider:         providerID,
			Label:            label,
			Reason:           reason,
			OSVaultAvailable: osVault,
		})
		return approvalDraft{
			Title:       "Secret request: " + displayName + " API key",
			Summary:     sanitizeV2Title(summary, displayName+" API key"),
			PayloadJSON: string(payload),
		}, nil

	case secretCardKindRPCAPIKey:
		if s == nil {
			return approvalDraft{}, errors.New("workspace store unavailable")
		}
		accView, err := s.Accounts(ctx)
		if err != nil {
			return approvalDraft{}, errors.New("wallets unavailable")
		}
		target := strings.TrimSpace(a.AccountID)
		var matched *Account
		for i := range accView.Record.Accounts {
			acc := &accView.Record.Accounts[i]
			if target != "" {
				if acc.ID == target || strings.EqualFold(acc.Name, target) {
					matched = acc
					break
				}
			} else if acc.RPC != nil && (acc.RPC.Endpoint == FixtureRPCURL || rpcProviderRequiresKey(rpcProvider(acc.RPC.Endpoint, acc.Network))) {
				matched = acc
				break
			}
		}
		if matched == nil && target == "" {
			for i := range accView.Record.Accounts {
				if accView.Record.Accounts[i].RPC != nil {
					matched = &accView.Record.Accounts[i]
					break
				}
			}
		}
		if matched == nil {
			return approvalDraft{}, errors.New("request_secret rpc_api_key requires an existing wallet with configured RPC")
		}
		if matched.RPC == nil {
			return approvalDraft{}, errors.New("configure an RPC endpoint for the selected wallet first")
		}
		version := 1
		if matched.Version == 2 || managedNetworkAccount(*matched) || ordinaryNetworkAccount(*matched) {
			version = 2
		}
		if version == 2 && !rpcProviderRequiresKey(rpcProvider(matched.RPC.Endpoint, matched.Network)) {
			return approvalDraft{}, errors.New("selected wallet RPC endpoint does not require an API key")
		}
		status, _ := s.RPCCredential(ctx, RPCCredentialCommand{
			AccountID: matched.ID,
			Revision:  accView.Record.Revision,
			Action:    "status",
		})
		ref := strings.TrimSpace(a.Ref)
		if ref == "" {
			ref = status.Ref
		}
		label := strings.TrimSpace(a.Label)
		if label == "" {
			label = matched.Name + " RPC API key"
		}
		if len(label) > 80 {
			label = sanitizeV2Title(label, "RPC API key")
		}
		summary := "Enter RPC API key for " + sanitizeV2Title(matched.Name, matched.ID)
		if reason != "" {
			summary = reason
		}
		payload, _ := json.Marshal(requestSecretPayload{
			Kind:             kind,
			AccountID:        matched.ID,
			AccountName:      matched.Name,
			Ref:              ref,
			Network:          matched.Network,
			RPCEndpoint:      sanitizePublicURL(matched.RPC.Endpoint),
			Label:            label,
			Reason:           reason,
			OSVaultAvailable: osVault,
		})
		return approvalDraft{
			Title:       "Secret request: " + sanitizeV2Title(matched.Name, matched.ID) + " RPC API key",
			Summary:     sanitizeV2Title(summary, "RPC API key"),
			PayloadJSON: string(payload),
		}, nil

	default:
		return approvalDraft{}, errors.New("request_secret kind must be provider_api_key or rpc_api_key")
	}
}

func executeRequestSecretMissingInput(_ context.Context, _ *Store, _ string, _ json.RawMessage) (any, error) {
	return nil, errors.New("request_secret requires an encrypted owner secret submission via the secret-card approval dialog")
}

func executeRequestSecretWithInput(ctx context.Context, s *Store, ap AgentApproval, secret *AgentV2SecretSubmission) (map[string]any, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	if secret == nil {
		return nil, errors.New("secret input required")
	}
	valBytes := []byte(strings.TrimSpace(secret.Value))
	passBytes := []byte(secret.Passphrase)
	defer func() {
		clear(valBytes)
		clear(passBytes)
	}()

	val := string(valBytes)
	if val == "" || len(val) > 512 || strings.ContainsAny(val, "\r\n\x00") {
		return nil, errors.New("invalid secret input")
	}

	var payload requestSecretPayload
	if err := json.Unmarshal([]byte(ap.PayloadJSON), &payload); err != nil {
		return nil, errors.New("invalid secret request payload")
	}

	switch payload.Kind {
	case secretCardKindProviderAPIKey:
		providerID, displayName, ok := supportedSecretCardProvider(payload.Provider)
		if !ok {
			return nil, errors.New("unsupported provider for secret request")
		}
		s.provider.mu.Lock()
		apiCreds := s.provider.apiCredentials
		fixture := s.providerFixture
		s.provider.mu.Unlock()

		mode := providerID
		if !apiCreds || (fixture && testAICredential(val)) {
			mode = "test"
		}
		name := strings.TrimSpace(secret.Name)
		if name == "" {
			name = displayName
		}
		if len(name) > 80 {
			name = sanitizeV2Title(name, displayName)
		}
		view, err := s.ChangeAIConnection(ctx, AIConnectionCommand{
			Action:         "add",
			Provider:       providerID,
			CredentialMode: mode,
			Name:           name,
			Key:            val,
			Passphrase:     string(passBytes),
		})
		if err != nil {
			// Guard against any downstream error accidentally reflecting user secret bytes.
			if strings.Contains(err.Error(), val) {
				return nil, errors.New("failed to save provider credential securely")
			}
			return nil, err
		}
		return map[string]any{
			"status":      "saved",
			"kind":        payload.Kind,
			"provider":    view.Provider,
			"ref":         view.Ref,
			"name":        view.Name,
			"osProtected": view.OSProtected,
		}, nil

	case secretCardKindRPCAPIKey:
		accView, err := s.Accounts(ctx)
		if err != nil {
			return nil, errors.New("wallets unavailable")
		}
		status, _ := s.RPCCredential(ctx, RPCCredentialCommand{
			AccountID: payload.AccountID,
			Revision:  accView.Record.Revision,
			Action:    "status",
		})
		rpcView, err := s.RPCCredential(ctx, RPCCredentialCommand{
			AccountID:  payload.AccountID,
			Revision:   accView.Record.Revision,
			Ref:        status.Ref,
			Action:     "save",
			Key:        val,
			Passphrase: string(passBytes),
		})
		if err != nil {
			if strings.Contains(err.Error(), val) {
				return nil, errors.New("failed to save RPC credential securely")
			}
			return nil, err
		}
		return map[string]any{
			"status":      "saved",
			"kind":        payload.Kind,
			"accountId":   payload.AccountID,
			"accountName": payload.AccountName,
			"ref":         rpcView.Ref,
			"osProtected": rpcView.OSProtected,
		}, nil

	default:
		return nil, errors.New("unsupported secret request kind")
	}
}
