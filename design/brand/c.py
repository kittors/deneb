"""Deneb's mark since 2026-11-13: the little star (batch 4's C, the user's
choice: "我觉得C挺好的").

The star of the earlier logos, plump and soft, in the brand blue with the
gloss of the device tiles (DESIGN §3.4), and two eyes; nothing else. Its
vertical pair stays the longer, 43 to 36, as Deneb's star always was, so it
stands like the star it is named after rather than any star with eyes.

Three cuts:
  - the full one, 48 px and up: soft body, gloss, a rim of light, a cast
    shadow, eyes with a catch-light;
  - the small ones, each drawn on its own pixel grid: 16, 20, 24 and 32 px
    for the tray, 18 and 36 px for the menu bar, 16 and 32 px for the macOS
    icon, 16, 24 and 32 px for the Windows one; the eyes are whole pixels,
    the star centred on a pixel corner so it falls the same on both sides;
  - one-colour glyphs for the apps (MyGo) and the web, the eyes cut out.

States, the same in the menu bar and the tray: connected, eyes open; a
thing waits for you, a dot at the lower right with a gap around it; nothing
connects here, eyes closed and the star faded (asleep).
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "batch4"))
from geom import Star, f, squircle  # noqa: E402

STAR = Star.by_waist(43, 36, 9.0, 23.0)  # the full cut
STAR_S = Star.by_waist(44, 36.8, 9.5, 24.0)  # the small cuts and the glyphs

BLUE = {"hi": "#a3c0ff", "mid": "#4a80ff", "lo": "#2a5ef0", "deep": "#1a3fb0"}
EYE = "#0d1a4f"
DOT = "#ff8a00"


def svg(w, h, defs, body, extra=""):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {f(w)} {f(h)}" width="100%" height="100%"{extra}>'
            f'<defs>{"".join(defs)}</defs>{"".join(body)}</svg>\n')


# ------------------------------------------------------------- full cut

def eyes_full(cx, cy, s, shine=True, color=EYE):
    ex, ey, rx, ry = 7.8, 4.2, 3.9, 5.7
    out = []
    for sx in (-1, 1):
        x, y = cx + sx * ex * s, cy + ey * s
        out.append(f'<ellipse cx="{f(x)}" cy="{f(y)}" rx="{f(rx * s)}" ry="{f(ry * s)}" fill="{color}"/>')
        if shine:
            out.append(f'<circle cx="{f(x + 1.3 * s)}" cy="{f(y - 2.4 * s)}" r="{f(1.55 * s)}" fill="#fff"/>')
    return out


def character(cx, cy, s, uid="c", shadow=0.30):
    """The soft star with its eyes at (cx, cy), s pixels per unit."""
    d = STAR.path(cx, cy, s)
    defs = [
        f'<radialGradient id="{uid}body" gradientUnits="userSpaceOnUse" cx="{f(cx - 14 * s)}" cy="{f(cy - 18 * s)}" r="{f(60 * s)}">'
        f'<stop offset="0" stop-color="{BLUE["hi"]}"/><stop offset="0.33" stop-color="{BLUE["mid"]}"/>'
        f'<stop offset="0.7" stop-color="{BLUE["lo"]}"/><stop offset="1" stop-color="{BLUE["deep"]}"/></radialGradient>',
        # Inner shading: deeper along the lower right, light along the upper
        # left, and a faint rim of light reflected from below.
        f'<filter id="{uid}soft" x="-20%" y="-20%" width="140%" height="140%">'
        f'<feGaussianBlur in="SourceAlpha" stdDeviation="{f(2.6 * s)}" result="b"/>'
        f'<feOffset in="b" dx="{f(-1.8 * s)}" dy="{f(-2.4 * s)}" result="o"/>'
        f'<feComposite in="SourceAlpha" in2="o" operator="out" result="edge"/>'
        f'<feFlood flood-color="#0f2f9e" flood-opacity="0.42"/><feComposite in2="edge" operator="in" result="dark"/>'
        f'<feGaussianBlur in="SourceAlpha" stdDeviation="{f(1.6 * s)}" result="b2"/>'
        f'<feOffset in="b2" dx="{f(1.4 * s)}" dy="{f(1.8 * s)}" result="o2"/>'
        f'<feComposite in="SourceAlpha" in2="o2" operator="out" result="edge2"/>'
        f'<feFlood flood-color="#bcd2ff" flood-opacity="0.85"/><feComposite in2="edge2" operator="in" result="lit"/>'
        f'<feGaussianBlur in="SourceAlpha" stdDeviation="{f(1.2 * s)}" result="b3"/>'
        f'<feOffset in="b3" dx="0" dy="{f(-1.6 * s)}" result="o3"/>'
        f'<feComposite in="SourceAlpha" in2="o3" operator="out" result="edge3"/>'
        f'<feFlood flood-color="#9ab8ff" flood-opacity="0.45"/><feComposite in2="edge3" operator="in" result="bounce"/>'
        f'<feMerge><feMergeNode in="SourceGraphic"/><feMergeNode in="dark"/><feMergeNode in="lit"/><feMergeNode in="bounce"/></feMerge></filter>',
        f'<filter id="{uid}blur" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="{f(2.4 * s)}"/></filter>',
        f'<filter id="{uid}shadow" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="{f(4.2 * s)}"/></filter>',
        f'<filter id="{uid}shadowT" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="{f(1.3 * s)}"/></filter>',
        f'<clipPath id="{uid}clip"><path d="{d}"/></clipPath>',
    ]
    body = []
    if shadow:
        body.append(f'<path d="{d}" fill="#0e2266" opacity="{shadow * 0.55:.2f}" filter="url(#{uid}shadowT)" '
                    f'transform="translate(0,{f(2.0 * s)})"/>')
        body.append(f'<path d="{d}" fill="#14307f" opacity="{shadow * 0.8:.2f}" filter="url(#{uid}shadow)" '
                    f'transform="translate(0,{f(9 * s)}) scale(0.8 0.62)" transform-origin="{f(cx)} {f(cy)}"/>')
    body.append(f'<path d="{d}" fill="url(#{uid}body)" filter="url(#{uid}soft)"/>')
    body.append(f'<g clip-path="url(#{uid}clip)">'
                f'<ellipse cx="{f(cx - 10 * s)}" cy="{f(cy - 12 * s)}" rx="{f(9 * s)}" ry="{f(12 * s)}" fill="#fff" opacity="0.38" '
                f'filter="url(#{uid}blur)" transform="rotate(40 {f(cx - 10 * s)} {f(cy - 12 * s)})"/>'
                f'<ellipse cx="{f(cx - 15 * s)}" cy="{f(cy - 9.5 * s)}" rx="{f(1.7 * s)}" ry="{f(4.4 * s)}" fill="#fff" opacity="0.85" '
                f'transform="rotate(48 {f(cx - 15 * s)} {f(cy - 9.5 * s)})"/></g>')
    body += eyes_full(cx, cy, s)
    return defs, body


def tile(x, y, size, outline, shadow, uid="c"):
    u = size / 824
    defs = [f'<linearGradient id="{uid}bg" x1="0" y1="{f(y)}" x2="0" y2="{f(y + size)}" gradientUnits="userSpaceOnUse">'
            f'<stop offset="0" stop-color="#f8faff"/><stop offset="1" stop-color="#dfe8ff"/></linearGradient>']
    if shadow:
        defs.append(f'<filter id="{uid}tsh" x="-20%" y="-20%" width="140%" height="150%">'
                    f'<feDropShadow dx="0" dy="{f(12 * u)}" stdDeviation="{f(14 * u)}" flood-color="#1a2a66" flood-opacity="0.22"/></filter>')
    body = [f'<path d="{outline}" fill="url(#{uid}bg)"' + (f' filter="url(#{uid}tsh)"' if shadow else "") + "/>"]
    return defs, body


def app_icon_macos():
    """1024 canvas, the 824 squircle (Apple's grid), its shadow."""
    d1, b1 = tile(100, 100, 824, squircle(100, 100, 824), True)
    d2, b2 = character(512, 500, 824 * 0.80 / 100)
    return svg(1024, 1024, d1 + d2, b1 + b2)


def app_icon_windows():
    """No tile, as Windows 11's own icons: the star itself."""
    d2, b2 = character(512, 500, 1024 * 0.98 / 100, shadow=0.16)
    return svg(1024, 1024, d2, b2)


# ------------------------------------------------------------ small cuts
#
# Each size its own grid. The star is centred on a pixel corner, so both
# halves fall alike; eyes and dot are given in whole pixels.

# px: (star half-height in px, eye w, eye h, gap between eyes, eye top, eye corner,
#      dot centre, dot radius, dot gap, closed-eye thickness)
GRID = {
    16: (7.8, 2, 3, 2, 7, 0, 13.0, 2.5, 1.0, 1),
    18: (8.2, 2, 3, 2, 8, 0, 15.0, 2.5, 1.0, 1),
    20: (9.75, 2, 3, 2, 9, 0, 16.5, 3.0, 1.2, 1),
    24: (11.7, 3, 4, 2, 11, 0.5, 20.0, 3.5, 1.3, 1.5),
    32: (15.6, 4, 6, 4, 14, 2, 26.5, 4.8, 1.8, 2),
    36: (16.4, 4, 6, 4, 16, 2, 30.0, 5.0, 2.0, 2),
}


def _eye_rects(px, closed=False, r=None):
    """The two eyes of size px as SVG path data (clockwise rectangles with
    rounded corners, r overriding the grid's), or the closed lids."""
    _, w, h, gap, top, gr, *_ , lid = GRID[px]
    r = gr if r is None else r
    c = px / 2
    out = []
    for x in (c - gap / 2 - w, c + gap / 2):
        if closed:
            # A lid: a flat bar across the lower part of the eye, as wide as
            # the eye, rounded at the ends from 24 px up.
            y = top + h - lid - (1 if h >= 4 else 0)
            rr = min(lid / 2, w / 2) if px >= 24 else 0
            out.append(_rrect_d(x - (0.5 if px >= 24 else 0), y, w + (1 if px >= 24 else 0), lid, rr))
        else:
            out.append(_rrect_d(x, top, w, h, r))
    return out


def _rrect_d(x, y, w, h, r):
    if r <= 0:
        return f"M{f(x)},{f(y)}H{f(x + w)}V{f(y + h)}H{f(x)}Z"
    r = min(r, w / 2, h / 2)
    return (f"M{f(x + r)},{f(y)}H{f(x + w - r)}A{f(r)},{f(r)} 0 0 1 {f(x + w)},{f(y + r)}"
            f"V{f(y + h - r)}A{f(r)},{f(r)} 0 0 1 {f(x + w - r)},{f(y + h)}"
            f"H{f(x + r)}A{f(r)},{f(r)} 0 0 1 {f(x)},{f(y + h - r)}"
            f"V{f(y + r)}A{f(r)},{f(r)} 0 0 1 {f(x + r)},{f(y)}Z")


def star_small_d(px):
    hh = GRID[px][0]
    return STAR_S.path(px / 2, px / 2, hh / STAR_S.tv)


def _mark(px, fill, state, uid, dot_color, grad=None, opacity=1.0):
    """The star of size px: body in fill (or the gradient grad, a pair of
    colours top to bottom), its eyes cut out (closed when state is hollow),
    with the dot when state is dot. Returns (defs, body)."""
    star = star_small_d(px)
    eyes = _eye_rects(px, closed=(state == "hollow"))
    _, _, _, _, _, _, dc, dr, dg, _ = GRID[px]
    defs, body = [], []
    paint = fill
    if grad:
        defs.append(f'<linearGradient id="{uid}g" x1="0" y1="0" x2="0" y2="{px}" gradientUnits="userSpaceOnUse">'
                    f'<stop offset="0.1" stop-color="{grad[0]}"/><stop offset="0.9" stop-color="{grad[1]}"/></linearGradient>')
        paint = f"url(#{uid}g)"
    shape = f'<path d="{star}{"".join(eyes)}" fill-rule="evenodd" fill="{paint}" fill-opacity="{opacity}"/>'
    if state == "dot":
        defs.append(f'<mask id="{uid}m" maskUnits="userSpaceOnUse" x="0" y="0" width="{px}" height="{px}">'
                    f'<rect width="{px}" height="{px}" fill="#fff"/><circle cx="{f(dc)}" cy="{f(dc)}" r="{f(dr + dg)}" fill="#000"/></mask>')
        body.append(f'<g mask="url(#{uid}m)">{shape}</g>')
        body.append(f'<circle cx="{f(dc)}" cy="{f(dc)}" r="{f(dr)}" fill="{dot_color}"/>')
    else:
        body.append(shape)
    return defs, body


def menubar_svg(state, px):
    """The menu bar's template, px 18 (1x) or 36 (2x): black and clear;
    asleep, the star at half its alpha."""
    d, b = _mark(px, "#000", state, "m", "#000", opacity=0.5 if state == "hollow" else 1.0)
    return svg(px, px, d, b)


TRAY = {
    # dark taskbar / light taskbar: the star's gradient, top to bottom
    (True, "on"): ("#8fb3ff", "#4d80ff"),
    (False, "on"): ("#4f84ff", "#2152e0"),
    (True, "off"): ("#b9bec8", "#9ba1ac"),
    (False, "off"): ("#858d9b", "#626a78"),
}


def tray_svg(state, dark, px):
    """Windows' and Linux's tray icon, in colour: the star's eyes are cut
    out, so the taskbar shows through them (dark eyes on a dark taskbar,
    light on a light one)."""
    grad = TRAY[(dark, "off" if state == "hollow" else "on")]
    d, b = _mark(px, None, state, "t", DOT, grad=grad)
    return svg(px, px, d, b)


def app_icon_small_macos(px):
    """The macOS icon at 16 or 32 px: the light tile, the star larger and
    flat but for a little light, dark eyes on whole pixels."""
    d1, b1 = tile(100, 100, 824, squircle(100, 100, 824), True)
    k = 1024 / px  # canvas units per pixel
    hh = {16: 5.9, 32: 11.8}[px]  # the star's half-height, px
    cx = cy = 512
    s = hh * k / STAR_S.tv
    d = STAR_S.path(cx, cy, s)
    d1.append(f'<linearGradient id="sb" x1="0" y1="{f(cy - hh * k)}" x2="0" y2="{f(cy + hh * k)}" gradientUnits="userSpaceOnUse">'
              f'<stop offset="0" stop-color="#5f90ff"/><stop offset="1" stop-color="#2352d8"/></linearGradient>')
    b1.append(f'<path d="{d}" fill="url(#sb)"/>')
    # Eyes: 16 px: 1 x 2 at columns 6 and 9, rows 7-8; 32 px: 2 x 3 at
    # columns 13-14 and 17-18, rows 15-17.
    ew, eh, xs, top = {16: (1, 2, (6, 9), 7), 32: (2, 3, (13, 17), 15)}[px]
    for x in xs:
        b1.append(f'<rect x="{f(x * k)}" y="{f(top * k)}" width="{f(ew * k)}" height="{f(eh * k)}" fill="{EYE}"/>')
    return svg(1024, 1024, d1, b1)


def app_icon_small_windows(px):
    """Windows' 16, 24 and 32 px: the tray's star (light-taskbar colours)
    with dark eyes, on its own grid."""
    star = star_small_d(px)
    eyes = _eye_rects(px)
    defs = [f'<linearGradient id="g" x1="0" y1="0" x2="0" y2="{px}" gradientUnits="userSpaceOnUse">'
            f'<stop offset="0.1" stop-color="#5b8cff"/><stop offset="0.9" stop-color="#2352d8"/></linearGradient>']
    body = [f'<path d="{star}" fill="url(#g)"/>', f'<path d="{"".join(eyes)}" fill="{EYE}"/>']
    return svg(px, px, defs, body)


# --------------------------------------------------------------- glyphs
#
# One colour, for MyGo's Icon (the text colour) and the web's currentColor.

def glyph_small_d(closed=False):
    """The 18-unit glyph's path data: the star and its eyes, evenodd. Its
    eyes fall on whole points, rounded into capsules, so that at 18 points
    on a Retina display they are the menu bar's, and larger (the 40-point
    onboarding star) they read as eyes, not boxes."""
    return star_small_d(18) + "".join(_eye_rects(18, closed=closed, r=1.0))


def glyph_svg(fill="#000"):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 18 18">'
            f'<path fill="{fill}" fill-rule="evenodd" d="{glyph_small_d()}"/></svg>')


