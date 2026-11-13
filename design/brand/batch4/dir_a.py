"""Direction A, 晶: the star cut as a crystal.

The star stands as a low pyramid: eight facets, each tip split along its
ridge into a lit half and a shaded half, lit from the upper left. White and
ice-blue on the lit side, the brand blue on the shaded side, a faint
spectrum where light leaves the glass at the tips, and the bright point at
the centre where the facets meet. Behind it a night sky, lit around the
star. The facets carry the shape at every size: even at 16 px the star
reads as two tones, and on a template image as two alphas.
"""
from geom import Star, StarV, f, mix, pt, squircle, rrect

# The star in 100 units: tips 46 up and down, 37 across (Deneb's ratio),
# nearly sharp tips, the waist at 13.
STAR = StarV(46, 36, 0.7, 16.0)
# The small cut (16-32 px): a fuller waist, so each half-tip keeps a pixel
# or two of its tone.
STAR_SMALL = Star.by_waist(46, 38, 1.2, 16.5)

LIGHT = (-0.55, -0.75, 0.62)  # from the upper left, a little above
HEIGHT = 11.0  # how high the centre stands above the tips, in star units

# Night sky, top to bottom, and the glow the star throws on it.
SKY = [("0", "#0a1a5e"), ("0.6", "#14339c"), ("1", "#1f4fd6")]
GLOW = "#3d6dff"

# Facet tones from the most shaded to the most lit: (at the centre, at the tip).
TONES = [
    (0.00, "#4c7cff", "#1f45c8"),
    (0.30, "#86a9ff", "#3a66e8"),
    (0.70, "#e8f0ff", "#a6c2ff"),
    (1.00, "#ffffff", "#dce8ff"),
]
# The spectrum at the tips, one hue per tip (top, right, bottom, left).
SPECTRUM = ["#7fe3ff", "#ff9ad5", "#b69bff", "#8ff0e0"]


def _brightness(star, i, side):
    n = star.facet_normal(i, side, HEIGHT)
    L = LIGHT
    ln = sum(a * b for a, b in zip(n, L)) / (sum(c * c for c in L) ** 0.5)
    return ln


def _tone(b, lo, hi):
    """The (centre, tip) colours for brightness b, b in [lo, hi] mapped to
    the TONES scale."""
    t = (b - lo) / (hi - lo) if hi > lo else 0.5
    t = max(0.0, min(1.0, t))
    for k in range(len(TONES) - 1):
        a, b2 = TONES[k], TONES[k + 1]
        if t <= b2[0]:
            u = (t - a[0]) / (b2[0] - a[0])
            return mix(a[1], b2[1], u), mix(a[2], b2[2], u)
    return TONES[-1][1], TONES[-1][2]


