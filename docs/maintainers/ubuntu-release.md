# Ubuntu releases

Fased uses saved GitHub authentication and automated build attestations. Customers install a compiled package through one entry and use Settings → Update or the installed `fased update`. They need no Go compiler, Node, source checkout or release-signing password.

## Status and authority

This is the controlling staged-package acceptance contract for Fased App. The active queue and permissions remain in `/home/fc/fasedbot/docs/fased/execution.md`. B01 is the active connected installation/update package. The producer, installed-acceptance consumer and publisher must complete the same exact package journey before promotion; code and focused checks alone are insufficient. Mining and live AI/wallet/trading remain disabled. Automatic updating is an explicit user choice, tested against controlled releases with Manual restored afterward.

The release command must require the trusted workflow’s matching installed-acceptance artifact before creating a tag or publishing. If any required leg is absent or failed, promotion stops. Current records the actual verification status; no release is ready merely because this contract is documented. Existing published releases and trust profiles remain immutable.

## One candidate, one acceptance flow

| Stage                | Required result                                                                                                                                                                                                                                                               |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Review source        | Finish one connected correction, focused checks, protected PR and required CI. Merge only within recorded authority. Ordinary PRs and merges create no tag.                                                                                                                   |
| Stage once           | Freeze the reviewed source commit/tree and one proposed version. An authorized run of the existing protected Ubuntu workflow builds and attests one unpublished candidate from that commit, without a release tag. Retain its artifacts and digests.                          |
| Accept the package   | Exercise the complete supported Local/Remote install and update journey below using that candidate. Record exact inputs and every required result. A failed or missing leg holds promotion.                                                                                   |
| Promote once         | Check the acceptance record against the exact source, workflow/run/attempt and artifact digests. Create the annotated tag at the accepted commit only after PASS. Publish the retained artifacts without rebuilding, recompiling, repacking or rerendering accepted payloads. |
| Confirm distribution | Verify real immutable GitHub release/asset identities, attestations and anonymous download digests. Then perform any authorized existing-owner update through the normal product entry. Public readback confirms distribution; it cannot replace installed acceptance.        |

The candidate identity is the source commit/tree, proposed version, platform, workflow identity, run ID/actual attempt and artifact inventory digests. Preserve that identity in the existing candidate directory and publisher journal. Changing relevant bytes or inputs creates a new unpublished candidate under the same proposed version and invalidates affected evidence. No duplicate full journey before and after a tag is required. A tag does not rebuild the candidate.

## Staged acquisition and trust boundary

Use the existing candidate verifier, controlled transport fixtures, packaged installer, wizard and update controller. Extend those facilities only where the joined route is missing. No new service, trust system or customer setup command is required.

- Acquire the candidate from the exact authenticated CI artifact and verify its inventory and original attestation before executing it. Candidate provenance must bind the protected repository/workflow, reviewed source commit and actual run/attempt. A proposed tag is not evidence that the build came from a tag.
- Adapt the existing provenance policy explicitly for protected, commit-bound staging. At promotion, bind the final annotated tag to that same accepted commit and original artifact provenance. Preserve strict verification for existing tag-bound profiles. Never relabel a workflow event, forge an attempt or relax repository/workflow/source checks to make a candidate pass.
- The controlled acceptance harness supplies staged release discovery/download responses at the existing acquisition boundary. It runs the packaged entry, verifier, wizard, authentication and installation/update controller with the retained executable/package bytes. Only the external delivery responses differ. Record this as controlled staged acceptance, not proof of a public immutable release. The harness must exercise the entry's actual TTY, root-to-ordinary-user and profile handoff; directly invoking `install --bundle` alone cannot close the gate.
- Staged access is confined to the acceptance harness and its disposable state. Ordinary customer acquisition must continue to reject drafts, mutable/unattested artifacts and arbitrary endpoints. No test CA, local-path bypass or staging switch may enable ordinary production admission. The missing harness connection is implementation work, not a currently available command.
- Release/asset IDs created during promotion belong in publication receipts and journals. Every executable, archive, embedded UI and customer entry script accepted by digest must be published unchanged. Any metadata affecting acquisition, compatibility or execution is also an acceptance input. If it cannot be finalized before publication, resolve that producer/consumer contract before calling the candidate ready; do not patch binaries after acceptance.

