# Updates

Fased uses the official [GitHub releases](https://github.com/fased-ai/fased-app/releases). Installation and updates verify the downloaded package automatically and preserve the same workspace, owner account and recovery records.

## Update

Open **Settings → Updates → Check**, review the available version and choose **Update now**. Fased restarts and reconnects to the same instance.

The installed command follows the same update flow:

```sh
fased update
```

A repeat reports **Already current**. The CLI reuses its owner session; if it expires, sign in with your existing Fased password inside the command.

## Automatic updates

Choose **Automatic** in Settings → Updates. The installed service checks for eligible releases and applies them within your selected maintenance window. You can return to **Manual** at any time.

The service must be running and its update maintenance authority configured. Closing the browser does not stop that service. An active operation, incompatible package or unresolved recovery state can hold an update. Updating preserves credential locks and existing approvals.

## Earlier installations

If the installed updater needs a compatibility upgrade, use the official installer:

```sh
bash <(curl -fsSL https://github.com/fased-ai/fased-app/releases/latest/download/install.sh) --update
```

The wizard selects Local or Remote and upgrades the existing installation. Use the same Linux installation user. Subsequent updates use Settings or `fased update`.

## Recovery

Open Settings → Updates to inspect recovery, or run:

```sh
fased update recover
```

Fased checks the retained transaction before continuing. Keep the installation and recovery records; an uncertain result must be resolved before another update starts.

## Verification

The installer verifies the official repository, build attestation and exact downloaded bytes. Release verification runs automatically. An available release must pass its packaged installation/update checks before publication.

The [maintainer release guide](maintainers/ubuntu-release.md#one-candidate-one-acceptance-flow) describes distribution and verification. [Recovery details](maintainers/ubuntu-release.md#recovery-and-tag-discipline) cover retained release transactions.
