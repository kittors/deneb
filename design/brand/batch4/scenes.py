"""Scene shots for the fourth logo batch: each direction in the Dock, the
menu bar and the Windows taskbar, its sheet, its small sizes pixel by
pixel, and the three side by side. build.py calls build().

Every shot comes in a light and a dark version. The other icons are this
Mac's own (its apps' icons as the Dock draws them, SF Symbols); on the
Windows taskbar, plain shapes stand in for other apps. The wallpapers are
soft gradients drawn here.
"""
import json
import os
import subprocess

HERE = os.path.dirname(os.path.abspath(__file__))

WORDS = {
    "A": ("水晶星", "还是 Deneb 这颗星，做成一颗切好的水晶，放在夜空里。每道光芒一半亮一半暗，小到菜单栏里也看得出立体。",
          ["连着：水晶星，亮面实、暗面淡", "要你处理：右下角多一个点", "连不了：只剩轮廓"]),
    "B": ("光环", "界面里连线用的那根玻璃管，弯成一颗星，管里流着每个人的颜色：几个人的设备连成一圈。",
          ["连着：中间镂空的星", "要你处理：右下角多一个点", "连不了：变成虚线，和界面里连不上的线一样"]),
    "C": ("小星", "这颗星变成一个软软的小家伙，只有一双眼睛。连着时睁着眼，连不上时闭眼睡着。",
          ["连着：睁着眼", "要你处理：右下角多一个点", "连不了：闭眼、变淡，像睡着了"]),
}
STATES = [("solid", "连着"), ("dot", "要你处理"), ("hollow", "连不了")]

FONT = "-apple-system, 'PingFang SC', 'Helvetica Neue', sans-serif"
WALL = {
    False: ("radial-gradient(1200px 700px at 15% 10%, #ffffff 0%, rgba(255,255,255,0) 60%),"
            "radial-gradient(900px 600px at 85% 90%, #f9dfe9 0%, rgba(249,223,233,0) 65%),"
            "linear-gradient(160deg, #dfe8ff 0%, #cfdcff 40%, #e6dcfb 72%, #f6e2ec 100%)"),
    True: ("radial-gradient(1100px 700px at 20% 0%, #2a3a80 0%, rgba(42,58,128,0) 60%),"
           "radial-gradient(900px 600px at 85% 100%, #3a2560 0%, rgba(58,37,96,0) 65%),"
           "linear-gradient(160deg, #0f1733 0%, #18214f 45%, #241a46 75%, #10152c 100%)"),
}

SYS_APPS = [("Finder", "/System/Library/CoreServices/Finder.app"), ("Messages", "/System/Applications/Messages.app"),
            ("Mail", "/System/Applications/Mail.app"), ("Notes", "/System/Applications/Notes.app"),
            ("Calendar", "/System/Applications/Calendar.app"), ("Photos", "/System/Applications/Photos.app"),
            ("App_Store", "/System/Applications/App Store.app"), ("Music", "/System/Applications/Music.app"),
            ("System_Settings", "/System/Applications/System Settings.app")]
SYMBOLS = ["wifi", "battery.100percent", "switch.2", "magnifyingglass", "apple.logo", "speaker.wave.2.fill", "chevron.up"]


def u(p):
    return "file://" + p


def ensure_sys(tmp):
    d = os.path.join(tmp, "sys")
    os.makedirs(d, exist_ok=True)
    art = os.path.join(tmp, "sysart")
    for n, p in SYS_APPS:
        o = os.path.join(d, n + ".png")
        if not os.path.exists(o):
            subprocess.run([art, "icon", o, "256", p], check=True)
    for s in SYMBOLS:
        for pt in (13, 15, 20):
            o = os.path.join(d, f"sym-{s}-{pt}.png")
            if not os.path.exists(o):
                subprocess.run([art, "symbol", o, str(pt), "2", s, "medium" if pt == 13 else "regular"], check=True)
    return d


