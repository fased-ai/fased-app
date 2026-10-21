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

## Development-only release-unit and provenance admission

### Frozen fixture contract

This additive, read-only slice consumes the existing TUF descriptor's exact
`evidenceSha256`; it does not widen the TUF role/target policy. All records retain
the seed's fixture binding and add profile `fased-app-release-admission-fixture:v1`.
A separately SHA-256-pinned version-1 policy supplies an exact fixture build
identity, target and disposable Ed25519 builder public key, valid for at most seven
days. The identity binds fixture source repository, 40-character commit/tree,
fixture workflow path/revision/run/attempt, fixture builder, toolchain and lock
digests. These are synthetic identities, not verified GitHub/OIDC/Sigstore claims.

The bounded evidence document contains exact base64 unit and provenance-envelope
bytes. The unit binds descriptor sequence, platform, build identity, compatibility
(core/signer protocol 1, owner entrypoint 4, installer format 1), and exactly four
ordered, distinct subjects: `core`, `signer`, `owner-helper`, `installer`. Each
subject binds its role, platform, source commit, fixed `<role>.fixture` filename,
size and SHA-256. Both helper and installer are independently named required
subjects; neither is inferred from the runtime pair. Unknown fields, mixed pairs,
omitted/extra/reordered subjects, paths, archives and hooks refuse.

The provenance envelope signs its exact payload bytes with Ed25519 over
`fased-app-build-provenance-fixture:v1` plus NUL plus payload. The payload binds
the unit digest, exact policy identity, all four subjects and a build time within
the policy interval and no later than the injected observation clock. Key ID,
signature, fixture binding, purpose and evidence digest must all match. This is a
finite fixture attestation format, not a production provenance standard or proof
that a builder honestly compiled source.

`updates release-inspect` requires the same explicit development seed/state/clock
flags as `trust-inspect`, plus `--policy-file`, `--policy-sha256`, `--evidence-file`
and `--artifact-dir`. Policy/evidence are bounded by 64 KiB. The real, absolute
nonsymlink artifact directory must contain exactly the four regular, nonexecutable
files (each at most 64 KiB), beginning `FASED-INERT-ARTIFACT:v1` plus newline.
Contained, stable opens and complete size/hash checks read these bytes; nothing is
downloaded, unpacked, executed, installed or written. No installed defaults or
owner/signer calls exist. All execution/automatic-update/wallet flags stay false.

Successful admission returns an opaque fixture trust transition and an inspection
report. A failure after trusted metadata is learned retains that knowledge as an
opaque progress-only transition; the CLI prints that report and exits nonzero.
Observed descriptor release floors may advance, but installed/reserved floors and
preservation anchors remain unchanged. Retrying the identical descriptor can
complete admission; older/conflicting metadata cannot erase learned revocation or
version knowledge. The CLI cannot commit; only the existing fixture CAS store can
persist the opaque transition. No financial or production authority follows.

Acceptance requires malformed/mixed/omitted subjects, substituted evidence,
builder/source/workflow/run identity, signature/time/policy, changed/truncated or
executable artifacts and path/symlink/extra-file rejection; restart/progressive
trust preservation; compiled ordinary-user read-only checks locally and on Ubuntu
24.04. Real wallets, automatic updates, public publisher credentials/feeds and
installed production handoff remain disabled. RT-04 evidence/RT-06 fixture coverage
does not close either public P4-10/11 gate.

## Independent owner/signer fixture admission handoff design

### Scope and authority

The pure Go fixture model/store implements this contract; no installed command or
RPC exposes it. The separate compiled fixture transport below reuses actor-local
verification, not shared session/receipt authority. The fixture profile is `fased-app-owner-signer-admission-fixture:v1`, protocol
1, mode `inspection-only`. Every message requires that profile, mode and the
existing repository/development fixture binding. Real keys, financial execution,
installed execution, automatic updates, production trust and P5 remain disabled.
No `IntentRequest`, lifecycle-4 method, service or runtime capability changes here.

The app may propose a unit digest and display progress. Its reports, files,
`verified` booleans, inventory, clock, model output and public receipts confer no
authority. Owner and signer share verifier code but independently load their own
protected configuration, clock, trust checkpoint and fixture state, and independently
verify metadata, provenance and all four inert subjects. Neither accepts the
other's verification result as a substitute. Host-administrator compromise is
outside this identity boundary, as in the existing signer model.

| Participant     | Owns and verifies                                                                                                     | Cannot do in this profile                                                                                   |
| --------------- | --------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| App/UI          | Untrusted proposal and display only                                                                                   | Bootstrap trust, review as owner, call owner-only admission, choose verifier inputs or change either store  |
| Owner verifier  | Separately provisioned seed/policy/transport pins, owner clock/checkpoint, local review and owner fixture snapshot    | Forward an app report as approval, unlock a wallet, reserve a release or invoke installed lifecycle actions |
| Signer verifier | Independently provisioned pins, signer clock/checkpoint, private challenge/replay journal and signer fixture snapshot | Adopt owner/app trust state or time, infer consent from a session, mutate financial state or execute a unit |

### Independent inputs, time and persistence

The harness provisions identical public seed, admission policy and controlled
HTTPS transport policy to each verifier through separate explicit configuration.
Their SHA-256 pins are independently retained outside the app namespace. Neither
peer can provision, replace or repair those pins over RPC. The transport policy
binds the exact seed origin, fixture CA fingerprint and existing no-redirect,
TLS/hostname/size/timeout rules. There is no production origin, downloaded trust
fallback or TLS bypass. Evidence and the four inert artifact files are supplied
in each verifier's private fixture input tree, outside request-controlled paths;
each verifier rehashes them against its independently authenticated descriptor.
No candidate executable or installed data path is accepted as fixture input.

Owner and signer each have a distinct private fixture CAS store, external expected
head and independent monotonic fixture-state revision. Sharing a directory, lock,
head pointer or app-writable parent is refused. A proposed version-1
`HandoffFixtureStore` has one atomic checkpoint containing the role/configuration
binding, embedded trust snapshot and digest, last trusted clock observation,
fixture snapshot revision and replay/inspection records. It is capped at 256 KiB
and 32 records (each at most 4 KiB); exhaustion refuses new sessions without
erasing history. Learned trust and the corresponding replay/result state publish
together through one local CAS, immutable hash-named payload and durable head
replacement. There are no separately committed trust/replay heads to reconcile.
Reuse the existing private-file/locking/interruption primitives and opaque trust
transitions, not the old store's literal whitelist or snapshot wire format. The
new store/adapter is `HandoffFixtureStore`; the existing `FixtureTrustStore` keeps
its separate wire format and 64 KiB limit. The harness injects two role-local
clocks through configuration, never a request field; host time is unchanged.
Each clock must be at least its own persisted last observation. Missing time
confidence after a simulated restore, backward time, missing/corrupt state or an
external-anchor mismatch blocks a new session. The peer's time and HTTP Date are
not clock sources. Whole-store plus external-anchor rollback is still outside
this model; the design does not claim production rollback-resistant storage/time.

Each verifier checks all applicable metadata deadlines and policy `notAfter`
strictly against its own clock at initial verification and final inspection.
Owner review is at most one hour old. A challenge lasts at most 60 seconds and
never outlives known local policy validity. Final inspection uses the earlier of
that challenge deadline, both independently derived authorization/policy expiries
and the review deadline; a receipt may shorten but never extend it. Owner/signer
observation skew above 30 seconds refuses joint
inspection; skew tolerance never extends any local expiry. Exactly-at-deadline
is expired. A clock that cannot satisfy these rules yields recovery guidance,
not a request override or a relaxed validity window. Trusted production clock
and installed recovery remain separate unassigned obligations.

Both stores persist authenticated root/role/observed-release progress before a
later candidate failure is returned. Use opaque verifier transitions and the
existing CAS/interruption rules; an RPC JSON snapshot cannot be reconstructed into
a trusted transition. Keep installed/reserved fixture release floors, originals,
counters, reservations, unknown operations and holds unchanged. Their canary
snapshots are role-owned disposable records; this rehearsal never reads a real
vault or financial journal. A unit sequence must exceed both installed/reserved
fixture floors and must not conflict with either observed descriptor floor.
Identical already-observed units may retry without reserving a sequence.

### Exact identities and wire boundary

`UnitID` is SHA-256 of the exact authenticated release-unit bytes. Define
`HashTuple(domain, values)` as SHA-256 of UTF-8 domain, NUL, then, for each value,
its unsigned 32-bit big-endian UTF-8 byte length followed by those bytes. Hashes
use lowercase hex, integers canonical unsigned decimal, and values have the
fixed order below. No JSON serialization or delimiter ambiguity defines identity.

`AuthorizationID = HashTuple("fased-app-fixture-handoff-authorization:v1", ...)`
binds, in order: seed digest, policy digest, transport-policy digest, repository,
channel, target OS, target architecture, release sequence, final root digest,
timestamp digest, snapshot digest, targets digest, descriptor digest, evidence
digest and UnitID. Both verifiers must obtain the same AuthorizationID and UnitID
from their own inputs. Distinct local checkpoint heads and root catch-up histories
are allowed; the final authorized bytes must agree. A fresher independently
observed authorization requires a fresh request and review, even for the same unit.

Messages use strict bounded JSON, at most 64 KiB each; reject duplicate/unknown
fields, unsupported versions, noncanonical IDs and extra capabilities. Metadata,
artifact bytes, paths, seed/policy documents, trust snapshots, CA roots and clock
overrides are not supplied through this request protocol. Fixed configuration owns
those inputs. Bound observation/review/deadline timestamps are claims checked
against each verifier's own clock, never clock overrides. All IDs/nonces are fresh
cryptographically random 128-bit values.

| Record                    | Required binding                                                                                                                                                                                                                                                                                                                                                                                                             |
| ------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Challenge                 | Profile/mode/binding, challenge ID, signer boot ID, instance ID, signer authority/epoch digest, signer fixture snapshot digest/revision, signer checkpoint-head digest, required seed/policy/transport pins, supported handoff capability, signer observedAt and deadline                                                                                                                                                    |
| Request                   | Challenge ID, request ID, owner boot ID, instance/authority binding, expected signer snapshot/head from challenge, owner snapshot digest/revision and checkpoint head, AuthorizationID, UnitID, target, sequence, owner observedAt and `reviewedAt`                                                                                                                                                                          |
| RequestDigest             | `HashTuple("fased-app-fixture-handoff-request:v1", ...)` over profile, mode, repository, channel, challenge ID, request ID, both boot IDs, instance ID, authority/epoch digest, owner then signer snapshot digest/revision, owner then signer baseline checkpoint head, AuthorizationID, UnitID, OS, architecture, sequence, owner observedAt, signer challenge observedAt, reviewedAt and challenge deadline, in that order |
| Signer inspection receipt | Exact RequestDigest and request/challenge IDs, AuthorizationID/UnitID, signer boot/authority, baseline and resulting signer checkpoint heads, signer snapshot revision, observed sequence, signer observedAt/deadline and outcome; every execution capability false                                                                                                                                                          |
| Owner completion record   | Exact request and signer receipt digests, same-unit/authorization identities, owner baseline/resulting checkpoint heads, both fixture snapshot revisions, final local observation time and outcome; every execution capability false                                                                                                                                                                                         |

Checkpoint heads identify complete role checkpoints, not a shared financial
ledger or raw peer-supplied trust state. Publication-head wrappers are outside
the hashed payload they identify: do not embed a checkpoint's own resulting head
inside that checkpoint or create a self-referential receipt hash. A challenge
record is persisted first; its returned wrapper supplies that resulting signer
head for the request's expected-head check. Receipt payloads are likewise durable
before their resulting-head wrappers are returned. Each verifier follows only
its own protected CAS lineage; the peer's claimed lineage is not local authority.

The fixture transport models the existing owner-only channel: the signer checks
the configured owner kernel UID before issuing a challenge; the owner verifies
the signer kernel UID before sending a request. The app UID is always denied.
A fresh connection-bound challenge permits one bounded request. Neither a public
receipt nor its hash is a bearer credential; it cannot authorize a later action.
Same-UID in-process tests demonstrate protocol logic only, not this OS boundary.
No wallet password, recovery secret or signing key enters the protocol.

### Ordered inspection and recovery

1. The owner verifier reads its own pinned configuration/checkpoint, refreshes
   the TUF chain, verifies provenance and its inert files, and persists learned
   trust. It obtains an authenticated signer challenge with signer-owned fixture
   identity/snapshot/head. No app-provided inventory is adopted.
2. The owner compares the fixture identities/pins, builds the immutable request,
   and displays target, sequence, all four subjects, build/source/workflow,
   AuthorizationID, limits and expiry in its trusted fixture terminal. A local
   fixture acknowledgment binds the exact RequestDigest and reviewedAt. No
   app-session acknowledgment or real wallet password substitutes for it.
