# Ubuntu release preparation

The proposed first version is **0.1.0**, targeting Ubuntu 24.04 amd64. This is a
publisher procedure; customers do not manage release-signing keys.

The staging workflow in `.github/workflows/release.yml` is dormant. It creates no
GitHub Release, signed update metadata, deployment or update activation. Its
current configuration intentionally lacks production trust. Do not create a tag
or run a release build until the owner authorizes it.

## Inputs required before building

- An approved public version-3 bootstrap seed: production TUF root, metadata
  branch/transport identity, minimum sequence and offline Sigstore trust roots.
- Independent signing custodians and recovery arrangements: Root and Targets
  each require two of three distinct keys; Snapshot and Timestamp each use a
  separate key. Private keys stay outside source, CI and chat.
- An approved independent way to authenticate the first installer and its trust
  material. A binary and checksum from the same download location do not supply
  independent authentication.
- Recorded custody and initial-trust approvals, then separate owner authorization
  for the canonical annotated tag and its build. Configuration digests record
  those reviews; they are not signatures or substitutes for them.

Record only the reviewed public seed path, digest and approval-record digests in
`release/ubuntu.json`. Public repository and owner IDs are pinned there. Missing
values stay `null`; development keys must never fill them.

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
Production trust/custodians, independent first-installer authentication, signed
metadata readback and the installed manual-update journey remain release gates.
Controlled finalizer tests prove first acquisition and update admission with the
same verifiers. Unchanged controller commit, no-op and interrupted-completion
proof is reused; those tests do not claim a fresh installed-service acceptance.

Public/automatic updates, mining and live features remain disabled. The
[release-trust contract](../updates.md#production-release-trust-operations-specification)
defines the retained role and recovery boundaries.
