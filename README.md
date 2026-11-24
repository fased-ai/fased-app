<p align="center">
  <img src="docs/assets/fased-mark-color.svg" alt="Fased" width="96">
</p>

# Fased

Self-hosted agents for trading research and automation. Run Fased on your computer or a private server, with one workspace for Chat, Tasks, News and Memory.

**[Install](#install) · [User guide](docs/index.md) · [Providers](docs/guide/providers.md) · [Network](docs/guide/network.md) · [Status](docs/reference/status.md)**

> **Status:** Attested Ubuntu 24.04 x86-64 Local/Remote installation and shared UI/CLI updates, cross-platform CLI & unsigned desktop pre-release packages (Linux, macOS, Windows), live AI providers (OAuth, API key, local server, and Host CLI/ACP) with native OS-protected credentials, Agent V2 with MCP, and watch-only Solana wallets with live read-only RPC are available. Live wallet signing, trading execution, and WEN mining activation remain strictly disabled; Fased Network is planned. See [Availability](docs/reference/status.md).

## What’s inside

| Feature           | Use it for                                                                                                                                               |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Dashboard**     | Arrange your workspace and open the features you use.                                                                                                    |
| **Providers**     | Connect OAuth sign-in, API-key, local-server, or Host CLI/ACP accounts (`codex`, `claude`, `antigravity`, `gemini`, `grok`) with OS-protected secrets.   |
| **Agents & Chat** | Give each agent instructions, rules, tools and persistent threads (`agents.sqlite`), use `/compact`, `/side`, voice input and local/Remote MCP access.   |
| **Tasks**         | Schedule reminders, watch news, mirror completed runs into agent threads and pause automation. Deterministic tasks need no AI provider.                  |
| **News & Memory** | Follow RSS/Atom feeds and retain scoped, pinned or searchable context you can inspect, edit or forget.                                                   |
| **Wallets & RPC** | Watch-only Solana accounts with live read-only RPC (keyless public RPC or OS-protected RPC keys) and portable backups; signing remains disabled.         |
| **Ops & Backup**  | Run `fased-app doctor` diagnostics, hot SQLite `fased-app backup` / `restore`, and unified **Settings** for appearance, devices, updates and MCP access. |

Onchain, Hyperliquid, Mining and the trading-focused Fased Network have separate [roadmaps and availability](docs/reference/status.md). They are not live trading services in this build.

## Install

Attested Ubuntu 24.04 x86-64 is certified for Local and mandatory-Tailscale Remote installation with shared UI/CLI updates (`install.sh`). Cross-platform CLI installers (`install-cli.sh` on Linux/macOS, `install.ps1` on Windows) and unsigned native desktop packages (`.dmg`, Windows `.exe`, Linux `.deb`/`.AppImage`) are available via `-desktop` pre-releases; desktop code signing and in-app auto-updating remain disabled until signing certificates are enabled. Format-4 installations update in place; older format-2 evaluation builds require a fresh install. See [Install](docs/guide/install.md) and [Availability](docs/reference/status.md).

| Choose      | Setup and access                                                                                                                                                              |
| ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Local**   | Automatic setup for your existing OS user; protected local app/browser session. No Tailscale or separate Fased password by default.                                           |
| **Remote**  | Initial SSH/provider-console setup; mandatory verified Tailscale before Fased deployment; private HTTPS and safe public-access lockdown. No continuing SSH session or tunnel. |
| **Connect** | Open the same existing instance and records through its verified owner access.                                                                                                |

The installer supplies compiled Fased and required OS components; no Go, Node, npm, Git, provider or wallet is needed. Root prepares the OS. The app and signer stay unprivileged; service accounts are not customer logins. A new recovery administrator uses an approved SSH public key, never a blank password. Public access closes only after private access and independent recovery work.

## Updates

**Settings → Updates** and `fased update` share one updater, authorization boundary, progress view and recovery transaction on managed Linux installations. Routine updates reuse existing owner/device/OS authority without another password or SSH session, and a CLI client reaches Remote over Tailscale directly. Manual is the default; Automatic is explicit opt-in. Standalone desktop and cross-platform CLI installations check available releases read-only via **Settings → Updates** or `fased-app updates check`, and upgrade in place via `install-cli.sh --upgrade` / `install.ps1 -Upgrade`.

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
