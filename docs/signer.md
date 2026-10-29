# P3 signer identity and recovery

This is an engineering fixture for disposable keys. Explicit Ubuntu enrollment
can move an existing installed instance into the isolated app/signer topology.
It has no transaction adapter and cannot sign transactions or arbitrary messages. Never fund its public keys. P2's installed app still reports wallet
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
the app and signer under installer-created locked system accounts. Standalone
`custody install` creates a separate instance; `custody enroll` preserves the
existing instance identity and app data through a protected cutover. Root-owned binaries/configuration and
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

## Network-bound disposable custody

The same signer and protected owner helper also accept an explicit engineering
configuration with `version: 2`, `mode: "disposable-custody:v2"` and `network`
set to `devnet` or `mainnet-beta`. This is a disposable-key test path, not live
wallet admission. `custody install --network devnet` (or `mainnet-beta`)
with the existing `--disposable-fixture --prepare-host --signer-binary` options
creates this isolated configuration. Omitting `--network` retains v1. The network
is immutable in the installation intent, manifest and retry; existing
installations cannot be retargeted or silently converted.
Never fund these keys or import a real wallet.

Version 2 binds the network into the encrypted vault, backup, key identity and
local wire protocol. Setup/import, bounded manual unlock, verified encrypted
backup and recovery use the existing owner helper and three-UID isolation.
Recovery keeps the public key/wallet ID, creates a new authority epoch and
stays locked with a recovery hold. Raw fixture export, fixture grants and
all signing/financial mutations refuse this mode. Owner-only update inventory
and cold inspection report v2 vault bytes, key history and the recovery hold.
Explicitly reviewed development updates use the existing prepare/stage/pause/
backup/install/complete controller with `custody-compatibility` contracts.
Maintenance records append only to the existing key-history journal; they never
create fixture grants or mock receipts. Release floors survive abort, rollback,
restart and key recovery. Binary replacement never rewrites encrypted wallet
bytes, clears the recovery hold or unlocks a key.

Both installed and candidate binaries must advertise the same managed maintenance
format. Earlier v2 readers reject extended history, and incompatible rollback
candidates are refused before replacement. Reader bootstrap remains v1-only;
this does not silently upgrade an existing pre-maintenance v2 installation.
All candidates/trust used for acceptance are disposable and unpublished. Its key-history
record cannot contain fixture policies, grants, transactions or mock outcomes.
Missing/conflicting history and interrupted publication preserve originals;
ordinary and predecessor readers refuse the new format.

Core schema 22 preserves version-3 public account records for these version-2
signer references. UI and CLI share that record and the existing RPC workflow.
A recovered epoch makes old RPC credentials stale, including during an in-flight
check. No profile, app record or RPC credential grants signer authority.
Provider credential admission and real RPC dispatch remain disabled; controlled
tests alone admit disposable provider strings and HTTPS fixtures.

The ordinary update contract still covers version-1 signer custody only. The
explicit managed engineering installer and recovery-aware v2 maintenance path
remain disposable-only. Same-instance Ubuntu enrollment is implemented below;
privileged Ubuntu, native, real-host and real-wallet acceptance remain open.
Do not roll out this mode as an accepted customer installation.

## Wallets setup

**Wallets → Add wallet** provides **Watch**, **Create** and **Import**. Select
Devnet or Mainnet and a name. Watch accepts a canonical Solana public address;
only public metadata is saved. Existing network account versions and encrypted
vault formats are unchanged. Opening setup or refreshing status creates nothing.

Create and Import use the existing protected instance-terminal owner helper.
The UI displays its command and public status, never a seed/private-key/unlock
password field. Import takes a private **raw 32-byte disposable seed file**;
a JSON array or 64-byte keypair file is not this engineering import format.
Setup/import derive the immutable installed network; `--network` belongs only
to custody installation. A missing/legacy/unavailable signer is a prerequisite,
not a wallet or successful connection. Native owner custody remains Linux-only.

When the signer is absent on a verified installed Ubuntu instance, Wallets shows
`custody enroll --source local|remote --expected-instance <current ID>` with the
chosen network, matching signer binary and explicit disposable/host consent.
Run it on the instance machine (SSH for Remote), then return to the same URL and
refresh. A standalone preview cannot enroll an installation. Custom Local paths
also require the original `--prefix` and `--data-dir`. Use the current compiled
engineering binary; the retained older launcher cannot gain new commands.

