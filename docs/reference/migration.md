---
title: "Documentation migration"
description: "Current Go-app guides and preserved legacy source."
---

The new documentation retains Mintlify Markdown/frontmatter, `docs.json`, Fased branding and the existing diagram style. The active navigation covers the Go application and the separately planned trading Network.

The complete old `fased/docs` working-tree corpus is copied byte-for-byte into `docs/legacy/`, including translations, images and its original Mintlify source. `migration-manifest.json` records every file hash and the source revision. `legacy-source-license.txt` preserves the source license. The existing eight new-app engineering guides are unchanged.

Legacy gateway, plugin, messenger and deployment instructions describe the old application. They are excluded from this site's processing and search with [Mintlify's `.mintignore`](https://www.mintlify.com/docs/organize/mintignore). Preserved engineering originals also stay in source, outside the public navigation, while these guides explain the current user journey.

The old hosted docs remain the serving site until a separately reviewed Mintlify repository/branch switch. Copying the documentation changes no domain, installation, data, server or public update setting. No hosting cutover is performed by this migration.

Source: [new repository docs](https://github.com/fased-ai/fased-app/tree/main/docs). The migration branch is `docs/network-trading-roadmap`; pages become production documentation only after review and the hosting decision.
