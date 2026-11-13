"""Direction B, 环: the star as a glass tube with light flowing in it.

Deneb's screens draw a connection as a clear glass tube with coloured light
flowing through it, one colour per person (DESIGN §3.2, §3.5). Here that
tube is bent into the star: blue, violet, pink and cyan, one per tip,
running into each other, so the mark says what the product does - a few
people's devices joined into one loop. A porcelain-white tile; the glass
throws a coloured shadow on it.

The tube's colour runs around the centre, which SVG cannot paint, so the
icon is an HTML page: CSS conic gradients masked by the ring.
"""
import urllib.parse

from geom import Star, f, squircle, rrect

# The tube's centre line in 100 units, and its width.
RING = Star.by_waist(42, 35, 4.3, 15.0)
TUBE = 7.2
# The small cut: a wider tube on a slightly smaller star, so the hole stays
# open at 16 px.
RING_SMALL = Star.by_waist(38, 33, 8.0, 17.0)
TUBE_SMALL = 15.0

# One colour per tip, clockwise from the top: owner colours (DESIGN §3.3),
# a touch deeper so they hold on white.
HUES = ["#3d74ff", "#8b5cf6", "#ec4899", "#14b8d6"]
# The same, lighter, for the light inside the tube.
HUES_LIGHT = ["#a9c4ff", "#d2bdff", "#ffbfdd", "#9debf7"]
# And deeper, for the rod's edges.
HUES_DEEP = ["#1f4fd8", "#6a3fd8", "#c52a7c", "#0b8fae"]


def conic(hues, start=-20):
    stops = []
    n = len(hues)
    for k, h in enumerate(hues + hues[:1]):
        stops.append(f"{h} {start + 360 * k / n:.1f}deg")
    return f"conic-gradient(from 0deg at 50% 50% in oklch, {', '.join(stops)})"


def _mask(d, width, vb=100, dx=0.0, dy=0.0):
    svg = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {vb} {vb}">'
           f'<path d="{d}" fill="none" stroke="#000" stroke-width="{f(width)}" stroke-linejoin="round" '
           f'transform="translate({f(dx)},{f(dy)})"/></svg>')
    return "url('data:image/svg+xml," + urllib.parse.quote(svg) + "')"


def _mask_band(d, outer, inner, vb=100):
    """The tube's wall: the ring of width outer less the ring of width inner."""
    svg = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {vb} {vb}"><defs><mask id="m">'
           f'<path d="{d}" fill="none" stroke="#fff" stroke-width="{f(outer)}" stroke-linejoin="round"/>'
           f'<path d="{d}" fill="none" stroke="#000" stroke-width="{f(inner)}" stroke-linejoin="round"/></mask></defs>'
           f'<rect width="{vb}" height="{vb}" fill="#000" mask="url(#m)"/></svg>')
    return "url('data:image/svg+xml," + urllib.parse.quote(svg) + "')"


def _clip_shift(d, width, clip_width, dx, dy, vb=100):
    """A line of the ring shifted by (dx, dy), kept inside the tube."""
    svg = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {vb} {vb}"><defs><mask id="m">'
           f'<path d="{d}" fill="none" stroke="#fff" stroke-width="{f(clip_width)}" stroke-linejoin="round"/></mask></defs>'
           f'<g mask="url(#m)"><path d="{d}" fill="none" stroke="#000" stroke-width="{f(width)}" '
           f'stroke-linejoin="round" transform="translate({f(dx)},{f(dy)})"/></g></svg>')
    return "url('data:image/svg+xml," + urllib.parse.quote(svg) + "')"


def layer(mask, background, blur=None, outer=None, style="", fade=None):
    """One full-page layer: background painted through mask; blurred after
    the mask when blur is given (so the masked shape itself softens), and
    then kept inside outer, another mask, if given."""
    inner = (f'<div class="L" style="-webkit-mask-image:{mask};mask-image:{mask};'
             f'background:{background}"></div>')
    if blur:
        inner = f'<div class="L" style="filter:blur({blur})">{inner}</div>'
    if outer:
        inner = f'<div class="L" style="-webkit-mask-image:{outer};mask-image:{outer}">{inner}</div>'
    if fade:
        inner = f'<div class="L" style="-webkit-mask-image:{fade};mask-image:{fade}">{inner}</div>'
    return f'<div class="L" style="{style}">{inner}</div>'


