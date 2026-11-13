#!/usr/bin/env python3
"""Review sheets for the second logo (batch 3, 2026-11-11), next to the
first one (v1/), rendered by headless Chrome:

    python3 -I design/brand/review.py <out-dir>

  overview.png   old and new side by side: the app icon, the marks, the
                 sizes, the menu bar, the lockups (2x)
  sizes-light.png, sizes-dark.png
                 the app icon at 512 to 16 px, each its real pixels (1x),
                 on a light and a dark desktop, 32 and 16 blown up
  menubar.png    the macOS menu bar, light and dark, old and new (2x)
  windows.png    the Windows taskbar, dark and light: the app and the tray
  linux.png      GNOME's top bar and dash
  tabs.png       a browser's tabs, light and dark, at 1x and 2x

Everything is read from final/ and v1/ (run build-final.sh first). The
other icons in the bars are plain shapes standing in for the system's.
"""
import os
import re
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
F, V1 = os.path.join(HERE, "final"), os.path.join(HERE, "v1")
CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

CSS = """
* { box-sizing: border-box; }
body { margin: 0; font-family: -apple-system, 'PingFang SC', sans-serif; color: #18181b; background: #f4f4f6; }
.page { padding: 40px 44px; }
h1 { font-size: 26px; margin: 0 0 6px; letter-spacing: -.2px; } p.sub { margin: 0 0 26px; color: #71717a; font-size: 15px; }
.row { display: flex; gap: 20px; align-items: stretch; margin-bottom: 20px; }
.card { background: #fff; border: 1px solid #e4e4e7; border-radius: 20px; padding: 18px 20px; box-shadow: 0 6px 18px rgba(0,0,0,.05); }
.card.dark { background: #18181b; border-color: #27272a; color: #e4e4e7; }
.card.grey { background: #ececef; }
.lab { font-size: 13px; color: #71717a; margin-bottom: 12px; } .dark .lab { color: #a1a1aa; }
.tag { display: inline-block; font-size: 12px; padding: 2px 8px; border-radius: 999px; background: #f4f4f5; color: #52525b; margin-left: 6px; }
.tag.new { background: #e8efff; color: #2457e0; }
.sizes { display: flex; align-items: flex-end; gap: 18px; }
.sizes div { text-align: center; font-size: 11px; color: #a1a1aa; }
.sizes img { display: block; margin: 0 auto 6px; }
img { display: block; }
"""


def uri(path):
    return "file://" + path


def page(body, width):
    return (f'<!doctype html><meta charset="utf-8"><style>{CSS}</style>'
            f'<div class="page" style="width:{width}px">{body}</div>')


def svg_tinted(path, color):
    """An SVG file as a data URI in one colour: its dark-appearance rule
    dropped, every colour in it set to color."""
    s = open(path).read()
    s = re.sub(r"@media \(prefers-color-scheme:dark\)\{[^{}]*\{[^{}]*\}\}", "", s)
    s = re.sub(r"#[0-9a-fA-F]{6}\b", color, s)
    return "data:image/svg+xml;utf8," + s.replace("#", "%23").replace('"', "'").replace("\n", "")


# ---------------------------------------------------------------- overview

