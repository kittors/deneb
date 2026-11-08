"""Frame check for recordings of the native client (DESIGN §11, §6.6).

The client's tester writes JPEG frames at 60 a second of the view's clock
(cmd/deneb: TestRealAcceptance, TestMotionFrames); there is no per-element
trace as record.mjs has for the HTML demos, so this looks at the pixels.

usage: python3 -I frame-check.py <frames_dir> [--upto N] [--order mtime]

Prints, per recording:
- runs of motion: frames where more than the flowing light changes;
- one-frame glitches: frame i differs from both neighbours, which agree
  (something blinked, or jumped and came back: §6.6);
- instant changes: a big change in one frame between still frames (a jump
  or a cut, to look at).
With --order mtime the frames are taken in the order they were written,
for folders where a second recording wrote over part of a first.
"""
import os
import sys

import numpy as np
from PIL import Image

args = sys.argv[1:]
d = args[0]


def opt(name, default):
    if "--" + name in args:
        return args[args.index("--" + name) + 1]
    return default


files = sorted(f for f in os.listdir(d) if f.endswith(".jpg"))
if opt("order", "name") == "mtime":
    files.sort(key=lambda f: os.stat(os.path.join(d, f)).st_mtime)
upto = int(opt("upto", len(files)))
files = files[:upto]

T = 18  # a pixel counts as changed past this (JPEG noise stays below)


def load(f):
    im = Image.open(os.path.join(d, f)).convert("L")
    im = im.resize((im.width // 2, im.height // 2), Image.BILINEAR)
    return np.asarray(im, dtype=np.int16)


def diff(a, b):
    return int((np.abs(a - b) > T).sum())


frames = [None, None]
act = [0]
glitch = []
prev2 = None
prev = load(files[0])
for i in range(1, len(files)):
    cur = load(files[i])
    act.append(diff(prev, cur))
    if prev2 is not None:
        a, b, c = act[i - 1], act[i], diff(prev2, cur)
        if a > 150 and b > 150 and c < 0.25 * min(a, b):
            glitch.append((i - 1, a, b, c))
    prev2, prev = prev, cur

act = np.array(act)
base = int(np.median(act))
moving = act > max(3 * base, base + 150)
runs = []
i = 0
while i < len(act):
    if moving[i]:
        j = i
        while j + 1 < len(act) and (moving[j + 1] or (j + 2 < len(act) and moving[j + 2])):
            j += 1
        runs.append((i, j, int(act[i:j + 1].max())))
        i = j + 1
    else:
        i += 1
instant = [i for i in range(1, len(act) - 1)
           if act[i] > 2500 and act[i - 1] < max(3 * base, 300) and act[i + 1] < max(3 * base, 300)]

print(f"{d}: {len(files)} frames, still frames change {base} px")
print("motion:", ", ".join(f"{a}-{b} (peak {p})" for a, b, p in runs) or "none")
print("one-frame glitches:", ", ".join(f"{i} ({a},{b} vs {c})" for i, a, b, c in glitch) or "none")
print("instant changes:", ", ".join(f"{i} ({act[i]})" for i in instant) or "none")
names = {i: files[i] for i in set([g[0] for g in glitch] + instant)}
if names:
    print("files:", ", ".join(f"{i}={n}" for i, n in sorted(names.items())))
