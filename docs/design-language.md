# Shared Fased design language

Fased uses WEN's **Muted Pro** design language (`web/src/vendor/wen-design/style.css`, `web/src/primitives.css`, `web/src/shell.css`) synced directly from WEN's terminal theme (`retail-theme.css`), with full Dark and Light mode parity and WCAG 2.1 AA contrast checked in CI (`core-a11y.spec.ts`).

| Element       | Dark Mode (`:root.wt`)                                                                                                                    | Light Mode (`:root.wt[data-theme="light"]`)                                                                      |
| ------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| Surface       | Obsidian canvas `#0A0C10`, panel `#0F1218`, raised `#161A22`, hover/selected `#1D222C`, hairline `rgba(255, 255, 255, 0.06)`              | Canvas `#F3F4F7`, panel `#FFFFFF`, raised `#F1F3F6`, hover/selected `#E6E9EE`, hairline `rgba(16, 24, 40, 0.08)` |
| Text          | Primary `#E6E9F0`, secondary `#BEC5D2`, muted `#868FA2`, dim `#5D6678`, CTA button ink `#FFFFFF`                                          | Primary `#121722`, secondary `#343D4B`, muted `#505A6B`, dim `#5F697A`, CTA button ink `#FFFFFF`                 |
| Accent        | Single rose accent `#E5476A` (`--wt-coral`), hover `#EC5A7A` (`--shell-primary-hover`); neutral secondary `--wt-violet: var(--wt-text-2)` | Rose accent `#C92A4B` (`--wt-coral`), hover `#B4203F` (`--shell-primary-hover`)                                  |
| Status        | Range/status dots & bars (`#E0566F`, `#D98466`, `#6F7889`, `#2FB88C`, `#33A9B8`, `#D4A24C`) without highlighter card rings                | High-contrast semantic tones (`#C83E58`, `#B8603C`, `#5B6475`, `#178663`, `#1B7989`, `#A6731E`)                  |
| Layout        | Single `56px` borderless top bar (`Fased`, `Mine`, `Trade`, `Tasks`, `News`, `Chat`, `More`), zero left rail, right icon bar              | Full-width `100dvh` 3-pane borderless workspaces (`border: 0`), responsive mobile switcher `<= 760px`            |
| Numbers       | `tabular-nums`, compact precision with full raw precision in hover `title`, explicit units/venues                                         | Same numeric formatting and subtle focus-visible rings                                                           |
| Accessibility | Visible keyboard focus rings, icon buttons with explicit `aria-label` + `title`, zero sole-color status cues                              | Audited Dark & Light in `web/tests/core-a11y.spec.ts`                                                            |

## Navigation and UI discipline

The single `56px` top navigation bar organizes **Mine (`#mine`), Trade (`#trade`), Tasks (`#tasks`), News (`#news`), Chat (`#agents/chat`), and More (`Lab`, `Overview`, `Agents`, `Network`, `Activity`)**, with right-side **Wallets (`#wallets`)**, notifications, theme toggle, **Settings** gear opening `<SettingsModal>`, and instance badge. Legacy highlighter washes, box-in-box borders, form walls, and redundant navigation rails are replaced by neutral luminance steps, compact `28×28px` icon buttons, and on-demand modal dialogs (`AGENTS.md`).

## WEN Lean V1 (`LV1X`) Mining Alignment

The `#mine` terminal mirrors WEN's 3-pane Lean V1 workspace (`Mine`, `Claim`, `Stake`, `Auto Task`) backed by isolated `fased-signer` `RoleMining` custody (Devnet enabled; Mainnet gated until official Mainnet `LV1X` program IDs and verified ELF hashes are published and explicitly enabled by the owner), while legacy fixture code (`internal/wenpm1`) remains untouched and deprecated (`miningActivationEnabled: false`).
