# Fased App

A focused financial automation workspace. **P2 engineering foundation**: Go serves the production React interface, owns setup in SQLite, authenticates the instance owner, and exposes the same setup to the CLI. A thin native client, ordinary-user platform installers, private-access helpers and unsigned engineering packages are implemented. Ubuntu runtime and sandbox preparation use explicit OS authorization in the terminal wizard or `--prepare-host`. Public trust/signing and supported-host acceptance remain unfinished. Wallet signing, financial execution, AI connections and updates are later phases.

The interface includes shared navigation, resumable setup, wallet-mode and permission previews, persistent local agent profiles/conversations, scoped memory with corrections/expiry, bounded tasks, a durable notification inbox, paper examples, and four labelled terminal mockups. WEN Asset Mining and Loss Mining remain prototypes while their independent contracts are developed. Provider/wallet selections save preferences only; they create no account or authority. P5 now includes test-only OpenAI connection management and a passphrase-encrypted API credential vault; see [AI connections](docs/ai-connections.md). Pinned OpenAI model/token/tariff/access admission is also implemented and exercised only with controlled fixtures. Live provider calls and real credentials remain disabled.

See [installation, private access and recovery](docs/install.md) for engineering package use and platform boundaries.

P5 [bounded news inputs](docs/news-inputs.md) add owner-selected public HTTPS RSS,
retained provenance and freshness checks to the same profiles and tasks.
Unavailable/stale input holds dependent work without cached fallback or stopping
independent tasks. All acceptance uses controlled HTTPS fixtures.

P3's separate Linux [disposable signer](docs/signer.md) adds installer-managed identities, protected owner backup/recovery, independent bounded fixture grants, reservations and a durable mock-operation journal. It cannot sign real transactions, accepts no real wallet import, and is excluded from ordinary installation. The connected Wallets UI and `accounts` CLI share revision-checked disposable public account records and permission proposals. Protected owner terminal approval remains separate; balances, on-chain reads and real execution stay unavailable.

P4 starts with [signed development manifest and compatibility inspection](docs/updates.md). It checks explicit test trust, complete local binary hashes, protocol/features and state formats, including approval journals. The read-only plan never authorizes installation, resume or real wallets; automatic updates remain disabled.

Managed disposable fixtures also expose authenticated service snapshots and protected owner update intents. The signer owns exclusion and a durable reserved-sequence floor; abort/restart preserve financial history and holds. There is still no installation, migration or automatic resume transition.

## Develop

Requires Go 1.26 and Node.js 22.12+ for development. The compiled core needs neither Node nor a database server.

```sh
npm ci
npm run build
npm run build:core
```

For a disposable development instance:

```sh
./.evidence/bin/fased-app serve --data-dir .evidence/manual-core
```

Open the printed loopback URL. In another terminal, obtain the one-use owner code:

```sh
./.evidence/bin/fased-app claim-code --data-dir .evidence/manual-core
```

Enter it in the UI and create an owner password of at least 12 characters. The code expires after 15 minutes; obtaining a new code invalidates the previous one. `claim` offers masked owner claim in a terminal instead. Browser setup and the CLI below use the same versioned state:

```sh
./.evidence/bin/fased-app login --data-dir .evidence/manual-core
./.evidence/bin/fased-app setup --data-dir .evidence/manual-core --interactive
./.evidence/bin/fased-app status --data-dir .evidence/manual-core
./.evidence/bin/fased-app logout --data-dir .evidence/manual-core
```

The wizard reviews preferences before saving. Advanced clients can use `setup --host local --wallet-mode watch --provider none`. Passwords are masked, never command arguments. Explicit `login --password-stdin` is available for controlled automation. A second engine using the same state directory fails rather than starting another instance. Closing the browser leaves the foreground core running; Ctrl-C stops it and preserves its state.

To work on the fixture interface without the Go core:

```sh
npm run dev
```

Only this development server can fall back to fixture mode when the API is absent. The production interface fails closed. These are source development commands, not the future retail installer.

