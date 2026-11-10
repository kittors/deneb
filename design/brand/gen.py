#!/usr/bin/env python3
"""Deneb logo, first batch: three directions, drawn from parameters.

    python3 -I design/brand/gen.py          # writes the SVGs next to this file
    design/brand/render.sh <out-dir>        # renders them to PNG with Chrome

Directions:
  a-star      一颗亮星: a round core and four light spikes, the way a bright
              star looks through a telescope (the vertical spikes are longer).
  b-two-lans  两个局域网: two LANs overlapping, and this computer, the star,
              in both, the thing Deneb does that others do not.
  c-d         D 字: the D of the wordmark holding the star.

The wordmark "Deneb" is drawn here from circles and straight strokes, not
set in a font, so that it is ours: the app's system font may not be used
for a logo.
"""
import math
import os

HERE = os.path.dirname(os.path.abspath(__file__))

BLUE = "#2f6bff"
BLUE_DEEP = "#2457e0"
INK = "#18181b"
DARK_BG = "#18181b"


def f(v):
    s = f"{v:.2f}".rstrip("0").rstrip(".")
    return "0" if s in ("-0", "") else s


# ---------------------------------------------------------------- shapes

def star4(cx, cy, rv, rh, a=0.05, b=0.42):
    """A four-point star with concave sides. rv, rh: the vertical and
    horizontal half lengths. Each side is a cubic that hugs the axes near
    the tips (a) and swings in toward the centre (b)."""
    T, R, B, L = (cx, cy - rv), (cx + rh, cy), (cx, cy + rv), (cx - rh, cy)

    def side(p0, p1, sx, sy):
        # p0 is on the vertical axis, p1 on the horizontal one, or back.
        c1 = (cx + sx * a * rh, cy + sy * b * rv) if p0[0] == cx else (cx + sx * b * rh, cy + sy * a * rv)
        c2 = (cx + sx * b * rh, cy + sy * a * rv) if p0[0] == cx else (cx + sx * a * rh, cy + sy * b * rv)
        return f"C{f(c1[0])},{f(c1[1])} {f(c2[0])},{f(c2[1])} {f(p1[0])},{f(p1[1])}"

    return (f"M{f(T[0])},{f(T[1])} " + side(T, R, 1, -1) + " " + side(R, B, 1, 1) + " " +
            side(B, L, -1, 1) + " " + side(L, T, -1, -1) + "Z")


def circle(cx, cy, r):
    return (f"M{f(cx - r)},{f(cy)} A{f(r)},{f(r)} 0 1 0 {f(cx + r)},{f(cy)} "
            f"A{f(r)},{f(r)} 0 1 0 {f(cx - r)},{f(cy)}Z")


def squircle(x, y, size, n=5.0, steps=360):
    """The macOS app icon shape, as a superellipse."""
    a = size / 2
    cx, cy = x + a, y + a
    pts = []
    for i in range(steps):
        t = 2 * math.pi * i / steps
        ct, st = math.cos(t), math.sin(t)
        pts.append((cx + a * math.copysign(abs(ct) ** (2 / n), ct),
                    cy + a * math.copysign(abs(st) ** (2 / n), st)))
    return "M" + " L".join(f"{f(px)},{f(py)}" for px, py in pts) + "Z"


# ---------------------------------------------------------------- wordmark
#
# Units: x-height 100, cap height 140, ascender 146, stroke W. Round letters
# overshoot the x-height and the baseline by 1-2 units, as type does.

W = 19.0
XH, CAP, ASC = 100.0, 140.0, 146.0


def letter_D(x, base):
    w2 = W / 2
    sx = x + w2
    R = (CAP - W) / 2
    xc = x + 126 - w2 - R
    yt, yb = base - CAP + w2, base - w2
    return [f"M{f(sx)},{f(base)} V{f(base - CAP)}",
            f"M{f(sx)},{f(yt)} H{f(xc)} A{f(R)},{f(R)} 0 0 1 {f(xc)},{f(yb)} H{f(sx)}"], [], 126


