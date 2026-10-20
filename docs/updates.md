# Development update verification

P4 begins with a **read-only engineering verifier**. Automatic updates and
installation remain disabled. There is no public signing key, release download,
bootstrap trust, key-rotation mechanism or updater service. Existing installation
commands still refuse in-place binary replacement. Nothing here enables wallets.

The core exposes its compiled format/feature contract:

```sh
fased-app updates compatibility
fased-signer fixture-compatibility
```

These commands need no instance, wallet, password, service or network. Protocol
v1 by itself cannot distinguish the earlier signer from approval-aware replay.
The contracts explicitly include core schema 2, vault format 1, both journal
formats at version 1, and the operations journal's `approvals-v1` feature. The
core requires the signer's independent approval-receipt capability. Their format
constants are shared with the actual store/vault/journal readers and writers.

## Inspect a disposable development proposal

```sh
fased-app updates inspect --development \
  --manifest /absolute/test/manifest.json \
  --trust-file /absolute/test/development-trust.json \
  --inventory /absolute/test/disposable-inventory.json \
  --artifact-dir /absolute/test/binaries
```

All inputs are local. The verifier neither downloads nor executes artifacts.
It returns a JSON plan only after authentication, compatibility and complete byte
verification succeed. Failure returns a nonzero exit with no plan on stdout.
The plan always reports `installAllowed`, `resumeAllowed`, `updaterEnabled`,
`walletSigner` and `financialExecution` as false. Compatibility is a necessary
input to future update coordination, not permission to replace files.

The ordinary-user test runner creates a fresh signing key **in memory only**,
signs metadata for the two actual compiled test binaries, exercises acceptance
and rejection paths, and removes its generated public fixtures:

```sh
# Build the core's embedded UI first if no compatible build exists.
npm run build
npm run build:core
go build -trimpath -o .evidence/bin/fased-signer ./cmd/fased-signer
node scripts/check-update-manifest.mjs
```

Results and input binary hashes remain in `.evidence/p4-updates`. Never use a real
wallet or production release key. `FASED_UPDATE_CORE` and `FASED_UPDATE_SIGNER`
may select a compatible pair in the same artifact directory, avoiding overwriting
retained engineering builds. `FASED_UPDATE_EVIDENCE` selects a phase-owned result
directory; use it when preserving earlier phase receipts. The Ubuntu CI signer job runs this same journey as
an ordinary user; its privileged P3 acceptance remains a separate evidence class.

## Version 1 wire contract

All documents are bounded to 64 KiB. Unknown fields, duplicate names (including
escaped aliases), case aliases, trailing values and excessive nesting fail.
Field names are exact. Schemas and protocols are positive bounded integers;
release sequence and floor are unsigned decimal **strings**. SHA-256 values and
key IDs are 64 lowercase hex characters; keys and signatures use JSON base64.

The envelope contains `version: 1`, `keyId`, `payload` (a JSON object) and
`signature`. Ed25519 signs the UTF-8 bytes of
`fased-app-update-development:v1` followed by one NUL byte followed by the **exact
payload bytes**. Do not reformat the payload after signing. `keyId` is SHA-256
of the 32-byte raw Ed25519 public key. The plan's manifest digest is SHA-256 of
those same payload bytes. Signing has no production CLI or stored private key.

The explicitly supplied trust document has `version: 1`,
`purpose: "fased-app-update-development:v1"` and 1–8 unique `keys`, each with
`id`, `publicKey`, `notBefore` and `notAfter` (Unix seconds). The selected key
must be valid now and for the manifest's entire lifetime. There is no built-in
trust, test-key fallback or network key discovery. This development mechanism
does **not** establish protected publisher identity or authorize public releases.

The payload contains `version: 1`, `product: "fased-app"`,
`channel: "development"`, `sequence`, `issuedAt`, `expiresAt`, `target`
(`os`, `arch`), and exactly two `components`: core and signer. Validity is at
most seven days; future, expired or untrusted signatures fail. Initial target
identities are Linux amd64, macOS arm64 and Windows amd64. This is binary-format
checking, not signed/native-package acceptance or proof of native custody.

Each component carries `version`, an `artifact` (`file`, `size`, `sha256`) and a
`contract`, as returned by the compiled compatibility command. Artifact names
are bounded single names with no traversal, separator, drive or case collision.
Each file must be regular, nonsymlink, present, complete, at most 256 MiB and
match its signed length/hash. Files are streamed, not loaded into memory. The
signature authorizes **declared** contracts; the verifier does not execute an
artifact to prove what it implements. Later installation must verify staged
bytes again and check actual runtime health/contracts.

Contracts contain `version`, `component`, `protocol`, `peerProtocol` (`min`,
`max`), `features`, `requiresPeer`, and `state` entries (`name`, `versions`,
`features`). Both peer protocols/features must match. Every owned state format
must be covered, with no duplicate or unknown state names. An older signer that
cannot replay approvals fails even when its protocol is still 1.

The disposable inventory contains `version: 1`, `fixtureOnly: true`, `target`,
`sequenceFloor`, `states`, `pending`, `unknown` and `recoveryHold`. Each state
has `name`, `version`, `features`, `anchor` (a SHA-256 checkpoint identity).
All four are required: `core-db`, `vault`, `operations`, `mock-receipts`. Core
accounts/sessions/setup and signer epochs/policy/reservations/originals/approvals
must remain understood; missing feature declarations cannot erase those needs.
Future formats or features unsupported by the proposed pair fail closed.

