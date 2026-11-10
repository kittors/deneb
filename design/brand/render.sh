#!/bin/sh
# render.sh <out-dir> [file.svg|file.html W H scale]...
#
# Renders SVG or HTML files to PNG with headless Chrome (ImageMagick's own
# SVG renderer gets gradients and filters wrong). With no files after the
# out-dir it regenerates the SVGs and renders:
#   <out-dir>/sheet-<kind>.png   everything about one direction (2x)
#   <out-dir>/small-<kind>.png   the small sizes, each pixel blown up 4x
#   <out-dir>/compare.png        the three directions side by side (2x)
#   <out-dir>/png/<kind>/*.png   every SVG on its own (icons 1x, the rest 2x)
# Chrome runs on a profile of its own, so the user's Chrome is not touched.
set -eu
cd "$(dirname "$0")"
OUT=${1:?usage: render.sh <out-dir> [file W H scale]...}
shift
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
PROFILE=${TMPDIR:-/tmp}/deneb-logo-chrome

shot() { # shot <file> <w> <h> <scale> <png>
	"$CHROME" --headless=new --disable-gpu --hide-scrollbars --no-first-run \
		--user-data-dir="$PROFILE" --allow-file-access-from-files \
		--force-device-scale-factor="$4" --default-background-color=00000000 \
		--window-size="$2,$3" --screenshot="$5" "file://$1" >/dev/null 2>&1
}
size() { # the width and height on an SVG's root element
	python3 -I -c 'import re, sys
s = open(sys.argv[1]).read(400)
print(*(round(float(re.search(k + r"=\"([0-9.]+)\"", s).group(1))) for k in ("width", "height")))' "$1"
}

if [ $# -gt 0 ]; then
	while [ $# -ge 4 ]; do
		f=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
		shot "$f" "$2" "$3" "$4" "$OUT/$(basename "${1%.*}").png"
		shift 4
	done
	exit 0
fi

python3 -I gen.py >/dev/null
python3 -I sheet.py "$OUT/sheets"
for k in a-star b-two-lans c-d; do
	shot "$OUT/sheets/sheet-$k.html" 1170 1000 2 "$OUT/sheet-$k.png"
	shot "$OUT/sheets/small-$k.html" 560 118 1 "$OUT/small-$k-1x.png"
	magick "$OUT/small-$k-1x.png" -filter point -resize 400% "$OUT/small-$k.png"
	rm -f "$OUT/small-$k-1x.png"
	mkdir -p "$OUT/png/$k"
	for f in "batch1/$k"/*.svg; do
		set -- $(size "$f")
		case $f in */app-icon*.svg | */mark-on-*.svg) s=1 ;; *) s=2 ;; esac
		shot "$PWD/$f" "$1" "$2" "$s" "$OUT/png/$k/$(basename "${f%.svg}").png"
	done
done
shot "$OUT/sheets/compare.html" 1480 760 2 "$OUT/compare.png"
echo "$OUT"