## Complete packaged journey

Use existing authorized Ubuntu environments. Fresh disposable installs need their recorded environment authority; an existing owner workspace is upgraded in place with its data preserved. Reuse supported platform/private-access proofs when their inputs remain valid.

1. Run the packaged official entry in a real terminal. Local opens the automatic wizard and local browser. Remote starts from SSH, handles root-safe OS preparation, drops privileges before Fased runs, and guides SSH or Tailscale to the private browser link. Do not add an operator wrapper that changes the entry's TTY or privilege behavior.
2. Finish first-owner password setup and reopening/sign-in/logout through the normal product UI. Existing workspaces keep their identity and authentication. Installation needs no provider, wallet or copied Fased claim code.
3. Upgrade a supported predecessor using the candidate through authenticated Settings → Update and the installed CLI route. Cover the older-install official bootstrap handoff when needed. One flow must complete without an evidence script or an internal path supplied to the customer.
4. Verify usable workspace, preserved records/drafts/authentication and applicable signer/recovery holds, restart and Remote reboot/reconnect. Repeating Update reports Already current without rewriting state. Verify cancellation and interrupted recovery at changed boundaries; reuse unchanged compatible recovery evidence.
5. Keep automatic-update implementation evidence separate from activation. Reuse controlled automatic-mode proof where unchanged, and exercise changed shared-controller behavior with fixtures. Customer scheduling/activation, live AI, wallets/trading and mining remain disabled.

Keep one acceptance record in the existing candidate/evidence area and summarize it in Current. It records exact input identities, environment/profile, literal entry, predecessor, preservation baseline/result, each required leg and reused evidence with its validity basis. Required legs must be PASS or carry a reviewed supported-scope exclusion; unresolved FAIL/BLOCKED cannot be hidden as N/A. The publisher must independently verify the matching trusted CI acceptance result, not trust a user-editable local `PASS` field. Owner credentials and sessions never enter artifacts or logs.

## Recovery and tag discipline

- Reuse a valid candidate and its original provenance when resuming publication. Inspect the journal and remote release/asset state before mutation. A timeout after a write requires readback, not blind recreation.
- One proven transient pre-artifact infrastructure failure may receive one bounded same-source retry with its real attempt. The conductor must bind that actual successful attempt. Existing attempt1-only checks need correction; do not falsify their input or consume a new version.
- A failed candidate stays unpublished and untagged. Correct the cause, run affected checks and replace only invalidated evidence. Preserve useful original failure records.
- Never move/delete a release tag or replace published assets. If an exceptional source defect is found after tagging, hold publication. Changed source must pass the same gate before a later deliberate release; never create a chain of diagnostic tags.
- A publication failure resumes the accepted candidate without a rebuild. If its original artifacts or trusted acceptance evidence are unavailable, hold and record the missing input.

## User experience and remaining work

The official download location remains `https://github.com/fased-ai/fased-app/releases/latest/download/install.sh`. Customer Update uses the installed UI/CLI and the same preserving controller. A valid owner session is reused; an expired CLI session signs in within that command. OS consent stays in guided setup. No release password or developer helper is added to either flow.

The remaining connected implementation package is the literal terminal/authentication handoff, pre-tag candidate staging and installed acceptance, digest-bound promotion, and actual-attempt recovery. Existing `fased-install --verify-candidate` verifies artifacts only; it does not install them. The current public verifier correctly requires an immutable stable release. Keep that public rule while implementing the isolated staging boundary. Required CI remains governed by protected repository policy.

Publication, owner deployment and automatic-update activation are separate authorities. Reuse an already recorded authorization within its stated scope. Authority comes from the owner instruction recorded in Current, not this guide.
