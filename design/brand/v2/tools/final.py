#!/usr/bin/env python3
"""The Deneb logo, second version (2026-11-11): 一颗亮星, a bright star as
a telescope shows it (DESIGN §15). The star's shape is star.py's; this
writes everything made from it. The first version (2026-11-09) is kept in
v1/; batch 1, the three directions the user chose from, in batch1/.

    design/brand/build-final.sh      # runs this, then renders final/

writes the SVG sources under final/; build-final.sh renders the PNGs, the
.icns and the .ico from them, and copies the menu bar images from
cmd/deneb/trayicons (drawn by its TestTrayIcons from the same glyphs).

  final/mark/      deneb-mark{,-black,-white}.svg          40 px and up
                   deneb-mark-small{,-black,-white}.svg    16 to 32 px
  final/wordmark/  deneb-wordmark{,-white}.svg
  final/lockup/    deneb-lockup.svg (on light), deneb-lockup-dark.svg (on dark)
  final/macos/     app-icon.svg (48 px and up), app-icon-small.svg (22 to
                   32), app-icon-16.svg, AppIcon.iconset/, Deneb.icns
  final/windows/   tile.svg, tile-small.svg, tile-16.svg (the same cuts),
                   Deneb.ico
  final/linux/     deneb-<n>.png
  final/web/       favicon.svg, favicon-16.png, favicon-32.png, apple-touch-icon.png
  final/tray/      solid.png, dot.png, hollow.png
  final/glyphs.txt the 24-unit glyphs of the client and the web
"""
import math
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # python3 -I leaves the script's folder off the path

import gen  # noqa: E402
import star  # noqa: E402
from gen import INK, f, squircle  # noqa: E402

BLUE = "#2f6bff"
BLUE_ON_DARK = "#4c82ff"  # the primary of the dark UI (DESIGN §3)
OUT = os.path.join(HERE, "final")


def svg(w, h, body, defs=""):
    return gen.svg(w, h, body, defs)


# ---------------------------------------------------------------- the mark

def mark(fg, small=False):
    """The mark in its 100-unit box, centred on (50, 50)."""
    return f'<path d="{star.mark_d(50, 50, 1, small)}" fill="{fg}"/>'


def mark_svg(fg, small=False):
    return svg(512, 512, f'<g transform="scale(5.12)">{mark(fg, small)}</g>')


# ---------------------------------------------------------------- the icon
#
# A night sky in the brand's blue: deep indigo at the top to the brand blue
# at the bottom, and around the star the sky lit by it. The star is white,
# its rays cooling to a pale blue at the tips, with a soft glow of its own
# shape; past the tips run the faint diffraction spikes a bright star shows
# through a telescope. All of it was tuned on macOS's 824 px squircle and
# scales with the tile.

SKY = (("0", "#1b3cab"), ("0.5", "#2355e0"), ("1", "#2f6bff"))
LIT = "#4f86ff"          # the sky around the star
BLOOM = "#a8c6ff"        # the glow of the star's own shape
TIP = "#d6e4ff"          # the rays' tips
SPIKE_LEN = 1.45         # diffraction spikes, in ray lengths from the centre
STAR_SCALE = 0.67        # the 100-unit box across this much of the tile
STAR_SCALE_SMALL = 0.80  # the small cut: a larger star, bolder rays
STAR_SCALE_16 = 0.86     # the 16 px cut: larger still


def _stops(spec):
    out = ""
    for o, c, a in spec:
        out += f'<stop offset="{o}" stop-color="{c}"' + (f' stop-opacity="{star.fmt(a, 3)}"' if a != 1 else "") + "/>"
    return out


def _glow(color, peak, n=7, k=2.6):
    """Stops of a radial gradient that fades like a soft glow."""
    return [(star.fmt(i / (n - 1), 3), color, peak * math.exp(-k * (i / (n - 1)) ** 2) * (1 - i / (n - 1)) ** 0.6)
            for i in range(n)]


