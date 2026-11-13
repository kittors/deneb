#!/usr/bin/env python3
"""The Deneb mark, second version (2026-11-11): 一颗亮星, a bright star,
drawn the way a bright star looks through a telescope (DESIGN §15).

The star is four rays, the vertical pair a little longer than the
horizontal one (Deneb's own proportion, kept from the first version). Each
quarter of its outline is one concave edge from a vertical tip to a
horizontal one: the superellipse |x/rh|^n + |y/rv|^n = 1 with n < 1, its
rays narrowing evenly from the waist to the tips, and at each tip a small
angle added so that a ray ends in a clean point instead of a needle.

The outline is fitted with a few cubic Béziers per quarter, so that the
path stays short and every renderer (Chrome, MyGo, the browsers) draws the
same shape:

    python3 -I design/brand/star.py      # prints the fitted marks and their error

Two cuts of the same star (DESIGN §15.2):
  FULL   40 px (pt) and up: slender rays.
  SMALL  16 to 32 px (pt): bolder rays and a wider tip, which keep their
         width where the slender ones would fade to nothing.
"""
import math

import numpy as np

# Shape parameters, in a 100-unit box: half lengths rv (vertical) and rh
# (horizontal), the superellipse exponent n (smaller: slimmer rays), the
# tip angle in degrees (half the angle at each point).
FULL = dict(rv=46.0, rh=37.0, n=0.40, tip=2.5)
SMALL = dict(rv=46.0, rh=38.0, n=0.50, tip=4.6)
# 16 px app icons only (final.py): stouter still, as there a ray has one
# pixel of width to show.
TINY = dict(rv=46.0, rh=39.0, n=0.56, tip=7.0)

# Bump when outline() or the fitting changes, to refit.
FIT_VERSION = 2


def outline(rv, rh, n, tip, steps=4000):
    """Points of the upper right quarter, from the top tip (0, -rv) to the
    right tip (rh, 0), centre at the origin, y down (SVG)."""
    tt = math.tan(math.radians(tip))
    p = 4.0
    pts = []
    for i in range(steps + 1):
        t = (math.pi / 2) * (1 - i / steps)
        ct, st = math.cos(t), math.sin(t)
        xs = rh * ct ** (2 / n)
        ys = rv * st ** (2 / n)
        # The tip angle: each ray at least as wide as a wedge of that angle
        # from its tip, joined to the superellipse by a smooth maximum, so
        # that the width still only grows faster toward the waist (the
        # edges stay concave all the way, no bulge near the tip).
        x = (xs ** p + ((rv - ys) * tt) ** p) ** (1 / p)
        y = (ys ** p + ((rh - xs) * tt) ** p) ** (1 / p)
        pts.append((x, -y))
    return np.array(pts)


def _bez(P, t):
    t = t[:, None]
    return (1 - t) ** 3 * P[0] + 3 * (1 - t) ** 2 * t * P[1] + 3 * (1 - t) * t ** 2 * P[2] + t ** 3 * P[3]


def _tangent(pts, i):
    a, b = pts[max(i - 2, 0)], pts[min(i + 2, len(pts) - 1)]
    d = b - a
    return d / np.linalg.norm(d)


