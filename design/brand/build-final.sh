#!/bin/sh
# build-final.sh: writes final/ (final.py) and renders it: every size drawn
# from the vector at that size by headless Chrome (ImageMagick's own SVG
# renderer gets gradients wrong), the 16-32 px ones from the small mark.
# Then the .icns (iconutil), the .ico (ImageMagick packs the PNGs), and the
# menu bar images, copied from cmd/deneb/trayicons (DENEB_TRAY_ICONS=1 go
# test -run TestTrayIcons ./cmd/deneb draws them from the client's glyphs).
set -eu
cd "$(dirname "$0")"
python3 -I final.py >/dev/null
F=$PWD/final
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
PROFILE=${TMPDIR:-/tmp}/deneb-logo-chrome
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# px <svg> <n> <png>: the SVG drawn at n x n pixels (its root's width and
# height set to n; the viewBox keeps the drawing).
cat >"$TMP/size.py" <<'EOF'
import re, sys
src, n, dst = sys.argv[1:4]
s = open(src).read()
s = re.sub(r'<svg([^>]*?) width="[0-9.]+" height="[0-9.]+"',
           lambda m: '<svg%s width="%s" height="%s"' % (m.group(1), n, n), s, count=1)
open(dst, "w").write(s)
EOF
px() {
	python3 -I "$TMP/size.py" "$1" "$2" "$TMP/px.svg"
	"$CHROME" --headless=new --disable-gpu --hide-scrollbars --no-first-run \
		--user-data-dir="$PROFILE" --force-device-scale-factor=1 --default-background-color=00000000 \
		--window-size="$2,$2" --screenshot="$3" "file://$TMP/px.svg" >/dev/null 2>&1
}
# at <scale> <svg> <png>: the SVG at its own size, times scale.
at() {
	set -- "$1" "$2" "$3" $(python3 -I -c 'import re, sys
s = open(sys.argv[1]).read(400)
print(*(round(float(re.search(k + r"=\"([0-9.]+)\"", s).group(1))) for k in ("width", "height")))' "$2")
	"$CHROME" --headless=new --disable-gpu --hide-scrollbars --no-first-run \
		--user-data-dir="$PROFILE" --force-device-scale-factor="$1" --default-background-color=00000000 \
		--window-size="$4,$5" --screenshot="$3" "file://$2" >/dev/null 2>&1
}
# icon <dir-svg-stem> <n> <png>: the small mark up to 48 px, the full one above.
icon() {
	if [ "$2" -le 48 ]; then px "$1-small.svg" "$2" "$3"; else px "$1.svg" "$2" "$3"; fi
}

# macOS: the .iconset, then the .icns. 32 px stays small (16@2x and 32@1x).
set_dir=$F/macos/AppIcon.iconset
rm -rf "$set_dir" && mkdir -p "$set_dir"
for spec in 16:16x16 32:16x16@2x 32:32x32 64:32x32@2x 128:128x128 256:128x128@2x 256:256x256 512:256x256@2x 512:512x512 1024:512x512@2x; do
	n=${spec%%:*}
	if [ "$n" -le 32 ]; then px "$F/macos/app-icon-small.svg" "$n" "$set_dir/icon_${spec#*:}.png"; else px "$F/macos/app-icon.svg" "$n" "$set_dir/icon_${spec#*:}.png"; fi
done
iconutil -c icns "$set_dir" -o "$F/macos/Deneb.icns"
cp "$set_dir/icon_512x512@2x.png" "$F/macos/icon-1024.png"

# Windows: 16 24 32 48 64 256 in one .ico.
rm -f "$F"/windows/*.png
for n in 16 24 32 48 64 256 1024; do icon "$F/windows/tile" "$n" "$F/windows/deneb-$n.png"; done
magick "$F/windows/deneb-16.png" "$F/windows/deneb-24.png" "$F/windows/deneb-32.png" "$F/windows/deneb-48.png" \
	"$F/windows/deneb-64.png" "$F/windows/deneb-256.png" "$F/windows/Deneb.ico"

# Linux: the hicolor sizes, 16 to 512.
mkdir -p "$F/linux" && rm -f "$F"/linux/*.png
for n in 16 22 24 32 48 64 128 256 512; do icon "$F/windows/tile" "$n" "$F/linux/deneb-$n.png"; done

# Web.
px "$F/web/favicon.svg" 16 "$F/web/favicon-16.png"
px "$F/web/favicon.svg" 32 "$F/web/favicon-32.png"
px "$F/web/apple-touch-icon.svg" 180 "$F/web/apple-touch-icon.png"

# The lockups at 2x, for documents.
at 2 "$F/lockup/deneb-lockup.svg" "$F/lockup/deneb-lockup@2x.png"
at 2 "$F/lockup/deneb-lockup-dark.svg" "$F/lockup/deneb-lockup-dark@2x.png"

# The menu bar, as the client ships it.
mkdir -p "$F/tray"
cp ../../cmd/deneb/trayicons/solid.png ../../cmd/deneb/trayicons/dot.png ../../cmd/deneb/trayicons/hollow.png "$F/tray/"
echo "$F"