A sequence must exceed the supplied floor, including on repeated inspection
after a simulated acceptance. The checker **does not persist or authenticate
that floor/inventory**: both are disposable test inputs, labelled
`inventoryAuthority: "disposable-input-only"` in the plan. A malicious/stale
inventory could lie about history or the floor. Before installation is possible,
the trusted lifecycle boundary must independently acquire and bind inventory,
state anchors, clock/freshness and durable anti-rollback state to an exclusive
transaction. This checker is not that boundary.

The plan copies all state anchors, pending/unknown counts and recovery holds.
It requires pause/reconciliation when operations exist and preserves recovery
holds. It cannot turn key-only backup into authoritative history. It cannot
clear holds, reissue unknown originals or restore old budgets/counters.

## Protected disposable update intents

The next engineering layer obtains actual service snapshots and records an
exclusive intent in the signer's existing journal. It adds no service, socket,
installed updater, private signing key or real-wallet capability.

After the managed fixture has an owner account/CLI login and a generated wallet:

```sh
fased-app custody updates inventory --disposable-fixture --prepare-host
fased-app custody updates prepare --disposable-fixture --prepare-host \
  --intent-id <32-lowercase-hex-id> --manifest /owner/development-manifest.json \
  --trust-file /owner/development-trust.json --artifact-dir /owner/test-binaries
fased-app custody updates abort --disposable-fixture --prepare-host \
  --intent-id <same-id>
```

These delegate to the immutable installed owner helper. It verifies the managed
inventory, root-owned code/configuration and actual owner UID. The fixed trusted
OS identity helper executes the immutable core client **as app UID** over private
IPC with the existing owner session. Project code never runs as root. No new
financial inventory-file input is accepted by this path. `--prepare-host` here
authorizes that fixed identity switch; it does not authorize an installation.

The authenticated core endpoint `/api/v1/updates/inventory?probe=<32-hex>` reads
one SQLite transaction: schema, instance, setup/account documents and revisions,
and credential identity are bound into a hash. It emits no credential/session
material. Ephemeral sessions and claim codes are excluded, so login does not
invalidate the durable metadata anchor. A fresh boot ID and owner challenge bind
each observation. `update-inventory --data-dir ... --probe-id ...` is the same
private CLI read. Core metadata is app-owned; it cannot declare financial state
or relax signer limits, and it is not a complete portable database backup.

The signer's owner-only IPC computes vault/operations/mock-receipt anchors after
checking actual disk bytes/heads against its running state. It binds current
authority/epoch, boot/challenge, spent/held units, pending/unknown counts,
recovery hold, reserved sequence floor and active intent. App identity cannot
read these owner inventory/intent RPCs or prepare/abort an intent. Public status
reports `updateHeld`; the core's existing signer bridge stays read-only.

The owner helper verifies the signed manifest and complete artifact bytes before
review, displays all component identities/digests and current state/limits, and
requires `PREPARE <full review digest>` in the real terminal. It rechecks core
metadata after review. The signer separately authenticates the development
manifest, compares its **own** fresh snapshot, and atomically appends one intent
while increasing its sequence floor. Manifest plus trust input is limited to
16 KiB for this RPC. A wallet credential is unnecessary: this operation locks
the fixture and reserves lifecycle metadata; it never decrypts or uses a key.
`--approval-stdin` is explicit disposable-fixture automation only; Ubuntu
acceptance uses actual owner PTYs for approval and cancellation.

An active intent prevents unlock and durable financial changes, including new
reservations, execution, grants/revocation and reconciliation. Concurrent intents
fail; an exact request retry returns its existing receipt without a second
append. Reusing an ID with changed bindings fails. Restart preserves the slot,
floor, original operations and wallet lock. Uncertain writes require restart;
damaged history is preserved and refused, never truncated or reset.

`ABORT <id> <review digest>` releases only the update slot. It does not rewind
the reserved floor, modify financial state, clear a recovery hold or unlock the
wallet. A later new intent needs a higher sequence. This floor records reserved
**intent sequences**, not successfully installed releases: there is no install
or complete transition. Key-only recovery cannot establish missing sequence
history and remains barred from preparation. Hash chains do not protect against
a malicious signer/root or restoration of an entire old disk; portable
authoritative recovery and production anti-rollback remain later gates.

Core metadata can still change after the final read. This is an engineering
intent boundary, not an atomic installation snapshot or staged backup. Future
installation must pause/revalidate core state, stage/reverify bytes, establish
public/bootstrap trust and complete the recovery/health gates. All installation,
resume, automatic-update and real-wallet flags remain false.

Focused checks use `go test -race ./internal/update ./internal/core
./internal/signer ./cmd/fased-app ./cmd/fased-signer ./internal/custodyinstall`.
The compiled local core journey is `node scripts/check-update-state.mjs` with
`FASED_UPDATE_CORE` selecting a compatible engineering binary. Actual managed
identity/terminal acceptance runs only in disposable Ubuntu CI.

## Remaining P4 work

The complete installation controller, paused/revalidated core inventory and
installed-sequence commit, recoverable staging/backup, signed package/bootstrap trust, migrations,
process interruption/disk-full handling, coherent service updates, rollback
against current durable state, UI/timer integration and cross-platform installed
acceptance are later P4 packages. Public-path acceptance requires exact published
artifacts and separate release authority. Until those gates pass, a valid plan
always ends at inspection.
