# Shared Fased and WEN design direction

Use one recognizable market workspace across Fased and WEN, while retaining each product's navigation and branding. This is a design direction, not a shipped cross-app integration.

The shipped Fased App WEN design system uses obsidian surfaces (`#0d0d12`), restrained ember-orange accents (`#FF5A36` / `#ff6b35`), compact cards, readable tables, full Light mode parity (`#f5f4f8`), and clear activity state. Keep Fcode Lab as the umbrella; give Satcoin Mining the same round terms and financial labels wherever it appears.

| Element       | Shared direction                                                                                             |
| ------------- | ------------------------------------------------------------------------------------------------------------ |
| Surface       | Obsidian background `#0d0d12`, panels `#161621`, borders `#242434` (Light mode `#f5f4f8` / `#ffffff`).       |
| Text          | High-contrast primary text `#f0eef5` and secondary `#9490a8` meeting WCAG 2.1 AA across Dark and Light.      |
| Accent        | Ember `#FF5A36` (configurable accent tokens) for primary actions and active states; color is never sole cue. |
| Layout        | Stable left navigation, central workspace and optional right strategy/detail panel.                          |
| Numbers       | Tabular quantities, visible asset units and consistent rounding; SOL principal, BTC and SAT remain separate. |
| States        | Available, committed, settling, pending conversion, claimable, claimed and recovery required.                |
| Accessibility | Visible keyboard focus, labeled controls, readable contrast and reduced-motion support.                      |

## Satcoin Mining workspace

Lead with the BTC chart and range strip. Keep the round timer and commitment state beside them. The allocation ticket shows selected ranges, SOL sizes, total exposure and conditional payouts before commitment. Results and the activity ledger follow below; a strategy/chat panel is optional. On narrow screens, keep the range selector, amount and final review usable without a fixed desktop grid.

Use **Mine → Trade SAT → Stake → Daily competition → Portfolio/social strategies** in WEN. Inside Fased, the same mining workspace sits in **Terminals → WEN**; account navigation remains Overview, Agents, Terminals, Wallets and Activity.

Shared controls must use the same definitions, not merely matching colors. A conditional reward is not a received balance. An estimated crowd is not revealed live commitments. A connected profile is not a spending mandate. Keep prototype data and disabled financial actions labeled until their actual adapters are accepted.

Avoid physical rigs, random-tile imagery and a casino jackpot as the core mining metaphor. BTC movement, range allocation, capital and accuracy explain the mechanism. Team identities, profiles and daily competition can add personality without obscuring the ledger.
