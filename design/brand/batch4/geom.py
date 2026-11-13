"""Geometry for the fourth logo batch (2026-11-13).

One star for all three directions: four tips, the vertical pair longer
(Deneb's 46:37, kept from the first logo), each tip rounded to a circle of
radius rt, and between two tips a concave circular arc of radius R that
touches both tip circles. Small R pinches the waist (a slender star), large
R fills it out (a plump one); rt sharpens or softens the tips. Arcs make
the outline short and exact: it is a handful of SVG arc commands, which
MyGo's glyph parser reads as well as a browser does.
"""
import math


def f(v):
    """A number for SVG, two decimals at most."""
    s = f"{v:.2f}".rstrip("0").rstrip(".")
    return "0" if s in ("-0", "") else s


def pt(p):
    return f"{f(p[0])},{f(p[1])}"


class Star:
    """The star in its own units, centred at (0, 0), y down: tips at
    (0, -tv), (th, 0), (0, tv), (-th, 0)."""

    def __init__(self, tv, th, rt, R):
        self.tv, self.th, self.rt, self.R = tv, th, rt, R
        # Tip circle centres, clockwise from the top.
        self.tips = [(0.0, -(tv - rt)), (th - rt, 0.0), (0.0, tv - rt), (-(th - rt), 0.0)]
        self.apex = [(0.0, -tv), (th, 0.0), (0.0, tv), (-th, 0.0)]
        self.edges = []
        d = R + rt
        for i in range(4):
            P, Q = self.tips[i], self.tips[(i + 1) % 4]
            mx, my = (P[0] + Q[0]) / 2, (P[1] + Q[1]) / 2
            dx, dy = Q[0] - P[0], Q[1] - P[1]
            L = math.hypot(dx, dy)
            h = L / 2
            if d <= h:
                raise ValueError(f"R={R} too small for tips {tv},{th}")
            nx, ny = dy / L, -dx / L
            if nx * mx + ny * my < 0:
                nx, ny = -nx, -ny
            s = math.sqrt(d * d - h * h)
            O = (mx + nx * s, my + ny * s)
            tP = (P[0] + rt * (O[0] - P[0]) / d, P[1] + rt * (O[1] - P[1]) / d)
            tQ = (Q[0] + rt * (O[0] - Q[0]) / d, Q[1] + rt * (O[1] - Q[1]) / d)
            Ol = math.hypot(*O)
            W = (O[0] - R * O[0] / Ol, O[1] - R * O[1] / Ol)
            self.edges.append({"O": O, "tP": tP, "tQ": tQ, "W": W})

    @property
    def waist(self):
        """The distance from the centre to the edge's nearest point."""
        return math.hypot(*self.edges[0]["W"])

    @classmethod
    def by_waist(cls, tv, th, rt, waist):
        """The star whose waist is the given distance (R solved for)."""
        lo = math.hypot(th - tv, tv - th) / 2  # any R below the minimum fails
        lo = max(lo, 0.01)
        hi = 10000.0
        # The smallest valid R first.
        P, Q = (0.0, -(tv - rt)), (th - rt, 0.0)
        h = math.hypot(Q[0] - P[0], Q[1] - P[1]) / 2
        lo = h - rt + 1e-6
        for _ in range(200):
            mid = (lo + hi) / 2
            if cls(tv, th, rt, mid).waist < waist:
                lo = mid
            else:
                hi = mid
        return cls(tv, th, rt, (lo + hi) / 2)

    def _map(self, p, cx, cy, s):
        return (cx + p[0] * s, cy + p[1] * s)

    def path(self, cx=0.0, cy=0.0, s=1.0):
        """The outline, clockwise, as an SVG path."""
        rt, R = self.rt * s, self.R * s
        m = lambda p: pt(self._map(p, cx, cy, s))  # noqa: E731
        out = [f"M{m(self.edges[3]['tQ'])}"]
        for i in range(4):
            e = self.edges[i]
            big = 1 if self.tip_span(i) > math.pi else 0
            out.append(f"A{f(rt)},{f(rt)} 0 {big} 1 {m(e['tP'])}")
            out.append(f"A{f(R)},{f(R)} 0 0 0 {m(e['tQ'])}")
        return "".join(out) + "Z"

    def tip_span(self, i):
        """The angle the rounded tip i turns through, clockwise from where
        the edge before it leaves off to where the edge after it starts."""
        c = self.tips[i]
        a = self.edges[(i - 1) % 4]["tQ"]
        b = self.edges[i]["tP"]
        t0 = math.atan2(a[1] - c[1], a[0] - c[0])
        t1 = math.atan2(b[1] - c[1], b[0] - c[0])
        return (t1 - t0) % (2 * math.pi)

    def facet(self, i, side, cx=0.0, cy=0.0, s=1.0):
        """Half of tip i (0 top, 1 right, 2 bottom, 3 left): side +1 is the
        half clockwise of its ridge, -1 the other. A triangle from the
        centre to the apex and back by the waist, its outer side the
        star's own outline."""
        rt, R = self.rt * s, self.R * s
        m = lambda p: pt(self._map(p, cx, cy, s))  # noqa: E731
        c = (cx, cy)
        if side > 0:
            e = self.edges[i]
            return (f"M{pt(c)}L{m(self.apex[i])}A{f(rt)},{f(rt)} 0 0 1 {m(e['tP'])}"
                    f"A{f(R)},{f(R)} 0 0 0 {m(e['W'])}Z")
        e = self.edges[(i - 1) % 4]
        return (f"M{pt(c)}L{m(self.apex[i])}A{f(rt)},{f(rt)} 0 0 0 {m(e['tQ'])}"
                f"A{f(R)},{f(R)} 0 0 1 {m(e['W'])}Z")

    def facet_normal(self, i, side, height):
        """The facet's normal (unit, z up out of the page) when the centre
        stands height above the tips and the waist."""
        a = self.apex[i]
        w = self.edges[i]["W"] if side > 0 else self.edges[(i - 1) % 4]["W"]
        u = (a[0], a[1], -height)
        v = (w[0], w[1], -height)
        n = (u[1] * v[2] - u[2] * v[1], u[2] * v[0] - u[0] * v[2], u[0] * v[1] - u[1] * v[0])
        if n[2] < 0:
            n = (-n[0], -n[1], -n[2])
        L = math.sqrt(sum(c * c for c in n))
        return tuple(c / L for c in n)


