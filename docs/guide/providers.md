---
title: "Providers"
description: "Connect an account and choose models for agents, Chat and Tasks."
---

Open **Providers**, choose a provider and select an available **Sign in**, **API key** or **Server** method. Supported methods vary by provider. A consumer subscription does not always include API access.

When signing in with ChatGPT on a Remote server, the browser redirects to a `127.0.0.1` callback URL; copy that address-bar URL into **Redirect URL** and choose **Finish**.

The provider selection includes OpenAI, Anthropic, xAI, Ollama, LM Studio, vLLM, Moonshot/Kimi, Google, OpenRouter, Qwen and OpenCode Zen. Available models belong to the selected account; refresh the model list to see current choices.

Choose an account and model in **Agents**, then use them in Chat and authorized Tasks. Adding a connection does not generate a paid answer. Usage begins when you send a message or authorize a Task that uses AI.

Credentials are stored encrypted. **Reconnect** updates account access while preserving history. **Lock** ends access to stored credentials. **Disconnect** removes the local connection; revoke the key or session at the provider if you also want to end provider-side access.

For a local model server, the address is reached from the machine running Fased. When using Remote, that is your server, not the computer displaying the browser.

Credentials stay encrypted in OS-protected storage across restarts without a vault password prompt on supported Linux hosts (with passphrase fallback when OS keyrings are unavailable). See [Availability](/reference/status).
