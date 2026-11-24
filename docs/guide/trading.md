---
title: "Strategies and trading"
description: "Define rules and limits before authorizing an action."
---

A strategy defines its market, entry/exit logic, timing, and deterministic risk limits in **Lab (`#lab`)** using the sandboxed **FasedScript** DSL, and can be overlaid directly on **`#trade` charts** or attached to **Event Triggers**.

The **#trade** workspace provides synchronized `1×1`–`4×4` Lightweight Charts v5 across all 9 candle styles (`Standard`, `Heikin-Ashi`, `Renko`, `Range`, `Kagi`, `Point & Figure`, `Three Line Break`, `Tick`, `Volume`), Orderflow (`Footprint`, `DOM`, `Tape`, `Volume Profile`, `CVD`, and `Cohorts/FRED/SEC Intel`), symbol-scoped drawings, FasedScript overlays, and the **Paper / Shadow Desk** dock.

In **#lab**, strategies run against deterministic **4-Gate Statistical Validation** (plateau sweep, 1,000× bar permutation, 3-fold walk-forward + holdout, and 200× walk-forward permutation) and can execute bounded cycles on the **8-Seat Trading Desk** (`Chief`, `Scout`, `Chartist`, `Quant`, `Skeptic`, `Risk`, `Executor`, `Journal`). Desk execution is locked to `shadow`/`paper` mode only and never places real-money trades.

**Live wallet signing, Onchain (Jupiter/Solana) swaps, and Hyperliquid live perps remain strictly disabled** in this build. Connecting a provider or watch-only wallet never authorizes a live order.

[Fased Network](/guide/network) is planned to share attributed signals across instances. [WEN SAT Mining](/wen/automation) is **NOT READY / DO NOT IMPLEMENT NOW**. See [Availability](/reference/status).
