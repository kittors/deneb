#!/usr/bin/env python3
"""A loop of the real window for the website (DESIGN §14.16), from a test's
frames (TestSiteDragLoop): the pointer drawn where pointer.txt says, the
part of the window the page shows cropped out, and the last half second
faded into the first, so that it goes round without a jump. Writes
<name>-dark.webm and .mp4 into media/, and its first frame as the picture
under it, img/dark-<name>.webp, with its size in img/sizes.json.

usage: python3 -I design/tools/site-loop.py <frames> <name> <x0,y0,x1,y1> [web/site]
       (the box in the window's points; the frames are twice as sharp)
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile

from PIL import Image, ImageDraw, ImageFilter

FPS = 30
FADE = 15  # frames: the last half second fades into the first


def pointer(scale):
    """The arrow, as the site's recordings draw it (site.js), at scale."""
    k = 8 * scale  # drawn large, then made small: smooth edges
    pts = [(5, 2.5), (5, 19.7), (9.3, 15.6), (12.2, 22), (15.1, 20.7), (12.3, 14.5), (18.3, 14.4)]
    big = Image.new('RGBA', (int(24 * k), int(26 * k)), (0, 0, 0, 0))
    d = ImageDraw.Draw(big)
    poly = [(x * k, y * k) for x, y in pts]
    d.polygon(poly, fill=(255, 255, 255, 255))
    # The white edge, 1.4 wide: the dark arrow drawn inside it.
    inner = Image.new('L', big.size, 0)
    ImageDraw.Draw(inner).polygon(poly, fill=255)
    inner = inner.filter(ImageFilter.MinFilter(int(1.4 * k) | 1))
    big.paste((17, 17, 17, 255), (0, 0), inner)
    shadow = Image.new('RGBA', big.size, (0, 0, 0, 0))
    alpha = big.split()[3].point(lambda a: a * 0.35)
    shadow.putalpha(alpha)
    shadow = shadow.filter(ImageFilter.GaussianBlur(1.2 * k))
    out = Image.new('RGBA', big.size, (0, 0, 0, 0))
    out.alpha_composite(shadow, (0, int(1 * k)))
    out.alpha_composite(big)
    small = out.resize((int(24 * scale), int(26 * scale)), Image.LANCZOS)
    return small, (5 * scale, 2.5 * scale)  # the tip


def main():
    if len(sys.argv) < 4:
        sys.exit(__doc__)
    frames, name = sys.argv[1], sys.argv[2]
    box = [int(v) * 2 for v in sys.argv[3].split(',')]
    site = sys.argv[4] if len(sys.argv) > 4 else os.path.join(os.path.dirname(__file__), '..', '..', 'web', 'site')
    track = [l.split() for l in open(os.path.join(frames, 'pointer.txt'))]
    n = len(track)
    arrow, tip = pointer(2)

    def frame(i):
        im = Image.open(os.path.join(frames, f'{i:04d}.png')).convert('RGBA')
        x, y = float(track[i][0]) * 2, float(track[i][1]) * 2
        im.alpha_composite(arrow, (int(round(x - tip[0])), int(round(y - tip[1]))))
        return im.convert('RGB').crop(box)

    tmp = tempfile.mkdtemp(prefix='deneb-loop-')
    try:
        # Out: frames FADE.. to the end, the last FADE of them going over
        # into frames 0..FADE-1, so that the loop's end runs into its start.
        out = list(range(FADE, n))
        for j, i in enumerate(out):
            im = frame(i)
            k = j - (len(out) - FADE)
            if k >= 0:
                t = (k + 1) / FADE
                t = t * t * (3 - 2 * t)
                im = Image.blend(im, frame(k), t)
            im.save(os.path.join(tmp, f'{j:04d}.png'))
        first = Image.open(os.path.join(tmp, '0000.png'))
        img = os.path.join(site, 'img')
        first.save(os.path.join(img, f'dark-{name}.webp'), 'WEBP', quality=90, method=6)
        sizes_path = os.path.join(img, 'sizes.json')
        sizes = json.load(open(sizes_path))
        sizes[name] = list(first.size)
        with open(sizes_path, 'w') as f:
            f.write('{\n' + ',\n'.join(f' {json.dumps(k)}: [{w}, {h}]' for k, (w, h) in sorted(sizes.items())) + '\n}\n')
        media = os.path.join(site, 'media')
        src = ['-framerate', str(FPS), '-i', os.path.join(tmp, '%04d.png')]
        common = ['ffmpeg', '-v', 'error', '-y', '-threads', '2']
        subprocess.run(common + src + ['-c:v', 'libvpx-vp9', '-b:v', '0', '-crf', '34', '-row-mt', '1', '-pix_fmt', 'yuv420p', '-an', os.path.join(media, f'{name}-dark.webm')], check=True)
        subprocess.run(common + src + ['-c:v', 'libx264', '-crf', '24', '-preset', 'slow', '-pix_fmt', 'yuv420p', '-movflags', '+faststart', '-an', os.path.join(media, f'{name}-dark.mp4')], check=True)
        for ext in ('webm', 'mp4'):
            p = os.path.join(media, f'{name}-dark.{ext}')
            print(f'{p}: {os.path.getsize(p) // 1024} KB')
        print(f'{name}: {first.size[0]}x{first.size[1]}, {len(out)} frames')
    finally:
        shutil.rmtree(tmp)


if __name__ == '__main__':
    main()
