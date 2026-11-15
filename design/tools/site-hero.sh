#!/bin/sh
# The home page's moving window (DESIGN.md §14.16): from the frames
# TestSiteHeroLoop writes (cmd/deneb/site_shots_test.go), a 6 s loop each for
# light and dark, as WebM (VP9) and MP4 (H.264), silent, into
# web/site/media/. The light runs on cycles of its own, so the loop's last
# half second fades into its first. A heavy job: take the lock first.
#
#   DENEB_SITE_HERO=/tmp/deneb-site-hero go test -p 1 -run TestSiteHeroLoop ./cmd/deneb
#   design/tools/site-hero.sh /tmp/deneb-site-hero
set -eu
cd "$(dirname "$0")/../.."
in=${1:-/tmp/deneb-site-hero}
out=web/site/media
mkdir -p "$out"
loop='[0:v]split[a][b];[a]trim=start_frame=15:end_frame=195,setpts=PTS-STARTPTS[main];[b]trim=start_frame=0:end_frame=15,setpts=PTS-STARTPTS[head];[main][head]xfade=transition=fade:duration=0.5:offset=5.5,format=yuv420p'
for t in light dark; do
	ffmpeg -y -loglevel error -threads 2 -framerate 30 -i "$in/$t/%04d.png" -filter_complex "$loop" \
		-c:v libvpx-vp9 -b:v 0 -crf 36 -row-mt 1 -deadline good -cpu-used 2 -an "$out/hero-$t.webm"
	ffmpeg -y -loglevel error -threads 2 -framerate 30 -i "$in/$t/%04d.png" -filter_complex "$loop" \
		-c:v libx264 -preset slow -crf 26 -tune animation -movflags +faststart -an "$out/hero-$t.mp4"
done
ls -l "$out"/hero-*
