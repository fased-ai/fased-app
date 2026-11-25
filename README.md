<p align="center">
  <img src="https://docs.fased.ai/assets/fased-mark-color.svg" alt="Fased" width="96">
</p>

# Fased

Self-hosted agents for trading research and automation. Run Fased on your computer or a private server, with one workspace for Chat, Tasks, News and Memory.

**[Install](#install) · [User guide](https://docs.fased.ai) · [Providers](https://docs.fased.ai/guide/providers) · [Network](https://docs.fased.ai/guide/network) · [Status](https://docs.fased.ai/reference/status)**

> **Status:** Attested Ubuntu 24.04 x86-64 Local/Remote installation and shared UI/CLI updates, cross-platform CLI & unsigned desktop pre-release packages (Linux, macOS, Windows), live AI providers (OAuth, API key, local server, and Host CLI/ACP) with native OS-protected credentials, TypeSafe Jev System-1 decisions, Agent V2 with `/api/v1/mcp` server and external MCP client, watch-only Solana wallets with live read-only RPC, and connected Stages 1–3 research/charting/intelligence/automation/QuantLab/Paper-Desk workspaces (`Trade`, `Intel`, `Lab`, `Tasks` + Telegram) are available. Live wallet signing, real-money trading execution, and in-app auto-updates remain strictly disabled; **WEN `SAT` mining execution is NOT READY / DO NOT IMPLEMENT NOW**. See [Availability](https://docs.fased.ai/reference/status).

## What’s inside

| Feature                  | Use it for                                                                                                                                                                                                                           |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Dashboard**            | Arrange your workspace with live overview cards from Trade, Intel, Lab/Desk, Tasks, and Wallets.                                                                                                                                     |
| **Trade**                | Multi-chart `1×1`–`4×4` Lightweight Charts v5 workspace with 9 candle styles, Orderflow (`Footprint`, `DOM`, `Tape`, `VP`, `CVD`, `Intel`), FasedScript overlays, and Paper/Shadow `Desk` dock.                                      |
| **News & Intel**         | Clean-room global WorldMap (`GDELT`, `USGS`, `RSS`, `SEC`, `FRED`), Polymarket↔Kalshi odds & arbitrage scanner, and clickable chart event pins.                                                                                      |
| **Lab & Paper Desk**     | Sandboxed `FasedScript` strategy DSL, deterministic 4-Gate validation (plateau, 1,000× bar perm, walk-forward + holdout, 200× WF perm), and 8-seat Paper/Shadow Trading Desk (`Chief → Journal`).                                    |
| **Providers, Jev & MCP** | Connect OAuth sign-in, API-key, local-server, or Host CLI/ACP accounts (`codex`, `claude`, `antigravity`, `gemini`, `grok`), configure TypeSafe Jev System-1, and connect SSRF-guarded external MCP servers alongside `/api/v1/mcp`. |
| **Agents & Chat**        | Persistent `agents.sqlite` threads, `/compact`, `/side`, voice input, FTS5 memory & rules, and 39 Agent V2 tools / 36 MCP tools (`draft_lab_script`, `draft_event_trigger`, `run_desk_cycle`, etc.).                                 |
| **Tasks & Telegram**     | Schedule cron jobs and event-driven triggers (`price`, `indicator`, `fasedscript`, `news`, `prediction`), webhooks, and fail-closed Telegram alerts/approvals without requiring an AI provider for deterministic checks.             |
| **Wallets & RPC**        | Watch-only Solana accounts with live read-only RPC (keyless public RPC or OS-protected RPC keys) and portable backups; signing and live execution remain disabled.                                                                   |
| **Ops & Backup**         | Run `fased-app doctor` diagnostics, hot SQLite `fased-app backup` / `restore` across core and Stage 1–3 databases, and unified **Settings** for appearance, devices, updates, notifications, and MCP access.                         |

Onchain live execution, Hyperliquid live perps, and Fased Network have separate [roadmaps and availability](https://docs.fased.ai/reference/status). WEN `SAT` mining execution is marked **NOT READY / DO NOT IMPLEMENT NOW** (`miningActivationEnabled: false`). Real-money order placement is disabled in this build.

## Install

Attested Ubuntu 24.04 x86-64 is certified for Local and mandatory-Tailscale Remote installation with shared UI/CLI updates (`install.sh`). Cross-platform CLI installers (`install-cli.sh` on Linux/macOS, `install.ps1` on Windows) and unsigned native desktop packages (`.dmg`, Windows `.exe`, Linux `.deb`/`.AppImage`) are available via `-desktop` pre-releases; desktop code signing and in-app auto-updating remain disabled until signing certificates are enabled. Format-4 installations update in place; older format-2 evaluation builds require a fresh install. See [Install](https://docs.fased.ai/guide/install) and [Availability](https://docs.fased.ai/reference/status).

| Choose      | Setup and access                                                                                                                                                              |
| ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Local**   | Automatic setup for your existing OS user; protected local app/browser session. No Tailscale or separate Fased password by default.                                           |
| **Remote**  | Initial SSH/provider-console setup; mandatory verified Tailscale before Fased deployment; private HTTPS and safe public-access lockdown. No continuing SSH session or tunnel. |
| **Connect** | Open the same existing instance and records through its verified owner access.                                                                                                |

The installer supplies compiled Fased and required OS components; no Go, Node, npm, Git, provider or wallet is needed. Root prepares the OS. The app and signer stay unprivileged; service accounts are not customer logins. A new recovery administrator uses an approved SSH public key, never a blank password. Public access closes only after private access and independent recovery work.

## Updates

**Settings → Updates** and `fased update` share one updater, authorization boundary, progress view and recovery transaction on managed Linux installations. Routine updates reuse existing owner/device/OS authority without another password or SSH session, and a CLI client reaches Remote over Tailscale directly. Manual is the default; Automatic is explicit opt-in. Standalone desktop and cross-platform CLI installations check available releases read-only via **Settings → Updates** or `fased-app updates check`, and upgrade in place via `install-cli.sh --upgrade` / `install.ps1 -Upgrade`.

Updates preserve data and wallet permissions; they never unlock signing or grant spending authority. [Details](https://docs.fased.ai/guide/install).

## Get started

1. Open the address printed by the wizard and complete owner setup, or sign in to your existing instance.
2. Open **Providers** and choose an available connection method.
3. Create an **Agent**, select its account and model, then open **Chat**.
4. Review a **Task**, inspect its result and save useful context to **Memory**.

Provider access is optional for reminders and other deterministic work. Enter credentials only in the dedicated setup screens, never in Chat. Trading execution, signing keys, and WEN mining remain disabled.

## Guides

[Agents](https://docs.fased.ai/guide/agents) · [Chat](https://docs.fased.ai/guide/chat) · [Tasks](https://docs.fased.ai/guide/tasks) · [News](https://docs.fased.ai/guide/news) · [Memory](https://docs.fased.ai/guide/memory) · [Wallets](https://docs.fased.ai/guide/wallets) · [Settings & recovery](https://docs.fased.ai/guide/settings)

Original Fased App code is licensed under [PolyForm Perimeter 1.0.0](LICENSE). Third-party terms remain in [NOTICE](NOTICE).