def ring_layers(box, ring=RING, tube=TUBE, detail=True, grey=False):
    """The glass rod's layers, each a full-page div; box: the share of the
    page the star's 100-unit frame takes."""
    k = box
    d = ring.path(50, 50, k)
    t = tube * k
    hues = HUES if not grey else ["#8d96a6"] * 4
    deep = HUES_DEEP if not grey else ["#5d6573"] * 4
    light = HUES_LIGHT if not grey else ["#c9ced8"] * 4
    vw = lambda u: f"{u:.3f}vw"  # noqa: E731  (the page is 100 units wide)
    body = _mask(d, t)
    out = []
    if detail:
        # The coloured shadow the glass throws on the tile, and a tight grey one.
        out.append(layer(body, conic(hues), blur=vw(1.8 * k), style=f"transform:translateY({vw(2.6 * k)});opacity:.42"))
        out.append(layer(body, "#1c2440", blur=vw(0.5 * k), style=f"transform:translateY({vw(0.9 * k)});opacity:.14"))
    # The rod, coloured through.
    out.append(layer(body, conic(hues)))
    # Deeper colour towards both edges, where the light crosses more glass.
    out.append(layer(_mask_band(d, t, t * 0.5), conic(deep), blur=vw(0.5 * k), outer=body, style="opacity:.75"))
    # Light through the middle of the rod.
    out.append(layer(_mask(d, t * 0.34), conic(light), blur=vw(0.6 * k), outer=body, style="opacity:.9"))
    if detail:
        # The highlight along its upper-left side, and light coming back
        # through the glass on the lower right.
        out.append(layer(_clip_shift(d, t * 0.15, t * 0.86, -t * 0.24, -t * 0.24), "#fff", blur=vw(0.12 * k),
                         outer=body, style="opacity:.9",
                         fade="linear-gradient(135deg,#000 22%,rgba(0,0,0,.55) 50%,transparent 78%)"))
    return out


CSS = ("html,body{margin:0;width:100%;height:100%;background:transparent;overflow:hidden}"
       ".S{position:relative;width:100vw;height:100vh}"
       ".L{position:absolute;inset:0;-webkit-mask-size:100% 100%;mask-size:100% 100%;"
       "-webkit-mask-repeat:no-repeat;mask-repeat:no-repeat}"
       "svg.L{width:100%;height:100%}")


def page(inner):
    return f'<!doctype html><meta charset="utf-8"><style>{CSS}</style><div class="S">{"".join(inner)}</div>\n'


def tile_svg(x, y, size, outline, shadow):
    u = size / 824
    sh = (f'<filter id="sh" x="-20%" y="-20%" width="140%" height="150%">'
          f'<feDropShadow dx="0" dy="{f(12 * u)}" stdDeviation="{f(14 * u)}" flood-color="#1a2240" flood-opacity="0.22"/></filter>'
          if shadow else "")
    return (f'<svg class="L" viewBox="0 0 1024 1024"><defs>{sh}'
            f'<linearGradient id="pc" x1="0" y1="{f(y)}" x2="0" y2="{f(y + size)}" gradientUnits="userSpaceOnUse">'
            f'<stop offset="0" stop-color="#ffffff"/><stop offset="1" stop-color="#e8ecf4"/></linearGradient>'
            f'<radialGradient id="pl" cx="{f(x + size * 0.5)}" cy="{f(y + size * 0.42)}" r="{f(size * 0.6)}" gradientUnits="userSpaceOnUse">'
            f'<stop offset="0" stop-color="#fff" stop-opacity="0.9"/><stop offset="1" stop-color="#fff" stop-opacity="0"/></radialGradient>'
            f'</defs><path d="{outline}" fill="url(#pc)"' + (' filter="url(#sh)"' if shadow else "") + '/>'
            f'<path d="{outline}" fill="url(#pl)"/></svg>')


def app_icon_macos():
    return page([tile_svg(100, 100, 824, squircle(100, 100, 824), True)] + ring_layers(0.86 * 824 / 1024))


def app_icon_windows():
    """No tile on Windows: the ring itself, as Windows 11's own icons are."""
    return page(ring_layers(0.98))