def squircle(x, y, size, n=5.0, steps=360):
    """The macOS app icon shape, as a superellipse (as gen.py's)."""
    a = size / 2
    cx, cy = x + a, y + a
    pts = []
    for i in range(steps):
        t = 2 * math.pi * i / steps
        ct, st = math.cos(t), math.sin(t)
        pts.append((cx + a * math.copysign(abs(ct) ** (2 / n), ct),
                    cy + a * math.copysign(abs(st) ** (2 / n), st)))
    return "M" + " L".join(pt(p) for p in pts) + "Z"


def rrect(x, y, w, h, r):
    """A rounded rectangle path."""
    return (f"M{f(x + r)},{f(y)}H{f(x + w - r)}A{f(r)},{f(r)} 0 0 1 {f(x + w)},{f(y + r)}"
            f"V{f(y + h - r)}A{f(r)},{f(r)} 0 0 1 {f(x + w - r)},{f(y + h)}"
            f"H{f(x + r)}A{f(r)},{f(r)} 0 0 1 {f(x)},{f(y + h - r)}"
            f"V{f(y + r)}A{f(r)},{f(r)} 0 0 1 {f(x + r)},{f(y)}Z")


def lerp(a, b, t):
    return a + (b - a) * t


def hexrgb(h):
    h = h.lstrip("#")
    return tuple(int(h[i:i + 2], 16) for i in (0, 2, 4))


