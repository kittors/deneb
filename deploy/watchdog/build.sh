#!/bin/sh
# Builds deneb-watchdog for Linux into deploy/watchdog/bin/<arch>/: one
# static file (pure Go, CGO off), which the scratch image takes as it is.
#
#   deploy/watchdog/build.sh            # amd64
#   ARCHES="amd64 arm64" deploy/watchdog/build.sh
set -eu
cd "$(dirname "$0")/../.."
for arch in ${ARCHES:-amd64}; do
	mkdir -p "deploy/watchdog/bin/$arch"
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags='-s -w' \
		-o "deploy/watchdog/bin/$arch/deneb-watchdog" ./cmd/deneb-watchdog
	echo "deploy/watchdog/bin/$arch/deneb-watchdog"
done
