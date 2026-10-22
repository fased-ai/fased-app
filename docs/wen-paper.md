# WEN paper journey

Open Terminals → Satcoin Mining in an unlocked private core. Choose a profile,
name, BTC range, paper SOL budget (0.01–1, at most nine decimal places), and
review validity (5–30 minutes). Prepare and review the exact plan, then approve
paper only. The existing task engine runs it once after 60 seconds while the
core runs, including with the browser closed. Run paper once evaluates sooner.
Pause, resume before expiry, and cancel are shared UI/CLI operations. Missed
intervals coalesce. Expired/changed plans hold without retry; prepare a fresh
entry. Cancelled, held and completed entries cannot resume. Restart preserves
reviews, cancellation, journal and inbox; concurrent ticks never duplicate runs.

This fixed `wen-pm1-paper-v1` template uses the public canonical WEN PM1 goldens
at `55a4dc562c2516a48f500047df85cb81fabf8084`. It builds and validates unsigned
open/commit/reveal templates with the Go client, then round-trips a synthetic
pending account and receipt. Public deterministic salts/nonces are fixture data,
not secure live commitments. There is no wallet, endpoint, AI dependency, BTC
price, live commitment deadline or settlement simulator. Review expiry is local
core time, not a WEN round cutoff. No funds are reserved or spent. Instruction
metadata mentioning a signer never supplies signing authority.

The receipt is explicitly unsettled and unsigned: no outcome, principal payout,
SAT/miner reward, P&L or claimability is calculated. Actual returns depend on the
canonical round, observations and crowding; this test predicts none. Golden
boundary-vector rewards/balances are never presented as a user's results.
Expandable evidence retains canonical revision/fixture hash, frozen plan digest,
request/template hashes, account/receipt hashes and exact integer amounts.
These hashes establish local reproducibility, not chain inclusion or payment.

CLI uses the same owner-authenticated, revision-checked endpoint:

```sh
fased-app agents --profile wen-operator
```

Read the current revision, then pass one JSON document via stdin (substitute the
returned revision, task ID and digest each time):

```json
{
  "revision": 0,
  "profileId": "wen-operator",
  "action": "wen-paper-prepare",
  "name": "Range C rehearsal",
  "wen": { "budgetSOL": "0.25", "selection": "2", "validForSeconds": 900 }
}
```

```sh
fased-app agents --request-stdin < paper-command.json
```

```json
{
  "revision": 1,
  "profileId": "wen-operator",
  "action": "wen-paper-approve",
  "id": "RETURNED_TASK_ID",
  "consentDigest": "RETURNED_PLAN_DIGEST"
}
```

Next submit `task-run`, `task-pause`, `task-resume` or `wen-paper-cancel` with
that task ID and the latest revision. Each response includes the same scoped
plans, runs and notices displayed by the UI. Range selection 0–4 corresponds to
A below −0.20%, B [−0.20%, −0.05%), C [−0.05%, +0.05%), D [+0.05%, +0.20%), and
E at least +0.20%. One run per review, at most 20 retained WEN paper entries,
within the existing workspace/journal capacity. Schema 10 and feature
`agents-wen-paper-v1` protect these records from older readers and include them
in update snapshots/backups. Paused schema-9 state must finish recovery before
migration. Wallet/owner mandates remain entirely separate and disabled for live
use; paper approval never authorizes finance. No automatic/public update or
real AI/wallet operation is enabled.

## Explain a saved paper entry

Select **Explain this paper entry** on a Satcoin paper card. The core creates a
new conversation in the same profile with a bounded, read-only snapshot of the
retained plan and optional journal/receipt. It captures the task state, budget,
range, review expiry, source/fixture and review/receipt digests, timestamp and
workspace revision, plus a SHA-256 of the snapshot text. The snapshot is plain
saved conversation data, not a wallet mandate or an executable instruction.

Captured facts stay frozen after task, profile or provider-preference changes and
restart. They describe historical fixture state, not current status, a live BTC
observation, payout, P&L or claim. Expired, cancelled and held entries may still be
explained when their retained plan/journal evidence is consistent. A changed plan,
missing/inconsistent receipt, stale revision, foreign profile or full conversation
storage refuses capture without changing the paper task or partially saving chat.

CLI uses the existing command endpoint; supply the latest revision and same-profile
task ID, then read the returned new session and its context message:

```json
{
  "revision": 2,
  "profileId": "wen-operator",
  "action": "wen-paper-explain",
  "id": "RETURNED_TASK_ID"
}
```

No AI runs when context is captured. Read the facts directly, or use the normal
optional AI request form with explicit disclosure, limits and consent. Ordinary
cores still refuse execution; only the standalone compiled development fixture
can explain. Cancellation and budget/context checks remain unchanged. Neither
capture nor an AI answer approves, resumes, executes or cancels the paper task.
No live provider, wallet, RPC, public update or new service is enabled.
