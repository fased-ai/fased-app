# Shared Fased design language

Fased uses the Obsidian-and-Coral palette (`web/src/shell.css`, `web/src/styles.css`) across desktop browser, mobile PWA, and Wails native wrappers, with full Dark and Light mode parity and WCAG 2.1 AA contrast checked in CI (`core-a11y.spec.ts`).

| Element       | Dark Mode (`:root.wt`)                                                                                                      | Light Mode (`:root.wt[data-theme="light"]`)                                                                 |
| ------------- | --------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| Surface       | Obsidian canvas `#0d0d12`, surface `#161621`, raised `#1f1f2d`, border `#252536`                                            | Canvas `#f3f3f8`, surface `#fff`, raised `#ececf4`, border `#d7d7e4`                                        |
| Text          | Primary `#f5f5fa`, muted `#8f8fa6`, coral button ink `#160a10`                                                              | Primary `#242231`, muted `#626177`, coral button ink `#fff`                                                 |
| Accent        | Coral `#ff4f7c` (`--wt-coral`), hover `#ff7a8e` (`--shell-primary-hover`), cyan `#27c5da`, violet `#8b6fff`                 | Coral `#b51e43` (`--wt-coral`), hover `#9d1637` (`--shell-primary-hover`), cyan `#087786`, violet `#6640bd` |
| Status        | Ok `#5be0a5`, warn `#f3c677`, danger `#ff7e92`, info `#7cd8e6`                                                              | High-contrast semantic surface/border pairs meeting WCAG 2.1 AA across Dark and Light                       |
| Layout        | Quiet top bar (`Overview` logo, search, `Wallets`, alerts, connection), collapsible left rail, bottom-left `Settings` modal | Full-width responsive canvas with mobile switcher below `<= 760px`                                          |
| Numbers       | Tabular numeric alignment, explicit units/venues, honest estimation markers for backfill vs live ticks                      | Same numeric formatting and keyboard focus-visible rings                                                    |
| Accessibility | Visible keyboard focus rings (`outline: 2px solid var(--lime)`), labeled controls, zero sole-color status cues              | Audited Dark & Light in `web/tests/core-a11y.spec.ts`                                                       |

## Navigation and UI discipline

The primary left rail organizes **Overview (`#overview`), Trade (`#trade`), Intel (`#intel`), Lab (`#lab`), Chat (`#agents/chat`), Tasks (`#agents/tasks`), Agents (`#agents/settings`), and Wallets (`#wallets`)**, with bottom-left **Settings** opening `<SettingsModal>` (`General`, `Providers`, `Instance`, `Notifications`, `Updates`, `Diagnostics`). Legacy secondary taglines and inline helper walls are replaced by direct controls and on-demand info dialogs (`AGENTS.md`).

## Deferred WEN Mining surface

The 3-pane WEN allocation histogram terminal (`#terminals/satcoin`) shares these Obsidian/Light CSS tokens (`MiningHistogramCharts.tsx`) but remains **strictly hidden and disabled (`NOT READY / DO NOT IMPLEMENT NOW`, `miningActivationEnabled: false`)** behind an independent gate decoupled from `walletSigner` and `financialExecution`. When authorized later after WEN Lean V1 Mainnet launch, visual alignment must never conflate conditional estimates with claimable rewards or bypass isolated signer limits.