def night(x, y, size, outline, small=False, shadow=False):
    """The night-sky tile, filling the square (x, y, size) of a 1024
    canvas: outline is its shape (a squircle, or the square itself).
    Returns (defs, body). small: the cut for 22 to 32 px: the small
    mark, larger, and of the light only the lit sky, as the rest would
    blur the star there. small="tiny": the 16 px one, the tiny mark half a
    pixel up and left, so that its one-pixel rays sit on the pixel grid
    instead of smearing over two."""
    cx, cy = x + size / 2, y + size / 2
    u = size / 824
    sc = size * (STAR_SCALE_16 if small == "tiny" else STAR_SCALE_SMALL if small else STAR_SCALE) / 100
    if small == "tiny":
        cx, cy = cx - 32, cy - 32  # half of a 16 px icon's pixel, 64 units
    d = star.mark_d(cx, cy, sc, small)
    defs = [
        f'<clipPath id="tile"><path d="{outline}"/></clipPath>',
        f'<linearGradient id="sky" x1="0" y1="{f(y)}" x2="0" y2="{f(y + size)}" gradientUnits="userSpaceOnUse">'
        + _stops([(o, c, 1) for o, c in SKY]) + "</linearGradient>",
        f'<radialGradient id="lit" cx="{f(cx)}" cy="{f(cy)}" r="{f(size / 2)}" gradientUnits="userSpaceOnUse">'
        + _stops(_glow(LIT, 0.45 if small else 0.65)) + "</radialGradient>",
    ]
    if shadow:
        defs.append(f'<filter id="shadow" x="-20%" y="-20%" width="140%" height="150%">'
                    f'<feDropShadow dx="0" dy="{f(12 * u)}" stdDeviation="{f(14 * u)}" flood-color="#081a52" flood-opacity="0.32"/></filter>')
    body = [f'<path d="{outline}" fill="url(#sky)"' + (' filter="url(#shadow)"' if shadow else "") + "/>",
            '<g clip-path="url(#tile)">',
            f'<circle cx="{f(cx)}" cy="{f(cy)}" r="{f(size / 2)}" fill="url(#lit)"/>']
    if small:
        body.append(f'<path d="{d}" fill="#fff"/>')
    else:
        rv, rh = star.FULL["rv"] * sc, star.FULL["rh"] * sc
        w = 4.5 * u  # half the spikes' width at the centre
        defs += [
            f'<filter id="bloom" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="{f(24 * u)}"/></filter>',
            f'<filter id="soft" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="{f(4 * u)}"/></filter>',
            f'<filter id="haze" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="{f(1 * u)}"/></filter>',
            f'<radialGradient id="spike" cx="{f(cx)}" cy="{f(cy)}" r="{f(rv * SPIKE_LEN)}" gradientUnits="userSpaceOnUse">'
            + _stops([("0", "#fff", 0.85), ("0.45", "#fff", 0.6), ("0.72", "#fff", 0.27), ("1", "#fff", 0)]) + "</radialGradient>",
            f'<radialGradient id="ray" cx="{f(cx)}" cy="{f(cy)}" r="{f(rv * 1.02)}" gradientUnits="userSpaceOnUse">'
            + _stops([("0", "#fff", 1), ("0.3", "#fff", 1), ("1", TIP, 1)]) + "</radialGradient>",
        ]
        Lv, Lh = rv * SPIKE_LEN, rh * SPIKE_LEN
        body += [
            f'<path d="{d}" fill="{BLOOM}" opacity="0.7" filter="url(#bloom)"/>',
            '<g fill="url(#spike)" filter="url(#haze)">'
            f'<path d="M{f(cx)},{f(cy - Lv)} L{f(cx + w)},{f(cy)} L{f(cx)},{f(cy + Lv)} L{f(cx - w)},{f(cy)}Z"/>'
            f'<path d="M{f(cx - Lh)},{f(cy)} L{f(cx)},{f(cy - w)} L{f(cx + Lh)},{f(cy)} L{f(cx)},{f(cy + w)}Z"/></g>',
            f'<path d="{d}" fill="#fff" opacity="0.35" filter="url(#soft)"/>',
            f'<path d="{d}" fill="url(#ray)"/>',
        ]
    body.append("</g>")
    return defs, body


def app_icon(small=False):
    """macOS: the 824 px squircle of a 1024 canvas, with its shadow (the
    system draws none of its own around an app icon)."""
    defs, body = night(100, 100, 824, squircle(100, 100, 824), small, shadow=True)
    return svg(1024, 1024, "".join(body), "".join(defs))


def tile(small=False, inset=0.03):
    """Windows and Linux: the squircle fills the canvas, less a 3 % margin,
    with no shadow: they draw no shadow around an app icon, and it would
    only blur at 16 px."""
    m = 1024 * inset
    defs, body = night(m, m, 1024 - 2 * m, squircle(m, m, 1024 - 2 * m), small)
    return svg(1024, 1024, "".join(body), "".join(defs))


def apple_touch():
    """180 px for iOS, which rounds the corners itself: the full square."""
    defs, body = night(0, 0, 1024, "M0,0 H1024 V1024 H0Z")
    return svg(1024, 1024, "".join(body), "".join(defs))


# ---------------------------------------------------------------- glyphs
#
# The client's and the web's glyphs are 24 units, the star 21 from tip to
# tip (cmd/deneb/glyphs.go starGlyph and markGlyph, trayglyph.go, the
# web's star() and G.star).

DOT = (20.0, 20.0, 3.2)  # the menu bar's dot: centre and radius


def glyph(d_body, fill="#000"):
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">{d_body}</svg>'


def tray_dot():
    """Something wants you: the star and a dot at its bottom right. The
    star's concave sides leave a ring of space around the dot."""
    cx, cy, r = DOT
    return glyph(f'<path fill="#000" d="{star.glyph_d(True)}"/><circle fill="#000" cx="{f(cx)}" cy="{f(cy)}" r="{f(r)}"/>')


# Nothing connects: the star's outline. The small star is too slender for
# a 1.6-unit line inside its rays, so the outline follows a stouter star,
# a little smaller, whose outer edge lands on the solid star's.
HOLLOW = dict(rv=9.7, rh=8.2, n=0.62, tip=8.0)
HOLLOW_W = 1.6


