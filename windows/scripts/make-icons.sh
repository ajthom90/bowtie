#!/usr/bin/env bash
# Regenerate the Windows app's MSIX logo assets and AppIcon.ico from the
# shared 1024px brand icon. Run after the logo changes:
#
#   windows/scripts/make-icons.sh [path/to/icon-1024.png]
#
# Needs ImageMagick 7 (`magick`). Defaults to the iOS app icon (the same art
# as the other apps' launchers).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
src="${1:-$repo/ios/App/iOS/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png}"
out="$repo/windows/Bowtie.App/Assets"
mkdir -p "$out"

# Background for the wide and splash tiles: the icon's own corner color.
bg="$(magick "$src" -format '%[pixel:p{4,4}]' info:)"

square() { magick "$src" -resize "${2}x${2}" -strip "$out/$1"; }
# The icon centered on a wide canvas of the background color.
wide() { magick -size "${2}x${3}" "xc:$bg" \( "$src" -resize "${3}x${3}" \) -gravity center -composite -strip "$out/$1"; }

square Square150x150Logo.scale-200.png 300
square Square44x44Logo.scale-200.png 88
square Square44x44Logo.targetsize-24_altform-unplated.png 24
square Square44x44Logo.targetsize-48_altform-unplated.png 48
square Square44x44Logo.targetsize-256_altform-unplated.png 256
square LockScreenLogo.scale-200.png 48
square StoreLogo.png 50
wide Wide310x150Logo.scale-200.png 620 300
wide SplashScreen.scale-200.png 1240 600

magick "$src" -define icon:auto-resize=256,64,48,32,24,16 "$out/AppIcon.ico"

echo "Wrote assets to $out (background $bg)"
