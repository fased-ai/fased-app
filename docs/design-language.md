# Shared Fased and WEN design direction

Use one recognizable market workspace across Fased and WEN, while retaining each product's navigation and branding. This is a design direction, not a shipped cross-app integration.

The existing Fased App engineering prototype is the visual reference: dark graphite surfaces, restrained lime highlights, compact cards, readable tables and clear activity state. Keep Fcode Lab as the umbrella; give Satcoin Mining the same round terms and financial labels wherever it appears.

| Element       | Shared direction                                                                                             |
| ------------- | ------------------------------------------------------------------------------------------------------------ |
| Surface       | Graphite background `#111416`, panels `#191d1f`, borders `#2b3032`.                                          |
| Text          | Light primary text `#e8eae5`; muted `#929b9e` only where contrast remains readable.                          |
| Accent        | Lime `#c3ed83` for the primary action and selected range; color is never the only status cue.                |
| Layout        | Stable left navigation, central workspace and optional right strategy/detail panel.                          |
| Numbers       | Tabular quantities, visible asset units and consistent rounding; SOL principal, BTC and SAT remain separate. |
| States        | Available, committed, settling, pending conversion, claimable, claimed and recovery required.                |
| Accessibility | Visible keyboard focus, labeled controls, readable contrast and reduced-motion support.                      |

## Satcoin Mining workspace

Lead with the BTC chart and range strip. Keep the round timer and commitment state beside them. The allocation ticket shows selected ranges, SOL sizes, total exposure and conditional payouts before commitment. Results and the activity ledger follow below; a strategy/chat panel is optional. On narrow screens, keep the range selector, amount and final review usable without a fixed desktop grid.

Use **Mine → Trade SAT → Stake → Daily competition → Portfolio/social strategies** in WEN. Inside Fased, the same mining workspace sits in **Terminals → WEN**; account navigation remains Overview, Agents, Terminals, Wallets and Activity.

Shared controls must use the same definitions, not merely matching colors. A conditional reward is not a received balance. An estimated crowd is not revealed live commitments. A connected profile is not a spending mandate. Keep prototype data and disabled financial actions labeled until their actual adapters are accepted.

Avoid physical rigs, random-tile imagery and a casino jackpot as the core mining metaphor. BTC movement, range allocation, capital and accuracy explain the mechanism. Team identities, profiles and daily competition can add personality without obscuring the ledger.
