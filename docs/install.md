# Installation and private access

These are **engineering package** instructions. No signed public release or
download command exists yet. Do not bypass Gatekeeper, SmartScreen or signature
warnings to make a development build appear production-ready. The compiled app
needs no Git, Go, npm, database server, RPC, AI or wallet account at runtime.

## First supported target

Ubuntu 24.04 LTS, x86-64 is the first Linux/VPS engineering target. Run the app
as an ordinary OS user. A VPS administrator must provide that account, working
systemd user services and, for execution after logout/reboot, user lingering.
Fased checks this prerequisite; it does not change accounts or login policy.
VPS sizing and a provider-specific public installation are still acceptance
work, not a measured resource guarantee.

The headless core has no WebView dependency. Linux desktop installation checks
its GTK3/WebKitGTK 4.1 runtime before starting. On Ubuntu 24.04, explicit
`install --install-runtime` authorizes the OS package manager to install missing
runtime libraries; sudo handles its own terminal prompt and Fased stays
unprivileged. Other distributions require their own OS packages. Windows desktop
builds select Wails' pinned embedded WebView2 bootstrapper and ask before its
installation. macOS uses OS frameworks. Missing-runtime installation needs its
own fresh-host acceptance; a CI host with prerequisites already present does not
prove that branch. Package publisher trust remains a separate release gate.

## Install

Extract an acquired package into its own directory. On Linux, from the extracted
directory, run:

```sh
./fased-app install --bundle . --interactive
```

The wizard chooses local/VPS, private access and login autostart. It verifies the
package's platform and file inventory before installation. This detects mixed
or corrupt package files; a checksum in the package is **not** independent proof
of who published it. Public bootstrap trust is a separate release requirement.

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
folder. Its first launch asks before installing/starting the private core as a
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