def letter_e(x, base):
    """Drawn as filled outlines, not strokes: the ring opens at the bar's
    lower edge, so the bar ends flush with the round outside instead of
    sticking out of it, and the lower end is cut along the radius."""
    ro = 51.5
    ri = ro - W
    cx, cy = x + ro, base - 50
    hb = W / 2
    po, pi_, pe = math.asin(hb / ro), math.asin(hb / ri), math.radians(40)
    o1 = (cx + ro * math.cos(po), cy + hb)
    o2 = (cx + ro * math.cos(pe), cy + ro * math.sin(pe))
    i2 = (cx + ri * math.cos(pe), cy + ri * math.sin(pe))
    i1 = (cx + ri * math.cos(pi_), cy + hb)
    ring = (f"M{f(o1[0])},{f(o1[1])} A{f(ro)},{f(ro)} 0 1 0 {f(o2[0])},{f(o2[1])} "
            f"L{f(i2[0])},{f(i2[1])} A{f(ri)},{f(ri)} 0 1 1 {f(i1[0])},{f(i1[1])}Z")
    bar = f"M{f(cx - ri - 2)},{f(cy - hb)} H{f(cx + ri + 2)} V{f(cy + hb)} H{f(cx - ri - 2)}Z"
    return [], [ring, bar], 2 * ro


def letter_n(x, base):
    wn = 92.0
    sl, sr = x + W / 2, x + wn - W / 2
    R = (sr - sl) / 2
    cx = (sl + sr) / 2
    cy = base - (XH + 2) + W / 2 + R
    return [f"M{f(sl)},{f(base)} V{f(base - XH)}",
            f"M{f(sl)},{f(cy)} A{f(R)},{f(R)} 0 0 1 {f(sr)},{f(cy)} V{f(base)}"], [], wn


def letter_b(x, base):
    sx = x + W / 2
    ro = 51.5
    r = ro - W / 2
    cx, cy = sx + r, base - 50
    return [f"M{f(sx)},{f(base)} V{f(base - ASC)}",
            f"M{f(cx - r)},{f(cy)} A{f(r)},{f(r)} 0 1 0 {f(cx + r)},{f(cy)} A{f(r)},{f(r)} 0 1 0 {f(cx - r)},{f(cy)}"], [], 2 * ro


# Gaps between the letters' outer edges: round to round is tighter than
# straight to round, as the eye reads round edges as farther away.
WORD = [(letter_D, 0), (letter_e, 13), (letter_n, 15), (letter_e, 15), (letter_b, 14)]


def wordmark(x, base):
    """"Deneb" with its left edge at x: (stroke paths, fill paths, width)."""
    strokes, fills = [], []
    for fn, gap in WORD:
        x += gap
        st, fl, w = fn(x, base)
        strokes += st
        fills += fl
        x += w
    return strokes, fills, x


WORD_STROKES, WORD_FILLS, WORD_W = wordmark(0, 0)  # baseline 0, top of the ascender -146


def word_svg(color):
    return (f'<path d="{" ".join(WORD_STROKES)}" fill="none" stroke="{color}" stroke-width="{f(W)}" stroke-linecap="butt"/>'
            # One element per shape: where a ring and a bar overlap, a
            # single path would cancel itself out under the fill rule.
            + "".join(f'<path d="{d}" fill="{color}"/>' for d in WORD_FILLS))


# ---------------------------------------------------------------- marks
#
# Each mark is drawn in a 100 x 100 box. mark(kind, fg, bg) returns SVG
# elements; bg is the colour behind it (for cut-outs).

def mark_a(fg, bg, small=False, hollow=False, tray=False):
    """A round core and four thin spikes, the way a bright star looks
    through a telescope; the vertical pair a little longer."""
    rv, rh, core = (47, 43, 19) if small else (46, 40, 16)
    a, b = (0.03, 0.24) if small else (0.02, 0.14)
    return (f'<path d="{star4(50, 50, rv, rh, a, b)}" fill="{fg}"/>'
            f'<circle cx="50" cy="50" r="{f(core)}" fill="{fg}"/>')


