# P3 signer identity and recovery

This is an engineering fixture for disposable keys. It is separate from the
installed app, has no chain/RPC adapter, and cannot sign transactions or arbitrary
messages. Never fund its public keys. P2's installed app still reports wallet
signing and financial execution unavailable. Ordinary packages never install a
signer automatically. The explicit Ubuntu fixture installer below manages both
services and their distinct identities.

The Linux boundary has three different non-root UIDs: owner, application, and
signer. The owner invokes a protected local helper (over SSH on a VPS). The
application cannot administer the signer. A root-owned immutable configuration
binds those identities, the signer ID and its directories; both helper and server
verify it. The server verifies kernel peer credentials on separate owner and app
sockets, and the client verifies the signer UID before sending secrets. The
signer directory is private to its UID. Root/host administrator compromise is
outside this isolation boundary.

P2's ordinary desktop core runs as the owner. The separate managed fixture runs
the app and signer under installer-created locked system accounts. It uses a new
instance and never migrates P2/legacy data. Root-owned binaries/configuration and
systemd sandboxing protect both services; the core checks its actual UID,
NoNewPrivs, read-only installation and writable private state before opening its
database or listener. The browser must never receive private keys or unlock
credentials through the app.
The read-only check uses a disposable, non-policy marker owned/writable by the
app UID and still requires kernel EROFS; root-owned code/configuration is never
made writable by the app. Permission denial on root's files is not mount proof.
Windows and macOS remain unavailable until their own identity/IPC proof passes.

The fixture uses Go's Ed25519 key generation, AES-256-GCM and the already pinned
`golang.org/x/crypto/argon2` Argon2id implementation (64 MiB, three iterations,
parallelism two). No source from legacy Fased or a wallet service is copied; no
new dependency is introduced. Go's standard-library license and x/crypto's BSD
license remain applicable. Version, purpose, public identity and recovery state
are authenticated with the encrypted seed. Unknown formats fail closed.

The CI fixture provisions the identities and immutable files through trusted OS
utilities, then drops UID, supplementary groups and capabilities before starting
any Fased executable. The daemon disables dumpability and gaining privileges.
Socket connect permission alone does not authorize a request: Linux
`SO_PEERCRED` must match the channel's configured UID before a handshake is sent.
Each connection gets a fresh signer-bound challenge and permits one bounded
request; the two channels have separate connection limits. There is no TCP
listener. Local clients also verify the server's kernel UID before sending a
credential. This is process/OS isolation, not an enclave or protection from root.

Manual unlock has a bounded lifetime (at most one hour), clears on lock/restart
and never saves a restart credential. Memory erasure is best effort in Go, not a
hardware guarantee. App-password recovery cannot recover a wallet password.
Backups use a separate owner-selected password and fresh encryption randomness.
The owner chooses the output destination; nothing is uploaded. A backup on the
same disk is not protection against loss of that disk.

Restore requires empty signer state, preserves the key/public identity, creates
a fresh authority epoch, and leaves the account locked with a recovery hold.
Backups contain no grants, counters or operation journal. Key-only recovery
stays held. With complete retained journals, the owner can explicitly reconcile
the mock outcomes and rebind fixture authority; lifetime spending survives and
all old grants are revoked. New bounded owner policy/grants are then required.
This is a fixture recovery proof, not permission to clear real on-chain holds.

Creation uses a private, fsynced pending file and exclusive publication. Restart
recovers the same encrypted identity after an interrupted publication, including
interruption while initializing its journals. The encrypted original precedes
journal initialization; only that pending original can resume initialization.
Missing history after publication fails closed. Conflicting or damaged state is
preserved and rejected. Existing state is never overwritten. Earlier engineering
vaults without these journals are not automatically migrated or enabled.

Local package tests use disposable, reconstructible, nonauthoritative fixtures
under `.evidence/p3-signer`. They establish cryptographic/storage/protocol behavior,
not separate installed UIDs. The Ubuntu CI boundary fixture runs the server and
clients as three different ordinary users, and tests the actual kernel denials.
Neither test class enables a wallet or establishes retail custody acceptance.

## Engineering commands

Run the locally executable checks from the source checkout:

