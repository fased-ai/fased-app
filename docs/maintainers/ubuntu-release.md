# Ubuntu releases

Fased uses a protected GitHub release workflow and automatic build attestations.
The first target is Ubuntu 24.04 amd64. Customers need no Go, Node, signing key
or GitHub account to install Fased.

## Release

From the committed source branch with its reviewed PR:

```sh
npm run release
```

The command checks the fixed repository and required CI, merges the exact passing
head, creates or verifies its annotated version tag, and waits for one Linux
build. GitHub authenticates the archive and installer with its workflow identity.
The actual installer verifies the candidate before publication. The release is
immutable; anonymous readback verifies the published installer and package.

There is no publisher password, desktop keyring setup or manually managed release
key. Existing GitHub authentication is reused. The workflow receives its own
scoped GitHub token; verifier processes receive no publishing credentials.

## Install and Update

The official entry is
`https://github.com/fased-ai/fased-app/releases/latest/download/install.sh`.
It verifies the Go installer, then opens the existing Local/Remote wizard.
Installed manual Update uses the same admission and controller as the installer,
including identity, version/sequence checks, data preservation and recovery.
An older installation uses the official bootstrap Update handoff to acquire the
new verifier; existing trust pins are not silently changed.

## Recovery

Rerun the same command after interruption. Keep the original build artifact and
operation records under `.evidence/ubuntu-release/`; do not move tags, replace
immutable assets, regenerate keys or rebuild an existing release. A failure after
publication resumes verification of that exact release. Missing or ambiguous
original bytes hold the operation for inspection.

Customer automatic updates, live AI, wallet/trading and mining remain disabled.
An owner release instruction authorizes its complete conditional pipeline; it
does not enable customer automatic installation or publish unrelated changes.