def mark_b(fg, bg, small=False, hollow=False, tray=False):
    """Two LANs overlapping, and this computer, the star, in both: the
    thing Deneb does that others do not. The circles are see-through like
    the LAN icons in the sidebar; the star is cut out of the part they
    share, so it shows whatever is behind the logo.

    small (32 and 16 px): the two circles run together into one blob there
    (it reads as an eye), so only the star is left, the one the menu bar
    and the sidebar already use."""
    if small:
        return f'<path d="{star4(50, 50, 40, 34, 0.06, 0.36)}" fill="{fg}"/>'
    d, r = 26.0, 29.0
    c1, c2 = 50 - d / 2, 50 + d / 2
    star = star4(50, 50, 19.5, 14, 0.05, 0.32)
    mask = (f'<mask id="cut-b" maskUnits="userSpaceOnUse" x="-10" y="-10" width="120" height="120">'
            f'<rect x="-10" y="-10" width="120" height="120" fill="#fff"/><path d="{star}" fill="#000"/></mask>')
    return (mask + f'<g mask="url(#cut-b)" fill="{fg}" fill-opacity="0.5">'
            f'<circle cx="{f(c1)}" cy="50" r="{f(r)}"/><circle cx="{f(c2)}" cy="50" r="{f(r)}"/></g>')


def mark_c(fg, bg, small=False, hollow=False, tray=False):
    """The wordmark's D, scaled to the box, with the star in its counter."""
    s = 74 / CAP  # cap height 74 in the box
    w = W * s * (1.25 if small else 1.0)
    x0, base = 50 - 126 * s / 2, 50 + 74 / 2
    w2 = w / 2
    sx = x0 + w2
    R = (74 - w) / 2
    xc = x0 + 126 * s - w2 - R
    d = (f"M{f(sx)},{f(base)} V{f(base - 74)} "
         f"M{f(sx)},{f(base - 74 + w2)} H{f(xc)} A{f(R)},{f(R)} 0 0 1 {f(xc)},{f(base - w2)} H{f(sx)}")
    # The star sits in the counter, a touch left of its middle so that it
    # reads as held by the D, not floating.
    inner_l, inner_r = x0 + w, x0 + 126 * s - w
    scx = (inner_l + inner_r) / 2 - 1.0
    st = 17.0 if not small else 17.5
    star = star4(scx, 50, st, st * 0.92, 0.06, 0.42)
    Dp = f'<path d="{d}" fill="none" stroke="{fg}" stroke-width="{f(w)}" stroke-linecap="butt"/>'
    if hollow:
        # Nothing can connect: the D without its star. (A star drawn as an
        # outline reads as a solid one at menu bar size.)
        return Dp
    return Dp + f'<path d="{star}" fill="{fg}"/>'


MARKS = {"a-star": mark_a, "b-two-lans": mark_b, "c-d": mark_c}
NAMES = {"a-star": "一颗亮星", "b-two-lans": "两个局域网", "c-d": "D 字"}

# How much of the app icon's squircle the 100-unit box takes, per direction.
ICON_SCALE = {"a-star": 0.62, "b-two-lans": 0.74, "c-d": 0.60}
# The mark's visible size in its 100-unit box (width, height), for lockups.
VIS = {"a-star": (80, 92), "b-two-lans": (84, 58), "c-d": (66.6, 74)}
# Its height in a lockup, in cap heights of the wordmark.
LOCK_H = {"a-star": 1.50, "b-two-lans": 1.22, "c-d": 1.18}
# Whether the mark gets a soft shadow on the app icon (see-through shapes
# would show their own shadow through them).
ICON_SHADOW = {"a-star": True, "b-two-lans": False, "c-d": True}


# ---------------------------------------------------------------- files

def svg(w, h, body, defs=""):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{f(w)}" height="{f(h)}" viewBox="0 0 {f(w)} {f(h)}">'
            + (f"<defs>{defs}</defs>" if defs else "") + body + "</svg>\n")