def _fit_segment(pts, t0, t3):
    """One cubic from pts[0] to pts[-1], leaving along t0 and arriving
    along t3: the two handle lengths that keep it closest to pts."""
    from scipy.optimize import least_squares

    P0, P3 = pts[0], pts[-1]
    L = float(np.linalg.norm(P3 - P0))
    ts = np.linspace(0, 1, 600)
    sample = pts[:: max(1, len(pts) // 120)]

    def ctrl(ab):
        return np.array([P0, P0 + ab[0] * t0, P3 - ab[1] * t3, P3])

    def resid(ab):
        B = _bez(ctrl(ab), ts)
        return np.sqrt(((sample[:, None, :] - B[None, :, :]) ** 2).sum(-1)).min(1)

    # Keep the handles inside the quarter (x >= 0, y <= 0): a handle across
    # an axis would let the thin end of a ray cross itself.
    hi = [1.5 * L, 1.5 * L]
    if t0[1] > 0:
        hi[0] = min(hi[0], -P0[1] / t0[1])
    if t3[0] > 0:
        hi[1] = min(hi[1], P3[0] / t3[0])
    hi = [max(h, 2e-3) for h in hi]
    r = least_squares(resid, [min(L / 3, h / 2) for h in hi], bounds=([1e-3, 1e-3], hi))
    P = ctrl(r.x)
    B = _bez(P, np.linspace(0, 1, 3000))
    err = np.sqrt(((pts[:, None, :] - B[None, :, :]) ** 2).sum(-1)).min(1).max()
    return P, float(err)


def _fit_cuts(pts, cuts, tan_start, tan_end):
    out, worst = [], 0.0
    for a, b in zip(cuts, cuts[1:]):
        t0 = tan_start if a == 0 else _tangent(pts, a)
        t3 = tan_end if b == len(pts) - 1 else _tangent(pts, b)
        P, err = _fit_segment(pts[a:b + 1], t0, t3)
        out.append(P)
        worst = max(worst, err)
    return out, worst


def quarter(rv, rh, n, tip, segs=3):
    """The upper right quarter as `segs` cubics: [(P0, P1, P2, P3), ...] and
    the largest distance from the true outline. Each joint keeps the
    outline's own tangent there; where the joints go is searched for, the
    places that keep the largest distance smallest."""
    from scipy.optimize import minimize

    pts = outline(rv, rh, n, tip, steps=1600)
    tr = math.radians(tip)
    tan_start = np.array([math.sin(tr), math.cos(tr)])
    tan_end = np.array([math.cos(tr), math.sin(tr)])
    # Joints are placed by how far the tangent has turned, 0 at the top
    # tip and 1 at the right one.
    ang = np.unwrap(np.arctan2(*np.gradient(pts, axis=0).T[::-1]))
    turn = (ang - ang[0]) / (ang[-1] - ang[0])

    def cuts_at(us):
        idx = [int(np.argmin(np.abs(turn - min(max(u, 0.03), 0.97)))) for u in sorted(us)]
        return [0] + sorted(set(idx)) + [len(pts) - 1]

    def err(us):
        c = cuts_at(us)
        return 1e9 if len(c) != segs + 1 else _fit_cuts(pts, c, tan_start, tan_end)[1]

    us = [k / segs for k in range(1, segs)]
    if segs > 1:
        us = minimize(err, us, method="Nelder-Mead",
                      options=dict(xatol=0.004, fatol=0.0005, maxiter=80, initial_simplex=None)).x
    return _fit_cuts(pts, cuts_at(us), tan_start, tan_end)


def fmt(v, nd=2):
    s = f"{v:.{nd}f}".rstrip("0").rstrip(".")
    return "0" if s in ("-0", "") else s


def path(cx, cy, scale, q, nd=2):
    """The whole star from its upper right quarter q, mirrored, as an SVG
    path: centre (cx, cy), q's units times scale."""
    def pt(p, sx, sy):
        return f"{fmt(cx + sx * p[0] * scale, nd)},{fmt(cy + sy * p[1] * scale, nd)}"

    quads = [
        [(s, 1, 1) for s in q],
        [(s[::-1], 1, -1) for s in q[::-1]],
        [(s, -1, -1) for s in q],
        [(s[::-1], -1, 1) for s in q[::-1]],
    ]
    d = "M" + pt(q[0][0], 1, 1)
    for quad in quads:
        for s, sx, sy in quad:
            d += f" C{pt(s[1], sx, sy)} {pt(s[2], sx, sy)} {pt(s[3], sx, sy)}"
    return d + "Z"


_cache = {}


def mark_quarter(small=False):
    """The fitted quarter: two cubics if they keep within 0.06 of the
    outline (1/1500 of the mark, a third of a pixel on a 1024 icon), else
    three. Fitting takes a while, so the result is kept in the temporary
    folder, under the parameters it was fitted for."""
    import hashlib
    import json
    import os
    import tempfile

    if small in _cache:
        return _cache[small]
    p = TINY if small == "tiny" else SMALL if small else FULL
    key = hashlib.sha1(json.dumps([p, FIT_VERSION]).encode()).hexdigest()[:12]
    cache = os.path.join(tempfile.gettempdir(), f"deneb-star-{key}.json")
    try:
        with open(cache) as fh:
            q, err = json.load(fh)
        q = [np.array(s) for s in q]
    except (OSError, ValueError):
        q, err = quarter(p["rv"], p["rh"], p["n"], p["tip"], 2)
        if err >= 0.06:
            q, err = quarter(p["rv"], p["rh"], p["n"], p["tip"], 3)
        with open(cache, "w") as fh:
            json.dump([[s.tolist() for s in q], err], fh)
    _cache[small] = (q, err)
    return q, err


def mark_d(cx=50, cy=50, scale=1.0, small=False, nd=2):
    """The mark's path, centred on (cx, cy), in the 100-unit box times
    scale. small: True for the small cut, "tiny" for the 16 px one."""
    q, _ = mark_quarter(small)
    return path(cx, cy, scale, q, nd)


def glyph_d(small=True):
    """The mark in the 24-unit box of the client's and the web's glyphs:
    21 units from tip to tip, as before."""
    p = SMALL if small else FULL
    return mark_d(12, 12, 10.5 / p["rv"], small, nd=2)


if __name__ == "__main__":
    for name, small in (("full", False), ("small", True)):
        q, err = mark_quarter(small)
        print(f"{name}: {len(q)} cubics a quarter, max error {err:.3f} of 100")
        print(f"  glyph: {glyph_d(small)}")
