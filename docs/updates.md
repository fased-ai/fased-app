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
retained engineering builds. The Ubuntu CI signer job runs this same journey as
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

## Remaining P4 work

The shared exclusive controller, trusted installed-state probes and sequence
commit, recoverable staging/backup, signed package/bootstrap trust, migrations,
process interruption/disk-full handling, coherent service updates, rollback
against current durable state, UI/timer integration and cross-platform installed
acceptance are later P4 packages. Public-path acceptance requires exact published
artifacts and separate release authority. Until those gates pass, a valid plan
always ends at inspection.
