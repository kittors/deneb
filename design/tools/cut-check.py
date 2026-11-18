"""Cut check for recordings: finds content that changes in one frame (a hard
cut), however small: a word, a number, a line (the user, 11-18: "所有的内容的
变化都得有微动效，要录屏逐帧检查效果"). frame-check.py finds what moves and
the big jumps; this finds the small ones it lets through.

usage: python3 -I cut-check.py <frames_dir> [--from N] [--to N] [--min 24]

A pixel is cut in frame t when it changes by a lot from t-1 to t (more than
a fade moves it in one frame: --step, 30 levels of grey by default) and is
still in the frames on either side. Cut pixels near one another (8 apart)
make a region; a region counts when it has --min pixels or more and
nothing else moves in its box in the frames before and after it: a fade
spreads over frames, and what slides moves on in the next one, so neither
is a cut. Each cut is printed with its frame and box, at the frames' scale.
A cut whose shapes are the same in both frames, give or take a pixel, is
marked "same shapes within a pixel: look". These are usually words a
fraction of a pixel over, at the end of a slow spring, but a person decides
(within_pixel).
"""
import os
import sys

import numpy as np
from PIL import Image

args = sys.argv[1:]
d = args[0]


def opt(name, default):
    if "--" + name in args:
        return type(default)(args[args.index("--" + name) + 1])
    return default


STEP = opt("step", 30)  # what a cut moves a pixel by, in one frame
STILL = 10  # what a still pixel may move by (JPEG noise)
MIN = opt("min", 24)
CELL = 8

files = sorted(f for f in os.listdir(d) if f.endswith(".jpg"))
lo, hi = opt("from", 0), opt("to", len(files))
files = files[lo:hi]


def load(f):
    return np.asarray(Image.open(os.path.join(d, f)).convert("L"), dtype=np.int16)


frames = [load(f) for f in files]


def soft(a):
    """a, each pixel the mean of the 3 by 3 around it: words drawn a part of
    a pixel over come out alike."""
    p = np.pad(a.astype(np.float32), 1, mode="edge")
    return sum(p[y:y + a.shape[0], x:x + a.shape[1]] for y in range(3) for x in range(3)) / 9


def shifted(a, b, x0, y0, x1, y1, most=3):
    """Whether box (x0,y0)-(x1,y1) of b is a's moved by up to most pixels
    (words and marks sliding as a pill stretches, slow at its end): moved,
    and both softened, it differs from b by a third of what it does in
    place, or less."""
    h, w = a.shape
    if x0 - most - 1 < 0 or y0 - most - 1 < 0 or x1 + most + 1 > w or y1 + most + 1 > h:
        return False
    A = soft(a[y0 - most - 1:y1 + most + 1, x0 - most - 1:x1 + most + 1])
    B = soft(b[y0 - most - 1:y1 + most + 1, x0 - most - 1:x1 + most + 1])
    k = most + 1
    here = B[k:k + y1 - y0, k:k + x1 - x0]
    still = np.abs(here - A[k:k + y1 - y0, k:k + x1 - x0]).mean()
    if still == 0:
        return True
    best = still
    for dy in range(-most, most + 1):
        for dx in range(-most, most + 1):
            best = min(best, np.abs(here - A[k + dy:k + dy + y1 - y0, k + dx:k + dx + x1 - x0]).mean())
    return best < still / 3


def grow(m):
    """m, with each pixel's 8 neighbours added."""
    p = np.pad(m, 1)
    return sum(p[y:y + m.shape[0], x:x + m.shape[1]] for y in range(3) for x in range(3)) > 0


def within_pixel(a, b, x0, y0, x1, y1):
    """Whether box (x0,y0)-(x1,y1) shows the same shapes in a and b, give or
    take a pixel. The ink is what lies past halfway from the box's ground
    (its median) to its farthest grey. Each frame's ink, all but one pixel,
    must lie within a pixel of the other's, and keep its tone.

    Words drawn a fraction of a pixel over, at the end of a slow spring, come
    out like this: only the grey of their edges changes. At the frames' scale
    a digit swapped for a similar one (4 for 5 in a small count) can too, so
    these cuts are marked for a person to look at, not dropped."""
    A, B = a[y0:y1, x0:x1], b[y0:y1, x0:x1]
    both = np.concatenate([A.ravel(), B.ravel()])
    ground = np.median(both)
    far = both[np.argmax(np.abs(both - ground))]
    half = (ground + far) / 2
    ia, ib = (A < half, B < half) if far < ground else (A > half, B > half)
    if not (ia & ib).any():
        return False
    new = int((ib & ~grow(ia)).sum() + (ia & ~grow(ib)).sum())
    tone = abs(A[ia & ib].mean() - B[ia & ib].mean())
    return new <= 1 and tone < 2 * STEP / 3


cuts = []
for t in range(2, len(frames) - 2):
    before = np.abs(frames[t - 1] - frames[t - 2])
    jump = np.abs(frames[t] - frames[t - 1])
    after = np.abs(frames[t + 1] - frames[t])
    cut = (jump > STEP) & (before <= STILL) & (after <= STILL)
    if cut.sum() < MIN:
        continue
    # Regions: cells of the grid with cut pixels, joined to their neighbours.
    h, w = cut.shape
    gh, gw = (h + CELL - 1) // CELL, (w + CELL - 1) // CELL
    pad = np.zeros((gh * CELL, gw * CELL), dtype=bool)
    pad[:h, :w] = cut
    cells = pad.reshape(gh, CELL, gw, CELL).sum(axis=(1, 3))
    seen = np.zeros_like(cells, dtype=bool)
    for gy in range(gh):
        for gx in range(gw):
            if cells[gy, gx] == 0 or seen[gy, gx]:
                continue
            stack, region = [(gy, gx)], []
            seen[gy, gx] = True
            while stack:
                y, x = stack.pop()
                region.append((y, x))
                for dy in (-1, 0, 1):
                    for dx in (-1, 0, 1):
                        ny, nx = y + dy, x + dx
                        if 0 <= ny < gh and 0 <= nx < gw and cells[ny, nx] and not seen[ny, nx]:
                            seen[ny, nx] = True
                            stack.append((ny, nx))
            n = sum(int(cells[y, x]) for y, x in region)
            if n < MIN:
                continue
            ys = [y for y, _ in region]
            xs = [x for _, x in region]
            y0, y1 = min(ys) * CELL, min(h, (max(ys) + 1) * CELL)
            x0, x1 = min(xs) * CELL, min(w, (max(xs) + 1) * CELL)
            # Nothing else moving in its box just before and after: else it is
            # a part of a motion (a slide, a spring), not a cut.
            area = (y1 - y0) * (x1 - x0)
            moving = max(int((before[y0:y1, x0:x1] > STILL).sum()), int((after[y0:y1, x0:x1] > STILL).sum()))
            if moving > 0.02 * area:
                continue
            # What only moved a pixel or two (the end of a spring, slow): the
            # frame before, shifted, is the frame after. Not a cut.
            if shifted(frames[t - 1], frames[t], x0, y0, x1, y1):
                continue
            cuts.append((lo + t, n, x0, y0, x1, y1, within_pixel(frames[t - 1], frames[t], x0, y0, x1, y1)))

print(f"{d}: {len(frames)} frames from {lo}")
if not cuts:
    print("cuts: none")
for t, n, x0, y0, x1, y1, near in cuts:
    note = "  [same shapes within a pixel: look]" if near else ""
    print(f"cut at frame {t}: {n} px in ({x0},{y0})-({x1},{y1})  file {files[t - lo]}{note}")
