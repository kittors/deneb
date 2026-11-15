#!/usr/bin/env python3
"""The website's pictures (web/site/img) from the real window's stills.

design/tools/site-shots.sh writes the stills: the desktop window at twice
its size, light and dark, from its tests. This crops each picture the site
and the docs use (PICTURES below), writes img/light-<name>.webp and
img/dark-<name>.webp, the film's cover as img/film-poster.webp, and
img/sizes.json, the pictures' sizes for the pages (web/site/build.mjs).

The home page's close-ups come from stills four times as sharp
(close/, TestSiteCloseups), made 1960 wide at most: twice their 980 on the
page; the share's is dark only, as its block is. The docs' pictures show a
part of the window at its real size, up to 688 wide (344 on the page); the
steps' are 6:5, as their boxes. Names after the folders make only those,
and keep the rest of img/sizes.json.

usage: python3 -I design/tools/site-images.py <stills> [web/site/img [name...]]
"""
import json
import os
import sys

from PIL import Image

# name: (still, box in the still's pixels or None for all of it[, at most
# this wide[, the themes]])
CLOSE = 4  # how much sharper close/'s stills are than the window
PICTURES = {
    # The home page.
    # The loop's first frame (site-hero.sh), the picture under the loop.
    'window': ('hero/{t}/0015.png', None),
    # The close-ups, in the window's points times CLOSE.
    'lines-close': ('close/{t}_lines.png', tuple(v * CLOSE for v in (275, 290, 877, 620)), 1960),
    'share-close': ('close/{t}_share.png', tuple(v * CLOSE for v in (872, 0, 1180, 91)), 1960, ('dark',)),
    'services-close': ('close/{t}_services.png', tuple(v * CLOSE for v in (250, 91, 852, 382)), 1960),
    'own-close': ('close/{t}_own.png', tuple(v * CLOSE for v in (460, 300, 1062, 620)), 1960, ('dark',)),
    'relay-line': ('site/{t}_窗口.png', (550, 580, 1754, 1240)),
    'step-drag': ('site/{t}_拖出来.png', (960, 594, 2000, 1460)),
    'step-code': ('m6/{t}-signin_验证码.png', (790, 270, 1570, 920)),
    'share': ('m3/{t}-share-drag_指到Mia.png', (1156, 0, 2360, 640)),
    'services': ('site/{t}_服务.png', (500, 183, 1704, 765)),
    'own-relay': ('close/{t}_own.png', tuple(v * CLOSE for v in (460, 300, 1062, 620)), 1204),
    # The docs (web/site/docs/*.md), shown at their real size.
    'install-first': ('m6/{t}-new-lan_第一个局域网.png', (760, 470, 1600, 1100)),
    'add-steps': ('mydev/{t}-add-one_三步.png', (685, 445, 1675, 1030)),
    'add-joined': ('mydev/{t}-add-one_已加入.png', (690, 248, 1678, 1234)),
    'lan-drag': ('m6/{t}-new-lan_拖进一台.png', (900, 560, 1900, 1460)),
    'lan-invite': ('site/{t}_邀请朋友.png', (1672, 0, 2360, 240)),
    'lan-card': ('m3/{t}-share-mode_局域网卡片.png', (1700, 160, 2340, 940)),
    'share-card': ('m3/{t}-share-drag_开放给Mia的.png', (1700, 160, 2340, 690)),
    'services-add': ('mydev/{t}-services_添加服务.png', (500, 190, 1560, 690)),
    'services-open': ('site/{t}_别人的设备.png', (1700, 160, 2340, 780)),
    'relays-plus': ('m4/{t}-own-server_加号.png', (1040, 1260, 1780, 1470)),
    'account-card': ('m7/{t}-account_账号卡片.png', (16, 1080, 656, 1380)),
    'account-delete-device': ('m7/{t}-delete-device_删除确认.png', (751, 511, 1604, 987)),
    'account-delete': ('m7/{t}-delete-account_会删掉的.png', (740, 140, 1620, 880)),
    'faq-proxy': ('m6/{t}-proxies_Surge卡片.png', (1540, 40, 2340, 600)),
    'faq-diag': ('m6/{t}-diagnostics_更多.png', (1700, 1060, 2340, 1420)),
}


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    stills = sys.argv[1]
    out = sys.argv[2] if len(sys.argv) > 2 else os.path.join(os.path.dirname(__file__), '..', '..', 'web', 'site', 'img')
    only = set(sys.argv[3:])
    sizes_path = os.path.join(out, 'sizes.json')
    sizes = json.load(open(sizes_path)) if only and os.path.exists(sizes_path) else {}
    for name, (src, box, *rest) in PICTURES.items():
        if only and name not in only:
            continue
        wide = rest[0] if rest else None
        themes = rest[1] if len(rest) > 1 else ('light', 'dark')
        for t in themes:
            im = Image.open(os.path.join(stills, src.format(t=t))).convert('RGB')
            if box:
                im = im.crop(box)
            if wide and im.width > wide:
                im = im.resize((wide, round(im.height * wide / im.width)), Image.LANCZOS)
            im.save(os.path.join(out, f'{t}-{name}.webp'), 'WEBP', quality=88 if wide else 90, method=6)
            sizes[name] = list(im.size)
        print(f'{name}: {sizes[name][0]}x{sizes[name][1]}')
    poster = os.path.join(stills, 'film', 'poster.png')
    if os.path.exists(poster):
        im = Image.open(poster).convert('RGB')
        im.save(os.path.join(out, 'film-poster.webp'), 'WEBP', quality=88, method=6)
        sizes['film-poster'] = list(im.size)
    elif os.path.exists(os.path.join(out, 'film-poster.webp')):
        sizes['film-poster'] = list(Image.open(os.path.join(out, 'film-poster.webp')).size)
    with open(os.path.join(out, 'sizes.json'), 'w') as f:
        f.write('{\n' + ',\n'.join(f' {json.dumps(k)}: [{w}, {h}]' for k, (w, h) in sorted(sizes.items())) + '\n}\n')


if __name__ == '__main__':
    main()