# ------------------------------------------------------------ menu bar, tray
#
# The menu bar: an 18 pt template, the ring in one colour. "dot": the dot
# at the lower right, the ring cut away around it; "hollow" (nothing
# connects here): the ring broken at its upper right and faded, as a
# connection that stopped is drawn in the app (a broken, empty tube).
#
# The tray: the ring in its four colours (lighter on a dark taskbar), grey
# and broken when nothing connects.

MB_RING = Star.by_waist(44, 37, 1.6, 15.5)
DOT = "#ff8a00"


def _ring_path_svg(d, width, color, opacity=1.0, broken=False, extra=""):
    # Broken: dashed, as a connection that cannot get through is drawn in
    # the app (DESIGN §3.5: only a grey dashed line is left).
    dash = ' pathLength="100" stroke-dasharray="5.2 3.13" stroke-dashoffset="2" stroke-linecap="round"' if broken else ""
    return (f'<path d="{d}" fill="none" stroke="{color}" stroke-width="{f(width)}" stroke-linejoin="round" '
            f'stroke-opacity="{opacity}"{dash}{extra}/>')


def _svg(w, h, defs, body):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {f(w)} {f(h)}" width="100%" height="100%">'
            f'<defs>{"".join(defs)}</defs>{"".join(body)}</svg>\n')


def menubar_svg(state):
    S, c = 18, 9
    tube = 2.1
    s = (8.1 - tube / 2) / MB_RING.tv
    d = MB_RING.path(c, c, s)
    defs, body = [], []
    if state == "hollow":
        body.append(_ring_path_svg(d, 1.55, "#000", 0.6, broken=True))
    elif state == "dot":
        defs.append('<mask id="cut" maskUnits="userSpaceOnUse" x="0" y="0" width="18" height="18">'
                    '<rect width="18" height="18" fill="#fff"/><circle cx="15.1" cy="15.1" r="3.6" fill="#000"/></mask>')
        body.append(f'<g mask="url(#cut)">{_ring_path_svg(d, tube, "#000")}</g>')
        body.append('<circle cx="15.1" cy="15.1" r="2.5" fill="#000"/>')
    else:
        body.append(_ring_path_svg(d, tube, "#000"))
    return _svg(S, S, defs, body)


def _lighten(hexes, t):
    from geom import mix
    return [mix(h, "#ffffff", t) for h in hexes]


def tray_html(state, dark):
    """The tray icon as a page (its colour runs around the centre)."""
    S = 16
    tube = 2.0
    s = (7.9 - tube / 2) / MB_RING.tv
    d = MB_RING.path(8, 8, s)
    if state == "hollow":
        grey = "#a3a8b3" if dark else "#6b7280"
        return page([f'<svg class="L" viewBox="0 0 16 16">{_ring_path_svg(d, 1.5, grey, 1.0, broken=True)}</svg>'])
    hues = _lighten(HUES, 0.18) if dark else HUES
    ring = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16">'
            + ('<defs><mask id="m"><rect width="16" height="16" fill="#fff"/><circle cx="13.3" cy="13.3" r="3.4" fill="#000"/></mask></defs><g mask="url(#m)">'
               if state == "dot" else "<g>")
            + f'<path d="{d}" fill="none" stroke="#000" stroke-width="{f(tube)}" stroke-linejoin="round"/></g></svg>')
    mask = "url('data:image/svg+xml," + urllib.parse.quote(ring) + "')"
    inner = [layer(mask, conic(hues))]
    if state == "dot":
        inner.append(f'<svg class="L" viewBox="0 0 16 16"><circle cx="13.3" cy="13.3" r="2.4" fill="{DOT}"/></svg>')
    return page(inner)


# ------------------------------------------------------------ small app icons

def app_icon_macos_small():
    """16 and 32 px: the porcelain tile and the ring as in the menu bar,
    in its four colours, no glass detail."""
    k = 0.86 * 824 / 1024
    tube = 16.0
    s = (50 - tube / 2) / MB_RING.tv * k
    d = MB_RING.path(50, 50, s)
    return page([tile_svg(100, 100, 824, squircle(100, 100, 824), True),
                 layer(_mask(d, tube * k), conic(HUES))])


def app_icon_windows_small():
    """Windows' 16 to 32 px: the tray's ring, without a tile."""
    tube = 12.8
    s = (49.5 - tube / 2) / MB_RING.tv
    d = MB_RING.path(50, 50, s)
    return page([layer(_mask(d, tube), conic(HUES))])