def symbol(sysd, name, pt, color, h):
    """An SF Symbol tinted, h points tall (keeping its own width)."""
    p = os.path.join(sysd, f"sym-{name}-{pt}.png")
    out = subprocess.run(["sips", "-g", "pixelWidth", "-g", "pixelHeight", p], capture_output=True, text=True).stdout
    w = int(out.split("pixelWidth:")[1].split()[0])
    hh = int(out.split("pixelHeight:")[1].split()[0])
    width = h * w / hh
    return (f'<span style="display:inline-block;width:{width:.1f}px;height:{h}px;background:{color};'
            f'-webkit-mask:url({u(p)}) center/100% 100% no-repeat"></span>')


def tmpl(src, w, h, color, pixelated=False):
    """A template image tinted as macOS tints it."""
    r = "image-rendering:pixelated;" if pixelated else ""
    return (f'<span style="display:inline-block;width:{w}px;height:{h}px;background:{color};{r}'
            f'-webkit-mask:url({u(src)}) center/100% 100% no-repeat"></span>')


CSS = f"""
*{{box-sizing:border-box}} html,body{{height:100%}} body{{margin:0;font-family:{FONT};-webkit-font-smoothing:antialiased}}
.lab{{font-size:15px;margin-bottom:14px;opacity:.75}}
.cap{{font-size:12px;opacity:.6;text-align:center;margin-top:8px}}
.pix{{image-rendering:pixelated;display:block}}
"""


def page(body, bg, color):
    return (f'<!doctype html><meta charset="utf-8"><style>{CSS}</style>'
            f'<body style="background:{bg};color:{color}">{body}</body>')


class Shots:
    def __init__(self, tmp):
        self.tmp = tmp
        self.jobs = []
        os.makedirs(os.path.join(tmp, "html"), exist_ok=True)

    def add(self, name, html, w, h, out, scale=2):
        p = os.path.join(self.tmp, "html", name + ".html")
        with open(p, "w") as fh:
            fh.write(html)
        self.jobs.append({"src": p, "w": w, "h": h, "scale": scale, "out": out, "wait": 250})

    def run(self):
        p = os.path.join(self.tmp, "jobs-scenes.json")
        with open(p, "w") as fh:
            json.dump(self.jobs, fh)
        subprocess.run(["node", os.path.join(HERE, "render.mjs"), p], check=True)
        self.jobs = []


# ---------------------------------------------------------------- the Dock

def dock(sysd, deneb, dark, label):
    icons = [os.path.join(sysd, n + ".png") for n, _ in SYS_APPS]
    icons.insert(3, deneb)
    ink = "#fff" if dark else "#1d1d1f"
    bar_bg = "rgba(36,36,44,.48)" if dark else "rgba(255,255,255,.36)"
    edge = "rgba(255,255,255,.14)" if dark else "rgba(255,255,255,.7)"
    cells = []
    for i, ic in enumerate(icons):
        op = ".85" if i in (0, 3) else "0"
        run = f'<div style="width:5px;height:5px;border-radius:50%;margin:5px auto 0;background:{ink};opacity:{op}"></div>'
        tip = ""
        if i == 3:
            tip = (f'<div style="position:absolute;left:50%;top:-46px;transform:translateX(-50%);padding:5px 12px;border-radius:9px;'
                   f'font-size:14px;white-space:nowrap;background:{"rgba(40,40,46,.9)" if dark else "rgba(246,246,248,.92)"};'
                   f'color:{ink};box-shadow:0 2px 10px rgba(0,0,0,.18)">Deneb</div>')
        cells.append(f'<div style="position:relative;width:80px">{tip}<img src="{u(ic)}" style="width:80px;height:80px;display:block">{run}</div>')
    return (f'<div style="height:100%;display:flex;flex-direction:column;justify-content:space-between;padding:34px 40px 22px;'
            f'background:{WALL[dark]}"><div class="lab" style="color:{ink}">{label}</div>'
            f'<div style="align-self:center;display:flex;gap:9px;padding:9px 12px 4px;border-radius:30px;background:{bar_bg};'
            f'border:1px solid {edge};backdrop-filter:blur(30px) saturate(1.6);box-shadow:0 10px 30px rgba(0,0,0,.12)">'
            f'{"".join(cells)}</div></div>')


