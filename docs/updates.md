# Development update verification

P4 provides a **development verifier and protected disposable installation flow**.
Real wallets and automatic updates remain disabled. Explicit development-only HTTPS
acquisition is available; there is no public signing key, public release feed,
production bootstrap trust, key-rotation mechanism or updater service. Existing installation
commands still refuse in-place binary replacement. Nothing here enables wallets.

The [production release-trust operations specification](#production-release-trust-operations-specification)
below is a proposed implementation contract. It does not change those capabilities.

## Production release-trust operations specification

**P4-10/11 design proposal, revision 1, 2026-10-20.** This section specifies future
production controls; none is activated by this document. Current commands accept
development trust only. Real wallets, financial execution and automatic updates
stay disabled. No production key, credential, repository, branch protection,
publisher workflow, trust configuration or public release is created here.

### Trust architecture and scope

Use The Update Framework (TUF) for production metadata verification. Its roles
separate root trust, target authorization, repository consistency and freshness.
Root succession requires sequential versions and both old and new thresholds;
clients persist trusted metadata. TUF does not authenticate the initial installer
for us or install Fased binaries. Follow the [TUF specification](https://theupdateframework.github.io/specification/latest/),
especially its client workflow and bootstrap limitations. Review and pin the
[go-tuf/v2 implementation](https://github.com/theupdateframework/go-tuf) before
implementation; add no dependency in this documentation package. The thresholds,
durations and operational requirements below are Fased proposals, not TUF defaults.

Keep four separate authorities:

| Authority                               | Permitted decision                                              | Boundary                                                                                  |
| --------------------------------------- | --------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| Offline root quorum                     | Appoint/remove metadata keys and thresholds                     | Cannot substitute a wallet grant or reset financial history                               |
| Offline targets quorum                  | Authorize one exact release descriptor and artifact set         | Cannot authorize arbitrary installer commands, paths or wallet use                        |
| Build/publisher identities              | Build, attest, transport and serve authorized bytes             | Cannot manufacture root/targets approval                                                  |
| Installed OS owner/lifecycle controller | Review and apply a verified compatible release to this instance | Must obtain actual signer inventory/floors, pause and recover; no automatic unlock/resume |

Release signing keys are **publisher software keys**, separate from every user
wallet key, Hyperliquid API wallet and fixture signer identity. An authorized bad
signer binary could compromise future wallet use; signatures prove publisher
authorization, not code safety. Code review, isolated builds, provenance and
owner review remain necessary. Network/repository compromise, one sub-quorum
offline key, stolen online metadata keys and interrupted persistence are in scope.
A compromised root quorum or host administrator requires independent recovery;
this design cannot protect an already compromised running host.

The first proposed public custody/update target is Ubuntu 24.04 Linux amd64.
Native macOS/Windows custody, signed installers, actual private VPS and retail
acceptance remain their own gates. Portable source/cross-builds do not close them.

### Key custody, roles and operating windows

| Role      | Proposed threshold            | Custody and permitted operation                                                                                              | Maximum metadata validity |
| --------- | ----------------------------- | ---------------------------------------------------------------------------------------------------------------------------- | ------------------------- |
| Root      | 2 of 3 distinct Ed25519 keys  | Offline; three independent custodians/locations, any quorum needs two accountable people; approve role/key/threshold changes | 365 days                  |
| Targets   | 2 of 3 different Ed25519 keys | Offline/manual signing; approve exact candidate digests and provenance; no reuse of root keys                                | 30 days                   |
| Snapshot  | 1 dedicated key               | Restricted metadata service/job; bind approved target-metadata versions                                                      | 7 days                    |
| Timestamp | 1 different dedicated key     | Restricted metadata service/job; advertise fresh approved snapshot                                                           | 48 hours                  |

Assign named primary/backup custodians and incident contacts before production.
Keeping three keys on one laptop does not meet independent custody. Backup
arrangements must not let one custodian recover a second key in the same role.
The same three people may hold separate root and targets keys; no person controls
a quorum in either role. Root/targets private keys must not enter source, CI
artifacts, app/signer services, ordinary developer environment files or publisher
secrets. Choose reviewed offline signing
devices/software with encrypted backups, separately stored recovery material and
an observed restore exercise. A hardware device is an option, not a claim that
existing infrastructure provides an enclave. No purchases or enrollment are
authorized here. Insufficient personnel/custody keeps release activation blocked;
do not silently lower the threshold.

Use separate least-privilege snapshot/timestamp/publish identities. They may
refresh already authorized metadata, never add targets or lengthen offline
authorization. Refresh timestamp at least every 12 hours, snapshot before its
remaining validity falls below 48 hours, targets before 7 days remain and root
before 30 days remain. Shorter parent-role validity limits effective freshness.
Repository refresh operations are distinct from client automatic installation;
neither job nor client timer is installed now. Staffing, monitoring and recovery
for these windows must pass a disposable rehearsal before public activation.

Every signing ceremony records the role, distinct public key IDs, metadata version,
exact signed-byte digest, validity, approvals, operator identities and backup test
reference. Retain public records and every intermediate root; never retain private
keys/unlock material in evidence. Pin reviewed library versions and retain their
licenses. Use TUF's own canonical signing representation, not the development
envelope's raw-byte signature convention.

### First installation and initial trust

The initial trusted seed consists of a reviewed verifier/installer, exact root
metadata bytes and fingerprint, project/repository identity, permitted channel,
transport policy and minimum supported bootstrap checkpoint. None has a production
value yet. Keep bootstrap trust inside the independently verified installer;
subsequent trust changes go through the verified root chain.

Repository/channel, transport and expected builder identities are seed-locked
policy, not values learned from an API response or proposal. Changing them needs
root-quorum approval, explicit owner review and renewed acceptance. The versioned
policy-authorization wire format must be specified before implementing that change;
no current metadata field grants an endpoint or verifier replacement bypass.

The first supported path must demonstrate installer authenticity through an
independent OS/package signing chain or a separately authenticated owner-reviewed
installer/root fingerprint. A hash and binary from the same GitHub account/site
are not independent authentication. An alternative website on that same account
is not automatically a second trust channel. A signed git commit alone does not
authenticate a downloaded executable. `curl ... | sh`, first-use acceptance,
downloaded replacement roots and an unverified self-verifying installer cannot
close this gate. The beginner journey must eventually make this understandable
without asking users to trust a checksum blindly.

New installations have only the approved seed/checkpoint and freshness window;
they cannot infer an unseen latest release. Existing installations additionally
retain their higher trust and signer floors. A development instance cannot become
production by changing a channel string, replacing its trust file or clearing
state. Such a transition requires an independently authenticated owner migration
and separate acceptance; no import of test wallets, grants or financial history.

### Distribution and HTTPS transport

Proposed canonical software release authority remains the owner-approved GitHub
repository `fased-ai/fased-app`, pinned by immutable repository ID as well as name.
Publish only immutable, versioned assets tied to the approved merged source.
Serve TUF metadata from a dedicated protected metadata branch in that repository;
pin its exact HTTPS raw-content origin/path and branch policy before activation.
That branch serves signed metadata, not executable scripts or a second software
release channel. This is a design recommendation, not a newly created branch.

Production transport needs a separate reviewed fetcher. [GitHub's release-asset API](https://docs.github.com/en/rest/releases/assets#get-a-release-asset)
can stream bytes or return a redirect. The current development downloader forbids
every redirect and must retain that rule; it is not a drop-in public GitHub client.
Specify and test these production restrictions before enabling network acquisition:

- Metadata uses exact approved URLs, HTTPS system roots, no redirects or credentials.
  Discovery pointers and API responses are untrusted locators, never release approval.
- Artifact locators bind the fixed repository ID, release/asset IDs, target name,
  byte length and SHA-256 through authenticated target metadata. A mutable tag,
  filename, API digest or GitHub `latest` response cannot authorize bytes.
- An asset request may make **at most one** HTTPS redirect to one exact reviewed
  GitHub asset-delivery hostname recorded in the transport policy. Its final
  response must be 200, with no further redirect. If actual delivery needs a
  different chain, stop and amend/retest the policy; no wildcard host fallback.
- Reject downgrade, userinfo, unexpected origin/path, private/special-address DNS
  resolution and rebinding. Requests are anonymous; do not forward authorization,
  cookies or proxy credentials. Signed CDN query tokens stay transient and redacted
  from logs, receipts and persisted locators. Limit a redirect URL to 8 KiB.
- Retain the development fetcher's bounded unencoded complete-response checks,
  16 KiB response headers, 64 KiB metadata/descriptor limits and 256 MiB per-artifact
  ceiling. Proposed refresh limits are 32 sequential root steps, 16 non-root metadata files
  per refresh, 20 seconds per response and 2 minutes total. Reaching a bound fails
  closed; very old clients need a reviewed checkpoint/recovery path, not a bypass.
  The root-step budget is separate from the non-root metadata-file budget.
- Verify signed lengths/hashes before exposing output. Never execute downloaded
  code for version discovery, extract arbitrary archive paths, evaluate a package
  hook or pass a remote path/command to a privileged tool. Any new package format
  needs a fixed-layout/allowed-hook review and its own acceptance first.

The exact metadata URL/branch, repository/release policy and asset-delivery host
remain UNASSIGNED. Controlled test host mappings must not become production
configuration or a user-selectable production TLS/host bypass.

### Authorized release unit and provenance

Use a distinct, strict production descriptor schema and repository/channel identity;
do not change the development signature purpose/channel to make it accept stable
releases. TUF authenticates the descriptor and every referenced artifact/provenance
target by exact length/hash. The descriptor is data, not a script or permission.
Its Fased fields must bind:

- schema/product, approved repository ID, stable channel, release identity and
  globally increasing release sequence represented without numeric precision loss;
- exact source commit/tree, immutable tag binding, workflow revision, run/attempt,
  toolchain/lockfile identities and build-provenance target;
- OS/architecture, the complete core/signer pair and each artifact's fixed logical
  destination, target/asset identity, size/hash and compatibility/lifecycle contract;
- any installer/helper/package separately, with its own digest and supported
  predecessor handoff. Two runtime binaries do not implicitly authorize replacing
  a privileged verifier/helper, unit, ACL, config or installer;
- the allowed schema/protocol/features and explicit predecessor/bootstrap limits.
  Unknown migrations, arbitrary hooks and omitted/mixed pairs fail closed.

Production TUF target names are distinct from filenames in the development
manifest. Define a reviewed mapping to fixed local destinations; neither TUF paths
nor an app-supplied descriptor may select an executable path. Keep the pair coherent
and reverify installed bytes independently of package-manager success.

Retain one immutable candidate build across review, offline targets approval,
publication and readback. A change to source, dependencies, workflow, signing or
final artifact bytes invalidates the approvals and affected evidence. Native
code signing/notarization must finish before final hashes and release authorization;
never rebuild or re-sign a published asset in place. Existing P4 fixtures remain
unpublished engineering evidence, not release candidates.

Build provenance must bind each subject digest to the approved repository, source
commit and allowlisted workflow/builder identity. Verify its signature, identity
and subjects against independently pinned expectations; an attestation merely
uploaded beside a binary is insufficient. Preserve verification material for
offline review. Define the actual attestation mechanism and OIDC/signing policy
before implementation. A valid provenance statement cannot replace targets-quorum
release authorization or prove absence of malicious code. No attestation credentials
or workflow are created now.

### Client trust state and lifecycle handoff

Use a dedicated private, owner-controlled trust store, separate from app-owned
policy/proposals and signer financial databases. Persist the trusted repository/
root, metadata versions/digests, transport-policy identity and accepted descriptor
bindings with exclusive locking, atomic durable writes and interruption recovery.
App/model/task identities cannot replace or roll it back. Follow the library's
TUF state transitions, including the specified cache reset when online role keys
rotate; never generalize that into resetting financial release floors.

Take a fixed refresh-start time as required by the TUF workflow and recheck final
applicable expirations before owner admission/replacement. Detected backward time,
untrusted time after restore, expiry, missing trusted state or corrupt persistence
stops new update admission with recovery guidance. Do not accept a server's Date
header as time authority or relax expiry. Intermediate expired roots may be walked
only by the standard sequential root-recovery procedure; the final root and release
authorization chain must be current. The precise trusted-clock/restart policy is a
prerequisite to public acceptance, not an existing system guarantee.

An internal verified-release result must bind the descriptor/artifact digests,
repository/channel, root and role versions, provenance decision and authorization
expiry. The expiry is the earliest applicable metadata expiry; release artifacts
may remain available across refreshed authorizations without rebuilding them.
There is no forced weekly binary release solely to keep metadata fresh. Owner
review must occur within one hour before creating the immutable intent and must
show this evidence. An app-written receipt/boolean is never such a result.

The trusted owner helper independently refreshes/reverifies this chain and bytes,
then obtains actual exclusive core/signer inventory. The production signer admission
path must independently validate the same production authorization and binding;
do not trust an app-supplied `verified` flag. Its exact proof/clock/IPC contract
needs a reviewed lifecycle version before production. The current development
`IntentRequest{Manifest, Trust}` and lifecycle-4 reader are not evidence that this
production handoff exists. No caller-supplied fixture inventory substitutes for it.

Root/role versions and release sequence are different counters. For example, with
root version 7, installed release 43 and reserved release floor 44, valid root
version 8 does not permit installing release 42 or clearing floor 44. A fresh
intent needs a release above floor 44; only a matching durable intent can retry
its already reserved sequence under the supported lifecycle contract.

Apply the existing review → reserve/stage → exclusive pause → private backup/index
→ coherent trial → installed commit → final completion boundaries. Reverify before
irreversible replacement/commit. If authorization expires or is revoked mid-flow,
stop candidate progress and retain the hold/recovery index. Pre-commit protected
abort/compatible executable rollback remains available; post-commit rollback and
old financial database restore remain forbidden. After an expired intent is
aborted, a fresh intent requires a higher authorized release sequence; reauthorize
the same build at that higher sequence if necessary. Never extend a stored intent
by editing its expiry or lower floors to retry it.

If revocation/expiry occurs after installed commit, preserve the financial hold
and committed sequence. Recovery is forward-only: an independently authenticated,
owner-reviewed recovery reader must bind the existing intent/authority and a
fresh higher authorized release, preserve history/unknown holds, and converge
the durable controller records. No ordinary new intent bypasses an active held
intent. That recovery handoff requires a specified compatible reader and a
post-commit rehearsal before public acceptance; until available, remain held
with independent rescue guidance. Do not improvise a completion or financial reset.

Failure to refresh metadata blocks new updates; it must not silently kill services,
erase state, unlock the wallet, clear unknown operations or grant a remote party
financial control. Notify the owner with the precise failed predicate. Publisher
approval, user update preferences and wallet permissions remain separate. Pinning
an old release or selecting a maintenance window cannot bypass expiry/revocation.

### Routine release, rotation and incident procedures

**Release ceremony:** select one explicitly authorized merged source/tag and unused
sequence; freeze/review the complete build and provenance; obtain distinct targets
signatures over the exact target set; publish immutable assets and role metadata
in dependency order with timestamp last; independently fetch/read back every byte
and metadata binding before announcing availability. Serialize ceremonies through
a publisher ledger; a sequence once allocated to different bytes is never reused.
Retries reuse exact immutable bytes and approvals or allocate a new sequence.
Separate code author, release approvers and publisher where staffing permits;
inability to meet the quorum is a BLOCKED release. PR CI cannot publish, tag or
access release signing keys. Publication/announcement needs separate owner authority.

**Planned rotation:** inventory the old/new public IDs and custodians, rehearse
with disposable keys, approve a sequential root transition under the required
thresholds, retain its public continuity chain, then rotate dependent metadata and
prove that both a current and long-offline client recover. Keep old verification
metadata for supported clients; retire old private material only after backup/
recovery and continuity evidence is retained. Overlap must not create an
unapproved extra release-authority path. Library-prescribed online-cache resets
must preserve Fased installed/reserved floors and financial journals.

**Suspected online key/publisher/repository compromise:** suspend publication and
refresh credentials only under separately authorized incident work. Root quorum
removes compromised online keys; publish fresh authorized metadata/provenance and
independently verify readback. A publisher cannot approve replacement targets.
Refresh failure is visible, not a reason to accept expired metadata.

**Suspected targets-key compromise:** stop candidate promotion; the intact root
quorum replaces targets keys, removes suspect targets and refreshes dependent
metadata. Investigate exact affected digests/source/builds. A corrected release
uses a new higher sequence; never mutate a revoked asset in place. A stale/offline
client may remain unaware of revocation within its accepted freshness window;
revocation is not an instantaneous remote kill switch or removal of installed code.

**Suspected root-quorum compromise, lost quorum or lost trusted store:** no in-band
replacement downloaded from that potentially compromised channel restores trust.
Block new updates and arrange an independently authenticated rescue verifier/root
with owner consent. Preserve signer journals, originals, installed/reserved floors
and all unknown/recovery holds; missing authoritative financial history stays held.
Host snapshot rollback can also rewind local trust state: local files alone do not
prove hardware/remote monotonicity. Disk-loss financial recovery remains P4-15.
Root renewal after mere expiry may use the valid continuity procedure; expiry is
not itself permission to replace the seed. No emergency `--force`/unsigned fallback.

Retain a public incident/correction record and contact instructions when publication
is separately authorized. Do not send messages, publish advisories or mutate hosts
as a consequence of this specification. A compromise of already installed code
may need owner-authorized containment beyond a metadata update.

### Decisions and acceptance before production

All entries below are **UNASSIGNED/BLOCKED**, not credentials to fill automatically.
Named owner approval and recorded evidence are required before activating them.

| Decision              | Required concrete record                                                                                                                         |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| Initial bootstrap     | Supported distribution/OS signing chain or independent verification channel; exact verifier/root/checkpoint digests                              |
| Roles/custodians      | Named 2-of-3 root and targets custodians, distinct online-role owners, backup locations/restore evidence and monitoring rota                     |
| Origins/transport     | Repository ID; protected metadata branch/path; exact metadata/API/CDN hosts and redirect/DNS policy; outage behavior                             |
| Release authorization | Approvers, sequence ledger owner, supported target/channel, retention policy, publisher identity/least privilege and protected workflow revision |
| Provenance            | Attestation verification mechanism, expected builder/source/workflow identities, retained verification roots/material                            |
| Trusted state/time    | Library/version/license review, durable store/clock policy, production signer/owner handoff contract and lifecycle-reader compatibility          |
| Incident/recovery     | Named contacts, compromise/expiry/lost-quorum drill, independently authenticated rescue path and supported old-client horizon                    |
| Platform/retail       | Native signing credentials/acceptance, real private VPS, authoritative recovery and representative first-install/update trial                    |

Required future acceptance uses disposable keys, controlled clocks/HTTPS, isolated
identities and no live wallets. Each case must retain exact inputs/results and
demonstrate zero financial/install authority until its explicit owner stage:

Inject fixture clocks; do not change the owner's system clock or trust settings.

| ID    | Predicate that must fail safely or pass with exact bindings                                                                                                     |
| ----- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| RT-01 | Independently authenticated seed succeeds; substituted installer/root, same-origin checksum-only proof and development-to-production promotion refuse           |
| RT-02 | Genuine distinct-key thresholds succeed; duplicate signatures, one-key root/targets approval, wrong role/channel/repository and unknown keys refuse             |
| RT-03 | Valid root succession and expired-intermediate catch-up succeed; skipped versions, missing old/new quorum and expired final root refuse                         |
| RT-04 | Expired/frozen/mixed/rolled-back metadata, invalid clock and unauthorized target/provenance changes refuse before candidate admission                           |
| RT-05 | Controlled exact asset/redirect succeeds; cross-origin/wildcard/downgrade/private-DNS/rebinding/token leakage, extra redirect and unbounded/partial data refuse |
| RT-06 | Complete pair/provenance/helper bindings pass; mixed platform, changed source/workflow/digest, omitted helper or arbitrary archive/hook/path refuses            |
| RT-07 | Interrupted/concurrent trust writes recover monotonically; corrupted/missing/restored-old trust state cannot reset release or financial floors                  |
| RT-08 | Current/offline client rotation and online-cache reset preserve financial state; revoked-key/target admission fails after verified fresh revocation             |
| RT-09 | Immutable owner and signer independently verify production authorization; app-forged receipts/inventory/approval, unsupported readers and expired intent refuse |
| RT-10 | Mid-lifecycle expiry/revocation keeps holds; protected pre-commit rollback/abort preserves state; committed rollback and stale financial restore refuse         |
| RT-11 | Public readback matches approved immutable source/artifacts/metadata/provenance; failed promotion retains evidence and never reuses a sequence for other bytes  |
| RT-12 | Lost quorum/store and publisher outage produce actionable independent recovery without an unsigned/force path, automatic unlock or bot resume                   |

These are a specification and future test obligations, **not newly passing tests**.
P4-10/11 stay BLOCKED until the chosen production bindings, implementation, rehearsal
and explicitly authorized public readback pass. P4-12/13/15/16 remain independent.
No approval in this document enables client automatic updates or real wallets.

## Development-only TUF trust rehearsal

### Frozen contract

The next engineering slice is explicitly disposable and separate from the
production proposal. Pin `github.com/theupdateframework/go-tuf/v2` v2.4.2
(`f5edbde31e5507f46db2069402dc38903fe6d9d4`, Apache-2.0). Its metadata API advertises
specification 1.0.31; this slice accepts that finite top-level subset, not a claim
of full latest-specification interoperability. Use its trusted-metadata/signature
verification, not its filesystem updater or default network fetcher.

`updates trust-inspect --development --seed-file <file> --seed-sha256 <independent-hash>
--fixture-time <unix-seconds>` requires either `--initial` or both `--state-file`
and `--state-sha256`. It is read-only: it fetches controlled HTTPS metadata and one
inert descriptor and returns a proposed fixture snapshot; no file/service/wallet
is written, no binary is acquired or executed, and no production handoff exists.
An optional `--tls-ca-file` supplies only controlled test CA roots; verification
and the development fetcher's no-redirect/origin/timeout rules remain intact.

The version-1 seed, snapshot and descriptor all require `fixtureOnly: true`,
purpose `fased-app-tuf-rehearsal:v1`, repository fixture ID and channel `development`.
The seed pins exact initial root bytes, HTTPS origin and opaque preservation
anchors/installed/reserved fixture floors. Every signed role also carries this
fixture binding. Only four top-level roles, distinct Ed25519 key sets (root/targets
2-of-3, timestamp/snapshot 1-of-1), consistent snapshots and one fixed descriptor
target are supported. Metadata is capped at 8 KiB each, the descriptor at 1 KiB,
the combined snapshot at 64 KiB and root discovery at 32 successors. There is no
delegation, artifact download, public GitHub redirect handling or provenance
verification. Those remaining production obligations do not become PASS here.

The injected clock never changes host time. Snapshots bind the seed, exact role
bytes, descriptor, observed time and highest observed fixture release. Missing,
corrupt, mismatched or backward-time snapshots refuse; a same-version metadata or
release identity cannot change bytes. Root succession uses the library's dual
quorum checks, including expired intermediate roots. Online-role key changes reset
only online metadata cache comparisons; fixture installed/reserved/observed release
floors and preservation anchors never rewind. A matching release may be re-observed
without installation; a lower or conflicting same-sequence release refuses.

An explicitly disposable Go snapshot store exercises real private-file locking,
immutable hash-named snapshots, compare-and-swap publication and durable head
replacement/retry. Only an opaque result from this verifier can be committed;
the CLI cannot commit. External expected-head digests detect stale/replaced heads.
Whole-store plus external-anchor rollback is outside this model: this is not
installed owner/signer isolation or authoritative financial recovery. Faults and
concurrent writers are rehearsed on generated fixtures, then cleaned. Partial
matching temporary bytes resume; mismatched or published corrupt bytes refuse.
The store is capped at 128 files and performs no automatic garbage collection. Production
configuration/credentials, automatic updates and real wallets remain unavailable.

Verified progress survives a later failure. A newly trusted root/timestamp/snapshot
is proposed as a `complete: false` checkpoint even if subsequent metadata or the
descriptor fails. The API returns both an opaque progress transition and the
error. The read-only CLI then exits nonzero and prints a typed
`trustProgressOnly: true` report with `metadataVerified: false`,
`descriptorVerified: false` and every execution capability false. Such a checkpoint
may retain older cached role bytes only as externally anchored rollback evidence;
they never authorize a candidate under new keys. The fixture controller commits
the opaque progress through CAS before retrying. Ignoring learned progress could
reintroduce revoked keys or older metadata; it is not an allowed production design.
Only a fresh complete chain produces `complete: true` and a verified descriptor.
The actual installed owner/signer trust store and handoff remain unimplemented.

## Development HTTPS acquisition and bootstrap trust

On Linux/macOS, an ordinary user can fetch a disposable signed development pair
without an installed instance. First prepare a private directory and obtain the
test public trust document and its SHA-256 fingerprint through an independent
trusted channel. The fingerprint covers the **exact file bytes**, including whitespace.
A fingerprint delivered by the same untrusted server does not establish authenticity.
There is no built-in key, trust-on-first-use, downloaded trust fallback or production default.

```sh
mkdir -m 700 /absolute/private-development-downloads
fased-app updates acquire --development \
  --origin https://controlled-fixture.example \
  --manifest-url https://controlled-fixture.example/release/manifest.json \
  --trust-file /absolute/disposable-public-trust.json \
  --trust-sha256 <independently-obtained-64-lowercase-hex-fingerprint> \
  --inventory /absolute/disposable-inventory.json \
  --output /absolute/private-development-downloads/new-pair
```

For controlled HTTPS fixtures only, `--tls-ca-file /absolute/fixture-ca.pem`
replaces the TLS root set with that explicit CA. Certificate chain and hostname
verification remain enabled. Without that option the system TLS roots apply.
No insecure TLS flag, proxy environment, credentials, cookies or automatic
decompression is accepted. The origin must be an exact HTTPS scheme/host/port
without a path. The manifest URL must use the same origin and a clean file path;
credentials, query strings, fragments, escapes and **all redirects** are refused.
Artifact URLs use the signed filenames beside the manifest, on that same origin.

The verifier authenticates the purpose-bound signature, validity, target,
compatibility and caller-supplied sequence floor **before** fetching artifacts.
It fetches exactly the complete core/signer pair, checks every size and SHA-256,
then rechecks signature expiry and all local bytes. Manifest/CA/trust documents
are limited to 64 KiB; each artifact is limited to 256 MiB. Header, connection,
TLS and response deadlines are bounded, with a one-minute overall deadline.
Only complete unencoded HTTP 200 responses pass; partial or altered downloads fail.

The output must not exist, including as a symlink. Its parent must be a real
0700 directory owned by this user. The new directory is 0700 and files are 0600,
without executable bits. Existing output and foreign files are never adopted
or overwritten. Failure removes this attempt's output; retry uses a fresh path.
Successful output contains the two artifacts, `manifest.json`, `trust.json`,
`inventory.json` and the last-written `acquisition.json` receipt. Crash leftovers
have no authority: preserve/review them and reverify bytes with `updates inspect`.
Windows acquisition refuses until its private-directory ACL boundary is verified;
ordinary offline inspection and cross-compilation remain supported.

The receipt binds both the exact envelope hash and signed payload hash, trust
fingerprint, origin and disposable compatibility plan. Installation, execution,
resume, wallet signing, financial execution and automatic updates remain disabled.
No candidate is executed, no instance is opened and no owner/signer API is called.
The inventory floor is disposable input, not a durable anti-rollback record;
repeating a valid download with the same test floor can pass. Installed lifecycle
review must still obtain authoritative inventory/floors and reverify bytes.

There is no automatic key rotation: a new key is refused unless an independently
approved replacement trust document and fingerprint are explicitly supplied.
This does not establish production key custody, revocation, publisher authorization,
provenance, public origin or release publication. P4-10/11 public gates remain open.
Local and Ubuntu CI run `scripts/check-update-acquisition.mjs` with disposable TLS
and Ed25519 keys; receipts/logs live in `.evidence/p4-acquisition/`, and the runner
removes its generated keys/downloads in `finally`.

## Shared read-only status and recovery guidance

Open **Settings → Updates & recovery** in the private UI, or use the same
authenticated core contract from the CLI:

```sh
fased-app updates status --data-dir /absolute/instance/directory
```

Use the existing instance's CLI login first (`fased-app login --data-dir
/absolute/instance/directory`). The command reads through private IPC. It does not
open SQLite directly, create an intent, contact a release server or install anything.
For the existing Linux managed disposable installation, use its verified immutable
app helper and identity switch:

```sh
fased-app custody updates status --disposable-fixture --prepare-host
```

That command requires the existing owner OS authorization and managed CLI login
(`fased-app custody login --disposable-fixture --prepare-host`). It does not take
the owner update lock or use the signer's administrative channel. Status cannot
repair an unavailable core: start/reconnect to the existing instance instead.

`GET /api/v1/updates/status` requires an authenticated session, exact host/origin,
and returns a noncacheable version-1 observation. UI and CLI consume this same
response. It reports the validated core metadata fence (`idle`, `paused`,
`released` or `unavailable`) and the signer's actual public connection, wallet
lock, update/storage hold and recovery hold. Missing/unsupported signer replies
produce unknown values, never an unlocked or cleared state. The UI removes its
previous observation during refresh and on failure; timestamps label observations,
and nothing polls or executes updates automatically. Prototype mode shows no live status.

Core and signer observations can change independently. A core intent reference is
app-owned metadata. A released fence or absent public hold does not prove an
installed sequence, successful update, latest release or permission to trade.
No owner inventory, sequence counter, authority anchor, key, credential, backup
image or financial journal is exposed through this view.

If paused, preserve the instance and recovery files. If the signer is unavailable,
its lock and holds are unknown. If it reports recovery required, key-only recovery
does not reconstruct financial history. On the installed owner's terminal, inspect
the actual protected inventory:

```sh
fased-app custody updates inventory --disposable-fixture --prepare-host
```

Then use the existing protected review for the exact retained intent and operation
documented below. The UI offers no apply, abort, release, restore, resume, approve
or unlock control. Never delete the pause/host marker, restore old signer databases,
rewind counters or clear unknown/recovery holds to make status look healthy.
Public acquisition/trust, active scheduling, native custody, real VPS and portable
financial-history recovery remain separate acceptance gates.

## Notify, pin, window and review-only proposals

**Settings → Update preferences & proposals** saves public app metadata through
the same revision-checked core API used by these commands:

```sh
fased-app updates preferences --data-dir /absolute/instance/directory
fased-app updates preferences --data-dir /absolute/instance/directory --request-stdin
# Existing managed disposable installation; existing managed CLI login required:
fased-app custody updates preferences --disposable-fixture --prepare-host
fased-app custody updates preferences --disposable-fixture --prepare-host --request-stdin
```

`--request-stdin` accepts one public JSON request, never a key, credential, grant,
signature approval or owner action. Both transports require the existing CLI
session. The managed helper switches to the app UID using the verified immutable
executable; it has no signer owner channel and does not take the owner update lock.

Read the current `revision` before each change. For example, replacing `12` with
that current revision:

```json
{
  "revision": 12,
  "action": "policy",
  "policy": {
    "notify": true,
    "pin": {
      "sequence": "7",
      "manifestSha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    },
    "window": {
      "timeZone": "America/Chicago",
      "days": [1, 2, 3, 4, 5],
      "start": "23:00",
      "end": "01:00"
    }
  }
}
```

Notify defaults to true and shows **in-app notices for saved pending proposals**.
There is no release feed, periodic check, desktop push, external notification or
active scheduler. Turning notices off hides them without deleting proposal history.
Reviewing or dismissing the exact proposal removes its pending notice. Identical
imports retain the same digest/state/timestamps/revision, including after restart;
different claims for the same sequence/manifest digest are refused.

A pin compares a claimed manifest SHA-256 and canonical positive decimal sequence
string. Values never pass through a JavaScript number. A mismatch remains visible.
The pin is a review preference, not release authentication or a restriction on the
protected owner's separate manual decision. Omit `pin` to clear it explicitly.

The optional weekly window uses `UTC` or an IANA region timezone from the embedded
timezone database. `days` are unique ascending weekdays, Sunday=0 through
Saturday=6, referring to the **start day**. Bounds are strict `HH:MM`, start inclusive
and end exclusive, 15 minutes to 12 hours apart; overnight windows are supported.
Both occurrences of a repeated DST minute qualify; skipped minutes never occur.
The server evaluates open/closed/unrestricted only on a read/change. It schedules
nothing and cannot install or resume when a window opens. Omit `window` to clear it.

Public acquisition and publisher trust remain gated. To record a **manual,
unverified** proposal for review, send:

```json
{
  "revision": 13,
  "action": "propose",
  "candidate": {
    "release": "0.0.0-disposable",
    "sequence": "7",
    "manifestSha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "target": { "os": "linux", "arch": "amd64" }
  }
}
```

The response supplies a domain-bound **review digest of this metadata**. That is
distinct from its claimed manifest digest and from any signer intent or financial
authority. Use `{"revision":14,"action":"review","digest":"<exact-review-digest>"}`
to mark reviewed; `dismiss` hides its pending notice. Only dismissed entries may
be `remove`d. Removal explicitly forgets that proposal and its notice history.
At most eight proposals are stored. URL, arbitrary-command and asserted-permission
fields are not accepted. Unknown/case-alias/duplicate fields and requests over
16 KiB are refused.

Every response reports `verified:false` for proposals and
`installAllowed`, `resumeAllowed`, `financialExecution`, `automaticUpdates` as false.
Marking reviewed acknowledges only the exact app-owned metadata; it cannot verify
a signature, prepare an intent or approve installation. The protected owner flow
must independently authenticate the actual signed envelope, complete artifact pair,
platform/contracts, current core/signer state and monotonic floors.

`GET/PUT /api/v1/updates/preferences` requires an authenticated session; writes also
require exact Origin, JSON and CSRF. Preferences and proposals share the existing
setup revision, snapshot anchor, SQL pause fence and private metadata backup.
Stale clients must refresh and review again; they never silently overwrite or
automatically retry. The UI discards the old view on errors and disables proposal
actions while policy edits are unsaved.

This is an optional version-1 extension of existing setup JSON in core schema 3;
no financial format, schema conversion or updater compatibility rule changes.
Saving it transactionally installs a preservation trigger. Current generic setup
clients retain the extension, and genuine older writers that would discard it are
refused by SQLite. Older interfaces do not manage this feature; use a capable
binary. Key/history recovery and automatic execution remain disabled.

The core exposes its compiled format/feature contract:

```sh
fased-app updates compatibility
fased-signer fixture-compatibility
```

These commands need no instance, wallet, password, service or network. Protocol
v1 by itself cannot distinguish the earlier signer from approval-aware replay.
The contracts explicitly include core schema 3, vault format 1, both journal
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
read these owner inventory/intent/staging RPCs or prepare/stage/abort an intent. Public status
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

The installation transaction and installed-sequence commit, recoverable backup,
signed package/bootstrap trust, installation migrations, coherent service
replacement/rollback, UI/timer integration and cross-platform installed acceptance
remain later P4 packages. Core pause/preflight and recoverable artifact staging
are engineering features; they do not grant installation permission. Public-path acceptance requires exact published
artifacts and separate release authority. Until those gates pass, a valid plan
always denies installation and financial resumption.

## Recoverable disposable artifact staging

After protected preparation, the same immutable owner helper can copy the exact
reviewed pair without another permission prompt:

```sh
fased-app custody updates stage --disposable-fixture --prepare-host \
  --intent-id <same-32-hex-id> --artifact-dir <complete-local-pair>
```

The core contract now contains a separate `lifecycle` contract: entrypoint 2,
stage format 1 and explicit owner-review, signer-staging and no-install features.
This is independent of runtime protocol 1 and SQLite schema 3. Candidates that
omit or change it, or cannot replay `update-staging-v1`, are rejected. These are
signed declarations; candidate code is never executed to discover its contract.
Later installation must verify actual candidate behavior before activation.

Preparation durably binds both artifact sizes/hashes and this lifecycle contract.
The owner helper rereads complete stable source files, including their prefix on
retry, and sends bounded 24 KiB chunks through owner-only signer IPC. The signer
stores inert mode-0600 files in its private `update-<intent-id>` directory using
FD-relative, no-symlink operations; it rejects foreign ownership, executable
files, extra hard links, offset gaps, changed bindings and conflicting retries.
Matching partial bytes are retained and extended, never truncated. Each chunk
and newly created directory entry is synced before acknowledgement. An uncertain
write holds the service until restart. Aborting leaves staged evidence intact.

Completion independently rehashes both entire staged files, revalidates the
active intent/digest/floor and financial state, and requires unchanged fresh core
metadata/boot. One synced `update-staged` journal record binds the receipt to the
intent. A lost response or signer restart reuses that receipt; repeated completion
does not append again. Every completed staging-status request rehashes stored bytes. Abort clears
only the exclusive slot and retains the floor, lock, financial originals,
spending, held operations and recovery holds. Only the owner channel can write,
inspect or finalize staging; app sessions cannot administer it.

Staging never installs or executes files, replaces the immutable helper, pauses
the app, migrates state, unlocks a key or resumes execution. Its receipt always
reports installation, resume and financial execution false. Core metadata remains
mutable after staging. A later installation transaction must first acquire an
exclusive core pause and fresh snapshot, revalidate staged bytes and authority,
and implement durable installation/health/recovery and installed-sequence commit.
No timer/UI automatic updater or public signing trust is enabled. The Linux
fixture must not be used with real wallets or funds.

## Exclusive core pause and preflight

```sh
fased-app custody updates preflight --disposable-fixture --prepare-host \
  --intent-id <same-staged-intent-id>
```

Preflight uses the already reviewed, staged intent without requesting another
financial permission. The immutable owner helper obtains fresh signer inventory,
then acquires the exact core pause through the existing fixed app-UID helper and
authenticated private CLI IPC. Browser HTTP cannot invoke this route, even with
an owner session. Private requests still require session authentication and CSRF. Authorization
uses the owner terminal; the separate public payload stream is attached only to
the final app-UID child, after authorization.
The app bridge remains read-only toward the signer; only protected owner IPC can
request signer preflight.

Core schema 3 adds one durable pause record and SQL write guards for setup,
account/proposal records and credentials, including OS-owner password recovery.
Metadata schema-1/2 migration preserves owner credentials, sessions, setup and
accounts; earlier schema-2 binaries refuse the new state. FULL synchronous WAL
commit precedes acknowledgement. Login/logout, public reads and private recovery
inspection remain available. The same pause identity survives helper death,
process termination and core restart. First acquisition rejects an old core boot
or changed metadata; retry after a durable pause accepts the new boot only while
its original metadata/binding remain unchanged. No timer expires the fence.

The signer independently rechecks the active staged intent/digest/floor, its
fresh boot/history/authority, every stored artifact byte and all financial
originals, spending, pending/unknown reservations and recovery holds. The owner
rereads the core pause after the signer reply. The resulting nonce/boot/digest-
bound proof is read-only, writes no financial journal record and grants no
installation, resume or unlock permission. It is a point-in-time observation,
not a lease: any future install transaction must revalidate both owning services
and current staged bytes under its own exclusive controller.

Protected `custody updates abort` first obtains the exact durable aborted signer
receipt, then releases only its matching metadata fence. A durable tombstone
prevents the same sequence from pausing again, including an abort that wins a
race before initial pause. Interrupted release can be retried; missing signer
proof or another pause leaves the core held. Wallet lock, original operations,
spent/held amounts, recovery holds and the signer reserved floor never reset.
Core pause records are app-owned metadata and cannot grant or revoke signer
authority; the financial signer remains the independent authority boundary.

Lifecycle entrypoint 2 advertises `owner-core-pause-v1`, and the signer advertises
`fixture-update-preflight-v1`. The `lifecycle-v2-v1` operations-state feature
explicitly requires readers of nested lifecycle-2 intent records. Existing
entrypoint-1 staging journal records stay
readable and abortable; new candidate admission still requires the current
contract. No binaries/helpers are replaced or executed, no financial state is
migrated, and no public trust, real wallets, automatic updates or execution resume
is enabled by this package.

## Private recoverable app-metadata backup

```sh
fased-app custody updates backup --disposable-fixture --prepare-host \
  --intent-id <same-staged-intent-id>
```

The immutable owner helper obtains fresh core/signer preflight, requests a backup
through private authenticated app IPC, then independently revalidates the signer,
reserved floor and complete staged hashes. No wallet secret or snapshot bytes
cross that bridge. The browser cannot call this operation. No additional financial
approval is created by the already reviewed development intent.

On Linux the SQLite owner holds `BEGIN IMMEDIATE`, rechecks the actual pause and
boot, and serializes its consistent WAL view. The bounded 64 MiB image preserves
app metadata, hashed credentials, sessions and the durable pause. It is private
plaintext at rest, protected by the app UID and directory/file modes 0700/0600;
it is not an encrypted portable backup. Do not distribute it or include it in
support bundles. Signer keys, policies, grants, originals, journals, spending,
reservations, recovery holds and sequence floors are never copied or restored.

Fixed paths under `app-state/lifecycle/<intent-id>/` contain a durable
`request.json`, `metadata.sqlite` and final `recovery.json`. The index binds the
pause/instance/metadata, signer authority and floor, staged identities, snapshot
hash/size and a versioned recovery binding. It is app-owned evidence, not signer
or installation authority. Boot/challenge values are refreshed for each request
and excluded only from the durable identity. Original intent snapshots remain
bound. Success still leaves core metadata paused and the wallet locked.

Writes resume matching prefixes, sync file bytes, publish with Linux atomic
no-replace rename and sync the directory. Completion rehashes the actual private
file and reads it with SQLite `immutable=1` for integrity, metadata and pause
verification; verification never migrates the image. Restart/reply-loss retries
reuse the same complete image/index. Symlinks, hard links, broad permissions,
conflicting prefixes, corrupt indices/images and stale service evidence refuse
without replacing data. If an incomplete image's source bytes change (including
a new ephemeral login), exact-byte regeneration is unavailable: preserve its
request/partial bytes and stop. Completed images remain reusable after login.
No cleanup/replacement or restore command is provided for such a conflict yet.

This same-filesystem backup does not survive loss of that disk and provides no
financial rollback. A successful receipt is a point-in-time observation; a future
install controller must revalidate both services and staged bytes. Installation,
helper replacement, financial restore/migration, installed-sequence commit,
health/rollback, public signing trust and native custody remain open. Real wallets,
network execution and automatic updates stay disabled.

## Recoverable compatible trial installation (Linux disposable fixture)

```sh
fased-app custody updates install --disposable-fixture --prepare-host \
  --intent-id <staged-id> --artifact-dir <verified-pair-directory>
fased-app custody updates install-status --disposable-fixture --prepare-host \
  --intent-id <same-id>
fased-app custody updates rollback --disposable-fixture --prepare-host \
  --intent-id <same-id>
```

`PREPARE` grants no installation. A separate protected owner terminal review
requires `INSTALL <full-review-digest>`. Initial installation needs an unexpired
signed development manifest and the exact already staged `fased-app` /
`fased-signer` pair. The helper verifies its actual contracts against the signed
intent, requires unchanged supported state formats, and checks space for the
candidate, previous pair and atomic replacement files. Both the previous and
candidate binaries must understand lifecycle entrypoint 4 and
`install-controller-v1` / `install-completion-v1` journal records. Lifecycle-1/2/3 staging histories remain readable
and abortable; upgrading an older immutable helper is a separate bootstrap gate.

Under the shared owner lifecycle lock, the helper retains an immutable previous
runner and signer plus the verified candidate in fixed root-owned
`updates/<intent-id>/` paths. The signer journals an exclusive installation hold
bound to the recovery index, installation identity, original and candidate hashes,
signed contracts and reserved sequence floor. Keys stay locked. This hold blocks
ordinary abort until executable rollback has passed health checks. Filesystem
phase receipts and the active-instance marker describe host progress; they cannot
authorize financial work. Install/remove/service writers cannot compete.

The controller stops both managed processes, verifies they are inactive, replaces
the known executable pair and inventory with synced atomic file operations, then
starts and checks both services. Every project executable, including compatibility
probes and cold recovery inspection, runs only after a fixed OS utility drops root,
groups and capabilities. The signer and core remain separate identities. Unknown
files, altered generations, unsafe parents and foreign units are preserved and
refused. No code, database or helper is executed as root.

A retry uses the retained verified runner even when the live pair is mixed. It
revalidates the actual live signer hold, or runs the previous immutable signer as
the configured signer UID against the locked state while that daemon is stopped.
A retained host receipt cannot replay an aborted or superseded signer intent.
Fresh private core recovery verification rereads the original snapshot without
creating another image when installation bookkeeping advances the journal head.
Signed expiry prevents new admission, but cannot strand an already held recovery.

Success reports `healthy-held`: the candidate is running and verified, but the
installed release sequence is **not committed**, metadata remains paused, and the
wallet remains locked. It is a trial outcome, not a completed production update.
A second identical call revalidates that state. Closing the terminal or losing a
reply does not clear the hold or change the candidate identity.

A failed candidate health/recovery check durably chooses executable rollback.
Explicit rollback requires `ROLLBACK <installation-digest>`. Both routes restore
only the previous compatible pair and inventory, verify old service health and
financial authority, then report `rolled-back-held`. A same-input retry follows a
persisted rollback decision. This outcome means the candidate failed or was
withdrawn; it never means installed. A separate protected `abort` can then release
that update intent and its metadata fence. Spending, original submissions,
unknown holds, key/authority identity and the reserved floor never rewind; no app
metadata or financial database is restored. A failed rollback retains the hold.

## Protected installed-sequence commit and completion

After a verified `healthy-held` trial:

```sh
fased-app custody updates complete --disposable-fixture --prepare-host \
  --intent-id <same-id>
```

The immutable owner helper displays the exact installation binding and requires
`COMPLETE <installation-digest>`. This is the point of no executable rollback.
Before commit it rechecks the exact installed pair/inventory, healthy services,
private recovery image, original signed contracts, current signer authority and
reserved floor. Foreign installer records are preserved and refused.

The signer durably journals `committed-held` and advances its independent
`installedSequence`; the reserved `sequenceFloor` never decreases. The financial
hold remains until root installer intent, ready record and private owner intent
converge to the target pair with synced publication. Then the signer journals
`completed`, clears only that active intent and stays locked. Only an actual
current signer receipt lets the protected owner conductor release the matching
core pause. A different or newer pause cannot be released by an old receipt.
The host active marker is removed last. No databases are restored.

A lost reply or interrupted helper resumes with the same `complete` command and
intent. After commit no repeat confirmation is needed: recovery only finishes the
already approved installation. Health failure after commit retains state for
explicit recovery rather than rewinding the installed sequence. `install-status`
reports the current signer receipt, including after service restart. Exact current
completion retries do not append signer records, including after later metadata
writes. A higher reserved sequence, another active intent or changed authority
rejects a historical receipt. Neither phase creates a financial grant, clears
missing-history/unknown holds, resumes bots, nor unlocks a key.

All paths are explicit disposable Ubuntu engineering operations. Public/bootstrap
trust, updater/helper self-upgrade across unsupported readers, native/private-VPS
acceptance and retail release remain open. Real wallets, on-chain execution and
automatic updates stay disabled.

## Older-helper reader bootstrap (Linux disposable fixture)

Lifecycle 3 cannot admit the lifecycle-4 helper it needs for completed updates.
Run the exact signed **target** core with a signed reader-bridge bundle:

```sh
./fased-app custody bootstrap --disposable-fixture --prepare-host \
  --intent-id <32-hex-id> --bundle <bridge.json> \
  --trust-file <development-public-trust.json> --artifact-dir <target-pair>
```

The owner reviews `BOOTSTRAP <full-digest>` in the terminal. The bundle contains
base64-encoded exact signed maintenance and target envelopes. Both signatures use
the same explicitly trusted development key, sequence, target and validity; the
target's `bootstrapFrom` binds the maintenance payload hash. The maintenance
manifest describes the exact **installed predecessor** pair. The target describes
the replacement pair. Ordinary release admission refuses a bootstrap target.
Only lifecycle 3 → 4 with the known completion-reader feature is supported; state
formats, protocol, identities and paths cannot migrate. Lifecycle 1/2 and other
reader changes stop unchanged.

The predecessor reserves a maintenance sequence, locks the wallet, stages its own
unchanged pair, pauses metadata and captures a recoverable private image. The new
verified helper lives in a fixed root-owned path and runs as the owner. Only fixed
trusted OS utilities run privileged. Before a cold switch or rollback, only the
new signer reader inspects state as the signer UID. Any unexpected operations
head, authority, counter, unknown outcome, recovery hold or floor prevents the
switch. A cached host receipt cannot establish that authority.

A healthy reader pair converges installer records, durably chooses forward-only
completion, then aborts just the predecessor-readable maintenance intent and
releases its matching core pause. Failed initial health restores compatible old
executables, verifies current history and aborts only that maintenance intent.
There is no financial database restore, installed-release commit or automatic
unlock. The reserved floor stays consumed. A subsequent ordinary signed update
must use a higher sequence and the normal INSTALL/COMPLETE flow.

Retry with only the same identity; retained verified envelopes/readers make the
original input files unnecessary, even after expiry or partial replacement:

```sh
./fased-app custody bootstrap --disposable-fixture --prepare-host \
  --intent-id <same-id>
```

`reader-upgraded` means the helper handoff succeeded, not that a release sequence
was installed. Exact current retry returns `alreadyCurrent` without signer writes.
Superseding state refuses historical replay. Unknown files and foreign records
are preserved. Public acquisition trust, signing-key operations, native custody,
real VPS acceptance and automatic updates remain separate gates; this command is
not a public installer.