def tray_glyph_svgs():
    """The menu bar's three states as one-colour 18-unit SVGs for MyGo
    (trayGlyph, drawn in the text colour: the tray tip's star)."""
    _, _, _, _, _, _, dc, dr, dg, _ = GRID[18]
    solid = glyph_svg()
    dot = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 18 18"><defs><mask id="m" maskUnits="userSpaceOnUse" '
           f'x="0" y="0" width="18" height="18"><rect width="18" height="18" fill="#fff"/>'
           f'<circle cx="{f(dc)}" cy="{f(dc)}" r="{f(dr + dg)}" fill="#000"/></mask></defs>'
           f'<path fill="#000" fill-rule="evenodd" mask="url(#m)" d="{glyph_small_d()}"/>'
           f'<circle fill="#000" cx="{f(dc)}" cy="{f(dc)}" r="{f(dr)}"/></svg>')
    hollow = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 18 18">'
              f'<path fill="#000" fill-opacity="0.5" fill-rule="evenodd" d="{glyph_small_d(closed=True)}"/></svg>')
    return {"solid": solid, "dot": dot, "hollow": hollow}


def _ellipse_d(cx, cy, rx, ry):
    return (f"M{f(cx - rx)},{f(cy)}A{f(rx)},{f(ry)} 0 1 0 {f(cx + rx)},{f(cy)}"
            f"A{f(rx)},{f(ry)} 0 1 0 {f(cx - rx)},{f(cy)}Z")


