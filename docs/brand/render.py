#!/usr/bin/env python3
"""Regenerate every platform's logo assets from the SVG masters in docs/brand.

Needs `resvg` and ImageMagick (`magick`) on PATH (brew install resvg imagemagick).
Run from anywhere:  python3 docs/brand/render.py
"""

import os
import re
import shutil
import subprocess
import tempfile

BRAND = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(BRAND))

BG_TOP = "#1A2027"   # surface
BG = "#101418"       # bg
AMBER = "#F0A428"
DIM = "#9BA5AE"
TEXT = "#F2EFE8"

# Visual bounds of the mark inside its 1024 canvas (measured with `magick -trim`).
MARK_BOX = (146, 159, 732, 674)  # x, y, w, h
MARK_CX = MARK_BOX[0] + MARK_BOX[2] / 2
MARK_CY = MARK_BOX[1] + MARK_BOX[3] / 2
# Farthest painted point from (MARK_CX, MARK_CY), for fitting inside circles.
MARK_RADIUS = 432

# Wordmark viewBox (see bowtie-wordmark.svg).
WORD_BOX = (30, 29, 453, 124)


def read(name):
    with open(os.path.join(BRAND, name)) as f:
        return f.read()


MARK = re.search(r"<!-- mark:begin -->(.*?)<!-- mark:end -->", read("bowtie-mark.svg"), re.S).group(1)
WORD = re.search(r"(<path [^>]*/>)", read("bowtie-wordmark.svg"), re.S).group(1)


def svg(w, h, body, bg=True):
    defs = ""
    rect = ""
    if bg:
        defs = (
            '<defs><linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">'
            f'<stop offset="0" stop-color="{BG_TOP}"/><stop offset="1" stop-color="{BG}"/>'
            "</linearGradient></defs>"
        )
        rect = f'<rect width="{w}" height="{h}" fill="url(#bg)"/>'
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}">'
        f"{defs}{rect}{body}</svg>"
    )


def mark(cx, cy, height):
    s = height / MARK_BOX[3]
    return f'<g transform="translate({cx:.2f} {cy:.2f}) scale({s:.5f}) translate({-MARK_CX} {-MARK_CY})">{MARK}</g>'


def mark_width(height):
    return MARK_BOX[2] * height / MARK_BOX[3]


def word(x, cy, height):
    """Wordmark with its left edge at x, vertically centred on cy."""
    s = height / WORD_BOX[3]
    return (
        f'<g transform="translate({x:.2f} {cy - height / 2:.2f}) scale({s:.5f}) '
        f'translate({-WORD_BOX[0]} {-WORD_BOX[1]})">{WORD}</g>'
    )


def word_width(height):
    return WORD_BOX[2] * height / WORD_BOX[3]


def lockup(w, h, mark_h, bg=True):
    """Mark beside the wordmark, centred as a group."""
    word_h = mark_h * 0.42
    gap = mark_h * 0.22
    total = mark_width(mark_h) + gap + word_width(word_h)
    x0 = (w - total) / 2
    cy = h / 2
    body = mark(x0 + mark_width(mark_h) / 2, cy, mark_h) + word(x0 + mark_width(mark_h) + gap, cy + mark_h * 0.03, word_h)
    return svg(w, h, body, bg)


def stacked(w, h, mark_h, bg=True):
    """Mark above the wordmark, centred (splash screens)."""
    word_h = mark_h * 0.36
    gap = mark_h * 0.16
    total = mark_h + gap + word_h
    y0 = (h - total) / 2
    body = mark(w / 2, y0 + mark_h / 2, mark_h) + word(w / 2 - word_width(word_h) / 2, y0 + mark_h + gap + word_h / 2, word_h)
    return svg(w, h, body, bg)


TMP = tempfile.mkdtemp()