3. The signer authenticates the channel/challenge, rejects replay or stale
   boot/epoch/snapshot/head, independently refreshes/verifies using its own clock,
   pins, evidence and inert bytes, and recomputes both identities. Any mismatch
   or change to the requested fixture inventory denies inspection. An active
   held fixture intent permits status only; this protocol cannot bypass it.
4. The signer persists learned trust and a replay/inspection record durably,
   then returns `checked`, `progress-only`, `rejected` or `expired`. `checked`
   means inert-unit inspection only. No financial journal, release reservation,
   staging area, installed sequence, service or wallet is mutated.
5. The owner verifies the authenticated receipt against its immutable request,
   rechecks its own current authorization/time and fixture inventory, and
   durably records `inspection-complete` only while the same authorization and
   context are valid. A changed root/metadata/policy/unit/boot/epoch/inventory
   requires fresh review. Unchanged authorization may acquire newer local clock
   observations/checkpoint heads by CAS; those descendants do not rewrite the
   reviewed request or extend its deadline. No automatic resume follows.

Never hold one participant's store lock while waiting for network or the other
participant. Each local write uses its own expected head/revision; a concurrent
writer causes a conflict, not a merged or reset trust state. A fresh session is
permitted only after the pending local outcome has been safely resolved.
Before any fresh verification, atomically record `verification-pending` for the
active attempt. Competing attempts refuse while that marker is active. A normal
outcome atomically publishes its opaque learned-trust transition and resolves
that marker. CAS failure never authorizes dropping learned progress: retain the
pending hold and block new sessions until the protected local result is recovered.
After a crash before that result was durable, the amount of newly learned trust
is unknown; historical status is available, but replaying the old checkpoint
against a possibly withholding server cannot clear the marker. A durable exact
result can complete local publication; otherwise independent fixture recovery or
a newly declared disposable experiment is required. No app acknowledgment or
clock reset clears an interrupted-verification marker.
There is no distributed financial commit to roll back. One verifier may learn
newer trust while the peer fails: retain both local histories and converge forward
through fresh independent verification. Never restore the newer store to the
peer's older state just to make digests agree.

Persist a consumed challenge plus exact request/result before sending a signer
receipt. Loss of the reply permits an authenticated read-only status lookup of
that exact record. Within the original connection/boot/deadline and unchanged
context, retry returns the identical result without a second admission. After
expiry or either process restart, historical status remains audit data; a fresh
challenge and review are required. Interrupted local checkpoint writes use the
existing matching-temp/CAS recovery; missing/corrupt published bytes or lost
external anchors block new admission with independent fixture reprovisioning
guidance. Reprovision only a newly declared disposable experiment, never reset an
existing authority. Original keys/history/counters/holds are never reset or copied
back from a snapshot. Actual pre/post-installed-commit recovery is outside this
profile and remains held behind the separate lifecycle design.

### Reader compatibility and implementation boundary

The new fixture capability is `fixture-release-handoff-v1`; both peers must
explicitly advertise and validate protocol 1 under the fixture profile. Unknown
or newer readers and absent capabilities refuse. No downgrade to the current
`IntentRequest{Manifest, Trust}`, owner password flow, lifecycle-3 bootstrap or
lifecycle-4 install path is allowed. Existing profiles continue their current
engineering behavior without gaining this capability. The inert unit's owner
entrypoint value 4 describes a fixture subject; it cannot certify a new installed
reader. Production lifecycle/schema numbering and the authenticated upgrade into
that reader are UNASSIGNED/BLOCKED, not implicitly version 5 or a feature flag.

Do not add a route into BeginIntent, reserve/stage/pause/backup/install/commit,
unlock, signing, submission or automatic scheduling. Every request/result is
fixture-only, read-only with respect to artifacts/financial/installed state, and
has `installAllowed`, `resumeAllowed`, `updaterEnabled`, `walletSigner` and
`financialExecution` false. Only explicit role-local fixture trust/replay stores
may change. The existing release inspector remains read-only and cannot commit
those stores on behalf of either actor.

### Finite acceptance and future fixture plan

HA-01–HA-15 have named model subtests in the six suites below. These exercise
in-process actor capabilities, independently provisioned fixture inputs and real
private-file CAS operations, not kernel peer authentication. HA-03's actual UID
predicate and HA-16 remain BLOCKED until the separate transport harness runs.

| ID    | Required falsifiable case                                                                                                                                                                                                              |
| ----- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| HA-01 | Two independently provisioned verifiers inspect the identical complete inert pair/helper/installer; all execution capabilities remain false                                                                                            |
| HA-02 | App-written verified report, receipt, inventory, clock, seed/policy replacement or shared/app-writable store cannot authorize inspection                                                                                               |
| HA-03 | Wrong peer UID, app channel, forged server, missing capability, old/new profile, unknown field and oversized message refuse before candidate handling                                                                                  |
| HA-04 | Changed unit/artifact/helper/installer/source/workflow/provenance or mismatched authorization bytes refuse independently at each verifier                                                                                              |
| HA-05 | Clock rollback/untrusted restore, skew over 30 seconds, equality at expiry, stale review and expired challenge refuse without extending deadlines                                                                                      |
| HA-06 | Learned root revocation/newer timestamp survives peer/artifact/provenance failure; withholding the successor or replaying older metadata cannot erase progress                                                                         |
| HA-07 | Replayed nonce/request, changed request under an ID, cross-connection receipt and old boot/authority epoch refuse; audit lookup grants no new authority                                                                                |
| HA-08 | Changed fixture identity/snapshot/revision or active held intent refuses; originals/counters/holds and installed/reserved floors stay unchanged                                                                                        |
| HA-09 | Below-floor or same-sequence/different-descriptor unit refuses; identical observed unit can retry without consuming a financial release reservation                                                                                    |
| HA-10 | Fresh metadata/policy change between review and response requires new review; both actors enforce their own current validity                                                                                                           |
| HA-11 | Concurrent requests/CAS conflicts, mismatched partial writes, corrupt/missing heads and stale external anchors refuse; no lock is held across peer/network waits                                                                       |
| HA-12 | Crash before/after trust write, replay-record publication, response delivery and owner completion recovers durable status without rewinding either store; unknown interrupted learning blocks new admission until independent recovery |
| HA-13 | One participant advances trust while the other fails; forward convergence works without copying peer state or resetting newer knowledge                                                                                                |
| HA-14 | Dropped response yields the identical durable result only under the original live context; after expiry/restart it is historical status only                                                                                           |
| HA-15 | Production/installed paths, real artifacts, mutation flags and every install/resume/wallet/financial/automatic route are unreachable; canary trees remain byte-identical                                                               |
| HA-16 | Disposable Ubuntu distinct-UID processes prove the peer/store boundary; ordinary-user local checks remain SUPPORTING for OS isolation; all generated resources are removed                                                             |

The first code package implements the typed model, deterministic identity tuples,
independent fixture-state adapters and recovery/replay logic in
`internal/update/handoff.go`, `handoff_store.go`, `handoff_test.go` and
`handoff_fixture_test.go`. The new combined checkpoint adapter accepts only opaque
verifier transitions plus typed local replay deltas, compares the embedded prior
trust bytes/head and fixture revision, and publishes one complete checkpoint.
Restart reads only independently anchored private checkpoints, never app/RPC
snapshots. Reuse the current TUF/admission verifiers and CAS primitives; do not modify
installed signer/controller APIs. The six required test entrypoints are:
`TestFixtureHandoffBindings`, `TestFixtureHandoffIndependentInputs`,
`TestFixtureHandoffClockAndReplay`, `TestFixtureHandoffTrustProgress`,
`TestFixtureHandoffCASAndRecovery` and `TestFixtureHandoffNoExecution`.

The ordinary-user completion command first prevents a vacuous pass:

```bash
set -eu
mkdir -p .evidence/p4-handoff-fixture
go test ./internal/update -list '^TestFixtureHandoff' \
  > .evidence/p4-handoff-fixture/test-list.txt
test "$(rg -c '^TestFixtureHandoff' .evidence/p4-handoff-fixture/test-list.txt)" -eq 6
go test -race -json ./internal/update -run '^TestFixtureHandoff' \
  > .evidence/p4-handoff-fixture/tests.jsonl
```

`node scripts/check-update-handoff.mjs` enforces the six exact entrypoints, runs
the race-tested model cases, verifies HA-01–HA-15 model coverage and source hashes,
and retains a receipt/logs while checking generated fixture cleanup. Ubuntu 24.04
CI runs this same model acceptance. This is not distinct-UID acceptance.
HA-03's real kernel-UID predicate remains unproven and is coupled to HA-16. The separate compiled fixture harness below owns bounded transport and
HA-16 on Ubuntu 24.04: three disposable nonzero UIDs, protected role directories,
fixed OS identity setup only, no installed services, real vaults or execution
endpoint. Local subprocesses cannot close that UID gate. A transport CLI/CI addition,
future lifecycle integration, production bootstrap/time/provenance and any
installed mutation require their own explicit checkpoint; none is implemented
or activated by the model. P4-10/11 and the other public gates stay BLOCKED.

### Model API and recovery limits

`NewFixtureHandoffActor` requires explicit disposable configuration, an ordinary-user
private store, independently pinned public inputs and role-local clock/inventory
callbacks. Pins and material are copied; the request cannot replace them.
`NewFixtureHandoffSession(owner, signer)` creates an opaque in-process channel
capability. `Prepare` independently inspects the owner's inputs and invokes its
trusted review callback; `Check` independently verifies signer inputs; `Finish`
refreshes owner authorization before publishing an inspection-only result.
`Status` returns audit JSON that cannot construct an opaque receipt. No API calls
the installed signer, owner helper, financial ledger or lifecycle controller.

The fixture inventory digest uses the same length-prefixed tuple algorithm over
instance, authority/epoch digest, canary digest, installed floor, reserved floor
and active intent, under the profile plus `:inventory`; revision is bound separately.
On actor restart a new boot ID publishes without changing trust or financial
canaries; unresolved verification remains held. This also fences an old actor
that still holds a previous checkpoint anchor. Malformed/rejected requests consume
their model session; a new session requires new review.

`RetryPublication` can retry only an exact opaque in-memory checkpoint publication.
After restart, externally anchored durable results support historical status and
fresh review; an unknown pending verification cannot be cleared or converted from
RPC JSON. Records are bounded including their complete envelope, not just body.
The store preserves old immutable checkpoints and refuses exhaustion/foreign paths
rather than garbage-collecting state. Root/administrator compromise and whole-store
plus external-anchor rollback remain outside this fixture model.

## Compiled owner/signer fixture transport and UID harness design

### Frozen scope and process split

This frozen contract is now implemented as a separate development-only fixture
command, transport and finite harness. Local race tests use compiled test peers;
HA-03/HA-16 require the actual three-UID Ubuntu runner receipt at the exact source.
A local PASS cannot stand in for that receipt or for installed/retail proof.
The existing model APIs and their coupled checks remain independent.
Real wallets, financial execution, installed execution, AI, automatic updates,
production trust and P5 remain disabled. Only disposable keys, controlled HTTPS,
inert `.fixture` subjects and separately declared fixture stores are allowed.

Use a separate development command, `cmd/fased-handoff-fixture`, excluded from
packages, services and all installed app/signer dispatch. Its only roles are
`owner inspect`, `owner status`, `signer serve` and adversarial `probe`; each requires
`--disposable-fixture`, a private role configuration and its independently
supplied `--config-sha256`. No RPC selects a role, UID, input path, clock, trust
pin, review callback or expected local head. Non-Linux transport fails closed.
There is no owner password, vault, wallet key or lifecycle controller dependency.

| Proposed process-local API                   | Boundary                                                                                                                                                                                                                                                      |
| -------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `NewFixtureOwnerClient` / `Inspect(ctx)`     | Takes only the owner actor/configuration, a fixed signer socket endpoint and local review function. Independently verifies before connecting, persists exact review, receives authenticated results and refreshes owner verification before local completion. |
| `NewFixtureSignerServer` / `Serve(ctx)`      | Takes only the signer actor/configuration and its private listener. Owns challenges, live connection contexts, independent verification and durable results. It never receives an owner actor or accesses the owner's store.                                  |
| `fixturePeer` / `authenticatedFixtureResult` | Unexported process-local capabilities constructed only after kernel peer checks and bounded reads on that same live socket. No public JSON decoder, report, digest or status lookup constructs them.                                                          |
| `Audit(ctx, requestID, exchangeDigest)`      | Authenticated, bounded historical lookup. Returns audit data only; cannot complete an owner inspection or reopen a consumed challenge.                                                                                                                        |

Refactor reusable actor-local inspection/publication logic from `handoff.go`;
preserve the existing model APIs/tests. Do not serialize
`FixtureHandoffSession`, its two actor pointers, its private reviewed request,
`ownerExpiry` or `FixtureHandoffReceipt.session`. The current signer's
`r == s.request` check becomes equality against its own issued challenge plus
strict request validation on an authenticated owner connection. It recomputes
authorization/unit identities independently. The owner alone checks its protected
review record; the signer cannot inspect that record across UIDs. Owner UID
authentication identifies the trusted owner process, not proof that an arbitrary
program under that UID displayed a review. Compromise of either trusted role UID
or the administrator remains outside this boundary.

