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
