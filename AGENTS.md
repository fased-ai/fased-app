# Fased App contributor notes

- This is the new application; the sibling `fased` and `server` repositories are reference sources, not dependencies.
- Product/phase authority: `../docs/fased/plan.md`, `implementation.md`, and the Current checkpoint in `execution.md`.
- P1 is a React/TypeScript interactive prototype only. Use explicit fixtures; no wallet keys, provider credentials, RPC or financial execution. WEN terminals remain labelled prototypes.
- Keep shared UI/state independent of a desktop shell. Add Go runtime modules in their planned phase rather than scaffolding them upfront.
- Original code uses PolyForm Perimeter 1.0.0. Preserve third-party terms/notices; record any copied source and its revision. Do not import the legacy code tree.
- Development: `npm ci`, `npm run dev`. Verify changed behavior with `npm run check` and affected `npm run test:ui` cases; use `git diff --check`.
- Browser evidence and disposable test outputs belong in ignored `.evidence/`. Preserve failure evidence until resolved; delete only task-created reconstructible artifacts.
- Main is the delivery branch; work on short feature branches. Never force-push, merge, tag, publish or install on an owner host without established authority. Public source push waits for a selected license.
- Never commit credentials, keys, user state or dependency/build outputs. Preserve unrelated edits.
