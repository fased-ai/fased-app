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
Backups contain no grants, counters or transaction journal. This phase offers no
API to clear the hold or authorize spending. Reconciliation, revocation, bounded
permissions and the original-before-send journal remain subsequent P3 work; a
restored key must never regain an old spending allowance from a snapshot.

Creation uses a private, fsynced pending file and exclusive publication. Restart
recovers the same encrypted identity after an interrupted publication. Conflicting
or damaged state is preserved and rejected. Existing state is never overwritten.

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

The fixture helper accepts `owner --action status|create|unlock|lock|backup|restore`
and advanced `export-fixture-seed`; the app channel accepts `app --action status`.
Every invocation requires `--config` and `--disposable-fixture`. Passwords are
requested in the terminal, confirmed when creating one, and never accepted as
arguments. `--password-stdin` is for scripted fixture checks only. Backup/export
requires `--file` inside an existing private owner directory and refuses to
overwrite any file; it never prints secret documents. Restore reads an encrypted
backup from the selected private owner file and asks for a new unlock password.

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
