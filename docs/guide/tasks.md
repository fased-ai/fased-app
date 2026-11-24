---
title: "Tasks"
description: "Schedule work, review results and stay in control."
---

Open **Tasks (`#agents/tasks`)** to manage cron schedules and **Event Triggers** (`price`, `indicator`, `fasedscript`, `news`, `prediction`). Deterministic checks run without an AI provider; AI-assisted agent prompts use the agent/model you select and mirror results into persistent Agent V2 threads.

Triggers can notify you through **Telegram** (with optional inline Approve/Reject controls for human-in-the-loop proposals), HMAC-signed webhooks, desktop/web alerts, or bounded `shadow`/`paper` cycles on the **8-Seat Trading Desk** (`desk_cycle`). Always dry-run triggers via **Test** before enabling live notifications.

**Pause** or the global **Kill Switch** stops trigger evaluation and cancels pending Desk tickets cleanly without erasing historical run logs.

Paper Desk cycles and triggered runs are strictly non-financial simulations (`shadow`/`paper` only) and never place real-money orders. See [Strategies](/guide/trading) and [Availability](/reference/status).