def glyph_full_d():
    """The full cut as one colour, 100 units: the star and its eyes cut out."""
    s = 0.98 * 50 / STAR.tv
    d = STAR.path(50, 50, s)
    ex, ey, rx, ry = 7.8, 4.2, 3.9, 5.7
    for sx in (-1, 1):
        d += _ellipse_d(50 + sx * ex * s, 50 + ey * s, rx * s, ry * s)
    return d


def glyph_full_svg(fill="#000"):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">'
            f'<path fill="{fill}" fill-rule="evenodd" d="{glyph_full_d()}"/></svg>')


def favicon_svg():
    """The browser tab's icon: the 16 px tray star in colour, lighter in a
    dark browser; the eyes show the tab through them."""
    star = star_small_d(16) + "".join(_eye_rects(16))
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16">'
            f'<style>.a{{stop-color:#4f84ff}}.b{{stop-color:#2152e0}}'
            f'@media (prefers-color-scheme:dark){{.a{{stop-color:#8fb3ff}}.b{{stop-color:#4d80ff}}}}</style>'
            f'<linearGradient id="g" x1="0" y1="0" x2="0" y2="16" gradientUnits="userSpaceOnUse">'
            f'<stop offset="0.1" class="a"/><stop offset="0.9" class="b"/></linearGradient>'
            f'<path fill="url(#g)" fill-rule="evenodd" d="{star}"/></svg>')


def apple_touch_svg():
    """iOS home screen, 180 px: the tile filling the square (iOS rounds it)."""
    d1 = [f'<linearGradient id="cbg" x1="0" y1="0" x2="0" y2="1024" gradientUnits="userSpaceOnUse">'
          f'<stop offset="0" stop-color="#f8faff"/><stop offset="1" stop-color="#dfe8ff"/></linearGradient>']
    b1 = ['<rect width="1024" height="1024" fill="url(#cbg)"/>']
    d2, b2 = character(512, 500, 1024 * 0.80 / 100, shadow=0.24)
    return svg(1024, 1024, d1 + d2, b1 + b2)