def star_layers(cx, cy, s, star=STAR, uid="a", detail=True, glow=True, spikes=0.0):
    """The crystal star at (cx, cy), s pixels per unit. Returns (defs, body).
    spikes: the opacity of the faint diffraction spikes beyond the tips."""
    defs, body = [], []
    if spikes:
        for (ax, ay) in ((0, star.tv), (star.th, 0)):
            gid = f"{uid}spk{ax}"
            L = 1.55
            defs.append(f'<linearGradient id="{gid}" gradientUnits="userSpaceOnUse" x1="{f(cx - ax * s * L)}" y1="{f(cy - ay * s * L)}" '
                        f'x2="{f(cx + ax * s * L)}" y2="{f(cy + ay * s * L)}">'
                        f'<stop offset="0" stop-color="#cfe0ff" stop-opacity="0"/>'
                        f'<stop offset="0.5" stop-color="#cfe0ff" stop-opacity="{spikes}"/>'
                        f'<stop offset="1" stop-color="#cfe0ff" stop-opacity="0"/></linearGradient>')
            body.append(f'<path d="M{f(cx - ax * s * L)},{f(cy - ay * s * L)}L{f(cx + ax * s * L)},{f(cy + ay * s * L)}" '
                        f'stroke="url(#{gid})" stroke-width="{f(0.45 * s)}" fill="none"/>')
    bs = [(i, side, _brightness(star, i, side)) for i in range(4) for side in (-1, 1)]
    lo, hi = min(b for *_, b in bs), max(b for *_, b in bs)
    outline = star.path(cx, cy, s)
    if glow:
        defs.append(f'<filter id="{uid}bloom" x="-60%" y="-60%" width="220%" height="220%">'
                    f'<feGaussianBlur stdDeviation="{f(9 * s)}"/></filter>')
        body.append(f'<path d="{outline}" fill="#5c8bff" opacity="0.55" filter="url(#{uid}bloom)"/>')
    for i, side, b in bs:
        c0, c1 = _tone(b, lo, hi)
        ax, ay = star.apex[i]
        gid = f"{uid}f{i}{'p' if side > 0 else 'm'}"
        defs.append(f'<linearGradient id="{gid}" gradientUnits="userSpaceOnUse" x1="{f(cx)}" y1="{f(cy)}" '
                    f'x2="{f(cx + ax * s)}" y2="{f(cy + ay * s)}">'
                    f'<stop offset="0" stop-color="{c0}"/><stop offset="1" stop-color="{c1}"/></linearGradient>')
        t = (b - lo) / (hi - lo) if hi > lo else 1
        op = 1
        body.append(f'<path d="{star.facet(i, side, cx, cy, s)}" fill="url(#{gid})" fill-opacity="{op:.2f}"/>')
    if detail:
        # The spectrum where light leaves the glass, on the tips' outer half.
        for i in range(4):
            ax, ay = star.apex[i]
            gid = f"{uid}sp{i}"
            defs.append(f'<linearGradient id="{gid}" gradientUnits="userSpaceOnUse" x1="{f(cx + ax * s * 0.35)}" '
                        f'y1="{f(cy + ay * s * 0.35)}" x2="{f(cx + ax * s)}" y2="{f(cy + ay * s)}">'
                        f'<stop offset="0" stop-color="{SPECTRUM[i]}" stop-opacity="0"/>'
                        f'<stop offset="1" stop-color="{SPECTRUM[i]}" stop-opacity="0.55"/></linearGradient>')
            for side in (-1, 1):
                body.append(f'<path d="{star.facet(i, side, cx, cy, s)}" fill="url(#{gid})" '
                            f'style="mix-blend-mode:soft-light"/>')
        # Ridges: a fine bright line from the centre out along each tip,
        # fading before the tip.
        for i in range(4):
            ax, ay = star.apex[i]
            gid = f"{uid}rg{i}"
            defs.append(f'<linearGradient id="{gid}" gradientUnits="userSpaceOnUse" x1="{f(cx)}" y1="{f(cy)}" '
                        f'x2="{f(cx + ax * s)}" y2="{f(cy + ay * s)}">'
                        f'<stop offset="0" stop-color="#fff" stop-opacity="0.9"/>'
                        f'<stop offset="1" stop-color="#fff" stop-opacity="0"/></linearGradient>')
            body.append(f'<path d="M{f(cx)},{f(cy)}L{f(cx + ax * s)},{f(cy + ay * s)}" stroke="url(#{gid})" '
                        f'stroke-width="{f(0.35 * s)}" fill="none"/>')
        # Dispersion: a cyan and a pink thread either side of each ridge,
        # as light splits leaving the glass.
        for i in range(4):
            ax, ay = star.apex[i]
            L = (ax * ax + ay * ay) ** 0.5
            nx, ny = -ay / L, ax / L
            for col, sg in (("#7ff0ff", 1), ("#ff8fd0", -1)):
                o = 0.55 * s * sg
                gid = f"{uid}dp{i}{sg}"
                defs.append(f'<linearGradient id="{gid}" gradientUnits="userSpaceOnUse" x1="{f(cx)}" y1="{f(cy)}" '
                            f'x2="{f(cx + ax * s)}" y2="{f(cy + ay * s)}">'
                            f'<stop offset="0.12" stop-color="{col}" stop-opacity="0"/>'
                            f'<stop offset="0.45" stop-color="{col}" stop-opacity="0.75"/>'
                            f'<stop offset="0.9" stop-color="{col}" stop-opacity="0"/></linearGradient>')
                body.append(f'<path d="M{f(cx + nx * o)},{f(cy + ny * o)}L{f(cx + ax * s + nx * o * 0.2)},{f(cy + ay * s + ny * o * 0.2)}" '
                            f'stroke="url(#{gid})" stroke-width="{f(0.32 * s)}" fill="none"/>')
        # The rim: a hairline of light around the outline.
        body.append(f'<path d="{outline}" fill="none" stroke="#fff" stroke-opacity="0.5" '
                    f'stroke-width="{f(0.3 * s)}"/>')
        # The bright point where the facets meet.
        defs.append(f'<radialGradient id="{uid}core" gradientUnits="userSpaceOnUse" cx="{f(cx)}" cy="{f(cy)}" '
                    f'r="{f(7.5 * s)}"><stop offset="0" stop-color="#fff" stop-opacity="1"/>'
                    f'<stop offset="0.2" stop-color="#fff" stop-opacity="0.8"/>'
                    f'<stop offset="1" stop-color="#fff" stop-opacity="0"/></radialGradient>')
        body.append(f'<circle cx="{f(cx)}" cy="{f(cy)}" r="{f(7.5 * s)}" fill="url(#{uid}core)"/>')
        for (lx, ly) in ((0, 15), (12, 0)):
            body.append(f'<path d="M{f(cx - lx * s)},{f(cy - ly * s)}L{f(cx + lx * s)},{f(cy + ly * s)}" stroke="#fff" '
                        f'stroke-width="{f(0.28 * s)}" stroke-linecap="round" opacity="0.85"/>')
    return defs, body