```sh
npm run check
npm run test:core
npm run build:core
npm run test:ui
npm run test:core-ui
```

Browser tests require Playwright's Chromium (`npx playwright install chromium`). Core browser tests launch disposable instances and exercise UI/CLI edits, restart, logout and API failure on desktop/mobile. Screenshots, results and traces go to ignored `.evidence/`. Cross-compilation checks are source evidence, not installed OS acceptance.

## Engineering installation on Linux

The built core can install itself as the current user's service. This is an offline development path; release acquisition and verified updates are not implemented yet. Run it as an ordinary user:

```sh
./.evidence/bin/fased-app install
```

It installs under `~/.local/share/fased-app`, keeps data separately in the OS configuration directory, starts the loopback-only service and prints its URL. Login autostart is off. Explicit `--autostart` enables it; `--start=false` only installs files. A different existing binary is refused rather than overwritten. Partial first-copy staging can resume, and unknown files are never adopted. No wallet, RPC, AI account or root permission is required.

```sh
~/.local/share/fased-app/fased-app open
~/.local/share/fased-app/fased-app claim-code
~/.local/share/fased-app/fased-app service status
~/.local/share/fased-app/fased-app service stop
~/.local/share/fased-app/fased-app service start
```

`open` starts the same installed engine and opens the native client if it was bundled, otherwise the default browser. `open --print` returns the verified loopback URL. Obtain the one-use claim code in a private terminal, then establish your owner password in the UI. Closing a window leaves the engine running. Reinstalling the identical input preserves settings and existing files; changed binaries must wait for the verified update flow.

Use the originally downloaded/built core to remove the application:

```sh
./.evidence/bin/fased-app uninstall
```

Uninstall first verifies the installed inventory and service identity, then stops/unregisters only its own service and removes its known application files. All instance data and unknown application files are preserved. `--prefix` and `--data-dir` are available for explicitly selected engineering instances; repeat them consistently across commands.

The user service uses the current OS identity, `NoNewPrivileges`, read-only system/home mounts, private temporary space and a writable exception for its private state. The service definition alone is not evidence of enforcement: the local acceptance runner tests actual write-open permissions through the process mount namespace. This is application containment, not the future isolated wallet-custody boundary.

On a VPS, a user service alone does not guarantee operation after logout or boot. The private-access/host setup must verify the service manager and the owner's linger or system-service policy. This installer changes no login/linger policy automatically. macOS uses a user LaunchAgent; Windows uses a limited interactive-user background task. These do not provide execution after OS logout.

## Native window source

The separate `desktop/` Go module pins Wails, keeping desktop dependencies out of the headless core. Build the shared UI, then:

```sh
npm run build:desktop
```

