---
title: "Settings and recovery"
description: "Manage access, appearance and your saved workspace."
---

Use **Settings** to review the connected Local or Remote instance, manage trusted devices, inspect the `/api/v1/mcp` endpoint, and choose appearance, accent color and language. Verify the destination before connecting.

Local uses your existing OS-authorized session. Remote uses your verified Tailscale ownership and private HTTPS without removing existing authentication.

**Settings → Updates** and the installed CLI use one updater on managed Linux hosts. Both show the same operation, progress, result and recovery state. Routine updates reuse existing owner/OS authority without an additional password or SSH session. Manual is the default; Automatic is an explicit saved choice. On standalone desktop and cross-platform CLI builds, **Settings → Updates** and `fased-app updates check` check available desktop releases read-only without automatic installation. See [Install](/guide/install) and [Availability](/reference/status).

The Fased logo opens **Dashboard**. Moving, resizing or removing a widget changes your layout without deleting its Tasks, wallet records or results.

Start/stop and autostart controls manage the app, and `fased-app doctor`, `fased-app backup`, and `fased-app restore` provide diagnostics and online SQLite backups from the CLI. Removal preserves data. Keep a working private administrator and independent provider recovery route. App access, OS administration and wallet recovery are separate; software updating never grants wallet authority.