def night(x, y, size, outline, uid="a", shadow=False, star=STAR, scale=0.72, detail=True, spikes=0.0):
    """The night-sky tile with the crystal star, in the square (x, y, size)."""
    cx, cy = x + size / 2, y + size / 2
    s = size * scale / 100
    defs = [
        f'<clipPath id="{uid}tile"><path d="{outline}"/></clipPath>',
        f'<linearGradient id="{uid}sky" x1="0" y1="{f(y)}" x2="0" y2="{f(y + size)}" gradientUnits="userSpaceOnUse">'
        + "".join(f'<stop offset="{o}" stop-color="{c}"/>' for o, c in SKY) + "</linearGradient>",
        f'<radialGradient id="{uid}lit" cx="{f(cx)}" cy="{f(cy)}" r="{f(size * 0.55)}" gradientUnits="userSpaceOnUse">'
        f'<stop offset="0" stop-color="{GLOW}" stop-opacity="0.75"/>'
        f'<stop offset="0.45" stop-color="{GLOW}" stop-opacity="0.28"/>'
        f'<stop offset="1" stop-color="{GLOW}" stop-opacity="0"/></radialGradient>',
        f'<linearGradient id="{uid}sheen" x1="0" y1="{f(y)}" x2="0" y2="{f(y + size)}" gradientUnits="userSpaceOnUse">'
        f'<stop offset="0" stop-color="#fff" stop-opacity="0.05"/><stop offset="0.35" stop-color="#fff" stop-opacity="0"/>'
        f'</linearGradient>',
    ]
    if shadow:
        u = size / 824
        defs.append(f'<filter id="{uid}shadow" x="-20%" y="-20%" width="140%" height="150%">'
                    f'<feDropShadow dx="0" dy="{f(12 * u)}" stdDeviation="{f(14 * u)}" flood-color="#050b2a" '
                    f'flood-opacity="0.35"/></filter>')
    body = [f'<path d="{outline}" fill="url(#{uid}sky)"' + (f' filter="url(#{uid}shadow)"' if shadow else "") + "/>",
            f'<g clip-path="url(#{uid}tile)">',
            f'<rect x="{f(x)}" y="{f(y)}" width="{f(size)}" height="{f(size)}" fill="url(#{uid}lit)"/>',
            f'<rect x="{f(x)}" y="{f(y)}" width="{f(size)}" height="{f(size)}" fill="url(#{uid}sheen)"/>']
    d2, b2 = star_layers(cx, cy, s, star, uid, detail=detail, spikes=spikes)
    defs += d2
    body += b2
    body.append("</g>")
    return defs, body


def svg(w, h, defs, body):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {f(w)} {f(h)}" width="100%" height="100%">'
            f'<defs>{"".join(defs)}</defs>{"".join(body)}</svg>\n')


def app_icon_macos():
    d, b = night(100, 100, 824, squircle(100, 100, 824), shadow=True)
    return svg(1024, 1024, d, b)


def app_icon_windows():
    m = 1024 * 0.03
    size = 1024 - 2 * m
    d, b = night(m, m, size, rrect(m, m, size, size, size * 0.22))
    return svg(1024, 1024, d, b)


# ------------------------------------------------------------ menu bar, tray
#
# The menu bar: an 18 pt template (black and alpha; macOS tints it). The
# facets survive as two alphas, lit 1 and shaded 0.42, so the star keeps
# its volume in one colour. "dot": the dot at the lower right, the star
# cut away around it; "hollow": the outline when nothing connects here.
#
# The Windows tray: 16 units drawn at 16, 20, 24 and 32 px, in colour, one
# version for a dark taskbar and one for a light one (Windows says which).

MB_STAR = StarV(46, 38, 1.4, 19.0)
DOT = "#ff8a00"


def _facets_flat(star, cx, cy, s, lit, shade, lit_op=1.0, shade_op=1.0):
    out = []
    bs = [(i, side, _brightness(star, i, side)) for i in range(4) for side in (-1, 1)]
    mid = sorted(b for *_, b in bs)[3:5]
    cut = sum(mid) / 2
    for i, side, b in bs:
        on = b > cut
        out.append(f'<path d="{star.facet(i, side, cx, cy, s)}" fill="{lit if on else shade}" '
                   f'fill-opacity="{lit_op if on else shade_op}"/>')
    return out


