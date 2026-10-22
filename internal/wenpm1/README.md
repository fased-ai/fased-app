# PM1 disposable fixture client

This pure Go package implements WEN's **fixture-only minimal PM1 v1** profile.
It is not imported by the core, UI, CLI, signer, scheduler or installer. It adds
no RPC, transport, private-key access, signing, sending, policy grant or live
execution. WEN's complete PM1 and deployment/admission gates remain open. The
Satcoin Mining UI remains an inert prototype.

`testdata/source.json` pins the copied canonical descriptor and unmodified
Rust-produced golden vectors to Satcoin revision
`55a4dc562c2516a48f500047df85cb81fabf8084`. These are owner-authorized interface
data, not imported legacy implementation. Models, codecs and tests are Fased
original code under the repository's PolyForm Perimeter license. There is no
test/runtime dependency on the private sibling repository, Node or Python.
The copied schema and fixtures retain their exact bytes and provenance.

- `EncodeRequest` / `DecodeRequest` cover Open, Commit, Reveal, Observe, Settle,
  ClaimSol and ClaimSat. `BuildInstruction` returns unsigned public bytes and
  account-role metadata, never a transaction or executable action. Use value
  requests such as `Open{Slot: 7}`; nil and pointer requests refuse.
- `ReadAccount` verifies caller-supplied address, owner and all five bindings.
  These structural checks do not authenticate RPC/source data. The caller must
  supply authenticated state before using `ProjectReceipt`.
- Account/receipt codecs enforce exact frame/profile/kind/version, resource
  bounds, sorted unique annual keys, tags and identity matching. Only accounts
  allow zero allocation padding. Receipts are unsigned state projections,
  **not payment/execution proofs**.
- Native Go integers and `Uint128` preserve the full wire ranges. Parsing a
  decimal `Uint128` or hex public identity rejects noncanonical representations.
  `Commitment` preserves the canonical domain, version and all bindings, including
  v1's same-owner beneficiary field.

The codec preserves structural wire values; it does not simulate or approve
business rules, validate oracle evidence, or assert spend authority. No fixture
identity is a deployment default. A future live profile needs separate admission
and implementation; changing this fixture flag is not an activation mechanism.

Run `go test ./internal/wenpm1` for byte/metadata/commitment parity, complete
schema-layout checks, malformed/bounded-input refusal and fuzz seed regressions.
No new module, SDK, service, dependency or workflow is needed.
