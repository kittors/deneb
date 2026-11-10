#!/usr/bin/env python3
"""Review sheets for the logo directions (HTML, rendered by render.sh).

    python3 -I design/brand/sheet.py <work-dir>

writes <work-dir>/sheet-<kind>.html (everything about one direction) and
<work-dir>/compare.html (the three side by side). Small sizes are drawn at
their real pixel size; render.sh renders at 1x so they are what a screen
shows.
"""
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
KINDS = [("a-star", "一颗亮星"), ("b-two-lans", "两个局域网"), ("c-d", "D 字")]

CSS = """
body { margin: 0; font-family: -apple-system, 'PingFang SC', sans-serif; color: #18181b; background: #f4f4f6; }
.page { padding: 40px 48px; }
h1 { font-size: 26px; margin: 0 0 6px; } p.sub { margin: 0 0 28px; color: #71717a; font-size: 15px; }
.row { display: flex; gap: 22px; align-items: stretch; margin-bottom: 22px; }
.card { background: #fff; border: 1px solid #e4e4e7; border-radius: 20px; padding: 20px; box-shadow: 0 6px 18px rgba(0,0,0,.05); }
.card.dark { background: #18181b; border-color: #27272a; color: #e4e4e7; }
.lab { font-size: 13px; color: #71717a; margin-bottom: 12px; } .dark .lab { color: #a1a1aa; }
.sizes { display: flex; align-items: flex-end; gap: 18px; }
.sizes div { text-align: center; font-size: 11px; color: #a1a1aa; }
.sizes img { display: block; margin: 0 auto 6px; }
.menubar { display: flex; align-items: center; gap: 16px; height: 30px; padding: 0 14px; border-radius: 8px; background: #ececef; }
.menubar.dark { background: #2c2c2e; }
.menubar img { width: 18px; height: 18px; }
.menubar.dark img { filter: invert(1); }
.lock img { display: block; }
"""


def sheet(kind, name):
    d = f"file://{HERE}/batch1/{kind}"
    return f"""<!doctype html><meta charset="utf-8"><style>{CSS}</style>
<div class="page">
<h1>方向：{name}</h1><p class="sub">Deneb logo 第 1 批 · 应用图标、标志、小尺寸、菜单栏、横排组合</p>
<div class="row">
  <div class="card"><div class="lab">应用图标（macOS）</div><img src="{d}/app-icon.svg" width="300" height="300"></div>
  <div class="card"><div class="lab">白底</div><img src="{d}/mark-blue.svg" width="300" height="300"></div>
  <div class="card dark"><div class="lab">深底</div><img src="{d}/mark-white.svg" width="300" height="300"></div>
</div>
<div class="row">
  <div class="card"><div class="lab">应用图标：64、32、16 像素（实际大小）</div><div class="sizes">
    <div><img src="{d}/app-icon.svg" width="64" height="64">64</div>
    <div><img src="{d}/app-icon-small.svg" width="32" height="32">32</div>
    <div><img src="{d}/app-icon-small.svg" width="16" height="16">16</div></div></div>
  <div class="card"><div class="lab">网页小图标：白底</div><div class="sizes">
    <div><img src="{d}/mark-blue.svg" width="64" height="64">64</div>
    <div><img src="{d}/mark-blue-small.svg" width="32" height="32">32</div>
    <div><img src="{d}/mark-blue-small.svg" width="16" height="16">16</div></div></div>
  <div class="card dark"><div class="lab">网页小图标：深底</div><div class="sizes">
    <div><img src="{d}/mark-white.svg" width="64" height="64">64</div>
    <div><img src="{d}/mark-white-small.svg" width="32" height="32">32</div>
    <div><img src="{d}/mark-white-small.svg" width="16" height="16">16</div></div></div>
  <div class="card"><div class="lab">菜单栏：连着、要你处理、连不了</div>
    <div class="menubar"><img src="{d}/tray-solid.svg"><img src="{d}/tray-dot.svg"><img src="{d}/tray-hollow.svg"></div>
    <div style="height:10px"></div>
    <div class="menubar dark"><img src="{d}/tray-solid.svg"><img src="{d}/tray-dot.svg"><img src="{d}/tray-hollow.svg"></div></div>
</div>
<div class="row">
  <div class="card lock"><div class="lab">横排组合</div><img src="{d}/lockup.svg" height="150"></div>
  <div class="card dark lock"><div class="lab">横排组合：深底</div><img src="{d}/lockup-dark.svg" height="150"></div>
</div>
</div>"""


