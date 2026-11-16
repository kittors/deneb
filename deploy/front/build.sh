#!/bin/sh
# Builds deneb-front for Linux into deploy/front/bin/<arch>/: one static
# file (pure Go, CGO off), which the scratch image takes as it is.
#
#   deploy/front/build.sh            # amd64
#   ARCHES="amd64 arm64" deploy/front/build.sh
set -eu
cd "$(dirname "$0")/../.."
for arch in ${ARCHES:-amd64}; do
	mkdir -p "deploy/front/bin/$arch"
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags='-s -w' \
		-o "deploy/front/bin/$arch/deneb-front" ./cmd/deneb-front
	echo "deploy/front/bin/$arch/deneb-front"
done