Linux needs GTK3/WebKitGTK 4.1 development packages; macOS needs Xcode; Windows execution needs WebView2. These are [Wails platform prerequisites](https://v2.wails.io/docs/gettingstarted/installation/). End-user runtime packaging is still a release gate. No global Wails CLI is required by this build. A matching Linux binary can be included with `install --desktop-binary /absolute/path/to/fased-desktop`.

The window serves the same production UI and forwards only allowlisted API methods over the protected local IPC transport. It inherits no CLI session, supplies no owner credentials, pins instance identity per window and keeps bearer/CSRF checks. Its native custom-scheme origin handling is separate from the browser listener. There are no generic application Go bindings, file controls or shell tools; file-drop navigation is disabled. Window close never stops the engine.

Linux CI uses a real WebKit window under Xvfb to exercise owner claim and private IPC setup against a separate core process, then verifies that core survives window close. The smoke executable and injected fixture script are development-only and are not installed. Native compilation and this controlled window journey do not establish signed/notarized installers or real Windows/macOS host acceptance.

## Private access and state

The HTTP listener accepts explicit loopback IPs only (default `127.0.0.1:7331`). CLI traffic uses a Unix socket or Windows named pipe protected for the instance's OS user, and verifies instance/API identity before sending credentials. Browser mutations require an allowed origin and authenticated CSRF token; foreign hosts/origins, unsupported fields and oversized bodies are rejected. Concurrent stale setup writes return 409 and require review.

Local HTTP uses an origin-scoped bearer in tab session storage, never local storage or an HTTP cookie shared between ports. An explicitly configured private HTTPS origin uses a Secure, HttpOnly, SameSite=Strict cookie. Sessions expire after 12 hours and logout revokes them. Passwords use Argon2id; claim/session tokens are stored hashed in SQLite. This is app access authentication, not the future wallet-administration ceremony.

For a VPS, retain the loopback listener and use private access. An owner-established SSH tunnel can forward it; a trusted local HTTPS proxy such as Tailscale Serve can forward it with `serve --public-origin https://<exact-private-host>`. No proxy is installed automatically yet. This option only allowlists that exact origin; it does not establish private networking, validate tailnet policy or make public exposure safe. The core changes no SSH, firewall or Tailscale settings.

State defaults to the OS user's configuration directory under `fased-app`; `--data-dir` selects another private directory. Unix directories must be owner-only (0700), files owner-only (0600); broad permissions and final-path symlinks are rejected. Windows uses a protected current-user/SYSTEM DACL and named pipe. SQLite owns durable ownership/settings/sessions; schema downgrade is refused. Endpoint and CLI-session files are atomically replaced. No password reset or financial backup/restore workflow is implemented yet. Preserve state; do not delete it as an upgrade method.

## Data and scope

In core mode, setup and the local agent workspace live in private SQLite. Profiles,
named conversations, messages, memory corrections/expiry, tasks, fixed-input paper
bots, their evaluation journal and inbox survive core restart. Provider/model
choices are preferences only. Chat saves local notes with an explicit system
acknowledgement; it never fabricates a model response. No provider credentials,
model calls, RPC requests, wallet access or financial orders are available.
Browser-only design mode retains its separate illustrative data. Reset prototype
preserves the core's setup and agent records.

Tasks run in the existing core with the UI closed. Reminders and the versioned
`fixed-paper-buy-v1` strategy are the only task kinds. Each task has a 60-second to
seven-day interval and 1–20 evaluations; missed intervals coalesce into one run.
Each paper evaluation records its actual fixed inputs and available fixture
budget. Budget failures spend nothing. Results, budgets, inbox and next schedule
commit in one transaction, so restart/retry cannot repeat a completed evaluation.
Pausing a task stops future evaluations; resuming starts a new interval. Update
pause fences all agent writes and scheduling. There is no background OS service
per profile or task.

The CLI uses the same authenticated API and profile scope as the UI:

```sh
fased-app agents --profile trading-analyst
fased-app agents --profile trading-analyst --search thesis
fased-app agents --request-stdin < local-command.json
```

A command includes the revision returned by the previous read. For example:

```json
{
  "revision": 0,
  "action": "session-create",
  "profileId": "trading-analyst",
  "name": "My thesis"
}
```

Stale commands fail with a conflict; they are never replayed automatically.
Supported actions: `profile-create`, `profile-update`, `session-create`,
`message-add`, `memory-put`, `memory-delete`, `memory-chat-save`, `bot-create`, `task-create`,
`task-pause`, `task-resume`, `task-run`, `notice-read`. Profile/session/memory/task
IDs are scoped by the server. Personas and stored text grant no tools or wallet
permissions. Every memory has evidence/source, timestamp, optional
expiry and up to eight previous revisions. Memory deletion leaves task/paper
journals intact.

### Find a memory and inspect its source

In **Agents → Memory**, search current note text or source within the selected profile. Matching is case-insensitive and bounded to 200 UTF-8 bytes; previous correction text does not match current-note search. Expired notes stay visible and labelled. **Inspect source** resolves a recorded chat message or completed manual answer and compares its retained text with the recorded SHA-256. **Open conversation** opens that existing conversation with the chat filter cleared. These reads do not change the workspace revision, create a conversation, fetch a URL, call AI, alter disclosed AI context or grant execution authority.

Use the same authenticated read path in the CLI:

```sh
fased-app agents --profile trading-analyst --memory-search "preference"
fased-app agents --profile trading-analyst --memory-source MEMORY_ID
```

An empty `--memory-search ""` lists the profile's notes. Search and inspection can be combined; chat `--search` and `--request-stdin` cannot be mixed with these reads. Results include the workspace revision, scoped notes/corrections/expiry and optional source inspection. A matched reference includes only retained message/answer text, kind, identities and hash. Free-form, malformed, missing, ambiguous or changed evidence is reported without source text or an opening action. Deleted or foreign memory identities refuse. Source references are owner-editable untrusted data: a matching hash establishes a text match, not the truth of a claim or immutable authorship. Original conversations and existing separately reviewed memory edits remain unchanged.

### Discuss a saved headline

In Agents → News, choose **Discuss headline** on one item in a completed saved
fetch. It opens a new Chat conversation with a bounded historical snapshot and
SHA-256. The message retains the title, item URL/publication time, source/fetch
identities, retrieval time and content/evidence digests. Retrieval/publication
ages and freshness are observations at capture; they grant no permission to use
the item as a fresh task input. The headline remains untrusted data.

CLI: read `agents --profile trading-analyst`, select a completed `newsFetches`
entry and one of its `items`, then submit through `agents --request-stdin`:

```json
{
  "action": "news-item-explain",
  "profileId": "trading-analyst",
  "revision": 12,
  "id": "selected-saved-fetch-id",
  "source": "selected-item-id"
}
```

Both interfaces resolve the same-profile source/fetch/item on the server. Missing,
ambiguous, foreign, changed, unfinished or invalid references and revision/pause/
capacity/size failures refuse atomically. An older completed item remains
discussable after a later failure or source disablement, labelled historical.
Capture never refetches, follows URLs, changes source/task/results/inbox or calls
AI. Raw article/provider bodies and private connection data are excluded. Optional
explanation still needs separate manual disclosure/consent with compiled fixtures;
**Remember this** reuses reviewed memory. No schema/stored type/dependency change,
real AI/wallet authority or automatic/public update is introduced.

### Discuss a saved reminder run

In Agents → Tasks, choose **Discuss run** on a saved reminder result. This opens
a new Chat conversation containing a bounded historical snapshot and SHA-256.
It records the task/run identities, outcome/time and available AI result/usage
with recorded limits. Task state/counters are labelled as observed at capture.
Missing output stays missing; an unknown result is not a successful result.
Private connection/context/disclosure data and raw provider bodies are excluded.

CLI: read `agents --profile trading-analyst`, select a retained `runs` ID and
submit through `agents --request-stdin`:

```json
{
  "action": "task-run-explain",
  "profileId": "trading-analyst",
  "revision": 12,
  "id": "selected-saved-run-id"
}
```

Both interfaces resolve same-profile saved plain/fixture-AI reminder records on
the server under the latest revision. Missing, ambiguous, changed or unsupported
references and capacity/size/paused writes refuse atomically. Financial/paper-bot
and WEN runs are excluded. Capture never runs/resumes/retries a task, changes
its result/inbox, fetches news or calls AI. Historical authorization need not be
current because capture grants none. Optional explanation still requires a new
manual disclosure/consent and only uses compiled fixtures; **Remember this** can
save an edited note through existing reviewed memory. No schema or stored type
changes, real AI, wallet authority or automatic/public updates are introduced.

### Reviewed chat memory

In Agents → Chat, select **Remember this** on a saved user message or a completed
manual answer. Review the retained source, edit the note and optional expiry,
then choose **Save reviewed memory**. Opening/cancelling the form saves nothing.
A changed workspace requires selecting the source again. Notes are owner-reviewed
interpretations; source text is not independently verified and grants no authority.
Agents → Memory provides correction history and deletion. Current unexpired notes
are included in later explicit AI disclosure; expiry/deletion affect future context,
not retained requests or chat history. This is memory, not model training.

The shared CLI action uses the latest workspace revision and the ID returned by
`agents --profile trading-analyst`. Inspect `messages` or completed manual
`aiRequests` first; `source` selects `message` or `answer`, not arbitrary evidence:

```json
{
  "action": "memory-chat-save",
  "profileId": "trading-analyst",
  "revision": 12,
  "id": "selected-retained-message-id",
  "source": "message",
  "text": "My reviewed preference",
  "expiresAt": 0
}
```

Submit that reviewed JSON with `agents --request-stdin`. The server resolves one
same-profile source/conversation and records its kind, identities and SHA-256 in
the existing memory source field, separately from the edited note. It rejects
missing/ambiguous/foreign/system/incomplete/scheduled-AI sources, stale revisions,
expired new notes and capacity/size failures atomically. Each explicit save creates
a note; correct an existing note through `memory-put` with its memory ID. Expiry 0
means no expiry. Saving works in the ordinary core with AI disabled and never
calls AI, schedules a task or enables wallets, mining or updates. Schema 10 and
existing stored types are unchanged.

This bounded prototype holds at most 30 profiles, 100 sessions, 200 messages,
100 memories, 30 paper bots, 100 tasks and 200 total scheduled evaluations, within
a 512 KiB document. Capacity failures preserve existing records; no automatic
pruning removes history. Schema 4 adds this workspace and includes it in update
snapshot/backup identity. Schema-3 instances migrate only outside an update hold;
older binaries refuse schema 4. This is not a supported public upgrade/downgrade
path. Existing P4 production/native/VPS/retail/recovery blockers remain open.

Remaining P2 acceptance includes the platform package journeys, retail signing/trust and runtime prerequisites, and an authorized real Ubuntu VPS/private-access journey. Current engineering package/private-access/recovery instructions are in `docs/install.md`. The Go wallet signer, its independent policy/recovery storage, and a restricted update helper follow in P3/P4. The signer status API reports unavailable and the health API explicitly disables financial/provider/update capabilities.

The controlling product/implementation/checkpoint documents are maintained in the separate `fased-ai/docs` repository under `fased/` (local workspace: `../docs/fased/`). Source commands and boundaries live in [AGENTS.md](AGENTS.md). The old Fased and network server are independent repositories, not runtime dependencies.

## License

Original Fased App code is under [PolyForm Perimeter 1.0.0](LICENSE). It is source-available with restrictions on providing competing products/services; it is not described as permissive or OSI open source. See [NOTICE](NOTICE) for attribution and dependency boundaries. License selection does not change third-party terms.

Optional AI request review is a development fixture, not a connected provider.
Create a conversation in Agents → Chat, prepare a request, inspect its complete
wire disclosure and limits, then consent to that exact digest. Ordinary cores can
prepare and cancel reviews but cannot execute them. A standalone disposable
`fased-app serve --ai-fixture --listen 127.0.0.1:0 --data-dir <disposable-directory>`
can run the compiled in-process Responses fixture. It accepts no API credentials,
never contacts OpenAI and cannot be combined with a managed custody/sandbox or
public-origin option. The fixture uses invented prices; a ChatGPT subscription
is not connected and no billing assurance for real API use is implied.

Requests allow one call, zero tools, no retries/fallback, up to 5 seconds and
explicit input/output/cost limits. Current-profile/session context and unexpired
notes are frozen for review; changed or ten-minute-old disclosure must be prepared
again. Cancellation after dispatch, uncertain usage and restart preserve the
reservation and block further calls for that profile. Verified usage settles even
when output is refused, incomplete or invalid. Returned reminder/paper-strategy
cards are drafts: keep/dismiss records review without creating a task, bot,
permission or trade. Schema 5 protects these records and update snapshots; paused
schema-3/4 instances must finish their existing recovery before migration. There
are at most ten retained requests, with bounded context/output and reserved result
space; records are never silently pruned.

The fixture follows the [Responses API](https://developers.openai.com/api/reference/resources/responses/methods/create),
[Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs)
and [per-run spending example](https://developers.openai.com/cookbook/articles/per_run_spending_controller_responses_api).
Real API credentials, verified model/tokenizer/pricing, subscription runners,
scheduled AI-dependent decisions and financial execution remain later work.

Agents → Tasks also supports bounded **development fixture AI schedules**. Create
a conversation, choose a reminder or existing fixed paper bot, prepare the task,
then review the frozen context, cadence, evaluation count, consent expiry,
per-call limits and total example-cost budget. Only an explicitly started
standalone `--ai-fixture` core can authorize and run it. This is fixed-context
prototype automation; fresh messages, memory corrections/expiry or changed paper
inputs require a new review. It does not fetch news or connect a real provider.

Each evaluation reserves a stable request identity and budget before dispatch.
A validated response allows only the reviewed deterministic step; the model cannot
change quantities/prices/budgets or apply its cards. Completion commits the request,
usage, dependent paper/reminder result, journal, inbox and next schedule together.
Failure, expiry, cancellation or uncertain dispatch holds that task; independent
no-AI tasks continue. A held task cannot resume silently. Pending cancellation
retains any dispatched reservation; restart never retries a dispatched request.
Starting without the fixture flag stops queued AI dependencies without running
them. Schema 6 protects these records; paused predecessor recovery must finish
before migration. Local storage reserves future request/result capacity, with a
shared ceiling of ten retained or planned AI requests and no silent pruning.

### Select memory for one AI review

In Chat → Optional AI step, choose **All active notes**, **None**, or **Selected notes** before preparing the disclosure. Review the exact note IDs, text, sources and expiry in **Exact context disclosure**, then consent separately to a development fixture. These choices affect saved notes only: the same profile persona and conversation history remain in the disclosure. Preparation never runs AI. Mining and real AI/wallet execution remain disabled.

The shared CLI command accepts `memorySelection` only with `ai-prepare`:

```json
{
  "action": "ai-prepare",
  "profileId": "trading-analyst",
  "revision": 42,
  "id": "CONVERSATION_ID",
  "text": "Compare these inputs",
  "memorySelection": { "mode": "selected", "ids": ["MEMORY_ID"] },
  "limits": {
    "maxInputTokens": 24000,
    "maxOutputTokens": 1024,
    "deadlineSeconds": 3,
    "budgetMicrousd": 30000
  }
}
```

Read the current revision and note IDs with `fased-app agents --profile trading-analyst`, then send the command to `fased-app agents --request-stdin`. Use `{"mode":"none"}` or `{"mode":"all"}` without IDs for the other choices. Omitting `memorySelection` preserves the legacy all-active-note context. No notes are silently dropped or truncated; missing, expired, foreign or ambiguous IDs and context/token/budget limits refuse preparation. Changing a selected note invalidates the frozen review. Scheduled AI tasks keep their existing all-active-note consent and reject manual selection input.

### Reuse a saved AI request

In Chat, choose **Use as draft** on a saved manual request. Edit its prompt, model, connection, limits, news policy and memory choice, then **Prepare disclosure**. Opening, editing or **Cancel draft** saves nothing and makes no provider/source call. Legacy requests retain **Legacy active notes** until you explicitly select another memory policy. Missing/expired selected notes, disabled news and unavailable models/connections remain visible; correct them explicitly before preparing.

Read the same settings through private authenticated CLI access:

```sh
fased-app agents --profile trading-analyst --ai-draft REQUEST_ID
```

The result contains preparation fields and scope/revision metadata only. To prepare, explicitly map `sessionId` to command `id`, `prompt` to `text`, and pass the other editable fields to `ai-prepare` with the current revision via `agents --request-stdin`. Omit `memorySelection` for legacy input; do not turn omission into `all`. This creates a new request/disclosure/digest using current context and admission policy. Review it and separately consent to fixture execution. Old consent/results/state/reservations are never copied, and pending or uncertain holds still block another call. Scheduled, foreign, ambiguous, damaged or unsupported saved requests refuse. Mining, real AI/wallets and automatic/public updates remain disabled.

### Inspect saved agent evidence

In an agent’s Chat, use **Saved evidence** to choose a memory note, saved headline or plain reminder run, then **Inspect evidence**. The read-only result shows the selected source, provenance, content hash, record/inspection times and freshness. **Cancel inspection** only closes the local preview. Inspection creates no message, task, model call or source fetch. Existing explicit Discuss, Save memory and AI disclosure/consent remain separate actions.

The same private authenticated CLI projection is available:

```sh
fased-app agents --profile trading-analyst --evidence memory --evidence-id MEMORY_ID
fased-app agents --profile trading-analyst --evidence headline --evidence-id FETCH_ID --evidence-item ITEM_ID
fased-app agents --profile trading-analyst --evidence reminder-run --evidence-id RUN_ID
```

Each response uses `agent-evidence-v1`, identifies one same-profile record and includes a hash of its allowlisted source data and identity. The hash is evidence, not authorization. Memory is the current retained note (including an explicit expired status); headlines/runs remain historical even when a headline is recent. Disabled/stale sources never refresh on inspection. Missing, changed, ambiguous, foreign, unsupported or oversized sources refuse without substitution/truncation. The complete encoded response, including its trailing newline, is at most 16 KiB. Private AI context/credentials, memory revision history and scheduling/financial authority are excluded. AI/news-bound or financial runs are outside this plain reminder inspector. A changed workspace requires reinspection. No schema, service, provider or model-tool execution is added; mining, real AI/wallets and automatic/public updates remain disabled.

### Use inspected evidence in AI review

In Chat, inspect one saved record and select **Use in AI review**. Edit the question, model, limits and memory choices, then select **Prepare disclosure**. Selection, editing, removal and cancellation save nothing. Inspect the complete source facts/hash/times in the disclosure before separately consenting to a development fixture. Normal instances keep provider execution disabled.

CLI uses the existing authenticated `agents --request-stdin` command with `action: "ai-prepare"` and one `evidenceSelection` reference from `agents --profile PROFILE --evidence KIND --evidence-id ID [--evidence-item ITEM_ID]`:

```json
{
  "action": "ai-prepare",
  "profileId": "PROFILE",
  "revision": 0,
  "id": "CONVERSATION_ID",
  "text": "Explain this selected record",
  "memorySelection": { "mode": "none" },
  "evidenceSelection": {
    "kind": "memory",
    "id": "MEMORY_ID",
    "sha256": "INSPECTED_SHA256"
  },
  "limits": {
    "maxInputTokens": 24000,
    "maxOutputTokens": 256,
    "deadlineSeconds": 3,
    "budgetMicrousd": 30000
  }
}
```

Replace the example identities, hash and revision with current private instance values. A headline additionally requires `itemId`; supported kinds are `memory`, `headline` and `reminder-run`. Only the server's allowlisted saved facts are attached. Complete context, including the record, remains limited to 8,000 bytes; an inspectable record up to 16 KiB can still be too large for AI preparation. Nothing is clipped or silently omitted. Expired memory refuses; explicitly selected historical news remains historical and cannot satisfy a fresh-news dependency.

Consent, dispatch, access/count/create, in-flight work and completion recheck source identity/content and eligibility. Changed evidence requires inspecting and preparing a new review. Saved-request drafts preserve unavailable references for explicit correction. Source timestamps remain frozen; unrelated workspace revisions do not renew consent. Existing request/usage/uncertain holds remain intact. No model tools, automatic source fetch, scheduled evidence or live execution is added.