def overview():
    def icon_pair(v, label, tag):
        return (f'<div class="card" style="flex:1"><div class="lab">{label}<span class="tag{" new" if tag == "新" else ""}">{tag}</span></div>'
                f'<img src="{uri(v + "/macos/icon-1024.png")}" width="380" height="380" style="margin:0 auto"></div>')

    def marks(v, tag):
        out = ""
        for label, name, cls in (("品牌蓝", "deneb-mark.svg", ""), ("纯黑", "deneb-mark-black.svg", "grey"), ("纯白", "deneb-mark-white.svg", "dark")):
            out += (f'<div class="card {cls}" style="flex:1"><div class="lab">{label}<span class="tag{" new" if tag == "新" else ""}">{tag}</span></div>'
                    f'<img src="{uri(v + "/mark/" + name)}" width="150" height="150" style="margin:0 auto"></div>')
        return out

    def sizes(v):
        s = v + "/macos/AppIcon.iconset/"
        cells = ""
        for pt, f in ((128, "icon_128x128@2x.png"), (64, "icon_32x32@2x.png"), (32, "icon_16x16@2x.png"), (16, "icon_16x16@2x.png")):
            cells += f'<div><img src="{uri(s + f)}" width="{pt}" height="{pt}">{pt} 点</div>'
        return cells

    def tray(v, dark):
        imgs = "".join(f'<img src="{uri(v + "/tray/" + st + ".png")}" width="18" height="18" style="{"filter:invert(1)" if dark else ""}">'
                       for st in ("solid", "dot", "hollow"))
        bg = "#2c2c2e" if dark else "#ececef"
        return f'<div style="display:flex;gap:16px;align-items:center;height:30px;padding:0 14px;border-radius:8px;background:{bg};margin-bottom:8px">{imgs}</div>'

    body = ('<h1>Deneb logo · 第 3 批：精致版</h1>'
            '<p class="sub">2026-11-11 · 还是这颗亮星，重画了比例、曲线和光 · 全套在 design/brand/final/，旧版留在 design/brand/v1/，规则在 DESIGN §15</p>'
            '<div class="row">' + icon_pair(V1, "应用图标（macOS）", "旧") + icon_pair(F, "应用图标（macOS）", "新") + '</div>'
            '<div class="row">' + marks(V1, "旧") + '</div>'
            '<div class="row">' + marks(F, "新") + '</div>'
            '<div class="row">'
            f'<div class="card" style="flex:1"><div class="lab">Retina 屏上的大小<span class="tag">旧</span></div><div class="sizes">{sizes(V1)}</div></div>'
            f'<div class="card" style="flex:1"><div class="lab">Retina 屏上的大小<span class="tag new">新</span></div><div class="sizes">{sizes(F)}</div></div>'
            '</div><div class="row">'
            f'<div class="card" style="flex:1"><div class="lab">菜单栏：连着 · 要你处理 · 连不了<span class="tag">旧</span></div>{tray(V1, False)}{tray(V1, True)}</div>'
            f'<div class="card" style="flex:1"><div class="lab">菜单栏：连着 · 要你处理 · 连不了<span class="tag new">新</span></div>{tray(F, False)}{tray(F, True)}</div>'
            '</div><div class="row">'
            f'<div class="card" style="flex:1"><div class="lab">横排组合<span class="tag new">新</span></div><img src="{uri(F + "/lockup/deneb-lockup.svg")}" style="width:100%;max-width:520px"></div>'
            f'<div class="card dark" style="flex:1"><div class="lab">横排组合：深底<span class="tag new">新</span></div><img src="{uri(F + "/lockup/deneb-lockup-dark.svg")}" style="width:100%;max-width:520px"></div>'
            '</div>')
    return page(body, 1100)


# ---------------------------------------------------------------- sizes (1x)

def sizes(dark):
    bg, ink = ("#1e1e22", "#a1a1aa") if dark else ("#e9e9ee", "#71717a")
    s = F + "/macos/AppIcon.iconset/"
    files = ((512, "icon_512x512.png"), (256, "icon_256x256.png"), (128, "icon_128x128.png"), (64, "icon_32x32@2x.png"),
             (32, "icon_32x32.png"), (16, "icon_16x16.png"))
    cells = "".join(f'<div><img src="{uri(s + f)}" width="{n}" height="{n}">{n}</div>' for n, f in files)
    zoom = "".join(f'<div><img src="{uri(s + f)}" width="{n * k}" height="{n * k}" style="image-rendering:pixelated">{n} 像素，放大 {k} 倍</div>'
                   for n, f, k in ((32, "icon_32x32.png", 6), (16, "icon_16x16.png", 12)))
    win = "".join(f'<div><img src="{uri(F + "/windows/deneb-" + str(n) + ".png")}" width="{n}" height="{n}">{n}</div>' for n in (256, 64, 48, 32, 24, 16))
    css = f"body{{background:{bg}}} .sizes div{{color:{ink}}} .lab{{color:{ink}}}"
    body = (f'<style>{css}</style><div class="lab">macOS 应用图标，每个尺寸都是它自己的像素（48 以上完整版，22 到 32 小尺寸版，16 单独对齐像素）</div>'
            f'<div class="sizes" style="gap:28px">{cells}</div><div style="height:28px"></div>'
            f'<div class="sizes" style="gap:40px">{zoom}</div><div style="height:28px"></div>'
            f'<div class="lab">Windows、Linux（方块铺满，没有阴影）</div><div class="sizes" style="gap:28px">{win}</div>')
    return page(body, 1240)


# ---------------------------------------------------------------- the bars

WIFI = ('<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="{c}" stroke-width="2.1" stroke-linecap="round">'
        '<path d="M3 9.5a13 13 0 0 1 18 0M6 13a8.5 8.5 0 0 1 12 0M9 16.5a4 4 0 0 1 6 0"/><circle cx="12" cy="20" r="1.3" fill="{c}" stroke="none"/></svg>')