# ------------------------------------------------------------ the menu bar

def menubar_strip(sysd, mb_png, dark, width=1180, note=True):
    ink = "#fff" if dark else "rgba(0,0,0,.86)"
    menus = "".join(f'<span style="margin-right:20px">{m}</span>' for m in ("文件", "编辑", "显示", "前往", "窗口", "帮助"))
    right = [tmpl(mb_png, 18, 18, ink), symbol(sysd, "battery.100percent", 15, ink, 13), symbol(sysd, "wifi", 15, ink, 13),
             symbol(sysd, "magnifyingglass", 15, ink, 14), symbol(sysd, "switch.2", 15, ink, 13)]
    tag = (f'<span style="position:absolute;top:27px;left:50%;transform:translateX(-50%);white-space:nowrap;font-size:13px;'
           f'opacity:.8">↑ Deneb</span>') if note else ""
    r = "".join(f'<span style="position:relative;display:inline-flex;align-items:center;justify-content:center;min-width:30px;'
                f'height:24px">{x}{tag if i == 0 else ""}</span>' for i, x in enumerate(right))
    return (f'<div style="width:{width}px;height:24px;display:flex;align-items:center;justify-content:space-between;'
            f'padding:0 14px 0 20px;color:{ink};font-size:13.5px">'
            f'<div style="display:flex;align-items:center">{symbol(sysd, "apple.logo", 15, ink, 15)}'
            f'<span style="font-weight:700;margin:0 20px 0 22px">访达</span>{menus}</div>'
            f'<div style="display:flex;align-items:center;gap:4px">{r}'
            f'<span style="margin-left:12px">11月13日 周四 14:20</span></div></div>')


def menubar(sysd, mbdir, dark, label):
    ink = "#fff" if dark else "#1d1d1f"
    strip = menubar_strip(sysd, os.path.join(mbdir, "连着@2x.png"), dark)
    zoom = []
    for st, zh in STATES:
        zoom.append(f'<div style="text-align:center"><div style="width:150px;height:150px;border-radius:18px;display:flex;'
                    f'align-items:center;justify-content:center;background:{"rgba(0,0,0,.22)" if dark else "rgba(255,255,255,.45)"}">'
                    f'{tmpl(os.path.join(mbdir, zh + "@2x.png"), 108, 108, "#fff" if dark else "rgba(0,0,0,.86)", True)}</div>'
                    f'<div class="cap" style="color:{ink}">{zh}</div></div>')
    return (f'<div style="height:100%;background:{WALL[dark]};position:relative">{strip}'
            f'<div style="padding:70px 40px 0"><div class="lab" style="color:{ink}">{label}</div>'
            f'<div style="display:flex;gap:28px">{"".join(zoom)}'
            f'<div style="align-self:center;max-width:430px;font-size:14px;line-height:1.7;color:{ink};opacity:.8;margin-left:12px">'
            f'菜单栏用单色模板图，系统自己染色：浅色壁纸上是黑的，深色壁纸上是白的，点开时跟着反色。左边三个是放大 6 倍的样子（每格一个像素，Retina 屏）。</div>'
            f'</div></div></div>')


# -------------------------------------------------------- Windows taskbar