def small(kind, name):
    """Everything small at its real pixel size, to be blown up 4x with
    nearest-neighbour so that each screen pixel shows as a block."""
    d = f"file://{HERE}/batch1/{kind}"
    cell = "display:flex;align-items:flex-end;gap:10px;padding:8px;border-radius:6px"
    return f"""<!doctype html><meta charset="utf-8"><style>
body {{ margin: 0; background: #f4f4f6; font-family: -apple-system, 'PingFang SC', sans-serif; }}
.w {{ display: flex; gap: 10px; padding: 8px; align-items: flex-end; }}
.t {{ font-size: 9px; color: #71717a; margin: 6px 8px 0; }}
img {{ display: block; }}
</style>
<div class="t">{name}　应用图标 64 32 16　网页小图标 32 16（白底、深底）　菜单栏：连着、要你处理、连不了</div>
<div class="w">
  <div style="{cell}"><img src="{d}/app-icon.svg" width="64"><img src="{d}/app-icon-small.svg" width="32"><img src="{d}/app-icon-small.svg" width="16"></div>
  <div style="{cell};background:#fff"><img src="{d}/mark-blue-small.svg" width="32"><img src="{d}/mark-blue-small.svg" width="16"></div>
  <div style="{cell};background:#18181b"><img src="{d}/mark-white-small.svg" width="32"><img src="{d}/mark-white-small.svg" width="16"></div>
  <div style="{cell};background:#ececef"><img src="{d}/tray-solid.svg" width="18"><img src="{d}/tray-dot.svg" width="18"><img src="{d}/tray-hollow.svg" width="18"></div>
  <div style="{cell};background:#2c2c2e"><span style="display:flex;gap:10px;filter:invert(1)"><img src="{d}/tray-solid.svg" width="18"><img src="{d}/tray-dot.svg" width="18"><img src="{d}/tray-hollow.svg" width="18"></span></div>
</div>"""


def compare():
    """The three side by side: cards of one width, and the lockups at one
    scale, so that the wordmark is the same size in each."""
    import re
    sizes = {}
    for kind, _ in KINDS:
        head = open(os.path.join(HERE, "batch1", kind, "lockup.svg"), encoding="utf-8").read(300)
        sizes[kind] = tuple(float(re.search(k + r'="([0-9.]+)"', head).group(1)) for k in ("width", "height"))
    k = 400 / max(w for w, _ in sizes.values())
    box = max(h for _, h in sizes.values()) * k
    cells = []
    for kind, name in KINDS:
        d = f"file://{HERE}/batch1/{kind}"
        w, h = sizes[kind]
        cells.append(f"""<div class="card" style="flex:1 1 0;min-width:0"><div class="lab">{name}</div>
<img src="{d}/app-icon.svg" width="260" height="260" style="display:block;margin:0 auto 14px">
<div style="height:{box:.0f}px;display:flex;align-items:center;justify-content:center"><img src="{d}/lockup.svg" width="{w * k:.0f}" height="{h * k:.0f}"></div></div>""")
    return f"""<!doctype html><meta charset="utf-8"><style>{CSS}</style>
<div class="page"><h1>Deneb logo · 第 1 批 · 三个方向</h1><p class="sub">挑一个，或者说哪里要改</p>
<div class="row">{''.join(cells)}</div></div>"""


def main():
    out = sys.argv[1]
    os.makedirs(out, exist_ok=True)
    for kind, name in KINDS:
        with open(os.path.join(out, f"sheet-{kind}.html"), "w", encoding="utf-8") as fh:
            fh.write(sheet(kind, name))
        with open(os.path.join(out, f"small-{kind}.html"), "w", encoding="utf-8") as fh:
            fh.write(small(kind, name))
    with open(os.path.join(out, "compare.html"), "w", encoding="utf-8") as fh:
        fh.write(compare())


if __name__ == "__main__":
    main()
