#!/bin/sh
# Builds the Web version for Linux into deploy/web/: deneb-web, one static
# file (pure Go, CGO off), into bin/<arch>/, and the browser app into app/.
# The app is built from web/app/src in a scratch copy, so the image always
# has the source as it is and nothing in the repository changes (web/app/dist
# is the Web line's).
#
#   deploy/web/build.sh            # amd64 and arm64
#   ARCHES=amd64 deploy/web/build.sh
set -eu
cd "$(dirname "$0")/../.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/web/app"
cp -R web/app/src web/app/build.mjs web/app/index.html "$tmp/web/app/"
cp web/tokens.json web/tokens.css "$tmp/web/"
node "$tmp/web/app/build.mjs"
rm -rf deploy/web/app
cp -R "$tmp/web/app/dist" deploy/web/app
echo "deploy/web/app: $(find deploy/web/app -type f | wc -l | tr -d ' ') files"
for arch in ${ARCHES:-amd64 arm64}; do
	mkdir -p "deploy/web/bin/$arch"
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags='-s -w' \
		-o "deploy/web/bin/$arch/deneb-web" ./cmd/deneb-web
	echo "deploy/web/bin/$arch/deneb-web"
done