def tray_strip(sysd, tray_png, scale, dark, app_png=None, width=None):
    """The right end of a Windows 11 taskbar at a display scale, in device
    pixels: ^, Deneb, network, volume, battery, the clock."""
    k = scale
    ink = "#fff" if dark else "#1a1a1a"
    bg = "#202020" if dark else "#eef0f3"
    h = round(48 * k)
    ico = round(16 * k)

    def slot(inner, w=None):
        return (f'<span style="display:inline-flex;align-items:center;justify-content:center;width:{w or round(30 * k)}px;'
                f'height:{h}px">{inner}</span>')

    tray = [slot(symbol(sysd, "chevron.up", 13, ink, round(8 * k))),
            slot(f'<img src="{u(tray_png)}" style="width:{ico}px;height:{ico}px;display:block">'),
            slot(symbol(sysd, "wifi", 15, ink, round(12 * k))),
            slot(symbol(sysd, "speaker.wave.2.fill", 15, ink, round(12 * k))),
            slot(symbol(sysd, "battery.100percent", 15, ink, round(10 * k)))]
    clock = (f'<span style="display:inline-flex;flex-direction:column;align-items:flex-end;justify-content:center;height:{h}px;'
             f'padding:0 {round(12 * k)}px 0 {round(8 * k)}px;font-size:{12 * k:.1f}px;line-height:1.35;color:{ink};'
             f'font-family:\'Segoe UI\',{FONT}"><span>14:20</span><span>2026/11/13</span></span>')
    left = ""
    if app_png:
        apps = []
        for i in range(5):
            if i == 3:
                apps.append(f'<span style="display:inline-flex;flex-direction:column;align-items:center;justify-content:center;'
                            f'width:{round(44 * k)}px;height:{h}px;position:relative;border-radius:{round(4 * k)}px;'
                            f'background:{"rgba(255,255,255,.07)" if dark else "rgba(0,0,0,.04)"}">'
                            f'<img src="{u(app_png)}" style="width:{round(24 * k)}px;height:{round(24 * k)}px;display:block">'
                            f'<span style="position:absolute;bottom:{round(3 * k)}px;width:{round(16 * k)}px;height:{round(3 * k)}px;'
                            f'border-radius:{round(2 * k)}px;background:{"#7aa7ff" if dark else "#1d5fd8"}"></span></span>')
            else:
                shade = ("#3a3a3a", "#4a4a4a", "#353535", "", "#444")[i] if dark else ("#c9ccd3", "#d6d8de", "#c2c6ce", "", "#cfd2d8")[i]
                apps.append(f'<span style="display:inline-flex;align-items:center;justify-content:center;width:{round(44 * k)}px;'
                            f'height:{h}px"><span style="width:{round(24 * k)}px;height:{round(24 * k)}px;border-radius:{round(5 * k)}px;'
                            f'background:{shade}"></span></span>')
        left = (f'<div style="position:absolute;left:50%;transform:translateX(-50%);display:flex;gap:{round(2 * k)}px">'
                + "".join(apps) + "</div>")
    w = f"width:{width}px;" if width else ""
    return (f'<div style="position:relative;{w}height:{h}px;background:{bg};display:flex;justify-content:flex-end;'
            f'align-items:center;border-top:1px solid {"#2e2e2e" if dark else "#dcdfe4"}">{left}'
            f'<div style="display:flex;align-items:center">{"".join(tray)}{clock}</div></div>')


def taskbar(sysd, key, sizes_dir, strips, dark, label):
    """The whole taskbar at 150 % in its real pixels (one device pixel
    each), then the tray end at 100, 125, 150 and 200 %, each pixel blown
    up to three."""
    ink = "#fff" if dark else "#1d1d1f"
    rows = []
    for k, pct in ((1.0, "100%"), (1.25, "125%"), (1.5, "150%"), (2.0, "200%")):
        p, w, h = strips[(dark, k)]
        rows.append(f'<div style="display:flex;align-items:center;gap:20px;margin-bottom:14px">'
                    f'<div style="width:118px;font-size:13px;opacity:.75;line-height:1.5">缩放 {pct}<br>托盘图标 {round(16 * k)} 像素</div>'
                    f'<img class="pix" src="{u(p)}" style="width:{w * 1.5:.1f}px;height:{h * 1.5:.1f}px"></div>')
    full, fw, fh = strips[(dark, "full")]
    return (f'<div style="min-height:100%;background:{WALL[dark]};color:{ink};padding:34px 40px 36px">'
            f'<div class="lab">{label}</div>'
            f'<div style="font-size:14px;line-height:1.7;opacity:.8;margin:-4px 0 16px">150% 缩放的整条任务栏，真实像素：'
            f'中间第 4 个是 Deneb 的应用图标，右边 ^ 旁边是托盘图标。</div>'
            f'<img src="{u(full)}" style="width:{fw / 2:.1f}px;height:{fh / 2:.1f}px;display:block;margin-bottom:30px;'
            f'box-shadow:0 8px 24px rgba(0,0,0,.18)">'
            f'<div style="font-size:14px;line-height:1.7;opacity:.8;margin-bottom:14px">托盘那一头在四种缩放下的样子，每个像素放大成 3 格'
            f'（托盘按任务栏深浅各给一版彩色图，"要你处理"的点是橙色）：</div>'
            f'{"".join(rows)}</div>')


