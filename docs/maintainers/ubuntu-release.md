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

## Signing and publication remain separate

The staging packet is **not yet a signing request**. Exact GitHub release/asset
locators are available only after separately authorized asset staging. A subsequent
publisher step must construct the real descriptor and Targets/Snapshot/Timestamp
payloads from those locators and the actual verified bytes, obtain independent
quorum signatures, and exercise final production bootstrap/manual-update
verification before publication. That finalizer is not implemented by this
staging package; do not fabricate locators, provenance or metadata to bypass it.

Initial installer authentication and production signing remain open acceptance
gates. Public/automatic updates, mining and live features remain disabled.
The [release-trust contract](../updates.md#production-release-trust-operations-specification)
defines the retained role and recovery boundaries.