BATT = ('<svg width="24" height="16" viewBox="0 0 30 20" fill="none" stroke="{c}" stroke-width="1.6"><rect x="1.5" y="4" width="23" height="12" rx="3.4" opacity=".45"/>'
        '<rect x="3.6" y="6.1" width="15" height="7.8" rx="1.8" fill="{c}" stroke="none"/><path d="M27 8.3v3.4" stroke-linecap="round" opacity=".45"/></svg>')
VOL = ('<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="{c}" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round">'
       '<path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z" fill="{c}"/><path d="M15.5 9a4.5 4.5 0 0 1 0 6M18.5 6.5a8 8 0 0 1 0 11"/></svg>')
CHEV = ('<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="{c}" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">'
        '<path d="m7 14 5-5 5 5"/></svg>')


def tray_img(v, st, size, white):
    return (f'<img src="{uri(v + "/tray/" + st + ".png")}" width="{size}" height="{size}"'
            + (' style="filter:invert(1)"' if white else "") + ">")


def menubar():
    def bar(v, dark, tag):
        c = "#fff" if dark else "#000"
        bg = "linear-gradient(#3a3a3c,#2e2e30)" if dark else "linear-gradient(#f6f6f7,#ececee)"
        items = "".join(f'<span style="display:flex;width:26px;justify-content:center">{tray_img(v, st, 18, dark)}</span>' for st in ("solid", "dot", "hollow"))
        return (f'<div style="display:flex;align-items:center;justify-content:flex-end;gap:14px;height:28px;padding:0 14px;border-radius:10px;background:{bg};color:{c};font-size:13px;margin-bottom:10px">'
                f'<span style="margin-right:auto;font-size:12px;opacity:.5">{tag}</span>{items}<span style="display:flex">{WIFI.format(c=c)}</span>'
                f'<span style="display:flex">{BATT.format(c=c)}</span><span style="font-weight:500">11月11日 周三 09:41</span></div>')
    body = ('<div class="card" style="width:640px"><div class="lab">macOS 菜单栏：连着 · 要你处理（右下角一个点，外面空一圈）· 连不了（只有轮廓）；单色，跟着系统染色</div>'
            + bar(V1, False, "旧") + bar(F, False, "新") + bar(V1, True, "旧") + bar(F, True, "新") + '</div>')
    return page(body, 728)


def windows():
    def taskbar(dark):
        c = "#fff" if dark else "#1b1b1b"
        bg = "#1c1c1c" if dark else "#eef0f4"
        ph = "#3b3b3b" if dark else "#d5d8de"
        apps = "".join(f'<span style="width:24px;height:24px;border-radius:6px;background:{ph};display:block"></span>' for _ in range(3))
        app = f'<span style="position:relative;display:flex;flex-direction:column;align-items:center"><img src="{uri(F + "/windows/deneb-48.png")}" width="24" height="24"><span style="position:absolute;bottom:-9px;width:16px;height:3px;border-radius:2px;background:#4c82ff"></span></span>'
        tray = "".join(f'<span style="display:flex;width:28px;justify-content:center">{tray_img(F, st, 16, dark)}</span>' for st in ("solid", "dot", "hollow"))
        return (f'<div style="display:flex;align-items:center;height:48px;padding:0 12px;background:{bg};border-radius:10px;color:{c};margin-bottom:12px">'
                f'<span style="flex:1"></span><span style="display:flex;gap:20px;align-items:center">{apps}{app}</span><span style="flex:1"></span>'
                f'<span style="display:flex;align-items:center;gap:6px">{CHEV.format(c=c)}{tray}'
                f'<span style="display:flex;gap:10px;margin-left:8px">{WIFI.format(c=c)}{VOL.format(c=c)}</span>'
                f'<span style="font-size:12px;line-height:16px;text-align:right;margin-left:12px">09:41<br>2026/11/11</span></span></div>')
    body = ('<div class="card" style="width:720px"><div class="lab">Windows 11 任务栏：中间是应用图标（Deneb.ico 里的方块），右边托盘是三种状态（深色任务栏白色，浅色黑色）</div>'
            + taskbar(True) + taskbar(False) + '</div>')
    return page(body, 808)


