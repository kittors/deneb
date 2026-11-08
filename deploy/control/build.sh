#!/bin/sh
# Builds deneb-control for Linux into deploy/control/bin/<arch>/: one
# static file (pure Go, CGO off), which the scratch image takes as it is.
#
#   deploy/control/build.sh            # amd64 and arm64
#   ARCHES=arm64 deploy/control/build.sh
set -eu
cd "$(dirname "$0")/../.."
for arch in ${ARCHES:-amd64 arm64}; do
	mkdir -p "deploy/control/bin/$arch"
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags='-s -w' \
		-o "deploy/control/bin/$arch/deneb-control" ./cmd/deneb-control
	echo "deploy/control/bin/$arch/deneb-control"
done
