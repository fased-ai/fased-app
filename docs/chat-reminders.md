# Chat drafts to reviewed reminders

A completed manual chat answer can contain a **reminder-draft** card. Keep and
Dismiss retain their existing meaning: neither creates a task. Mining is deferred.
There are no real AI calls, wallets or public updates in this flow.

Choose **Review reminder task** on an available reminder card. Edit the name,
reminder text, interval (60–604800 seconds) and maximum runs (1–20), then choose
**Review reminder schedule**. Inspect the exact values and source, confirm the
checkbox, and choose **Create reviewed reminder**. Cancel exits without creation.
A changed workspace, edited input or ten-minute expiry requires another review.

The result is a plain local reminder. Its first run is one interval after creation
while the core runs; missed intervals coalesce. It writes to the existing inbox and
run history. Tasks provides pause/resume and Run now, shared with CLI. No AI,
news fetch, bot, wallet operation or permission is attached. A source card can create
only one reminder, including after restart. Manage that task through Tasks.

Source cards currently come from the explicitly consented development fixture.
Once saved, their reminders can be reviewed/created/run in an ordinary core with
AI disabled. Other profiles, scheduled-AI cards, dismissed cards and strategy cards
cannot be converted. Creating a reminder records the source card as kept; the
source answer and conversation stay unchanged. Existing task limits/capacity and
update pause apply. No new state format or service is introduced.

## Shared CLI flow

Use `fased-app agents --profile trading-analyst` to inspect a completed manual chat
request and its reminder card. Submit strict JSON via
`fased-app agents --request-stdin < reminder-command.json`.

```json
{
  "revision": 5,
  "profileId": "trading-analyst",
  "action": "ai-reminder-review",
  "id": "SOURCE_CARD_ID",
  "name": "Review saved research",
  "text": "Read my saved headlines and notes",
  "intervalSeconds": 3600,
  "maxRuns": 2
}
```

This returns `reminderReview` without changing the revision or saving a task.
Review its source, values, `digest` and `expiresAt`. To create, send exactly the
reviewed inputs at that same revision, change the action to `ai-reminder-create`,
and include `consentDigest` and `expiresAt` from the review. Do not calculate or
invent a digest. A stale, changed, expired or duplicate submission refuses without
partial creation; inspect and review again after a conflict.

The new task ID is returned in `tasks`. Existing `task-pause`, `task-resume` and
`task-run` commands use that ID and the latest revision. These are ordinary reminder
controls, not renewed AI consent. No live provider or financial authority is enabled.

## Reuse a saved plain reminder

In Agents → Tasks, choose **Reuse** on a plain reminder. Inspect **Saved reminder
source**, edit the name, text, interval and maximum evaluations, then choose
**Review new reminder**. Only **Create reviewed reminder** creates a new task.
Discard leaves all saved records unchanged. The new reminder starts with zero
runs and its first evaluation one interval after creation; the original task,
counters, schedule and journal remain intact. Completed reminders can be reused.
AI, news, WEN and bot-backed tasks cannot be reused through this flow.

The CLI reads the same transient source and settings:

```sh
fased-app agents --profile trading-analyst --reminder-draft SAVED_TASK_ID
```

Use its `revision` and `source.id` in a strict command through the existing
`agents --request-stdin` path. Send `task-reminder-review` with the edited `name`,
`text`, `intervalSeconds` and `maxRuns`, then inspect `taskReminderReview`.
To create, send exactly those inputs with action `task-reminder-create`, its
`consentDigest` and `expiresAt`. Preparation and review do not write records or
reserve a run. A changed workspace, changed input or ten-minute expiry requires
another review. A repeated confirmation cannot duplicate creation; another copy
requires a fresh explicit review. Restart grants no execution or renewed consent.
Existing per-task and aggregate limits and update pause still apply. No AI,
credential, wallet, new storage format or service is involved.

## Create a task directly

Tasks → **New task** uses the same read-only review and atomic creation in UI
and CLI. Through `fased-app agents --request-stdin`, submit `task-review` with
`revision`, `profileId`, `name`, `text`, `intervalSeconds` and `maxRuns`. Optional
`botId` and `newsSourceId`/`newsMaxAgeSeconds` select existing same-profile inputs.
Inspect `taskReview`, then submit exactly those inputs as `task-reviewed-create`
with its `consentDigest` and `expiresAt`. Changed settings, profile, revision or
expiry refuse without writing. Plain reminders accept no AI, wallet, tool or
mining grant. The separate **Analysis** choice below uses the same commands.
The older explicit `task-create` CLI command remains compatible.

