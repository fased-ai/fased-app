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

Schema 8 and `agents-news-v1` prevent older readers from dropping news evidence
and dependencies. Paused schema-7 state must finish update recovery before
migration. Existing records remain intact. The revised scheduled-AI consent
digest requires fresh review for older plans; it never silently reauthorizes
them. Existing manual requests without news keep their original digest format.
