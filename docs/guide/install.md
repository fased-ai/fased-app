---
title: "Install"
description: "Local setup and private Remote access."
---

**Installation and updates are under revision. The required experience below is not complete in current evaluation builds.** See [Availability](/reference/status).

## Choose Local or Remote

| Choice  | Where Fased runs         | Your access                                                        |
| ------- | ------------------------ | ------------------------------------------------------------------ |
| Local   | Your computer            | Your existing OS account and a protected local app/browser session |
| Remote  | Your dedicated VPS       | Your verified Tailscale owner identity and private HTTPS           |
| Connect | An existing installation | The same instance, ownership and saved records                     |

Installation needs no Go compiler, Node, Git, AI provider, wallet or RPC.

## Local

Run the official installer or supported native package. The wizard prepares required components and opens Fased for your existing OS user. Local needs no Tailscale, SSH tunnel, new Linux user or separate Fased password by default. Your OS may request its normal administrator approval during initial system setup.

The local listener stays on loopback. The browser session is authorized by the local launcher; being able to reach a localhost port alone does not grant ownership. Background work follows the selected OS service/autostart setting; a sleeping or powered-off computer cannot run it.

## Remote — Tailscale required

1. Connect to the VPS once through SSH or its provider console and start the official installer.
2. The wizard prepares Tailscale and guides its official sign-in. Enroll your accessing computer too. It verifies the intended private network and owner access.
3. **Fased deployment cannot proceed until Tailscale is ready.** Declining enrollment or failing verification leaves setup incomplete. There is no SSH-only installation fallback.
4. The wizard installs verified Fased files and isolated services, then configures private HTTPS. Open its private address using your verified Tailscale owner identity and the trusted device bound by the private setup link. New devices require owner approval; no extra Fased password or copied setup code is part of this target flow.
5. Before closing public access, verify private access, a private administrator/recovery route and an independent provider console or rescue path. Setup stages the network change with automatic rollback if confirmation fails.
6. After verification, setup closes public SSH/web/application access, saves the firewall policy and finishes. Close the bootstrap connection. Fased starts at reboot and works without an SSH session, forwarding tunnel or open browser.

Approve only devices you control. Remote access trusts the approved device, not a separate browser password. In **Settings → Devices**, use **Add device** to create a one-use link for another device on the same Tailscale account; **Remove** revokes that device and its saved sessions.

Tailscale is separate OS software on the VPS and your accessing device. Fased guides enrollment but cannot silently sign in another device. Other VPNs can interfere with setup; resolve conflicting routing before accepting private access. See [Tailscale setup](https://tailscale.com/docs/how-to/quickstart).

## Accounts and ports

Local reuses your existing OS user. Remote reuses an existing administrator when possible. A root-only VPS needs a verified key-based recovery administrator before root SSH is disabled. The installer uses only the approved public key; your private key stays on your computer. No new Linux password is required: the approved SSH key authenticates the recovery administrator, and password login stays disabled. Blank-password login is forbidden. An existing administrator's credentials are preserved.

Local runs the app as your ordinary OS user inside its supported sandbox/service. Remote uses a dedicated locked app account. The signer has a separate protected identity when wallet setup is selected. Service identities are not customer logins; you never manage their passwords. Neither app nor signer gets general sudo access. Root is confined to initial OS setup and restricted maintenance/recovery operations.

After replacement access is proved, root/password SSH is disabled and public SSH, web and raw Fased/RPC access is closed on IPv4 and IPv6. Private HTTPS is available only through Tailscale. Private key-only administrator SSH may remain for recovery; ordinary Fased usage and updates never depend on it. Essential network traffic and outbound provider/RPC/download access continue to work.

## One updater, UI or CLI

Use **Settings → Updates → Update**, or the installed command:

```sh
fased update
```

Both are equal interfaces to the same authorization rules, release checks, controller, progress, result and recovery transaction. A CLI client connected to Remote uses private Tailscale HTTPS directly; it does not require SSH or another running engine. Start in one interface and inspect the same operation in the other. Routine Update reuses your existing owner/OS authority and does not ask for an app, root, sudo, wallet or signing password. It never unlocks a wallet or expands spending permissions.

The updater preserves data and authentication, restarts Fased and reports the result. Repeating it reports **Already current**. Interrupted work is recovered through the same operation; it is not silently retried as a new installation.

**Manual** is the default. **Automatic** is an explicit choice using that same updater and your saved policy. It needs no open browser or SSH session. New system permissions require a clearly identified setup review rather than an ambiguous password prompt.

## Recovery and availability

Keep a provider console or rescue route that works without Tailscale. Private SSH through Tailscale is useful for administration, but cannot replace that independent route. If your host offers no console/rescue, this Remote setup cannot safely finish its public-access lockdown. Use a host with recovery; do not remove your last working access path.

A temporary Tailscale coordination outage may leave established connections working, but new connections and changes can fail. Do not rely on that for a permanent outage or account loss. Setup failure rolls back the staged network change; a later outage never automatically reopens public ports, resets ownership or unlocks wallets. [Tailscale availability](https://tailscale.com/docs/concepts/control-data-planes).

Wallet recovery must not depend on the VPS or Tailscale account. Before using real funds, keep a protected recovery copy outside the VPS and verify that it restores on another accepted installation. Wallet recovery remains under acceptance; it is not enabled by this guide.

Existing records, credentials and recovery rights must survive upgrades and removal. Do not overwrite an installation to repair its updater. Wallet recovery is separate from app access and software updates.

Ubuntu 24.04 x86-64 is the first evaluation target. The revised setup, owner authentication and complete update flow remain under implementation. Other OS releases, architectures and native installers require their own acceptance. See [Availability](/reference/status) and [Settings](/guide/settings).
