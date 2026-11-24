# Satcoin Mining in Fased (`NOT READY / DO NOT IMPLEMENT NOW`)

> **Owner Hold (2026-11-24): WEN `SAT` Mining & Staking execution is marked `NOT READY / DO NOT IMPLEMENT NOW`.**
> Live WEN mining activation (`miningActivationEnabled: false`) is strictly disabled, hidden from navigation (`#terminals/satcoin` decoupled from `walletSigner` and `financialExecution`), and excluded from Stage 4 crypto execution until WEN Lean V1 (`LV1X` Option 3) completes audit and Mainnet deployment and receives explicit owner re-authorization.

**WEN** is an independent Bitcoin/Solana asset-mining protocol whose canonical specification and live economics live in the WEN Lean V1 documentation (`https://docs.wen.network` and `https://docs.satcoin.app`). Fased's `internal/wenpm1` package holds pre-Lean-V1 fixture code only (`PM1`) and is not wired for live Devnet/Mainnet Lean V1 (`LV1X`) participation.

## Round results and safety boundaries

When WEN Lean V1 (`LV1X`) integration is separately designed, audited, and explicitly authorized in the future, Fased will adhere to these boundaries:

1. Present real-time BTC context, published ranges, and commitment/reveal/settlement phases with clear units (SOL principal vs conditional BTC vs unlocked SAT).
2. Enforce explicit owner budgets, per-round limits, expiry, pause controls, and crash-safe uncertainty reconciliation without repeating unconfirmed transactions.
3. Require verified Lean V1 program bindings inside the isolated `fased-signer` before any financial permission can be requested or enabled.
4. Keep manual, CLI, and agent triggers subject to separate per-action permissions — connecting a wallet or provider never grants mining authority.

Use [WEN documentation](https://docs.wen.network), [Satcoin documentation](https://docs.satcoin.app), and [Availability](reference/status.md) for current boundaries.
