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

Ubuntu's Noble packages do not install this extra profile. A host administrator
must review and install it, refusing an existing conflicting profile.
See `docs/install.md`. Fased never loads or changes AppArmor policy itself.
CI applies it only on its disposable Ubuntu runner. Do not disable global
namespace restrictions or run Fased as root.