### Role provisioning, time and checkpoint anchors

Provision two separate private input trees with public seed/policy/evidence,
fixture CA, four inert artifacts, clock, inventory, configuration and anchor
files. Owner and signer have different nonzero UIDs and separate directories;
the app has a third nonzero UID and no write access to either tree or ancestors.
Validate real/effective UID, exact role, all three distinct configured UIDs,
canonical nonsymlink paths, ownership/modes and regular-file/size constraints
before creating actors or connecting. Reject root, inherited alternative input
sources, shared store inodes, app-writable ancestors and unknown options.

Each role's configuration is strict bounded JSON, at most 64 KiB, pinned by the
launcher independently of the app. It fixes the fixture binding, all UIDs,
role-specific input/store/socket/clock/inventory/anchor paths, seed/policy/CA
fingerprints and transport capability. Changes require a new declared experiment;
neither RPC nor a fetched document updates configuration. Inputs/files are opened
with the existing contained nofollow/nonblocking rules. Review is a role-local
fixture acknowledgment of exact displayed identities, not an app-provided boolean.
After the owner emits its review summary through an owner-only fixture channel,
the trusted harness checks the expected unit/build/limits and publishes a private
acknowledgment of that exact ExchangeDigest before the review deadline. This
test acknowledgment is not retail consent; adversarial requests never select or
overwrite it. A probe has only a harness-declared endpoint/case configuration;
it cannot instantiate an actor or supply acknowledgment to the owner process.

The clock adapter reads only its role's private `{now, confident}` fixture file;
missing confidence, regression, restore mismatch or invalid bounds blocks new
inspection. It is not an RPC timestamp or HTTP Date. Test mutations are made by
the harness as that role, never by the app. Existing clock, 30-second skew,
one-hour review and 60-second challenge bounds remain; no host-clock changes.

Keep an independently provisioned role anchor outside the checkpoint directory,
binding configuration fingerprint, experiment and expected head. Fresh bootstrap
is explicit and refuses existing stores; transport never adopts a model-only
store or searches for the newest snapshot. Publish the combined checkpoint first,
then atomically advance the private anchor with the exact prior/new head.
A crash between those publications causes anchor mismatch and BLOCKED inspection;
only a retained exact local publication may retry, never arbitrary head adoption.
After restart, missing anchors or unknown pending learning remain held. Fixture
reprovisioning creates a new declared experiment and preserves failed evidence.
Whole-store plus anchor rollback is still not production rollback protection.

### Socket, framing and authenticated exchange

Use Linux pathname Unix stream sockets only. A dedicated signer-owned socket
directory is mode 0711 under a non-app-writable fixture prefix; socket mode 0666
permits the deliberate app-connect denial test. Private role state stays 0700
with files 0600. Directory traversal/connection permission never grants a role.
Before consuming any frame or issuing any challenge, the server obtains
`SO_PEERCRED` on the accepted connection and requires the configured owner UID.
The owner verifies the configured signer UID on its connected socket before
sending even `hello`. Reject missing/mismatched credentials; close unauthorized
connections without a protocol reply or checkpoint change. Check socket/parent
type, owner and inode; refuse foreign/live/stale sockets rather than unlinking
them opportunistically. Explicit fixture cleanup removes only inventoried sockets.
No TCP, abstract socket, inherited descriptor, proxy or same-UID fallback exists.

One frame is a four-byte unsigned big-endian payload length followed by exactly
that many UTF-8 JSON bytes. Reject zero or over-65,536 length before allocation;
use exact bounded reads and existing strict JSON rules for duplicate/case-alias,
unknown/trailing fields, depth and invalid UTF-8. Frame length includes the entire
JSON envelope. Partial headers/bodies, unexpected states and extra frames fail
closed. The envelope has version 1, transport profile
`fased-app-owner-signer-transport-fixture:v1`, mode `inspection-only`, capability
`fixture-release-transport-v1`, the development fixture binding, kind, channel ID
and a kind-specific strict body. There is no generic method or arbitrary payload.

1. After mutual kernel checks, `hello` binds a fresh client nonce, owner boot ID
   and operation (`inspect` or `status`). The server checks the exact profile,
   binding/capability and assigns a fresh random channel ID. A status connection
   produces audit data only and never issues a challenge or changes trust.
2. For inspection, the signer durably publishes its challenge before replying.
   It retains that exact challenge, peer UID, both boot IDs, client nonce,
   channel, baseline head/inventory and absolute deadline in its own context.
   The challenge wrapper supplies the resulting head outside the hashed payload.
   The owner checks pins/identity/time and uses only its own verification result.
3. The owner constructs the existing immutable `FixtureHandoffRequest` plus
   `ownerNotAfter` (its independently derived authorization expiry) and
   `reviewDeadline = min(ownerNotAfter, challenge.deadline, reviewedAt + 3600)`.
   Define `ExchangeDigest = HashTuple("fased-app-fixture-transport-exchange:v1",
profile, capability, repository, channel, channelId, clientNonce, ownerUid,
signerUid, SHA256(exact request JSON), ownerNotAfter, reviewDeadline)`.
   IDs/hashes/integers follow the existing canonical rules. This binds fields
   absent from the old model digest, including the full challenge and channel;
   the legacy RequestDigest remains unchanged. The owner displays both digests,
   unit/authorization, build/subjects, limits and expiry, and publishes the exact
   review/exchange before sending `inspect`. Never change bytes after review.
4. The signer recomputes both digests, compares the full issued challenge and
   live peer/boot/context, checks review/deadline/skew/inventory, and independently
   verifies its own inputs. Claimed owner expiry can only shorten validity;
   signer metadata/policy expiry is independently enforced. The result deadline
   is the minimum of challenge, ownerNotAfter, reviewDeadline and signer expiry.
   Publish pending learning before HTTPS and atomically publish learned trust,
   consumed challenge, exact exchange/result and resolved pending state before
   replying. No store lock spans network I/O or peer waits.
5. `result` binds both digests, context/boots, unit/authorization and the existing
   outcome fields; all five execution flags are false. Its publication wrapper
   contains the resulting signer head outside the persisted result payload.
   The owner accepts it only from its authenticated live connection, checks all
   bindings against its local review and refreshes its own verification/inventory.
   Before completion the owner sends `confirm` on the same connection, binding
   ExchangeDigest and SHA256(exact durable result payload). The signer checks its
   own current boot/head/inventory/time, no pending hold and the unchanged result
   context, then durably records and returns `confirmed` with its publication-head
   wrapper. No fresh admission or deadline extension occurs. The fixture owner deliberately
   rehearses one exact inspection retry and one exact confirmation retry;
   `liveRetryVerified` is reported only after byte-identical responses. The owner checks this
   response and its own unchanged refreshed context before local completion,
   recording result/confirmation digests. This replaces the model's ability to
   inspect the other actor's live private state. Peer heads remain binding/audit
   claims, never local trust snapshots. Confirmation proves that point-in-time
   observation; it does not atomically lock another process until owner commit or
   authorize any later operation.

Initial hello/status and writes have five-second wall deadlines; payload reads
have five seconds after the bounded header. Waiting for a reviewed request may
use the remaining challenge lifetime, never a renewed timeout. The entire
inspection connection is bounded by 60 seconds of local monotonic wall time
from challenge issuance as well as role-local fixture time. Each HTTPS inspection
has a 30-second context ceiling further shortened by remaining validity.
Maximum four accepted connections and one active inspection per signer boot;
unavailable capacity refuses without issuing a challenge. Bound status lookups
to one record and connection frames to hello/challenge, one inspection, one
confirmation, at most two byte-identical retries of either and their replies.
Keep the active inspection slot until close/completion, including final review;
no competing inspection can reuse the checked context. Cancellation closes owned connections,
stops work and preserves any unresolved pending publication.

### Retry, restart and error semantics

Use finite outcomes `checked`, `progress-only`, `rejected`, `expired`; bounded
errors distinguish `unauthorized` (no wire reply), `unsupported`, `malformed`,
`busy`, `conflict`, `held` and `unavailable`. Errors/status cannot carry executable
paths, configuration, private file contents or credentials. `checked` never
authorizes installation. Verification/publication errors retain learned trust
and pending holds; do not turn an error into a successful empty response.

Only a byte-identical inspection/confirmation retry on the original live connection,
exchange/boots and deadline may return the identical durable result without
another verification. A changed request consumes the channel. A dropped socket
cannot be resumed using JSON, digests or a status result. A new authenticated
connection may query that record as historical audit; it needs fresh challenge
and review for inspection. Restart changes the local boot ID and fences old
connections/results while preserving trust, floors, canaries and pending holds.
One peer's failure never rewinds the other's learned state. New authorization,
epoch, inventory or incompatible head requires fresh review; exact local
publication retry follows the existing opaque store rules. No distributed
financial commit, recovery reset, release reservation or automatic resume exists.

Transport records retain the complete 4 KiB envelope bound, 32-record and
256 KiB checkpoint limits; no transport payload silently enlarges the store.
Transport stores use a separately declared namespace/configuration anchor;
they are not a migration of model or installed stores. Both transport capability
and existing handoff capability must be explicitly supported; no downgrade to
legacy owner RPC, lifecycle-4, current opaque receipt JSON or a same-UID path.
Production lifecycle/schema reader numbering remains UNASSIGNED/BLOCKED.

### Finite compiled and Ubuntu UID acceptance

The following twelve cases are the entire new transport inventory. Every case
must retain input hashes, process identities, actual credentials/result, store
before/after hashes and cleanup outcome. Existing HA-01–HA-15 model proof stays
a distinct evidence class; no fixture result closes a public P4 gate.

| Case  | Required proof / mapping                                                                                                                                                                                                    |
| ----- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| UT-01 | Separately compiled owner and signer independently inspect the same inert unit and persist completion; all execution flags false, artifacts/canaries/floors unchanged. HA-01/15.                                            |
| UT-02 | Actual app UID can connect but receives no challenge/reply, cannot inspect/status/write role trees; configured owner UID succeeds. HA-03/16.                                                                                |
| UT-03 | Owner rejects a fake signer under app/owner UID before sending hello; root, wrong/equal/missing UIDs, foreign socket or shared/app-writable paths refuse. HA-02/03/16.                                                      |
| UT-04 | Zero/oversized length, duplicate/unknown/case-alias JSON, truncation/slow frames, wrong profile/capability/version and state/order refuse with bounded resources. HA-03.                                                    |
| UT-05 | Mutated request/expiry/exchange, wrong channel/client nonce, copied response/status or cross-connection replay never completes; identical live retry returns the same result once. HA-07/14.                                |
| UT-06 | Independent input/pin/byte/provenance mismatch or owner review denial refuses; changing only one role's fixture inputs changes only its own verification result. HA-02/04.                                                  |
| UT-07 | Role-local backward/uncertain time, skew, equal expiry, expired review and monotonic wall deadline refuse without extending validity. HA-05.                                                                                |
| UT-08 | Restart either process fences old boots/results; final owner refresh and same-connection signer confirmation reject changed authorization/inventory or signer restart after result, preserving both histories. HA-07/08/10. |
| UT-09 | Kill/drop after pending, result publication and before/after reply; exact durable status is historical, unknown learning/anchor gap holds, no financial/store rewind. HA-06/12/13/14.                                       |
| UT-10 | Concurrent connection/CAS conflict, held intent, missing/corrupt anchor/checkpoint and exhausted caps fail closed; no reset, shared-lock wait or unbounded worker. HA-08/09/11.                                             |
| UT-11 | Command rejects absent fixture/config pins and production/artifact/execution options; ordinary packages/services/core/signer expose no new route, unsupported platforms refuse. HA-15.                                      |
| UT-12 | Three actual distinct nonzero UIDs on Ubuntu 24.04; project code never root; owned processes, sockets, generated keys/fixtures and identities cleaned, foreign resources preserved. HA-16.                                  |

The local runner uses compiled Go test child processes with the current nonzero
UID and a private `_test.go` same-UID constructor. It is SUPPORTING for transport
logic and cannot assert UT-02/03's distinct-UID or UT-12 predicates. That override
is private test construction only: the fixture command and UID runner never
accept it as a configuration/CLI fallback.

The UID runner is restricted to an explicitly opted-in disposable Ubuntu 24.04
GitHub runner, not owner hosts/VPS. Start as the normal runner user, check
`sudo -n true` once and stop if unavailable. Provision three new locked nonzero
UIDs and a random inventoried scratch prefix; refuse collisions/adoption.
Only fixed validated OS tools run privileged (identity/directory/file ownership
setup and credential drop). Compile under the ordinary user, stage hash-verified
read-only binaries in a non-role-writable prefix, then launch every project
process only after `setpriv` drops real/effective/saved UID/GID, supplementary
groups and capabilities with no-new-privileges. Child code asserts nonroot
identity. No root shell, `sudo node/go`, service manager, installed vault,
AppArmor/network policy, owner identity or real home/data mutation is allowed.
Controlled HTTPS serves only loopback disposable fixtures; retain no key in logs.