Enrollment stops/disables the original engine, holds its instance lock, transfers
only bounded app metadata and encrypted credentials, fences the original data and
publishes the complete staged target before activation. It preserves the original
instance ID, Local/Remote profile, listener/private origin, records/drafts, vault
bindings, usage and recovery holds. No signer key is transferred; an existing
foreign or funded target is not adopted. Owner/app/signer UIDs remain distinct.
Original installation files and archived data remain retained. Completed owner-bound
host-preparation records stay in that retained source; they are not transferred
to the app UID. Enrollment holds their existing lock and refuses unfinished,
foreign or replaced preparation state.

Retry interruption with the same complete enrollment command, binary pair,
source ID and network. Once fencing begins, recovery proceeds forward; never
restart or copy the old data over the target. Conflicting state or proven loss of
the source lock holds recovery without publication; preserve both copies for
inspection. Ordinary `open`, `service start|stop|status` and Remote commands route
to the committed instance and refuse pending/foreign enrollment. Protected
service changes require OS authorization. Local opens its private browser URL;
native custody acceptance remains open. The sign-in screen uses the immutable
managed core for setup-code and app-password recovery. `custody reset-app-password`
recovers app access; `custody recover --backup-file ...` remains wallet recovery. Existing Remote private-access and
firewall policy identities stay bound to the original instance; unfinished
network lockdown must be resolved before enrolling. Verified executable updates
retain enrollment identity without weakening current installed-pair verification.

After protected creation/import and verified encrypted backup, **Refresh status**
then **Attach** records the exact current version-2 signer reference for that
network. Attachment grants no signing, unlock or permission. Locked/recovery
states remain held. A recovered epoch may be explicitly attached as a new public
record while retaining the old record and its stale encrypted credential binding;
no old identity, key, hold or RPC vault is silently rewritten or removed.

**RPC** uses the saved account's network. Network accounts start with an empty
endpoint. Check and Save reuse the existing bounded endpoint/network checks;
API keys use the separate encrypted credential editor. A recovered/unavailable
signer makes its old saved credential publicly stale/locked. Public-address
formatting and an RPC health result do not establish a balance or signing right.
Ordinary constructors keep network-watch admission, provider credentials and
live RPC disabled; only controlled tests inject disposable admission/HTTPS.
No production CA/endpoint override, owner administration route or new service is
introduced. Installation, real-host and live-key acceptance remain separate.

The same owner-authenticated revision-bound operation is available through
`fased-app accounts --data-dir /private/instance --setup-stdin`:

```json
{
  "revision": 0,
  "action": "watch",
  "name": "Research",
  "network": "devnet",
  "address": "11111111111111111111111111111111"
}
```

This is synthetic public fixture data, not a funding address. Ordinary held
builds refuse its network admission. `action: "attach"` replaces `address` with
`reference`, the exact public `fixture.status.account` returned by `accounts`.
The server rechecks revision, network, current signer identity and maintenance
hold before saving. `create`, `import`, secret fields, stale references and
network retargets are refused. Legacy raw records remain inspectable in
**Advanced** and through the existing `--request-stdin` path.

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

The owner-helper/core public status view includes an optional versioned `publicIdentity`: the canonical
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

The version-1 signer IPC response is unchanged. The existing owner helper and
core decorate the authenticated public status locally; older strict IPC clients
therefore receive no new fields. No additional signer command is required.

## Disposable RPC check

Wallets → RPC and `fased-app accounts --check-rpc-stdin` share one authenticated,
CSRF-protected core check. Supply JSON on stdin:

```json
{
  "endpoint": "https://rpc.fased.invalid/solana",
  "network": "disposable-fixture:v1"
}
```

Only this exact endpoint/network pair is admitted. The runtime uses a compiled
in-process fixture, never the public network. It verifies a synthetic genesis
identity and health response with bounded time/body sizes; redirects, credentials,
other destinations, mismatched networks and malformed replies fail closed.
The result is transient and does not save an endpoint, alter accounts or authorize
signing. Controlled HTTPS tests exercise the same parser with a disposable TLS
server. This is not verification of a real Solana cluster or production RPC
security; live DNS/IP admission, credential handling and activation remain held.

## Protected disposable import

`fased-app custody import --disposable-fixture --seed-file /private/seed.fixture --backup-file /private/backup.json`

Use an unfunded disposable 32-byte Ed25519 seed file, owned by the ordinary owner,
mode 0600 in a private directory. No mnemonic, expanded private-key JSON, key
argument, environment variable or UI upload is accepted. The immutable owner
helper reads the seed directly; the app backend never handles it. Passwords use
the owner terminal (private stdin is available only for fixture automation).

Import requires an empty signer and a new backup path. It assigns fresh wallet
and epoch identifiers, retains a recovery hold, encrypts and verifies the backup,
and finishes locked. It neither adopts old permissions nor enables signing.
An interrupted import preserves the new identity: use `custody setup` with the
same backup path and current password to finish backup verification. Never retry
by deleting signer state. Existing input/backup files are not overwritten or removed.
Real keys, funds, signing and mining remain disabled.

