# Installation and update contract

The [user installation guide](guide/install.md) describes the required Local/Remote experience. The [release acceptance guide](maintainers/ubuntu-release.md#complete-packaged-journey) defines its engineering gates. Current authority/status is `/home/fc/fasedbot/docs/fased/execution.md`.

The revised contract is not fully implemented by published0.1.6. Do not advertise its optional SSH/password/terminal-bridge behavior as the required target, or replace the user's UI-update acceptance with a command they must run.

- **Local first:** actual installed supported public predecessor → protected owner session → UI and CLI through the same updater → preservation/restart/recovery/Already current → bounded Automatic operation. No new human OS account or default Fased password.
- **Remote prerequisite:** OS/Tailscale preparation is allowed first. Verified enrollment and private reachability are mandatory before Fased deployment, database initialization, services or wallet setup. Refusal/failure stops there; no SSH-only fallback or skip flag.
- **Remote completion:** verified owner/private HTTPS plus private administrator and independent console/rescue access → existing timed hardening transaction → public denial/private continuity → persist → close bootstrap SSH. Core and signer are unprivileged; no ongoing tunnel is needed.
- **Accounts:** reuse the Local OS user; Remote reuses or provisions a key-authenticated recovery administrator without an empty password. App/signer accounts are locked, non-login identities. Their users/passwords never become customer forms. Do not disable root SSH until a replacement administrator and recovery route work.
- **Updates:** Settings and installed `fased update` use one controller, release policy, journal, progress/result and recovery. UI uses the existing owner session; Local/host CLI uses authorized OS-owner/admin proof through protected IPC, while a Remote client CLI uses its approved owner/device connection over Tailscale HTTPS; Automatic uses saved scoped software-update authority. No session/cookie copying or generic password/sudo handoff. Initial OS approval or exceptional new privileges are separate setup actions.

Keep published assets immutable and existing data/credentials/recovery holds intact. A terminal repair is not a successful UI upgrade. The old0.1.5 UI compatibility path remains an explicit unresolved implementation predicate. No new release is ready until the corrected Local-first and Tailscale-only journeys pass.
