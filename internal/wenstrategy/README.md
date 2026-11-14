# WEN strategy contracts (F1)

This pure package mirrors `wen/packages/strategy-contract`. It adds no HTTP
handlers, tasks, storage, wallet access, signing, transaction submission or
settlement calculations. WENSAT remains internal construction metadata; market
identity is the complete `Market` binding, never a ticker.

- `Decode` validates the closed `wen-strategy/1` JSON, preserves a copy of exact
  UTF-8 bytes and computes their SHA-256. `Parse` returns only the typed document.
- `CompileAllocation` uses integer largest-remainder allocation, ties A–E,
  rejecting dust or positive weights rounded out of existence.
- `CandidateSlots`, `PinRandom` and `Eligible` are pure scheduling decisions over
  explicitly supplied canonical slot metadata. The caller supplies complete
  inventory, approved pinned random IDs and previously accepted-round state;
  pinning verifies consecutive slot/time coverage but not authenticity. These
  functions do not reserve or execute a round.
- `Evaluate` consumes admitted `wen-research/1` integer metric records. Missing,
  stale, disconnected, gapped, ambiguous or differently bound inputs skip the
  decision. A conditional branch with unavailable inputs cannot fall through to
  another branch. No price or volatility arithmetic is duplicated here.
- `CheckLimits` checks supplied turnover, unsettled capital, fee and gross-loss
  state plus a proposed reservation. It cannot make that reservation atomic or
  establish the correctness of a supplied canonical worst-case deduction.

A parsed mandate reference grants no authority. Runtime use still requires
actual binding admission, custody permissions, atomically reserved state,
commit/reveal ownership and the existing financial execution gates.

Focused checks:

```sh
go test ./internal/wenstrategy
WEN_STRATEGY_CORPUS=../wen/packages/strategy-contract/fixtures/conformance.json \
  WEN_STRATEGY_OUTPUT=.evidence/f1-strategy-contract/go-conformance.json \
  go test ./internal/wenstrategy -run TestSharedConformance -count=1
```

The default Go test run consumes the exact copied shared corpus under `testdata/`;
`testdata/source.json` records its source paths and SHA-256 digests. The optional
`WEN_STRATEGY_CORPUS` override permits joined source parity checks. The joined
preview test compiles the copied five-SOL strategy, matches its five allocations
to the canonical PM1 all-five fixture and verifies canonical request/receipt bytes.
It does not calculate settlement or submit transactions.

The corpus driver exists only in `_test.go`; it is not a production command.
All amounts/IDs are bounded canonical u64 decimal strings; signed research
thresholds are i128. JSON counters forbid exponent/fraction forms and negative
zero. Documents use exact-byte versions, not normalized JSON digests.
