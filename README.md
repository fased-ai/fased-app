<p align="center">
  <img src="docs/assets/fased-mark-color.svg" alt="Fased" width="96">
</p>

# Fased

Self-hosted agents for trading research and automation. Run Fased on your computer or a private server, with one workspace for Chat, Tasks, News and Memory.

**[Install](#install) · [User guide](docs/index.md) · [Providers](docs/guide/providers.md) · [Network](docs/guide/network.md) · [Status](docs/reference/status.md)**

> **In development.** Public installation is not released yet. Live AI, wallets, trading and automatic updates remain disabled; mining is deferred. Use development installations with test data only.

## What’s inside

| Feature           | Use it for                                                                                                      |
| ----------------- | --------------------------------------------------------------------------------------------------------------- |
| **Dashboard**     | Arrange your workspace and open the features you use.                                                           |
| **Providers**     | Connect supported sign-in, API-key or local-server accounts; share model choices across agents, Chat and Tasks. |
| **Agents & Chat** | Give each agent instructions and context, keep conversations and review proposed work.                          |
| **Tasks**         | Schedule reminders, watch news, inspect results and pause automation. Deterministic tasks need no AI provider.  |
| **News & Memory** | Follow sources and retain useful context you can inspect, correct or forget.                                    |
| **Wallets & RPC** | Protected setup through a separate Go signer; currently limited to disposable keys and controlled endpoints.    |

Onchain, Hyperliquid, Mining and the trading-focused Fased Network have separate [roadmaps and availability](docs/reference/status.md). They are not live trading services in this build.

## Install

Ubuntu 24.04 x86-64 is the first target. **Public downloads are not released yet.** The one-command installer is being verified on a private VPS; no public install command is available today. See the [installation guide](docs/guide/install.md).

The release installer downloads and verifies Fased, prepares the required OS components and opens the terminal wizard automatically. Choose Local, Remote, Connect or Update. A root SSH session is supported: setup prepares an ordinary owner before running Fased. You will choose **SSH** or **Tailscale**, then open the private browser link and create your password. No Go, Node, npm, Git, provider or wallet is required.

| Choose      | Where Fased runs           | How you connect                           |
| ----------- | -------------------------- | ----------------------------------------- |
| **Local**   | Your computer              | Local browser or available desktop client |
| **Remote**  | Your Ubuntu server         | Private SSH or Tailscale access           |
| **Connect** | An existing instance       | Verify the instance and open its UI       |
| **Update**  | Your existing installation | Check, update or inspect recovery         |

Keep SSH and provider-console recovery access. Tailscale is separate software on the server and your computer; the wizard guides server preparation and enrollment. Fased needs no public management port. macOS and Windows distribution remain under acceptance; see [platform status](docs/reference/status.md).

## Updates

`fased update` uses the existing installation and preserves its data. Manual is the default; opt-in Automatic and its maintenance window live in **Settings → Updates**. An existing `fased` command is preserved; use the command name printed by setup. Public update activation remains disabled.

Local and Remote updates have controlled installed verification on Ubuntu24.04 x86-64. Other platform rows and public distribution remain under acceptance; see the [installation guide](docs/guide/install.md#platform-availability).

## Get started

1. Open the address printed by the wizard and complete owner setup, or sign in to your existing instance.
2. Open **Providers** and choose an available connection method.
3. Create an **Agent**, select its account and model, then open **Chat**.
4. Review a **Task**, inspect its result and save useful context to **Memory**.

Provider access is optional for reminders and other deterministic work. Enter credentials only in the dedicated setup screens, never in Chat. Current live-feature restrictions still apply to this flow.

## Guides

[Agents](docs/guide/agents.md) · [Chat](docs/guide/chat.md) · [Tasks](docs/guide/tasks.md) · [News](docs/guide/news.md) · [Memory](docs/guide/memory.md) · [Wallets](docs/guide/wallets.md) · [Settings & recovery](docs/guide/settings.md)

Original Fased App code is licensed under [PolyForm Perimeter 1.0.0](LICENSE). Third-party terms remain in [NOTICE](NOTICE).
