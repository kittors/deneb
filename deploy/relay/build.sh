#!/bin/sh
# Builds deneb-relay for Linux into deploy/relay/bin/<arch>/: one static
# file (pure Go, CGO off), which the scratch image takes as it is.
#
#   deploy/relay/build.sh            # amd64 and arm64
#   ARCHES=arm64 deploy/relay/build.sh
set -eu
cd "$(dirname "$0")/../.."
for arch in ${ARCHES:-amd64 arm64}; do
	mkdir -p "deploy/relay/bin/$arch"
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags='-s -w' \
		-o "deploy/relay/bin/$arch/deneb-relay" ./cmd/deneb-relay
	echo "deploy/relay/bin/$arch/deneb-relay"
done
