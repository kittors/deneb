#!/usr/bin/env python3
"""The Deneb logo as decided on 2026-11-09: 一颗亮星, a bright star (DESIGN
§15). Batch 1, the three directions the user chose from, is in batch1/.

    design/brand/build-final.sh      # runs this, then renders final/

writes the SVG sources under final/; build-final.sh renders the PNGs, the
.icns and the .ico from them, and copies the menu bar images from
cmd/deneb/trayicons (drawn by its TestTrayIcons from the same glyphs).

  final/mark/      deneb-mark{,-black,-white}.svg          40 px and up
                   deneb-mark-small{,-black,-white}.svg    16 to 32 px
  final/wordmark/  deneb-wordmark{,-white}.svg
  final/lockup/    deneb-lockup.svg (on light), deneb-lockup-dark.svg (on dark)
  final/macos/     app-icon.svg, app-icon-small.svg, AppIcon.iconset/, Deneb.icns
  final/windows/   tile.svg, tile-small.svg, Deneb.ico
  final/linux/     deneb-<n>.png
  final/web/       favicon.svg, favicon-16.png, favicon-32.png, apple-touch-icon.png
  final/tray/      solid.png, dot.png, hollow.png
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)  # python3 -I leaves the script's folder off the path

import gen  # noqa: E402
from gen import BLUE, INK, f, squircle, star4  # noqa: E402

OUT = os.path.join(HERE, "final")

# The mark in its 100-unit box. Full: 40 px and up; small: 16 to 32 px,
# where thinner spikes would fade, so they are bolder and the core bigger.
FULL = dict(rv=46, rh=40, core=16, a=0.02, b=0.14)
SMALL = dict(rv=47, rh=43, core=19, a=0.03, b=0.24)


def mark(fg, small=False):
    m = SMALL if small else FULL
    return (f'<path d="{star4(50, 50, m["rv"], m["rh"], m["a"], m["b"])}" fill="{fg}"/>'
            f'<circle cx="50" cy="50" r="{f(m["core"])}" fill="{fg}"/>')


# The same mark in the 24-unit box the client's and the web's glyphs use
# (cmd/deneb/glyphs.go starGlyph and markGlyph, web/app/src star()): 21
# units from tip to tip, as the star it replaces.
def glyph_paths(small=True):
    m = SMALL if small else FULL
    k = 21 / (2 * m["rv"])
    return star4(12, 12, m["rv"] * k, m["rh"] * k, m["a"], m["b"]), f(m["core"] * k)


def svg(w, h, body, defs=""):
    return gen.svg(w, h, body, defs)


def mark_svg(fg, small=False):
    return svg(512, 512, f'<g transform="scale(5.12)">{mark(fg, small)}</g>')


GRAD = (
    '<linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">'
    '<stop offset="0" stop-color="#5b8fff"/><stop offset="0.55" stop-color="#3a72ff"/>'
    '<stop offset="1" stop-color="#2353e6"/></linearGradient>'
    '<radialGradient id="hi" cx="0.5" cy="0.02" r="0.72">'
    '<stop offset="0" stop-color="#ffffff" stop-opacity="0.30"/>'
    '<stop offset="0.6" stop-color="#ffffff" stop-opacity="0.06"/>'
    '<stop offset="1" stop-color="#ffffff" stop-opacity="0"/></radialGradient>'
    '<filter id="ms" x="-20%" y="-20%" width="140%" height="140%">'
    '<feDropShadow dx="0" dy="6" stdDeviation="8" flood-color="#0d2a8a" flood-opacity="0.22"/></filter>'
)


def tile(small=False, inset=0.03, shadow=False, scale=0.62):
    """The blue tile with the white mark, on a 1024 canvas. macOS draws it
    as an 824 squircle with a shadow (gen.app_icon); Windows and Linux
    fill the canvas, less a 3 % margin, with no shadow: they draw none of
    their own around an app icon, and it would only blur at 16 px."""
    m = 1024 * inset
    size = 1024 - 2 * m
    sq = squircle(m, m, size)
    sc = size * scale * (1.12 if small else 1.0) / 100
    off = 512 - 50 * sc
    defs = GRAD + ('<filter id="sh" x="-20%" y="-20%" width="140%" height="150%">'
                   '<feDropShadow dx="0" dy="14" stdDeviation="16" flood-color="#0d1f5c" flood-opacity="0.30"/></filter>'
                   if shadow else "")
    body = (f'<path d="{sq}" fill="url(#bg)"{" filter=" + chr(34) + "url(#sh)" + chr(34) if shadow else ""}/>'
            f'<path d="{sq}" fill="url(#hi)"/>'
            f'<path d="{sq}" fill="none" stroke="#ffffff" stroke-opacity="0.16" stroke-width="3"/>'
            f'<g transform="translate({f(off)} {f(off)}) scale({f(sc)})"{"" if small else " filter=" + chr(34) + "url(#ms)" + chr(34)}>'
            + mark("#ffffff", small) + "</g>")
    return svg(1024, 1024, body, defs)


def apple_touch():
    """180 px for iOS, which rounds the corners itself: a full square."""
    sc = 1024 * 0.60 / 100
    off = 512 - 50 * sc
    body = ('<rect width="1024" height="1024" fill="url(#bg)"/><rect width="1024" height="1024" fill="url(#hi)"/>'
            f'<g transform="translate({f(off)} {f(off)}) scale({f(sc)})" filter="url(#ms)">' + mark("#ffffff") + "</g>")
    return svg(1024, 1024, body, GRAD)


def favicon():
    d, r = glyph_paths(small=True)
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">'
            f'<path fill="{BLUE}" d="{d}"/><circle fill="{BLUE}" cx="12" cy="12" r="{r}"/></svg>\n')


def wordmark_svg(color):
    pad = 40
    w, h = gen.WORD_W + 2 * pad, gen.ASC + 2 * pad
    return svg(w, h, f'<g transform="translate({pad} {f(pad + gen.ASC)})">' + gen.word_svg(color) + "</g>")


def lockup_svg(dark=False):
    """Mark and wordmark, transparent, with the clear space of DESIGN §15
    around them: the mark centred on the cap height, 1.5 cap heights tall,
    0.4 cap heights from the D."""
    word_c = "#ffffff" if dark else INK
    mark_c = "#ffffff" if dark else BLUE
    cap = 140.0
    unit = cap / gen.CAP
    vw, vh = 80.0, 92.0
    msc = cap * 1.5 / vh
    gap = cap * 0.40
    pad = cap * 0.5
    ww = gen.WORD_W * unit
    width = pad * 2 + vw * msc + gap + ww
    height = pad * 2 + vh * msc
    mid = height / 2
    mx = pad - (100 - vw) / 2 * msc
    my = mid - 50 * msc
    body = (f'<g transform="translate({f(mx)} {f(my)}) scale({f(msc)})">' + mark(mark_c) + "</g>"
            f'<g transform="translate({f(pad + vw * msc + gap)} {f(mid + cap / 2)}) scale({f(unit)})">'
            + gen.word_svg(word_c) + "</g>")
    return svg(width, height, body)


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
    gen.MARKS["final"] = lambda fg, bg, small=False, hollow=False, tray=False: mark(fg, small)
    gen.ICON_SCALE["final"] = 0.62
    gen.ICON_SHADOW["final"] = True
    write("macos/app-icon.svg", gen.app_icon("final"))
    write("macos/app-icon-small.svg", gen.app_icon("final", small=True))
    write("windows/tile.svg", tile())
    write("windows/tile-small.svg", tile(small=True))
    write("web/favicon.svg", favicon())
    write("web/apple-touch-icon.svg", apple_touch())
    d, r = glyph_paths(True)
    D, R = glyph_paths(False)
    write("glyphs.txt", f"small {d} r={r}\nfull {D} r={R}\n")
    print("wrote", OUT)


if __name__ == "__main__":
    main()
