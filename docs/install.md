# Installation and private access

These are **engineering package** instructions. No signed public release or
download command exists yet. Do not bypass Gatekeeper, SmartScreen or signature
warnings to make a development build appear production-ready. The compiled app
needs no Git, Go, npm, database server, RPC, AI or wallet account at runtime.

P3's [managed disposable signer mode](signer.md#explicit-managed-fixture-installation)
is a separate, explicit Ubuntu engineering installation. Ordinary packages do
not enable it. It manages dedicated identities and protected owner setup/recovery
while keeping real wallets disabled. Wallet fields remain preferences/prototypes;
physical-host/native custody and later permission/reconciliation gates stay open.

## First supported target

Ubuntu 24.04 LTS, x86-64 is the first Linux/VPS engineering target. Run the app
as an ordinary OS user. A VPS administrator must provide that account, working
systemd user services and, for execution after logout/reboot, user lingering.
Fased checks this prerequisite; it does not change accounts or login policy.
VPS sizing and a provider-specific public installation are still acceptance
work, not a measured resource guarantee.

The headless core has no WebView dependency. Ubuntu 24.04 requires its OS
`bubblewrap` package for both headless and desktop installations. Its
reviewed restrictive AppArmor profile permits the private filesystem sandbox;
Fased does not disable the system's namespace restrictions. The engine checks
that its application files are actually read-only before opening state or a
listener and refuses to start if protection was skipped. Other Linux builds use
systemd's user-service sandbox with the same enforcement check.
[Ubuntu's Bubblewrap manual](https://manpages.ubuntu.com/manpages/noble/man1/bwrap.1.html)
describes the helper's namespace and read-only mount controls.

Ubuntu 24.04 does not install that extra AppArmor profile by default. The
terminal wizard offers automatic preparation. For an explicit terminal command:

```sh
./fased-app install --bundle . --prepare-host
```

Fased verifies the package and checks the separate policy against a compiled
reviewed digest before requesting sudo. One terminal authorization permits the
fixed OS package operations and restrictive policy setup. No Fased executable or
shell script runs as root; sudo handles its own password prompt. Desktop first
launch opens the system terminal for this preparation when needed, then attaches
to the installed core. A Linux desktop missing GTK/WebKit must start with the
terminal installer so those libraries can be installed before opening a window.

The policy is published without overwriting `/etc/apparmor.d/fased-app-bwrap-v1`.
Its bytes, root ownership and permissions are verified before loading. A private
intent receipt supports interrupted staging/loading; retry uses the same
command. A changed destination or conflicting loaded profile stops preparation
for administrator inspection. Fased never replaces a loaded profile or disables
global AppArmor restrictions. The profile applies to the OS Bubblewrap helper
and therefore also affects its other clients; the wizard's authorization covers
that host change. Its source, GPL license and provenance accompany Linux bundles.
The host prerequisite is deliberately retained on app uninstall because other
instances or Bubblewrap clients may depend on it. It is not wallet state.

Linux desktop installation also checks GTK3/WebKitGTK 4.1. `--prepare-host`
installs missing Ubuntu runtime packages. The existing `--install-runtime` flag
remains package-only and does not authorize policy changes. Other distributions
use their own OS packages. Windows selects Wails' pinned embedded WebView2
bootstrapper with owner confirmation; macOS uses OS frameworks. Signed public
acquisition and actual supported-host acceptance remain separate gates.
[Ubuntu's AppArmor guide](https://ubuntu.com/server/docs/how-to/security/apparmor/)
documents profile operation.

## Choose Local or Remote

The terminal installer offers **Local** (install on this computer), **Remote** (install on this server) and **Connect** (open an existing instance). Connect verifies the selected Fased API/instance and opens its browser UI; it never installs or starts a local engine. Local asks whether to start at login, off by default. Remote asks whether to keep running after logout/reboot, on by default after confirmation; Linux autostart and lingering readiness are still required. Remote retains the `--vps` CLI alias and requires explicit SSH or Tailscale access. Tailscale enrollment, HTTPS and operator permissions remain owner prerequisites; no tailnet, SSH or firewall policy is reset.

For an existing instance:

```sh
fased-app connect --url https://your-instance.ts.net
```

Use `--print` to verify/print without opening a browser, and optional `--instance-id ID` to require an already known identity. Native clients accept `fased-desktop --connect URL`; choosing **No** in first-launch Local installation also leaves the Remote connection screen available without creating an engine. Remote opens in the system browser. The native connection operation is a fixed, origin-checked verify/open action, not a network credential proxy or a remote service installer.

HTTPS is required for network destinations. HTTP is admitted only for explicit loopback/localhost SSH-forwarding addresses. Credentials, paths, query strings, fragments, redirects, incompatible APIs/identities, oversized responses and untrusted TLS fail before connection. CLI/native opening carries the verified public instance ID to the destination UI; the UI checks health before showing owner setup/sign-in or sending a password. Public health discovery omits credentials. Browser-to-browser connection opens the selected origin in a separate tab with no opener or copied session; its UI verifies its own runtime before sign-in. No local credential, conversation or setup preference is forwarded.

Tailscale setup uses the same readiness check as Connect: the private URL must reach the healthy, compatible instance with its Local/Remote descriptor. An incompatible response leaves setup pending; retry verifies the recorded route without replacing it or touching instance data.

The connection row follows the runtime profile and access transport, with the public instance ID under Instance details. Private HTTPS is displayed as Remote; a server installed as Remote also reports Remote through an SSH loopback connection. The stored deployment preference remains compatible metadata and is inspectable only in Diagnostics. Changing it does not move the runtime or change connection state. Existing owner data and authentication are reused across same-instance restart; connecting elsewhere leaves them on the original instance.

## Install

### Independent verification before the bundle runs

The separate `fased-install` verifier now implements an explicit development-only
first-install path. Build it from the reviewed checkout with `npm run build:installer`
or `go build -trimpath -o fased-install ./cmd/fased-install`. It runs without Node,
Git or a Go toolchain. Authenticate this verifier and its public seed independently
before using them; the downloaded app does not authenticate its own first launch.
There is no public installer URL, production seed or signing identity configured.
This development command is for controlled acceptance, not retail distribution.

```sh
./fased-install --development --seed-file /private/seed.json \
  --seed-sha256 "INDEPENDENT_SEED_SHA256" --output /private/new-download
```

Use the actual independently obtained digest in place of the placeholder. The
output must be absent under a real current-user-owned directory with mode0700.
Controlled HTTPS fixtures may supply `--tls-ca-file /private/fixture-ca.pem`;
TLS verification stays enabled. No proxy credentials, cookies, redirects or
downloaded replacement roots are accepted. No app data is opened by verification.

The seed locks the development purpose, repository, channel, Linux amd64 target,
HTTPS origin, initial root and minimum release sequence. The pinned TUF library
checks two distinct signatures from three separate root keys, two from three
different targets keys, separate online roles, expiry and snapshot/target hashes.
The signed descriptor binds the archive, inner manifest, version and source.
Existing update/rehearsal receipts cannot authorize this first-install path.

Downloaded bytes stay non-executable until the complete archive and extracted
inventory pass verification. Extraction rejects unsafe paths/types, duplicate
entries, oversized expansion and trailing data. Verification alone never runs
the app. Add `--install` to explicitly open the existing Local/Remote wizard:

```sh
./fased-install --development --seed-file /private/seed.json \
  --seed-sha256 "INDEPENDENT_SEED_SHA256" --output /private/new-download \
  --install
```

The verifier rechecks expiry, archive/bundle bytes and the held executable before
calling the original installer. Its normal installation/data defaults are resolved
and checked for overlap with the verified download. Optional `--prefix`, `--data-dir`
and `--start=false` are forwarded as fixed arguments; there is no arbitrary command
or automatic retry. Existing installer/private-access/owner-authentication behavior
continues below. Interrupted downloads never resume as trusted input: preserve
unclassified residue and retry into a new absent output directory. The verifier
does not delete application records, enable wallets or install an updater.

The production-capable GitHub transport now has a separate version-2 controlled
seed/descriptor profile and TLS/DNS fixture acceptance. Its policy is pinned in
the seed and release/asset locator authorized by TUF, then the same archive and
installer checks apply. Ordinary GitHub activation stays disabled; CLI/JSON cannot
select a fixture resolver, dialer or test CA for that profile. Version 1 above
retains its no-redirect transport. No production seed/endpoint/binding is created.

Production initial-verifier authentication, publisher/provenance bindings and real
GitHub delivery remain release gates. Disposable TUF keys and controlled HTTPS
acceptance do not establish a trusted public installer or real Ubuntu/VPS/Tailscale
acceptance. The production operations contract remains in
[updates.md](updates.md#production-release-trust-operations-specification).

### Install an already acquired engineering bundle

Extract an acquired package into its own directory. On Linux, from the extracted
directory, run:

```sh
./fased-app install --bundle . --interactive
```

The wizard chooses Local/Remote installation or an existing Remote connection, private access and login autostart. It verifies the
package's platform, version and complete file inventory before host preparation.
The running core and any desktop binary must belong to that package. Ambiguous
JSON, extra files, symlinks and corrupt/mixed files are refused. New packages
include `INSTALL.txt` in the inventory; older engineering packages may have a
regular, bounded, unlisted guide, which is never executed by the installer.

If you obtained the exact `bundle.json` SHA-256 independently, you can pin it:

```sh
./fased-app install --bundle . --bundle-sha256 "INDEPENDENT_BUNDLE_JSON_SHA256" --interactive
```

Use the real 64-character lowercase digest in place of the placeholder. The
development package receipt records `manifestIdentity` for controlled verification.
A digest copied from the same untrusted package is not independent trust. These
checks run inside the already started core; they do **not** authenticate that
initial executable, supply a signed public bootstrap or establish publisher
identity. Public acquisition/signing remains a separate release requirement.

The CLI passes the verified core/desktop hashes to the existing installer, which
checks them before changing installation or instance directories. Each copied
file is verified in staging before publication. Changed input is refused without
publishing a bad target; restoring the same verified bundle permits retry with
the same installation identity and preserved instance data.

When installation finishes, the wizard prints the selected connection steps and
commands using the installed executable and exact state directory; adding Fased
to PATH is not required. SSH mode prints the forwarding command for your chosen
port before directing you to the browser on your computer. Tailscale mode prints
the private tailnet URL. Get the first owner setup code on the installed host;
existing owners sign in with their existing password. Installation, browser and
CLI all use this same engine and state. With `--start=false`, the wizard instead
prints the start command and, for Tailscale, the subsequent access-enable command;
it does not claim that the private UI is ready.

Later examples use `fased-app` as shorthand for that installed executable. Use
the full path printed by the wizard unless you have added its directory to PATH;
retain printed `--prefix` and `--data-dir` values for a custom installation.

For a prepared Linux VPS with SSH access:

```sh
./fased-app install --bundle . --vps --autostart --access ssh
```

For an enrolled Tailscale VPS:

```sh
./fased-app install --bundle . --vps --autostart --access tailscale
```

The core always binds loopback. If login autostart is off, closing a UI still
leaves the engine running for the current OS session. Linux VPS requires linger
and autostart to pass always-on readiness. No automatic SSH/firewall hardening,
Funnel, root runtime, wallet funding or third-party account creation is performed.

On Windows, the unsigned engineering setup executable supplies Start-menu
entries and installs for the current user. A least-privilege Task Scheduler job
owns the background engine, stores no OS password and runs only while that user
is logged in. It is not a Windows system service or an always-on Windows VPS.
The future signer has a separate identity/administration gate.

On macOS, the DMG contains `Fased App.app`; place it in the user's Applications
folder. Its first launch asks before installing/starting the Local instance as a
user LaunchAgent. It does not install a system LaunchDaemon. The app/DMG still
need Developer ID signing and notarization before retail distribution. Unsigned
Windows/macOS packaging tests are engineering evidence only.

Desktop first launch and terminal installation use the same installer/state.
The native claim screen can retrieve this OS user's one-use setup code. Browser
setup requires `fased-app claim-code` in the host owner's terminal; enter it in
the private UI and choose an owner password. Claim codes expire in 15 minutes.
App ownership does not grant wallet authority. Reopening the client reuses the
same background engine. Preferences resume from the instance's SQLite state.

## Private access

Use `fased-app access plan --mode ssh` for an SSH guide. On the owner's computer,
substitute the normal verified SSH destination in the printed forwarding command.
The local port must match the configured core port; choose an unused matching
port at installation if another local app already occupies 7331. Keep the tunnel
open and browse the printed local URL. Preserve normal SSH host-key checking.

For Tailscale, install/authorize the VPS and your computer into your own tailnet,
enable tailnet HTTPS and restrict its identity/device access policy. The owner
must give the ordinary OS account permission to configure Serve. No tailnet auth
key is stored in Fased. [Tailscale's Serve documentation](https://tailscale.com/docs/reference/tailscale-cli/serve)
describes its private HTTPS endpoint.

The Linux helper uses the local daemon's version-sensitive Serve API with
ETag/If-Match checks. It refuses existing or changed Serve/Funnel configuration,
records a recovery intent before changing an empty configuration, and verifies
the private HTTPS endpoint's Fased instance/API identity. Unknown daemon schemas
or missing conditional-write support fail closed. A Tailscale upgrade therefore
requires the focused compatibility check before support is claimed.

If setup is interrupted, keep SSH/provider-console access and rerun the same
install command or `fased-app access enable`. The recorded route is resumed;
unrelated routes are never reset. `fased-app access remove` removes only its
unchanged owned endpoint. Uninstall also removes that endpoint first; it stops
with recovery guidance if ownership/configuration no longer matches. It never
logs out, uninstalls or resets the owner's Tailscale installation.

The owner must confirm the private UI works from another authorized device
before considering SSH/firewall tightening. A local TLS fixture or CI runner is
not proof of a real VPS/tailnet journey. App authentication remains required.

## Recovery and removal

The installed CLI supports `service status`, `service start`, `service stop` and
`open`. For custom paths, keep the same `--prefix` and `--data-dir` arguments.
Read-only binaries are separate from private state. Installation refuses unknown
files, a different application version or a foreign service/shortcut identity.
Updating binaries in place remains unavailable until P4 verification is ready.

If the app password is lost, use the existing OS-owner SSH/terminal:

```sh
fased-app service stop
fased-app recover
fased-app service start
```

Recovery masks and confirms the new password, requires a stopped engine, revokes
all app sessions, and preserves instance identity/settings. It is not wallet-key
recovery and is not exposed through the app HTTP API. For off-machine backup of
the current P2 state, stop the engine and copy the entire private state directory
using an owner-controlled encrypted backup tool. Preserve its permissions; do
not copy an active SQLite file alone. Wallet backup/restore is a later phase.

`fased-app uninstall` stops/unregisters only its verified background service and
removes only inventoried application files. It keeps **all** instance data and
unknown files. Interrupted file removal has a durable receipt and can resume
without deleting data. A changed identity requires inspection, never a forced
delete. Signing/notarization, public acquisition, runtime prerequisite handling
and real supported-host acceptance remain release gates before retail use.
