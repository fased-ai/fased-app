# Fased App

A focused financial automation workspace. **P2 private-core source foundation**: Go serves the production React interface, owns setup in SQLite, authenticates the instance owner, and exposes the same setup to the CLI. Native installation and desktop packaging are still unfinished. Wallet signing, financial execution, AI connections and updates are later phases.

The interface includes shared navigation, resumable setup, wallet-mode and permission previews, agent profiles, scripted chat/action cards, scoped draft memory, a notification inbox, and four labelled terminal mockups. WEN Asset Mining and Loss Mining remain prototypes while their independent contracts are developed. Provider/wallet selections save preferences only; they create no account or authority.

## Develop

Requires Go 1.26 and Node.js 22.12+ for development. The compiled core needs neither Node nor a database server.

```sh
npm ci
npm run build
npm run build:core
```

For a disposable development instance:

```sh
./.evidence/bin/fased-app serve --data-dir .evidence/manual-core
```

Open the printed loopback URL. In another terminal, obtain the one-use owner code:

```sh
./.evidence/bin/fased-app claim-code --data-dir .evidence/manual-core
```

Enter it in the UI and create an owner password of at least 12 characters. The code expires after 15 minutes; obtaining a new code invalidates the previous one. `claim` offers masked owner claim in a terminal instead. Browser setup and the CLI below use the same versioned state:

```sh
./.evidence/bin/fased-app login --data-dir .evidence/manual-core
./.evidence/bin/fased-app setup --data-dir .evidence/manual-core --interactive
./.evidence/bin/fased-app status --data-dir .evidence/manual-core
./.evidence/bin/fased-app logout --data-dir .evidence/manual-core
```

The wizard reviews preferences before saving. Advanced clients can use `setup --host local --wallet-mode watch --provider none`. Passwords are masked, never command arguments. Explicit `login --password-stdin` is available for controlled automation. A second engine using the same state directory fails rather than starting another instance. Closing the browser leaves the foreground core running; Ctrl-C stops it and preserves its state.

To work on the fixture interface without the Go core:

```sh
npm run dev
```

Only this development server can fall back to fixture mode when the API is absent. The production interface fails closed. These are source development commands, not the future retail installer.

```sh
npm run check
npm run test:core
npm run build:core
npm run test:ui
npm run test:core-ui
```

Browser tests require Playwright's Chromium (`npx playwright install chromium`). Core browser tests launch disposable instances and exercise UI/CLI edits, restart, logout and API failure on desktop/mobile. Screenshots, results and traces go to ignored `.evidence/`. Cross-compilation checks are source evidence, not installed OS acceptance.

## Private access and state

The HTTP listener accepts explicit loopback IPs only (default `127.0.0.1:7331`). CLI traffic uses a Unix socket or Windows named pipe protected for the instance's OS user, and verifies instance/API identity before sending credentials. Browser mutations require an allowed origin and authenticated CSRF token; foreign hosts/origins, unsupported fields and oversized bodies are rejected. Concurrent stale setup writes return 409 and require review.

Local HTTP uses an origin-scoped bearer in tab session storage, never local storage or an HTTP cookie shared between ports. An explicitly configured private HTTPS origin uses a Secure, HttpOnly, SameSite=Strict cookie. Sessions expire after 12 hours and logout revokes them. Passwords use Argon2id; claim/session tokens are stored hashed in SQLite. This is app access authentication, not the future wallet-administration ceremony.

For a VPS, retain the loopback listener and use private access. An owner-established SSH tunnel can forward it; a trusted local HTTPS proxy such as Tailscale Serve can forward it with `serve --public-origin https://<exact-private-host>`. No proxy is installed automatically yet. This option only allowlists that exact origin; it does not establish private networking, validate tailnet policy or make public exposure safe. The core changes no SSH, firewall or Tailscale settings.

State defaults to the OS user's configuration directory under `fased-app`; `--data-dir` selects another private directory. Unix directories must be owner-only (0700), files owner-only (0600); broad permissions and final-path symlinks are rejected. Windows uses a protected current-user/SYSTEM DACL and named pipe. SQLite owns durable ownership/settings/sessions; schema downgrade is refused. Endpoint and CLI-session files are atomically replaced. No password reset or financial backup/restore workflow is implemented yet. Preserve state; do not delete it as an upgrade method.

## Data and scope

In core mode, setup lives in SQLite. Custom profiles, draft notes and notifications remain browser demo data; chats are temporary and scripted. Settings → Reset prototype clears those demo preferences and preserves core setup. Do not enter secrets or real financial information in demo pages. The application makes no provider, RPC or financial requests.

Next P2 work: thin Wails client, package/service identities, guided private-access setup, clean supported-host install/uninstall and installed platform acceptance. The Go wallet signer, its independent policy/recovery storage, and a restricted update helper follow in P3/P4. The signer status API currently reports unavailable and the health API explicitly disables financial/provider/update capabilities.

The controlling product/implementation/checkpoint documents are maintained in the separate `fased-ai/docs` repository under `fased/` (local workspace: `../docs/fased/`). Source commands and boundaries live in [AGENTS.md](AGENTS.md). The old Fased and network server are independent repositories, not runtime dependencies.

## License

Original Fased App code is under [PolyForm Perimeter 1.0.0](LICENSE). It is source-available with restrictions on providing competing products/services; it is not described as permissive or OSI open source. See [NOTICE](NOTICE) for attribution and dependency boundaries. License selection does not change third-party terms.
