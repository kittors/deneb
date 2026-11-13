#!/usr/bin/env python3
"""Every file of Deneb's mark (c.py, the little star), and the copies the
app builds with. Run by build-final.sh:

    python3 -I design/brand/final_c.py

Writes design/brand/final/:
  macos/    app-icon*.svg, AppIcon.iconset/ (16-1024), Deneb.icns, icon-1024.png
  windows/  tile*.svg, deneb-16 ... deneb-256.png, Deneb.ico, deneb-1024.png
  linux/    deneb-16 ... deneb-512.png
  menubar/  the three states' templates: <state>-18.svg, -36.svg, <state>.png
            (18 px), <state>@2x.png (36 px), <state>.tiff (both, for macOS)
  tray/     the three states in colour, dark and light taskbar, 16 20 24 32 px
  web/      favicon.svg (and 16, 32 px), apple-touch-icon.svg / .png (180 px)
  mark/     the one-colour marks: full and small, brand blue, black, white
  glyphs.txt  the paths the app and the web draw, and the favicon's data URI

and copies into cmd/deneb/: Deneb.icns, icon.png (Windows' 1024, which MyGo
packs for Windows and Linux), icon-dev.png (the macOS one), trayicons/
(<state>.tiff for the menu bar, color/ for Windows and Linux).

Needs headless Chrome and node (batch4/render.mjs), iconutil and tiffutil
(macOS), ImageMagick (the .ico).
"""
import json
import os
import shutil
import subprocess
import sys
import urllib.parse

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import c  # noqa: E402

F = os.path.join(HERE, "final")
APP = os.path.join(HERE, "..", "..", "cmd", "deneb")
STATES = ("solid", "dot", "hollow")
TRAY_PX = (16, 20, 24, 32)


def write(rel, text):
    p = os.path.join(F, rel)
    os.makedirs(os.path.dirname(p), exist_ok=True)
    with open(p, "w") as fh:
        fh.write(text)
    return p


def render(jobs):
    p = os.path.join(F, ".jobs.json")
    with open(p, "w") as fh:
        json.dump(jobs, fh)
    try:
        subprocess.run(["node", os.path.join(HERE, "batch4", "render.mjs"), p], check=True)
    finally:
        os.remove(p)