def mix(c1, c2, t):
    """Two #rrggbb colours mixed, t of the way to c2."""
    a, b = hexrgb(c1), hexrgb(c2)
    return "#" + "".join(f"{round(lerp(x, y, t)):02x}" for x, y in zip(a, b))


class StarV:
    """A star with straight edges: from each tip, rounded to radius rt, a
    straight line to the waist point between two tips, w from the centre on
    the diagonal. Crystal-like where Star's arcs are soft. Same interface
    as Star for path(), facet(), apex and facet_normal()."""

    def __init__(self, tv, th, rt, w):
        self.tv, self.th, self.rt, self.w = tv, th, rt, w
        self.tips = [(0.0, -(tv - rt)), (th - rt, 0.0), (0.0, tv - rt), (-(th - rt), 0.0)]
        self.apex = [(0.0, -tv), (th, 0.0), (0.0, tv), (-th, 0.0)]
        k = w / math.sqrt(2)
        self.W = [(k, -k), (k, k), (-k, k), (-k, -k)]  # between tip i and i+1
        # Tangent points: on tip i, the one towards W[i] (after) and W[i-1] (before).
        self.after, self.before = [], []
        for i in range(4):
            self.after.append(self._tangent(self.tips[i], self.W[i], +1))
            self.before.append(self._tangent(self.tips[i], self.W[(i - 1) % 4], -1))
        self.edges = [{"W": self.W[i]} for i in range(4)]

    def _tangent(self, c, p, sign):
        """The point where a line from p touches the circle (c, rt), on the
        side away from the star's centre."""
        r = self.rt
        dx, dy = p[0] - c[0], p[1] - c[1]
        d = math.hypot(dx, dy)
        a = math.acos(r / d)
        base = math.atan2(dy, dx)
        best = None
        for t in (base + a, base - a):
            q = (c[0] + r * math.cos(t), c[1] + r * math.sin(t))
            score = math.hypot(*q)
            if best is None or score > best[0]:
                best = (score, q)
        return best[1]

    @property
    def waist(self):
        return self.w

    def _map(self, p, cx, cy, s):
        return (cx + p[0] * s, cy + p[1] * s)

    def path(self, cx=0.0, cy=0.0, s=1.0):
        rt = self.rt * s
        m = lambda p: pt(self._map(p, cx, cy, s))  # noqa: E731
        out = [f"M{m(self.before[0])}"]
        for i in range(4):
            out.append(f"A{f(rt)},{f(rt)} 0 0 1 {m(self.after[i])}")
            out.append(f"L{m(self.W[i])}L{m(self.before[(i + 1) % 4])}")
        return "".join(out) + "Z"

    def facet(self, i, side, cx=0.0, cy=0.0, s=1.0):
        rt = self.rt * s
        m = lambda p: pt(self._map(p, cx, cy, s))  # noqa: E731
        c = (cx, cy)
        if side > 0:
            return f"M{pt(c)}L{m(self.apex[i])}A{f(rt)},{f(rt)} 0 0 1 {m(self.after[i])}L{m(self.W[i])}Z"
        return f"M{pt(c)}L{m(self.apex[i])}A{f(rt)},{f(rt)} 0 0 0 {m(self.before[i])}L{m(self.W[(i - 1) % 4])}Z"

    def facet_normal(self, i, side, height):
        a = self.apex[i]
        w = self.W[i] if side > 0 else self.W[(i - 1) % 4]
        u = (a[0], a[1], -height)
        v = (w[0], w[1], -height)
        n = (u[1] * v[2] - u[2] * v[1], u[2] * v[0] - u[0] * v[2], u[0] * v[1] - u[1] * v[0])
        if n[2] < 0:
            n = (-n[0], -n[1], -n[2])
        L = math.sqrt(sum(c * c for c in n))
        return tuple(c / L for c in n)
