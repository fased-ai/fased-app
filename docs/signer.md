# P3 signer identity and recovery

This is an engineering fixture for disposable keys. It is separate from the
installed app, has no chain/RPC adapter, and cannot sign transactions or arbitrary
messages. Never fund its public keys. P2's installed app still reports wallet
signing and financial execution unavailable. No signer is installed automatically.

The Linux boundary has three different non-root UIDs: owner, application, and
signer. The owner invokes a protected local helper (over SSH on a VPS). The
application cannot administer the signer. A root-owned immutable configuration
binds those identities, the signer ID and its directories; both helper and server
verify it. The server verifies kernel peer credentials on separate owner and app
sockets, and the client verifies the signer UID before sending secrets. The
signer directory is private to its UID. Root/host administrator compromise is
outside this isolation boundary.

P2's current desktop core runs as the owner. That topology is **not sufficient**
for custody. A future installer must establish the separate app/signer identities
and immutable executable/configuration ownership before enabling wallets. The
browser must never receive private keys or unlock credentials through the app.
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

The separate identity check is `node scripts/check-signer-isolation.mjs` on its
disposable Ubuntu CI job. It refuses ordinary local execution. No owner should
create accounts or manually configure this fixture to use the app.

The fixture helper accepts `owner --action status|create|unlock|lock|backup|restore`
and advanced `export-fixture-seed`; the app channel accepts `app --action status`.
Every invocation requires `--config` and `--disposable-fixture`. Passwords are
requested in the terminal, confirmed when creating one, and never accepted as
arguments. `--password-stdin` is for scripted fixture checks only. Backup/export
requires `--file` inside an existing private owner directory and refuses to
overwrite any file; it never prints secret documents. Restore reads an encrypted
backup from the selected private owner file and asks for a new unlock password.

Installer-managed identities, the production owner-helper ceremony, public
account registration, grants/revocation, typed mock operations and reconciliation
are still P3 work. The engineering signer binary is deliberately excluded from
the app's package inventory and first-launch flow.