## Wallet-bound RPC checks

Choose RPC on a saved Wallets row. The same CLI check accepts `accountId` and
`accountRevision` alongside the disposable endpoint/network. These two fields
must be supplied together. The core reads the saved network and public identity;
managed records must match the signer's current reference. It checks again after
the diagnostic, refusing deleted/changed records, changed epochs and unavailable
signers. The returned account/revision/state is a historical diagnostic, never a
balance, persistent RPC configuration or signing permission. Locked/held wallets
can be diagnosed without unlocking or removing their hold. Standalone CLI checks
remain compatible and return no wallet binding. No account data is written.

## Persistent fixture RPC settings

Wallets → RPC → Save checks and stores the selected disposable endpoint/network
against the saved wallet revision. Remove clears only the RPC preference. The
same operations use `accounts --rpc-stdin`:

```json
{
  "accountId": "<saved-account-id>",
  "revision": 1,
  "endpoint": "https://rpc.fased.invalid/solana",
  "network": "disposable-fixture:v1"
}
```

To remove, supply only `accountId`, `revision` and `"remove":true`. To check the
saved settings, `accounts --check-rpc-stdin` accepts `accountId` and
`accountRevision` without an endpoint/network. Inspect `accounts` for the latest
revision; stale writes fail without retry. Ordinary rename edits preserve the
configuration, including clients that omit its optional field. Rebinding a key,
mode, network or signer epoch clears it; saving again needs fresh validation.
Removal remains available for stale/offline signer records. Saving never clears
locks or recovery holds, grants authority, or stores a lasting verification.

Core schema 20 protects these public preferences from older readers. Migration
preserves account bytes/revisions and refuses paused recovery or RPC data already
present in an unprotected schema. Existing snapshot/backup/pause mechanisms cover
the settings. Only the compiled fixture URL is accepted; API keys, authenticated
URLs, live transports and real RPC credential storage remain unavailable.

### RPC test credentials

Wallets → RPC → API key and `accounts --rpc-credential-stdin` use the same
owner-authenticated core operation. Save RPC settings first. Input is private
JSON with `accountId`, current `revision`, current credential `ref` (empty for
first save), `action` (`status`, `save`, `verify`, `remove`), and, except status,
`passphrase`. Only save accepts `key`, with the deliberately nonproduction
`fased-rpc-test-` prefix (30–256 bytes). Never put secrets in command arguments.

Each account has a private encrypted vault outside SQLite and metadata backups.
AES-GCM and the existing password KDF bind ciphertext to the instance, account,
RPC configuration and fresh credential reference. Replacement/removal require
the existing vault password; stale references fail. Verify checks local decryption
only: it never proves provider acceptance, sends credentials, or leaves a key
unlocked. No password/key is returned. Restart has no retained plaintext state.

Keep vault files with the original instance when backing up; metadata-only
recovery does not include them. Lost vault passwords cannot be recovered by
resetting the app password. Malformed vaults are preserved rather than replaced.
Identity/configuration changes make the saved binding stale; explicit replacement
requires its password. Removed wallet records do not silently delete vault files.
This is disposable credential storage acceptance, not live RPC integration or
production credential recovery acceptance. The Go signer remains unchanged.

### One-operation authenticated RPC check

`accounts --rpc-credential-stdin` also accepts `action: "check"`, with the saved
account revision, credential reference and vault passphrase; no `key` or endpoint
override is accepted. Wallets → RPC → API key → Check connection uses that same
operation. The result is transient and labelled demo, never persisted readiness.

The existing SQLite write fence prevents concurrent account/credential edits
through the core while the operation runs (five-second context, three-second HTTP
budget). Each of the two fixed genesis/health requests and final completion
rechecks the credential bytes and current managed signer reference. Plaintext is
cleared after the operation; no unlock session, retries or unattended grant exists.

Ordinary builds use an inert authenticated responder. Go tests may inject a
controlled HTTPS adapter that maps only the compiled fixture origin to a literal
loopback IP with explicit certificate roots. No proxy, redirect, cookie, runtime
origin/CA override or live transport is exposed. Credentials appear only in the
Authorization header, never in URLs, public results or errors. Responses remain
bounded and must match the fixture network and healthy status. This proves the
authenticated transport contract, not real provider acceptance or live connectivity.

### Encrypted RPC credential recovery