def _cutout(uid, size, dot_c, dot_r, gap):
    return (f'<mask id="{uid}cut" maskUnits="userSpaceOnUse" x="0" y="0" width="{size}" height="{size}">'
            f'<rect width="{size}" height="{size}" fill="#fff"/>'
            f'<circle cx="{f(dot_c)}" cy="{f(dot_c)}" r="{f(dot_r + gap)}" fill="#000"/></mask>')


def menubar_svg(state):
    S, c = 18, 9
    s = 8.4 / 46
    defs, body = [], []
    if state == "hollow":
        hs = 7.7 / 46
        body.append(f'<path d="{MB_STAR.path(c, c, hs)}" fill="none" stroke="#000" stroke-width="1.35" stroke-linejoin="round"/>')
    else:
        g = _facets_flat(MB_STAR, c, c, s, "#000", "#000", 1.0, 0.42)
        if state == "dot":
            defs.append(_cutout("m", S, 15.1, 2.5, 1.1))
            body.append('<g mask="url(#mcut)">' + "".join(g) + "</g>")
            body.append('<circle cx="15.1" cy="15.1" r="2.5" fill="#000"/>')
        else:
            body += g
    return svg(S, S, defs, body)


TRAY = {
    # (lit, shaded) on a dark taskbar and on a light one
    True: ("#ffffff", "#5b8dff"),
    False: ("#6e9bff", "#1d48c9"),
}
GREY = {True: "#a3a8b3", False: "#6b7280"}


def tray_svg(state, dark):
    S, c = 16, 8
    s = 7.6 / 46
    defs, body = [], []
    if state == "hollow":
        hs = 7.0 / 46
        body.append(f'<path d="{MB_STAR.path(c, c, hs)}" fill="none" stroke="{GREY[dark]}" stroke-width="1.3" stroke-linejoin="round"/>')
    else:
        lit, shade = TRAY[dark]
        g = _facets_flat(MB_STAR, c, c, s, lit, shade)
        if state == "dot":
            defs.append(_cutout("t", S, 13.3, 2.4, 1.0))
            body.append('<g mask="url(#tcut)">' + "".join(g) + "</g>")
            body.append(f'<circle cx="13.3" cy="13.3" r="2.4" fill="{DOT}"/>')
        else:
            body += g
    return svg(S, S, defs, body)


# ------------------------------------------------------------ small app icons
#
# 16 and 32 px (and Windows' 24): the menu bar's fuller star, larger, its
# facets in two flat tones, the sky's glow kept; no dispersion, ridges or
# rim, which would only blur there.

def _night_small(x, y, size, outline, uid, shadow):
    cx, cy = x + size / 2, y + size / 2
    s = size * 0.84 / 100
    defs = [
        f'<clipPath id="{uid}tile"><path d="{outline}"/></clipPath>',
        f'<linearGradient id="{uid}sky" x1="0" y1="{f(y)}" x2="0" y2="{f(y + size)}" gradientUnits="userSpaceOnUse">'
        + "".join(f'<stop offset="{o}" stop-color="{c}"/>' for o, c in SKY) + "</linearGradient>",
        f'<radialGradient id="{uid}lit" cx="{f(cx)}" cy="{f(cy)}" r="{f(size * 0.5)}" gradientUnits="userSpaceOnUse">'
        f'<stop offset="0" stop-color="{GLOW}" stop-opacity="0.7"/><stop offset="1" stop-color="{GLOW}" stop-opacity="0"/></radialGradient>',
    ]
    if shadow:
        u = size / 824
        defs.append(f'<filter id="{uid}shadow" x="-20%" y="-20%" width="140%" height="150%">'
                    f'<feDropShadow dx="0" dy="{f(12 * u)}" stdDeviation="{f(14 * u)}" flood-color="#050b2a" flood-opacity="0.35"/></filter>')
    body = [f'<path d="{outline}" fill="url(#{uid}sky)"' + (f' filter="url(#{uid}shadow)"' if shadow else "") + "/>",
            f'<g clip-path="url(#{uid}tile)"><rect x="{f(x)}" y="{f(y)}" width="{f(size)}" height="{f(size)}" fill="url(#{uid}lit)"/>']
    body += _facets_flat(MB_STAR, cx, cy, s * 46 / 46, "#ffffff", "#7ea3ff")
    body.append("</g>")
    return defs, body


def app_icon_macos_small():
    d, b = _night_small(100, 100, 824, squircle(100, 100, 824), "s", True)
    return svg(1024, 1024, d, b)


def app_icon_windows_small():
    m = 1024 * 0.03
    size = 1024 - 2 * m
    d, b = _night_small(m, m, size, rrect(m, m, size, size, size * 0.22), "s", False)
    return svg(1024, 1024, d, b)
