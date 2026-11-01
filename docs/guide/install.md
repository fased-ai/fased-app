---
title: "Install"
description: "Run Fased locally or connect to a private server."
---

Public Fased downloads are not released. Ubuntu 24.04 x86-64 is the currently verified entry. If you are a project tester, use only a verified package provided for that purpose; development installations are for test data.

## Choose where to run Fased

| Choice  | Use it for                                                              |
| ------- | ----------------------------------------------------------------------- |
| Local   | Run Fased on your own computer.                                         |
| Remote  | Run Fased on your private server and reach it through SSH or Tailscale. |
| Connect | Open an existing Fased instance.                                        |
| Update  | Check and update an existing Local or Remote installation.              |

## One-command installation

The public one-command installer is not released yet. Setup follows this path:

1. Open a terminal on your supported Ubuntu computer, or SSH into your private server. A provider's root SSH session is supported.
2. Run the release installer command from the official Fased installation page. It detects Ubuntu, downloads the pinned verifier and verifies the package before running Fased.
3. The terminal wizard opens automatically. Choose **Local**, **Remote**, **Connect** or **Update**. Remote asks **SSH** or **Tailscale**; approve the required system preparation and keep reboot autostart enabled.
4. Open the private setup link printed by the wizard on your own computer and choose your password. No setup code or second installation command is required.
5. Use Fased in the browser. Remote runs as an isolated service after you close the browser, disconnect SSH or reboot. Local runs in your OS account and can start at login.

Root performs system preparation only. The verifier and terminal wizard run as an ordinary owner. Remote uses a separate locked app account; Local uses your OS account. The installer does not change your SSH login policy or close your existing recovery route. Installation needs no Go, Node, npm, Git, provider, RPC or wallet.

For SSH access, the wizard prints the forwarding command for your existing SSH administrator. Run that command on your own computer before opening the private browser link. The new Fased service account is not an SSH login.

Until public releases are available, project testers must use the separately supplied, controlled package or installer. `./fased-app` alone is not a fresh-server installation command: it exists only after a package has been obtained.

## Installed commands and updates

Use **Open**, **Start**, **Stop** and **Update** from the installed Fased command. The terminal prints the exact command name and browser address. Ordinary installations use `fased`; an existing command with that name is preserved and Fased uses `fased-app` instead.

- `fased open` opens the existing workspace.
- `fased status` checks it; `fased start` and `fased stop` control its service.
- `fased login` signs the CLI in to that same workspace.
- `fased update` checks the trusted release channel and applies one available update. `fased update status` shows progress. If recovery is held, the installation owner can use `fased update recover` from the terminal; it resumes the same recorded operation. The installer menu also offers **Update**.

Updates preserve the workspace and use the same installation controller. **Manual** is the default. **Automatic**, when an accepted release channel is available, is an explicit choice in **Settings → Updates**. Its window and pin apply to unattended work; closing the browser does not stop the installed service. Recovery must finish before another update can begin. Local needs its user service/session; Remote uses its reboot-enabled service. Stopping the core stops new checks; an update already admitted to its maintenance controller may finish or require recovery. Signing out of the browser does not revoke an explicitly saved Automatic policy. Public update activation remains disabled in development builds.

Older installations are upgraded through the same verified installer entry; it may ask you to sign in to the existing workspace. It does not replace an existing workspace with a fresh one.

On Linux, setup prepares a restricted maintenance service that runs as your ordinary user. Update recovery may request OS-administrator approval. Fased itself never runs as root.

**Current availability:** Local and Remote signed fixture updating and recovery have passed on Ubuntu24.04 x86-64. Local verification includes manual and automatic updating and recovery after an interrupted updater. Public downloads and ordinary update activation remain unavailable.

## Platform availability

| Ubuntu        | Local                                  | Remote                                 | Package targets |
| ------------- | -------------------------------------- | -------------------------------------- | --------------- |
| 22.04         | Mapped                                 | Unavailable                            | amd64 / arm64   |
| 24.04         | x86-64 fixture install/update verified | x86-64 fixture install/update verified | amd64 / arm64   |
| 26.04 / 26.10 | Mapped; host verification pending      | Mapped; host verification pending      | amd64 / arm64   |

Mappings select OS dependencies and signed package targets; they are not evidence that every row has passed installation on a real host. An unknown release is refused until its mapping is reviewed. Remote requires the supported sandbox and bounded OS-update permissions. Native macOS/Windows distribution remains under acceptance.

## Private Remote access

With **SSH**, keep the forwarding terminal open while using the browser. Closing that tunnel disconnects your browser from Fased; it does not stop the server application.

With **Tailscale**, a separate Tailscale application runs on both the server and your computer. It is not installed inside Fased. During a new installation, the Ubuntu wizard can guide server package installation and print a browser sign-in link. Install Tailscale on your computer and join the same private network. With the required HTTPS and access permissions, Fased configures a private Tailscale Serve address. Open that HTTPS address and sign in with your Fased password; the Tailscale account and Fased password serve different purposes. See [Tailscale setup](https://tailscale.com/docs/how-to/quickstart) and [private web access](https://tailscale.com/docs/features/tailscale-serve).

Choose private access in the wizard. Administrator setup can change access on an unenrolled workspace while preserving its identity and data, restoring the previous configuration if the change fails. Enrolled signer installations remain held for a separately accepted access migration. Tailscale requires your own enrollment and HTTPS permissions.

Retain SSH and provider-console recovery access until your chosen private connection has been verified. The Fased management interface should stay private.

Cloudflare access and signed macOS/Windows distribution are not available. See [Availability](/reference/status) and [Settings and recovery](/guide/settings).