```sh
go test -race ./internal/signer ./cmd/fased-signer
```

The identity check is `node scripts/check-signer-isolation.mjs`; actual managed
installation is tested by `node scripts/check-managed-custody.mjs` in disposable
Ubuntu CI. Both refuse ordinary local execution. No local owner account, service,
sudo policy or default instance is modified by the unit tests.

The fixture helper accepts `owner --action status|create|unlock|lock|backup|verify-backup|restore`
and advanced `export-fixture-seed`; the app can read `status` and request only the
typed fixture operations described below.
Every invocation requires `--config` and `--disposable-fixture`. Passwords are
requested in the terminal, confirmed when creating one, and never accepted as
arguments. `--password-stdin` is for scripted fixture checks only. Backup/export
requires `--file` inside an existing private owner directory and refuses to
overwrite any file; it never prints secret documents. Restore reads an encrypted
backup from the selected private owner file and asks for a new unlock password.

## Bounded mock operations

This separate engineering interface models authorization and recovery. It has
no RPC URL, arbitrary message, transaction bytes, real asset or chain name.
Only `fixture-transfer`, `fixture-credit:v1` and the fixed destinations
`fixture:treasury` (mock acceptance) / `fixture:reject` (mock rejection) exist.
The wallet remains unable to sign real transactions. UI wallet/terminal content
is still a prototype; this interface does not connect an account to the UI.

The public `status.account` reference binds version, signer, wallet, authority
epoch, fixture network and public key. Policy, grant and intent must use the exact
reference. Amounts are unsigned integer units encoded as JSON strings, never
floats. A wallet policy caps cumulative spent plus held budget across all bots;
each immutable grant also caps one bot, asset, destinations, amount and expiry.
Grant/bot IDs are 32 lowercase hex characters. IDs and history are never reused
or pruned. A persona or an app database entry cannot grant authority. The app
UID can impersonate its own bot labels, so independent wallet totals remain the
security limit; these labels do not establish process isolation between bots.

Owner-only actions are `fixture-policy`, `fixture-grant`, `fixture-revoke` and
`fixture-recover-authority`. Policy/grant creation requires a manually unlocked
vault. Revocation can stop new execution while locked, and does not release a
pending reservation. Administration is over the authenticated owner socket;
neither UI sessions nor the app channel can perform it. The new `--request-stdin`
flag reads at most 16 KiB of public JSON with only these optional fields:
`account`, `policy`, `grant`, `intent`, `grantId`, `operationId`. Unknown fields,
credentials and non-integer amounts are rejected. It cannot be combined with
the fixture password-input flag. Use the immutable helper printed in the managed
inventory, as the owner; no shell command should impersonate a service UID.

For example, after protected setup/unlock, use the exact inventory paths and a
reviewed public policy request:

```sh
/var/lib/fased-app-custody-1000/fased-signer owner \
  --config /var/lib/fased-app-custody-1000/identity.json \
  --disposable-fixture --action fixture-policy --request-stdin < policy-request.json
```

The configured app UID can request `fixture-state`, `fixture-reserve`,
`fixture-execute`, `fixture-operation` and `fixture-reconcile`. It supplies a
typed intent for reservation and an operation ID for later steps. The signer
atomically reserves both budgets, persists the exact canonical original and
hash, persists `sending`, then calls its fixed synchronous local mock transport.
The mock writes and syncs a separate receipt before acknowledging an outcome.
Normal terminal states are `confirmed` or `rejected`. No real signature is made.

A retry with the same ID/body reads the same operation. A changed body is
rejected. Prepared/sending operations are held and never sent again. Restart
locks the vault and retains reservations. Explicit reconciliation uses only
the signer's durable mock receipts; callers cannot supply an outcome. A complete
mock ledger can prove `not-submitted` when no receipt exists because it is
synchronous, local, and has no surviving in-flight worker. **An absent real RPC
transaction is not that proof.** Future adapters must supply venue-specific
finality/reconciliation and cannot copy this absence rule.

