# Fased App contributor notes

- This is the new application; the sibling `fased` and `server` repositories are reference sources, not dependencies.
- Product/phase authority: `../docs/fased/plan.md`, `implementation.md`, and the Current checkpoint in `execution.md`.
- P2 adds the private Go core, owner authentication, app-owned SQLite, and shared UI/CLI setup. Terminal, agent and wallet content is still explicit prototype data; no wallet keys, provider credentials, RPC or financial execution. WEN terminals remain labelled prototypes.
- Keep shared UI/state independent of a desktop shell. Add Go runtime modules in their planned phase rather than scaffolding them upfront.
- Original code uses PolyForm Perimeter 1.0.0. Preserve third-party terms/notices; record any copied source and its revision. Do not import the legacy code tree.
- Development: `npm ci`, `npm run dev` for the fixture UI. `npm run build` then `npm run build:core` embeds the production UI. The core requires Go 1.26; no Node runtime is required by the compiled application.
- Verify affected behavior with `npm run check`, `npm run test:core`, `npm run test:ui`, and `npm run test:core-ui`; use `git diff --check`. Core browser tests need the built binary. Test state is disposable, reconstructible and nonauthoritative; results belong in `.evidence/`.
- Keep the listener loopback-only, CLI transport OS-protected, owner/session material private, and setup writes revision-checked. Never make a demo preference imply a connected account or signing authority. Core state and future signer state have independent owners.
- Browser evidence and disposable test outputs belong in ignored `.evidence/`. Preserve failure evidence until resolved; delete only task-created reconstructible artifacts.
- Main is the delivery branch; work on short feature branches. Source uses the selected PolyForm Perimeter license. Never force-push, merge, tag, publish a release or install on an owner host without established authority.
- Commit author and committer must both be `fcode-ai <155309118+fcode-ai@users.noreply.github.com>`. Verify `git var GIT_AUTHOR_IDENT` and `git var GIT_COMMITTER_IDENT` before committing; do not use the inherited former identity.
- Never commit credentials, keys, user state or dependency/build outputs. Preserve unrelated edits.