def app_icon(kind, small=False):
    """The macOS app icon: 1024 canvas, an 824 squircle, the mark in white.
    The squircle is the same glossy material as the device blocks. small:
    the version drawn for 16 and 32 px, with a bolder, bigger mark."""
    sq = squircle(100, 100, 824)
    defs = (
        '<linearGradient id="bg" x1="0" y1="0" x2="0" y2="1">'
        '<stop offset="0" stop-color="#5b8fff"/><stop offset="0.55" stop-color="#3a72ff"/>'
        '<stop offset="1" stop-color="#2353e6"/></linearGradient>'
        '<radialGradient id="hi" cx="0.5" cy="0.02" r="0.72">'
        '<stop offset="0" stop-color="#ffffff" stop-opacity="0.30"/>'
        '<stop offset="0.6" stop-color="#ffffff" stop-opacity="0.06"/>'
        '<stop offset="1" stop-color="#ffffff" stop-opacity="0"/></radialGradient>'
        '<filter id="sh" x="-20%" y="-20%" width="140%" height="150%">'
        '<feDropShadow dx="0" dy="14" stdDeviation="16" flood-color="#0d1f5c" flood-opacity="0.30"/></filter>'
        '<filter id="ms" x="-20%" y="-20%" width="140%" height="140%">'
        '<feDropShadow dx="0" dy="6" stdDeviation="8" flood-color="#0d2a8a" flood-opacity="0.22"/></filter>'
    )
    sc = 824 * ICON_SCALE[kind] * (1.12 if small else 1.0) / 100
    off = 512 - 50 * sc
    shadow = ' filter="url(#ms)"' if ICON_SHADOW[kind] else ""
    body = (f'<path d="{sq}" fill="url(#bg)" filter="url(#sh)"/>'
            f'<path d="{sq}" fill="url(#hi)"/>'
            f'<path d="{sq}" fill="none" stroke="#ffffff" stroke-opacity="0.16" stroke-width="3"/>'
            f'<g transform="translate({f(off)} {f(off)}) scale({f(sc)})"{shadow}>'
            + MARKS[kind]("#ffffff", BLUE, small) + "</g>")
    return svg(1024, 1024, body, defs)


def flat_mark(kind, fg, bg, size=1024, pad=0.20, small=False):
    sc = size * (1 - 2 * pad) / 100
    off = size * pad
    body = ((f'<rect width="{size}" height="{size}" fill="{bg}"/>' if bg else "") +
            f'<g transform="translate({f(off)} {f(off)}) scale({f(sc)})">' + MARKS[kind](fg, bg or "#fff", small) + "</g>")
    return svg(size, size, body)


# The client's menu bar star today (cmd/deneb/glyphs.go, trayglyph.go): a
# 24-unit box drawn at 16 pt, in the middle of the 18 pt icon.
CLIENT_STAR = ("M12 1.5c.7 5.6 2.9 7.8 9.5 10.5-6.6 2.7-8.8 4.9-9.5 10.5-.7-5.6-2.9-7.8-9.5-10.5"
               "C9.1 9.3 11.3 7.1 12 1.5z")
CLIENT_HOLLOW = ("M12 2.6c.75 5.1 2.95 7.3 8.4 9.4-5.45 2.1-7.65 4.3-8.4 9.4-.75-5.1-2.95-7.3-8.4-9.4"
                 "C9.05 9.9 11.25 7.7 12 2.6z")


def client_star(state):
    g = '<g transform="translate(2 2) scale(1.33333)">'
    if state == "hollow":
        return g + f'<path d="{CLIENT_HOLLOW}" fill="none" stroke="#000" stroke-width="1.7" stroke-linejoin="round"/></g>'
    dot = '<circle cx="20" cy="20" r="3.2" fill="#000"/>' if state == "dot" else ""
    return g + f'<path d="{CLIENT_STAR}" fill="#000"/>{dot}</g>'


# Where a direction's own mark sits in the 36 px icon: scale, and a shift
# that leaves the bottom right corner free for the dot.
TRAY_FIT = {"a-star": (32 / 92, 0), "c-d": (0.38, -2.5)}
# The dot: centre, radius, and the radius of the ring of nothing around it
# (DESIGN: the dot keeps a ring of space so that the two do not run
# together).
TRAY_DOT = {"a-star": (29.5, 29.5, 4.4, 7.0), "c-d": (31.8, 31.8, 4.0, 6.8)}