def main():
    shutil.rmtree(F, ignore_errors=True)
    jobs = []

    def job(src, px, out, scale=1):
        jobs.append({"src": src, "w": px, "h": px, "scale": scale, "out": os.path.join(F, out)})

    # macOS: the iconset (16 and 32 px their own cuts), the 1024 alone.
    mac = write("macos/app-icon.svg", c.app_icon_macos())
    m16 = write("macos/app-icon-16.svg", c.app_icon_small_macos(16))
    m32 = write("macos/app-icon-32.svg", c.app_icon_small_macos(32))
    for fn, px, src in (("icon_16x16.png", 16, m16), ("icon_16x16@2x.png", 32, m32), ("icon_32x32.png", 32, m32),
                        ("icon_32x32@2x.png", 64, mac), ("icon_128x128.png", 128, mac), ("icon_128x128@2x.png", 256, mac),
                        ("icon_256x256.png", 256, mac), ("icon_256x256@2x.png", 512, mac), ("icon_512x512.png", 512, mac),
                        ("icon_512x512@2x.png", 1024, mac)):
        job(src, px, "macos/AppIcon.iconset/" + fn)
    job(mac, 1024, "macos/icon-1024.png")

    # Windows: no tile; 16, 24, 32 px their own cuts.
    win = write("windows/tile.svg", c.app_icon_windows())
    for px in (16, 24, 32):
        job(write(f"windows/tile-{px}.svg", c.app_icon_small_windows(px)), px, f"windows/deneb-{px}.png")
    for px in (48, 64, 128, 256, 1024):
        job(win, px, f"windows/deneb-{px}.png")

    # Linux: the hicolor sizes, from Windows' cuts.
    for px in (16, 24, 32):
        job(os.path.join(F, f"windows/tile-{px}.svg"), px, f"linux/deneb-{px}.png")
    for px in (48, 64, 128, 256, 512):
        job(win, px, f"linux/deneb-{px}.png")

    # The menu bar: templates at 18 and 36 px, each its own grid.
    for st in STATES:
        job(write(f"menubar/{st}-18.svg", c.menubar_svg(st, 18)), 18, f"menubar/{st}.png")
        job(write(f"menubar/{st}-36.svg", c.menubar_svg(st, 36)), 36, f"menubar/{st}@2x.png")

    # The tray in colour, Windows and Linux.
    for st in STATES:
        for dark in (True, False):
            for px in TRAY_PX:
                name = f"tray/{st}-{'dark' if dark else 'light'}-{px}"
                job(write(name + ".svg", c.tray_svg(st, dark, px)), px, name + ".png")

    # The web.
    fav = write("web/favicon.svg", c.favicon_svg())
    job(fav, 16, "web/favicon-16.png")
    job(fav, 32, "web/favicon-32.png", scale=1)
    job(write("web/apple-touch-icon.svg", c.apple_touch_svg()), 180, "web/apple-touch-icon.png")

    # One-colour marks.
    for name, fill in (("", "#2f6bff"), ("-black", "#000"), ("-white", "#fff")):
        write(f"mark/deneb-mark{name}.svg", c.glyph_full_svg(fill) + "\n")
        write(f"mark/deneb-mark-small{name}.svg", c.glyph_svg(fill) + "\n")

    render(jobs)

    subprocess.run(["iconutil", "-c", "icns", os.path.join(F, "macos/AppIcon.iconset"),
                    "-o", os.path.join(F, "macos/Deneb.icns")], check=True)
    subprocess.run(["magick", *[os.path.join(F, f"windows/deneb-{px}.png") for px in (16, 24, 32, 48, 64, 128, 256)],
                    os.path.join(F, "windows/Deneb.ico")], check=True)
    for st in STATES:
        subprocess.run(["tiffutil", "-cathidpicheck", os.path.join(F, f"menubar/{st}.png"),
                        os.path.join(F, f"menubar/{st}@2x.png"), "-out", os.path.join(F, f"menubar/{st}.tiff")],
                       check=True, stderr=subprocess.DEVNULL)

    # glyphs.txt: what the app and the web draw.
    tg = c.tray_glyph_svgs()
    fav_uri = "data:image/svg+xml," + urllib.parse.quote(c.favicon_svg().strip(), safe=" =:/,;'()-.")
    write("glyphs.txt", "\n".join([
        "# Deneb's mark (design/brand/c.py). Copied by hand into the places named; build-final.sh writes this.",
        "",
        "## starGlyph (cmd/deneb/glyphs.go), G.star (web/app/src/v3-glyphs.ts), star() (web/app/src/download.ts)",
        "## 18 units, fill-rule evenodd: the star and its eyes",
        c.glyph_small_d(),
        "",
        "## markGlyph (cmd/deneb/glyphs.go): the full cut, 100 units, evenodd",
        c.glyph_full_d(),
        "",
        "## trayGlyph (cmd/deneb/trayglyph.go): the three states, 18 units",
        *[f"{k}: {v}" for k, v in tg.items()],
        "",
        "## favicon (web/app/index.html <link rel=icon>)",
        fav_uri,
        "",
    ]))

    # Into the app.
    shutil.copy(os.path.join(F, "macos/Deneb.icns"), os.path.join(APP, "Deneb.icns"))
    shutil.copy(os.path.join(F, "windows/deneb-1024.png"), os.path.join(APP, "icon.png"))
    shutil.copy(os.path.join(F, "macos/icon-1024.png"), os.path.join(APP, "icon-dev.png"))
    ti = os.path.join(APP, "trayicons")
    shutil.rmtree(ti, ignore_errors=True)
    os.makedirs(os.path.join(ti, "color"))
    for st in STATES:
        shutil.copy(os.path.join(F, f"menubar/{st}.tiff"), os.path.join(ti, f"{st}.tiff"))
        for dark in ("dark", "light"):
            for px in TRAY_PX:
                shutil.copy(os.path.join(F, f"tray/{st}-{dark}-{px}.png"), os.path.join(ti, "color", f"{st}-{dark}-{px}.png"))
    print("final/ written; copied into cmd/deneb")


if __name__ == "__main__":
    main()
