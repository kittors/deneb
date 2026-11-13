#!/usr/bin/env python3
"""The fourth logo batch (2026-11-13): sources, every size, scene shots.

    python3 -I design/brand/batch4/build.py [out-dir]

Three directions, each a module: dir_a (水晶星), dir_b (光环), dir_c (小星).
This writes their sources to design/brand/batch4/src/<A|B|C>/, then into
out-dir (default ~/Desktop/Deneb组网demo/品牌/第4批-重做/), one folder per
direction:

  各尺寸/macOS/     the iconset (16-1024; 16 and 32 the small cut) and Deneb.icns
  各尺寸/Windows/   16-256 (16-32 the small cut) and Deneb.ico
  各尺寸/菜单栏/     the three states, 18 px and @2x
  各尺寸/托盘/       the three states at 16, 20, 24, 32 px, dark and light taskbar
  and the scene shots scenes.py composes (Dock, menu bar, taskbar, sheets).

Rendering is headless Chrome (render.mjs); the Dock shots use the icons this
Mac draws itself (sysart.swift: NSWorkspace, as Finder and the Dock do), so
macOS 27's own glass edge is in them.
"""
import json
import os
import shutil
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import dir_a  # noqa: E402
import dir_b  # noqa: E402
import dir_c  # noqa: E402

DIRS = [("A", "水晶星", dir_a), ("B", "光环", dir_b), ("C", "小星", dir_c)]
STATES = [("solid", "连着"), ("dot", "要你处理"), ("hollow", "连不了")]
TMP = "/tmp/deneb-logo4"
SYSART = os.path.join(TMP, "sysart")


def ext_of(src):
    return "html" if src.lstrip().lower().startswith("<!doctype") else "svg"


