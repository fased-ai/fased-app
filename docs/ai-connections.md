# Provider connections

## Connect a provider

Open **Providers**, choose a provider and enter its API key directly in Fased.
The first connection form opens immediately. Saved accounts share one encrypted
vault and one catalogue across setup, Agents, Chat and Tasks. Select an account
and one of its available current models; saved requests retain their original
account/model identity.

OpenAI, Anthropic and xAI provide **API key** connections. OpenAI also has a
distinct **Continue with ChatGPT** path, implemented against the official public
client protocol and exercised only by controlled fixtures in this build. The
ordinary build reports sign-in unavailable while live admission remains held.
API-key and ChatGPT registrations keep distinct encrypted identities and model
catalogues. Switching methods requires an explicit separate connection.

Saving, reconnecting, unlocking or refreshing an account may list its available
models. These operations must not create a generated answer, queue a task or grant
Chat/task permission. Generation requires an explicit Send or an authorized task.
Connection setup needs no billing pilot or paid test response. Development uses
disposable keys and controlled fixtures; normal live dispatch remains held.

Lock ends local access. Reconnecting an API key changes its connection reference while
preserving history and uncertain usage holds. Removing a connection deletes its
local credential; provider-side key revocation remains a separate owner action.

## Local models

Providers → Local server offers **Ollama**, **LM Studio** and **vLLM** address presets. Each uses the same encrypted account, `/v1/models` discovery and stateless `/v1/responses` engine. An API key is optional. Presets fill the address; they do not install a server or attest its implementation. Models must be present in both the reviewed registry and that server's response. Setup never generates an answer.

Local means the machine running the selected core: a Remote instance connects to that server's loopback, not the browser's machine. Only explicit loopback IPs/ports are accepted; DNS, proxy routing, redirects and cloud/custom gateways remain refused. Endpoint/key bytes are encrypted together in the unchanged v6 envelope. A locked saved account conceals its address until unlock; it never displays a guessed replacement address.

The private CLI command accepts `action: "add"`, `provider: "local"`, `server: "ollama"`, `"lmstudio"` or `"vllm"` and `passphrase`. Optional `endpoint` overrides the preset address; optional `key` is encrypted with it. Send JSON through `ai-connection --request-stdin`, never secret arguments. Server presets are valid only for local add/save. Replacing a connection revokes the old binding and requires explicit agent/task review.

| Server    | Default address             | Reviewed current model IDs                       |
| --------- | --------------------------- | ------------------------------------------------ |
| Ollama    | `http://127.0.0.1:11434/v1` | `gemma4:e2b`, `gemma4:e4b`                       |
| LM Studio | `http://127.0.0.1:1234/v1`  | `google/gemma-4-e2b`, `google/gemma-4-e4b`       |
| vLLM      | `http://127.0.0.1:8000/v1`  | `google/gemma-4-E2B-it`, `google/gemma-4-E4B-it` |

Identifiers are not automatically aliased. Unavailable/older models remain unavailable; a newly observed Gemma generation invalidates older choices without switching saved profiles or erasing history. The existing `node scripts/provider-models.mjs --check` path records official source hashes and requires manual policy review for the differing server documentation formats; discovery cannot renew admission policy. A registry revision requires fresh review of affected saved permissions; original records/holds are preserved.

Chat/tasks retain finite requests, duration and input/output limits, with no API-dollar allowance or claim about hardware/electricity cost. Optional bearer keys go only to the bound loopback endpoint. Stop, lock, revoke and restart preserve uncertainty holds and never replay dispatched work. Local deadlines remain 1–120 seconds; cloud limits are unchanged. Schema 27/v6 storage and migration boundaries are unchanged. Ordinary dispatch and all live features stay held. Controlled protocol/UI evidence does not claim installed-vendor runtime or real-VPS acceptance.

