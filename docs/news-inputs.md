# Bounded news inputs

Agents → News manages profile-scoped public HTTPS RSS 2.0 sources. There is no
account, API key, article crawler or extra service. Nothing is fetched until the
owner requests a fetch or schedules a news-dependent task. Normal operation can
read an explicitly selected public feed; standalone `serve --ai-fixture` uses
only the compiled `https://news.fased.invalid/headlines.xml` fixture instead.
Tests use controlled HTTPS endpoints with disposable trust roots, never live news.
Real AI calls, wallets and automatic/public updates remain disabled.

Add a source with a name, URL and freshness limit of 60–86400 seconds. URLs must
use public DNS and HTTPS without credentials, query parameters or fragments.
The core refuses private/reserved IP answers, redirects, environment proxies,
cookies and authorization headers. It validates all DNS answers and dials a
checked address directly, retaining normal TLS hostname/certificate verification.
Each fetch has a three-second deadline, an 8 KiB header limit and a 64 KiB body
limit. Only uncompressed RSS/XML is accepted. Atom and authenticated feeds are
outside this first adapter.

The parser admits at most 32 feed entries and retains at most five fresh
headlines, with bounded titles and public HTTPS article links. It requires a
publication time, rejects future/ambiguous times and XML directives/entities,
and discards stale entries. It never follows article links. Headlines are plain
untrusted data; a headline cannot authorize a tool, order or permission change.

Each attempt retains its source/fetch identity, requested URL, selected freshness
limit, state and safe failure reason. A bounded received XML body also records
retrieval time, byte count and SHA-256, including parser failures. Successful
snapshots add stable item identities, headline/link/publication times and an
evidence digest. Publication times are publisher claims; hashes record what this
instance observed, not truth, authorship or market accuracy. Fresh status is
recomputed by the core using its current clock. Source records, attempts and
dependent journal references survive restart and metadata snapshots; disabling
a source preserves its history.

In Tasks, select the source and a freshness limit within its configured maximum.
Each evaluation durably queues a new fetch before its reminder, fixed-input
paper step or optional fixture AI step. Both publication age and retrieval age
are checked before use; AI dispatch and completion recheck the exact evidence.
AI task consent binds the source and freshness policy. Each actual AI request
discloses/binds its exact snapshot as untrusted data. Manual AI review uses the
latest fresh snapshot and requires its ordinary exact-disclosure consent.

Unavailable, stale, malformed, interrupted or cancelled input holds only its
dependent task. The held evaluation consumes one of the task's bounded runs;
independent tasks and recovery continue. There is no automatic retry or cached
fallback. Interrupted dispatch becomes unknown after restart or pause recovery,
and is never replayed. Review the source and explicitly resume a non-AI task
with evaluations/capacity remaining, or prepare a new reviewed AI plan. Once its
evaluation limit is consumed, create a new bounded task. Verified AI usage stays
accounted for if news expires during a call; uncertain usage retains its hold.

This prototype retains at most ten sources and twenty fetch attempts across all
profiles, including reserved future attempts. Storage reserves also bound total
workspace size, so large plans can be refused earlier. Resuming a held task must
reserve capacity again. There is no silent deletion of provenance to make room.

The same owner-authenticated, revision-checked commands are available through
`fased-app agents --data-dir /private/instance --request-stdin`. Send one JSON
object with the current `revision` and `profileId`:

- `news-source-add`: `name`, `source` (URL), `newsMaxAgeSeconds`.
- `news-source-disable`: `id` of the source.
- `news-fetch`: `id` of the source.
- `news-cancel`: `id` of the queued/fetching attempt.
- `task-create`, `task-ai-prepare` or `ai-prepare`: optional `newsSourceId` and
  `newsMaxAgeSeconds`. Omitting both preserves the no-news path.

`fased-app agents --profile PROFILE` returns the same scoped source, fetch,
task and journal records used by the UI. Use the existing task controls for
pause/resume; the CLI grants no additional financial or model authority.

## News watch

