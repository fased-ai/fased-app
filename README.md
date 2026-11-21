<p align="center">
  <img src="docs/assets/fased-mark-color.svg" alt="Fased" width="96">
</p>

# Fased

Self-hosted agents for trading research and automation. Run Fased on your computer or a private server, with one workspace for Chat, Tasks, News and Memory.

**[Install](#install) · [User guide](docs/index.md) · [Providers](docs/guide/providers.md) · [Network](docs/guide/network.md) · [Status](docs/reference/status.md)**

> **Status:** Ubuntu 24.04 x86-64 Local/Remote installation and updates, live AI providers with OS-protected credentials, and watch-only Solana wallets with live read-only RPC are available. Live wallet signing, trading execution, and WEN mining activation remain disabled; Fased Network is planned. See [Availability](docs/reference/status.md).

## What’s inside

| Feature           | Use it for                                                                                                                                          |
| ----------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Dashboard**     | Arrange your workspace and open the features you use.                                                                                               |
| **Providers**     | Connect supported sign-in, API-key or local-server accounts with OS-protected credentials across agents, Chat and Tasks.                            |
| **Agents & Chat** | Give each agent instructions and context, keep conversations and review proposed work.                                                              |
| **Tasks**         | Schedule reminders, watch news, inspect results and pause automation. Deterministic tasks need no AI provider.                                      |
| **News & Memory** | Follow sources and retain useful context you can inspect, correct or forget.                                                                        |
| **Wallets & RPC** | Watch-only Solana accounts with live read-only RPC (keyless public Solana RPC or OS-protected RPC keys); signing and funds custody remain disabled. |

Onchain, Hyperliquid, Mining and the trading-focused Fased Network have separate [roadmaps and availability](docs/reference/status.md). They are not live trading services in this build.

## Install

Ubuntu 24.04 x86-64 is supported for Local and mandatory-Tailscale Remote installation; other platforms remain unverified. Format-4 installations update in place; older format-2 evaluation builds require a fresh install. See [Install](docs/guide/install.md) and [Availability](docs/reference/status.md).

| Choose      | Setup and access                                                                                                                                                              |
| ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Local**   | Automatic setup for your existing OS user; protected local app/browser session. No Tailscale or separate Fased password by default.                                           |
| **Remote**  | Initial SSH/provider-console setup; mandatory verified Tailscale before Fased deployment; private HTTPS and safe public-access lockdown. No continuing SSH session or tunnel. |
| **Connect** | Open the same existing instance and records through its verified owner access.                                                                                                |

The installer supplies compiled Fased and required OS components; no Go, Node, npm, Git, provider or wallet is needed. Root prepares the OS. The app and signer stay unprivileged; service accounts are not customer logins. A new recovery administrator uses an approved SSH public key, never a blank password. Public access closes only after private access and independent recovery work.

## Updates

**Settings → Updates** and optional `fased update` use the same updater, authorization rules, progress, result and recovery. Routine updates reuse existing owner/device/OS authority without another password or account switch. A CLI client reaches Remote over Tailscale directly, without SSH. Manual is the default; Automatic is explicit opt-in and runs without an open browser or SSH session.

Updates preserve data and wallet permissions; they never unlock signing or grant spending authority. [Details](docs/guide/install.md).

## Get started

1. Open the address printed by the wizard and complete owner setup, or sign in to your existing instance.
2. Open **Providers** and choose an available connection method.
3. Create an **Agent**, select its account and model, then open **Chat**.
4. Review a **Task**, inspect its result and save useful context to **Memory**.

Provider access is optional for reminders and other deterministic work. Enter credentials only in the dedicated setup screens, never in Chat. Trading execution, signing keys, and WEN mining remain disabled.

## Guides

[Agents](docs/guide/agents.md) · [Chat](docs/guide/chat.md) · [Tasks](docs/guide/tasks.md) · [News](docs/guide/news.md) · [Memory](docs/guide/memory.md) · [Wallets](docs/guide/wallets.md) · [Settings & recovery](docs/guide/settings.md)

Original Fased App code is licensed under [PolyForm Perimeter 1.0.0](LICENSE). Third-party terms remain in [NOTICE](NOTICE).