def out(rel):
    path = os.path.join(ROOT, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    return path


def render(svg_text, rel, w, h=None, opaque=False):
    src = os.path.join(TMP, "in.svg")
    with open(src, "w") as f:
        f.write(svg_text)
    dst = out(rel)
    subprocess.run(["resvg", "-w", str(w), *(["-h", str(h)] if h else []), src, dst], check=True)
    if opaque:
        # App Store / tvOS / Roku want no alpha channel at all.
        subprocess.run(["magick", dst, "-background", BG, "-alpha", "remove", "-alpha", "off", "-strip", dst], check=True)
    else:
        subprocess.run(["magick", dst, "-strip", dst], check=True)
    print(rel)


def render_file(name, rel, w, opaque=False):
    render(read(name), rel, w, opaque=opaque)


# ---------------------------------------------------------------- iOS
render_file("bowtie-icon.svg", "ios/App/iOS/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png", 1024, opaque=True)

# ---------------------------------------------------------------- tvOS
TV = "ios/App/tvOS/Assets.xcassets/App Icon & Top Shelf Image.brandassets"
for stack, sizes in (("App Icon", ((400, 240, ""), (800, 480, "@2x"))), ("App Icon - App Store", ((1280, 768, ""),))):
    for w, h, sfx in sizes:
        base = f"{TV}/{stack}.imagestack"
        render(svg(w, h, ""), f"{base}/Back.imagestacklayer/Content.imageset/back{sfx}.png", w, h, opaque=True)
        # Front layer: transparent, inset so the parallax effect never clips it.
        render(svg(w, h, mark(w / 2, h / 2, h * 0.66), bg=False), f"{base}/Front.imagestacklayer/Content.imageset/front{sfx}.png", w, h)
for name, w in (("Top Shelf Image", 1920), ("Top Shelf Image Wide", 2320)):
    for scale, sfx in ((1, ""), (2, "@2x")):
        W, H = w * scale, 720 * scale
        render(lockup(W, H, H * 0.42), f"{TV}/{name}.imageset/shelf{sfx}.png", W, H, opaque=True)

# ---------------------------------------------------------------- Android (phone + TV)
LEGACY = (("mdpi", 48), ("hdpi", 72), ("xhdpi", 96), ("xxhdpi", 144), ("xxxhdpi", 192))
for module in ("app", "tv"):
    res = f"android/{module}/src/main/res"
    for dpi, px in LEGACY:
        # Square launcher icon: rounded tile with the standard 2/48 margin.
        m = 1024 * 2 / 48
        tile = (
            f'<defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="{BG_TOP}"/>'
            f'<stop offset="1" stop-color="{BG}"/></linearGradient></defs>'
            f'<rect x="{m}" y="{m}" width="{1024 - 2 * m}" height="{1024 - 2 * m}" rx="{1024 * 0.09}" fill="url(#g)"/>'
        )
        render(svg(1024, 1024, tile + mark(512, 512, 1024 * 0.62), bg=False), f"{res}/mipmap-{dpi}/ic_launcher.png", px)
        disc = tile.replace(f'<rect x="{m}" y="{m}" width="{1024 - 2 * m}" height="{1024 - 2 * m}" rx="{1024 * 0.09}"', f'<circle cx="512" cy="512" r="{512 - m}"')
        r_fit = (512 - m) * 0.86
        render(svg(1024, 1024, disc + mark(512, 512, MARK_BOX[3] * r_fit / MARK_RADIUS), bg=False), f"{res}/mipmap-{dpi}/ic_launcher_round.png", px)
render(lockup(1280, 720, 720 * 0.44), "android/tv/src/main/res/drawable/banner.png", 320, 180, opaque=True)
# The adaptive-icon XML (foreground/monochrome vectors) is hand-written in
# android/*/src/main/res/drawable; keep its paths in sync with bowtie-mark.svg.

# ---------------------------------------------------------------- Roku
render(lockup(1344, 840, 840 * 0.46), "roku/images/mm_icon_focus_hd.png", 336, 210, opaque=True)
render(lockup(1230, 700, 700 * 0.46), "roku/images/mm_icon_focus_sd.png", 246, 140, opaque=True)
for w, h, name in ((1920, 1080, "fhd"), (1280, 720, "hd")):
    body = stacked(w, h, h * 0.34)
    # Splash sits on the manifest's flat splash_color, so drop the gradient.
    render(body.replace("url(#bg)", BG), f"roku/images/splash_screen_{name}.png", w, h, opaque=True)

# ---------------------------------------------------------------- Web
shutil.copy(os.path.join(BRAND, "bowtie-favicon.svg"), out("web/public/favicon.svg"))
print("web/public/favicon.svg")
render_file("bowtie-icon.svg", "web/public/apple-touch-icon.png", 180, opaque=True)
icos = []
for px in (16, 32, 48):
    p = os.path.join(TMP, f"fav{px}.png")
    subprocess.run(["resvg", "-w", str(px), os.path.join(BRAND, "bowtie-favicon.svg"), p], check=True)
    icos.append(p)
subprocess.run(["magick", *icos, out("web/public/favicon.ico")], check=True)
print("web/public/favicon.ico")

# ---------------------------------------------------------------- docs
with open(os.path.join(BRAND, "bowtie-lockup.svg"), "w") as f:
    body = lockup(1200, 360, 240)
    body = body.replace('<rect width="1200" height="360"', '<rect width="1200" height="360" rx="40"')
    f.write(body + "\n")
print("docs/brand/bowtie-lockup.svg")

shutil.rmtree(TMP)
