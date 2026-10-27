# Contributing

Start with [AGENTS.md](AGENTS.md), the active phase, and a bounded change. P1 uses fixtures; do not add credentials, wallet execution, arbitrary plugins or protocol integrations.

Contributions are offered under the same PolyForm Perimeter 1.0.0 terms as the original project code. This does not transfer a contributor's copyright or grant unrelated relicensing rights. Preserve applicable third-party notices and identify copied source and its origin before adding it.

Use a feature branch and PR with the behavior changed, validation and limitations. Run the affected checks; keep generated output and private data out of Git. Merging, publishing releases and owner-host installation remain separate actions.

Run `node --test --test-isolation=none scripts/check-docs.test.mjs` and `node scripts/check-docs.mjs --documentation` for documentation checks. The default foundation validator still rejects application source. The required `prototype` aggregate now requires documentation plus the selected application lanes; documentation checks never certify runtime behavior. Use the protected-base routing policy and reuse compatible prior evidence.
