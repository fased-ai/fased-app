---
title: "Providers"
description: "One account and model catalogue across agents, Chat and Tasks."
---

Open **Providers**, choose a provider and use an available **Sign in**, **API key** or **Server** method. Connection setup must not generate a paid answer. Requests begin with Send or an authorized Task.

The intended catalogue covers OpenAI, Anthropic, xAI, Ollama, LM Studio, vLLM, Moonshot/Kimi, Google, OpenRouter, Qwen and OpenCode Zen. Method availability is provider-specific; a vendor's consumer subscription is not automatically API access. Unsupported sign-in remains unavailable rather than using another application's credentials.

Supported connector engineering uses one encrypted account vault and reviewed model catalogue. Models must be available to the selected account and satisfy the current provider review. Refreshing the catalogue preserves saved choices/history; it does not silently replace a Task's reviewed model. Model names and availability come from the account, not static examples in these docs.

Assign an account/model in **Agents**, then use the same identity in Chat and authorized Tasks. Local server means the machine running the selected Fased core; a Remote core's loopback is not the browser's computer.

**Reconnect** replaces the credential binding while preserving history. **Lock** ends local access. **Disconnect** removes the local connection; provider-side revocation is a separate action. Uncertain usage retains its hold. Changes to accounts or models may require a new Task review.

<Note>Controlled integrations are implemented; live credentials, model calls and user acceptance remain held in this development stage. No paid pilot or real API test is required to document the integration. See [Status](/reference/status).</Note>