def linux():
    c = "#fff"
    bar = (f'<div style="display:flex;align-items:center;height:32px;padding:0 14px;background:#000;color:{c};font-size:13px;border-radius:10px 10px 0 0">'
           '<span style="font-weight:600">活动</span><span style="flex:1;text-align:center;font-weight:600">11月11日 09:41</span>'
           + "".join(f'<span style="display:flex;width:26px;justify-content:center">{tray_img(F, st, 16, True)}</span>' for st in ("solid", "dot", "hollow"))
           + f'<span style="display:flex;gap:10px;margin-left:10px">{WIFI.format(c=c)}{VOL.format(c=c)}</span></div>')
    dash = ('<div style="height:150px;background:linear-gradient(#41365c,#1f1b2c);border-radius:0 0 10px 10px;display:flex;align-items:flex-end;justify-content:center;padding-bottom:14px">'
            '<div style="display:flex;gap:14px;padding:10px 14px;background:rgba(40,40,44,.85);border-radius:20px">'
            + "".join('<span style="width:48px;height:48px;border-radius:12px;background:#4a4a50;display:block"></span>' for _ in range(3))
            + f'<img src="{uri(F + "/linux/deneb-48.png")}" width="48" height="48"></div></div>')
    body = ('<div class="card" style="width:640px"><div class="lab">Linux（GNOME）：顶栏右边是托盘的三种状态（白色，顶栏在浅色、深色样式里都是黑的）；下面 Dash 里是应用图标</div>'
            + bar + dash + '</div>')
    return page(body, 728)


def tabs():
    def strip(dark):
        c = "#e8eaed" if dark else "#1f1f1f"
        bg, tab, other = ("#202124", "#35363a", "#9aa0a6") if dark else ("#dee1e6", "#ffffff", "#5f6368")
        fav = svg_tinted(F + "/web/favicon.svg", "#4c82ff" if dark else "#2f6bff")
        old = svg_tinted(V1 + "/web/favicon.svg", "#4c82ff" if dark else "#2f6bff")
        t = lambda icon, title, active: (
            f'<span style="display:flex;align-items:center;gap:8px;height:34px;padding:0 14px;width:210px;border-radius:10px 10px 0 0;'
            f'background:{tab if active else "transparent"};color:{c if active else other};font-size:12px">'
            f'<img src="{icon}" width="16" height="16"><span>{title}</span></span>')
        return (f'<div style="display:flex;align-items:flex-end;gap:2px;height:42px;padding:0 10px;background:{bg};border-radius:10px;margin-bottom:10px">'
                + t(old, "Deneb · 我的设备（旧）", False) + t(fav, "Deneb · 我的设备", True)
                + t("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'><circle cx='8' cy='8' r='6.5' fill='%239aa0a6'/></svg>", "新标签页", False)
                + '</div>')
    body = ('<div class="card" style="width:720px"><div class="lab">浏览器标签页：网页小图标是小尺寸版的星，深色浏览器里换成深色界面的主色 #4c82ff</div>'
            + strip(False) + strip(True) + '</div>')
    return page(body, 808)


# ---------------------------------------------------------------- render

def shoot(html, out, width, height, scale):
    with tempfile.NamedTemporaryFile("w", suffix=".html", delete=False, dir=tempfile.gettempdir()) as fh:
        fh.write(html)
    subprocess.run([CHROME, "--headless=new", "--disable-gpu", "--hide-scrollbars", "--no-first-run",
                    f"--user-data-dir={tempfile.gettempdir()}/deneb-logo-chrome", "--allow-file-access-from-files",
                    f"--force-device-scale-factor={scale}", f"--window-size={width},{height}", f"--screenshot={out}",
                    "file://" + fh.name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
    os.unlink(fh.name)
    # Trim the page's empty bottom.
    subprocess.run(["magick", out, "-background", "#f4f4f6", "-gravity", "south", "-chop", "0x0", "-trim", "+repage",
                    "-bordercolor", "#f4f4f6", "-border", str(int(28 * scale)), out], check=False)


def main():
    out = os.path.abspath(sys.argv[1] if len(sys.argv) > 1 else ".")
    os.makedirs(out, exist_ok=True)
    shoot(overview(), os.path.join(out, "overview.png"), 1100, 2600, 2)
    shoot(sizes(False), os.path.join(out, "sizes-light.png"), 1240, 1200, 1)
    shoot(sizes(True), os.path.join(out, "sizes-dark.png"), 1240, 1200, 1)
    shoot(menubar(), os.path.join(out, "menubar.png"), 728, 420, 2)
    shoot(windows(), os.path.join(out, "windows.png"), 808, 360, 2)
    shoot(linux(), os.path.join(out, "linux.png"), 728, 420, 2)
    shoot(tabs(), os.path.join(out, "tabs.png"), 808, 300, 2)
    shoot(tabs(), os.path.join(out, "tabs-1x.png"), 808, 300, 1)
    print(out)


if __name__ == "__main__":
    main()
