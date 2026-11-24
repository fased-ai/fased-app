---
title: "Availability"
description: "What you can use and what is planned."
---

Fased includes certified Local and mandatory-Tailscale Remote setup on Ubuntu 24.04 x86-64, cross-platform CLI & unsigned desktop pre-release packages (Linux, macOS, Windows), live AI providers with native OS-protected credentials, Agent V2 with MCP, operational diagnostics/backups, and watch-only Solana wallets with live read-only RPC. Trading execution, signing keys, and WEN mining remain strictly disabled.

| Area                                 | Availability                                                                                                                                                                                                                       |
| ------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Ubuntu 24.04 Local/Remote install    | Certified & available for Ubuntu 24.04 x86-64 (attested `install.sh`, Local loopback and mandatory-Tailscale Remote with dual-stack nftables lockdown and shared UI/CLI updater).                                                  |
| Cross-platform CLI & Desktop         | Available as unsigned pre-release packages (`install-cli.sh` for Linux/macOS, `install.ps1` for Windows, and native `.dmg`, `.exe`, `.deb`, `.AppImage` builds with `--upgrade`/`-Upgrade`); Apple/Windows signing pending.        |
| Agents, Chat, Tasks, News and Memory | Available (Agent V2 persistent `agents.sqlite` threads, FTS5 memory & rules, scheduled task mirroring, `/compact`, `/side`, voice input, and local or owner-authenticated Remote `/api/v1/mcp`).                                   |
| Providers and models                 | Available with OAuth sign-in, API key, local server, and Host CLI/ACP adapters (`codex`, `claude`, `antigravity`, `gemini`, `grok`) backed by native OS credentials (Linux `systemd-creds`/Secret Service, macOS Keychain, DPAPI). |
| Diagnostics and backups              | Available (`fased-app doctor`, hot SQLite `fased-app backup` / `restore`, and portable watch-only wallet, RPC and venue backup/restore).                                                                                           |
| Wallets and RPC                      | Watch-only Solana accounts and live read-only Solana RPC (keyless public Solana RPC or OS-protected Helius/Alchemy/custom RPC key) available; real signing keys, funds custody and transaction signing remain strictly disabled.   |
| Strategies                           | Paper evaluation only; does not place live orders.                                                                                                                                                                                 |
| Onchain and Hyperliquid              | Live trading is not available.                                                                                                                                                                                                     |
| Mining                               | Controlled WEN readiness only; live mining activation is strictly disabled.                                                                                                                                                        |
| Fased Network                        | Planned.                                                                                                                                                                                                                           |
| Updates                              | Shared UI (**Settings → Updates**) and CLI (`fased update`) on managed Linux (Manual default, Automatic opt-in); read-only release checks and opt-in `--upgrade`/`-Upgrade` on standalone desktop/CLI builds.                      |

Do not send funds to development addresses or enter wallet seed phrases or recovery keys. Use only the platform instructions and availability stated for your release.

Read [Install](/guide/install), [Wallets](/guide/wallets) and the [Network guide](/guide/network).
