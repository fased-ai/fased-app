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