# ------------------------------------------------------------- the sheet

def sheet(key, zh, words, states, d, sysicon, win1024, dark):
    ink = "#f4f4f5" if dark else "#18181b"
    sub = "#a1a1aa" if dark else "#71717a"
    card = "#1c1c20" if dark else "#ffffff"
    edge = "#2a2a30" if dark else "#e4e4e7"
    bg = "#111113" if dark else "#f4f4f6"
    S = os.path.join(d, "各尺寸")
    mac = os.path.join(S, "macOS")
    ret = "".join(f'<div style="text-align:center;min-width:56px"><img src="{u(os.path.join(mac, fn))}" style="width:{pt}px;height:{pt}px;display:block;margin:0 auto">'
                  f'<div class="cap">{pt} 点</div></div>'
                  for fn, pt in (("icon_128x128@2x.png", 128), ("icon_32x32@2x.png", 64), ("icon_32x32.png", 32),
                                 ("icon_16x16@2x.png", 16)))
    mbz = []
    for st, zs in STATES:
        for dk in (False, True):
            mbz.append(f'<div style="text-align:center"><div style="width:76px;height:52px;border-radius:12px;display:flex;align-items:center;'
                       f'justify-content:center;background:{"#2b2b30" if dk else "#e6e6e9"}">'
                       f'{tmpl(os.path.join(S, "菜单栏", zs + "@2x.png"), 36, 36, "#fff" if dk else "rgba(0,0,0,.86)")}</div>'
                       f'<div class="cap">{zs}{"·深" if dk else "·浅"}</div></div>')
    trz = []
    for st, zs in STATES:
        for dk in (True, False):
            trz.append(f'<div style="text-align:center"><div style="width:76px;height:52px;border-radius:12px;display:flex;align-items:center;'
                       f'justify-content:center;background:{"#202020" if dk else "#eef0f3"}">'
                       f'<img src="{u(os.path.join(S, "托盘", f"{zs}_{"深色" if dk else "浅色"}任务栏_32.png"))}" style="width:32px;height:32px"></div>'
                       f'<div class="cap">{zs}{"·深" if dk else "·浅"}</div></div>')
    st_lines = "".join(f"<li>{x}</li>" for x in states)
    c = f"background:{card};border:1px solid {edge};border-radius:22px;padding:22px 24px"
    winpair = "".join(
        f'<div style="text-align:center"><div style="width:250px;height:250px;border-radius:16px;display:flex;align-items:center;'
        f'justify-content:center;background:{b}"><img src="{u(win1024)}" style="width:200px;height:200px"></div>'
        f'<div class="cap">{t}</div></div>' for b, t in (("#f3f3f3", "浅色背景"), ("#202020", "深色背景")))
    return (f'<div style="padding:44px 48px;background:{bg};color:{ink};min-height:100%">'
            f'<div style="font-size:30px;font-weight:700;letter-spacing:-.3px">方向 {key} · {zh}</div>'
            f'<div style="font-size:16px;color:{sub};margin:8px 0 26px;max-width:1100px;line-height:1.6">{words}</div>'
            f'<div style="display:flex;gap:20px;margin-bottom:20px">'
            f'<div style="{c};flex:1"><div class="lab">macOS 应用图标（这台 Mac 自己画出来的样子）</div>'
            f'<img src="{u(sysicon)}" style="width:330px;height:330px;display:block;margin:0 auto"></div>'
            f'<div style="{c};flex:1"><div class="lab">Windows 应用图标（开始菜单、任务栏、安装包）</div>'
            f'<div style="display:flex;justify-content:center;gap:20px;margin-top:30px">{winpair}</div></div></div>'
            f'<div style="display:flex;gap:20px;margin-bottom:20px">'
            f'<div style="{c};flex:1"><div class="lab">菜单栏（单色模板图，系统染色）</div>'
            f'<div style="display:grid;grid-template-columns:repeat(6,76px);gap:10px">{"".join(mbz)}</div>'
            f'<ul style="font-size:13px;color:{sub};margin:14px 0 0;padding-left:18px;line-height:1.7">{st_lines}</ul></div>'
            f'<div style="{c};flex:1"><div class="lab">Windows 托盘（彩色，按任务栏深浅各一版）</div>'
            f'<div style="display:grid;grid-template-columns:repeat(6,76px);gap:10px">{"".join(trz)}</div>'
            f'<div style="font-size:13px;color:{sub};margin-top:14px;line-height:1.7">这里是 32 像素（200% 缩放）。'
            f'别的缩放的真实像素见"Windows任务栏"和"小尺寸逐像素"。</div></div></div>'
            f'<div style="{c}"><div class="lab">Retina 屏上的大小（16、32 点用小尺寸那一版）</div>'
            f'<div style="display:flex;align-items:flex-end;gap:40px">{ret}</div></div>'
            f'</div>')


