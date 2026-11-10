#!/usr/bin/env python3
"""The comparison images of a shoot.sh run, from the screenshots in its folder.

    compose.py <folder>

Fills in what a system gives no rectangle for:
  ubuntu-window.png  the largest region that changed between the screen
                     before the window opened and after (GNOME on Wayland
                     tells no window's position)
  mac-tray.png       the menu bar right of the notch
  ubuntu-tray.png    the top bar right of the clock
then writes, each system's image at one logical scale (points, DIPs) and in
the order macOS, Windows, Ubuntu:
  compare-window.png  the windows side by side
  compare-tray.png    the tray icons, twice their size
  compare-screen.png  the screens, at one height
Needs Pillow.
"""
import os
import re
import sys

from PIL import Image, ImageChops, ImageDraw, ImageFont

PLATFORMS = [("mac", "macOS"), ("windows", "Windows"), ("ubuntu", "Ubuntu")]
BG = (238, 238, 240)
INK = (40, 40, 48)


def load(folder, name):
    path = os.path.join(folder, name)
    return Image.open(path).convert("RGB") if os.path.exists(path) else None


def info(folder, platform):
    path = os.path.join(folder, platform + "-info.txt")
    return open(path, encoding="utf-8", errors="replace").read() if os.path.exists(path) else ""


def changed(before, after, box, threshold=48):
    """The mask of pixels inside box (left, top, right, bottom) that changed."""
    a = before.crop(box).convert("L")
    b = after.crop(box).convert("L")
    return ImageChops.difference(a, b).point(lambda v: 255 if v > threshold else 0)


def dominant_box(mask, share=0.3):
    """The bounding box of the rows and columns where much changed: the
    largest changed region, and not a notification or a clock beside it."""
    w, h = mask.size
    px = mask.load()
    rows = [sum(1 for x in range(0, w, 2) if px[x, y]) for y in range(h)]
    if not rows or max(rows) == 0:
        return None
    ys = [y for y, n in enumerate(rows) if n >= share * max(rows)]
    y0, y1 = ys[0], ys[-1] + 1
    cols = [sum(1 for y in range(y0, y1, 2) if px[x, y]) for x in range(w)]
    xs = [x for x, n in enumerate(cols) if n >= share * max(cols)]
    return (xs[0], y0, xs[-1] + 1, y1)


def scale_of(platform, folder, image_width):
    text = info(folder, platform)
    if platform == "mac":
        m = re.search(r"scale ([0-9.]+)", text)
        return float(m.group(1)) if m else 2.0
    if platform == "windows":
        m = re.search(r"dpi (\d+)", text)
        return int(m.group(1)) / 96 if m else 1.0
    # The virtual machine: 2x from 2560 pixels across.
    return 2.0 if image_width >= 2560 else 1.0


def fill_in(folder):
    """Writes the crops the systems gave no rectangle for."""
    notes = []
    before, after = load(folder, "ubuntu-before.png"), load(folder, "ubuntu-screen.png")
    if before and after and not os.path.exists(os.path.join(folder, "ubuntu-window.png")):
        s = scale_of("ubuntu", folder, after.width)
        bar, dock = int(32 * s), int(70 * s)  # GNOME's top bar, Ubuntu's dock
        box = dominant_box(changed(before, after, (dock, bar, after.width, after.height)))
        if box:
            after.crop((box[0] + dock, box[1] + bar, box[2] + dock, box[3] + bar)).save(os.path.join(folder, "ubuntu-window.png"))
        else:
            notes.append("ubuntu: no window appeared")
    # The tray icons: the system's part of the bar, where the icon shows
    # (or does not: a full menu bar, a menu bar manager, Windows 11 keeping
    # new icons among the hidden ones). Reviewed by eye: what else is there
    # changes on its own (clocks, meters), so a difference would not tell.
    after = load(folder, "ubuntu-screen.png")
    if after:
        s = scale_of("ubuntu", folder, after.width)
        # GNOME's top bar, right of the clock in its middle.
        after.crop((after.width // 2 + int(120 * s), 0, after.width, int(32 * s))).save(os.path.join(folder, "ubuntu-tray.png"))
    after = load(folder, "mac-menubar.png")
    if after:
        s = scale_of("mac", folder, after.width)
        # Right of the notch.
        after.crop((after.width // 2 + int(110 * s), 0, after.width, after.height)).save(os.path.join(folder, "mac-tray.png"))
    if load(folder, "windows-screen.png") and load(folder, "windows-tray.png") is None:
        notes.append("windows: no notification area found")
    return notes


def font(size):
    for path in ("/System/Library/Fonts/Helvetica.ttc", "/System/Library/Fonts/Supplemental/Arial.ttf"):
        if os.path.exists(path):
            return ImageFont.truetype(path, size)
    return ImageFont.load_default(size=size)


def side_by_side(items, path, gap=40, label_h=56):
    """items: (label, image) pairs, in a row, labels above."""
    items = [(l, im) for l, im in items if im is not None]
    if not items:
        return False
    width = sum(im.width for _, im in items) + gap * (len(items) + 1)
    height = max(im.height for _, im in items) + label_h + gap
    sheet = Image.new("RGB", (width, height), BG)
    draw = ImageDraw.Draw(sheet)
    f = font(22)
    x = gap
    for label, im in items:
        draw.text((x, 16), label, fill=INK, font=f)
        sheet.paste(im, (x, label_h))
        x += im.width + gap
    sheet.save(path)
    return True


def scaled(im, factor):
    if im is None or factor == 1:
        return im
    return im.resize((max(1, round(im.width * factor)), max(1, round(im.height * factor))), Image.LANCZOS)


def main():
    folder = sys.argv[1]
    notes = fill_in(folder)
    windows, trays, screens = [], [], []
    for p, name in PLATFORMS:
        win = load(folder, p + "-window.png")
        screen = load(folder, p + "-screen.png")
        s = scale_of(p, folder, screen.width if screen else 0)
        if win:
            w1 = scaled(win, 1 / s)
            windows.append((f"{name}  {w1.width}x{w1.height} at 1x (shot at {s:g}x)", w1))
        tray = load(folder, p + "-tray.png")
        if tray:
            trays.append((name, scaled(tray, 2 / s)))
        hidden = load(folder, p + "-tray-hidden.png")
        if hidden:
            trays.append((name + " hidden icons", scaled(hidden, 2 / s)))
        if screen:
            screens.append((f"{name}  {screen.width}x{screen.height}", scaled(screen, 720 / screen.height)))
    made = []
    for items, name in ((windows, "compare-window.png"), (trays, "compare-tray.png"), (screens, "compare-screen.png")):
        if side_by_side(items, os.path.join(folder, name)):
            made.append(name)
    with open(os.path.join(folder, "compare-notes.txt"), "w", encoding="utf-8") as f:
        f.write("\n".join(notes) + ("\n" if notes else ""))
    for n in notes:
        print("compose: " + n)
    print("compose: " + ", ".join(made))


if __name__ == "__main__":
    main()
