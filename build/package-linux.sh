#!/usr/bin/env bash
# Builds the Linux release formats from an already-built build/bin/podsteer:
# an AppImage, a .deb and an .rpm. Usage: build/package-linux.sh <amd64|arm64> <version>
#
# <version> is the tag (v1.2.3, v1.2.3-rc-1) or "dev". Output goes to
# build/dist/ under stable names that the release job versions:
#   podsteer-linux-<arch>.AppImage | .deb | .rpm
#
# Needs: nfpm and appimagetool on PATH. CI installs both.
#
# THE APPIMAGE DOES NOT BUNDLE GTK OR WEBKIT. A bundled webview is hundreds of
# MB and ties the image to one glibc; like the zip, it uses the host's
# libgtk-3 and libwebkit2gtk-4.1, which every mainstream desktop has installed.
set -euo pipefail

arch="${1:?arch (amd64|arm64) required}"
tag="${2:-dev}"

case "$arch" in
  amd64) appimage_arch=x86_64 ;;
  arm64) appimage_arch=aarch64 ;;
  *) echo "unsupported arch: $arch" >&2; exit 2 ;;
esac

# Package versions must start with a digit, and "-" means "release" to rpm and
# deb. A pre-release tag becomes 1.2.3~rc.1, which sorts BEFORE 1.2.3.
version="${tag#v}"
version="${version/-rc-/~rc.}"
version="${version/-dev-/~dev.}"
[[ "$version" =~ ^[0-9] ]] || version="0.0.0~${version}"

out=build/dist
rm -rf "$out" build/AppDir
mkdir -p "$out"

export VERSION="$version" ARCH="$arch"
nfpm package --config build/linux/nfpm.yaml --packager deb --target "$out/podsteer-linux-${arch}.deb"
nfpm package --config build/linux/nfpm.yaml --packager rpm --target "$out/podsteer-linux-${arch}.rpm"

appdir=build/AppDir
mkdir -p "$appdir/usr/bin"
cp build/bin/podsteer "$appdir/usr/bin/podsteer"
cp build/linux/podsteer.desktop "$appdir/podsteer.desktop"
cp build/appicon.png "$appdir/podsteer.png"
cat > "$appdir/AppRun" <<'RUN'
#!/bin/sh
here="$(dirname "$(readlink -f "$0")")"
exec "$here/usr/bin/podsteer" "$@"
RUN
chmod +x "$appdir/AppRun"

# --appimage-extract-and-run: hosted runners have no FUSE.
ARCH="$appimage_arch" APPIMAGE_EXTRACT_AND_RUN=1 \
  appimagetool "$appdir" "$out/podsteer-linux-${arch}.AppImage"

ls -la "$out"
