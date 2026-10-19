# Fased App

A new, focused financial automation workspace. **P1 interactive interface prototype**: no wallet keys, financial execution, real AI connections, installation service, or updater is implemented yet.

The prototype includes shared navigation, a resumable setup simulation, wallet-mode and permission previews, agent profiles, scripted chat/action cards, scoped draft memory, a notification inbox, and four explicitly labelled terminal mockups. WEN Asset Mining and Loss Mining remain prototypes while their independent contracts are developed.

## Develop

Requires Node.js 22.12+ and npm for development only:

```sh
npm ci
npm run dev
```

Open the loopback URL printed by Vite. Do not expose the development server publicly. This is not an end-user installer or a hosted service.

```sh
npm run check
npm run test:ui
```

Browser tests require Playwright's Chromium (`npx playwright install chromium`). Screenshots, test results and failure traces go to ignored `.evidence/`.

## Data and scope

Prototype preferences, custom profiles, draft notes and notifications use this browser's local storage. Chat messages are temporary and scripted. Use Settings → Reset prototype to clear demo data. Do not enter secrets or real financial information. The prototype makes no provider, RPC or financial requests.

Future architecture: Go application + isolated Go wallet service, shared React UI, thin Wails desktop client, CLI, and a restricted update helper. The current source tree contains only the modules needed for P1.

The controlling product/implementation/checkpoint documents are maintained in the separate `fased-ai/docs` repository under `fased/` (local workspace: `../docs/fased/`). Source commands and boundaries live in [AGENTS.md](AGENTS.md). The old Fased and network server are independent repositories, not runtime dependencies.

## License

Original Fased App code is under [PolyForm Perimeter 1.0.0](LICENSE). It is source-available with restrictions on providing competing products/services; it is not described as permissive or OSI open source. See [NOTICE](NOTICE) for attribution and dependency boundaries. License selection does not change third-party terms.
