#!/bin/sh
# Builds the probe's site in /tmp/dnbweb/site (outside the repo): MyGo's
# counter, the smallest Deneb web app (web/probe/size) and the whole desktop
# client in demo mode, all with the flags of `mygo build -platform web`,
# each with brotli and gzip twins as a CDN would serve them.
# The desktop client needs one file of the engine's local API swapped for
# js/wasm; a build overlay does it, so the repo stays as it is.
set -e
cd "$(dirname "$0")/../.."
ROOT=$PWD
SITE=/tmp/dnbweb/site
FLAGS="-tags mygo_noinspector -trimpath"
LD="-s -w -X github.com/egoist/mygo.production=1"
mkdir -p $SITE/deneb $SITE/counter $SITE/size
rm -f $SITE/wasm_exec.js && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/probe/probe.js $SITE/
printf '{"Replace":{"%s/engine/localapi/localclient/detach_unix.go":"%s/web/probe/overlay/detach_unix.go.txt"}}' "$ROOT" "$ROOT" > /tmp/dnbweb/overlay.json
GOOS=js GOARCH=wasm go build -overlay /tmp/dnbweb/overlay.json $FLAGS -ldflags="$LD" -o $SITE/deneb/app.wasm ./cmd/deneb
GOOS=js GOARCH=wasm go build $FLAGS -ldflags="$LD" -o $SITE/counter/app.wasm github.com/egoist/mygo/examples/counter-native
GOOS=js GOARCH=wasm go build $FLAGS -ldflags="$LD" -o $SITE/size/app.wasm ./web/probe/size
for d in deneb counter size; do
	cp web/probe/page.html $SITE/$d/index.html
	gzip -9 -k -f $SITE/$d/app.wasm
	brotli -q 11 -k -f $SITE/$d/app.wasm
	ls -l $SITE/$d/app.wasm $SITE/$d/app.wasm.gz $SITE/$d/app.wasm.br | awk '{print $5, $9}'
done