# --------------------------------------------------- small sizes, by pixel

def smallpx(d):
    S = os.path.join(d, "各尺寸")
    Z = 8

    def img(p, px, bg):
        return (f'<div style="text-align:center"><div style="padding:10px;background:{bg};border-radius:10px">'
                f'<img class="pix" src="{u(p)}" style="width:{px * Z}px;height:{px * Z}px"></div><div class="cap">{px} 像素</div></div>')

    def tm(p, px, bg, col, name):
        return (f'<div style="text-align:center"><div style="padding:10px;background:{bg};border-radius:10px">'
                f'{tmpl(p, px * Z, px * Z, col, True)}</div><div class="cap">{name}</div></div>')

    rows = []
    rows.append(("应用图标：macOS 16、32（浅底），Windows 16、24（深底）、32（浅底）", "".join(
        img(os.path.join(S, "macOS", fn), px, "#e9e9ee") for fn, px in (("icon_16x16.png", 16), ("icon_32x32.png", 32)))
        + "".join(img(os.path.join(S, "Windows", f"{px}.png"), px, bg)
                  for px, bg in ((16, "#202020"), (24, "#202020"), (32, "#f3f3f3")))))
    mb = ""
    for st, zh in STATES:
        mb += tm(os.path.join(S, "菜单栏", zh + "@2x.png"), 18, "#e6e6e9", "rgba(0,0,0,.86)", zh + " @2x")
    for st, zh in STATES:
        mb += tm(os.path.join(S, "菜单栏", zh + "@2x.png"), 18, "#2b2b30", "#fff", zh + " @2x")
    rows.append(("菜单栏（Retina，每格一个像素）", mb))
    for dark in (True, False):
        r = ""
        for st, zh in STATES:
            for px in (16, 24, 32):
                r += img(os.path.join(S, "托盘", f"{zh}_{'深色' if dark else '浅色'}任务栏_{px}.png"), px,
                         "#202020" if dark else "#eef0f3")
        rows.append((f"Windows 托盘 · {'深色' if dark else '浅色'}任务栏（16、24、32：连着、要你处理、连不了）", r))
    body = "".join(f'<div style="margin-bottom:26px"><div class="lab">{t}</div><div style="display:flex;flex-wrap:wrap;'
                   f'gap:12px;align-items:flex-end">{r}</div></div>' for t, r in rows)
    return f'<div style="padding:36px 40px;background:#fff;color:#18181b">{body}</div>'


