"""Contact sheet of recorded frames, for reviewing motion frame by frame.

usage: python3 -I sheet.py <frames_dir> <out.jpg> [--from ms] [--to ms]
       [--step ms] [--cols n] [--width px] [--crop x,y,w,h]

--crop takes a region in frame pixels before scaling, to look closely at
one part of the window across time.
"""
import os
import sys

from PIL import Image, ImageDraw, ImageFont

args = sys.argv[1:]
frames, out = args[0], args[1]


def opt(name, default):
    if "--" + name in args:
        return args[args.index("--" + name) + 1]
    return default


STEP = 1000 / 60
files = sorted(f for f in os.listdir(frames) if f.endswith(".jpg"))
t_from = float(opt("from", 0))
t_to = float(opt("to", (len(files) - 1) * STEP))
step = float(opt("step", 250))
cols = int(opt("cols", 4))
width = int(opt("width", 640))
crop = opt("crop", None)

picks = []
t = t_from
while t <= t_to + 0.01:
    i = min(len(files) - 1, round(t / STEP))
    picks.append((t, files[i]))
    t += step

thumbs = []
for t, f in picks:
    im = Image.open(os.path.join(frames, f)).convert("RGB")
    if crop:
        x, y, w, h = (int(v) for v in crop.split(","))
        im = im.crop((x, y, x + w, y + h))
    h = round(im.height * width / im.width)
    thumbs.append((t, im.resize((width, h), Image.LANCZOS)))

pad, label_h = 6, 24
cell_w, cell_h = width, thumbs[0][1].height
rows = (len(thumbs) + cols - 1) // cols
sheet = Image.new("RGB", (cols * (cell_w + pad) + pad, rows * (cell_h + label_h + pad) + pad), (24, 24, 26))
draw = ImageDraw.Draw(sheet)
font = ImageFont.load_default(size=18)
for k, (t, im) in enumerate(thumbs):
    cx = pad + (k % cols) * (cell_w + pad)
    cy = pad + (k // cols) * (cell_h + label_h + pad)
    draw.text((cx + 2, cy + 2), f"{t / 1000:.2f}s", fill=(220, 220, 220), font=font)
    sheet.paste(im, (cx, cy + label_h))
sheet.save(out, quality=88)
print(out, sheet.size)
