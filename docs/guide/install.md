---
title: "Install"
description: "Ubuntu first, with a single engine and private access."
---

Ubuntu 24.04 x86-64 is the first engineering target. The compiled application does not need a Node runtime, database server, provider, wallet, RPC or Network account to start.

Trusted public distribution is not yet accepted. Use only a verified engineering bundle supplied through the project's reviewed workflow; this page provides no public curl installer or security-warning bypass.

From a verified extracted bundle, the terminal entry is:

```sh
./fased-app install --bundle . --prepare-host
```

The interactive setup guides host preparation, Local or Remote installation, owner setup and opening the interface. On Ubuntu, narrowly scoped host preparation may request administrator authorization for trusted OS tools and the restrictive sandbox profile. Fased itself does not run as root.

- **Local:** an instance on your computer, reached through loopback.
- **Remote:** an instance on your server, reached through private SSH/Tailscale access.
- **Connect:** select and verify an existing instance rather than create another one.

Remote setup keeps the administrator separate from the application identity. Review network hardening before applying it and retain recovery access. Tailscale enrollment and a real VPS/private-HTTPS retail journey remain separate acceptance gates. Cloudflare integration is not delivered.

Choose a password in the trusted setup flow. Never paste owner passwords, provider keys or wallet recovery material into Chat. See [Settings and recovery](/guide/settings), [Wallets](/guide/wallets) and [Status](/reference/status).
