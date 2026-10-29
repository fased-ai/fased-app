# Installation and private access

These are **engineering package** instructions. No signed public release or
trusted public download command exists yet. Do not bypass Gatekeeper, SmartScreen
or signature warnings to make a development build appear production-ready. The compiled app
needs no Git, Go, npm, database server, RPC, AI or wallet account at runtime.

The [disposable Go signer](signer.md#wallets-setup) requires explicit Ubuntu
engineering enrollment or a standalone managed installation. Ordinary packages do
not provision wallet keys. Wallets setup uses protected owner Create/Import, public
account records and encrypted RPC settings; real credentials/dispatch stay disabled.
Privileged same-instance, physical-host/native custody and later
permission/reconciliation acceptance gates stay open.

## Ubuntu prerequisites

Ubuntu 24.04 LTS, x86-64 is the first Linux/VPS engineering target. Run the app
as an ordinary OS user. A VPS administrator must provide the ordinary administrative account. The
per-user service path needs working systemd user services and, for execution
after logout/reboot, user lingering.
The ordinary per-user Remote path checks this prerequisite. The explicit managed
Remote path below instead creates a locked application account and system service;
it does not require that service account to log in or have user lingering.
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

## Install an acquired engineering bundle

Extract an acquired package into its own directory. On Linux, from the extracted
directory, run:

```sh
./fased-app install --bundle . --interactive
```

The terminal installer offers **Local** (install on this computer), **Remote** (install on this server) and **Connect** (open an existing instance). Connect verifies the selected Fased API/instance and opens its browser UI; it never installs or starts a local engine. Local asks whether to start at login, off by default. Remote offers a dedicated locked app account, then asks whether to keep running after logout/reboot. The dedicated option uses a system service; the older current-user option still requires Linux user-service/linger readiness. Remote retains the `--vps` CLI alias and requires explicit SSH or Tailscale access. Tailscale enrollment, HTTPS and operator permissions remain owner prerequisites; no tailnet, SSH or firewall policy is reset.

For Local or Remote installation, the wizard verifies the package's platform,
version and complete file inventory before host preparation.
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
prints the start command and, for Tailscale, the subsequent access-enable command.
Run those commands first. For SSH, obtain the forwarding steps with
`fased-app access plan --mode ssh`, repeating the installation's `--listen` value
if customized, and keep the tunnel open. Then run
`fased-app claim-code` on the installed host with the exact `--data-dir`, open
the selected local/tailnet URL on your computer and claim the instance or sign
in. A deferred installation has no ready private UI until these steps succeed.

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
The disposable signer requires explicit protected installation or same-instance
Ubuntu enrollment (`custody enroll`, documented in [signer setup](signer.md#wallets-setup));
ordinary install does not provision wallet keys. Enrollment preserves the original
instance and data, and its privileged acceptance remains open. The standalone
managed fixture retains protected owner administration; ordinary installation
does not enable wallet authority.

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

## Dedicated Ubuntu Remote installation

The explicit managed Remote option installs the existing Go core under a locked
application OS account with no interactive login or sudo. The ordinary administrator
keeps installation/maintenance authority; application owner sign-in remains separate.
It adds no signer, wallet, provider account or live capability. Its privileged
host acceptance remains pending disposable Ubuntu CI and a real VPS; this is an
engineering path, not a claim of accepted public distribution.

Use a verified **headless** Ubuntu bundle as the ordinary administrator:

```sh
./fased-app install --bundle . --managed-remote --autostart --prepare-host --access tailscale
```

For SSH forwarding, choose `--access ssh`. Interactive Remote installation offers
the same dedicated-account choice. OS authorization covers the locked runtime
identity, root-owned verified application files, sandbox prerequisites and system
service. Fased code never runs as host root. Existing unrelated accounts, files,
services and configuration are not adopted or overwritten. The installation and
state directories are fixed per administrator; custom `--prefix`/`--data-dir` and
desktop payloads are rejected for this option. The ordinary Local path is unchanged.

Tailscale must already be installed and owner-enrolled. The administrator needs
Serve permission; the application account does not receive it. Private Serve
configuration/recovery belongs to the administrator's private control directory.
App data belongs to the application account, and the browser still requires its
owner claim/password. The wizard prints the installed management command:

```sh
fased-app vps status
fased-app vps claim-code
fased-app vps stop
fased-app vps start
fased-app vps recover
fased-app vps access
fased-app vps remove
```

Use the full installed path printed by the wizard. `recover` needs a stopped core
and an owner terminal; the credential-switching OS tool drops privileges before
Fased reads the new password. `remove` unregisters the owned system service and removes its private endpoint
configuration. It retains application data, the immutable recovery/management
helper, the locked identity and recovery metadata. `--start=false` stages the installation;
`vps start` is the later readiness/private-access handoff. SSH forwarding still
needs the printed tunnel on the owner's computer.

### Secure a dedicated VPS

Installation keeps existing SSH and firewall policy. Lockdown is a separate
administrator-terminal action, after private sign-in and provider-console recovery
both work. It currently supports OpenSSH on port 22 over Tailscale on Ubuntu
24.04. The OS `nftables` package must be installed; Fased refuses to alter an
unsupported or unprepared host. A root-only login or an SSH tunnel alone cannot
approve lockdown.

The supported SSH profile uses Ubuntu's vendor service, empty `SSHD_OPTS` and the
standard `sshd_config.d` include. Custom daemon options, unit overrides, `Match`
blocks and additional includes are preserved and refused.

Keep your current connection open. Open SSH to the VPS's **Tailscale address** as
your ordinary administrator, then use the installed path:

```sh
fased-app vps harden
```

The command checks the Tailscale device/interface, exact private Serve route (no
Funnel), the running instance identity and owner setup. Confirm that you have
actually signed in through private HTTPS and tested the provider console. `sudo`
then authorizes fixed OS tools; Fased itself remains unprivileged.

The pending policy closes public inbound connections, preserves existing
connections and outbound traffic, and allows private SSH/HTTPS, Tailscale UDP,
ICMP and DHCP replies. Root, password and keyboard-interactive SSH login are
disabled. Fased adds its own firewall table and SSH drop-in; it never resets the
host firewall or rewrites other SSH configuration.

A five-minute rollback is armed **before** applying changes. Keep that connection
open and establish another SSH TCP connection through Tailscale. Disable SSH
multiplexing for that connection (`-o ControlMaster=no -o ControlPath=none`).
Verify private sign-in, then run the exact `vps confirm ATTEMPT_ID` command printed
by Fased. Another shell, tmux or a multiplexed channel on the original connection
does not count. Confirmation commits only the reviewed attempt; otherwise the OS
rollback removes its unchanged policy. A pending reboot also rolls back. A
committed policy persists across reboot. SSH startup depends on successful firewall
restoration; a failed firewall job keeps SSH closed until console recovery.

```sh
fased-app vps network
fased-app vps rollback
```

Rollback remains an administrator-terminal recovery action even if the core is
stopped. It preserves foreign changes; if another administrator changes an owned
file/table during the pending window, status reports that recovery requires
inspection rather than deleting that change. Keep the provider console available.
Removing the app does not silently reopen public SSH or delete retained network
policy, identities or data. Real tailnet/second-device/VPS acceptance remains
separate from disposable Ubuntu checks.

## Connect to an existing instance

For an existing instance:

```sh
fased-app connect --url https://your-instance.ts.net
```

Use `--print` to verify/print without opening a browser, and optional `--instance-id ID` to require an already known identity. Native clients accept `fased-desktop --connect URL`; choosing **No** in first-launch Local installation also leaves the Remote connection screen available without creating an engine. Remote opens in the system browser. The native connection operation is a fixed, origin-checked verify/open action, not a network credential proxy or a remote service installer.

HTTPS is required for network destinations. HTTP is admitted only for explicit loopback/localhost SSH-forwarding addresses. Credentials, paths, query strings, fragments, redirects, incompatible APIs/identities, oversized responses and untrusted TLS fail before connection. CLI/native opening carries the verified public instance ID to the destination UI; the UI checks health before showing owner setup/sign-in or sending a password. Public health discovery omits credentials. Browser-to-browser connection opens the selected origin in a separate tab with no opener or copied session; its UI verifies its own runtime before sign-in. No local credential, conversation or setup preference is forwarded.

Tailscale setup uses the same readiness check as Connect: the private URL must reach the healthy, compatible instance with its Local/Remote descriptor. An incompatible response leaves setup pending; retry verifies the recorded route without replacing it or touching instance data.

The connection row follows the runtime profile and access transport, with the public instance ID under Instance details. Private HTTPS is displayed as Remote; a server installed as Remote also reports Remote through an SSH loopback connection. The stored deployment preference remains compatible metadata and is inspectable only in Diagnostics. Changing it does not move the runtime or change connection state. Existing owner data and authentication are reused across same-instance restart; connecting elsewhere leaves them on the original instance.

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
The ordinary installer does not replace a different binary in place. The
separate [controlled update flow](updates.md) applies only to its explicitly
managed disposable installations; it is not an ordinary/public updater.

If the app password is lost, use the existing OS-owner SSH/terminal:

```sh
fased-app service stop
fased-app recover
fased-app service start
```

Recovery masks and confirms the new password, requires a stopped engine, revokes
all app sessions, and preserves instance identity/settings. It is not wallet-key
recovery and is not exposed through the app HTTP API. For off-machine backup of
ordinary instance state, stop the engine and copy the entire private state directory
using an owner-controlled encrypted backup tool. Preserve its permissions; do
not copy an active SQLite file alone. Disposable signer backup/recovery uses its
separate [owner procedure](signer.md); ordinary instance backup is not wallet
recovery.

`fased-app uninstall` stops/unregisters only its verified background service and
removes only inventoried application files. It keeps **all** instance data and
unknown files. Interrupted file removal has a durable receipt and can resume
without deleting data. A changed identity requires inspection, never a forced
delete. Signing/notarization, trusted public acquisition and real supported-host
acceptance remain release gates before retail use. Implemented prerequisite
handling is engineering functionality, not proof of those host journeys.

## Development bootstrap reference

The separate `fased-install` verifier implements development first-install profiles
and the inactive production-admission profile described below. Build it from the reviewed checkout with `npm run build:installer`
or `go build -trimpath -o fased-install ./cmd/fased-install`. It runs without Node,
Git or a Go toolchain. Authenticate this verifier and its public seed independently
before using them; the downloaded app does not authenticate its own first launch.
There is no public installer URL, production seed or signing identity configured.
The following development command is for controlled acceptance, not retail distribution.

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
or automatic retry. It uses the same installation, private-access and owner setup
described above. Interrupted downloads never resume as trusted input: preserve
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

### Production first-install admission (not activated)

A distinct version-3 stable seed/profile now verifies the production release unit
through the same independent bootstrap. It requires threshold-signed descriptor,
archive and offline Sigstore provenance, exact pinned publisher/build identity and
schema-2 headless Ubuntu bundle integrity. The provenance check precedes download
output and is repeated with retained original evidence before explicit installation.
This is not a signer installation, an update permit or an alternate wallet flow.

Version-1/2 development profiles retain their restrictions and cannot consume the
new release-bundle format. The ordinary CLI exposes no test transport or verifier
factory; public GitHub activation remains compiled off. No production seed, trust
root or endpoint is supplied automatically, and native signing/real VPS acceptance
is not implied. See [the frozen policy](updates.md#production-ubuntu-first-install-policy-v1).

The package producer supports explicit `--ubuntu-release-format` for this schema.
It requires Linux amd64, a canonical source commit and no desktop payload before
creating output. The resulting receipt still has `publicRelease=false`: producing
the format does not authenticate or publish it. The current engineering version
cannot pass stable-release admission.
