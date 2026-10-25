# OpenAI development connection

This build accepts **disposable `fased-test-` keys only**, never real OpenAI credentials. Normal and managed cores cannot execute provider calls. A standalone `serve --ai-fixture` core can authenticate a bound test connection to its compiled in-process fixture. Tests inject a trusted loopback HTTPS Responses endpoint; no endpoint/CA override, real model call, subscription, wallet or public-update authority is exposed in the application.

In Settings → OpenAI connection → Connection settings (also available in Agents → Settings), save a disposable test key with a separate vault passphrase of at least 12 characters. Both fields clear on submission. The core returns only a random connection reference, model/capability information and lock state. This is the only credential adapter currently available; ChatGPT subscriptions, Anthropic and xAI sign-in are not connected.

In Chat, select a conversation or choose **Start chat**. Select **Local fixture** (no credential) or the unlocked **OpenAI fixture**, then the fixture model or supported admission snapshot. Type into the single composer and choose **Send** (Enter; Shift+Enter inserts a line). Send prepares one reply; it does not dispatch. Inspect **Exact context disclosure**, check the consent box and choose **Run development fixture**. **Stop** cancels a prepared, queued or running request using the same durable cancellation/hold rules as the CLI. This adapter returns one completed response; token streaming is not simulated.

Connection/model controls stay beside the composer. **Providers** opens connection management; **Message options** holds memory/source/limit controls and the optional **Save message** local-only action. Completed replies retain **Remember this**, **Use as draft** and reviewed proposal controls. A selected locked, replaced or missing connection preserves the question and requires an explicit correction; it never switches credentials or falls back automatically. Connection changes and profile preferences do not authorize requests or erase history. Restart locks the vault and preserves conversations/results.

The existing private core owns `ai-credentials.json`: Argon2id (64 MiB, three iterations, parallelism two) derives an AES-256-GCM key from the vault passphrase and a random salt. Authentication binds the encrypted API key to the instance and random connection reference. The private data directory/file protections apply (0700/0600 on Unix, current-user/SYSTEM DACL on Windows). No unlock password or encryption master key is stored next to the ciphertext. The unlocked credential stays in core memory for at most 15 minutes, and lock, logout, restart or ciphertext changes invalidate it. These controls protect stored data; a compromised owner account/core can access an unlocked credential. Go/browser memory cannot promise perfect zeroization.

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

## Pinned OpenAI admission

In Chat or Scheduled AI steps, explicitly choose the unlocked test connection and
`gpt-4.1-mini-2025-04-14` in **AI model**. The existing fixture model remains the
default. A profile's saved provider/model preference never selects or enables an
API call. CLI `ai-prepare` and `task-ai-prepare` accept the same `model` and
`connectionRef` fields through the existing `agents --request-stdin` path.
Aliases, unknown snapshots and missing/locked connections are refused.

The compiled `openai-text-standard-2026-10-21-v1` policy was reviewed on
2026-10-21 and expires on 2026-11-20 (UTC). It admits the pinned text/Structured
Outputs Responses contract, with a 1,047,576-token context and 32,768-token model
output limit. Fased's tighter owner limits remain 1,000–32,000 input tokens,
128–1,024 output tokens and a 1–5 second total deadline. No reasoning-specific
options, tools, images, files, prior-response state or alternate service tiers
are enabled. These capabilities come from the official
[model page](https://developers.openai.com/api/docs/models/gpt-4.1-mini).

The [published standard tariff](https://developers.openai.com/api/docs/pricing)
is USD $0.40 input, $0.10 cached input and $1.60 output per million tokens. The
policy stores integer micro USD per million tokens. Reserve the uncached
maximum, then round the combined per-request cost upward to a whole micro USD;
24,000 input plus 1,024 output tokens reserves 11,239 micro USD ($0.011239).
Verified reported cache tokens use the cached rate; absent cache details use the
uncached ceiling. Accounting records token counts and the cost basis. These are
public-tariff estimates, not an invoice, account-specific price or tax guarantee.
Tariff/model changes require a new reviewed build and fresh consent; expiry or
mismatch stops dependent work, with no remote tariff fetch or silent fallback.

Before any dispatch, review the exact context, limits, policy and consent digest.
Consent also covers preflight disclosure: model visibility uses
[GET /v1/models/{snapshot}](https://developers.openai.com/api/reference/resources/models/methods/retrieve),
then [POST /v1/responses/input_tokens](https://developers.openai.com/api/reference/resources/responses/subresources/input_tokens/methods/count)
receives the same model, instructions, input, output schema, empty tools and
truncation setting as response creation. Fased strips only fields unsupported by
counting: store, background, service tier and maximum output tokens. Actual token
admission uses the count response, never a byte-count approximation.

Every evaluation checks again with its bound credential reference. Model
visibility is not proof of billing credit, quota or response-create permission;
response creation can still be denied. A denied, malformed, oversized, ambiguous,
expired or unavailable preflight records a failed request and holds dependent
work without creating a response, retrying or stopping independent reminders.
Access/token evidence retains timestamps and hashes of the model record, exact
count payload/response and generation wire. After generation is attempted,
missing/mismatched model, tier or usage retains the reservation as unknown.
Reported input must equal the admitted count. Unverified or interrupted creation
is never replayed, including after update-pause recovery in the same process.

**All three API operations remain fixture-only.** The standalone compiled fixture
returns a simulated count of 256 tokens; it is not a tokenizer or account-access
proof. Controlled HTTPS tests inject trusted loopback endpoints and test keys.
There is no runtime endpoint, CA or real-key override. Normal/managed provider
execution, real credentials, wallets and automatic/public updates stay disabled.
Schema 9 plus `agents-ai-admission-v1` protects policy/evidence fields from older
writers. Paused schema-8 state must finish recovery before migration. Legacy
fixture requests/plans without a policy preserve their existing disclosure/digest
format; newly reviewed snapshot plans bind their exact policy.

This completes model/token/tariff/access admission implementation and controlled
acceptance. Actual account access, billing and live response acceptance require
separate authorization and remain an explicit external boundary.
