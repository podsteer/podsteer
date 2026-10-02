# Package-manager manifests

Templates only. Nothing here is submitted anywhere automatically, and the
release workflow never contacts a package manager.

Placeholders: `@VERSION@` is the release without the leading `v`; `@SHA256@`
comes from the release's `podsteer_<tag>_checksums.txt`.

## winget

Needs the signed Windows installer from a release that includes it.

1. Copy the three `packaging/winget/*.tmpl` files, drop the `.tmpl` suffix and
   fill the placeholders. winget wants them in a folder named for the version
   as `PodSteer.PodSteer.yaml`, `PodSteer.PodSteer.installer.yaml` and
   `PodSteer.PodSteer.locale.en-US.yaml`.
2. Validate: `winget validate --manifest <folder>`.
3. Test: `winget install --manifest <folder>`.
4. Submit a pull request to https://github.com/microsoft/winget-pkgs under
   `manifests/p/PodSteer/PodSteer/<version>/` (or use `wingetcreate submit`).
   The installer is `nullsoft` (NSIS) and installs per user; confirm `/S` is
   still its silent switch.

An unsigned installer will be flagged by SmartScreen and may be rejected in
winget's validation, so submit only releases published with signing.

## Scoop

Scoop installs the Windows zip, so it does not depend on the installer.

1. Fill `packaging/scoop/podsteer.json.tmpl` and save it as `podsteer.json`.
2. Test: `scoop install ./podsteer.json`.
3. Either publish it from a bucket repository (for example
   `podsteer/scoop-bucket`, which does not exist yet) or propose it to
   https://github.com/ScoopInstaller/Extras per their contributing guide.
   `checkver` and `autoupdate` let the bucket's own tooling keep it current.