Register cleanup before creating resources, including failure/cancellation traps.
Stop exact recorded child PIDs with start-identity checks, save small logs/receipts,
then remove only inventoried scratch/resources and newly created identities after
verifying UID/path ownership. Never recursively delete a supplied path or adopt
an existing account/socket. Cleanup failure is FAIL, not HA-16 PASS. A hard runner
loss remains BLOCKED until disposal is established. Preserve failure evidence;
do not reinitialize a failed store in the same experiment to manufacture success.

### Implemented files and completion commands

The connected fixture package is in `internal/update/handoff_transport.go`,
`handoff_transport_linux.go`, `handoff_transport_unsupported.go`,
`handoff_transport_test.go`, `handoff_transport_linux_test.go`, with anchor hooks in
`handoff.go` and the unchanged bounded combined-store primitives. It adds
`cmd/fased-handoff-fixture/main.go` and `main_test.go`,
`scripts/check-update-handoff-transport.mjs` and
`scripts/check-update-handoff-uid.mjs`. `scripts/handoff-transport-evidence.mjs` shares only fingerprint/receipt helpers.
`.github/workflows/checks.yml` owns a dedicated Ubuntu 24.04 UID job and aggregate dependency, without changing installed
signer/model acceptance, permissions or existing timeouts. Keep documentation in
this section and the existing Fased planning files. Do not touch installed command,
RPC, packaging, service or controller dispatch to expose the fixture command.

Six exact new test entrypoints are required:
`TestFixtureTransportBindings`, `TestFixtureTransportFraming`,
`TestFixtureTransportIndependentPeers`, `TestFixtureTransportReplayRecovery`,
`TestFixtureTransportBounds` and `TestFixtureTransportNoExecution`.
The ordinary-user runner enforces this exact list before a focused race run,
compiles the fixture command once for rejection/argument checks and the race-test
child binary once for local peers, checks absent installed routes and runs the
finite local cases with their UID predicates explicitly SUPPORTING/BLOCKED.
It preserves sources/receipts and cleans generated resources. The UID runner
reuses its matching binary or compiles once in CI, independently proves all twelve
cases and records the three UIDs/kernel credentials. No matching test is a failure.
Freeze implementation inputs before running the coupled model/update/command
checks once; retain the existing six model entrypoints and run portability compile
checks for changed commands/stubs. Reuse unrelated UI/legacy evidence when valid.

These development-only commands require explicit fixture opt-in. Their exact
receipts and source identities, rather than this document, establish test results:

```bash
set -eu
test -f scripts/check-update-handoff-transport.mjs
test -f scripts/check-update-handoff-uid.mjs
FASED_HANDOFF_TRANSPORT_FIXTURE=1 node scripts/check-update-handoff-transport.mjs
```

On the disposable Ubuntu CI runner only, after local checks:

```bash
set -eu
test -f scripts/check-update-handoff-uid.mjs
test "${GITHUB_ACTIONS:-}" = true
test "${RUNNER_OS:-}" = Linux
FASED_HANDOFF_UID_CI=1 node scripts/check-update-handoff-uid.mjs
```

Both scripts must self-enforce the stated flags/host/identity boundaries, finite
cases and source/binary hashes. Completion receipts live in
`.evidence/p4-handoff-transport/` and `.evidence/p4-handoff-uid/`, with exact
source/tree, cases, OS/kernel UID proof, zero-execution and cleanup. Missing/failed receipts keep the corresponding compiled/UID predicate BLOCKED.
Even future fixture PASS does not authorize installed integration, production
bootstrap/time/provenance, real keys/funds, public updates or P5.

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

## Installed release-admission and reader-bootstrap compatibility design

**P4 design contract, revision 1, 2026-10-20.** The non-installed model below
rehearses this contract. No installed RPC, reader conversion, configuration,
service or acquisition route is added here.
Real wallets, financial execution, installed execution and automatic updates stay
disabled. The compiled fixture transport is reusable engineering evidence, not an
installed authority. Public P4 remains ten PASS/six BLOCKED; P5 is not authorized.

