#!/bin/sh
# The real window's stills for the website and its docs (DESIGN.md §14.16),
# twice as sharp, light and dark, from the desktop's tests: the site's own
# (cmd/deneb/site_shots_test.go, site_close_test.go: the home page's
# close-ups, four times as sharp) and the scenes of M3, M4, M6, M7 and My
# Devices the docs show, and the frames of the home page's loops.
# design/tools/site-images.py then crops them into web/site/img;
# design/tools/site-hero.sh <folder>/hero makes the first screen's loop, and
# design/tools/site-loop.py <folder>/drag drag 250,0,1180,740 the drag's. A
# heavy job: take the lock first (AGENTS.md §3).
#
#   design/tools/site-shots.sh [folder]      # default /tmp/deneb-site-stills
set -eu
cd "$(dirname "$0")/../.."
out=${1:-/tmp/deneb-site-stills}
rm -rf "$out"
mkdir -p "$out"
DENEB_SITE=$out/site DENEB_SITE_HERO=$out/hero DENEB_SITE_CLOSE=$out/close DENEB_SITE_DRAG=$out/drag \
DENEB_M3=$out/m3 DENEB_M3_ONLY=share-drag,share-card,seats,share-mode \
DENEB_M4=$out/m4 DENEB_M4_ONLY=own-server,public-card \
DENEB_M6=$out/m6 DENEB_M6_ONLY=signin,new-lan,join,diagnostics,proxies \
DENEB_M7=$out/m7 DENEB_M7_ONLY=account,delete-device,delete-account \
DENEB_MYDEV=$out/mydev DENEB_MYDEV_ONLY=services,add-one,lans \
	go test -p 1 -count=1 -timeout 60m -run 'TestSiteShots|TestSiteHeroLoop|TestSiteCloseups|TestSiteDragLoop|TestM3Frames|TestM4Frames|TestM6Frames|TestM7Frames|TestMyDevFrames' ./cmd/deneb
# The scenes' frames are for their own videos, not the site's; the home
# page's loops keep theirs (design/tools/site-hero.sh, site-loop.py).
find "$out" -name '*.jpg' -delete
find "$out" -name '*.png' -not -path "$out/hero/*" -not -path "$out/drag/*" | sed "s#$out/##" | sort