Sources: [Ollama](https://docs.ollama.com/api/openai-compatibility), [LM Studio Responses](https://lmstudio.ai/docs/developer/openai-compat/responses), [LM Studio models](https://lmstudio.ai/docs/developer/openai-compat/models), [vLLM](https://docs.vllm.ai/en/latest/serving/online_serving/) and [Google Gemma](https://huggingface.co/google/gemma-4-E2B-it).

## Google API sign-in

Providers → Google retains **API key** and adds **Sign in** for the Gemini developer API. Select the Google Cloud quota **Project**, continue in an external browser, and finish with the private vault passphrase. Sign-in and model discovery never generate a response. The resulting account shares the reviewed Gemini 3.8 Flash model, agent assignment, Chat and finite task grants with the existing API-key adapter. It retains standard API allocation/billing; it is not a Gemini consumer subscription or a free Code Assist quota.

Old Fased's `google-gemini-cli-auth` could extract an installed Gemini CLI client identity and call Code Assist. Current [Gemini CLI terms](https://geminicli.com/docs/resources/tos-privacy/) explicitly prohibit third-party use of the services powering that login. Fased does not extract installed CLI credentials, borrow that client registration or call `cloudcode-pa`. This implementation follows the documented [Gemini API OAuth](https://ai.google.dev/gemini-api/docs/oauth) and [Desktop OAuth](https://developers.google.com/identity/protocols/oauth2/native-app) paths instead.

**Production prerequisite, not created or activated here:** an appropriately registered Fased Desktop OAuth application/consent configuration, supplied to the core through `FASED_GOOGLE_OAUTH_CLIENT_ID` and `FASED_GOOGLE_OAUTH_CLIENT_SECRET`, plus the owner's quota project with Generative Language API access and appropriate IAM/billing. No production client ID/secret is bundled. Missing registration disables Google sign-in; API-key setup remains separate. Standalone fixture mode selects an internal disposable client and signing fixture without reading production application credentials or contacting Google.

Local sign-in uses an ephemeral `127.0.0.1` callback with state, nonce and S256 PKCE. Remote uses the same private loopback callback, requiring private SSH forwarding of that callback port before browser completion; the port is present in the authorization URL's `redirect_uri`. No public listener, deprecated out-of-band code flow, borrowed CLI identity or automatic tunnel setup is added. Local/Remote protocol fixtures pass; real Remote/native-browser and production-registration acceptance remain open.

The shared v5 encrypted vault binds provider, mode, account reference and fixture/live class. Google OAuth has its own typed credential (`google-oauth`) and `oauth` account method; it never becomes ChatGPT plan access or a raw API-key import. Tokens, refresh token, application client secret and quota project stay encrypted. Only the account's access token and exact project reach the fixed Google API route; the sanitizing transport deliberately preserves `X-Goog-User-Project` only for this typed OAuth path. Refresh, reconnect and remote revocation use fixed Google OAuth endpoints; reconnect preserves the account/project/subject and explicit refresh revokes previous grants.

An expiring access token may refresh in memory only while the account is unlocked or the exact current task grant permits that request. This does not extend unlock, scope, attempts, deadlines or budget; it never rewrites the encrypted vault. A rotated refresh token requires the explicit passphrase-protected Refresh operation. Expired/ungranted/signed-out accounts cannot refresh or fall back to an API key. Restart locks credentials and drops transient grants while preserving records, results, memory and holds. All ordinary live features remain disabled.

## Kimi regions and billing

Providers → Kimi offers **China · CNY** and **International · USD** as explicit separate API accounts. An existing mainland account stays on `api.moonshot.cn`; it is never changed into an international account. To use the reviewed USD path, add an international API account with its own key. The shared CLI accepts `provider: "moonshot-intl"` through private `ai-connection --request-stdin`; there is no arbitrary cloud endpoint override. Account setup refreshes models only and cannot queue generation.

The international connector uses the official [`api.moonshot.ai/v1` contract](https://platform.kimi.ai/docs/api/chat), [authenticated model list](https://platform.kimi.ai/docs/api/list-models) and latest Kimi K3.1 Pro. Its internal model ID is `moonshot-intl/kimi-k3.1-pro`; only the wire strips the region namespace. Encrypted account identity, agent assignment, review/consent, Chat, finite task grants and result history keep that regional binding. A key/model/endpoint failure cannot fall back to the other region or account.

**USD tariff reviewed 28 October 2026:** the [international pricing page](https://platform.kimi.ai/) lists $2 input, $0.20 cache hit and $10 output per million tokens, with $2 cache writes for the reviewed five-minute cache. The connector explicitly pins `prompt_cache_options` to `implicit` / `5m` and reasoning effort to `low`. Under the [cache contract](https://platform.kimi.ai/docs/guide/context-caching), uncached input, cache reads and cache writes are mutually exclusive portions of total input. Since the five-minute write price equals the input price, existing integer USD accounting charges the noncached portion once at the input rate and the read portion at the cache rate; it does not add a second input charge. Missing, negative or overlapping cache counters retain the unknown usage hold. No FX conversion or synthetic zero-cost subscription allowance is used.

The original mainland CNY model/tariff remains inspectable but unselectable for execution in the USD-only budget engine. Its keys, selected model, drafts, completed results and holds remain intact. Native-CNY accounting is not implemented by this international route. Provider maintenance includes international model, tariff, cache, discovery and billing sources; review cannot change currency/region, silently renew policy or enable inference. Kimi memberships/Code subscriptions and browser sign-in are distinct from this API-key package. Live use remains disabled.

## Qwen authentication

Qwen's current official [authentication guide](https://github.com/QwenLM/qwen-code/blob/main/docs/users/configuration/auth.md) and [quickstart](https://github.com/QwenLM/qwen-code/blob/main/docs/users/quickstart.md) mark portal OAuth discontinued on April 15, 2026. Old Fased's `qwen-portal-auth` plugin is a legacy reference, not an available new-account sign-in method. Fased therefore shows the supported API-key method for Qwen; explicit legacy sign-in requests return the reason without exchanging tokens or changing accounts, drafts or permissions.

The Qwen editor identifies **ModelStudio · Singapore**. Existing encrypted accounts retain that provider, host and billing identity. Account refresh and pre-generation admission use ModelStudio's documented [native model listing](https://www.alibabacloud.com/help/en/model-studio/list-models), not an assumed OpenAI `/models` route: authenticated `GET /api/v1/models?page_no=1&page_size=100&providers=qwen`. Up to four complete, consistent pages are accepted; errors, duplicate IDs, incomplete pages, changing totals and larger catalogs fail closed. Offline notices disable that model. The account list remains intersected with the compiled latest-only model/price policy; discovery does not grant new model or spending authority, and adding an account cannot generate a response.

QwenCloud is a separate API product with region/workspace-specific [keys](https://docs.qwencloud.com/developer-guides/administration/api-keys) and [endpoints](https://docs.qwencloud.com/developer-guides/getting-started/regions). Its generation routes are documented, but an authenticated account-model discovery contract was not established in this package. Fased does not silently migrate ModelStudio accounts, expose an unverified QwenCloud route, or infer a key's region from the interface language. Other regions require separately reviewed routes, models and tariffs.

**Subscription exception, reviewed 28 October 2026:** the detailed terms for Token Plan [Personal](https://docs.qwencloud.com/token-plan/personal/token-plan-personal-overview) and [Team](https://docs.qwencloud.com/token-plan/team/token-plan-team-overview) restrict use to interactive compatible tools and prohibit automated scripts/application backends. ModelStudio [Coding Plan](https://www.alibabacloud.com/help/en/model-studio/coding-plan) likewise prohibits noninteractive automation. These products are not offered for Fased's backend/unattended tasks. Their `sk-sp-` keys are rejected before account creation/replacement, and never transmitted from older saved records through the standard API route. Refusal preserves encrypted records and drafts; there is no pay-as-you-go fallback. Broad compatibility statements on overview pages do not override these specific terms.

The existing provider maintenance proposal now includes the native discovery and subscription terms as official sources alongside authentication/model/protocol/pricing sources. Changes require reviewed policy work, never automatic activation. Live inference stays disabled.

## ChatGPT sign-in

In a standalone `serve --ai-fixture` core, choose OpenAI → Continue with ChatGPT.
The returned link completes a disposable callback on `127.0.0.1`; it never opens
OpenAI or uses a subscription. Finish with a private vault passphrase. A Remote
connection can paste the exact redirect URL instead. This is the same pending
attempt, state/nonce/PKCE validation, encrypted storage and account selection used
by the held production implementation. No answer or task is created by sign-in.

The implemented production protocol uses a stable installation host ID, dynamic
first registration and the issued client ID on reconnect. It verifies the ID
token's RSA signature against the issuer's JWKS, issuer, audience, expiry and
attempt nonce before saving account identity. Identity sign-in alone grants no
model access: the token must include the ChatGPT plan-use and resource scopes.
Tokens, issued registration and verified identity are encrypted together in a
version-5 vault envelope. Older readers refuse that envelope. Ordinary startup
does not admit a fixture envelope or expose an endpoint/trust override.

Refresh requires the account's vault passphrase and rotates the encrypted token
set without changing account identity. Refreshes are serialized per account and
revoke earlier interactive/task grants. Expired access fails closed until refresh
or reconnect; a background task cannot refresh itself or extend its credential
allowance. Disconnect first locks access, attempts provider-side renewable-session
revocation and, on success, removes tokens while retaining the encrypted client
and verified-account mapping for reconnect. If revocation fails, the account stays
locked and the UI reports that confirmation is missing. Lock/logout/restart
cancel pending sign-in and invalidate active access; no request is replayed.

ChatGPT account model discovery consumes its `models`/`slug`/`visibility` format,
then applies the reviewed latest-only registry. Subscription inference has its
own HTTP wire contract and must never fall back to API-key billing. New requests
use the `openai-signin-v2` plan policy: API-dollar rates and monetary allowances
are zero, while request count, authorization lifetime and application input/output
acceptance limits remain finite. These limits are not a forecast of ChatGPT quota
or a provider-enforced output cap; the route omits unsupported `max_output_tokens`
and token-count endpoints. Only verified response usage becomes token usage in
the journal. No subscription price or reset time is invented. Retained v1
synthetic records keep their original policy, disclosure and holds; their grants
are not silently upgraded. Ordinary live activation remains disabled.

Direct HTTP errors, failed/incomplete terminal events and interrupted streams
retain bounded status, body shape, code/parameter, request ID and content hash.
Provider descriptions, raw error bodies and reflected credentials are omitted.
The affected account pauses without deleting its vault or changing billing.
**Review access** uses the existing explicit vault unlock, revokes old grants and
requires fresh Chat/task permission; it never retries a request. Model refresh,
page focus and timers cannot clear the pause. Transient failures retain credentials.
Unverified dispatched usage remains held even though no API money is reserved.
Verified terminal usage settles its counters on failed or stopped requests while
discarding all output and preserving cancellation/dependent-task failure.

Schema 26 protects the new zero-money authority and usage holds from older
readers. Migration from idle schema 25 changes only the version; documents,
revisions, encrypted credentials and recovery holds are preserved. Paused state
must finish its existing recovery first. Runtime dispatch and credential-admission
flags stay closed; controlled ordinary-mode HTTPS fixtures test the production
admission path without a developer API key, subscription call or paid pilot.

Provider route review, 27 October 2026:

- **OpenAI:** [registration](https://developers.openai.com/siwc/token-sharing-open-source/sign-in),
  [session renewal/revocation](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions),
  [models/inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)
  and [HTTP limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)
  define the implemented locally hosted/self-hosted client route. Paid or hosted
  distribution requires its own provider-approved registration path; it does not
  authorize copying an official Codex client identity.
- **Anthropic:** its current [authentication and credential-use rules](https://code.claude.com/docs/en/legal-and-compliance#authentication-and-credential-use)
  prohibit collecting/intermediating Claude subscription tokens in a third-party
  application. Keep API keys here. Running its unmodified Claude Code binary is
  a separate integration, not the old app's direct OAuth-token route.
- **xAI — sign-in BLOCKED, source review 28 October 2026:** old Fased's `src/commands/xai-oauth.ts` defaults to a Grok CLI client identifier and CLI-specific scope. The [official CLI authentication guide](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/02-authentication.md) describes that application's browser/device flows. The [enterprise documentation](https://docs.x.ai/build/enterprise) distinguishes OAuth inference on `cli-chat-proxy.grok.com` from the API-key path on `api.x.ai`. The public [issuer metadata](https://auth.x.ai/.well-known/openid-configuration) advertises PKCE, device authorization and ES256 ID tokens, but no dynamic client-registration endpoint. It does not establish a supported Fased client, permitted inference route, account model-discovery contract, entitlement/accounting or unattended-task rights. This is an unresolved integration prerequisite, not a claim that xAI forbids every third-party client. No CLI identity/token import, proxy impersonation, API-key substitution or simulated working login is added. Explicit `sign-in-start` for xAI returns that reason before exchange/attempt creation, preserving keys, records, drafts and unrelated pending logins. The existing API-key → current model → agent → Chat/tasks route is retained; the UI offers only its implemented API method. Revisit sign-in when xAI provides the Fased registration and associated API/proxy/usage contract, not after another fixture milestone or real API probe.

The CLI uses the same authenticated `ai-connection --request-stdin` interface:
`sign-in-start` takes `provider:"openai"`, optional `ref`/`name` and `remote`;
`sign-in-complete` takes `signInId`, the private `passphrase` and optional pasted
`callback`; `sign-in-cancel` takes `signInId`; `sign-in-refresh` and
`sign-in-disconnect` take `ref` and private `passphrase`. Read-only status exposes
the pending attempt and public account state, never tokens. Keep secrets in a
private pipe; never place them in command arguments or shell history.

## Current ordinary build

This build accepts **disposable `fased-test-` keys only**, never real provider credentials. Normal and managed cores cannot execute provider calls. A standalone `serve --ai-fixture` core can authenticate a bound test connection to its compiled in-process fixture. The shared worker now has a fixed-origin HTTPS transport for the official OpenAI, Anthropic and xAI model/count/generation paths, but its production dispatch gate remains closed. Tests exercise that same transport with a loopback TLS dialer defined only in Go test files; no endpoint/CA override, real model call, subscription, wallet or public-update authority is exposed in the application.

Open **Providers** from the toolbar or Agents settings. Choose **Add provider**, select OpenAI, Anthropic or xAI, name the account, and enter its disposable key and vault passphrase. Up to eight accounts have independent encryption, unlocks and task access. Saving or unlocking discovers models for that exact account; the refresh icon repeats discovery. Agent creation/settings choose the account and model; Chat and Tasks inherit them, with explicit reviewed overrides. OpenAI sign-in uses this same account/model flow through the controlled route described above; ordinary live use remains disabled.

In Chat, select a conversation or choose **Start chat**. Select **Demo** or the unlocked account, then a supported current model. **Send** (Enter; Shift+Enter inserts a line) first opens **Allow replies** for this conversation, memory scope, model and connection: at most 20 replies / 15 minutes with a reviewed cost allowance (normally $0.60; a higher model reservation is shown explicitly). **Allow and send** dispatches one reply; subsequent Send uses only the remaining permission. Changed scope requires a new review. **Stop** cancels the current request. Partial text comes from controlled provider SSE deltas; it is transient and cannot become memory or action cards until complete response/usage validation. The fixture never contacts a provider. **Message options → Review each reply**, new evidence/news scope and saved-request drafts retain the existing exact-disclosure review; schedules/tools are outside Chat permission.

Connection/model controls stay beside the composer. **Providers** opens connection management; **Message options** holds memory/source/limit controls and the optional **Save message** local-only action. Completed replies retain **Remember this**, **Use as draft** and reviewed proposal controls. A selected locked, replaced or missing connection preserves the question and requires an explicit correction; it never switches credentials or falls back automatically. Connection changes and profile preferences do not authorize requests or erase history. Restart locks the vault and preserves conversations/results.

**Latest models only.** Current recommendations are GPT-6.1 Terra, GPT-6.1 Sol and GPT-6 Astra; Claude Haiku/Sonnet/Opus 5.5 and Fable 5.1; and Grok 4.7. Each account offers only its provider’s reviewed current models. The core intersects reviewed model definitions with each account's returned model list. Listing does not establish credit, quota, compatibility or permission to generate. Unknown/older models cannot execute; older histories and drafts remain inspectable and require explicit current-model review to run again. Discovery failure, expiry or account changes disable selection without substituting a different model/account. Known superseded models stay refused after failed refreshes. Discovery is bounded, in memory, and cleared with credential access; restart locks every account.

Fresh model selection proposes a sufficient budget for the selected tariff. An account refresh never raises owner-edited limits. Exact model/account, wire settings, tariff and budgets remain bound to consent. OpenAI uses explicit-only caching with no breakpoints. Anthropic sends no cache-creation directives; xAI uses standard automatic cache reads. Reported cache usage is validated against the pinned tariff. Reasoning metadata cannot become an answer or action. Actual usage and admission must still validate before completion.

## Provider maintenance

Runtime refresh uses the selected account and the official Models API. It changes availability metadata, not approved tasks, model definitions or adapter code.

Maintainers use one source update path:

```sh
node scripts/provider-models.mjs --check
node scripts/provider-models.mjs --apply-reviewed .evidence/provider-models/provider-update-proposal.json
```

The check fetches the current provider catalogue, authentication/discovery/cache contracts, model capabilities/limits and standard pricing from approved official sources for all three API-key adapters. Native documentation uses its official machine-readable Markdown link, recorded with its hash. It writes one local proposal with source hashes, current model metadata and proposed removals. An empty/incomplete catalogue, missing source, incompatible model or new family cannot be silently renewed/applied. New model families and API/authentication changes need adapter review and focused checks. Review the proposal and source diff before applying; application changes repository definitions only. Build/test and the existing trusted release process deliver accepted changes. Automatic/public updates remain disabled.

Run this before provider releases, after an upstream change or when a model stops working. Additional provider adapters must register their official sources and contract checks in this same maintenance path. It covers OpenAI, Anthropic and xAI API adapters; review the distinct sign-in protocol sources above when changing that route. A newer generation is removed from old recommendations in the proposal and requires explicit adapter review before addition. No schedule, public policy downloader or new service is installed.

Schema 25 protects provider identity, native request policy and billable-output reservation. Schema 24 introduced current-model policy/wire fields. Migration preserves records and revisions, refuses paused state and leaves encrypted credential bytes unchanged. Older readers refuse this state.

The Local or Remote engine owns `ai-credentials.json`: Argon2id (64 MiB, three iterations, parallelism two) derives an AES-256-GCM key from the vault passphrase and a random salt. Authentication binds the encrypted API key to the instance and random connection reference; native envelopes also bind the provider and credential mode. The private data directory/file protections apply (0700/0600 on Unix, current-user/SYSTEM DACL on Windows). No unlock password or encryption master key is stored next to the ciphertext. Interactive unlock lasts at most 15 minutes. Explicitly reviewed task access may retain the credential in memory until its finite expiry; it does not unlock Chat. Lock, logout, restart or ciphertext changes invalidate access. These controls protect stored data; a compromised owner account/core can access an unlocked credential. Go/browser memory cannot promise perfect zeroization.

Replacement requires the current reference and vault passphrase and creates a fresh reference. Prepared/queued old references cannot dispatch, and active requests are cancelled on revocation/lock. Unknown dispatched usage retains its reservation and is never retried. Completed answers and journals survive replacement/removal. Removal requires the vault passphrase, preserves request history/holds and is local deletion, not provider-side revocation or secure disk erasure.

The encrypted vault is separate from SQLite and deliberately excluded from metadata snapshots/backups. Metadata recovery cannot unlock or recreate a credential; missing/changed vault references fail closed. Back up the encrypted file separately if desired and keep its passphrase separately. Losing the passphrase requires provider-side revocation and reconnection; app-password recovery cannot decrypt it. Preserve a damaged vault before owner recovery. This fixture path does not claim an OS keychain or unattended background unlock.

The advanced CLI shares the same owner-authenticated private IPC:

```sh
fased-app ai-connection --data-dir /private/instance
fased-app ai-connection --data-dir /private/instance --request-stdin
```

`--request-stdin` requires a private pipe (an echoing terminal is refused). Send one strict JSON object: action `save` with `key`, `passphrase` and current `ref` (empty on first save); `unlock` or `remove` with `ref`/`passphrase`; `lock` with `ref` and no secrets. Never put keys/passphrases in command arguments, shell history or shared files. Responses never return secrets. Generic `agents` commands accept `connectionRef`, never credentials; both manual and scheduled consent bind it.

The Responses contract uses fixed instructions, scoped data, `store:false`, no tools, a strict answer/draft schema and explicit input/output/deadline/example-cost limits. Controlled HTTPS fixtures verify Bearer authentication, certificate validation, exact origin/path, redirect refusal, no environment proxy, no retries and usage reconciliation. Credential reflections/errors never enter history. Model `fixture-openai-v1` and rates 1/4 micro USD per input/output token are **fixture capabilities**, not an OpenAI model/tokenizer/tariff guarantee. Production model/access/pricing evaluation and live activation remain separate gates.

The wire contract follows the official [Responses API](https://developers.openai.com/api/reference/resources/responses/methods/create), [Structured Outputs](https://developers.openai.com/api/docs/guides/structured-outputs) and [API key guidance](https://developers.openai.com/api/docs/guides/production-best-practices#api-keys). No SDK/service dependency was added. Schema 7 plus `agents-ai-connections-v1` protects binding fields from older writers; a paused schema-6 instance must finish recovery before migration. Existing unbound fixture plans/requests remain records, but changed request digests require fresh manual consent before a new dispatch.

## Finite access for reviewed tasks

In **Providers → Background access**, select the agent and already-reviewed tasks, choose up to 24 hours and **Review access**. The core derives the selected task digests, one model, per-request token/time ceilings, at most ten remaining requests and their total cost ceiling. Confirm the exact review with the vault passphrase using **Allow task access**. Task disclosure, schedule, limits, freshness checks and failure isolation remain authoritative. Connected scheduled tasks require this allowance even during an interactive unlock. Local no-AI tasks need no credential and continue independently.

Closing the browser preserves the finite allowance. **Revoke**, lock, sign-out, expiry, rotation, clock rollback or restart stops new dependent calls and cancels in-flight access conservatively. Dispatched unknown usage keeps its reservation. An allowance counts each attempted request by its full cost ceiling, without recycling it; verified spending remains in the request journal. Restart is locked with no credential/grant persistence or replay. No owner password, vault passphrase or master key is stored next to the ciphertext, and no wallet authority is introduced.

UI and CLI use the same owner-authenticated records. `agents --request-stdin` accepts revision-checked `chat-review`, `chat-allow` with the returned `chatPermission`/`consentDigest`, `chat-send` and `chat-revoke`. `ai-connection --request-stdin` accepts `task-access-review` with `ref`, current `revision`, `profileId`, selected `taskIds` and `authorizationSeconds`; `task-access-allow` requires returned `allowance`, its `consentDigest` and vault `passphrase` through a private pipe; `task-access-revoke` takes only `ref`. Never pass secrets as command-line arguments. Read status with `ai-connection` and `agents --profile PROFILE`.

Schema 19 / `agents-chat-permission-v1` protects durable Chat permissions and stream bindings; older readers refuse. The format-only migration preserves document bytes/revisions and rejects paused recovery or new fields in schema 18. Partial output and task credential grants are never persisted. Existing active-document and ten-request ceilings remain; capacity failures preserve linked news/research/tasks and uncertain holds. Archive settled conversations through existing History to free slots, with journals/spending/source inspection retained. Linked or unsettled work refuses archiving; no automatic pruning, cap increase or unbounded automation is claimed.

## Current model admission

The reviewed registries in `internal/core/provider_models.json`, `provider_anthropic.json` and `provider_xai.json` own the current model IDs, capabilities, tariffs and expiry. They are shared by UI and CLI. New requests freeze model, account, tariff, reasoning and cache wire settings in their policy; later updates cannot rewrite their disclosure. Expired/changed policies require a fresh review. The tighter owner limits remain 1,000–32,000 input tokens, 128–1,024 output tokens and a 1–5 second exchange deadline.

OpenAI and xAI use Responses; Anthropic uses Messages with API version `2026-10-21`. All use streaming and structured text output, with no tools, images, attachments or alternate billing tier. Exact-model access is checked before generation. OpenAI’s count covers its complete token-bearing request; Anthropic’s native token count is an estimate. xAI tokenizes the full submitted wire as text, also applies a byte bound plus framing reserve, and treats the result only as an estimate. Actual usage must validate inside reviewed limits before completion. Costs are public-tariff estimates, not provider invoices or tax/discount claims.

Anthropic’s inclusive output count already covers thinking. Grok reports reasoning separately from visible output, and its visible output cap is not a billable ceiling. Grok reviews therefore disclose a conservative 500,000-token output reservation, including reasoning, in addition to the requested visible cap. Default 24,000 input limits reserve $3.048 per attempt at the reviewed standard tariff. This is deliberately conservative; it is not a measured typical price or a provider-enforced hard spend cap. Unknown/over-limit usage keeps its reservation without retry. Development live dispatch remains closed. Owner-elected runtime use and release activation are separate decisions; no developer billing pilot is a prerequisite for this integration.

A missing/mismatched model, tier, usage or completion retains uncertain reservations without retry. Account listing alone cannot prove quota or generation permission. Every request retains admission timestamps and payload/response hashes. Legacy records keep their original policies and costs and remain inspectable; latest-only selection does not rewrite them. Older provider models cannot be selected or dispatched.

Model-list, exact-model, token-count and Responses operations remain controlled-fixture acceptance only. Normal/managed provider execution, real credentials, wallets and automatic/public updates remain held.

## Transport implementation and activation boundary

`openai_transport.go` supplies the fixed-origin TLS transport used by the core worker. It accepts only the already-admitted model, token-count and Responses paths, drops ambient headers, disables proxy inheritance and connection reuse, removes replayable request bodies, bounds each exchange to five seconds and refuses redirects. The existing authenticated wrapper, admission, SSE parsing, consent, finite task grants and journal remain authoritative. No second provider engine is introduced.

Production construction is disabled: neither CLI, environment, UI nor IPC can open this gate. Real credentials remain rejected by the existing vault admission. Controlled tests replace only the TCP destination and trust root, retain the official request Host, and pass through the same transport. This injection exists only in `_test.go` files. Runtime `--ai-fixture` still uses the explicit inert development responder; it never silently stands in for an unavailable provider.

The dormant HTTPS transport and mode-bound credential implementation are present; neither establishes live provider acceptance. A separately authorized activation change must admit the credential lifecycle, recheck the pinned model/tariff contract and enable dispatch together; it must not simply toggle the transport gate. Actual account availability, billing, remote responses and retail usability remain unverified. No UI or stored data changes accompany this transport delivery.

Protocol references: [OpenAI Responses streaming](https://developers.openai.com/api/docs/guides/streaming-responses), [Anthropic Messages](https://platform.claude.com/docs/en/api/messages/create) and [xAI Responses](https://docs.x.ai/developers/rest-api-reference/inference/responses), reviewed against current official documentation. Model definitions and tariffs travel through the reviewed source-update path above.

## Shared agent selection

An agent stores its provider, immutable account reference and current model preference. Chat and scheduled tasks inherit that selection for new reviews. A deliberate override affects only that review; changes never retarget saved requests or tasks. Missing, locked, replaced, superseded or unavailable choices require owner correction. Profile settings select defaults; consent and finite limits authorize work.

## Dormant API-mode vault lifecycle

The existing provider boundary can process a version-2 vault with `mode: "openai"`; all ordinary runtime constructors keep its admission gate closed. Only Go tests enable it, using an explicitly disposable string that was not issued by a provider and the existing loopback TLS transport. No environment, HTTP, CLI or UI switch opens admission or transport. Live calls remain disabled independently of credential readiness.

Version 2 authenticates the instance, fixed OpenAI provider, mode, vault version and random connection reference in AES-GCM associated data. The same passphrase derivation, private-file protection and metadata-backup exclusion apply. API-mode inputs are bounded opaque ASCII tokens (24–256 bytes); syntax is not evidence that a key or account is valid. Both interactive unlock and finite task access use the same authenticated decryption. API-mode requests require the admitted model and policy, never the synthetic fixture model. Legacy integer fixture-rate fields are omitted for API mode; the versioned admission policy carries the model tariff.

Existing version-1 test vaults remain byte-for-byte unchanged by reads and unlocks. Only explicit owner-passphrase-authorized replacement can create another mode/version, with fresh salt, nonce and reference. Version-1 readers reject version 2; downgrading metadata cannot make ciphertext decrypt under version-1 associated data. Never overwrite an unrecognized or damaged vault to recover it. Restart locks API mode with its admission gate closed. Inactive API vaults report their mode and locked state, refuse unlock/task access and retain their bytes; an owner can still remove one or replace it with a test vault after proving its passphrase. Removal is local deletion, not provider-side revocation or secure disk erasure.

Controlled evidence covers replacement/rotation, wrong passphrase, mode/version/reference/instance/ciphertext tamper, closed restart, inactive cleanup, Chat and finite scheduled use through loopback HTTPS. Real credentials, actual account/model availability, billing and public activation remain unaccepted and disabled. No keys or owner state are migrated automatically.

Native version-4 envelopes bind instance, provider, mode and reference in AES-GCM associated data. Adding another provider preserves the existing OpenAI version-1/2 envelope bytes inside the version-3 account collection. Provider changes require adding another account, not relabeling an existing credential. Wrong-provider origins and headers are refused before credentials leave the core. Each account has its own discovery, lock, Chat epoch and finite task grant. Schema-24 readers refuse schema 25; paused migration is refused. Fixture and controlled-HTTPS acceptance do not activate ordinary API credential admission or network dispatch.

<details>
<summary>Retired maintainer pilot — historical only</summary>

The owner removed this pilot from the development queue before activation. Its
source/fixtures and unapplied patch are retained; the following instructions grant
no activation or API-use authority and are not normal provider setup.

## Scoped OpenAI pilot gate

Ordinary builds remain closed. No setting, environment variable, CLI flag or
provider key enables this pilot. Its target is unset in every runtime constructor;
a separately reviewed source patch is required to bind one directory/instance/account/agent/current model. Live
activation requires explicit owner authority. The shipped compiled flag remains false with an empty binding.

For that scoped path, the existing authenticated `ai/connection` API and
`ai-connection --request-stdin` accept `pilot-review`, `pilot-allow` and
`pilot-revoke`. Review binds the instance, boot, immutable account, agent,
current model/tariff, credential epoch and workspace revision. Allow requires the
exact digest and vault passphrase. Review/revoke carry no secret. The compact
**Pilot** section appears in Providers only when the core reports this scope.

The fixed allowance lasts 15 minutes: four attempts, split into three Chat
replies and one separately reviewed one-run task; $0.50 total, $0.25 per lane,
$0.10 per request, at most 2,000 input / 256 output tokens and five seconds.
Existing Chat permission and task credential access are still required. Only
synthetic local inputs are in scope; news/research and financial operations are
excluded. One Models listing and one ordered model/count/create sequence per
attempt are permitted, with no retry or fallback. Allocation is conservative:
an attempted request never replenishes its pilot reservation, even when verified
usage is lower. This is an app admission ceiling, not a provider invoice cap.

Stop, revocation, lock, sign-out, expiry and clock rollback cannot reset attempts
or allocated cost. Proven unsent queued work releases its journal hold;
dispatched/unknown usage retains its durable hold. The allowance cannot be
rearmed in the same boot. Restart restores no pilot permission or credential
unlock and never replays requests; history, drafts, results and memory remain.

## Activation preparation — maintainer only

The existing engine can prepare a new isolated target without starting a server:

```sh
mkdir -m 700 ./pilot-preparation
fased-app pilot prepare \
  --instance-dir "$PWD/pilot-preparation/instance" \
  --review-dir "$PWD/pilot-preparation/review"
```

Both directories must be new. Preparation refuses existing instance/review paths,
root execution and an already activated build. It creates fresh SQLite metadata
with the existing core and reserves an account reference. It creates no owner
password, provider key, permit, listener or service. Existing data is never adopted.

`review/review.json` records the exact directory, instance, agent, reserved account,
current model/tariff, limits and patch hash. `review/activation.patch` is an
**unapplied** source patch for `internal/core/openai_pilot_target.go`. The review
JSON is not configuration and cannot activate anything. The actual source still
ships `openAIPilotActivationEnabled = false` and an empty binding. Do not apply,
build an enabled candidate, enter real credentials or make calls without separate
authority and review of that exact target and candidate. Preparation does not
verify a real account's model access or billing.

The prepared path, when separately approved, checks its canonical directory and
read-only database identity/schema before normal startup writes. It permits only
standalone Local mode, one initial OpenAI key into the reserved encrypted slot,
and the existing finite pilot consent. First key entry does not dispatch or fetch
models; Allow pilot admits its one bounded listing. Other providers, extra or
replacement accounts, signer/Remote/fixture modes, RSS and external research are
refused. UI and CLI share this policy. Active and immutable archived conversation
records prevent rearming the one-time pilot, including zero-cost failed attempts.
Normal installations retain their existing behavior and closed live gate.

</details>

## Kimi Code membership

Providers → Kimi → **Code · Interactive** adds a separate membership API-key account. Use a Code console `sk-kimi-` key; Platform API keys and Code keys are not interchangeable. The private CLI uses `provider: "kimi-code"` and `credentialMode: "kimi-code"` for API keys, through the existing stdin credential command. Fixture builds continue using disposable test credentials. No account setup generates a response.

The connector pins `https://api.kimi.ai/coding/v1`, sends its true `Fased` client identity and discovers account models through `/models`. Only current reviewed `kimi-code/k3.1-pro` and `kimi-code/k3.1-pro-highspeed` are selectable; wire IDs omit the namespace. The application uses a conservative 256K context ceiling and its existing finite reply token/time/request limits, with thinking enabled at `low`. It never substitutes older aliases, a different region, Platform API or another account.

[Kimi's official third-party guide](https://www.kimi.com/code/docs/en/third-party-tools/hermes.html) documents the membership-key Chat Completions route. [Current guidelines](https://www.kimi.com/en/help/kimi-code/community-guidelines) restrict subscriptions to personal interactive use and exclude non-interactive automation and reselling access. Therefore this account supports explicitly permitted **Chat**, but not scheduled/background AI tasks or task credential grants. The same task engine enforces that boundary, including direct CLI and dispatch paths; Providers hides Background access. Choose a separately configured Platform API account for unattended tasks. Fased never switches accounts automatically.

Membership requests consume the user's subscription quota. No API dollar tariff or remaining-quota figure is invented. Verified token usage, finite reply permission, uncertain-usage holds, lock/reconnect and restart behavior reuse the shared core and encrypted v4 account envelope. Existing Platform accounts, drafts and journals are preserved. Model/terms updates use the existing provider review command and require manual policy review. Live requests remain disabled; local fixture verification is not vendor/live acceptance.

## Anthropic API workspaces

Providers → Anthropic keeps **API key** authentication. Workspace-scoped keys need no extra setting. For a personal or service-account key that is not workspace-scoped, open **Options → Workspace** and enter its `wrkspc_…` ID from Claude Console. The private CLI uses `workspace` on the existing `ai-connection --request-stdin` add/save command. Setup only discovers models; it cannot generate a response.

The [current official API-key contract](https://platform.claude.com/docs/en/manage-claude/authentication) requires `anthropic-workspace-id` for unscoped keys. Fased encrypts that ID together with the key in a version-7 account envelope; the associated data binds instance/provider/mode/reference. Old v4 workspace-scoped accounts retain their bytes. A v7 replacement requires explicit workspace selection and creates a new reference, revoking old authority. Locked accounts conceal the workspace; protected unlock/reconnect restores it. Older readers refuse the new envelope version.

The same bound workspace travels through authenticated model discovery, token admission, Chat and separately granted Tasks. The sanitizer drops ambient workspace headers and forwards only the credential-derived private binding. No workspace discovery/admin permission, different workspace, model, key or API region is silently substituted. Existing model/tariff/consent/result/memory rules remain authoritative. Explicit API-key expiry/revocation failures need reconnect; the app does not retry or mint a replacement key.

Claude subscription OAuth/setup tokens are excluded under [Anthropic's current credential rules](https://code.claude.com/docs/en/legal-and-compliance#authentication-and-credential-use). API-key entry refuses the recognizable subscription-token format and cannot import a typed credential JSON. Sign-in attempts explain the API-key alternative without touching accounts, pending sign-ins or work. WIF/App Attest and hosted unmodified Claude Code are separate enterprise/native integrations, not required additions to this retail self-hosted provider package. All live execution remains disabled.

## Ollama Cloud access

**Ollama Cloud → API key** uses the documented fixed `https://ollama.com/v1` endpoint without installing Ollama. Private CLI setup uses `provider: "ollama-cloud"`; the same encrypted v4 account, model review, configured agent, Chat and finite task grants are reused. Selectable direct-cloud model: current `ollama-cloud/gemma4:31b`. Namespace removal happens only on the wire; local/server accounts cannot be silently changed into Cloud accounts.

For server-mediated access, sign into the user's own Ollama installation through its official app or `ollama signin`, and make its `gemma4:cloud` alias available. Connect that loopback server through the existing **Local server → Ollama** preset. Fased admits the cloud alias only when the server advertises it; it never reads/copies Ollama's login files, tokens or CLI client identity. The existing stateless Responses adapter already avoids server-enforced schema options, so the same account/agent/Chat/task engine handles the advertised cloud alias. Actual vendor login/installed-server acceptance is not claimed by fixtures.

The [official cloud guide](https://docs.ollama.com/cloud) distinguishes direct key names from server cloud aliases, and [authentication](https://docs.ollama.com/api/authentication) leaves server sign-in with Ollama. [Cloud structured outputs are unsupported](https://docs.ollama.com/capabilities/structured-outputs). Direct Chat Completions therefore omits `response_format`, and both routes validate prompted output locally before saving answers/cards. No tools, retries or model/account fallback are granted. Malformed/incomplete use keeps the existing hold.

Usage is subscription quota, not an app-invented API dollar price or an asserted free service. Finite time/token/request/task allowances and reported-token reconciliation remain mandatory. Both direct and server cloud routes are labelled Ollama Cloud in usage/failure inspection. Source/model updates use the existing manual reviewed-provider update path. Fixtures verify the supported transports and consumers; all real inference remains disabled.

Unmetered-provider rejection or uncertain usage pauses the shared account across profiles, invalidates existing Chat permissions and revokes task credential grants. Model refresh does not resume it. Explicit protected unlock clears the account pause but requires fresh permissions; uncertain request records and encrypted credentials remain intact.
