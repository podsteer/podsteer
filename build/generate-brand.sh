#!/usr/bin/env bash
#
# Renders every brand PNG from its SVG source.
#
# The SVGs are the originals; everything in brand/png is derived and safe to
# delete. This script also rewrites the two places the brand is wired into the
# application — the packaged icon and the frontend's favicons — so those cannot
# drift from the source when the mark changes.
#
# Requires librsvg and ImageMagick:  brew install librsvg imagemagick
set -euo pipefail

cd "$(dirname "$0")/.."

for tool in rsvg-convert magick; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: $tool is not installed. brew install librsvg imagemagick" >&2
    exit 1
  fi
done

mkdir -p brand/png web/public

echo "Marks and lockups"
rsvg-convert -w 512 -h 512 brand/podsteer-mark.svg        -o brand/png/podsteer-mark-512.png
rsvg-convert -w 512 -h 512 brand/podsteer-mark-white.svg  -o brand/png/podsteer-mark-white-512.png
rsvg-convert -w 1024        brand/podsteer-logo.svg       -o brand/png/podsteer-logo-light-1024.png
rsvg-convert -w 1024        brand/podsteer-logo-dark.svg  -o brand/png/podsteer-logo-dark-1024.png

echo "Avatars"
# LinkedIn wants 400, Bluesky accepts up to 1000, GitHub organisations 460.
# All three crop to a circle, which the tile's 80% mark inset allows for.
rsvg-convert -w 400  -h 400  brand/podsteer-tile.svg -o brand/png/podsteer-avatar-400.png
rsvg-convert -w 460  -h 460  brand/podsteer-tile.svg -o brand/png/podsteer-avatar-460.png
rsvg-convert -w 1000 -h 1000 brand/podsteer-tile.svg -o brand/png/podsteer-avatar-1000.png

echo "Share card"
rsvg-convert -w 1280 -h 640 brand/podsteer-social.svg -o brand/png/podsteer-social-1280x640.png

echo "Application icon"
# 880 inside 1024: macOS icons are not edge-to-edge and Wails adds no margin.
rsvg-convert -w 880 -h 880 brand/podsteer-tile.svg -o /tmp/podsteer-tile-880.png
magick /tmp/podsteer-tile-880.png -background none -gravity center -extent 1024x1024 build/appicon.png
rm -f /tmp/podsteer-tile-880.png

echo "Frontend favicons"
cp brand/podsteer-favicon.svg web/public/favicon.svg
rsvg-convert -w 32  -h 32  brand/podsteer-favicon.svg -o web/public/favicon-32.png
rsvg-convert -w 180 -h 180 brand/podsteer-tile.svg    -o web/public/apple-touch-icon.png

echo "Done."
