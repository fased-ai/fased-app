# Install Fased

Fased runs on your computer or Ubuntu VPS. The installer downloads and verifies a compiled package, installs the required system packages and starts the setup wizard. You do not need Go, Node or a source checkout.

## Ubuntu

Run this command in an interactive terminal:

```sh
bash <(curl -fsSL https://github.com/fased-ai/fased-app/releases/latest/download/install.sh)
```

Choose **Local** for your computer or **Remote** for a VPS. An SSH terminal suggests Remote. The currently verified Ubuntu package targets **24.04 LTS, amd64**; the installer checks platform compatibility before installation.

## Local

The wizard installs Fased for your ordinary account, prepares its isolated service and opens the browser. Create your Fased password to claim the workspace. Reopening an existing workspace uses its existing account and data.

Installation needs no AI provider, wallet or RPC. Connect those later for the activity you choose.

## Remote

1. SSH to your VPS and run the installer above.
2. Choose Remote, then **Tailscale** or **SSH** for private access.
3. Complete the selected access setup and open the printed private browser link.
4. Create your Fased password in the browser.

A root SSH session prepares an ordinary operator and a separate locked application account. Fased runs without root or sudo access. The installed root-safe `fased` command delegates to the recorded operator.

### Tailscale

The wizard guides installation and enrollment on the VPS and configures private HTTPS access. Enroll the computer or phone you will use in the same tailnet, then open the private address. Tailscale is a separate OS application. Fased still requires its owner password.

A conflicting VPN can prevent access. Pause it or configure compatible routing; the SSH access option remains available.

### SSH

The wizard prints the port-forward command for your accessing computer and the browser link to open afterward. Fased listens on loopback; you do not need to expose its application port publicly. Keep your normal SSH access for recovery.

## Open and manage

The installed service can start after reboot. Closing the browser leaves that service running; provider access still follows its credential and task permissions.

```sh
fased open
fased status
fased stop
fased start
```

The browser UI and CLI use the same instance and stored records.

## Update

Open **Settings → Updates**, or run:

```sh
fased update
```

The updater verifies the release, preserves your data and authentication, restarts Fased and reports the result. A repeat reports **Already current**.

Choose **Automatic** in Settings → Updates to opt in. It requires the installed update controller and follows your maintenance window and version preferences. Manual remains the default. Unresolved recovery or incompatible state stops an update for inspection.

For an earlier installation that needs a compatibility upgrade, run the official installer with `--update` as the same installation user. The wizard upgrades that instance. Ordinary updates then continue through the UI or installed command. See [Updates](updates.md) for the full user flow.

## Remove

```sh
fased uninstall
```

Removal preserves instance data and recovery records. Reinstall as the same installation user to reconnect to them.

## Distribution

Native installers and additional platform acceptance are tracked separately. Use the package supported for your platform; a portable Go executable alone does not establish OS dependency compatibility.

The [release contract](maintainers/ubuntu-release.md#one-candidate-one-acceptance-flow) describes package verification and acceptance. [Recovery](maintainers/ubuntu-release.md#recovery-and-tag-discipline) describes retained release transactions.
