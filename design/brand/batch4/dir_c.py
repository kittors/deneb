"""Direction C, 小星: the star as a little character.

The same star, plump and soft, in the brand blue with the gloss of the
device tiles' coloured balls (DESIGN §3.4), and two eyes. Nothing else: no
mouth, no cheeks. The eyes carry the states in the menu bar: open when
connected, closed when nothing connects here ("it's asleep").
"""
from geom import Star, f, squircle, rrect

STAR = Star.by_waist(41, 38, 9.0, 23.5)
STAR_SMALL = Star.by_waist(44, 39, 9.0, 24.0)

BLUE = {"hi": "#a3c0ff", "mid": "#4a80ff", "lo": "#2a5ef0", "deep": "#1a3fb0"}
EYE = "#0d1a4f"


def eyes(cx, cy, s, closed=False, color=EYE, shine=True, uid="c"):
    """Two eyes, centred a little below the star's centre."""
    ex, ey, rx, ry = 8.2, 4.2, 4.0, 5.8
    out = []
    for sx in (-1, 1):
        x, y = cx + sx * ex * s, cy + ey * s
        if closed:
            w = rx * 1.25 * s
            out.append(f'<path d="M{f(x - w)},{f(y)} Q{f(x)},{f(y + 3.4 * s)} {f(x + w)},{f(y)}" fill="none" '
                       f'stroke="{color}" stroke-width="{f(2.0 * s)}" stroke-linecap="round"/>')
        else:
            out.append(f'<ellipse cx="{f(x)}" cy="{f(y)}" rx="{f(rx * s)}" ry="{f(ry * s)}" fill="{color}"/>')
            if shine:
                out.append(f'<circle cx="{f(x + 1.3 * s)}" cy="{f(y - 2.5 * s)}" r="{f(1.6 * s)}" fill="#fff"/>')
    return out


def character(cx, cy, s, star=STAR, uid="c", detail=True, closed=False, shadow=0.30):
    """The soft blue star with its eyes at (cx, cy), s pixels per unit."""
    d = star.path(cx, cy, s)
    defs = [
        f'<radialGradient id="{uid}body" gradientUnits="userSpaceOnUse" cx="{f(cx - 14 * s)}" cy="{f(cy - 18 * s)}" r="{f(60 * s)}">'
        f'<stop offset="0" stop-color="{BLUE["hi"]}"/><stop offset="0.33" stop-color="{BLUE["mid"]}"/>'
        f'<stop offset="0.7" stop-color="{BLUE["lo"]}"/><stop offset="1" stop-color="{BLUE["deep"]}"/></radialGradient>',
        # Inner shading: darker along the lower right edge, a rim of light
        # along the lower left, as a soft glossy body has.
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
    if detail and shadow:
        # A tight contact shadow, and a soft one cast below.
        body.append(f'<path d="{d}" fill="#0e2266" opacity="{shadow * 0.55:.2f}" filter="url(#{uid}shadowT)" '
                    f'transform="translate(0,{f(2.0 * s)})"/>')
        body.append(f'<path d="{d}" fill="#14307f" opacity="{shadow * 0.8:.2f}" filter="url(#{uid}shadow)" '
                    f'transform="translate(0,{f(9 * s)}) scale(0.8 0.62)" transform-origin="{f(cx)} {f(cy)}"/>')
    body.append(f'<path d="{d}" fill="url(#{uid}body)" filter="url(#{uid}soft)"/>')
    if detail:
        # The gloss: a soft patch on the upper-left tip and a crisp streak.
        body.append(f'<g clip-path="url(#{uid}clip)">'
                    f'<ellipse cx="{f(cx - 10 * s)}" cy="{f(cy - 12 * s)}" rx="{f(9 * s)}" ry="{f(12 * s)}" fill="#fff" opacity="0.38" '
                    f'filter="url(#{uid}blur)" transform="rotate(40 {f(cx - 10 * s)} {f(cy - 12 * s)})"/>'
                    f'<ellipse cx="{f(cx - 16 * s)}" cy="{f(cy - 9 * s)}" rx="{f(1.7 * s)}" ry="{f(4.4 * s)}" fill="#fff" opacity="0.85" '
                    f'transform="rotate(48 {f(cx - 16 * s)} {f(cy - 9 * s)})"/></g>')
    body += eyes(cx, cy, s, closed=closed, shine=detail)
    return defs, body


def svg(w, h, defs, body):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {f(w)} {f(h)}" width="100%" height="100%">'
            f'<defs>{"".join(defs)}</defs>{"".join(body)}</svg>\n')


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
    d1, b1 = tile(100, 100, 824, squircle(100, 100, 824), True)
    d2, b2 = character(512, 500, 824 * 0.80 / 100)
    return svg(1024, 1024, d1 + d2, b1 + b2)


def app_icon_windows():
    d2, b2 = character(512, 500, 1024 * 0.98 / 100, shadow=0.16)
    return svg(1024, 1024, d2, b2)


# ------------------------------------------------------------ menu bar, tray
#
# The menu bar: an 18 pt template, the star solid, its eyes cut out of it,
# on whole pixels at 1x and 2x. "dot": the dot at the lower right; "hollow"
# (nothing connects here): eyes closed, the star faded - asleep.
#
# The tray: the blue star with its eyes cut out, so the taskbar shows
# through them, dark eyes on a dark taskbar and light ones on a light one;
# grey and asleep when nothing connects.

