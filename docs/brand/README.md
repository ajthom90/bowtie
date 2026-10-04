# Bowtie brand

The logo is a classic UHF "bowtie" TV antenna: two triangular wire loops
meeting at a feed point, on a short mast and set-top stand, with a signal
arc above. It is meant to read as an antenna, not a necktie.

| File | Use |
| --- | --- |
| `bowtie-icon.svg` | Square app icon on the dark background (full bleed; platforms mask the corners). |
| `bowtie-mark.svg` | The antenna alone on a transparent background, for layered and adaptive icons and dark UI. |
| `bowtie-favicon.svg` | Simplified version on a 32 grid for 16 to 48 px (favicon, tiny launcher sizes). |
| `bowtie-wordmark.svg` | "Bowtie" in Avenir Next Condensed Bold, outlined to paths (no font needed). |
| `bowtie-lockup.svg` | Mark and wordmark side by side on a dark tile. `render.py` generates it. |

## Colors

These are the app's existing theme tokens (`ios/App/Shared/Theme.swift`,
`android/app/.../Theme.kt`, `web/src/global.css`):

| Token | Hex | In the logo |
| --- | --- | --- |
| amber (`--accent`) | `#F0A428` | Bowtie elements and signal arcs (the outer arc at 50% opacity) |
| dim (`--text-dim`) | `#9BA5AE` | Mast and stand |
| text (`--text`) | `#F2EFE8` | Feed point and wordmark |
| bg (`--bg`) | `#101418` | Background (icon gradient ends here; splash and adaptive-icon background) |
| surface (`--surface`) | `#1A2027` | Top of the icon background gradient |

The mark is designed for dark backgrounds. On light backgrounds, use the
icon (with its own background) rather than the bare mark.

## Regenerating platform assets

After editing the SVGs, run:

```sh
python3 docs/brand/render.py   # needs resvg and ImageMagick (brew install resvg imagemagick)
```

It rewrites the iOS app icon, the tvOS layered icons and top-shelf images, the
Android legacy launcher PNGs (phone and TV), the Android TV banner, the Roku
channel posters and splash screens, and the web favicon, `favicon.ico` and
`apple-touch-icon.png`.

Two copies of the geometry are written by hand and must be kept in sync with
`bowtie-mark.svg`:

- `android/{app,tv}/src/main/res/drawable/ic_launcher_{foreground,monochrome}.xml`
  (adaptive icon vectors, scaled by 0.078 into the 66 dp safe zone)
- `web/src/BowtieMark.tsx` (inline mark in the web header and login screen)
