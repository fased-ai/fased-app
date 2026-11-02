# Ubuntu release preparation

The proposed first version is **0.1.0**, targeting Ubuntu 24.04 amd64. This is a
publisher procedure; customers do not manage release-signing keys.

The staging workflow in `.github/workflows/release.yml` is dormant. It creates no
GitHub Release, signed update metadata, deployment or update activation. Its
current configuration intentionally lacks production trust. Do not create a tag
or run a release build until the owner authorizes it.

## Ubuntu0.1.0 local signing and recovery

The owner approved the GitHub installer URL and single-owner local signing plan.
The engineering baseline is main `7a2ee5112aa7241e80df82e2e6c70577c605c632`;
no release source/tag is selected and no production key or public trust is bound.
The first public row remains Ubuntu24.04/amd64.

| Input               | Recorded direction                                                                                                                                                     |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Owner               | The owner operating `fcode-ai`; one owner/workflow, four distinct internal role keys under `single-owner-v1`.                                                          |
| Official installer  | `https://github.com/fased-ai/fased-app/releases/latest/download/install.sh` after publication.                                                                         |
| Exact first version | `https://github.com/fased-ai/fased-app/releases/download/v0.1.0/install.sh` after publication.                                                                         |
| Trust               | GitHub HTTPS for the first script; the rendered script pins verifier bytes/public seed. Installed Update verifies signed metadata, provenance, expiry and floors.      |
| Key storage         | One encrypted local keystore and a versioned encrypted local recovery archive, outside Git and customer/application data. No cloud integration or additional hardware. |

These URLs are not live release downloads. Do not distribute the raw repository
`install.sh` template. The delivered first script uses a fixed versioned verifier
URL/hash; `latest` locates the initial script, not installed update authority.
Customers keep the same one-command Local/Remote wizard and Install → Update;
release keys and recovery files belong only to the publisher.

### One owner workflow

The existing maintainer entry provides:

- `node scripts/prepare-ubuntu-release.mjs signer-setup`: confirm a privately
  entered terminal passphrase, generate four separate Ed25519 roles, encrypt the
  local store and retain an encrypted recovery archive plus public identity.
- `node scripts/prepare-ubuntu-release.mjs signer-sign`: unlock once, request exact
  role payloads from the pinned Go finalizer, sign only Targets/Snapshot/Timestamp
  in that plan, and produce verified metadata. No publisher/network dispatch.
- `node scripts/prepare-ubuntu-release.mjs signer-recover`: verify the encrypted
  archive and independently retained public-identity digest, restore into a
  separate private directory without overwriting existing files, and verify a
  harmless signature from every restored role.

The commands read the private reviewed
`.evidence/ubuntu-release/local-signing-request.json`; the tooling/caller prepares
operation paths/pins, not four manual key ceremonies. `version:1`, explicit
`keyClass` (`disposable` or `production`) and `mode` (`start` or `resume`) are
required. Setup adds `directory`/`archiveDirectory`; recover adds `directory`,
`archiveFile`, `archiveSHA256` and `identitySHA256`. Signing adds `directory`,
`storeFile`, `identitySHA256`, `candidateFile`/`candidateSHA256`,
`seedFile`/`seedSHA256`, `verifierFile`/`verifierSHA256`, `workDirectory` and
`outputDirectory`. Paths are explicit absolute locations with owned, stable
parents. The tool creates private working/archive leaf directories; it never
changes existing permissions to make unsafe locations pass.

Passphrases are accepted only through the hidden terminal prompt, not request
fields, environment variables, arguments, logs or operation state. They must be
16–1024 UTF8 bytes. The encrypted format fixes scrypt N131072/r8/p1 and
AES256-GCM, binding repository/profile/key class and public identity. Private
PKCS8 keys exist only in the owner process during the operation; no plaintext PEM
file is required by the new workflow. Buffer cleanup is best effort; Node/V8
strings/KeyObjects do not provide a guaranteed memory-erasure contract. Use a
short-lived trusted maintainer process, not an agent session/service.

The existing production ownership gate remains closed: `production` operations
need a private `ownerApprovalFile` matching configured
`custodyApprovalSha256` before private input/key admission. Current configuration
has null approval/trust fields; only disposable fixture setup was exercised.
Nothing here authorizes real key creation or populates those fields.

### Recovery and interrupted operations

The existing private journal freezes the encrypted setup artifact before file
publication. Registered temporary ciphertext, no-replace file linking, file and
parent-directory synchronization preserve the same identities through resume.
Changed bytes, permissions, identity, paths or an unknown operation refuse rather
than overwrite work. Journal state contains encrypted key material, never a
passphrase/plaintext key. Treat it as private and preserve it with the local
keystore and recovery receipt.

Signing journals retain public approvals and exact plan identity. A lost finalizer
acknowledgement or completed no-op uses Go `release-inspect` to revalidate the
full existing output against the current candidate/seed/approvals, without new
signatures or replacement. Partial, tampered or unexpected output remains held.
Neither key restoration nor inspection resets metadata floors, release/asset IDs,
publisher journals, unknown outcomes or application state.

Metadata refresh can use `localSigner: {storeFile, identitySHA256}` in its existing
reviewed request instead of `onlineKeyFiles` (never both). It lazily unlocks once
only if signing is due, accepts Snapshot/Timestamp requests only, and closes after
the operation. Not-due checks do not read/unlock keys. This creates no scheduler,
unattended unlock or automatic-install permission. Production refresh still needs
all existing trust bindings and production-class identities.

Two local directories can recover an accidentally deleted/corrupt working store;
they cannot recover loss of this computer/disk or all copies/passphrases. GitHub
administration/public keys cannot recreate lost private keys. Total loss needs a
separately authorized trusted rebootstrap path; no transparent replacement or
customer-data reset is provided. Signed key rotation and actual production
backup/operator acceptance are separate from this local restore proof.

Controlled evidence covers setup/sign/lost-ack/restore/refresh, hidden prompt and
cancellation, identity/pin/permission refusal, and interrupted ciphertext/directory
sync. It is not production-key, public-download or whole-product acceptance.
No cloud/removable-storage decision is outstanding for the active local plan.

## Inputs required before building

- Record the already approved single-owner local method, GitHub publishing
  account/URLs and first-download trust assumption in the actual public approval inputs.
- Separately authorized local key setup, encrypted working/recovery storage and
  restore evidence; public keys/fingerprints only for review.
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