MB_STAR = Star.by_waist(44, 40, 10.0, 25.0)
DOT = "#ff8a00"


def _eyes_cut(cx, cy, dx, ey, w, h, closed):
    """Eye holes for a mask (black on white): ellipses, or closed lids."""
    out = []
    for sx in (-1, 1):
        x = cx + sx * dx
        if closed:
            out.append(f'<path d="M{f(x - w * 0.5)},{f(ey)} Q{f(x)},{f(ey + h * 0.55)} {f(x + w * 0.5)},{f(ey)}" '
                       f'fill="none" stroke="#000" stroke-width="{f(h * 0.3)}" stroke-linecap="round"/>')
        else:
            out.append(f'<ellipse cx="{f(x)}" cy="{f(ey)}" rx="{f(w / 2)}" ry="{f(h / 2)}" fill="#000"/>')
    return out


def _mark(S, c, s, fill, eyes, dot=None, opacity=1.0, uid="k"):
    d = MB_STAR.path(c, c, s)
    m = [f'<mask id="{uid}" maskUnits="userSpaceOnUse" x="0" y="0" width="{S}" height="{S}">'
         f'<rect width="{S}" height="{S}" fill="#fff"/>']
    m += eyes
    if dot:
        m.append(f'<circle cx="{f(dot[0])}" cy="{f(dot[0])}" r="{f(dot[1] + dot[2])}" fill="#000"/>')
    m.append("</mask>")
    body = [f'<path d="{d}" fill="{fill}" fill-opacity="{opacity}" mask="url(#{uid})"/>']
    if dot:
        body.append(f'<circle cx="{f(dot[0])}" cy="{f(dot[0])}" r="{f(dot[1])}" fill="{dot[3]}"/>')
    return m, body


def menubar_svg(state):
    S, c = 18, 9
    s = 8.1 / MB_STAR.tv
    # Eyes 2 x 3 px at 1x, on whole pixels: centres at x 6 and 12, y 10.5.
    eyes = _eyes_cut(c, c, 2.0, 10.5, 2.0, 3.0, closed=(state == "hollow"))
    dot = (15.1, 2.5, 1.1, "#000") if state == "dot" else None
    defs, body = _mark(S, c, s, "#000", eyes, dot, 0.5 if state == "hollow" else 1.0)
    return svg(S, S, defs, body)


def tray_svg(state, dark):
    S, c = 16, 8
    s = 7.8 / MB_STAR.tv
    eyes = _eyes_cut(c, c, 2.0, 8.5, 2.0, 3.0, closed=(state == "hollow"))
    if state == "hollow":
        fill = "#a3a8b3" if dark else "#6b7280"
    else:
        fill = "#5b8cff" if dark else "#2f6bff"
    dot = (13.3, 2.4, 1.0, DOT) if state == "dot" else None
    defs, body = _mark(S, c, s, fill, eyes, dot)
    return svg(S, S, defs, body)


# ------------------------------------------------------------ small app icons

def app_icon_macos_small():
    """16 and 32 px: the light tile, the menu bar's plumper star larger,
    flat blue with a little light at the upper left, dark eyes."""
    d1, b1 = tile(100, 100, 824, squircle(100, 100, 824), True)
    s = 824 * 0.86 / 100 * 50 / MB_STAR.tv
    cx, cy = 512, 512
    d = MB_STAR.path(cx, cy, s)
    d1.append(f'<radialGradient id="sb" gradientUnits="userSpaceOnUse" cx="{f(cx - 160)}" cy="{f(cy - 200)}" r="{f(560)}">'
              f'<stop offset="0" stop-color="#6f9bff"/><stop offset="0.5" stop-color="#3a72ff"/><stop offset="1" stop-color="#2352d8"/></radialGradient>')
    b1.append(f'<path d="{d}" fill="url(#sb)"/>')
    # Eyes: 1 x 2 px at 16 px (a 64-unit pixel), on whole pixels at 16
    # and at 32 (2 x 4 there): columns 6 and 9 of 16, rows 8 and 9.
    for x in (384, 576):
        b1.append(f'<rect x="{x}" y="512" width="64" height="128" rx="32" fill="{EYE}"/>')
    return svg(1024, 1024, d1, b1)


def app_icon_windows_small():
    """Windows' 16 to 32 px: the tray's star with dark eyes, no tile."""
    s = 1024 * 0.985 / 100 * 50 / MB_STAR.tv
    cx, cy = 512, 512
    d = MB_STAR.path(cx, cy, s)
    defs = [f'<radialGradient id="sb" gradientUnits="userSpaceOnUse" cx="{f(cx - 180)}" cy="{f(cy - 220)}" r="{f(640)}">'
            f'<stop offset="0" stop-color="#6f9bff"/><stop offset="0.5" stop-color="#3a72ff"/><stop offset="1" stop-color="#2352d8"/></radialGradient>']
    body = [f'<path d="{d}" fill="url(#sb)"/>']
    for x in (384, 576):
        body.append(f'<rect x="{x}" y="512" width="64" height="128" rx="32" fill="{EYE}"/>')
    return svg(1024, 1024, defs, body)
