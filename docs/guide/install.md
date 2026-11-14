---
title: "Install"
description: "Run Fased locally or connect to a private server."
---

Public Fased downloads are not released. Ubuntu 24.04 x86-64 is the initial target. If you are a project tester, use only a verified package provided for that purpose; development installations are for test data.

## Choose where to run Fased

| Choice  | Use it for                                                              |
| ------- | ----------------------------------------------------------------------- |
| Local   | Run Fased on your own computer.                                         |
| Remote  | Run Fased on your private server and reach it through SSH or Tailscale. |
| Connect | Open an existing Fased instance.                                        |

## Open the setup wizard

From the verified, extracted package, run as an ordinary user:

```sh
./fased-app install --bundle . --interactive
```

The wizard guides setup and opening the interface. It may ask for administrator approval to install required system components. The app runs without root privileges.

Choose a password in the setup screen. For an existing instance, sign in with your existing password. Keep the address and recovery instructions printed by the wizard.

## Private Remote access

Set up your server and retain its recovery-console access. Choose SSH forwarding or enroll your computer and server in Tailscale. The Fased management interface should be reached privately rather than exposed to the public internet.

Cloudflare access and signed macOS/Windows distribution are not available. See [Availability](/reference/status) and [Settings and recovery](/guide/settings).
