#!/bin/sh
# Home page thumbnails (design/demos/index.html) from the 2x stills that
# record.mjs leaves in $TMPDIR/deneb-rec/<scene>-<theme>/.
#
# Each still is the 1440x900 viewport at 2x; the window (1180x740 zoomed to
# fit) sits at x 183..2697, y 56..1632. Cropped to that, scaled to 960 wide,
# saved as thumbs/0N.jpg (light) and thumbs/0N-dark.jpg.
#
# usage: sh design/tools/thumbs.sh
set -e
cd "$(dirname "$0")/../demos"
REC="${TMPDIR:-/tmp}/deneb-rec"
mkdir -p thumbs
one() { # index scene still-label
  for theme in light dark; do
    src="$REC/$2-$theme/$2_${theme}_$3.png"
    [ -f "$src" ] || { echo "missing $src" >&2; continue; }
    case $theme in dark) out="thumbs/0$1-dark.jpg" ;; *) out="thumbs/0$1.jpg" ;; esac
    sips --cropOffset 56 183 -c 1576 2514 "$src" --out /tmp/deneb-thumb.png >/dev/null
    sips -Z 960 -s format jpeg -s formatOptions 82 /tmp/deneb-thumb.png --out "$out" >/dev/null
    echo "$out"
  done
}
one 0 sample 主界面
one 1 01-direct-relay 直接连上
one 2 02-drag-relay 接通
one 3 03-multi-network 加入桌游夜
one 4 04-device-share 出现在小林的面板
one 5 05-lan-members Leo加入
rm -f /tmp/deneb-thumb.png