Signer-only `operations.log` and `mock-receipts.log` use bounded, hash-chained,
fsynced append records. Torn, missing, conflicting, symlinked or hardlinked
journals are preserved/refused; an uncertain write blocks further operations
until restart. There is no automatic truncation, allowance reset or history
pruning. The fixture caps history at 1024 operations, 256 grants and 8 MiB per
journal. A hash chain is not protection from signer/root compromise or complete
disk rollback. Back up the original encrypted key separately. The cold portable
archive below now preserves complete history in a private read-only quarantine;
it does not import live authority or clear a recovery hold.

Local tests terminate actual non-root child processes at reservation,
original-persistence and mock-send boundaries. They prove process crash behavior,
not physical disk/power-loss durability. Controlled Ubuntu tests also exercise
the installed service UIDs, raw app-channel administration denial, recovery,
and journal preservation across removal/reinstallation.

## Explicit managed fixture installation

Ubuntu 24.04 and an ordinary owner with OS authorization are required. These are
paired source-built engineering binaries, with no publisher authenticity claim.
This command is not a public download or a wallet-enablement command:

```sh
./fased-app custody install --signer-binary ./fased-signer \
  --disposable-fixture --prepare-host
```

One sudo authorization permits fixed OS tools to create dedicated no-login,
password-locked accounts and root-owned inventory under
`/var/lib/fased-app-custody-<owner-uid>`, then install and start two system services.
No Fased executable or setup script runs as root. Services have separate private
state, no capabilities, NoNewPrivileges, ProtectSystem=strict and explicit write
exceptions. Signer sockets support only AF_UNIX. Unknown systemd overrides and
changed units fail before activation. Autostart is off. The browser entry is
`http://127.0.0.1:7332`; use an explicit unprivileged loopback `--listen` port if
needed. No public listener or Tailscale/SSH/firewall policy is installed.

Immutable input hashes and a private durable owner intent support same-command
retry after interruption. Root staging/publication is exclusive; conflicting
accounts, directories, files and units are preserved. Installed binaries are not
updated in place. P4 must supply coordinated signed updates.

Owner administration uses these entry points, including from an owner's SSH
terminal. First create a private owner backup directory; select an off-machine
backup destination where available. No backup is uploaded automatically.

```sh
./fased-app custody claim-code --disposable-fixture --prepare-host
./fased-app custody setup --disposable-fixture --backup-file /private/owner/wallet.backup
./fased-app custody recover --disposable-fixture --backup-file /private/owner/wallet.backup
./fased-app custody reset-app-password --disposable-fixture --prepare-host
./fased-app custody status --disposable-fixture
./fased-app custody remove --disposable-fixture --prepare-host
```

The claim code establishes web app ownership and confers no wallet authority.
Setup asks for a new unlock password and a backup password, writes an exclusive
encrypted backup, verifies it against the unlocked key, then locks. Retrying an
interrupted setup uses the existing key. Recovery accepts only an empty signer
or an interrupted recovery hold; it verifies the recovered key and stays held
and locked. Existing wallets/backups are never overwritten. Helper interruption
leaves at most a 60-second manual unlock, and failure attempts an independent
bounded lock request. No restart secret is saved.

App-password recovery stops only the app, runs the existing password-reset helper
as its app UID in the owner's terminal, revokes app sessions, then restarts it.
The signer is preserved and no wallet is unlocked or recovered. Scripted
`--password-stdin` is allowed only for disposable wallet checks, never app-password
recovery. Neither administration route is an HTTP or model tool.

Removal verifies inventory and stops/removes its service definitions. It retains
all app/signer state, unknown files, immutable inventory and disabled OS accounts
to prevent UID reuse over surviving data. Reinstallation with the same inputs
restores the services with the same identity and locked/held state. There is no
erase-wallet command. Physical-host/public/native-client custody acceptance and
installer upgrades remain open; the managed engineering mode currently uses the
browser and owner CLI. Public account registration, grants/revocation, typed mock
operations and reconciliation remain P3 work. Real wallets remain disabled.

## Public accounts and protected approval