In Tasks, choose **Watch news**, a source and **Contains**. The rule matches a
literal phrase in headline titles, without case sensitivity. Choose an interval,
a date or a recurring calendar, and a maximum of 1–20 runs. Review the source,
rule, timing and limit, then create the task. Review alone saves nothing.

Each run fetches a fresh bounded snapshot. The first successful run reports all
matching items as new. Later runs notify the local Inbox only for matching item
identities the task has not seen. Identity includes source, title, link and
publication time; a changed article is a distinct item. A successful check with
no new matches still saves a result, without another notification. The task's
cursor survives pause, resume and restart. Source failure, stale data or an
interrupted fetch holds only dependent work; resume is explicit and consumes the
remaining run allowance. With **Analyze** off, there is no cached fallback,
retry or AI interpretation.
No provider, API key, model or background AI access is required, including when
the attached agent has a locked provider. Headlines remain untrusted data.

Open a result to see its new headlines and retained source details. **Discuss**
copies the saved result to Chat without rerunning it or fetching news. **Remember
this** uses the existing reviewed memory flow; it never saves a note silently.

The CLI uses the same review and execution commands. After `agents --profile
PROFILE` returns the current revision and selected source ID, send this JSON to
`fased-app agents --data-dir /private/instance --request-stdin`:

```json
{
  "action": "task-review",
  "profileId": "PROFILE",
  "revision": 12,
  "name": "Market headlines",
  "newsSourceId": "SOURCE_ID",
  "newsMaxAgeSeconds": 300,
  "watch": { "version": "news-watch-v1", "contains": "bitcoin" },
  "intervalSeconds": 300,
  "maxRuns": 3
}
```

Replace the example revision and identities with your instance's values. Inspect
`taskReview`, then submit the same inputs as `task-reviewed-create`, adding its
`revision`, `expiresAt` and `digest` as `consentDigest`. Calendar rules use the
existing `calendar` object instead of an interval. Direct `task-create` refuses
watch rules. Use `task-run`, `task-pause` and `task-resume` with the saved task ID;
`task-run-explain` takes a run ID. Every command retains normal authentication,
profile scope, revision and capacity checks. Schema 29, `agents-news-watch-v1`
and `agents-task-analysis-v1` protect typed rules, analysis policies, run results
and notification cursors; schema-28 readers refuse newer state. Migration changes only the schema floor,
preserving original documents, revisions and encrypted credentials. Retained
source/run capacity remains finite; a watch is not an unlimited background feed.

### Optional analysis

In the same watch editor, enable **Analyze**, enter a prompt and choose **If
analysis fails**: **Keep news result** or **Hold task**. Review binds the source,
rule, prompt, selected account/model, context and finite run/token/time/budget
limits. Analysis watches allow at most ten runs. Creating a task does not grant
background credential access; use the existing task access review separately.

Each run fetches fresh news first. No new matching items means no model request.
**Hold task** requires successful, still-authorized analysis before committing
the watch result and cursor. **Keep news result** retains the deterministic
watch result if AI access is unavailable or analysis fails, with analysis marked
skipped. It never substitutes another model, fabricates an answer or removes
unknown-usage holds. Source failure still holds dependent work. Explicit pause
or cancellation stops the job rather than continuing its optional steps.

The CLI adds `analysis: {"version":"task-analysis-v1","failure":"optional"}`
or `failure: "required"` to the same review/create commands, with `text` for the
prompt and the bounded AI fields documented in [Analysis tasks](chat-reminders.md#analysis-tasks).
Opening or reviewing a task makes no model request. A paused optional watch can
resume its deterministic work after AI access expires; dispatch still rechecks
AI consent and credentials before any model request. Held, cancelled and review
states retain the fresh-review requirement for AI tasks.

Schema 8 and `agents-news-v1` prevent older readers from dropping news evidence
and dependencies. Paused schema-7 state must finish update recovery before
migration. Existing records remain intact. The revised scheduled-AI consent
digest requires fresh review for older plans; it never silently reauthorizes
them. Existing manual requests without news keep their original digest format.
