# Fased App product guide

Fased is the self-hosted agent, research, charting, intelligence, and paper-strategy operating system in the Fcode Lab ecosystem. A shared design language never pools keys or grants spending authority.

**Availability:** Attested Ubuntu 24.04 x86-64 Local/Remote installers, cross-platform CLI & unsigned desktop pre-release builds, live AI providers (OAuth, API key, local server, Host CLI/ACP), watch-only Solana RPC, and Stages 1–3 research/charting/intelligence/automation/QuantLab/Paper-Desk capabilities are shipped (`v0.1.41+`). Live wallet signing, real-money trading execution, and auto-updates remain strictly disabled. **WEN `SAT` mining execution is marked `NOT READY / DO NOT IMPLEMENT NOW` (`miningActivationEnabled: false`).** See the [User guide](https://docs.fased.ai) and [Availability](https://docs.fased.ai/reference/status).

The active shell navigation covers **Overview, Trade, Intel (News), Lab (FasedScript & Paper Desk), Chat, Tasks, Agents, and Wallets**, with a unified bottom-left **Settings** modal (`General`, `Providers`, `Instance`, `Notifications`, `Updates`, `Diagnostics`).

- **Trade:** Synchronized `1×1`–`4×4` multi-asset charts, 9 candle styles, Orderflow (`Footprint`, `DOM`, `Tape`, `VP`, `CVD`, `Intel`), FasedScript overlays, and the Paper/Shadow Desk dock.
- **Intel (News):** Clean-room WorldMap, multi-source feeds, Polymarket↔Kalshi prediction odds/arbitrage, and chart event pins.
- **Lab & Desk:** Sandboxed `FasedScript` DSL, deterministic 4-Gate validation, and the 8-seat Paper/Shadow Trading Desk.
- **Agents & Tasks:** Conversational Agent V2 with `/api/v1/mcp`, External MCP Client, cron schedules, event-driven triggers, and fail-closed Telegram alerts/approvals.
- **Wallets:** Watch-only Solana accounts, live read-only RPC, and portable backups; signing stays OFF until authorized.

Read [Satcoin Mining](satcoin-mining.md) for the deferred WEN `NOT READY` hold and [shared design language](design-language.md) for Fased's Obsidian/Light design tokens. Each live execution venue requires explicit pre-live hardening and owner authorization; a UI control never activates live trading or signing.