def hollow_d():
    p = HOLLOW
    q, _ = star.quarter(p["rv"], p["rh"], p["n"], p["tip"], 2)
    return star.path(12, 12, 1, q)


def tray_hollow():
    return glyph(f'<path fill="none" stroke="#000" stroke-width="{f(HOLLOW_W)}" stroke-linejoin="round" d="{hollow_d()}"/>')


def favicon():
    """The small mark in the brand blue, a step lighter in a dark browser
    (the dark UI's primary). Drawn at 20 px or less (a tab at 1x), it is
    the 16 px cut instead, its centre moved to where a 16 px drawing has
    the middle of a pixel, so that its one-pixel rays land on the grid: an
    SVG used as an image takes its own size as the viewport the media
    query reads."""
    q, _ = star.mark_quarter("tiny")
    tiny = star.path(11.25, 11.25, 10.5 / star.TINY["rv"], q)
    return ('<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">'
            f'<style>path{{fill:{BLUE}}}.px{{display:none}}@media (max-width:20px){{.px{{display:inline}}.pt{{display:none}}}}'
            f'@media (prefers-color-scheme:dark){{path{{fill:{BLUE_ON_DARK}}}}}</style>'
            f'<path class="pt" d="{star.glyph_d(True)}"/><path class="px" d="{tiny}"/></svg>\n')


def favicon_uri():
    """favicon() as the data: URI web/app/index.html links."""
    s = favicon().strip().replace('"', "'")
    for a, b in (("%", "%25"), ("#", "%23"), ("<", "%3C"), (">", "%3E"), ("{", "%7B"), ("}", "%7D")):
        s = s.replace(a, b)
    return "data:image/svg+xml," + s


# ---------------------------------------------------------------- type

LOCK_H = 1.6  # the mark's height in a lockup, in cap heights


def wordmark_svg(color):
    pad = 40
    w, h = gen.WORD_W + 2 * pad, gen.ASC + 2 * pad
    return svg(w, h, f'<g transform="translate({pad} {f(pad + gen.ASC)})">' + gen.word_svg(color) + "</g>")


def lockup_svg(dark=False):
    """Mark and wordmark, transparent, with the clear space of DESIGN §15
    around them: the mark centred on the cap height, LOCK_H cap heights
    tall, 0.4 cap heights from the D."""
    word_c = "#ffffff" if dark else INK
    mark_c = "#ffffff" if dark else BLUE
    cap = 140.0
    unit = cap / gen.CAP
    vw, vh = 2 * star.FULL["rh"], 2 * star.FULL["rv"]
    msc = cap * LOCK_H / vh
    gap = cap * 0.40
    pad = cap * 0.5
    ww = gen.WORD_W * unit
    width = pad * 2 + vw * msc + gap + ww
    height = pad * 2 + vh * msc
    mid = height / 2
    mx = pad - (100 - vw) / 2 * msc
    my = mid - 50 * msc
    body = (f'<g transform="translate({f(mx)} {f(my)}) scale({star.fmt(msc, 4)})">' + mark(mark_c) + "</g>"
            f'<g transform="translate({f(pad + vw * msc + gap)} {f(mid + cap / 2)}) scale({star.fmt(unit, 4)})">'
            + gen.word_svg(word_c) + "</g>")
    return svg(width, height, body)


# ---------------------------------------------------------------- files

def write(rel, text):
    path = os.path.join(OUT, rel)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(text)


def main():
    for name, color in (("", BLUE), ("-black", "#000000"), ("-white", "#ffffff")):
        write(f"mark/deneb-mark{name}.svg", mark_svg(color))
        write(f"mark/deneb-mark-small{name}.svg", mark_svg(color, small=True))
    write("wordmark/deneb-wordmark.svg", wordmark_svg(INK))
    write("wordmark/deneb-wordmark-white.svg", wordmark_svg("#ffffff"))
    write("lockup/deneb-lockup.svg", lockup_svg())
    write("lockup/deneb-lockup-dark.svg", lockup_svg(dark=True))
    write("macos/app-icon.svg", app_icon())
    write("macos/app-icon-small.svg", app_icon(small=True))
    write("macos/app-icon-16.svg", app_icon(small="tiny"))
    write("windows/tile.svg", tile())
    write("windows/tile-small.svg", tile(small=True))
    write("windows/tile-16.svg", tile(small="tiny"))
    write("web/favicon.svg", favicon())
    write("web/apple-touch-icon.svg", apple_touch())
    write("glyphs.txt",
          "# 24-unit glyphs (DESIGN §15.2): cmd/deneb/glyphs.go, trayglyph.go, the web's star() and G.star\n"
          f"small (starGlyph, star(), G.star, favicon)\n  {star.glyph_d(True)}\n"
          f"full (markGlyph)\n  {star.glyph_d(False)}\n"
          f"tray dot (trayStarDot)\n  {tray_dot()}\n"
          f"tray hollow (trayStarHollow)\n  {tray_hollow()}\n"
          f"favicon (web/app/index.html)\n  {favicon_uri()}\n")
    print("wrote", OUT)


if __name__ == "__main__":
    main()