# ------------------------------------------------------------- comparison

def compare(dirs, outdirs, tmp, dark):
    ink = "#f4f4f5" if dark else "#18181b"
    sub = "#a1a1aa" if dark else "#71717a"
    card = "#1c1c20" if dark else "#ffffff"
    edge = "#2a2a30" if dark else "#e4e4e7"
    bg = "#111113" if dark else "#f4f4f6"
    cols = []
    for key, zh, _ in dirs:
        S = os.path.join(outdirs[key], "各尺寸")
        words = WORDS[key][1]
        mb = "".join(f'<span style="display:inline-flex;width:40px;justify-content:center">'
                     f'{tmpl(os.path.join(S, "菜单栏", z + "@2x.png"), 18, 18, "#fff" if dark else "rgba(0,0,0,.86)")}</span>'
                     for _, z in STATES)
        tr = "".join(f'<img src="{u(os.path.join(S, "托盘", f"{z}_{"深色" if dark else "浅色"}任务栏_32.png"))}" '
                     f'style="width:16px;height:16px;margin:0 12px">' for _, z in STATES)
        cols.append(f'<div style="flex:1;background:{card};border:1px solid {edge};border-radius:24px;padding:26px 26px 22px">'
                    f'<div style="font-size:24px;font-weight:700">{key} · {zh}</div>'
                    f'<div style="font-size:14px;color:{sub};line-height:1.65;height:70px;margin:8px 0 6px">{words}</div>'
                    f'<img src="{u(os.path.join(tmp, key, "sys-512.png"))}" style="width:300px;height:300px;display:block;margin:0 auto 10px">'
                    f'<div style="display:flex;justify-content:center;gap:18px;align-items:flex-end;margin-bottom:18px">'
                    + "".join(f'<img src="{u(os.path.join(S, "macOS", fn))}" style="width:{pt}px;height:{pt}px">'
                              for fn, pt in (("icon_32x32@2x.png", 64), ("icon_32x32.png", 32), ("icon_16x16@2x.png", 16)))
                    + '</div>'
                    f'<div style="font-size:12px;color:{sub};margin-bottom:6px">菜单栏：连着 · 要你处理 · 连不了</div>'
                    f'<div style="height:30px;border-radius:9px;display:flex;align-items:center;padding:0 8px;margin-bottom:12px;'
                    f'background:{"#2b2b30" if dark else "#e6e6e9"}">{mb}</div>'
                    f'<div style="font-size:12px;color:{sub};margin-bottom:6px">Windows 托盘（彩色）</div>'
                    f'<div style="height:30px;border-radius:9px;display:flex;align-items:center;padding:0 8px;'
                    f'background:{"#202020" if dark else "#eef0f3"}">{tr}</div></div>')
    return (f'<div style="padding:44px 48px;background:{bg};color:{ink};min-height:100%">'
            f'<div style="font-size:30px;font-weight:700;letter-spacing:-.3px">Deneb logo · 第 4 批：三个方向</div>'
            f'<div style="font-size:16px;color:{sub};margin:8px 0 26px">2026-11-13 · 都还是 Deneb 这颗星（天津四，竖的光芒比横的长），做法不同。'
            f'挑一个方向，或者说哪里要改；选定前客户端和网页都不动。</div>'
            f'<div style="display:flex;gap:20px">{"".join(cols)}</div></div>')


# ------------------------------------------------------------------ build