def tray(kind, state="solid"):
    """The menu bar template image: 36 px (18 pt at 2x), black on
    transparent. state: solid, dot (something wants you), hollow (nothing
    can connect).
      a-star      its own mark; hollow is the client's hollow star.
      b-two-lans  the client's three stars as they are: two circles cannot
                  be told apart at 18 pt (they read as a chain link).
      c-d         the D with the star; hollow is the D without it."""
    if kind == "b-two-lans" or (kind == "a-star" and state == "hollow"):
        return svg(36, 36, client_star(state))
    sc, dx = TRAY_FIT[kind]
    ox, oy = 18 - 50 * sc + dx, 18 - 50 * sc
    g = (f'<g transform="translate({f(ox)} {f(oy)}) scale({f(sc)})">'
         + MARKS[kind]("#000", "#fff", True, hollow=(state == "hollow"), tray=True) + "</g>")
    if state == "dot":
        cx, cy, r, gap = TRAY_DOT[kind]
        m = (f'<mask id="m"><rect width="36" height="36" fill="#fff"/>'
             f'<circle cx="{f(cx)}" cy="{f(cy)}" r="{f(gap)}" fill="#000"/></mask>')
        return svg(36, 36, f'<g mask="url(#m)">{g}</g><circle cx="{f(cx)}" cy="{f(cy)}" r="{f(r)}" fill="#000"/>', m)
    return svg(36, 36, g)


def lockup(kind, dark=False):
    """Mark and wordmark side by side: the mark centred on the cap height,
    its visible height LOCK_H cap heights, a gap of 0.4 cap heights."""
    word_c = "#ffffff" if dark else INK
    mark_c = "#ffffff" if dark else BLUE
    cap = 140.0
    unit = cap / CAP
    vw, vh = VIS[kind]
    msc = cap * LOCK_H[kind] / vh
    pad_x, pad_y = 90.0, 100.0
    gap = cap * 0.40
    ww = WORD_W * unit
    width = pad_x * 2 + vw * msc + gap + ww
    height = pad_y * 2 + max(vh * msc, ASC * unit)
    mid = height / 2
    base = mid + cap / 2
    mx = pad_x - (100 - vw) / 2 * msc
    my = mid - 50 * msc
    body = ((f'<rect width="{f(width)}" height="{f(height)}" fill="{DARK_BG if dark else "#ffffff"}"/>') +
            f'<g transform="translate({f(mx)} {f(my)}) scale({f(msc)})">' + MARKS[kind](mark_c, None) + "</g>"
            f'<g transform="translate({f(pad_x + vw * msc + gap)} {f(base)}) scale({f(unit)})">' + word_svg(word_c) + "</g>")
    return svg(width, height, body)


def write(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(text)


def main():
    for kind in MARKS:
        d = os.path.join(HERE, "batch1", kind)
        write(os.path.join(d, "app-icon.svg"), app_icon(kind))
        write(os.path.join(d, "app-icon-small.svg"), app_icon(kind, small=True))
        write(os.path.join(d, "mark-blue.svg"), flat_mark(kind, BLUE, None))
        write(os.path.join(d, "mark-white.svg"), flat_mark(kind, "#ffffff", None))
        write(os.path.join(d, "mark-on-white.svg"), flat_mark(kind, BLUE, "#ffffff"))
        write(os.path.join(d, "mark-on-dark.svg"), flat_mark(kind, "#ffffff", DARK_BG))
        write(os.path.join(d, "mark-blue-small.svg"), flat_mark(kind, BLUE, None, small=True, pad=0.04))
        write(os.path.join(d, "mark-white-small.svg"), flat_mark(kind, "#ffffff", None, small=True, pad=0.04))
        for st in ("solid", "dot", "hollow"):
            write(os.path.join(d, f"tray-{st}.svg"), tray(kind, st))
        write(os.path.join(d, "lockup.svg"), lockup(kind))
        write(os.path.join(d, "lockup-dark.svg"), lockup(kind, dark=True))
    write(os.path.join(HERE, "batch1", "wordmark.svg"),
          svg(WORD_W + 40, 200, f'<g transform="translate(20 170)">' + word_svg(INK) + "</g>"))
    print("wrote", ", ".join(MARKS))


if __name__ == "__main__":
    main()
