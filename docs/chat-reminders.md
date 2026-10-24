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