def build(out, stamp, dirs, srcs, outdirs, tmp):
    sysd = ensure_sys(tmp)
    sh = Shots(tmp)
    strips = {}
    # Taskbar strips first: their real pixels are what the taskbar shot blows up.
    for key, zh, _ in dirs:
        S = os.path.join(outdirs[key], "各尺寸")
        for dark in (True, False):
            for k in (1.0, 1.25, 1.5, 2.0):
                px = round(16 * k)
                tp = os.path.join(S, "托盘", f"连着_{'深色' if dark else '浅色'}任务栏_{px}.png")
                p = os.path.join(tmp, key, f"strip-{int(k * 100)}-{'d' if dark else 'l'}.png")
                w = round(290 * k)
                sh.add(f"strip-{key}-{k}-{dark}", page(tray_strip(sysd, tp, k, dark, width=w), "transparent", "#000"),
                       w, round(48 * k), p, scale=1)
                strips[(key, dark, k)] = (p, w, round(48 * k))
            tp = os.path.join(S, "托盘", f"连着_{'深色' if dark else '浅色'}任务栏_24.png")
            app = os.path.join(tmp, key, "win-36.png")
            p = os.path.join(tmp, key, f"strip-full-{'d' if dark else 'l'}.png")
            sh.add(f"stripfull-{key}-{dark}", page(tray_strip(sysd, tp, 1.5, dark, app_png=app, width=2240),
                                                   "transparent", "#000"), 2240, 72, p, scale=1)
            strips[(key, dark, "full")] = (p, 2240, 72)
    sh.run()
    for key, zh, _ in dirs:
        d = outdirs[key]
        S = os.path.join(d, "各尺寸")
        pre = os.path.join(d, f"{stamp}_品牌-第4批_{key}-{zh}_")
        sysicon = os.path.join(tmp, key, "sys-512.png")
        for dark in (False, True):
            sfx = "_深色" if dark else ""
            sh.add(f"dock-{key}-{dark}", page(dock(sysd, os.path.join(tmp, key, "sys-256.png"), dark,
                                                    f"macOS 程序坞（{'深色' if dark else '浅色'}）：这台 Mac 自己画出来的图标，第 4 个是 Deneb"),
                                               "#000", "#fff"), 1100, 270, pre + f"程序坞{sfx}.png")
            sh.add(f"menubar-{key}-{dark}", page(menubar(sysd, os.path.join(S, "菜单栏"), dark,
                                                          f"macOS 菜单栏（{'深色' if dark else '浅色'}）：三种状态"),
                                                  "#000", "#fff"), 1180, 320, pre + f"菜单栏{sfx}.png")
            ts = {(dark, k): strips[(key, dark, k)] for k in (1.0, 1.25, 1.5, 2.0, "full")}
            sh.add(f"taskbar-{key}-{dark}", page(taskbar(sysd, key, S, ts, dark,
                                                          f"Windows 11 任务栏（{'深色' if dark else '浅色'}）"),
                                                  "#000", "#fff"), 1200, "auto", pre + f"Windows任务栏{sfx}.png")
            sh.add(f"sheet-{key}-{dark}", page(sheet(key, zh, WORDS[key][1], WORDS[key][2], d, sysicon,
                                                      os.path.join(tmp, key, "win-1024.png"), dark), "#000", "#fff"),
                   1500, "auto", pre + f"总览{sfx}.png")
        sh.add(f"small-{key}", page(smallpx(d), "#fff", "#000"), 1500, "auto", pre + "小尺寸逐像素.png", scale=1)
        # The big images on their own.
        import shutil
        shutil.copy(os.path.join(S, "macOS", "icon_512x512@2x.png"), pre + "应用图标-macOS-1024.png")
        shutil.copy(os.path.join(tmp, key, "win-1024.png"), pre + "应用图标-Windows-1024.png")
    for dark in (False, True):
        sh.add(f"compare-{dark}", page(compare(dirs, outdirs, tmp, dark), "#000", "#fff"), 1500, "auto",
               os.path.join(out, f"{stamp}_品牌-第4批_三个方向对比{'_深色' if dark else ''}.png"))
    sh.run()