The connected Wallets page now saves versioned public records in the app's SQLite
registry. Watch-only and external/manual records have no RPC reads, balances,
connected wallet or signing capability. Only disposable fixture network/public
keys are accepted. The managed mode takes its exact public signer/wallet/epoch
reference from the configured signer. Ordinary installation has no signer and
cannot create a managed reference. Stale references remain visible and cannot
propose new authority. Removing a public record never removes a key or revokes
an existing signer grant.

The authenticated UI and `fased-app accounts` CLI use the same revision-checked
API. Read the returned `record`, edit its public fields and pass that record to
`accounts --request-stdin`; stale revisions fail without retry. Up to eight
accounts and eight proposals are retained. Names, modes and drafts never confer
signer authority. Keys, credentials, balances and asserted approval flags are
not fields in the registry. Schema 1 migrates transactionally to schema 2,
preserving setup/owner sessions. A preceding binary refuses the newer schema;
this is not a coordinated update or rollback system.

For a managed Ubuntu instance, the owner can use the fixed CLI identity bridge:

```sh
./fased-app custody login --disposable-fixture --prepare-host
./fased-app custody accounts --disposable-fixture --prepare-host
./fased-app custody accounts --disposable-fixture --prepare-host --request-stdin < public-record.json
```

The OS credential switch happens before the immutable core runs as the app UID.
This needs ordinary OS authorization, and does not let the owner read app/signer
state directly. App login protects app access; wallet secrets are separate.

In Wallets, record the disposable signer, draft explicit wallet/bot/per-operation
caps and expiry, then download the public proposal. These proposals allow only
the typed mock operation and expire within 24 hours. They are not strategy code
or real transactions. Save the proposal in an owner-controlled directory (an
ordinary Downloads directory is sufficient), then run on the instance host:

```sh
./fased-app custody approve --disposable-fixture --proposal-file /absolute/owner/path/proposal.json
```

The coordinator passes the owner's terminal directly to the root-owned signer
helper running as the owner. The helper verifies the actual signer account,
reads the public file once, displays every limit/binding plus its canonical
digest, and requires `APPROVE <digest>` in the owner's terminal before asking
for the wallet unlock password. It unlocks for at most 60 seconds and locks on
completion/failure. Cancellation grants nothing. No secret reaches the app/UI.
Public input refuses symlinks, hardlinks, foreign ownership and writable files or
ancestors; secret backup/export privacy is unchanged. Explicit approval/password
stdin flags exist only for disposable fixture automation; the ordinary flow
requires the terminal. No root project executable is used.

One signer-owned fsynced journal event atomically installs the reviewed policy
and grant and records the proposal digest. A changed-body replay fails; retrying
a revoked approval never reactivates it. The app bridge can request only public
status/receipts, not policy changes, unlock, approval or execution. Refresh
Wallets to read the independent signer receipt. `approved` records owner consent
for that exact fixture mandate; it does not mean unlocked, unexpired allowance,
live trading, unattended unlock or real wallet support. A digest mismatch,
revocation, expiry, recovery hold or unavailable signer remains visible.

## Cold encrypted history capture and quarantined recovery

P4 now implements a bounded Linux-only portable evidence archive. It contains the
exact encrypted vault, complete operation journal and complete mock receipt
journal, including originals, lifetime spending, unresolved reservations and
installed/reserved update floors. The whole archive uses a fresh salt/nonce with
Argon2id (64 MiB, three iterations, two lanes) and AES-256-GCM. The original vault
inside remains encrypted by its original unlock password. Losing that password
is not repaired by the archive password.

This is offline maintenance under the configured **signer UID**, with a root-owned
immutable executable/configuration. The source daemon must be stopped: the actual
exclusive signer lock refuses capture while it is running. The source vault password authenticates the encrypted key without unlocking the
wallet; a wrong password or damaged key refuses capture. Cold reads create or
repair nothing. Active update intents refuse capture because they require their
original controller/core recovery context. The app and owner UIDs cannot call
these commands; there is no HTTP, app/owner RPC or UI route. All keys remain
explicit disposable fixtures, and unsupported platforms refuse.

