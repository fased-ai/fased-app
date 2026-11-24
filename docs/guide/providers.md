---
title: "Providers"
description: "Connect an account and choose models for agents, Chat and Tasks."
---

Open **Providers**, choose a provider and select an available **Sign in**, **CLI**, **API key** or **Server** method. Supported methods vary by provider. A consumer subscription does not always include API access.

When signing in with OAuth on a Remote server, the browser redirects to a `127.0.0.1` callback URL; copy that address-bar URL into the guided **Redirect URL** modal and choose **Finish**, or run the local callback bridge (`fased-app ai-connection --remote --sign-in <openai|google|openrouter>`).

The provider selection includes OpenAI (OAuth, API key, or Codex CLI), Anthropic (API key or Claude Code CLI), Google (OAuth, API key, Gemini CLI, or Antigravity ACP), xAI (API key or Grok CLI), OpenRouter, Moonshot/Kimi, Qwen, OpenCode Zen, Ollama, LM Studio and vLLM. Available models belong to the selected account; refresh the model list to see current choices.

Choose an account and model in **Agents**, then use them in Chat and authorized Tasks. Adding a connection does not generate a paid answer. Usage begins when you send a message or authorize a Task that uses AI.

Credentials are stored encrypted. **Reconnect** updates account access while preserving history. **Lock** ends access to stored credentials. **Disconnect** removes the local connection; revoke the key or session at the provider if you also want to end provider-side access.

For a local model server or Host CLI adapter, the endpoint or CLI runs on the machine hosting Fased. When using Remote, that is your server, not the computer displaying the browser.

Credentials stay encrypted in native OS-protected storage across restarts without a vault password prompt on supported hosts: Linux (`systemd-creds` / Secret Service), macOS (Keychain), and Windows (DPAPI), with passphrase fallback when OS keyrings are unavailable. See [Availability](/reference/status).
