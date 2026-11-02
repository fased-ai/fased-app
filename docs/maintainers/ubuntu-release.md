# Ubuntu release preparation

The proposed first version is **0.1.0**, targeting Ubuntu 24.04 amd64. This is a
publisher procedure; customers do not manage release-signing keys.

The staging workflow in `.github/workflows/release.yml` is dormant. It creates no
GitHub Release, signed update metadata, deployment or update activation. Its
current configuration intentionally lacks production trust. Do not create a tag
or run a release build until the owner authorizes it.

## Ubuntu 0.1.0 approval worksheet

**Draft single-owner recommendation; no keys, trust binding or activation.**
Version0.1.0 / Ubuntu24.04 amd64 is selected. Reference main `f78236f` is a
baseline, not the selected release source. Repository `fased-ai/fased-app`
(ID `1426211857`, owner `279582504`), workflow `.github/workflows/release.yml`
and proposed protected metadata branch `release-metadata` retain their identity.

### Single-owner signing recommendation

Use the [single-owner policy](../updates.md#single-owner-signing-policy--proposed-not-activated)
as the sole proposed production policy: one owner, four distinct role keys,
one required signature per role. Root/recovery stays offline; Targets/release
is unlocked for approval; restricted Snapshot/Timestamp automation maintains
freshness. No external custodians, customer key forms or signing vendor.

Customers install from the official link and click Update. The public key is
bundled for automatic verification; private software-signing keys remain with
the publisher, separate from wallets. A release push alone cannot provide that
approval to an installed verifier.

The proposed first installer uses conventional official-website HTTPS trust,
with explicit acceptance of website/TLS compromise risk. Installed updates retain
pinned signatures, provenance, expiry, rollback and preserving recovery checks.
The verifier accepts explicit `single-owner-v1` in the pinned public seed; empty
profile retains original eight-key/2-of-3 behavior. Controlled new-policy checks
pass; no production seed is bound and historical fixtures keep their old scope.

## Inputs required before building

- Owner confirmation of the single-owner profile and conventional HTTPS initial
  trust assumption; actual release-owner/account and official hostname.
- Separately authorized key setup, protected offline recovery/release storage,
  encrypted backup and restore evidence; public keys/fingerprints only for review.
- Actual approved `single-owner-v1` public seed and official HTTPS installer
  endpoint. Explicit-profile verification, finalization and publisher functions
  are implemented/controlled-verified; public acquisition is not accepted.
- Exact public v3 seed and recorded owner signing/initial-trust approvals, then
  separately authorized tag/build. Pre-build approval binds method/identity and
  seed; post-build receipt binds actual installer/source/run/seed. Never rewrite
  a pre-build approval digest to pretend it approved unbuilt bytes.

`release/ubuntu.json` retains null `seedFile`, `seedSha256`,
`custodyApprovalSha256` and `initialTrustApprovalSha256`. The existing field name
`custodyApprovalSha256` records publisher signing ownership/backup approval; it
does not require a third party. No config/schema change is made by this document.

## Existing publisher integration

The offline `release-finalize` output now includes `publication.json`, containing
only verified public metadata and pinned release identity. The dormant
`scripts/publish-ubuntu-release.mjs` library has no command-line entry, token
lookup or import-time network activity. Its explicit operations are:

1. `stageCandidate`: reviewed exact candidate inventory → verify existing annotated
   tag/source → draft release/assets with checked digests and recorded attempt IDs.
2. Existing offline finalizer: bind real asset IDs and signatures to the exact
   descriptor and metadata; retain the reviewed `publication.json` digest.
3. `releaseCandidate`: recheck reviewed draft inventory/tag/source → publish only
   the finalized candidate; require GitHub immutable-release confirmation.
4. `publishFinalized`: confirm immutable release/asset identity and metadata head;
   verify exact Git blobs/tree/commit; preserve versioned history; advance without
   force and read back the result. Concurrent/uncertain outcomes stop without retry.

### Bounded metadata refresh

`node scripts/prepare-ubuntu-release.mjs publisher-refresh` is an explicit,
one-shot maintainer operation. It automatically checks whether renewal is due,
requests only the required online signatures, verifies the result and uses the
existing recorded metadata publisher. No timer, workflow schedule or service is
installed. Production trust is still unconfigured; the command stops before
reading keys or publishing credentials until those bindings are approved.

The private `.evidence/ubuntu-release/refresh-request.json` names version 1,
`mode` (`start` or `resume`), `publicationFile`, `publicationSHA256`,
`previousCommit`, `verifierFile`, `verifierSHA256`, `onlineKeyFiles`
(`snapshot` and `timestamp` PEM paths), and `tokenFile`. File paths are explicit
and absolute. The verifier is the reviewed compiled `fased-release-candidate`
executable. Optional `resolutions` use the existing publisher's recovery contract.
No Root or Targets private key is accepted. Keep these files owned and private;
no credential comes from an environment default or enters the journal.

The Go verifier authenticates the pinned prior publication, Root, role signatures,
metadata links, descriptor and provenance. Before any new signature, the runner
checks that those exact public bytes exist at the reviewed current metadata
commit. It refuses a changed head or different release/asset/sequence. Expired
Snapshot/Timestamp can serve as historical input to this operation only; Root,
seed and Targets must still authorize the release. Consumer expiry checks stay
unchanged.

Snapshot renews when at most 48 hours remain and its bounded expiry can extend.
Timestamp renews when at most 36 hours remain, or Snapshot renews. Validity stays
within Snapshot 7 days, Timestamp 48 hours and all offline authorization expiries.
Targets and every immutable release file remain byte-for-byte unchanged. If a
parent expiry prevents useful extension, the result is a no-op with an offline
renewal warning rather than repeated signatures. Root/Targets renewal remains a
separate owner signing action.

The private `refresh-<version>-<baseline-publication-SHA256>/` journal freezes the
due plan before signing and retains public approvals/output. Resume uses those
exact payloads, not a newly computed clock. Its `metadata/` child uses the existing
publisher recorder, original IDs, uncertain-write inspection and head checks.
Completed work is verified read-only; no duplicate ref update or re-signing.
Not-due checks read no private signing key or API token and can be invoked again
when due. `publisher-inspect` includes retained refresh operations.

A successful result includes the renewed public `publication` object, its digest
and the confirmed metadata commit. Retain them together as the next baseline;
keep previous operation records. Missing/corrupt/expired state stops rather than
resetting an operation. Controlled disposable-key/time/transport checks are
engineering evidence, not public-release or scheduled-service acceptance.

### Durable operation and recovery

Use `runRecordedPublisher` for execution; it connects the existing operations to
private on-disk recording. Each mutation is flushed before dispatch and its
result is flushed afterward. Ubuntu's existing `flock` serializes writers and
releases automatically if the process dies. No service or database is added.

The existing maintainer entry uses two commands:

- `node scripts/prepare-ubuntu-release.mjs publisher-inspect` reads local progress
  without credentials or network access.
- `node scripts/prepare-ubuntu-release.mjs publisher` reads a private
  `.evidence/ubuntu-release/publisher-request.json`. Production trust must be
  configured first; missing bindings stop before request/credential access.

The request names version 1, `action` (`stage`, `release` or `metadata`), `mode`
(`start` or `resume`), exact candidate/publication file paths and approval hashes,
and an explicit `tokenFile`. Artifact files use the existing staging directory.
Inputs and the token file must be owned private regular files; no credential is
read from the environment or saved in operation state. Public signing keys and
approval records remain separate from the API publishing credential. No real
request, credential or production trust is supplied by this implementation.

State for staging/release lives in
`.evidence/ubuntu-release/publisher-<version>-<action>/`; metadata adds
`-<approved-publication-SHA256>` to that directory name. Renewed metadata retains
a separate operation; identical approved bytes reopen the same journal. Local
inspection lists retained metadata journals. Each contains private `operation.json`
and an OS lock file. Preserve it with the corresponding build,
finalizer inputs and approval hashes. Do not delete/reset it to retry. Atomic
replacement, file, operation-directory and parent-directory synchronization preserve a complete old or new
snapshot; damaged state refuses unchanged. This is process/crash recovery, not
a promise against disk loss or a compromised maintainer host.

On `resume`, known objects are inspected and retain their IDs. An uploaded asset
whose response was lost can be recovered by its exact release/name/digest/size;
public-release and metadata-ref outcomes are verified read-only. A completed
operation reopens as a verified no-op. Changed inputs, assets, source tag or
metadata head refuse. No uncertain mutation is automatically resent.

If a draft/tree/commit was created but its ID was never received, retain the hold.
An explicitly reviewed `resolutions` entry can bind the observed original ID to
that pending request index and `requestSha256`; inspection verifies the object
before recording it. For example, the record shape is
`{"0":{"requestSha256":"<exact pending request digest>","id":"<observed original ID>"}}`.
A missing or unverifiable object remains held; this does not authorize creating a
replacement or choosing a different ID. No force/reset command is provided.

Controlled fixtures cover these paths, including actual child-process SIGKILL.
Real protected branch/immutable-release configuration, scoped credentials,
backups, restricted freshness operation and public acquisition/readback still
require authorized production setup/acceptance. Automatic refresh and client
automatic installation remain unactivated. Customer Install/Update is unchanged.

## Staging flow

`node scripts/prepare-ubuntu-release.mjs plan` reports missing inputs without
building, signing or contacting a provider. The eventual authorized workflow:

1. Requires an exact annotated version tag pushed from a clean, main-merged
   source commit in the pinned repository. Dispatch, branch and pull-request
   contexts cannot produce a candidate.
2. Checks the approved seed using the existing production verifier. An overlay
   embeds those exact public bytes in the core and installer only for this build.
   Ordinary source defaults remain inactive.
3. Builds locked UI, core and verifier once on Ubuntu 24.04 amd64. Produces the
   schema-2 archive and records real artifact, source, lockfile and seed digests.
4. Generates actual GitHub build provenance for that archive and verifies the
   original bundle offline against the pinned roots, tag, source and Actions run.
   A future authorized run stores its attestation with GitHub; no run occurs as
   part of local preparation.
5. Renders the matching installer entry and retains public candidate files as a
   seven-day Actions artifact for independent review. This is not an installable
   public release. Download and preserve accepted evidence before artifact expiry.

Re-running a failed candidate must retain and classify its original evidence.
Available public inputs are retained after preparation even if building or
verification fails. Partial files or a missing verification decision never count
as an accepted candidate.
Do not rebuild approved bytes under a different run and reuse their provenance.
The proposed version is not a published version or an existing tag.

## Offline signing and finalization

The staged packet is not a signing request until actual immutable asset identities
and a reviewed metadata plan are supplied. The maintainer-only
`fased-release-candidate` tool now prepares requests and verifies imported public
signatures. It accepts no private key, generates no key and makes no network call.
It is not included in customer packages.

Provide one version-1 candidate JSON document with these fields:

| Field                                    | Content                                                                                                                                            |
| ---------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `version`                                | `1`                                                                                                                                                |
| `seedFile`, `seedSha256`                 | Exact independently approved public seed and its digest                                                                                            |
| `archiveFile`, `provenanceFile`          | Original staged archive and attestation bundle                                                                                                     |
| `plan.version`                           | `1`                                                                                                                                                |
| `plan.descriptor`                        | Existing production descriptor, binding actual archive/manifest, source/tree/tag/locks/run/provenance and approved immutable GitHub asset identity |
| `plan.previousSequence`                  | Reviewed prior sequence, encoded as a decimal string                                                                                               |
| `plan.versions`, `plan.previousVersions` | Current/prior `targets`, `snapshot`, `timestamp` integer versions                                                                                  |
| `plan.expires`                           | Explicit whole-second RFC3339 expiries for those three roles                                                                                       |

File paths resolve relative to the candidate document. Work and output directories
are explicit absolute private paths. No production values are supplied by default.
Descriptor identities come from the verified staging packet and approved asset
record; do not substitute example locators. Each role version advances its reviewed
floor exactly once. Sequence must advance and meet the seed minimum. Prior floors
are reviewed publisher inputs, not an independent ledger or replacements for the
installed client's persisted rollback protections.

Request signatures in dependency order:

```sh
go run ./cmd/fased-release-candidate release-request \
  --candidate-file "$candidate" --work-dir "$work" --role targets

go run ./cmd/fased-release-candidate release-request \
  --candidate-file "$candidate" --work-dir "$work" \
  --approvals-file "$approvals" --role snapshot

go run ./cmd/fased-release-candidate release-request \
  --candidate-file "$candidate" --work-dir "$work" \
  --approvals-file "$approvals" --role timestamp
```

The request contains the exact canonical TUF payload as base64, its SHA256, role,
authorized key IDs and threshold, plus public seed/plan digests for review. Only
the payload is the TUF signing preimage; context digests are supporting review
records. Custodians independently inspect the actual candidate and plan before
signing through their own offline custody process.

The versionless approvals document has `targets`, `snapshot` and `timestamp`
arrays of standard TUF `{keyid, sig}` public signatures. Import the Targets quorum
before requesting Snapshot, then Snapshot before Timestamp. Key identities must
match that role, every signature must verify and duplicate or insufficient
signatures fail. A changed upstream signature set or body requires new dependent
requests. Root rotation is not offered by this tool.

Finalize the complete approvals into a new directory:

```sh
go run ./cmd/fased-release-candidate release-finalize \
  --candidate-file "$candidate" --work-dir "$work" \
  --approvals-file "$approvals" --output "$output"
```

The tool revalidates the original archive, release profile, manifest, provenance,
expiry and role chain through the existing production bootstrap verifier. Output
contains the original root, versioned Targets/Snapshot, Timestamp, content-addressed
descriptor/provenance and `finalization.json`. It neither copies nor executes the
application. The finalization record is verification evidence, not publication,
installer authenticity or an owner execution grant.

Existing output is refused. If a filesystem write is interrupted, preserve and
inspect the partial directory; it is never adopted or overwritten on retry. A
missing, partial or invalid finalization record does not authorize publishing.

## Publication and installed acceptance

Finalization creates no GitHub asset, release, metadata branch or update activation.
Actual tag builds and immutable asset staging need separate owner authorization.
Production single-owner signing inputs, accepted first-installer bootstrap, signed
metadata readback and the installed manual-update journey remain release gates.
Controlled finalizer tests prove acquisition and update admission with the same
verifiers. PR #68 also verifies the installed disposable Ubuntu Remote path:
manual Apply, offline same-ID recovery after an actual new-kernel boot and an
authenticated no-op without another replacement. Checkpoint, ciphertext and
workspace preservation passed. Its source/CI evidence is retained in the internal
execution record and reused while compatible. This accepts the controlled
installed path; independent production first-installer authentication, real
publisher inputs, public readback and other platform rows remain open.

Public/automatic updates, mining and live features remain disabled. The
[release-trust contract](../updates.md#production-release-trust-operations-specification)
defines the retained role and recovery boundaries.