```sh
# Maintainer commands, already running under the configured non-root signer UID.
# Choose an existing private directory owned by that UID. No upload occurs.
fased-signer fixture-portable-export --disposable-fixture   --config /root-owned/identity.json --file /private/history.enc

# Keep the returned archiveSha256 independently, off the source disk.
# On recovery, the private quarantine parent exists; the destination is absent.
fased-signer fixture-portable-recover --disposable-fixture   --config /root-owned/recovery-identity.json --file /private/history.enc   --snapshot-sha256 <independently-retained-fingerprint>   --directory /private/recovered-history
```

Source-vault and archive passwords are read from a protected terminal, never command arguments or receipts.
Explicit `--password-stdin` is available only for disposable fixture automation.
Archives and recovered files are private, exclusive and never printed. Receipt
output contains the capture time, public source identity, exact file hashes and
replayed counters/floors. Corrupt/truncated/oversized input, a wrong password,
missing/substituted fingerprint, foreign state and unsupported reader contracts
refuse without modifying the destination. The fingerprint binds exact archive
bytes; it is not proof that the backup is the latest financial authority.

Recovery authenticates/decrypts the fixed archive frame and reuses the actual cold
vault/journal replay before publication. Output contains only `intent.json`,
`receipt.json`, `vault.json.snapshot`, `operations.log.snapshot` and
`mock-receipts.log.snapshot`. Those names are deliberately not live signer input.
A fresh target configuration can inspect the source history without importing
its signer ID, authority epoch, grants or permissions. Every receipt is historical,
read-only, locked and recovery-required, with install/resume/wallet/financial and
automatic-update flags false. No restored file is executed or automatically
adopted by a service.

A private pending directory and synced immutable prefix make interrupted imports
recoverable. Exact retry rehashes existing files and finishes durability;
conflicting, partial, linked or foreign files remain preserved/refused. An empty
pre-existing pending directory is also preserved: without a matching durable
intent it is not assumed to belong to this import. Final publication uses a
no-replace directory rename; completion retry remains a historical result.

This supplies off-disk **evidence preservation and inspection**, not authoritative
live financial recovery. A stale backup plus its own stale checksum cannot prove
latest spending, source retirement or venue finality. A real live recovery path
still requires independent current history/anchor and venue reconciliation,
while preserving unknown holds and requiring fresh bounded owner permissions.
Those P3/P6 dependencies keep public P4-15 BLOCKED. No mock absence proof, archive
receipt, filesystem restore or copied counter is permitted to clear that gate.
The existing key-only backup/recovery and live signer formats are unchanged.

`node scripts/check-portable-recovery.mjs` runs the local/Ubuntu race-enabled
archive, tamper, prefix, conflict and coupled-history checks. The installed Ubuntu
journey additionally exercises the immutable executable, real signer/app/owner
UIDs, a fresh empty target configuration, source-byte preservation, restart and
read-only recovery. Its evidence remains separate from public/native/VPS or
physical disk-loss acceptance.

## Recheck a saved backup

The protected owner helper supports `owner --action verify-backup --file <backup>`
with the same `--config` and `--disposable-fixture` requirements. Unlock the
fixture signer first; enter the backup password at the terminal prompt. This
checks the encrypted file against the current wallet without restoring it,
returning a seed, or changing permissions or recovery holds. A successful check
returns public status only. Wrong passwords, another wallet's backup, locked
state and application-channel requests are refused. Preserve the original backup;
verification never replaces it. This does not validate disk-loss recovery or
introduce RPC, real-wallet import, signing or network access.

## Disposable Solana public identity

Signer status now includes an optional versioned `publicIdentity`: the canonical
base58 address of the existing 32-byte Ed25519 public key, `chain: solana`,
`network: disposable-fixture:v1`, and `fixtureOnly: true`. It is available while
locked and passes through the same read-only core account view and CLI. Wallets
displays it only for a current managed fixture reference; stale/unavailable
references retain their original public key. The original hex key remains the
stored authority identity and is available in the address tooltip.

This is address formatting, not RPC verification or proof of an on-chain account.
No balance, cluster, funding, signing or Hyperliquid capability is implied. Never
fund these disposable addresses. Recovery preserves the address while changing
the authority epoch and retaining the lock/recovery hold. No vault/account schema
or key material is changed. Encoding follows the [Solana account address
format](https://solana.com/docs/core/accounts/account-structure).