A single run starts after the chosen delay; repeated runs use the same interval.
Run now consumes one of the reviewed runs. Pause stops future runs; resume starts
a new interval. Missed intervals coalesce into one run, with no catch-up burst.
AI tasks in held/review/cancelled states need their existing fresh AI review;
resume never substitutes for that consent. Failed news requires correction of the
selected source before resume. No cached-news fallback is added.

For one reminder at a specific date, choose **At a date & time**. The UI offers
your browser's timezone and UTC. Nonexistent or repeated daylight-saving wall
times require correction or an explicit UTC time. Review displays the named
zone and UTC offset; the stored instant never depends on the server's timezone.
Dates must be 1 minute–366 days ahead at review/creation. A plain reminder
uses one run; analysis and news watches have their own reviewed input and
permission boundaries.

The same CLI `task-review` command uses `maxRuns: 1`, no interval, and:

```json
{
  "calendar": {
    "version": "once-at-v1",
    "at": 1793514600,
    "timeZone": "America/Chicago"
  }
}
```

`at` is an absolute Unix timestamp in seconds; choose a future instant for the
actual request. Create with the exact reviewed digest/expiry as above. Direct
`task-create` does not bypass calendar review. Pause retains the original date;
resume or restart runs an overdue scheduled reminder once. **Run now** completes
it early and prevents a second run on that date. Completed reminders do not
restart. Choose **New task** for another date. Recurring tasks can use the
existing reviewed cron calendar in the same task engine.

Calendar fields were introduced in schema 17; the current schema floor is 29.
Format-only migrations preserve original documents and revisions. Older
binaries refuse newer state. Recovery to an older reader requires its matching
pre-migration backup; never clear the schema marker or point it at newer state.

This fixture workspace retains history and has finite capacity: 100 tasks,
200 allocated evaluations, 200 results, 200 notices and 512 KiB including other
agent records and reserved future output. Limits are workspace-wide and are not
reset by marking notifications read or completing a task. No automatic pruning,
archive or unbounded unattended-operation claim is made. Inspect and preserve
records on a capacity error; do not delete the instance or raise limits.

## Analysis tasks

In Tasks → **New task**, choose **Analysis**, describe the job and set its timing
and finite limits. The attached agent supplies its selected account and model;
an explicit override remains possible. Review shows the exact choices and
prompt. **Create** saves the reviewed job without making a model request or
granting background credential access. Review alone saves no task or conversation.
No reminder or paper action is required for an analysis result.

Before a required AI run, use the existing **Review access** flow to grant finite
background credential access for the selected task/account/model. Connecting a
provider or unlocking it for Chat does not grant this access. Execution rechecks
the reviewed context, account/model, consent, limits and credential permission.
This implementation is verified with fixtures only; live use remains disabled.

The private CLI uses the same transient review and atomic creation:

```json
{
  "action": "task-review",
  "profileId": "PROFILE",
  "revision": 12,
  "name": "Research summary",
  "text": "Summarize the selected evidence without proposing actions",
  "analysis": { "version": "task-analysis-v1", "failure": "required" },
  "useAgentDefaults": true,
  "memorySelection": { "mode": "relevant" },
  "intervalSeconds": 300,
  "maxRuns": 2,
  "authorizationSeconds": 3600,
  "totalBudgetMicrousd": 60000,
  "limits": {
    "maxInputTokens": 24000,
    "maxOutputTokens": 1024,
    "deadlineSeconds": 1,
    "budgetMicrousd": 30000
  }
}
```

Replace the revision/profile with the current instance values. The agent must
already have an admitted account/model. Inspect `taskReview.ai`, then send
exactly the same inputs as `task-reviewed-create`, adding `consentDigest` and
`expiresAt` from that review. These illustrative fixture limits are not a live
API test or spending authorization. At most ten runs are allowed; a calendar
object can replace the interval, with its first run inside the AI permission.
An existing same-profile conversation can be selected through `sessionId`.
Otherwise Create retains a private context for the job. Relevant memories are
bound by review; changed context requires fresh consent rather than silently
changing the saved task.

Run controls and results are shared with the UI. Failed required analysis holds
dependent work. A completed result retains its answer and request/source
identity; **Discuss** copies that saved result into Chat without another AI
request, and **Remember this** requires an explicit memory action. Completion
does not silently add memories. Pause/restart preserve runs and unknown-usage
holds without replay. For a watch with optional analysis and deterministic
continuation, see [News watch](news-inputs.md#optional-analysis).
