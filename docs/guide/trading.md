---
title: "Strategies and trading"
description: "Versioned rules, reviewed Tasks and independent venues."
---

A strategy owns financial rules: market, wallet/network binding, quantities, allocation, risk limits and timing. The agent can help research or propose a change; execution authority stays with the reviewed strategy and signer.

The B06.1 local package adds immutable paper SOL/USDC limit-buy versions to **Tasks → Strategies**. Save a version, review a finite Task and inspect its bounded paper result. Tasks of one version share its allocation. Editing preserves history and holds older Tasks until fresh review. Pause/Resume and results survive restart.

This package uses controlled quotes and is not yet delivered to main. It does not establish current market prices, liquidity, fees, profitability or live fills.

**Onchain** and **Hyperliquid** are independent future execution integrations. Each needs exact venue intent, bounded quotes/simulation, owner authority, signing, submit/cancel, reconciliation and recovery before use. A rendered page or provider connection is not execution readiness.

The future [Network](/guide/network) supplies signals to this same strategy/Task path. Mining remains [deferred](/wen/automation).
