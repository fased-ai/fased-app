# Ubuntu 24.04 sandbox profile

This is separately distributed OS policy data, not linked into Fased.
The unmodified `bwrap-userns-restrict` profile is from Ubuntu AppArmor revision
`43ef5b6c962d324ffc58ca75843df26827026bfa`, source path
`profiles/apparmor/profiles/extras/bwrap-userns-restrict`.
[Immutable source](https://git.launchpad.net/ubuntu/+source/apparmor/plain/profiles/apparmor/profiles/extras/bwrap-userns-restrict?id=43ef5b6c962d324ffc58ca75843df26827026bfa).
`ubuntu-copyright` records the upstream GPL-2.0-or-later terms; `COPYING`
contains the license. These terms apply to this policy, independently of
Fased's original PolyForm Perimeter source. The profile grants Bubblewrap
namespace capabilities while denying capabilities to its payload.

Ubuntu's Noble packages do not install this extra profile. Linux engineering
bundles include this policy and its original license/provenance. The installer
checks its compiled SHA-256 before using trusted OS tools to prepare it after
explicit `--prepare-host` or wizard consent. It never overwrites conflicting
policy or disables global restrictions. Fased itself stays unprivileged.
See the source installation guide in `docs/install.md`. CI exercises automatic
preparation only on its disposable Ubuntu runner; it changes no owner policy.