This section connects the [production operations proposal](#production-release-trust-operations-specification),
[independent handoff](#independent-ownersigner-fixture-admission-handoff-design) and
[compiled fixture transport](#compiled-ownersigner-fixture-transport-and-uid-harness-design)
to the existing immutable Linux helpers. It freezes the compatibility and safety
requirements below. Production profile/capability strings, lifecycle entrypoint,
wire version and trust/replay schema numbers remain **UNASSIGNED/BLOCKED** until
review of the implementation contract. Neither the fixture's version 1 nor
lifecycle 4 is an implicit allocation. No production dependency, seed, credential,
feed, publisher or activation is created or authorized.

### Current entrypoints and authority boundary

Paths below are relative to the source repository. `root` means the verified
managed installation root, never a caller-selected replacement path.

| Current surface                                                                                           | Verified existing behavior                                                                                                                                                                                                                                                                          | Future compatible seam / refusal                                                                                                                                                                                                                                                                                   |
| --------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `internal/update/compatibility.go:CurrentLifecycle`, `internal/signer/update.go:UpdateContract`           | Core lifecycle entrypoint 4/stage format 1; signer protocol 1 and explicit operation-reader features. These describe disposable development installation.                                                                                                                                           | Add separately reviewed admission and bridge capabilities on both readers; changing a feature list or treating lifecycle 4 as production support is forbidden.                                                                                                                                                     |
| `cmd/fased-app/update_owner.go:ownerUpdatesCommand`, `conductUpdate`                                      | Immutable helper checks owner UID, root-owned config/executable, installed manifest and shared owner lock. PREPARE reviews a development envelope and current snapshots.                                                                                                                            | Independent release inspection must precede any future prepare; `IntentRequest{Manifest, Trust}` cannot carry production approval. Existing PREPARE is not a conversion/admission fallback.                                                                                                                        |
| `internal/signer/rpc_linux.go:LoadConfig`, `CheckOwner`, `call`, `accept`                                 | Root-owned no-symlink config topology; client executable must have a protected root-owned path. Kernel peer UID is checked in both directions before the challenge/request; owner/app channels are separate. Server authentication currently establishes UID, not an attested peer executable hash. | Preserve all checks. Bind the installation ID, root/config digest and locally verified immutable helper/signer inventory as well as kernel UID; no app proxy or downloaded verifier gets direct owner-channel access. Exact helper provenance is a local immutable-file check, not a new remote-attestation claim. |
| `cmd/fased-app/update_install_linux.go:delegateInstallation`, `ownerInstallation`                         | Current helper or fixed verified controller selected by exact intent; separate INSTALL/COMPLETE reviews and live/cold preflight.                                                                                                                                                                    | Admission completion is inspection only. A later, separately authorized controller integration must revalidate it against the exact intent, current services, recovery index and financial holds. No controller calls in this design or its first model package.                                                   |
| `internal/custodyinstall/{transaction,completion}_linux.go`                                               | Durable controller phases, compatible executable rollback, independent installed-sequence commit and current-state verification.                                                                                                                                                                    | Preserve authoritative financial history, consumed reservation floor and committed installed floor. Release trust cannot repair, restore or overwrite these records.                                                                                                                                               |
| `cmd/fased-app/bootstrap_linux.go:prepareBootstrapCommand`                                                | Exact development-signed target runs as owner; inventory is delegated through installed immutable predecessor. New helper is published only after protected BOOTSTRAP review.                                                                                                                       | This is not independent authentication of a public downloaded verifier/root. A future conversion requires an independently authenticated verifier/seed before its execution; same-origin hashes or a candidate's own envelope are insufficient.                                                                    |
| `internal/update/bootstrap.go:VerifyBootstrap`, `internal/custodyinstall/bootstrap_linux.go:runBootstrap` | Exact lifecycle 3 → 4 and known completion-reader feature only; predecessor-readable maintenance, no schema/protocol/topology migration, new-reader cold checks, no installed-release commit.                                                                                                       | Keep this finite bridge unchanged. It cannot admit lifecycle 4 → a future admission reader. Define and review a separate finite bridge before any new reader runs; never broaden the existing predicate generically.                                                                                               |
| `updates status` and shared UI/CLI proposals                                                              | Read-only reports and review-only preferences; app metadata has no signing or update consent.                                                                                                                                                                                                       | Show unsupported reader, trust/time/anchor hold and exact recovery action. A displayed receipt, chat action, notification, policy window or password sent via core cannot authorize inspection or installation.                                                                                                    |
| `cmd/fased-handoff-fixture`, `internal/update/handoff_transport*.go`                                      | Standalone disposable profile, inert artifacts, private role inputs and bounded compiled transport; excluded from installed dispatch/packages.                                                                                                                                                      | Do not route its flags/configuration/ack files into installed custody. Reuse protocol invariants through separate typed adapters; fixture mode can never promote stored state to production.                                                                                                                       |

The threat boundary remains a compromised app UID/network/input versus separate
owner and signer identities. The ordinary owner account and host kernel/root are
trusted. UID checks and immutable files do not defend against a compromised
owner account, kernel or root. Never run project code as root. Only fixed trusted
OS utilities may publish verified immutable files in a later authorized bridge.

### Independently owned inputs and durable records

Each role loads its own inputs before any exchange. Public bytes may be identical;
trust decisions and mutable stores must be independent. An untrusted locator can
name bounded evidence but cannot supply trust, paths, executable instructions or
time confidence. App-owned SQLite, Downloads files, UI sessions and peer JSON
cannot provision or reset any authority below.

| Input / record                         | Owner helper                                                                                                                           | Signer                                                                                                   | Binding and failure rule                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| -------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Installation identity and capabilities | Verify root-owned installation/config, running helper hash and supported contract; acquire existing owner lock.                        | Verify root-owned config, own running signer hash, own UID and operation-reader contract.                | Exact installation ID/UIDs/root/config digest and both running-reader hashes; reject foreign, mixed, unknown or missing capabilities before evidence or secret exchange. A pinned root-owned executable remains required in addition to transport UID.                                                                                                                                                                                                                                            |
| Bootstrap seed                         | Independently authenticated installer/verifier provisions a protected public seed descriptor.                                          | Independently load the provisioned signer seed descriptor; never accept the owner's request as its seed. | Pin exact root bytes/digest, repository immutable identity, channel, minimum checkpoint and transport policy. Initial authentication mechanism/distribution remains production BLOCKED; no trust-on-first-use or development promotion.                                                                                                                                                                                                                                                           |
| Release/provenance policy              | Locally load immutable reviewed policy and its digest.                                                                                 | Locally load its separately provisioned copy.                                                            | Exact repository/channel/source/workflow/builder/attestation identity, target, complete unit and artifact bounds. Pin changes require a distinct reviewed trust migration, never ordinary release consent.                                                                                                                                                                                                                                                                                        |
| Transport/evidence/artifacts           | Fetch and verify metadata/provenance and exact bytes against local pins.                                                               | Independently fetch/read and verify the same unit against its pins; no owner verdict forwarding.         | Use the existing production-spec transport limits/redirect rules once implemented. Four explicit roles: core, signer, owner helper, installer. If helper and core share bytes, bind both roles explicitly; never silently omit installer/helper or execute candidate bytes to inspect them. No fixture artifact limit becomes a production artifact policy.                                                                                                                                       |
| Time and freshness                     | Local clock policy, persisted UTC high-water mark and monotonic live deadline.                                                         | Separate reads, persisted high-water mark and monotonic deadline under signer control.                   | Use each role's own confidence result and earliest applicable metadata/policy/review expiry; equality is expired. Peer timestamps cannot establish confidence or extend expiry. Both roles share a host clock, so this is not two independent time authorities. Regression, uncertain boot/restore or unavailable trusted freshness holds admission. Production confidence/rescue source is UNASSIGNED, not solved by `time.Now()` or an OS sync flag. Controlled clocks are model evidence only. |
| Combined trust/replay checkpoint       | Owner-private 0700 namespace, 0600 bounded records; root/role versions, observed sequence, pending/result/review and local completion. | Separate signer-private equivalent; never share the owner store/lock.                                    | Strict CAS, fsync/rename and exact versioned records. Trust learned before later failure is retained, including a peer failure. Observed release sequence is separate from financial reserved/installed floors. No latest-snapshot adoption, JSON import or empty-store reset.                                                                                                                                                                                                                    |
| External expected-head anchor          | Owner-private anchor outside the checkpoint store, bound to installation/role/seed/policy/profile.                                     | Signer-private independent anchor outside its checkpoint store.                                          | Verify on every load, advance only after durable checkpoint publication. Missing/corrupt/mismatched/gapped anchor holds; only an exact live retained publication may retry. Same-disk anchors protect against partial/store-only rollback, not rollback of the entire host or disk loss; P4-15 stays BLOCKED.                                                                                                                                                                                     |
| Inventory and authorization            | Fresh private core probe plus signer-authenticated challenge/state, protected local terminal review.                                   | Fresh signer-owned authority epoch, financial state/holds, boot ID, inventory and challenge.             | Bind actual snapshots and revisions, active intent, unknown outcomes, reserved/installed floors and recovery holds. Cached controller receipts/app reports cannot certify current state. Any concurrent transition invalidates the reviewed exchange.                                                                                                                                                                                                                                             |

Root-owned seed/policy files are public trust inputs, not wallet secrets. Mutable
role checkpoints/anchors must be writable only by their own ordinary role; they
are not root-written on every inspection. Provisioned directory names, schema
IDs and seed/policy activation records need reviewed migration rules before
installed implementation. They are not new paths accepted by existing commands.
Use separate namespaces for model, compiled fixture and future installed state;
no in-place adoption. A lost trust store requires an independently authorized
trust-recovery procedure while retaining financial history and holds; wallet
recovery or an app-password reset cannot recreate admission authority.

### Exact-unit review and final confirmation

A future installed exchange must be an explicitly negotiated inspection protocol,
with the compiled transport's bounded framing, exact retry, role separation and
kernel checks. Its symbolic profile/capability/version must refuse until allocated
by review. Legacy owner RPC cannot tunnel new frames or infer support from
protocol 1. The signer must reject unknown actions before any trust-state write.
No owner password, wallet secret or review authorization is forwarded by core.

1. Under the owner lock, both roles independently validate their configured
   identity, pins, time and anchors. Negotiate both explicit capabilities and
   reader contracts on an authenticated connection; a signer boot ID and fresh
   one-use challenge fence prior exchanges. Missing support stops before network
   acquisition, pause, reservation or maintenance mutation.
2. Each role independently verifies release metadata, source/build provenance and
   all four artifact roles. Persist a pending-verification record before an
   operation that may learn trust; persist every known outcome/learned state
   atomically with its replay head. Never discard trust learned because artifacts,
   provenance, a peer or owner consent later fail.
3. The immutable owner helper shows a complete local action card in the trusted
   terminal: inspection-only effect, repository/channel, source/build identity,
   authorization/unit digests, sequence/target, four role hashes/sizes/contracts,
   current reader/financial snapshot bindings and exact deadline. Explicit local
   consent binds the full exchange digest. UI may display a copy but cannot
   approve. Fixture ack files and `--approval-stdin` are test-only, never installed
   consent. INSTALL and COMPLETE remain separate later reviews.
4. Bind the domain/profile/capability, installation/config, both reader hashes and
   contracts, seed/policy/transport pins, both boot IDs/nonces, challenge/request,
   authorization/unit/provenance identities, platform/sequence, both baseline
   checkpoint heads, authority epoch, snapshot revisions, review digest and
   earliest expiry into the canonical exchange. The future schema must list exact
   field order/encoding and resource caps before a wire number is assigned;
   canonical JSON/status alone cannot mint a typed live authorization.
5. The signer independently rechecks its inputs and records its result durably.
   The owner independently refreshes its own evidence/time/inventory, then requests
   final confirmation on the same live connection. The signer confirms unchanged
   boot/challenge/authority/inventory/heads and unexpired review, recording its
   confirmation before reply. Owner completion binds that exact response. Exact
   live retries are idempotent within original bindings/deadline, never new
   approval or extra validity. Fresh-connection status is historical only.
6. Return inspection-only completion, with install/reserve/pause/unlock/resume,
   financial execution and automatic update authority all false. This is a
   point-in-time result, not a cross-process financial commit or enduring lease.
   Any later installation must recheck current pins, freshness, reviewed unit and
   authoritative financial state through a separately reviewed adapter. No such
   installed adapter is authorized or implemented here.

### Finite reader compatibility map

| Case  | Installed readers / state                                                                                | Admission and conversion decision                                                                                                                                                                                                  |
| ----- | -------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| RC-01 | Lifecycle 1/2 or unknown/unsupported schemas                                                             | Refuse unchanged. No generic bootstrap, forced feature addition or financial snapshot restore.                                                                                                                                     |
| RC-02 | Supported lifecycle 3, exact known predecessor state                                                     | Existing disposable 3 → 4 bridge only. Its engineering proof is reused at the recorded source; production admission remains unsupported afterward.                                                                                 |
| RC-03 | Current lifecycle 4, old development protocol/features                                                   | Existing disposable lifecycle operations remain as documented; new admission refuses. No fallback to `fixture-update-prepare` or to fixture CLI.                                                                                   |
| RC-04 | Lifecycle 4 → proposed admission-capable reader                                                          | New bridge profile/numbering, independently authenticated seed/verifier and explicit predecessor/target contract are required. All remain BLOCKED for installed execution; current `VerifyBootstrap` cannot be reused as approval. |
| RC-05 | One new reader and one old/mismatched reader                                                             | No admission, no new journal records and no financial actions. Recover through the exact recorded bridge with cold validation; never try an unreviewed protocol downgrade.                                                         |
| RC-06 | Both future readers, explicitly reviewed matching capabilities and readable histories                    | Permit inspection only after all independent bindings pass. This row defines a future predicate, not current support or installation consent.                                                                                      |
| RC-07 | Active update/bootstrap, pending unknown verification, recovery hold or inconsistent financial inventory | No new admission/conversion. Report the exact existing recovery identity and preserve its exclusive slot/holds. Recovery may inspect durable status without creating approval.                                                     |
| RC-08 | macOS/Windows/native custody or unsupported architecture/topology                                        | Refuse installed admission/conversion until that platform's identity, signing, service and recovery contracts have acceptance. Cross-compilation and ordinary desktop proof do not close this row.                                 |

A future RC-04 bridge must be a separate transaction from release admission. It
binds exact predecessor bytes/contract, target reader bytes/contract, provisioned
seed/policy identities and one maintenance intent. For the first bridge, preserve
financial formats, protocol/topology, all originals/history/counters/authority,
reserved and installed floors; require no active financial/update intent or
unknown outcome. Unlike today's narrow prototype, do not assume installed
sequence zero: nonzero committed history must be cold-verified or refused by the
explicit support matrix, never reset to zero.

Before changing readers, use only predecessor-readable maintenance records and
an exclusive metadata pause/recoverable app backup. The new authenticated reader
alone performs cold history validation under the signer UID; no old reader is
restarted on an unproven history. Do not append new admission records into the
financial operations journal. Seed/store provisioning and new-format publication
need a durable forward-only boundary with old-reader service start fenced.
After that boundary, recovery uses the new reader; never roll back trust learned
by either role. Before it, only a positively verified predecessor-compatible
executable rollback is permitted. Reject a bridge that cannot prove this boundary.
A healthy bridge converges immutable installer records and aborts only its exact
maintenance intent, leaving the consumed reservation floor and wallet lock.
It creates neither an installed release nor an admission result. A later release
needs fresh exact-unit inspection and separate protected installation reviews.

### Recovery and crash compatibility

| Durable / observed state                                                | Required disposition                                                                                                                                                                                                    |
| ----------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| No pending record or known clean denial                                 | New inspection requires a fresh authenticated challenge/review; no mutation on unsupported negotiation.                                                                                                                 |
| Pending before acquisition; outcome unknown after death/disconnect      | Retain pending and learned-state uncertainty as a hold. No older checkpoint replay, automatic reset or guessed failure/success. Only separately reviewed recovery can resolve uncertainty.                              |
| Known result/checkpoint written, anchor not advanced                    | Preserve both files. Only the exact still-live publication can retry its CAS/anchor advance; after restart, require recovery rather than adopting the newest snapshot.                                                  |
| Signer result/confirmation durable, reply lost                          | Exact original live retry may recover the identical reply within its deadline. After restart/connection loss, status proves history only; fresh review cannot clear an unresolved hold.                                 |
| Owner completion missing or peers disagree                              | Neither peer overwrites the other's history. Completed inspection is not financial authority; reconcile exact stored identities or hold before a new challenge.                                                         |
| Evidence expires/revokes, clock regresses, epoch/inventory/head changes | Preserve learning/results and refuse new completion; no extension based on reviewed-at time, status or maintenance retry. Existing exact controller recovery is a different authority, not fresh release admission.     |
| Mixed binaries or interrupted reader bridge                             | Select only the immutable recorded runner; stop both services, cold-validate with the new reader, then follow the durable pre-boundary rollback or forward-only decision. Preserve foreign files and unknown histories. |
| Installed commit already durable or later financial history exists      | Never restore old financial state, clear holds or reduce installed/reserved floors. Trust recovery and executable recovery cannot undo a committed release or authoritative history.                                    |

This is bounded single-host coordination, not distributed atomicity. No peer holds
its mutable store lock while waiting on network/owner input or another role.
Production record sizes, retention/exhaustion handling and wall-clock bounds must
be explicitly assigned and tested with the wire/schema contract; the first
non-installed model retains the existing 256 KiB/32-record/4 KiB envelope fixture
limits and refuses exhaustion rather than pruning uncertainty. Its 64 KiB frames
and controlled clocks do not implicitly define installed storage/time policy.

### Finite acceptance and file ownership

All IA rows below remain **BLOCKED** for installed runtime. The implementation
below closes only their non-installed model predicates. Existing
UT/HA results remain fixture engineering proof; no IA row can close public
P4-10/11, native/VPS, portable-recovery or retail gates by inference.

| Case  | Falsifiable predicate / eventual evidence                                                                                                                                                                                       |
| ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| IA-01 | RC-01–RC-08 classification is total; unsupported/missing capability or equal role UIDs refuse before any acquisition or state mutation.                                                                                         |
| IA-02 | Substituted verifier/seed, same-origin checksum, development promotion, foreign instance/config/helper or mixed reader hashes cannot establish trust.                                                                           |
| IA-03 | Each role independently loads pins/time/evidence; mismatch in just one role prevents joined completion and preserves the other's learning.                                                                                      |
| IA-04 | Missing/mutated any of four artifact roles, provenance/source/builder or platform/sequence binding refuses without executing candidate bytes.                                                                                   |
| IA-05 | App UI/status/legacy request, copied JSON, fixture config/ack or owner-password forwarding cannot create exact terminal consent or a live authorization.                                                                        |
| IA-06 | Full exchange mutation, wrong boot/nonce/epoch/inventory/head, cross-connection replay and old capability downgrade refuse; actual Linux UIDs required later.                                                                   |
| IA-07 | Independent expiry/equality, backward/uncertain clock, boot/restore and delayed owner review refuse; peer time never extends validity.                                                                                          |
| IA-08 | Pending-before-learning kill/disconnect holds; known rejection retains learned root/role/observed floors; no reset/import/latest-snapshot recovery.                                                                             |
| IA-09 | Checkpoint/anchor publication cuts, CAS conflict, corrupt/missing anchor and store exhaustion hold; only exact live retry is idempotent.                                                                                        |
| IA-10 | Signer result/confirmation lost reply and owner death/restart cannot convert historical status into fresh approval or installation.                                                                                             |
| IA-11 | RC-04 bridge rejects changed financial/protocol/topology contracts and unknown histories; nonzero installed sequence is preserved or explicitly refused.                                                                        |
| IA-12 | Every bridge cut before/after forward-only boundary and each mixed pair preserves records; only compatible pre-boundary rollback, no old-reader start afterward.                                                                |
| IA-13 | Maintenance abort consumes floor without installed commit/admission, unlock or resume; later release requires fresh inspection and higher valid reservation.                                                                    |
| IA-14 | Active intent, unknown outcome, portable-recovery gap or committed release cannot be bypassed by admission/trust/reader recovery.                                                                                               |
| IA-15 | Installed dispatch, services, packaging, public trust and financial controller remain unchanged in the first model; all execution flags false.                                                                                  |
| IA-16 | Future installed/Ubuntu proof uses actual immutable helpers and separate nonzero UIDs; no project root code, owner-host mutation or retained disposable key/process/resource; failure evidence and foreign resources preserved. |

One coordinator owns this connected compatibility contract; no parallel ownership
is selected. Future source ownership is finite:

- First, `internal/update/installed_admission_model.go` and
  `installed_admission_model_test.go`: typed compatibility/authority/bridge/recovery
  model only, not transport, installed I/O or production schema allocation.
  `scripts/check-update-installed-admission-model.mjs` owns the guarded model
  runner and source/case/canary/disabled-capability evidence under
  `.evidence/p4-installed-admission-model/`. Six exact test families:
  `TestInstalledAdmissionCompatibilityModel`, `AuthorityModel`, `ReviewModel`,
  `RecoveryModel`, `BridgeModel`, `NoExecutionModel` (each suffix after the first
  expands with the `TestInstalledAdmission` prefix). Require all sixteen IA case
  IDs and eight RC decisions in the model report; IA-16 remains BLOCKED for real
  installed proof even when its no-root/cleanup model assertions pass.
- Later, separately reviewed installed adapters would own
  `cmd/fased-app/update_owner.go`, `bootstrap_linux.go`, `update_install_linux.go`,
  `internal/signer/{rpc_linux.go,update.go}` and new role-local admission adapters,
  `internal/update/compatibility.go`, `internal/custodyinstall/{bootstrap,transaction,completion}_linux.go`.
  Assign profile/version/seed/time/storage/bridge contracts before editing these;
  no generic RPC exposure, old reader widening or financial journal migration.
- A later dedicated installed-admission acceptance runner and Ubuntu job must bind
  actual helper hashes/contracts, immutable config, peer UIDs, role state and
  every bridge crash cut. It is separate from today's fixture UID job. No workflow,
  package, service or owner host changes belong to this design or first model.

Future completion commands for the **non-installed model only**, not run here:

```sh
# Run from fased_app, as the ordinary user; missing runner/tests must fail.
test "${FASED_INSTALLED_ADMISSION_MODEL:-}" = 1 || exit 2
for file in internal/update/installed_admission_model.go \
  internal/update/installed_admission_model_test.go \
  scripts/check-update-installed-admission-model.mjs; do
  test -f "$file" || exit 2
done
node scripts/check-update-installed-admission-model.mjs
```

That runner must reject missing opt-in or execution as root, absent/zero selected tests, missing
IA/RC evidence, changed financial canaries/floors or any enabled capability. It
runs the six exact families under Go race detection plus directly coupled
update-model checks once, using disposable keys/inert artifacts/controlled HTTPS
only if those existing fixtures are needed. No network production origin, installed
RPC, system service, identity provisioning or controller invocation. Required
installed/Ubuntu acceptance remains BLOCKED until separately authorized adapters
and their prerequisites exist. The command above is a completion contract, not
an authorization to leave design-only scope now.

### Implemented non-installed model

`internal/update/installed_admission_model.go` and its tests implement private,
fixture-only reader decisions, independently pinned model roles, exact review,
opaque live receipts, final owner refresh/signer confirmation and recovery.
They have no installed dispatch or controller consumer. The symbolic proposed
reader has no allocated lifecycle number. Existing protocol/financial-format 1
and an exact topology digest are explicit preservation inputs, not new production
wire/schema assignments. A changed protocol, format, topology or unreadable
history refuses bridge creation before mutation. Nonzero installed history is
preserved in the copied bridge state.

Each role uses a separate fresh fixture checkpoint namespace and the existing
real disposable TUF/provenance/inert-artifact verifier. The bounded combined
store gains one strictly validated model record kind, binding its exchange to the
outer ID/channel, profile, identity, finite phase and request digest. Narrow
factoring of `handoff.go` preserves pair checks and prevents a model namespace
from becoming a legacy handoff session. Model re-open is historical only; JSON,
old status and restarted actors cannot mint a live receipt. Unknown pending
learning and anchor/publication failures remain held. Bridge checkpoints model
exact expected-head recovery and forward-only fencing; they do not perform OS
file publication, cold process execution or financial installation.

`scripts/check-update-installed-admission-model.mjs` requires explicit opt-in,
Linux and a nonzero current UID, six exact model families, all sixteen IA IDs and
eight RC decisions. It also runs the six directly coupled handoff families.
Each race group has its own two-minute test deadline; logs stream to persistent
files before process completion. Unique per-attempt private fixtures prevent an
interrupted attempt from supplying later evidence. Successful cleanup, actual
before/after financial canary/floor comparisons, unchanged source fingerprints
and disabled capability assertions are mandatory. Disposable private keys remain
in memory. The guard rejects missing/zero/skipped tests and absent coverage.

Run the guarded command above (or
`FASED_INSTALLED_ADMISSION_MODEL=1 node scripts/check-update-installed-admission-model.mjs`)
from the source root as the ordinary user. Existing Ubuntu core CI already runs
these tests through `go test -race ./internal/...`; no workflow or service changes
are required. Exact local/Ubuntu results, head/tree, corrections and cleanup are
recorded in the Fased execution checkpoint. Model UIDs, immutable-file identity,
seed authentication and cold-reader facts are trusted fixture inputs, not actual
OS/production proof. IA-16 installed helper/UID acceptance and every public P4
gate remain separate; real wallets, installed execution and automatic updates
remain disabled.

## Fixture-installed inspection, provisioning and reader-bridge adapter design

**P4 design contract, revision 1, 2026-10-20.** Documentation only. No loader,
profile, command, socket, service, reader conversion or installation is activated
by this package. Real wallets, financial execution, installed execution and
automatic updates remain disabled. P4 remains ten PASS/six BLOCKED; no P5.

This specializes the [installed compatibility contract](#installed-release-admission-and-reader-bootstrap-compatibility-design)
and [non-installed model](#implemented-non-installed-model) into a proposed Linux
amd64/Ubuntu 24.04 **disposable engineering** adapter. Its fixture values below
are a reviewable contract, not production allocations. Production bootstrap,
seed/time confidence, profile/wire/lifecycle/schema, credentials and public feed
remain **UNASSIGNED/BLOCKED**. Existing model/UT evidence cannot close the future
actual-helper acceptance rows below.

### Separate capability axes and current seams

Use proposed purpose/profile `fased-app-installed-inspection-fixture:v1`, capability
`fixture-installed-inspection-v1`, and purpose-qualified descriptor/reader/wire
format 1. The separate bridge purpose is `fased-app-fixture-reader-bridge:v1`.
These identifiers never match the compiled transport, in-process model or a
production profile. Every descriptor/result requires `fixtureOnly: true`.

Financial readers retain the existing protocol 1, lifecycle entrypoint 4,
stage format 1 and known vault/operations formats/features. A separate
`FixtureReaderContract` must explicitly advertise the inspection profile,
capability, descriptor/store/source formats and supported bridge predecessor.
Both readers must support it. Existing lifecycle 4 alone cannot negotiate it;
there is no implicit lifecycle 5, feature-string widening or legacy RPC fallback.
The model's proposed-reader marker maps to this extra axis; its lifecycle zero
is a symbolic unallocated value, never a runnable installed contract.

| Current implementation                                                    | Proposed adapter responsibility                                                                                                                                                           | Boundary                                                                                                                                                                      |
| ------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `custodyinstall.Load/Verify`, immutable `ownerUpdatesCommand`, owner lock | Derive the fixed installation identity and root, rehash the executing helper against the active fixture catalog, then enter an explicitly selected fixture inspection handler.            | Owner UID/session alone cannot supply pins. No path/seed/time overrides from app/UI/CLI request JSON.                                                                         |
| `signer.LoadConfig/CheckOwner/VerifyExecutable`, `rpc_linux.go`           | Preserve root-owned no-symlink config and client checks; load signer-side fixture pins independently. Authenticate both socket peers before protocol data.                                | Existing peer check establishes UID, not remote executable attestation. Each process checks its own exact allowed image; the owner account/kernel/root remain trusted.        |
| Existing `owner.sock`/`app.sock`, strict one-request newline protocol     | Keep legacy parsing/actions unchanged. A future `inspection.sock` in the existing signer IPC directory uses its own purpose-qualified, length-framed protocol in the same signer process. | No tunnel through `Request`, app channel, current `IntentRequest`, password forwarding or new background daemon.                                                              |
| `custodyinstall.definitions` and actual/stored unit verification          | Add a finite fixture-profile unit variant only during the separately reviewed bridge; bind exact before/guarded/final unit bytes in its immutable transaction.                            | Core privileges unchanged. Signer retains `AF_UNIX`, empty capabilities, strict filesystem/home protection and no new Internet access.                                        |
| `FixtureTransportRole`, `FixtureHandoffActor` and combined store          | Factor shared bounded verification/publication/framing primitives into typed adapters, with a distinct installed-fixture namespace and strict root-owned descriptor loader.               | Do not relax the current role-owned config loader, import a model store or use injected model `AuthenticatedSeed`, inventory, UID or cold-reader booleans as installed facts. |
| `VerifyBootstrap` / current `runBootstrap`                                | Preserve exact 3 → 4 support. A separate fixture bridge handles exactly the pinned lifecycle-4 pair → capability-bearing fixture pair.                                                    | No generic schema migration, no production release admission and no shortcut around independently authenticated initial reader bytes.                                         |
| Existing cold-inspection and controller patterns                          | Reuse stopped-service verification and fixed `setpriv` credential-drop patterns; obtain current history directly under signer UID through a read-only candidate reader.                   | Reusing a pattern does not authorize any controller operation or old financial database restore. New cold entrypoint must prove no repair/write side effects.                 |

### Immutable provisioning and independently owned inputs

Let `R` be the existing verified `custodyinstall.Root(ownerUID)`, never an incoming
path. Proposed paths below live under `R/fixture-inspection-v1/`; they do not extend
`identity.json` or the version-1 install manifest with unknown fields. The sidecar
catalog binds their bytes to the existing installation ID, owner/app/signer
UIDs/GIDs, root/config/install-manifest digests and exact reader/unit contracts.
It also records the finite candidate/final executable paths and exact before/target
root/owner installer-record digests allowed in each bridge phase. The catalog and
role descriptors stay immutable; known phase files under the catalog's exact
BridgeID record progress. They never select a latest store or replace pins.
Owner alone verifies its private installer record; signer verifies its own local
identity/financial state and the independently pinned root phase records. A peer's
hash claim never establishes local identity.

| Path / input                                                                           | Ownership / allowed write                                                                                                                      | Required binding and refusal                                                                                                                                                                                                                                                                                                                                |
| -------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `catalog.json`, `inputs/owner.json`, `inputs/signer.json`, seed/policy/public-CA bytes | Root-owned regular files 0644; immutable parents 0755, no group/other write or symlink. Only fixed trusted OS publication tools.               | Purpose, experiment/provisioning ID, installation/config IDs and both local reader hashes, repository/channel/source/transport pins, role UID/GID, supported formats and allowed fixed paths. Each role loads its own file and rechecks every digest; foreign/missing/partial input is preserved and refused.                                               |
| `capsules/<BridgeID>/`                                                                 | Root-owned immutable bootstrap capsule and exact current/candidate core+signer images, 0755 for images.                                        | Disposable harness authenticates exact source/build/output hashes through independently retained fixture pins before any candidate runs. This is an engineering bootstrap assumption, not public OIDC/distribution authentication. Actual reader images are bounded separately at 64 MiB each; they never pass through the inert release-artifact verifier. |
| `evidence/<BundleID>/`                                                                 | Root-owned immutable public evidence tree, non-executable regular files.                                                                       | BundleID hashes a canonical index of fixed metadata/subject names, hashes and sizes. No caller-selected filename, extraction, compression, hardlink, symlink, inherited FD or executable subject. Index/directory mismatch refuses.                                                                                                                         |
| `owner/state/`, `owner/anchor/`                                                        | Root-owned parent, owner-UID private subdirectories 0700; files 0600, writable only by owner.                                                  | Separate strict combined checkpoint and expected-head anchor bound to purpose/provisioning/config; never app SQLite or a shared peer store.                                                                                                                                                                                                                 |
| `signer/state/`, `signer/anchor/`                                                      | Root-owned parent, signer-UID private subdirectories 0700; files 0600, writable only by signer.                                                | Same invariants independently. Future signer unit grants write access only to these two exact directories in addition to its existing state/IPC paths. Clock, catalog and evidence stay read-only.                                                                                                                                                          |
| `clocks/owner.json`, `clocks/signer.json`                                              | Root-owned controlled fixture inputs, immutable to both roles/app; changed only by inventoried trusted OS tools in a disposable CI experiment. | Purpose, provisioning ID, role, clock epoch/revision, UTC sample and explicit confidence. Each role keeps its own persisted high-water mark and monotonic live deadline. Backward/equal-expiry/uncertain/mismatched epoch refuses; peer time cannot supply confidence. Never change host time or treat fixture confidence as production time.               |
| Authoritative inventory                                                                | Fresh private core probe plus signer-owned live/cold snapshot; no inventory file supplied by the app.                                          | Bind authority epoch, actual boot/revision, history/originals/counters, pending/unknown/recovery holds, active intent and reserved/installed floors. Owner uses the authenticated signer reply; signer reads its own authority.                                                                                                                             |

The existing signer unit has `RestrictAddressFamilies=AF_UNIX`. Preserve it by
using a closed **offline fixture metadata source** for signer verification. The
owner may acquire the public bundle from the controlled loopback HTTPS fixture;
a fixed OS publisher stages it immutably and rehashes the published bytes. Both
roles independently verify the complete TUF chain, provenance and four inert
artifact roles using their own pins, trusted state and time. The signer trusts
neither the downloader's verdict nor its cache head. Withholding a newer valid
bundle can still delay discovery within metadata validity; expiry/local floors
remain the bound, not a claim of instantaneous offline revocation.

Proposed offline-source bounds: at most 64 fixed files/1 MiB total, 64 KiB index
and evidence documents, existing 8 KiB metadata/1 KiB descriptor limits, at most
32 sequential root successors and four non-executable inert subjects of at most
64 KiB each. Root/role continuity and existing fixture origin/channel bindings
remain required. Derive a new source digest from the seeded origin/fetch-policy
pins and exact offline index hash; never claim the old live-HTTPS source digest
for this adapter. Reuse the existing signature/client verification logic through
a sealed source adapter; do not introduce a general fetch callback or widen the
current inert-artifact restrictions to admit reader binaries.

Provisioning has four durable states, all still inactive:

| State                 | Publication / retry rule                                                                                                                                                                                                              |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| IP-00 inventoried     | Verify exact current fixture installation, no active bootstrap/update, ordinary owner and three distinct nonzero UIDs; record original bytes and all planned resources. Refuse foreign resources.                                     |
| IP-01 staged          | Authenticate fixture bootstrap capsule, root-publish exclusive temporary files, verify complete hashes/modes and sync before naming them final. Retain prefix-safe retry identity; source paths are not authority.                    |
| IP-02 sealed          | Publish catalog and separate role descriptors; create inventoried empty private state/anchor directories and fixture clocks. No role checkpoint/root learning or socket starts yet.                                                   |
| IP-03 bridge-eligible | Re-read actual published identities and exact predecessor/target capability contracts. Only the linked bridge can activate them; arbitrary `--initial`, descriptor replacement, mode promotion or partial-directory adoption refuses. |

Only an explicitly marked, still-empty namespace may initialize after the
forward fence. Once any role head/anchor exists, missing/corrupt/gapped state
holds; retry cannot return to initial mode. Partial role initialization or
unknown pending verification never erases the other role's learned state.
Same-disk anchors do not solve whole-host rollback or portable financial recovery.

### Inspection authority, transport and protected review

Proposed handler boundaries are `custody fixture-inspection` → the exact immutable
`owner-fixture-inspection` helper, and `signer.NewFixtureInspectionServer` within
the existing signer process. These are future names, absent today. Ordinary
startup/packages do not opt in. The active fixture catalog, explicit disposable
mode, both reader capabilities and exact executing-image hashes are mandatory.
The signer channel checks configured owner UID before any reply; the owner checks
signer UID before sending anything. No same-UID test bypass exists in public
configuration. The existing app can receive only a public historical status copy.

Use separate profile/version negotiation and strict 64 KiB length-framed messages.
The envelope fields are version, profile, capability, provisioning ID, channel ID,
kind and bounded typed body; unknown/duplicate/case-alias fields refuse. The closed
kind set is hello, challenge, inspect, result, confirm, confirmed, status,
status-result and error. Historical status uses a fresh authenticated channel and
cannot create a challenge or result handle. Retain the existing fixture limits of four accepted connections/one
active inspection, 5s frame deadlines and a 60s monotonic connection/review ceiling.
Store limits remain 256 KiB, 32 replay records, 4 KiB per complete record and the
existing 256-file cap. Exhaustion holds without pruning uncertainty. No trust-store
lock is held while waiting for owner input or a peer. Namespace/profile mismatches
must refuse before acquisition, reservation or any financial-state change.

The exact canonical exchange binds, in order: profile/capability/wire version,
provisioning ID, installation ID, catalog/owner-descriptor/signer-descriptor/source
digests, owner/signer reader-contract and executable digests, channel/client nonce,
challenge ID and both boot IDs, both baseline checkpoint heads, authority epoch,
owner/signer snapshot digests/revisions, BundleID, AuthorizationID, UnitID,
provenance digest, OS/architecture/release sequence, both observations/clock epochs,
and the earliest deadline. Compute `ProposalDigest` under
`fased-app-installed-inspection-fixture-proposal:v1` over that ordered tuple,
excluding any review or final-exchange digest. Compute `ReviewDigest` under
`fased-app-installed-inspection-fixture-review:v1` from ProposalDigest, the
protected reviewed-at observation and exact consent. Then compute the final ExchangeDigest under
`fased-app-installed-inspection-fixture-exchange:v1` from ProposalDigest and
ReviewDigest using the existing length-prefixed tuple encoding. This avoids a
self-referential hash. Both roles revalidate every bound local value. Requests
carry digest observations, never paths, seed/policy/CA bytes, clock/expected-anchor
overrides, passwords or approval flags. A peer's baseline-head observation is an
exchange claim, not the local expected anchor or provisioning authority.

The immutable owner terminal displays an inspection-only action card with those
identities, exact four artifact hashes/sizes/contracts, source/build provenance,
current holds/floors and expiry. Consent is exactly `INSPECT FIXTURE <ProposalDigest>`; the final exchange also
binds the resulting ReviewDigest.
No UI/chat/session, private ack file or production `--approval-stdin` can approve.
Fixture input automation is a private test callback, not an installed CLI flag.
After independent signer verification/result publication, owner refreshes its
verification and obtains same-live-connection signer confirmation before local
completion. Exact live retries preserve original expiry/result; fresh connection
or reboot status is historical only. Every reserve/install/pause/unlock/resume,
wallet/financial execution and automatic-update field in the inspection result
is false. Reader maintenance uses a different review and authority below.

### Reader bridge and reboot-safe forward boundary

The existing 3 → 4 bootstrap and normal financial install controller stay finite
and unchanged. The new fixture bridge accepts only the exact pinned lifecycle-4
pair → inspection-capable fixture pair on Linux amd64. Both sides preserve protocol,
vault/operations formats/features, identity/config and financial topology.
`FinancialTopologySHA` covers existing UIDs/GIDs, financial/core/IPC paths, listener
and service identities; exact before/guarded/final executable and service-policy
digests separately bind the declared reader and private-inspection-path additions.
These additions cannot masquerade as a permitted financial topology migration.

A bootstrap capsule binds independent fixture pins, current/target reader bytes
and contracts, role-input/source identities, exact unit-policy variants, matching
maintenance manifest and its sequence, plus initial authoritative snapshots.
Before the fence, it authenticates only a fixed known bootstrap checkpoint/reader
capsule: no TUF root discovery, admission, pending verification or mutable
trust-store learning. Cold financial checking is read-only. Any operation that
might learn/update admission trust occurs only after the durable forward fence.

| Phase                     | Required operation / crash disposition                                                                                                                                                                                                                                                                                                                                                  |
| ------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| RB-00 reviewed            | Existing immutable predecessor supplies inventory. Protected owner reviews `BRIDGE FIXTURE <full-digest>` for the complete capsule and exact maintenance effect; no admission/install consent follows. Lifecycle 1/2/3/unknown, active/unknown history or unsupported nonzero installed history refuses.                                                                                |
| RB-01 maintenance-held    | Predecessor reserves a strictly higher sequence using its signed unchanged-pair lifecycle-4 maintenance manifest, stages only its own pair, locks the fixture wallet, pauses matching core metadata and captures the verified private recovery index. Installed sequence is unchanged.                                                                                                  |
| RB-02 stopped             | Stop both existing services; verify inactive/MainPID zero and all immutable files. Only fixed OS tools are privileged. New candidate cold inspection runs after dropping UID/GID/groups/capabilities to the signer UID with no-new-privileges.                                                                                                                                          |
| RB-03 guarded-units       | While stopped, publish exact stored and actual guarded unit variants and reload the manager. ExecStart names the immutable **new candidate**, entering a new bridge-guard handler before normal startup/state opening. With no forward fence it refuses activation without writes. Old Verify/Runtime unit checks must fail. A reboot cannot start old readers from old unit fragments. |
| RB-04 cold-verified       | New candidate validates current financial history, originals/counters/authority, maintenance head and reserved/installed floors, metadata pause/index and protocol/format/topology. It performs no repair, migration or admission trust learning. Before-fence rollback remains possible only on positive predecessor-readability proof.                                                |
| RB-05 forward-only        | Durably publish and sync the exact root-owned forward fence before creating any new role checkpoint/anchor or admitting new-format state. Re-read fence and guarded actual/stored units. From here, old-reader restart/rollback is forbidden even when health, time or trust initialization fails.                                                                                      |
| RB-06 new-readers-held    | Replace only the pinned fixed core/signer images and manifest hash fields; guarded units can still use the identical pinned candidate copies. Initialize each sealed private namespace once and verify its own offline evidence. Partial/pending/gapped initialization remains held. Future ordinary paths still cannot authorize installation or financial execution.                  |
| RB-07 converged-held      | Verify health and all current histories with the new pair. Converge root/owner installer records, catalog-bound phase files and final guarded units pointing to fixed new paths; stop/restart only new readers when needed and re-probe boots. Both guarded/final variants require the fence/catalog before state access. No copied host receipt certifies current state.               |
| RB-08 maintenance-aborted | Only after current cold/live health, actual pair/policy/record convergence and preserved histories, abort exactly the predecessor-readable maintenance intent and release only its matching core pause from the current signer receipt. Reserved floor remains consumed, installed floor unchanged and wallet locked.                                                                   |
| RB-09 reader-ready        | Record the completed bridge and retain the forward fence/capsule. Inspection needs a new challenge/review and unexpired independent verification; this phase itself creates no admission result or installed release.                                                                                                                                                                   |

Pre-fence rollback first proves **no** role trust namespace was initialized and
no unknown/new financial history appeared. Restore only exact predecessor
executables and stored/actual units, reload while stopped, verify compatible
cold history/health, then abort only maintenance. Foreign bytes or any uncertainty
leave services held. Nonzero installed sequence is explicitly supported only when
both cold readers verify it exactly; never coerce it to zero. After RB-05 use only
the authenticated new recovery runner, preserving all learned state and holds.

The guarded-unit step is mandatory, not a marker-file convention unknown to old
binaries. Future Ubuntu acceptance must actually reboot/recreate manager startup
conditions at each boundary and invoke the exact compiled predecessor commands.
If managed old startup remains possible, the bridge is unsupported. No guarantee
is made against a trusted owner/root deliberately running historical binaries
outside managed entrypoints.

Every phase record binds the immutable capsule/transaction digest. Retry requires
that exact ID and classifies actual bytes, units, service state and role anchors;
it never guesses success, follows latest files or restores a financial database.
Immutable accepted bridge recovery may converge/hold after its original capsule
expires; it cannot start a new inspection, extend trust validity, discover a new
root or substitute a target. Pending unknown learning remains blocked for a
separate reviewed rescue procedure. Disk preflight counts all actual current,
previous and candidate copies, evidence and private metadata backup requirements;
it cannot reclaim owner state or assume same-disk backup is disaster recovery.

### Finite compatibility and actual acceptance

| Case  | Supported/refused state                                                                                                                                 |
| ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| FC-01 | Lifecycle 1/2/unknown contract, unreadable history or changed protocol/format/topology: refuse without new state.                                       |
| FC-02 | Lifecycle 3: existing disposable 3 → 4 bridge only; still no inspection capability.                                                                     |
| FC-03 | Plain lifecycle 4: maintenance/recovery only; no inspection or legacy fallback.                                                                         |
| FC-04 | Exact lifecycle-4 predecessor → pinned fixture-capable pair: separate bridge/capsule and guarded-unit phases required.                                  |
| FC-05 | Mixed readers, torn unit/catalog publication or foreign bytes: held; exact recorded new runner only, no inferred downgrade.                             |
| FC-06 | Both capable readers, active sealed profile, current anchors/time/inventory: inspection only.                                                           |
| FC-07 | Active intent, unknown outcome, pending learning, recovery hold, expired/uncertain trust or anchor gap: status/held recovery only; no fresh completion. |
| FC-08 | Other OS/architecture or production configuration: refuse; cross-compilation/model evidence cannot supply support.                                      |

All sixteen FA rows are **BLOCKED** for actual installed-fixture acceptance now.
They refine IA-01–IA-16; neither designs nor partial primitive tests close them.

| Case  | Required falsifiable proof on disposable Ubuntu                                                                                                                                                                                                                           |
| ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| FA-01 | Three distinct nonzero UIDs; actual root-owned descriptor/current-image/config pin validation; app and foreign owner inputs refused before protocol or mutation. IA-01/02/16.                                                                                             |
| FA-02 | Exclusive/prefix-safe provisioning across IP-00–03, missing/foreign/torn files and changed public producer bytes; independently rehash published bytes and preserve foreign resources. IA-02/09.                                                                          |
| FA-03 | Separate private state/anchors, one-time initialization, missing/corrupt/whole-store-only rollback and no model/legacy namespace adoption. IA-03/08/09.                                                                                                                   |
| FA-04 | Controlled HTTPS owner acquisition and immutable offline signer verification of four inert roles/TUF/provenance; stale roots, source/index mismatch, altered/missing subjects and cap exhaustion refuse. IA-03/04.                                                        |
| FA-05 | Actual signer sandbox keeps AF_UNIX and existing hardening; writes succeed only in declared private paths. App cannot read/write them; inode ownership alone never substitutes for peer UID. IA-01/06/16.                                                                 |
| FA-06 | Real helper review/cancellation, app/status/JSON/ack-file/legacy channel denial; no secret forwarding and no public automation bypass. IA-05.                                                                                                                             |
| FA-07 | Strict framed profile negotiation, exact exchange/context bindings, wrong peer, replay, cross-connection status and downgrade refusal; bounded resources/deadlines. IA-01/06/10.                                                                                          |
| FA-08 | Independently controlled role clocks/high-water marks, equal expiry, regression, uncertain epoch, review delay and actual monotonic timeout refuse without host-time changes. IA-07.                                                                                      |
| FA-09 | Kill/drop across pending/result/anchor/confirmation cuts; exact live retry once, historical-only reconnect/restart, retained learning/unknown hold and no reset. IA-08/09/10.                                                                                             |
| FA-10 | Fixed capsule authenticates actual executable readers separately from inert targets; substituted reader/hash/capability/schema/source/seed refuses before execution. IA-02/04/11.                                                                                         |
| FA-11 | Actual nonzero installed history, unchanged protocol/format/topology/originals/authority/floors and no cold-inspection repair/write; changed or unknown history blocks conversion. IA-11/14.                                                                              |
| FA-12 | Every RB-00–09 cut, including each image/unit/installer-record publication and manager reload/reboot; old compiled managed entrypoints cannot start readers against post-fence state. IA-12.                                                                              |
| FA-13 | Positive pre-fence compatible rollback/maintenance abort; post-fence health/init failure holds on new readers and never restores old financial state. IA-12/14.                                                                                                           |
| FA-14 | Exact completed bridge retry, expiry-safe held recovery, superseding intent/floor refusal and later fresh inspection; no installed-release commit, auto unlock/resume or reused review. IA-10/13/14.                                                                      |
| FA-15 | No production capability/seed/feed/time authority, installed/financial execution, ordinary package opt-in, automatic updater, new networked signer or third daemon. IA-15.                                                                                                |
| FA-16 | Cleanup succeeds on success/failure/cancellation: exact owned processes/units/UIDs/files only, hash-bound evidence retained and foreign/owner resources preserved. Project code never root; actual VM-reboot proof cannot be replaced with a container simulation. IA-16. |

### File ownership and executable completion path

One coordinator owns this package; no parallel agent work is selected. Proposed
source ownership is finite and follows the existing dependency direction:

- `internal/update/inspection_fixture.go`, `_source.go`, `_files_linux.go`,
  `_files_unsupported.go` and their tests: purpose-qualified schemas, strict
  descriptors, sealed offline-source verification, role-owned checkpoint/anchor
  adapters and bounded typed results. This package must not import signer or
  custodyinstall; no model constructor/boolean is an installed trust factory.
  Narrow source factoring in existing `trust.go`/`admission.go` is permitted only
  to reuse verification; existing live fixture behavior and limits stay intact.
- Later, `internal/signer/inspection_fixture_linux.go`/tests and narrow
  `rpc_linux.go` lifecycle hooks: same-process separate listener, actual local
  authority reader and cold inspection. `cmd/fased-signer/main.go` owns guarded
  candidate startup, explicit fixture opt-in and unsupported-platform refusal.
- Later, `internal/custodyinstall/inspection_fixture_linux.go` and
  `inspection_bridge_linux.go`/tests own fixed provisioning/bridge publication;
  narrow `model.go`, `install_linux.go`, `system_linux.go` variants verify exact
  catalog/unit/installer records without widening generic privileged operations.
- Later, `cmd/fased-app/inspection_fixture_linux.go`, `_other.go` and tests own
  immutable owner review/dispatch/recovery; existing main/custody dispatch gets
  only explicit fixture branches. No web/desktop/agent authorization path.
- `scripts/check-inspection-fixture-inputs.mjs` owns the next non-activated
  input/verifier round trip. A later `scripts/check-installed-inspection-fixture.mjs`
  owns the connected FA inventory; a dedicated Ubuntu job/aggregate dependency
  may be added only with that later implementation, retaining all existing jobs.
  Reboot cuts require an explicitly disposable VM runner or equivalent real
  boot-capable fixture. Ordinary hosted CI/service restart alone is SUPPORTING.

**Next bounded implementation:** sealed input/catalog + offline verifier + private
checkpoint round trip, with no installed command/socket/service/bridge activation.
Own the first update files/tests and input runner above. Cover descriptor/profile
and actor identity refusal, immutable evidence verification, pending/anchor
failure/retry, complete before/after canary/floor preservation and disabled
capabilities. Local tests use controlled ordinary-user fixtures; a later requested
Ubuntu file/UID harness can exercise root-owned fixture files through fixed OS
tools without installing or starting services. Such proof remains primitive
engineering evidence; FA installed/reboot gates stay BLOCKED. Review this design
before allocating even its disposable profile values in code.

The command below was future at design time; its implementation is now recorded
under [the non-activated round trip](#implemented-non-activated-inputoffline-round-trip):

```sh
# Maintainer checks only, from fased_app as the ordinary user.
test "${FASED_INSPECTION_FIXTURE_INPUTS:-}" = 1 || exit 2
for file in internal/update/inspection_fixture.go \
  internal/update/inspection_fixture_source.go \
  internal/update/inspection_fixture_files_linux.go \
  internal/update/inspection_fixture_test.go \
  scripts/check-inspection-fixture-inputs.mjs; do
  test -f "$file" || exit 2
done
node scripts/check-inspection-fixture-inputs.mjs
```

The runner must require six nonzero exact test families:
`TestInspectionFixtureDescriptors`, `OfflineSource`, `RoleInputs`, `Publication`,
`Recovery`, `NoExecution` (expand the latter names with `TestInspectionFixture`).
Its finite input/verifier inventory is:

| Case   | Non-activated primitive predicate                                                                                  |
| ------ | ------------------------------------------------------------------------------------------------------------------ |
| IFP-01 | Descriptor identity/profile/capabilities, typed inputs and unsupported reader/OS refusal.                          |
| IFP-02 | Fixed immutable file/path/mode/hash validation, foreign/symlink/hardlink/prefix preservation.                      |
| IFP-03 | Complete bounded offline TUF/provenance/four inert subject verification; source/index/continuity mismatch refuses. |
| IFP-04 | Independent role pins/time, changed/uncertain clock epoch and deadline refusal.                                    |
| IFP-05 | Separate checkpoint/anchor CAS, strict record namespace, exhaustion and one-time initialization.                   |
| IFP-06 | Pending/expiry/restart/publication-gap hold, exact retry and historical-only status.                               |
| IFP-07 | Actual disposable before/after financial canary and reserved/installed floor preservation.                         |
| IFP-08 | All execution flags false, no installed dispatch/service/controller and verified success/failure cleanup.          |

Retain source hashes, per-case outcomes and actual cleanup; reject
missing/skipped/zero tests. Run the
smallest coupled verifier/store cases once after inputs freeze, with separate
bounded group deadlines and streamed persistent logs. Never initialize an
installed identity, start a service, call a controller or contact a production
origin from this runner. Actual installed/VM FA acceptance needs separate future
authority and prerequisites; this design authorizes none of those operations.

### Implemented non-activated input/offline round trip

`internal/update/inspection_fixture*` now implements the first IFP primitive
package. All entrypoints are package-private, disposable engineering helpers.
Nothing calls them from an installed command, signer, service, UI or updater.
The inspection profile/capability above is allocated only for these fixtures;
financial protocol 1, lifecycle 4 and existing packages remain unchanged.

A pinned closed catalog binds installation/provisioning identities, three distinct
logical UIDs, per-role public seed/policy/index pins, reader capabilities and
before/candidate image and unit hashes. The fixture loader checks exact real
paths, modes, ownership, single-link regular files and complete hashes. The
publisher retries only an exact prefix and uses atomic no-replace publication.
These ordinary-user ownership substitutions are SUPPORTING, not proof of
root-owned installed inputs, kernel UID isolation or authenticated reader images.

Each role independently copies a closed bounded offline index and file set,
including sequential root successors, exactly one active metadata set, provenance
and four inert subjects. Its source digest binds the seeded HTTPS origin, fetch
policy pin and index. It reuses the existing TUF/provenance verification code;
there is no fetch callback or network fallback. Tests close the controlled HTTPS
fixture before verification. Both sealed inputs and the role clock are reread
before completion. Controlled clocks are fixture inputs, not production time.

Each role has separate private checkpoint and expected-head anchor directories.
A bounded combined checkpoint records trust progress, clock high-water, pending
verification and historical outcome. OS-locked CAS publishes the synced snapshot,
checkpoint head, then anchor. Gaps hold; only the exact unexpired in-memory
publication can retry. Pending restart, missing/corrupt anchors, unknown state,
clock regression and exhausted history refuse initialization or new verification.
Status is historical data and conveys no install, resume, wallet or update right.
Same-disk anchors cannot detect a coordinated whole-store/host rollback.

Run the complete ordinary-user Linux engineering check with:

```sh
FASED_INSPECTION_FIXTURE_INPUTS=1 node scripts/check-inspection-fixture-inputs.mjs
```

It requires the six exact families and all eight IFP cases, records source hashes
and streamed logs under `.evidence/p4-inspection-fixture-inputs/`, checks actual
financial-canary/floor preservation and disposable cleanup, and rejects zero,
missing or skipped tests and every enabled execution capability. Existing Ubuntu
core CI runs these Go tests through its unfiltered internal-package race suite.
This adds no CI job, installed route/socket/service, reader bridge or controller
call. FA-01–FA-16, actual installed/VM acceptance and production provisioning,
seed/time/credentials/feed remain BLOCKED. Real wallets, installed execution,
automatic updates and P5 remain disabled; public P4 stays ten PASS/six BLOCKED.

### Installed fixture adapter and guarded reader bridge

The disposable Linux amd64 adapter is now implemented across the existing owner
helper, signer and installer. Ordinary packages and first launch never opt in.
Real wallets, real signing and automatic updates remain disabled. The fixture
inspection capability is separate from financial protocol 1/lifecycle 4.

`custody fixture-inspection provision` accepts a bounded public bootstrap capsule,
public input directory and exact reader pair, requires a protected terminal and
`BRIDGE FIXTURE <digest>`, then publishes through the existing fixed OS tools.
The authenticated candidate is copied before it is run. Its financial contract
must remain identical to the predecessor; its separate inspection capability
must match. Owner acquisition is bounded, pinned, controlled loopback HTTPS;
the signer independently verifies the immutable offline bundle without widening
its AF_UNIX sandbox. Fixture producer keys exist only in test-process memory.

The bridge reserves/stages only its signed unchanged predecessor pair, pauses
metadata and retains the existing private recovery index. It stops both readers,
publishes candidate guard units and verifies the loaded manager policy, then
cold-checks the actual signer history and read-only SQLite snapshot/pause. Cold
signer storage cannot create, repair a pending publication or append a journal;
cold SQLite uses immutable read-only access and refuses an outstanding WAL.
The synced root-owned forward fence precedes role checkpoint initialization.
Subsequent failure holds on the new readers. The controller replaces only the
pinned pair and bound installer metadata, converges final guarded units, verifies
health and aborts only the matching maintenance intent. Installed sequence and
financial history remain unchanged; the reserved maintenance sequence stays
consumed and the fixture wallet stays locked. Exact bridge retry remains
historical. Explicit pre-fence rollback requires empty role namespaces and
positive predecessor readability; post-fence rollback is refused.

The existing signer process owns `inspection.sock`; there is no extra daemon.
Kernel peer UID checks precede protocol data. The separate bounded framed channel
binds both role inputs/heads/boots, actual core/signer snapshots, verified unit,
source and clocks. The immutable owner helper displays the complete unit and
provenance card and requires `INSPECT FIXTURE <ProposalDigest>`. It refreshes
before same-connection confirmation. One exact result replay retains the original
exchange/deadline. A fresh status channel reports historical data without a
review, challenge or execution right. No stdin approval flag or app/UI forwarding
exists for this flow. Missing/gapped/pending stores hold; initialization never
resets learned trust. The ordinary financial RPC grammar remains unchanged.

Maintainer completion command (local checks are SUPPORTING; installed execution
is confined to the disposable Ubuntu CI job):

```sh
FASED_INSTALLED_INSPECTION_FIXTURE=1 node scripts/check-installed-inspection-fixture.mjs
```

The existing managed Ubuntu fixture supplies real root-owned files, three UIDs,
PTY consent, systemd and nonzero history. The dedicated CI job compiles the exact
plain lifecycle-4 predecessor `6c53e34`, reuses unchanged built UI/shared caches,
and exercises the connected installed journey. Evidence is retained under
`.evidence/p4-installed-inspection/`; only inventoried disposable resources are
cleaned. This is engineering evidence, not production trust or real reboot proof.
FA cases are credited only by their recorded actual predicates. Hosted CI service
restart cannot close the VM/reboot portions of FA-12/FA-16. Production seed/time,
credentials/feed, public acquisition and retail wallet/update activation remain
BLOCKED; P4 acceptance must not be inferred from this fixture implementation.