Export through the owner CLI into a new private file. Use
`accounts --rpc-credential-stdin --rpc-backup-out /private/path/rpc.json` with
`action: "backup"`, current `accountId`, `revision`, `ref` and `passphrase` on
private stdin. The command prints status only. It refuses existing destinations
and uses a private, synced temporary file with exclusive publication. The backup
contains ciphertext and original instance/account bindings, never plaintext keys.
Do not store it beside its password. Keep an off-device copy; a same-disk file
alone is not disaster recovery.

Wallets → RPC → API key → Recovery accepts that encrypted file for Verify backup
or Restore. The same `verify-backup`/`restore` actions accept a `backup` object
through private CLI stdin. Verification checks decryption and exact current
instance/account/RPC binding without changing state. Restore only fills a missing
vault, requires the backup password, and re-encrypts with a new credential reference.
It never overwrites an existing/corrupt vault or changes signer state. Preserve a
corrupt file before owner-assisted recovery. Changed identity/settings, another
instance, stale revisions and update pause refuse restoration. Recovery stays
locked and needs a new explicit connection check.

This supports recovery of a lost RPC credential file alongside the original
instance metadata. It does not recover a forgotten password, migrate credentials
to a different instance, undo provider revocation or establish real-key acceptance.
Browser export is intentionally absent: private-file export remains in the CLI;
the browser can inspect/restore an explicitly selected encrypted backup.

### Dormant public RPC admission

The existing read-only checker now contains a disabled HTTPS path for Solana
`mainnet-beta` and `devnet`. It shares the fixture check's bounded
`getGenesisHash` → `getHealth` protocol. The canonical genesis values are pinned
from [Solana validator cluster documentation](https://edge.docs.solanalabs.com/de/clusters/available);
[getGenesisHash](https://solana.com/docs/rpc/http/getgenesishash) reports the
connected cluster, not endpoint honesty or transaction finality.

Admission accepts only HTTPS DNS origins on the default port, with an optional
root slash. Credentials, other paths, queries, fragments, IP literals and local
names are refused. Every connection resolves once, rejects the entire answer set
if any address is non-public, and dials a checked IP while TLS verifies the
original hostname. Proxies, redirects and persistent connections are disabled.
Responses and elapsed time retain the existing bounds; errors do not echo URLs
or server bodies.

Ordinary constructors keep dispatch closed before DNS. No environment, UI or CLI
switch enables it. Tests alone inject a loopback TLS destination and trusted test
certificate into the actual transport. Existing saved wallet settings, credentials
and signer authority remain disposable-only and unchanged. This implements
unauthenticated origin/network checking; provider-specific Helius/Alchemy secret
routing, encrypted production credential admission, supported-network wallet
binding and separately authorized live acceptance remain unfinished. A root-only
URL policy must not be presented as support for those providers' authenticated
URLs. No new schema or automatic migration is introduced.

### Dormant authenticated provider settings

Schema 21 protects version-2 read-only (`watch`) public accounts and RPC settings.
Only mainnet-beta/devnet and the compiled Helius/Alchemy origins are admitted.
No signer reference, managed key or transaction permission is added. Public account
metadata stores the origin and network, never an API key. Saving settings does not
claim that the provider is reachable: the separate credential check performs
`getGenesisHash` and `getHealth`.

Version-2 RPC vaults bind the encrypted key to the original instance, account,
network, provider settings and fresh reference. Existing version-1 fixture files
are unchanged. Explicit rotation, locked restart, encrypted backup verification,
missing-file restore and password-confirmed removal reuse the existing command.
Backups cannot overwrite a vault or migrate an identity. Inactive provider state
can still be inspected, backed up and removed; opening a connection, saving a new
key and restoring require admission. There is no unattended unlock.

The transport inserts the key only for the outgoing request: Helius uses the
[`api-key` query](https://www.helius.dev/docs/api-reference/rpc/http/getgenesishash),
Alchemy uses the [`/v2/{apiKey}` path](https://www.alchemy.com/docs/chains/solana/solana-api-endpoints/get-identity).
Hosts are selected from the provider/network mapping, not supplied alongside the
secret. Public-IP pinning, TLS, bounded responses, cancellation, no redirects and
per-operation stale-reference checks remain enforced. URL-bearing transport errors
and response request metadata are sanitized before returning to the core.

Both account/credential admission and network dispatch remain disabled in ordinary
constructors. Only tests enable the private gates with disposable inputs and
loopback TLS. There is no UI/CLI/environment override. Existing Wallets and CLI
fixture flows remain unchanged; no new provider controls are exposed until live
onboarding is separately authorized. Production provider/account acceptance and
managed signer network/custody integration remain open. Schema-20 binaries refuse
schema-21 databases before access; schema migration preserves prior account and
credential bytes and refuses paused or prematurely upgraded records.