def write(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as fh:
        fh.write(text)
    return path


def sources(key, mod):
    """Write one direction's sources; return {name: path}."""
    d = os.path.join(HERE, "src", key)
    out = {}

    def put(name, src):
        out[name] = write(os.path.join(d, f"{name}.{ext_of(src)}"), src)

    put("app-macos", mod.app_icon_macos())
    put("app-macos-small", mod.app_icon_macos_small())
    put("app-windows", mod.app_icon_windows())
    put("app-windows-small", mod.app_icon_windows_small())
    for st, _ in STATES:
        put(f"menubar-{st}", mod.menubar_svg(st))
        for dark in (True, False):
            tray = mod.tray_html(st, dark) if hasattr(mod, "tray_html") else mod.tray_svg(st, dark)
            put(f"tray-{st}-{'dark' if dark else 'light'}", tray)
    return out


def render(jobs):
    p = os.path.join(TMP, "jobs.json")
    with open(p, "w") as fh:
        json.dump(jobs, fh)
    subprocess.run(["node", os.path.join(HERE, "render.mjs"), p], check=True)


# macOS iconset: (file, px, cut)
ICONSET = [("icon_16x16.png", 16, "small"), ("icon_16x16@2x.png", 32, "small"), ("icon_32x32.png", 32, "small"),
           ("icon_32x32@2x.png", 64, "big"), ("icon_128x128.png", 128, "big"), ("icon_128x128@2x.png", 256, "big"),
           ("icon_256x256.png", 256, "big"), ("icon_256x256@2x.png", 512, "big"), ("icon_512x512.png", 512, "big"),
           ("icon_512x512@2x.png", 1024, "big")]
WIN = [(16, "small"), (24, "small"), (32, "small"), (48, "big"), (64, "big"), (128, "big"), (256, "big")]
TRAY_PX = [16, 20, 24, 32]


def sizes(key, name, src, out):
    """Render every size of one direction into out/各尺寸 and TMP."""
    base = os.path.join(out, "各尺寸")
    iconset = os.path.join(TMP, key, "Deneb.iconset")
    os.makedirs(iconset, exist_ok=True)
    jobs = []
    for fn, px, cut in ICONSET:
        jobs.append({"src": src["app-macos-small" if cut == "small" else "app-macos"], "w": px, "h": px, "scale": 1,
                     "out": os.path.join(iconset, fn)})
    for px, cut in WIN:
        jobs.append({"src": src["app-windows-small" if cut == "small" else "app-windows"], "w": px, "h": px,
                     "scale": 1, "out": os.path.join(base, "Windows", f"{px}.png")})
    jobs.append({"src": src["app-windows"], "w": 1024, "h": 1024, "scale": 1, "out": os.path.join(TMP, key, "win-1024.png")})
    jobs.append({"src": src["app-windows"], "w": 36, "h": 36, "scale": 1, "out": os.path.join(TMP, key, "win-36.png")})
    for st, zh in STATES:
        for sc, suf in ((1, ""), (2, "@2x")):
            jobs.append({"src": src[f"menubar-{st}"], "w": 18, "h": 18, "scale": sc,
                         "out": os.path.join(base, "菜单栏", f"{zh}{suf}.png")})
        for dark in (True, False):
            for px in TRAY_PX:
                jobs.append({"src": src[f"tray-{st}-{'dark' if dark else 'light'}"], "w": px, "h": px, "scale": 1,
                             "out": os.path.join(base, "托盘", f"{zh}_{'深色' if dark else '浅色'}任务栏_{px}.png")})
    render(jobs)
    mac = os.path.join(base, "macOS")
    os.makedirs(mac, exist_ok=True)
    for fn, *_ in ICONSET:
        shutil.copy(os.path.join(iconset, fn), mac)
    subprocess.run(["iconutil", "-c", "icns", iconset, "-o", os.path.join(mac, "Deneb.icns")], check=True)
    w = os.path.join(base, "Windows")
    subprocess.run(["magick", *[os.path.join(w, f"{px}.png") for px, _ in WIN], os.path.join(w, "Deneb.ico")], check=True)


def system_icon(key):
    """The icon this Mac draws for an app with the direction's .icns, at
    256 and 512 px: a throwaway bundle in TMP, read through NSWorkspace."""
    app = os.path.join(TMP, "apps", f"Deneb{key}.app")
    shutil.rmtree(app, ignore_errors=True)
    os.makedirs(os.path.join(app, "Contents", "Resources"))
    os.makedirs(os.path.join(app, "Contents", "MacOS"))
    with open(os.path.join(app, "Contents", "Info.plist"), "w") as fh:
        fh.write(f"""<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>dev.deneb.logo4.{key.lower()}.{int(time.time())}</string>
<key>CFBundleName</key><string>Deneb</string><key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleIconFile</key><string>Deneb</string><key>CFBundleExecutable</key><string>Deneb</string>
</dict></plist>
""")
    shutil.copy(os.path.join(OUT_DIRS[key], "各尺寸", "macOS", "Deneb.icns"),
                os.path.join(app, "Contents", "Resources", "Deneb.icns"))
    # Something to run, or macOS draws the icon crossed out as a broken app.
    shutil.copy("/usr/bin/true", os.path.join(app, "Contents", "MacOS", "Deneb"))
    for px in (256, 512):
        subprocess.run([SYSART, "icon", os.path.join(TMP, key, f"sys-{px}.png"), str(px), app], check=True)


OUT_DIRS = {}


def main():
    out = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/Desktop/Deneb组网demo/品牌/第4批-重做")
    os.makedirs(TMP, exist_ok=True)
    if not os.path.exists(SYSART):
        subprocess.run(["swiftc", "-O", "-o", SYSART, os.path.join(HERE, "sysart.swift")], check=True)
    stamp = time.strftime("%Y%m%d-%H%M")
    srcs = {}
    for key, zh, mod in DIRS:
        d = os.path.join(out, f"{key}-{zh}")
        shutil.rmtree(os.path.join(d, "各尺寸"), ignore_errors=True)
        OUT_DIRS[key] = d
        srcs[key] = sources(key, mod)
        sizes(key, zh, srcs[key], d)
        system_icon(key)
    import scenes  # noqa: E402
    scenes.build(out, stamp, DIRS, srcs, OUT_DIRS, TMP)


if __name__ == "__main__":
    main()
