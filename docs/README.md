# Fased App product guide

Fased is the self-hosted agent, research, charting, intelligence, and paper-strategy operating system in the Fcode Lab ecosystem. A shared design language never pools keys or grants spending authority.

**Availability:** Attested dual-arch Linux (`amd64` + `arm64`) Local/Remote installers, cross-platform CLI & unsigned desktop pre-release builds, live AI providers (OAuth, API key, local server, Host CLI/ACP), zero-by-default pluggable `Data & Feeds` (`Exchanges`, `News`, `Predictions`, `RPCs`), isolated two-process Solana custody (`fased-app signer-daemon` with `SO_PEERCRED` + `systemd-creds` / OS Keychain KEK + fail-closed policies + step-up owner confirmation + Wallet Standard hardware signing), and Stages 1–6 research/charting/intelligence/automation/QuantLab/Paper-Desk/Hub/Swarm capabilities are shipped (`v0.2.8+`). Default live financial execution and auto-updates remain strictly disabled (`--execution-profile=off`, `updater: false`). **WEN `SAT` mining execution is marked `NOT READY / DO NOT IMPLEMENT NOW` (`miningActivationEnabled: false`).** See the [User guide](https://docs.fased.ai) and [Availability](https://docs.fased.ai/reference/status).

The active shell navigation covers **Overview, Trade, News & Intel, Lab (FasedScript & Paper Desk), Chat, Tasks, Agents, Network, and Wallets**, with a unified bottom-left **Settings** modal (`General`, `Providers`, `Data & Feeds`, `Instance`, `Notifications`, `Updates`, `Diagnostics`).

- **Trade:** Draggable widget terminal with synchronized `1×1`–`4×4` multi-asset charts, 9 candle styles + Footprint (Orderflow) chart mode, separated Order Execution ticket, DOM/Tape, Positions/Orders, and persistent Bots & AI Trader routing.
- **News & Intel:** Unified draggable intelligence workspace combining the clean-room WorldMap, multi-source feeds, Polymarket↔Kalshi prediction odds/arbitrage, and chart event pins (all sources opt-in from `Settings -> Data & Feeds`).
- **Lab & Desk:** Sandboxed `FasedScript` DSL, deterministic 4-Gate validation, and the 8-seat Paper/Shadow Trading Desk.
- **Agents & Tasks:** Conversational Agent V2 with `/api/v1/mcp`, External MCP Client, cron schedules, event-driven triggers, and fail-closed Telegram alerts/approvals.
- **Wallets:** Isolated out-of-process Solana signer daemon (`signer-daemon` over Unix socket with `SO_PEERCRED`, KEK sealing, genesis-pinned per-wallet RPC + witness cross-check, fail-closed policy/caps, pre-sign simulation gate, hash-chained audit log, owner step-up confirmation, Wallet Standard hardware signing, and Argon2id encrypted recovery backups; zero plaintext keys over HTTP).

Read [Satcoin Mining](satcoin-mining.md) for the deferred WEN `NOT READY` hold and [shared design language](design-language.md) for Fased's Obsidian/Light design tokens. Each live execution venue requires explicit pre-live hardening and owner authorization; a UI control never activates live trading or signing.
