# Contributing

Start with [AGENTS.md](AGENTS.md), the active phase, and a bounded change. Keep financial execution, wallet signing, mining activation and customer automatic updates strictly disabled unless explicitly authorized for a scoped milestone; never commit credentials, private keys, or generated build artifacts.

Contributions are offered under the same PolyForm Perimeter 1.0.0 terms as the original project code. This does not transfer a contributor's copyright or grant unrelated relicensing rights. Preserve applicable third-party notices and identify copied source and its origin before adding it.

Use a feature branch and PR with the behavior changed, validation and limitations. Run affected checks (`npm run check`, `go vet ./...`, `go test -race`, fuzz seed corpus checks, and relevant Playwright specs); keep generated output and private data out of Git. Merging, publishing releases and owner-host installation remain separate actions.

Run `node --test --test-isolation=none scripts/check-docs.test.mjs` and `node scripts/check-docs.mjs --documentation` for documentation checks. The default foundation validator still rejects application source. The required `prototype` aggregate now requires documentation plus the selected application lanes; documentation checks never certify runtime behavior. Use the protected-base routing policy and reuse compatible prior evidence.
