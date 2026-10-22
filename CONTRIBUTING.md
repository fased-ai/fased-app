# Contributing

Start with [AGENTS.md](AGENTS.md), the active phase, and a bounded change. P1 uses fixtures; do not add credentials, wallet execution, arbitrary plugins or protocol integrations.

Contributions are offered under the same PolyForm Perimeter 1.0.0 terms as the original project code. This does not transfer a contributor's copyright or grant unrelated relicensing rights. Preserve applicable third-party notices and identify copied source and its origin before adding it.

Use a feature branch and PR with the behavior changed, validation and limitations. Run the affected checks; keep generated output and private data out of Git. Merging, publishing releases and owner-host installation remain separate actions.

The current main branch contains the licensing and documentation foundation. Run `node --test --test-isolation=none scripts/check-docs.test.mjs` and `node scripts/check-docs.mjs` for its documentation checks. The required `prototype` job aggregates those results. The foundation validator rejects application source; integrating the application must bring its owning runtime checks and preserve the required aggregate, rather than expand documentation validation to certify runtime behavior.
